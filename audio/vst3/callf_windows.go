package vst3

import (
	"math"
	"syscall"
)

// callFloat calls method m of obj, of an integer argument and a double:
// as setParamNormalized. Go's calls on Windows put each argument in its
// floating register too, where x64 looks for a double.
func callFloat(obj uintptr, m int, a uintptr, v float64) uintptr {
	r, _, _ := syscall.SyscallN(method(obj, m), obj, a, uintptr(math.Float64bits(v)))
	return r
}

// callN calls fn with args, through the system's own call, which keeps
// them where the caller made them: purego's, on Windows, makes them
// escape to the heap, a slice made each call.
func callN(fn uintptr, args []uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}
