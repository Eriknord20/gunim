//go:build !windows && !js

package vst3

import "github.com/ebitengine/purego"

// newCallback returns fn as a function a plugin can call.
func newCallback(fn any) uintptr { return purego.NewCallback(fn) }

// newFloatCallback returns fn, which takes a double third, as a function
// a plugin can call.
func newFloatCallback(fn func(a, b uintptr, v float64) uintptr) uintptr {
	return purego.NewCallback(fn)
}

// newFloatCallback4 is newFloatCallback for a function of four
// arguments.
func newFloatCallback4(fn func(a, b uintptr, v float64, d uintptr) uintptr) uintptr {
	return purego.NewCallback(fn)
}
