//go:build darwin && !ios

package midi

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"
)

var (
	midiSourceCreate      func(client uint32, name uintptr, out *uint32) int32
	midiDestinationCreate func(client uint32, name uintptr, read, refCon uintptr, out *uint32) int32
	midiReceived          func(src uint32, list uintptr) int32
	midiEndpointDispose   func(e uint32) int32

	// forward is a virtual destination's MIDIReadProc: it passes what
	// comes in on to the source its refCon names.
	forward  uintptr
	testOnce sync.Once
)

// needCoreMIDI skips a test where the MIDI server is out of reach.
func needCoreMIDI(t *testing.T) {
	t.Helper()
	if err := startClient(); err != nil {
		t.Skipf("no CoreMIDI: %v", err)
	}
	testOnce.Do(func() {
		cm, err := purego.Dlopen("/System/Library/Frameworks/CoreMIDI.framework/CoreMIDI", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			t.Fatal(err)
		}
		purego.RegisterLibFunc(&midiSourceCreate, cm, "MIDISourceCreate")
		purego.RegisterLibFunc(&midiDestinationCreate, cm, "MIDIDestinationCreate")
		purego.RegisterLibFunc(&midiReceived, cm, "MIDIReceived")
		purego.RegisterLibFunc(&midiEndpointDispose, cm, "MIDIEndpointDispose")
		forward = purego.NewCallback(func(list, refCon, _ uintptr) { _ = midiReceived(uint32(refCon), list) })
	})
}

// virtual makes a virtual source of a name of the test's own, and a
// virtual destination of that name that passes what it is sent on to
// the source. It waits for both to be listed.
func virtual(t *testing.T) (name string, src uint32) {
	t.Helper()
	needCoreMIDI(t)
	name = fmt.Sprintf("gunim %s %d", t.Name(), os.Getpid())
	cf := cfStringCreate(0, name, cfStringEncodingUTF8)
	defer cfRelease(cf)
	var dst uint32
	if err := osErr("MIDISourceCreate", midiSourceCreate(client.ref, cf, &src)); err != nil {
		t.Skipf("no virtual ports: %v", err)
	}
	if err := osErr("MIDIDestinationCreate", midiDestinationCreate(client.ref, cf, forward, uintptr(src), &dst)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = midiEndpointDispose(dst)
		_ = midiEndpointDispose(src)
	})
	waitListed(t, name, true)
	return name, src
}

// waitListed waits until Inputs and Outputs both list name, or both
// do not.
func waitListed(t *testing.T, name string, listed bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ins, _ := Inputs()
		outs, _ := Outputs()
		if slices.Contains(ins, name) == listed && slices.Contains(outs, name) == listed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listed %v not reached for %q; inputs %q, outputs %q", listed, name, ins, outs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// loop opens an Out and an In on a pair of virtual ports, so what the
// Out sends comes straight back to the In.
func loop(t *testing.T, f func(Event)) (*Out, *In, uint32) {
	t.Helper()
	name, src := virtual(t)
	i, err := OpenIn(name, f)
	if err != nil {
		t.Fatal(err)
	}
	o, err := OpenOut(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = o.Close()
		time.Sleep(30 * time.Millisecond)
		_ = i.Close()
	})
	return o, i, src
}

// arrival is an event, and when the In's f was called with it.
type arrival struct {
	Event
	at time.Time
}

func collect(n int) (f func(Event), got chan arrival) {
	ch := make(chan arrival, n)
	return func(e Event) {
		if e.Sysex != nil {
			e.Sysex = append([]byte(nil), e.Sysex...)
		}
		select {
		case ch <- arrival{e, time.Now()}:
		default:
		}
	}, ch
}

func next(t *testing.T, ch chan arrival, within time.Duration) arrival {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(within):
		t.Fatalf("nothing came in %v", within)
		return arrival{}
	}
}

func TestMessagesSentAheadLeaveAtTheirTime(t *testing.T) {
	f, got := collect(100)
	o, _, _ := loop(t, f)
	start := time.Now().Add(50 * time.Millisecond)
	ats := make([]time.Time, 0, 5)
	// Sent out of order: they leave in time order.
	for _, k := range []int{3, 0, 4, 1, 2} {
		at := start.Add(time.Duration(k) * 7 * time.Millisecond)
		if err := o.Send(at, NoteOn(0, 60+k, 100)); err != nil {
			t.Fatal(err)
		}
		ats = append(ats, at)
	}
	slices.SortFunc(ats, func(a, b time.Time) int { return a.Compare(b) })
	for k, at := range ats {
		a := next(t, got, time.Second)
		if a.Key() != 60+k {
			t.Fatalf("message %d is key %d, want %d", k, a.Key(), 60+k)
		}
		// Never early: the bound is wide for a busy machine, and
		// catches a time base gone wrong.
		if late := a.at.Sub(at); late < -time.Millisecond || late > 100*time.Millisecond {
			t.Errorf("key %d came %v after its time, want 0 to 100 ms", a.Key(), late)
		}
		if d := a.Time.Sub(at).Abs(); d > 20*time.Millisecond {
			t.Errorf("key %d stamped %v from its time, want within 20 ms", a.Key(), d)
		}
	}
}

