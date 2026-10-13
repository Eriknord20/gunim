package vst3

import (
	"math"
	"syscall"
)

// newProgressCallback returns fn, which takes a float fourth, as a
// function a plugin can call: the float comes in XMM3, copied to R9.
func newProgressCallback(fn func(ctl, src uintptr, state int32, v float32)) uintptr {
	return floatThunkArgs(syscall.NewCallback(func(ctl, src, state, v uintptr) uintptr {
		fn(ctl, src, int32(uint32(state)), math.Float32frombits(uint32(v)))
		return 0
	}), 3)
}

// newPositionCallback returns fn, which takes a double second, as a
// function a plugin can call: the double comes in XMM1, copied to RDX.
func newPositionCallback(fn func(ctl uintptr, at float64)) uintptr {
	return floatThunkArgs(syscall.NewCallback(func(ctl, at uintptr) uintptr {
		fn(ctl, math.Float64frombits(uint64(at)))
		return 0
	}), 1)
}

// newCycleCallback returns fn, which takes doubles second and third, as
// a function a plugin can call: they come in XMM1 and XMM2, copied to
// RDX and R8.
func newCycleCallback(fn func(ctl uintptr, at, length float64)) uintptr {
	return floatThunkArgs(syscall.NewCallback(func(ctl, at, length uintptr) uintptr {
		fn(ctl, math.Float64frombits(uint64(at)), math.Float64frombits(uint64(length)))
		return 0
	}), 1, 2)
}
