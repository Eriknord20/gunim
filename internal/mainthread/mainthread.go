// Package mainthread runs work on the program's main thread, where a
// driver pumps its events there and lets it: macOS's AppKit, and the
// plugin editors built on it, take their calls there alone.
package mainthread

import "sync/atomic"

// poster runs work on the main thread, as Set gave it.
var poster atomic.Pointer[func(func()) bool]

// Set has post run work on the main thread from now on, or none where
// post is nil. post runs f there later, in turn, and reports false where
// it will not, as the driver stops.
func Set(post func(f func()) bool) {
	if post == nil {
		poster.Store(nil)
		return
	}
	poster.Store(&post)
}

// Available reports whether a driver runs work on the main thread.
func Available() bool { return poster.Load() != nil }

// Post hands f to the main thread, and reports whether it will run.
func Post(f func()) bool {
	p := poster.Load()
	return p != nil && (*p)(f)
}
