//go:build !(windows && amd64)

package vst3

// installGuard has nothing to do here: see guard_windows_amd64.go.
func installGuard() error { return nil }

// Recovered reports no faults here: see guard_windows_amd64.go.
func Recovered() (n uint64, module string, offset uintptr) { return 0, "", 0 }
