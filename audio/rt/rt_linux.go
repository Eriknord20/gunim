//go:build linux && !android

package rt

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const (
	// priority is the real-time priority an audio thread runs at: under
	// PipeWire's own thread, at RealtimeKit's limit of 20 by default, as
	// the thread that fills a ring must not keep the one that empties it
	// from running.
	priority = 10
	// rtTimeSoft and rtTimeHard limit how long, in microseconds, a thread
	// on RealtimeKit's real-time scheduling may run without blocking.
	// RealtimeKit asks for a limit, by default 200 ms at the most. The
	// system tells at the soft limit, and kills the program at the hard
	// one, so a thread that runs too long goes back to normal priority
	// before then.
	rtTimeSoft = 100_000
	rtTimeHard = 200_000
	// rtkitWait is how long a thread waits for RealtimeKit to answer.
	rtkitWait = 500 * time.Millisecond
)

// restAfterTrip is how long no thread is raised after the system said
// one ran too long: under such a load, raising them again would only
// bring it back.
const restAfterTrip = 30 * time.Second

var (
	// mu guards raised and tripped. No one holds it across a call to
	// RealtimeKit, as the guard must never wait on one.
	mu sync.Mutex
	// raised counts the Enters of each thread raised, by its ID, and
	// tripped is when the system last said one ran too long.
	raised  = map[int]int{}
	tripped time.Time
	// kitMu guards viaKit, the system bus, once RealtimeKit has been
	// asked, and noKit, which says RealtimeKit isn't there, so no thread
	// waits for it again. A refusal or a late answer doesn't set it, as
	// RealtimeKit refuses for a while when asked too often, and answers
	// late under load.
	kitMu  sync.Mutex
	viaKit *dbus.Conn
	noKit  bool
	// kitAsk and kitAnswer reach the goroutine that asks RealtimeKit.
	kitAsk    = make(chan int)
	kitAnswer = make(chan bool)
)

// init starts what lets go of real-time scheduling at the soft limit, and
// what asks RealtimeKit. They start here, not as a thread first enters, as
// that thread may be a test's in a testing/synctest bubble: what the Go
// runtime or the bus starts for them would be taken into it, for good.
func init() {
	watchRTTime()
	go func() {
		for tid := range kitAsk {
			kitAnswer <- askKitNow(tid)
		}
	}()
}

// raise asks for real-time scheduling for the calling thread, and
// returns what gives it up.
func raise() func() {
	tid := unix.Gettid()
	mu.Lock()
	if raised[tid] > 0 {
		raised[tid]++
		mu.Unlock()
		return func() { lower(tid) }
	}
	// One processor stays free of real-time threads, so a song that keeps
	// them all busy leaves the window, the collector and the guard room.
	full := len(raised) >= maxRaised(runtime.NumCPU())
	resting := !tripped.IsZero() && time.Since(tripped) < restAfterTrip
	if !full && !resting {
		raised[tid] = 0 // held, so the count above stays true as it asks
	}
	mu.Unlock()
	if full || resting {
		return func() {}
	}
	if !setFIFO(tid) && !askKit(tid) {
		mu.Lock()
		delete(raised, tid)
		mu.Unlock()
		return func() {}
	}
	mu.Lock()
	raised[tid] = 1
	mu.Unlock()
	return func() { lower(tid) }
}

// maxRaised is how many threads may be real-time on cpus processors.
func maxRaised(cpus int) int { return cpus - 1 }

// lower ends one Enter of thread tid, and puts it back at normal
// priority after the last.
func lower(tid int) {
	mu.Lock()
	defer mu.Unlock()
	if raised[tid] == 0 {
		return
	}
	raised[tid]--
	if raised[tid] == 0 {
		delete(raised, tid)
		_ = setNormal(tid)
	}
}

// setFIFO has the system make thread tid real-time, as where the user's
// rtprio limit allows it.
func setFIFO(tid int) bool {
	return unix.SchedSetAttr(tid, &unix.SchedAttr{
		Size: unix.SizeofSchedAttr, Policy: unix.SCHED_FIFO, Priority: priority, Flags: unix.SCHED_FLAG_RESET_ON_FORK,
	}, 0) == nil
}

