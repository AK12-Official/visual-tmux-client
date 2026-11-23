//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0 wayland-client
#include <gtk/gtk.h>
#include <gdk/wayland/gdkwayland.h>
#include <webkit/webkit.h>
#include <wayland-client.h>

static GtkWidget *vtcWaylandView(GtkWidget *widget) {
    if (WEBKIT_IS_WEB_VIEW(widget)) return widget;
    for (GtkWidget *child=gtk_widget_get_first_child(widget);child;child=gtk_widget_get_next_sibling(child)) {
        GtkWidget *view=vtcWaylandView(child);
        if (view) return view;
    }
    return NULL;
}

static unsigned int vtcWaylandTarget(void *window,int pointer,double *x,double *y,double *endx,double *endy) {
    GtkWidget *root=GTK_WIDGET(window);
    if (!GDK_IS_WAYLAND_DISPLAY(gtk_widget_get_display(root))) return 0;
    GdkSurface *surface=gtk_native_get_surface(GTK_NATIVE(root));
    if (!surface) return 0;
    if (pointer) {
        GtkWidget *view=vtcWaylandView(root);
        graphene_point_t from={*x,*y},to;
        if (!view || !gtk_widget_compute_point(view,root,&from,&to)) return 0;
        double dx,dy;
        gtk_native_get_surface_transform(GTK_NATIVE(root),&dx,&dy);
        *x=to.x+dx;*y=to.y+dy;
        from.x=*endx;from.y=*endy;
        if (!gtk_widget_compute_point(view,root,&from,&to)) return 0;
        *endx=to.x+dx;*endy=to.y+dy;
    }
    struct wl_surface *wayland=gdk_wayland_surface_get_wl_surface(surface);
    return wayland ? wl_proxy_get_id((struct wl_proxy *)wayland) : 0;
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"time"
	"unsafe"
)

const privateWaylandDeadline = 3 * time.Second

// Only the authenticated native test fixture calls this, on the UI thread.
// The compositor verifies the peer PID and target wl_surface's owning client.
func privateWaylandInput(window unsafe.Pointer, operation string, x, y, endX, endY float64, drag bool) bool {
	if os.Getenv("VTC_PRIVATE_WAYLAND") != "1" || os.Getenv("VTC_WAYLAND_INPUT") == "" {
		return false
	}
	var pointer, dragging C.int
	if operation == "pointer" {
		pointer = 1
	}
	if drag {
		dragging = 1
	}
	cx, cy, ex, ey := C.double(x), C.double(y), C.double(endX), C.double(endY)
	id := C.vtcWaylandTarget(window, pointer, &cx, &cy, &ex, &ey)
	if id == 0 {
		return false
	}
	connection, err := net.DialTimeout("unix", os.Getenv("VTC_WAYLAND_INPUT"), privateWaylandDeadline)
	if err != nil {
		return false
	}
	defer connection.Close() //nolint:errcheck // only an owned verification socket
	if err := connection.SetDeadline(time.Now().Add(privateWaylandDeadline)); err != nil {
		return false
	}
	if _, err := fmt.Fprintf(connection, "%d %d %s %.3f %.3f %.3f %.3f %d\n",
		os.Getpid(), uint32(id), operation, float64(cx), float64(cy),
		float64(ex), float64(ey), int(dragging)); err != nil {
		return false
	}
	reply, err := bufio.NewReader(connection).ReadString('\n')
	return err == nil && reply == "OK\n"
}
