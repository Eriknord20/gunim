//go:build android

package aaudio

/*
#include <stdlib.h>
*/
import "C"

import "unsafe"

// alloc returns n bytes of C memory, zeroed, which AAudio's thread may
// read and write while Go does: the collector neither moves nor frees
// it.
func alloc(n uintptr) unsafe.Pointer { return C.calloc(1, C.size_t(n)) }

// free lets memory from alloc go.
func free(p unsafe.Pointer) { C.free(p) }