func TestEveryKindOfMessageComesBackAsSent(t *testing.T) {
	f, got := collect(100)
	o, _, _ := loop(t, f)
	long := make([]byte, maxPacket*2+100)
	for k := range long {
		long[k] = byte(k % 128)
	}
	sent := []Message{
		NoteOn(1, 60, 100), NoteOff(1, 60, 64), Control(2, 7, 99), Program(3, 5),
		Bend(4, 1000), Pressure(5, 40), KeyPressure(6, 61, 30), SongPosition(17), Clock(), Start(), Stop(),
		Sysex([]byte{0x7E, 0x7F, 0x06, 0x01}),
		Sysex(long),
		NoteOn(0, 61, 1),
	}
	for _, m := range sent {
		if err := o.Send(time.Time{}, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range sent {
		a := next(t, got, time.Second)
		if a.Status != want.Status || a.Data1 != want.Data1 || a.Data2 != want.Data2 || !bytes.Equal(a.Sysex, want.Sysex) {
			t.Fatalf("got %v (%d sysex bytes), want %v (%d)", a.Message, len(a.Sysex), want, len(want.Sysex))
		}
	}
}

func TestSendMakesNothingNew(t *testing.T) {
	o, _, _ := loop(t, func(Event) {})
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts; sent an hour ahead,
	// so nothing comes in.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	at := time.Now().Add(time.Hour)
	ms := []Message{NoteOn(0, 60, 1), Bend(1, 100), Control(2, 7, 9), Clock(), SongPosition(4), Sysex([]byte{1, 2, 3})}
	_ = o.Send(at, ms[5])
	k := 0
	if n := testing.AllocsPerRun(500, func() {
		_ = o.Send(at, ms[k%len(ms)])
		k++
	}); n != 0 {
		t.Errorf("Send made %v things a run, want none", n)
	}
	_ = o.Cancel()
}

func TestCancelLetsGoOfWhatWaits(t *testing.T) {
	f, got := collect(100)
	o, _, _ := loop(t, f)
	if err := o.Send(time.Now().Add(150*time.Millisecond), NoteOn(0, 60, 100)); err != nil {
		t.Fatal(err)
	}
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(300 * time.Millisecond)
	for {
		select {
		case a := <-got:
			// CoreMIDI tells a virtual destination of a flush with a
			// lone 0xFF, which the loop passes on.
			if a.Status != 0xFF {
				t.Fatalf("%v came after Cancel", a.Message)
			}
		case <-timeout:
			return
		}
	}
}

func TestAnInputGoesAsItsSourceGoes(t *testing.T) {
	needCoreMIDI(t)
	name := fmt.Sprintf("gunim gone %d", os.Getpid())
	cf := cfStringCreate(0, name, cfStringEncodingUTF8)
	defer cfRelease(cf)
	var src uint32
	if err := osErr("MIDISourceCreate", midiSourceCreate(client.ref, cf, &src)); err != nil {
		t.Skipf("no virtual ports: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if ins, _ := Inputs(); slices.Contains(ins, name) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never listed", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
	i, err := OpenIn(name, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = i.Close() }()
	changed := Changed()
	if err := osErr("MIDIEndpointDispose", midiEndpointDispose(src)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("Changed never told the source went")
	}
	if err := i.Err(); !errors.Is(err, ErrGone) {
		t.Fatalf("Err = %v, want ErrGone", err)
	}
}

func TestTimesMatchTheClock(t *testing.T) {
	needCoreMIDI(t)
	now := time.Now()
	if d := timeOf(hostAt(now.Add(time.Second))).Sub(now); d > 2*time.Millisecond {
		t.Errorf("a host time ahead reads %v from now, want now at the latest", d)
	}
	at := now.Add(500 * time.Millisecond)
	h := hostAt(at)
	back := clock.at.Add(time.Duration(nanos(h) - nanos(clock.host)))
	if d := back.Sub(at).Abs(); d > time.Microsecond {
		t.Errorf("a time goes to host time and back %v off", d)
	}
	if hostAt(now.Add(-time.Second)) != 0 || hostAt(time.Time{}) != 0 {
		t.Error("a time past is not sent at once")
	}
}
