//go:build !android

package aaudio

import "unsafe"

// alloc returns n bytes, zeroed and aligned for an int64. Here no C
// reads them, so they are Go's, and kept alive by those who hold them.
func alloc(n uintptr) unsafe.Pointer {
	return unsafe.Pointer(unsafe.SliceData(make([]uint64, (n+7)/8)))
}

// free lets memory from alloc go: the collector frees it here.
func free(unsafe.Pointer) {}
