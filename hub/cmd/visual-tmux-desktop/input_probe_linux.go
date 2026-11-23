//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0 x11
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <webkit/webkit.h>
#include <X11/keysym.h>

// XTest is optional verification tooling, dynamically loaded only on the
// explicitly private X11 display. It is not a desktop runtime dependency.
typedef int (*VTCMotion)(Display *, int, int, int, unsigned long);
typedef int (*VTCButton)(Display *, unsigned int, int, unsigned long);
typedef int (*VTCKey)(Display *, unsigned int, int, unsigned long);

static GtkWidget *vtcInputWebView(GtkWidget *widget) {
    if (WEBKIT_IS_WEB_VIEW(widget)) return widget;
    for (GtkWidget *child=gtk_widget_get_first_child(widget); child; child=gtk_widget_get_next_sibling(child)) {
        GtkWidget *view=vtcInputWebView(child);
        if (view) return view;
    }
    return NULL;
}

static int vtcPrivatePointer(void *window, double x, double y, double endx, double endy, int drag) {
    if (g_strcmp0(g_getenv("VTC_PRIVATE_DISPLAY"), "1")) return 0;
    GtkWidget *root=GTK_WIDGET(window), *view=vtcInputWebView(root);
    if (!view || !GDK_IS_X11_DISPLAY(gtk_widget_get_display(root))) return 0;
    void *library=dlopen("libXtst.so.6", RTLD_NOW|RTLD_LOCAL);
    if (!library) return 0;
    VTCMotion motion=(VTCMotion)dlsym(library,"XTestFakeMotionEvent");
    VTCButton button=(VTCButton)dlsym(library,"XTestFakeButtonEvent");
    if (!motion || !button) { dlclose(library); return 0; }
    GdkSurface *surface=gtk_native_get_surface(GTK_NATIVE(root));
    Display *display=gdk_x11_display_get_xdisplay(gtk_widget_get_display(root));
    graphene_point_t from={x,y},to;
    if (!gtk_widget_compute_point(view,root,&from,&to)) { dlclose(library); return 0; }
    int px,py; Window child;
    int scale=gdk_surface_get_scale_factor(surface);
    XTranslateCoordinates(display,gdk_x11_surface_get_xid(surface),DefaultRootWindow(display),
        (int)(to.x*scale),(int)(to.y*scale),&px,&py,&child);
    motion(display,-1,px,py,CurrentTime);
    button(display,1,True,CurrentTime);
    if (drag) {
        from.x=endx; from.y=endy;
        if (!gtk_widget_compute_point(view,root,&from,&to)) {
            button(display,1,False,CurrentTime); XFlush(display); dlclose(library); return 0;
        }
        XTranslateCoordinates(display,gdk_x11_surface_get_xid(surface),DefaultRootWindow(display),
            (int)(to.x*scale),(int)(to.y*scale),&px,&py,&child);
        motion(display,-1,px,py,CurrentTime);
    }
    button(display,1,False,CurrentTime);
    XFlush(display);
    dlclose(library);
    return 1;
}

static int vtcPrivateKey(void *window, int key) {
    if (g_strcmp0(g_getenv("VTC_PRIVATE_DISPLAY"), "1")) return 0;
    GtkWidget *root=GTK_WIDGET(window);
    if (!GDK_IS_X11_DISPLAY(gtk_widget_get_display(root))) return 0;
    void *library=dlopen("libXtst.so.6",RTLD_NOW|RTLD_LOCAL);
    if (!library) return 0;
    VTCKey send=(VTCKey)dlsym(library,"XTestFakeKeyEvent");
    if (!send) {dlclose(library);return 0;}
    Display *display=gdk_x11_display_get_xdisplay(gtk_widget_get_display(root));
    unsigned int control=XKeysymToKeycode(display,XK_Control_L);
    unsigned int shift=XKeysymToKeycode(display,XK_Shift_L);
    unsigned int letter=XKeysymToKeycode(display,key==1 ? XK_c : XK_v);
    send(display,control,True,CurrentTime);
    if (key==2) send(display,shift,True,CurrentTime);
    send(display,letter,True,CurrentTime);
    send(display,letter,False,CurrentTime);
    if (key==2) send(display,shift,False,CurrentTime);
    send(display,control,False,CurrentTime);
    XFlush(display);
    dlclose(library);
    return 1;
}

static int vtcPrivateWheel(void *window,int down) {
    if (g_strcmp0(g_getenv("VTC_PRIVATE_DISPLAY"),"1")) return 0;
    GtkWidget *root=GTK_WIDGET(window);
    if (!GDK_IS_X11_DISPLAY(gtk_widget_get_display(root))) return 0;
    void *library=dlopen("libXtst.so.6",RTLD_NOW|RTLD_LOCAL);
    if (!library) return 0;
    VTCButton send=(VTCButton)dlsym(library,"XTestFakeButtonEvent");
    if (!send) {dlclose(library);return 0;}
    Display *display=gdk_x11_display_get_xdisplay(gtk_widget_get_display(root));
    for (int i=0;i<5;i++) { send(display,down ? 5 : 4,True,20);send(display,down ? 5 : 4,False,20); }
    XFlush(display);dlclose(library);return 1;
}

