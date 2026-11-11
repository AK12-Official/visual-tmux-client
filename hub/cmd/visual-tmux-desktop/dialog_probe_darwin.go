//go:build desktop && darwin

package main

/*
#include <stdlib.h>
int vtcScheduleDialogProbe(void *window, int approve, const char *text);
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
