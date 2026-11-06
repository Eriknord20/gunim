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
// the computer. On Linux it asks for real-time scheduling, from the
// system where the user's limits allow it, otherwise from RealtimeKit,
// which most desktops run. Elsewhere, or where neither gives it, the
// thread runs at the priority it has: Enter changes no privileges.
package rt

import (
	"runtime"
	"time"
)

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

// Breathe has the thread truly sleep, for as short as the system can. A
// thread that spins between its work, waiting for more, calls it every so
// often: on Linux the system limits how long a real-time thread may run
// without sleeping, and a yield doesn't count.
func Breathe() { doze() }

// Sleep has the thread sleep for about d, or less, as a signal may end it.
// Where a thread is real-time, this is what wakes it on time: waiting on a
// timer of Go's, it wakes only as the scheduler gets to it. Go still preempts
// a goroutine that runs on for long, and with every P busy, it then waits its
// turn. It is for a thread locked by [Enter].
func Sleep(d time.Duration) { nap(d) }
