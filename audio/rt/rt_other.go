//go:build !windows && (!linux || android)

package rt

import "time"

// raise leaves the thread's priority as it is.
func raise() func() { return func() {} }

func doze() {}

func nap(d time.Duration) { time.Sleep(d) }

func wasNotGiven() bool { return false }
