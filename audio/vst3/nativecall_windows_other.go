//go:build windows && !amd64

package vst3

import "syscall"

// nativeCall calls fn with args. Only Windows on x64 lives through a plugin's crash: see crashcall_windows_amd64.go.
func nativeCall(fn uintptr, args ...uintptr) (r1, r2 uintptr) {
	r1, r2, _ = syscall.SyscallN(fn, args...)
	return r1, r2
}

// noteUIThread has nothing to note here: see crashcall_windows_amd64.go.
func noteUIThread() {}
