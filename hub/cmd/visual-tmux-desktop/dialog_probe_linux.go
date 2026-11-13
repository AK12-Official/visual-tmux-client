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
    int configured;
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

// Only the probe drives GTK's real fallback chooser. Its widget automation
// interface is deprecated; production uses the modern GtkFileDialog API.
G_GNUC_BEGIN_IGNORE_DEPRECATIONS
static gboolean vtcRespondToSaveDialog(gpointer data) {
    VTCDialogProbe *probe = data;
    if (++probe->attempts >= 50) return G_SOURCE_REMOVE;
    GListModel *windows = gtk_window_get_toplevels();
    for (guint i = 0; i < g_list_model_get_n_items(windows); i++) {
        GtkWindow *window = g_list_model_get_item(windows, i);
        if (GTK_WIDGET(window) == probe->root || !GTK_IS_FILE_CHOOSER(window) || !GTK_IS_DIALOG(window) ||
            !gtk_widget_get_mapped(GTK_WIDGET(window))) {
            g_object_unref(window);
            continue;
        }
        if (!probe->approve) {
            if (++probe->configured < 3) {
                g_object_unref(window);
                return G_SOURCE_CONTINUE;
            }
            g_printerr("spike: GTK chooser automation cancels\n");
            gtk_dialog_response(GTK_DIALOG(window), GTK_RESPONSE_CANCEL);
            g_object_unref(window);
            return G_SOURCE_REMOVE;
        }
        if (!probe->configured) {
            char *directory = g_path_get_dirname(probe->text);
            GFile *folder = g_file_new_for_path(directory);
            gtk_file_chooser_set_current_folder(GTK_FILE_CHOOSER(window), folder, NULL);
            g_object_unref(folder);
            g_free(directory);
        }
        if (++probe->configured >= 3) {
            GFile *selected=gtk_file_chooser_get_file(GTK_FILE_CHOOSER(window));
            GFile *expected=g_file_new_for_path(probe->text);
            gboolean matches=selected && g_file_equal(selected,expected);
            if (selected) g_object_unref(selected);
            g_object_unref(expected);
            if (!matches) {
                char *name=g_path_get_basename(probe->text);
                char *directory=g_path_get_dirname(probe->text);
                GFile *folder=g_file_new_for_path(directory);
                GFile *current=gtk_file_chooser_get_current_folder(GTK_FILE_CHOOSER(window));
                if (current && g_file_equal(current,folder)) {
                    gtk_file_chooser_set_current_name(GTK_FILE_CHOOSER(window),name);
                } else if (probe->configured%5==0) {
                    gtk_file_chooser_set_current_folder(GTK_FILE_CHOOSER(window),folder,NULL);
                }
                if (current) g_object_unref(current);
                g_object_unref(folder);g_free(directory);
                g_free(name);g_object_unref(window);return G_SOURCE_CONTINUE;
            }
            g_printerr("spike: GTK chooser automation accepts\n");
            gtk_dialog_response(GTK_DIALOG(window), GTK_RESPONSE_ACCEPT);
            g_object_unref(window);
            return G_SOURCE_REMOVE;
        }
        g_object_unref(window);
        return G_SOURCE_CONTINUE;
    }
    return G_SOURCE_CONTINUE;
}
G_GNUC_END_IGNORE_DEPRECATIONS

static int vtcScheduleSaveProbe(void *window, int approve, const char *path) {
    VTCDialogProbe *probe = g_new0(VTCDialogProbe, 1);
    probe->root = g_object_ref(GTK_WIDGET(window));
    probe->approve = approve;
    probe->text = g_strdup(path);
    return g_timeout_add_full(G_PRIORITY_DEFAULT, 100, vtcRespondToSaveDialog, probe, vtcFreeDialogProbe) != 0;
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

func scheduleSaveProbe(window unsafe.Pointer, approved bool, path string) bool {
	var answer C.int
	if approved {
		answer = 1
	}
	value := C.CString(path)
	defer C.free(unsafe.Pointer(value))
	return C.vtcScheduleSaveProbe(window, answer, value) != 0
}