// setNormal puts thread tid back at normal priority, keeping its nice,
// as raising that needs privilege. A thread may always lower itself, but
// without privilege only with its reset-on-fork flag kept, which
// RealtimeKit sets.
func setNormal(tid int) error {
	attr := unix.SchedAttr{Size: unix.SizeofSchedAttr, Policy: unix.SCHED_NORMAL, Flags: unix.SCHED_FLAG_RESET_ON_FORK}
	if now, err := unix.SchedGetAttr(tid, 0); err == nil {
		attr.Nice = now.Nice
	}
	return unix.SchedSetAttr(tid, &attr, 0)
}

// askKit asks RealtimeKit to make thread tid real-time.
func askKit(tid int) bool {
	kitMu.Lock()
	defer kitMu.Unlock()
	if noKit {
		return false
	}
	kitAsk <- tid
	return <-kitAnswer
}

// askKitNow is askKit's, on the goroutine that asks, with kitMu held.
func askKitNow(tid int) bool {
	if err := limitRTTime(); err != nil {
		noKit = true
		return false
	}
	if viaKit == nil {
		c, err := dbus.SystemBus()
		if err != nil {
			noKit = true
			return false
		}
		viaKit = c
	}
	ctx, cancel := context.WithTimeout(context.Background(), rtkitWait)
	defer cancel()
	call := viaKit.Object("org.freedesktop.RealtimeKit1", "/org/freedesktop/RealtimeKit1").
		CallWithContext(ctx, "org.freedesktop.RealtimeKit1.MakeThreadRealtime", 0, uint64(tid), uint32(priority))
	if call.Err != nil {
		var e dbus.Error
		if errors.As(call.Err, &e) && (e.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || e.Name == "org.freedesktop.DBus.Error.NameHasNoOwner") {
			noKit = true
		}
		return false
	}
	return true
}

// limitRTTime sets the limit RealtimeKit asks for, unless a lower one
// is set.
func limitRTTime() error {
	var l unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_RTTIME, &l); err != nil {
		return err
	}
	if l.Max <= rtTimeHard && l.Cur <= rtTimeSoft {
		return nil
	}
	return unix.Setrlimit(unix.RLIMIT_RTTIME, &unix.Rlimit{Cur: min(l.Cur, rtTimeSoft), Max: min(l.Max, rtTimeHard)})
}

// watchRTTime hears the system say a thread has run too long without
// blocking, and puts every raised thread back at normal priority, so the
// system doesn't kill the program at the hard limit. Better late sound
// than lost work.
func watchRTTime() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGXCPU)
	go func() {
		for range c {
			if n := lowerAll(); n > 0 {
				log.Printf("rt: an audio thread ran over %d ms without a pause: %d threads back at normal priority", rtTimeSoft/1000, n)
			}
		}
	}()
}

// lowerAll puts every raised thread back at normal priority, for now and
// until restAfterTrip has passed, and returns how many there were. Their
// Enters end as they would.
func lowerAll() int {
	mu.Lock()
	defer mu.Unlock()
	tripped = time.Now()
	n := len(raised)
	for tid := range raised {
		if err := setNormal(tid); err != nil {
			log.Printf("rt: thread %d stays at real-time priority: %v", tid, err)
		}
		delete(raised, tid)
	}
	return n
}

// doze sleeps a moment. Long enough to be sure of it: a timer of a
// nanosecond can fire before the thread has gone to sleep, which then
// doesn't, and only a thread that truly slept starts its run anew. A
// real-time thread's timer has no slack, so it is back within microseconds.
func doze() {
	ts := unix.Timespec{Nsec: 20_000}
	_ = unix.Nanosleep(&ts, nil)
}

// nap sleeps by the system's own call, with the thread keeping its P, so it
// wakes by the system's clock and not when Go's scheduler gets to it. A signal
// ends it at once, so it keeps nothing in Go waiting.
func nap(d time.Duration) {
	ts := unix.NsecToTimespec(d.Nanoseconds())
	_, _, _ = unix.RawSyscall(unix.SYS_NANOSLEEP, uintptr(unsafe.Pointer(&ts)), 0, 0)
}
