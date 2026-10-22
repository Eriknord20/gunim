//go:build !windows

package speaker

// nanotime is a clock in nanoseconds, from a moment of its own.
func nanotime() int64 { return goNanotime() }
