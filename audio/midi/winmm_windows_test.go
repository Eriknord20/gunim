//go:build windows

package midi

import (
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// sink is a device of the test's own: it notes each message, and when
// it came.
type sink struct {
	mu  sync.Mutex
	got []arrival
}

type arrival struct {
	Message
	at time.Time
}

func (k *sink) write(m Message) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.got = append(k.got, arrival{m, time.Now()})
	return nil
}

func (k *sink) arrived() []arrival {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.got)
}

// fakeOut is an Out whose messages go to a sink, through the thread
// that sends what was sent ahead.
func fakeOut(t *testing.T) (*Out, *sink) {
	t.Helper()
	k := &sink{got: make([]arrival, 0, 1<<16)}
	o := &Out{name: "fake"}
	if err := o.sys.start(o, k.write); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o, k
}

func TestMessagesSentAheadLeaveAtTheirTime(t *testing.T) {
	o, k := fakeOut(t)
	start := time.Now().Add(30 * time.Millisecond)
	ats := make([]time.Time, 0, 6)
	for _, n := range []int{3, 0, 4, 1, 2, 2} {
		at := start.Add(time.Duration(n) * 7 * time.Millisecond)
		if err := o.Send(at, NoteOn(0, 60+n, 100)); err != nil {
			t.Fatal(err)
		}
		ats = append(ats, at)
	}
	slices.SortFunc(ats, func(a, b time.Time) int { return a.Compare(b) })
	for deadline := time.Now().Add(2 * time.Second); len(k.arrived()) < len(ats) && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	got := k.arrived()
	if len(got) != len(ats) {
		t.Fatalf("%d messages left, want %d", len(got), len(ats))
	}
	for i, a := range got {
		if i > 0 && a.Key() < got[i-1].Key() {
			t.Errorf("key %d left after key %d", a.Key(), got[i-1].Key())
		}
		late := a.at.Sub(ats[i])
		t.Logf("key %d left %v after its time", a.Key(), late)
		// Never early; the bound is wide for a busy CI machine.
		if late < 0 || late > 100*time.Millisecond {
			t.Errorf("key %d left %v after its time, want 0 to 100 ms", a.Key(), late)
		}
	}
}

func TestAMessageDueGoesAtOnceAndCancelLetsGoOfTheRest(t *testing.T) {
	o, k := fakeOut(t)
	_ = o.Send(time.Now().Add(time.Hour), NoteOn(0, 1, 1))
	if err := o.Send(time.Time{}, Clock()); err != nil {
		t.Fatal(err)
	}
	if got := k.arrived(); len(got) != 1 || got[0].Kind() != KindClock {
		t.Fatalf("got %v, want the Clock sent before Send returned", got)
	}
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	if _, ok := o.sys.sched.next(); ok {
		t.Error("a message waits after Cancel")
	}
}

func TestSendingMakesNothing(t *testing.T) {
	o, _ := fakeOut(t)
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	at := time.Now().Add(time.Hour)
	k := 0
	if n := testing.AllocsPerRun(500, func() {
		_ = o.Send(at, NoteOn(0, k%128, 1))
		_ = o.Send(time.Time{}, Clock())
		k++
		if k%100 == 0 {
			_ = o.Cancel()
		}
	}); n != 0 {
		t.Errorf("Send and Cancel made %v things a run, want none", n)
	}
}

// TestTheSystemsPortsList lists the ports, and plays a note on
// Windows' own synth where it is there.
func TestTheSystemsPortsList(t *testing.T) {
	outs, err := Outputs()
	if err != nil {
		t.Fatal(err)
	}
	ins, err := Inputs()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("outputs %q, inputs %q", outs, ins)
	const synth = "Microsoft GS Wavetable Synth"
	if !slices.Contains(outs, synth) {
		t.Skipf("no %s", synth)
	}
	o, err := OpenOut(synth)
	if err != nil {
		// As on a CI machine with no sound device.
		t.Skipf("the synth is listed, and opens on a machine with sound: %v", err)
	}
	defer func() { _ = o.Close() }()
	if err := o.Send(time.Time{}, NoteOn(0, 60, 1)); err != nil {
		t.Error(err)
	}
	if err := o.Send(time.Now().Add(20*time.Millisecond), NoteOff(0, 60, 0)); err != nil {
		t.Error(err)
	}
	if err := o.Send(time.Time{}, Sysex([]byte{0x7E, 0x7F, 0x09, 0x01})); err != nil {
		t.Error(err)
	}
	time.Sleep(50 * time.Millisecond)
}
