//go:build !windows && (!linux || android)

package rt

// raise leaves the thread's priority as it is.
func raise() func() { return func() {} }

func doze() {}
