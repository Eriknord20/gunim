//go:build !windows

package vst3

import (
	"sync"

	"github.com/ebitengine/purego"
)

// funcs of a double second argument, made once for each function.
var floatFuncs sync.Map

// callFloat calls method m of obj, of an integer argument and a double:
// as setParamNormalized.
func callFloat(obj uintptr, m int, a uintptr, v float64) uintptr {
	fn := method(obj, m)
	f, ok := floatFuncs.Load(fn)
	if !ok {
		var g func(obj, a uintptr, v float64) uintptr
		purego.RegisterFunc(&g, fn)
		f, _ = floatFuncs.LoadOrStore(fn, g)
	}
	g, _ := f.(func(obj, a uintptr, v float64) uintptr)
	return g(obj, a, v)
}

// callN calls fn with args, through purego, which copies them where
// the call takes them, so they stay where the caller made them.
func callN(fn uintptr, args []uintptr) uintptr {
	r, _, _ := purego.SyscallN(fn, args...)
	return r
}

// funcs of a double result, and of a double third argument, made once
// for each function.
var floatOutFuncs, floatArgFuncs sync.Map

// callFloatOut calls method m of obj, of an integer argument, which
// returns a double: as getParamNormalized.
func callFloatOut(obj uintptr, m int, a uintptr) float64 {
	fn := method(obj, m)
	f, ok := floatOutFuncs.Load(fn)
	if !ok {
		var g func(obj, a uintptr) float64
		purego.RegisterFunc(&g, fn)
		f, _ = floatOutFuncs.LoadOrStore(fn, g)
	}
	g, _ := f.(func(obj, a uintptr) float64)
	return g(obj, a)
}

// callFloatArg calls method m of obj, of an integer argument, a double
// and a pointer: as getParamStringByValue.
func callFloatArg(obj uintptr, m int, a uintptr, v float64, b uintptr) uintptr {
	fn := method(obj, m)
	f, ok := floatArgFuncs.Load(fn)
	if !ok {
		var g func(obj, a uintptr, v float64, b uintptr) uintptr
		purego.RegisterFunc(&g, fn)
		f, _ = floatArgFuncs.LoadOrStore(fn, g)
	}
	g, _ := f.(func(obj, a uintptr, v float64, b uintptr) uintptr)
	return g(obj, a, v, b)
}
