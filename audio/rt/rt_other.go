//go:build !windows

package rt

// raise leaves the thread's priority as it is.
func raise() func() { return func() {} }
