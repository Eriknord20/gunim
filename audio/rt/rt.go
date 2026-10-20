// Package rt readies a thread to do an audio output's work in time.
//
// A goroutine that renders sound calls [Enter] as it starts, and the
// func it returns as it ends:
//
//	leave := rt.Enter()
//	defer leave()
//
// Enter locks the goroutine to its thread. On Windows the thread then
// joins the Multimedia Class Scheduler Service's "Pro Audio" task, at
// high priority, so the system runs it before the rest of the work on
// the computer. Elsewhere the thread runs at the priority it has: Enter
// changes no privileges.
package rt

import "runtime"

// Enter locks the calling goroutine to its thread and raises the
// thread to the system's audio priority, where it has one. It returns
// what undoes both; the goroutine calls it once, on the same thread,
// before it ends. Where the priority cannot be had, the thread runs as
// it is, locked all the same.
func Enter() (leave func()) {
	runtime.LockOSThread()
	undo := raise()
	return func() {
		undo()
		runtime.UnlockOSThread()
	}
}
