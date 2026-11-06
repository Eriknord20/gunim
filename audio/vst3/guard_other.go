//go:build !js && !(windows && amd64)

package vst3

import "os"

// installGuard has nothing to do here: see guard_windows_amd64.go.
func installGuard() error { return nil }

// Recovered reports no faults here: see guard_windows_amd64.go.
func Recovered() (n uint64, module string, offset uintptr) { return 0, "", 0 }

// Crashes reports none here: a plugin's crash ends the program. See crashcall_windows_amd64.go.
func Crashes() []Crash { return nil }

// moduleDead reports none here: see crashcall_windows_amd64.go.
func moduleDead(uintptr) bool { return false }

// ownFn has nothing to note here: see crashcall_windows_amd64.go.
func ownFn(uintptr, string) {}

// setUIOwner has nothing to note here: see crashcall_windows_amd64.go.
func setUIOwner(string) {}

// noteUIThread has nothing to note here: see crashcall_windows_amd64.go.
func noteUIThread() {}

// crashSignal closes never here: see crashcall_windows_amd64.go.
func crashSignal() <-chan struct{} { return nil }

// EndNow ends the program at once, with code.
func EndNow(code uint32) { os.Exit(int(code)) }
