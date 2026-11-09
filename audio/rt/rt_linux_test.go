//go:build linux && !android

package rt

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// rested clears what a test's lowering left, for the next to raise.
func rested(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		mu.Lock()
		tripped = time.Time{}
		mu.Unlock()
	})
}

func policy(t *testing.T) uint32 {
	t.Helper()
	a, err := unix.SchedGetAttr(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a.Policy
}

// enterOrSkip enters, or skips where this machine gives no real-time
// scheduling, as a build machine.
func enterOrSkip(t *testing.T) func() {
	t.Helper()
	leave := Enter()
	if policy(t) != unix.SCHED_FIFO && policy(t) != unix.SCHED_RR {
		leave()
		t.Skip("no real-time scheduling here")
	}
	return leave
}

func TestEnterMakesTheThreadRealTimeAndLeaveEndsIt(t *testing.T) {
	leave := enterOrSkip(t)
	leave()
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("after leaving the policy is %d", p)
	}
}

func TestNestedEntersStayRealTimeUntilTheLast(t *testing.T) {
	outer := enterOrSkip(t)
	inner := Enter()
	inner()
	if p := policy(t); p == unix.SCHED_NORMAL {
		t.Error("the inner leave ended the outer enter's real-time scheduling")
	}
	outer()
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("after the last leave the policy is %d", p)
	}
}

func TestLowerAllPutsEveryThreadBack(t *testing.T) {
	rested(t)
	leave := enterOrSkip(t)
	defer leave()
	if n := lowerAll(); n == 0 {
		t.Error("lowered no threads")
	}
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("after lowering the policy is %d", p)
	}
}

func TestAReraisedThreadCountsItsEntersAnew(t *testing.T) {
	rested(t)
	leave := enterOrSkip(t)
	lowerAll()
	// Its leave after lowering changes nothing, and does no harm.
	leave()
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("the policy is %d", p)
	}
}

func TestAThreadStaysAtNormalPriorityJustAfterATrip(t *testing.T) {
	rested(t)
	mu.Lock()
	tripped = time.Now()
	mu.Unlock()
	leave := Enter()
	defer leave()
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("the policy is %d", p)
	}
}

func TestOneProcessorStaysFreeOfRealTimeThreads(t *testing.T) {
	if n := maxRaised(4); n != 3 {
		t.Errorf("4 processors take %d", n)
	}
	rested(t)
	mu.Lock()
	for i := range maxRaised(runtime.NumCPU()) {
		raised[-1-i] = 1
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		for tid := range raised {
			if tid < 0 {
				delete(raised, tid)
			}
		}
		mu.Unlock()
	}()
	leave := Enter()
	defer leave()
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("the policy is %d", p)
	}
}

func TestSetNormalKeepsTheNice(t *testing.T) {
	// A lower priority can't be raised again unprivileged, so this runs
	// on a thread of its own, which ends with the test.
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with it
		tid := unix.Gettid()
		if err := unix.Setpriority(unix.PRIO_PROCESS, tid, 5); err != nil {
			done <- err
			return
		}
		done <- setNormal(tid)
	}()
	if err := <-done; err != nil {
		t.Errorf("at nice 5: %v", err)
	}
}

func TestBreathingKeepsASpinningThreadWithinTheLimit(t *testing.T) {
	leave := enterOrSkip(t)
	defer leave()
	rested(t)
	if !strings.Contains(os.Getenv("GODEBUG"), "asyncpreemptoff=1") {
		t.Skip("run with GODEBUG=asyncpreemptoff=1 for a spin that nothing else ends")
	}
	// No calls in the loop but Breathe, as a call could be preempted, and
	// that alone would end the thread's run. About 2 s, 25 ms at a time.
	sink := 0
	for range 80 {
		for i := range 50_000_000 {
			sink += i
		}
		Breathe()
	}
	spin = sink
	if p := policy(t); p == unix.SCHED_NORMAL {
		t.Error("the thread was lowered though it breathed")
	}
}

func TestSleepSleeps(t *testing.T) {
	leave := Enter()
	defer leave()
	t0 := time.Now()
	Sleep(2 * time.Millisecond)
	if d := time.Since(t0); d < time.Millisecond || d > time.Second {
		t.Errorf("it slept %v", d)
	}
}

func TestAThreadRefusedSaysSo(t *testing.T) {
	kitMu.Lock()
	noKit = true
	kitMu.Unlock()
	t.Cleanup(func() {
		kitMu.Lock()
		noKit = false
		kitMu.Unlock()
		wasRefused.Store(false)
	})
	leave := Enter()
	defer leave()
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Skip("the user's limits give real-time scheduling here")
	}
	if !Refused() {
		t.Error("it doesn't say it was refused")
	}
}

func TestAThreadHeldBackOnPurposeIsNotRefused(t *testing.T) {
	rested(t)
	wasRefused.Store(false)
	mu.Lock()
	tripped = time.Now()
	mu.Unlock()
	leave := Enter()
	defer leave()
	if Refused() {
		t.Error("resting after a thread ran too long counts as refused")
	}
}

func TestLimitRTTimeIsWithinWhatRealtimeKitAsks(t *testing.T) {
	if err := limitRTTime(); err != nil {
		t.Fatal(err)
	}
	var l unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_RTTIME, &l); err != nil {
		t.Fatal(err)
	}
	if l.Cur > rtTimeSoft || l.Max > rtTimeHard {
		t.Errorf("the limit is %d, %d", l.Cur, l.Max)
	}
}

// A thread running past the soft limit must come back to normal priority
// before the hard one, where the system would kill the program. Plain Go
// doesn't reach it, as preempting a goroutine ends its thread's run, but
// code that can't be preempted does. Run with GODEBUG=asyncpreemptoff=1
// for the loop below to be such code.
func TestAThreadPastTheLimitLivesAtNormalPriority(t *testing.T) {
	rested(t)
	leave := enterOrSkip(t)
	defer leave()
	var l unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_RTTIME, &l); err != nil {
		t.Fatal(err)
	}
	if l.Max == unix.RLIM_INFINITY {
		t.Skip("this thread has no limit on its run")
	}
	if !strings.Contains(os.Getenv("GODEBUG"), "asyncpreemptoff=1") {
		// The system's word alone, as it would come.
		if err := unix.Kill(os.Getpid(), unix.SIGXCPU); err != nil {
			t.Fatal(err)
		}
	} else {
		sink := 0
		for i := range 4_000_000_000 {
			sink += i
		}
		spin = sink
	}
	end := time.Now().Add(time.Second)
	for policy(t) != unix.SCHED_NORMAL && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if p := policy(t); p != unix.SCHED_NORMAL {
		t.Errorf("after the limit the policy is %d", p)
	}
}

// spin keeps the loop above from being taken out.
var spin int
