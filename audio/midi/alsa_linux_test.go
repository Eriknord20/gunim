//go:build linux && !android

package midi

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ebitengine/purego"
)

// needSeq skips a test on a machine without the ALSA sequencer, as a CI
// runner.
func needSeq(t *testing.T) {
	t.Helper()
	if err := startQueue(); err != nil {
		t.Skipf("no ALSA sequencer: %v", err)
	}
}

// loop opens an Out and an In on the client's own port, so what the
// Out sends comes straight back to the In.
func loop(t *testing.T, f func(Event)) *Out {
	t.Helper()
	needSeq(t)
	own := portAddr{seq.client, seq.port}
	i := &In{name: "loop", addr: own, f: f}
	if err := i.sys.open(i); err != nil {
		t.Fatal(err)
	}
	o := &Out{name: "loop", addr: own}
	if err := o.sys.open(o); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeLoop(o, i) })
	return o
}

// closeLoop closes o, which lets go of its notes, and i once those
// have come in, so they reach no test after.
func closeLoop(o *Out, ins ...*In) {
	_ = o.Close()
	time.Sleep(30 * time.Millisecond)
	for _, i := range ins {
		_ = i.Close()
	}
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
	o := loop(t, f)
	start := time.Now().Add(30 * time.Millisecond)
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
		// Never early. The queue sends within a millisecond of the
		// time; the bound is wide for a busy machine, where the reader
		// may wait its turn, and catches a time base gone wrong.
		if late := a.at.Sub(at); late < 0 || late > 100*time.Millisecond {
			t.Errorf("key %d came %v after its time, want 0 to 100 ms", a.Key(), late)
		}
		// Straight from the queue, a message keeps the time it left at.
		if d := a.Time.Sub(at).Abs(); d > 100*time.Microsecond {
			t.Errorf("key %d stamped %v from its time, want within 100 µs", a.Key(), d)
		}
	}
}

func TestEveryKindOfMessageComesBackAsSent(t *testing.T) {
	f, got := collect(100)
	o := loop(t, f)
	big := make([]byte, 3*sysexChunk+17)
	for i := range big {
		big[i] = byte(i % 128)
	}
	for _, m := range kinds(big) {
		if err := o.Send(time.Time{}, m); err != nil {
			t.Fatalf("%v: %v", m, err)
		}
		a := next(t, got, time.Second)
		if !equal(a.Message, m) {
			t.Errorf("sent %v, got %v", m, a.Message)
		}
	}
}

func kinds(big []byte) []Message {
	return []Message{
		NoteOn(3, 60, 100), NoteOn(3, 60, 0), NoteOff(4, 61, 64), KeyPressure(5, 62, 33),
		Control(6, 64, 127), Program(7, 12), Pressure(8, 90), Bend(9, -8192), Bend(9, 8191),
		Bend(9, 0), Clock(), Start(), Continue(), Stop(), SongPosition(1234),
		{Status: 0xF3, Data1: 5}, {Status: 0xF1, Data1: 0x31}, {Status: 0xF6}, {Status: 0xFE},
		Sysex([]byte{0x7E, 0x7F, 0x06, 0x01}), Sysex(big),
	}
}

func equal(a, b Message) bool {
	return a.Status == b.Status && a.Data1 == b.Data1 && a.Data2 == b.Data2 && bytes.Equal(a.Sysex, b.Sysex)
}

