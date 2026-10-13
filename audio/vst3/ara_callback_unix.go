//go:build !windows

package vst3

import "github.com/ebitengine/purego"

// newProgressCallback returns fn, which takes a float fourth, as a
// function a plugin can call.
func newProgressCallback(fn func(ctl, src uintptr, state int32, v float32)) uintptr {
	return purego.NewCallback(func(ctl, src, state uintptr, v float32) uintptr {
		fn(ctl, src, int32(uint32(state)), v)
		return 0
	})
}

// newPositionCallback returns fn, which takes a double second, as a
// function a plugin can call.
func newPositionCallback(fn func(ctl uintptr, at float64)) uintptr {
	return purego.NewCallback(func(ctl uintptr, at float64) uintptr {
		fn(ctl, at)
		return 0
	})
}

// newCycleCallback returns fn, which takes doubles second and third, as
// a function a plugin can call.
func newCycleCallback(fn func(ctl uintptr, at, length float64)) uintptr {
	return purego.NewCallback(func(ctl uintptr, at, length float64) uintptr {
		fn(ctl, at, length)
		return 0
	})
}
