//go:build desktop && darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
int vtcInstallNavigation(void *window, const char *origin);
int vtcBlockedCount(void *window);
void vtcCancelOwnedDialogs(void *window);
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"unsafe"
)

func installNavigationGuard(window unsafe.Pointer, origin string) bool {
	cOrigin := C.CString(origin)
	defer C.free(unsafe.Pointer(cOrigin))
	return C.vtcInstallNavigation(window, cOrigin) != 0
}

func blockedNavigationCount(window unsafe.Pointer) int { return int(C.vtcBlockedCount(window)) }

func cancelOwnedDialogs(window *application.WebviewWindow) {
	application.InvokeSync(func() { C.vtcCancelOwnedDialogs(window.NativeWindow()) })
}