static int vtcPrivatePinyin(void *window) {
    if (g_strcmp0(g_getenv("VTC_PRIVATE_DISPLAY"),"1") ||
        g_strcmp0(g_getenv("VTC_PRIVATE_IME"),"1")) return 0;
    GtkWidget *root=GTK_WIDGET(window);
    if (!GDK_IS_X11_DISPLAY(gtk_widget_get_display(root))) return 0;
    void *library=dlopen("libXtst.so.6",RTLD_NOW|RTLD_LOCAL);
    if (!library) return 0;
    VTCKey send=(VTCKey)dlsym(library,"XTestFakeKeyEvent");
    if (!send) {dlclose(library);return 0;}
    Display *display=gdk_x11_display_get_xdisplay(gtk_widget_get_display(root));
    for (const char *p="zhongwen ";*p;p++) {
        unsigned int key=XKeysymToKeycode(display,(KeySym)*p);
        send(display,key,True,20);send(display,key,False,20);
    }
    XFlush(display);dlclose(library);return 1;
}

typedef struct { GtkWidget *root; int approve; int attempts; } VTCOverwriteProbe;
static GtkWidget *vtcOverwriteButton(GtkWidget *widget, const char *label) {
    if (GTK_IS_BUTTON(widget) && !g_strcmp0(gtk_button_get_label(GTK_BUTTON(widget)),label)) return widget;
    for (GtkWidget *child=gtk_widget_get_first_child(widget);child;child=gtk_widget_get_next_sibling(child)) {
        GtkWidget *button=vtcOverwriteButton(child,label);
        if (button) return button;
    }
    return NULL;
}
static gboolean vtcOverwriteReply(gpointer data) {
    VTCOverwriteProbe *probe=data;
    GListModel *windows=gtk_window_get_toplevels();
    for (guint i=0;i<g_list_model_get_n_items(windows);i++) {
        GtkWindow *window=g_list_model_get_item(windows,i);
        if (GTK_WIDGET(window)==probe->root || !gtk_widget_get_mapped(GTK_WIDGET(window))) {
            g_object_unref(window); continue;
        }
        GtkWidget *button=vtcOverwriteButton(GTK_WIDGET(window),probe->approve ? "Replace / 替换" : "Cancel / 取消");
        if (button) {
            g_signal_emit_by_name(button,"clicked");
            g_object_unref(window); return G_SOURCE_REMOVE;
        }
        // Accept only the chooser's preliminary confirmation; the application's
        // separate replacement question above still receives the requested answer.
        const char *labels[]={"_Replace","Replace","替换(_R)","替换",NULL};
        for (const char **label=labels;*label;label++) {
            button=vtcOverwriteButton(GTK_WIDGET(window),*label);
            if (button) { g_signal_emit_by_name(button,"clicked"); break; }
        }
        g_object_unref(window);
    }
    return ++probe->attempts<300 ? G_SOURCE_CONTINUE : G_SOURCE_REMOVE;
}
static void vtcOverwriteFree(gpointer data) {
    VTCOverwriteProbe *probe=data;
    g_object_unref(probe->root);g_free(probe);
}
static int vtcScheduleOverwrite(void *window,int approve) {
    VTCOverwriteProbe *probe=g_new0(VTCOverwriteProbe,1);
    probe->root=g_object_ref(GTK_WIDGET(window));probe->approve=approve;
    return g_timeout_add_full(G_PRIORITY_DEFAULT,100,vtcOverwriteReply,probe,vtcOverwriteFree)!=0;
}

static int vtcInputCommit(void *window,const char *text) {
    GtkWidget *view=vtcInputWebView(GTK_WIDGET(window));
    if (!view) return 0;
    WebKitInputMethodContext *context=webkit_web_view_get_input_method_context(WEBKIT_WEB_VIEW(view));
    if (!context) return 0;
    g_signal_emit_by_name(context,"preedit-started");
    g_signal_emit_by_name(context,"preedit-finished");
    g_signal_emit_by_name(context,"committed",text);
    return 1;
}
*/
import "C"

import (
	"os"
	"unsafe"
)

func privatePointer(window unsafe.Pointer, x, y, endX, endY float64, drag bool) bool {
	if os.Getenv("VTC_PRIVATE_WAYLAND") == "1" {
		return privateWaylandInput(window, "pointer", x, y, endX, endY, drag)
	}
	var dragging C.int
	if drag {
		dragging = 1
	}
	return C.vtcPrivatePointer(window, C.double(x), C.double(y), C.double(endX), C.double(endY), dragging) != 0
}

func privateKey(window unsafe.Pointer, paste bool) bool {
	if os.Getenv("VTC_PRIVATE_WAYLAND") == "1" {
		operation := "copy"
		if paste {
			operation = "paste"
		}
		return privateWaylandInput(window, operation, 0, 0, 0, 0, false)
	}
	key := C.int(1)
	if paste {
		key = 2
	}
	return C.vtcPrivateKey(window, key) != 0
}

func inputCommit(window unsafe.Pointer, text string) bool {
	value := C.CString(text)
	defer C.free(unsafe.Pointer(value))
	return C.vtcInputCommit(window, value) != 0
}

func scheduleOverwrite(window unsafe.Pointer, approved bool) bool {
	var answer C.int
	if approved {
		answer = 1
	}
	return C.vtcScheduleOverwrite(window, answer) != 0
}

func privateWheel(window unsafe.Pointer, down bool) bool {
	if os.Getenv("VTC_PRIVATE_WAYLAND") == "1" {
		operation := "wheel-up"
		if down {
			operation = "wheel-down"
		}
		return privateWaylandInput(window, operation, 0, 0, 0, 0, false)
	}
	var direction C.int
	if down {
		direction = 1
	}
	return C.vtcPrivateWheel(window, direction) != 0
}

func privatePinyin(window unsafe.Pointer) bool {
	if os.Getenv("VTC_PRIVATE_IME") != "1" {
		return false
	}
	if os.Getenv("VTC_PRIVATE_WAYLAND") == "1" {
		return privateWaylandInput(window, "pinyin", 0, 0, 0, 0, false)
	}
	return C.vtcPrivatePinyin(window) != 0
}
