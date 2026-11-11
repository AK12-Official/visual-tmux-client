//go:build desktop && darwin

package main

/*
#include <stdlib.h>
int vtcScheduleDialogProbe(void *window, int approve, const char *text);
int vtcScheduleSaveProbe(void *window, int approve, const char *path);
*/
import "C"

import "unsafe"

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
