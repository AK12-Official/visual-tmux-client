//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>

static void vtcStartupID(void *window, const char *id) {
    gtk_window_set_startup_id(GTK_WINDOW(window), id);
}
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Called on the GTK thread before presenting a repeated desktop launch.
func applyActivationToken(window *application.WebviewWindow, token string) {
	if token == "" || len(token) > 4096 {
		return
	}
	value := C.CString(token)
	defer C.free(unsafe.Pointer(value))
	C.vtcStartupID(window.NativeWindow(), value)
}
