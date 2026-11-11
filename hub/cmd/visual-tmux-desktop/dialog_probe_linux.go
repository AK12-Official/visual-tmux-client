//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <stdlib.h>
#include <string.h>
#include <gtk/gtk.h>

typedef struct {
    GtkWidget *root;
    int approve;
    char *text;
    int attempts;
} VTCDialogProbe;

static GtkWidget *vtcFindScriptDialog(GtkWidget *widget) {
    if (!strcmp(G_OBJECT_TYPE_NAME(widget), "WebKitScriptDialogImpl")) return widget;
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child; child = gtk_widget_get_next_sibling(child)) {
        GtkWidget *found = vtcFindScriptDialog(child);
        if (found) return found;
    }
    return NULL;
}

static void vtcDialogControls(GtkWidget *widget, GtkWidget **first, GtkWidget **last, const char *text) {
    if (GTK_IS_ENTRY(widget)) gtk_editable_set_text(GTK_EDITABLE(widget), text);
    if (GTK_IS_BUTTON(widget)) {
        if (!*first) *first = widget;
        *last = widget;
    }
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child; child = gtk_widget_get_next_sibling(child)) {
        vtcDialogControls(child, first, last, text);
    }
}

static gboolean vtcRespondToScriptDialog(gpointer data) {
    VTCDialogProbe *probe = data;
    GtkWidget *dialog = vtcFindScriptDialog(probe->root);
    if (dialog) {
        GtkWidget *first = NULL, *last = NULL;
        vtcDialogControls(dialog, &first, &last, probe->text);
        GtkWidget *button = probe->approve ? last : first;
        if (button) g_signal_emit_by_name(button, "clicked");
        return G_SOURCE_REMOVE;
    }
    return ++probe->attempts < 50 ? G_SOURCE_CONTINUE : G_SOURCE_REMOVE;
}

static void vtcFreeDialogProbe(gpointer data) {
    VTCDialogProbe *probe = data;
    g_object_unref(probe->root);
    g_free(probe->text);
    g_free(probe);
}

static int vtcScheduleDialogProbe(void *window, int approve, const char *text) {
    VTCDialogProbe *probe = g_new0(VTCDialogProbe, 1);
    probe->root = g_object_ref(GTK_WIDGET(window));
    probe->approve = approve;
    probe->text = g_strdup(text);
    return g_timeout_add_full(G_PRIORITY_DEFAULT, 100, vtcRespondToScriptDialog, probe, vtcFreeDialogProbe) != 0;
}
*/
import "C"

import "unsafe"

// Only the explicit disposable probe registers the HTTP action that calls this.
func scheduleDialogProbe(window unsafe.Pointer, approved bool, text string) bool {
	var answer C.int
	if approved {
		answer = 1
	}
	value := C.CString(text)
	defer C.free(unsafe.Pointer(value))
	return C.vtcScheduleDialogProbe(window, answer, value) != 0
}