func TestCancelLetsGoOfWhatWaitsAndLeavesWhatComesAfter(t *testing.T) {
	f, got := collect(100)
	o := loop(t, f)
	for k := range 10 {
		if err := o.Send(time.Now().Add(80*time.Millisecond), NoteOn(0, k, 1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := o.Send(time.Now().Add(100*time.Millisecond), Stop()); err != nil {
		t.Fatal(err)
	}
	if a := next(t, got, time.Second); a.Kind() != KindStop {
		t.Errorf("got %v first, want the Stop sent after Cancel", a.Message)
	}
}

func TestCancelLetsGoOfItsOwnOutsMessagesAlone(t *testing.T) {
	f, got := collect(100)
	a := loop(t, f)
	b := &Out{name: "loop", addr: a.addr}
	if err := b.sys.open(b); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	at := time.Now().Add(50 * time.Millisecond)
	_ = a.Send(at, NoteOn(0, 1, 1))
	_ = b.Send(at, NoteOn(0, 2, 1))
	if err := a.Cancel(); err != nil {
		t.Fatal(err)
	}
	if e := next(t, got, time.Second); e.Key() != 2 {
		t.Errorf("got key %d, want the other Out's key 2", e.Key())
	}
	select {
	case e := <-got:
		t.Errorf("got %v after Cancel", e.Message)
	case <-time.After(50 * time.Millisecond):
	}
}

// drain returns what comes in until nothing has for wait.
func drain(got chan arrival, wait time.Duration) []Message {
	var ms []Message
	for {
		select {
		case a := <-got:
			ms = append(ms, a.Message)
		case <-time.After(wait):
			return ms
		}
	}
}

func TestCloseLetsGoOfTheNotesLeftSounding(t *testing.T) {
	f, got := collect(100)
	o := loop(t, f)
	soon := time.Now().Add(300 * time.Millisecond)
	for _, s := range []struct {
		at time.Time
		m  Message
	}{
		{time.Time{}, NoteOn(0, 60, 100)},
		{soon, NoteOff(0, 60, 0)}, // waits, and Close lets it go
		{time.Time{}, NoteOn(1, 61, 100)},
		{time.Time{}, NoteOn(2, 62, 100)},
		{time.Time{}, NoteOn(2, 62, 0)}, // off already
		{time.Time{}, Control(3, 64, 127)},
		{soon, NoteOn(4, 63, 100)}, // waits, and Close lets it go
	} {
		if err := o.Send(s.at, s.m); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(drain(got, 50*time.Millisecond)); n != 5 {
		t.Fatalf("%d messages came before Close, want the 5 sent for now", n)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	want := []Message{NoteOff(0, 60, 0), NoteOff(1, 61, 0), NoteOff(4, 63, 0),
		Control(0, 64, 0), Control(1, 64, 0), Control(2, 64, 0), Control(3, 64, 0), Control(4, 64, 0)}
	// Past the time of the messages that waited, so one let through
	// would show.
	if ms := drain(got, 400*time.Millisecond); !slices.EqualFunc(ms, want, equal) {
		t.Errorf("Close sent %v\nwant %v", ms, want)
	}
}

func TestNotesOffAfterCancelEndsTheNotesOnce(t *testing.T) {
	f, got := collect(100)
	o := loop(t, f)
	_ = o.Send(time.Time{}, NoteOn(9, 36, 100))
	_ = o.Send(time.Now().Add(200*time.Millisecond), NoteOff(9, 36, 0))
	drain(got, 20*time.Millisecond)
	if err := o.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := o.NotesOff(); err != nil {
		t.Fatal(err)
	}
	want := []Message{NoteOff(9, 36, 0), Control(9, 64, 0)}
	if ms := drain(got, 300*time.Millisecond); !slices.EqualFunc(ms, want, equal) {
		t.Errorf("NotesOff sent %v, want %v", ms, want)
	}
	if err := o.NotesOff(); err != nil {
		t.Fatal(err)
	}
	if ms := drain(got, 30*time.Millisecond); len(ms) != 0 {
		t.Errorf("NotesOff again sent %v, want nothing", ms)
	}
}

func TestSendingMakesNothing(t *testing.T) {
	f, _ := collect(1)
	o := loop(t, f)
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts, so the reader
	// allocates as little as may be meanwhile; sent an hour ahead, so
	// nothing comes in to read.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	at := time.Now().Add(time.Hour)
	ms := []Message{NoteOn(0, 60, 1), Bend(1, 100), Control(2, 7, 9), Clock(), SongPosition(4), Sysex([]byte{1, 2, 3})}
	_ = o.Send(at, ms[5])
	k := 0
	if n := testing.AllocsPerRun(500, func() {
		_ = o.Send(at, ms[k%len(ms)])
		k++
		if k%100 == 0 {
			_ = o.Cancel()
			_ = o.NotesOff()
		}
	}); n != 0 {
		t.Errorf("Send, Cancel and NotesOff made %v things a run, want none", n)
	}
	_ = o.Cancel()
}

func TestAnInClosedHearsNothingMore(t *testing.T) {
	needSeq(t)
	var after atomic.Bool
	var calls atomic.Int64
	own := portAddr{seq.client, seq.port}
	i := &In{name: "loop", addr: own}
	i.f = func(Event) {
		calls.Add(1)
		if after.Load() {
			t.Error("f was called after Close returned")
		}
		time.Sleep(50 * time.Microsecond)
	}
	if err := i.sys.open(i); err != nil {
		t.Fatal(err)
	}
	f, got := collect(1000)
	other := &In{name: "loop", addr: own, f: f}
	if err := other.sys.open(other); err != nil {
		t.Fatal(err)
	}
	o := &Out{name: "loop", addr: own}
	if err := o.sys.open(o); err != nil {
		t.Fatal(err)
	}
	defer closeLoop(o, other)
	for k := range 100 {
		_ = o.Send(time.Time{}, NoteOn(0, k, 1))
	}
	for deadline := time.Now().Add(time.Second); calls.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	// Closed while messages are on their way to it.
	if err := i.Close(); err != nil {
		t.Fatal(err)
	}
	after.Store(true)
	for k := 100; k < 200; k++ {
		_ = o.Send(time.Time{}, NoteOn(0, k%128, 1))
	}
	// The second In on the same port hears on.
	for k := range 200 {
		if a := next(t, got, time.Second); a.Key() != k%128 {
			t.Fatalf("message %d to the In left open: key %d, want %d", k, a.Key(), k%128)
		}
	}
	if calls.Load() == 0 {
		t.Error("the In closed heard nothing before it closed")
	}
	if err := i.Close(); err != nil {
		t.Errorf("Close again: %v", err)
	}
}

// TestThroughMidiThrough sends through the kernel's Midi Through port,
// which sends back all it is sent: the path a hardware port takes.
func TestThroughMidiThrough(t *testing.T) {
	needSeq(t)
	const name = "Midi Through Port-0"
	outs, _ := Outputs()
	ins, _ := Inputs()
	if !slices.Contains(outs, name) || !slices.Contains(ins, name) {
		t.Skipf("no %s: outputs %q, inputs %q", name, outs, ins)
	}
	o, err := OpenOut(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	f, got := collect(100)
	i, err := OpenIn(name, f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = i.Close() }()
	for _, m := range kinds(make([]byte, 1000)) {
		sent := time.Now()
		at := sent.Add(5 * time.Millisecond)
		if err := o.Send(at, m); err != nil {
			t.Fatal(err)
		}
		a := next(t, got, time.Second)
		if !equal(a.Message, m) {
			t.Errorf("sent %v, got %v", m, a.Message)
		}
		// Stamped as it came in: after it left, and before f heard it,
		// give or take the error in measuring the queue against the
		// clock.
		const slack = 100 * time.Microsecond
		if a.Time.Before(at.Add(-slack)) || a.Time.After(a.at.Add(slack)) {
			t.Errorf("%v stamped %v after its time, and f called %v after; want the stamp between",
				m.Kind(), a.Time.Sub(at), a.at.Sub(at))
		}
	}
}

// testClient is a second client of the sequencer, with a port, as a
// keyboard plugged in.
type testClient struct{ h uintptr }

var (
	testSeqOpen       func(seq *uintptr, name string, streams, mode int32) int32
	testSeqClose      func(seq uintptr) int32
	testSeqCreatePort func(seq uintptr, name string, caps, typ uint32) int32
	testSeqClientName func(seq uintptr, name string) int32
)

func plugIn(t *testing.T, name string) *testClient {
	t.Helper()
	if testSeqOpen == nil {
		lib, err := purego.Dlopen("libasound.so.2", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			t.Skip(err)
		}
		purego.RegisterLibFunc(&testSeqOpen, lib, "snd_seq_open")
		purego.RegisterLibFunc(&testSeqClose, lib, "snd_seq_close")
		purego.RegisterLibFunc(&testSeqCreatePort, lib, "snd_seq_create_simple_port")
		purego.RegisterLibFunc(&testSeqClientName, lib, "snd_seq_set_client_name")
	}
	c := &testClient{}
	if r := testSeqOpen(&c.h, "default", openDuplex, 0); r < 0 {
		t.Fatalf("snd_seq_open: %d", r)
	}
	testSeqClientName(c.h, name)
	if r := testSeqCreatePort(c.h, name, capRead|capWrite|capSubsRead|capSubsWrite, typeMIDIGeneric); r < 0 {
		t.Fatalf("snd_seq_create_simple_port: %d", r)
	}
	return c
}

func (c *testClient) unplug() {
	if c.h != 0 {
		testSeqClose(c.h)
		c.h = 0
	}
}

func changedWithin(d time.Duration, ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func TestPortsComeAndGo(t *testing.T) {
	needSeq(t)
	name := fmt.Sprintf("gunim test port %d", os.Getpid())
	changed := Changed()
	c := plugIn(t, name)
	defer c.unplug()
	if !changedWithin(time.Second, changed) {
		t.Fatal("Changed stayed open as a port came")
	}
	outs, _ := Outputs()
	ins, _ := Inputs()
	// The port's name starts with its client's, so it stands alone.
	full := name
	if !slices.Contains(outs, full) || !slices.Contains(ins, full) {
		t.Fatalf("the port came, but is missing from outputs %q or inputs %q", outs, ins)
	}
	o, err := OpenOut(full)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	i, err := OpenIn(full, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = i.Close() }()
	// A note waits to leave as the port goes.
	if err := o.Send(time.Now().Add(time.Second), NoteOn(0, 60, 1)); err != nil {
		t.Fatal(err)
	}
	changed = Changed()
	c.unplug()
	if !changedWithin(time.Second, changed) {
		t.Fatal("Changed stayed open as a port went")
	}
	if !errors.Is(o.Err(), ErrGone) || !errors.Is(i.Err(), ErrGone) {
		t.Errorf("after the port went: Out.Err %v, In.Err %v; want ErrGone", o.Err(), i.Err())
	}
	if err := o.Send(time.Time{}, Clock()); !errors.Is(err, ErrGone) {
		t.Errorf("Send to a port gone: %v, want ErrGone", err)
	}
	if err := o.Close(); err != nil {
		t.Errorf("closing an Out gone: %v", err)
	}
	if err := i.Close(); err != nil {
		t.Errorf("closing an In gone: %v", err)
	}
	if !errors.Is(o.Err(), ErrClosed) {
		t.Errorf("Err after Close = %v, want ErrClosed", o.Err())
	}
	if outs, _ := Outputs(); slices.Contains(outs, full) {
		t.Errorf("the port went, and outputs still list it: %q", outs)
	}
	if _, err := OpenOut(full); !errors.Is(err, ErrNotFound) {
		t.Errorf("opening the port gone: %v, want ErrNotFound", err)
	}
}

func TestPortsOfOneNameAreNumbered(t *testing.T) {
	needSeq(t)
	name := fmt.Sprintf("gunim twin %d", os.Getpid())
	a, b := plugIn(t, name), plugIn(t, name)
	defer a.unplug()
	defer b.unplug()
	outs, _ := Outputs()
	var twins []string
	for _, s := range outs {
		if bytes.Contains([]byte(s), []byte(name)) {
			twins = append(twins, s)
		}
	}
	if len(twins) != 2 || twins[1] != twins[0]+" 2" {
		t.Errorf("two ports of one name listed as %q, want the second numbered 2", twins)
	}
}

// TestAPortIsNamedWithoutItsPadding: ALSA pads some names with spaces,
// and a port's name says its client's only where it does not already.
func TestAPortIsNamedWithoutItsPadding(t *testing.T) {
	for _, c := range []struct{ client, port, want string }{
		{"PipeWire-System", "input           ", "PipeWire-System: input"},
		{"Midi Through", "Midi Through Port-0", "Midi Through Port-0"},
		{"nanoKEY2 ", "nanoKEY2 MIDI 1", "nanoKEY2 MIDI 1"},
		{"Synth", "  ", "Synth"},
	} {
		if got := portName(c.client, c.port); got != c.want {
			t.Errorf("client %q, port %q is called %q, want %q", c.client, c.port, got, c.want)
		}
	}
}
