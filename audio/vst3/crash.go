//go:build !js

package vst3

// A plugin that crashes during a call the host makes into it need not end the program, on Windows on x64: see
// crashcall_windows_amd64.go. The call comes back as if it failed, and the plugin's module is dead from then on: nothing
// calls into it again, for every instance of every class it makes, as a fault may leave its locks held and its memory
// wrong. It stays loaded until the program ends, as code of its may still run on threads it started.

// A Crash is a plugin's module that crashed during a call into it.
type Crash struct {
	// Module is the file of the module called, or "" where it is none.
	Module string
	// Code is the exception's code, as 0xC0000005 for an access violation.
	Code uint32
	// Fault is the file the fault was in, which may be a system library the plugin called, or "" where it is none, and
	// Offset how far into it the fault was.
	Fault  string
	Offset uintptr
}

// resultCrashed is what a call into a plugin gives that crashed, or that was not made as the plugin's module crashed
// before. It is no result a plugin gives.
const resultCrashed = int32(0x8FEFDEAD - 1<<32)

// crashedResult is resultCrashed as a call returns it.
const crashedResult = uintptr(0x8FEFDEAD)

// pointerOf is r, what a call returned, as a pointer or a number whose zero means nothing: a call that crashed gave none.
func pointerOf(r uintptr) uintptr {
	if r == crashedResult {
		return 0
	}
	return r
}
