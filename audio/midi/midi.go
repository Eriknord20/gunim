// Package midi sends and receives MIDI 1.0 messages through the
// computer's MIDI ports: notes out to a hardware synth, with the MIDI
// clock if it follows one, and notes in from a keyboard.
//
// [Outputs] and [Inputs] list the ports by name, and [OpenOut] and
// [OpenIn] open one by its name. [Changed] tells as ports come and go,
// as a keyboard is plugged in or out.
//
// # Time
//
// Times are [time.Time] values on Go's monotonic clock, the one
// [time.Now] reads. [Out.Send] takes the time a message is to leave,
// and a message is held until then, so a host sends a block's notes as
// it renders the block, each at the time its frame is heard. A host
// that reads time.Now as it starts rendering a block, and plays its
// output a latency later, sends a note at frame f of the block at
//
//	start.Add(latency + time.Duration(f)*time.Second/time.Duration(rate))
//
// An [Event] from [OpenIn] carries the time its message arrived, on the
// same clock, so a host places it the same way back.
//
// # Systems
//
// On Linux the package speaks to the ALSA sequencer through libasound,
// loaded as the program runs. A message sent ahead waits in a queue of
// the sequencer's own, in the kernel, and leaves at its time to within
// a millisecond. Messages that arrive are stamped by the same queue.
//
// On Windows it speaks WinMM, the MIDI API every Windows has. Windows
// MIDI Services, the newer API, asks for its SDK runtime installed
// beside the program; where the service runs, WinMM reaches the ports
// through it, and several programs can share a port. WinMM sends a
// message at once, so the package holds messages sent ahead itself, and
// sends each at its time from a thread on a high-resolution timer, to
// within a millisecond. WinMM stamps messages that arrive to the
// millisecond. Its port names are 31 characters at most.
//
// Elsewhere [Outputs] and [Inputs] return [ErrUnsupported].
package midi

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrUnsupported is returned on a system the package has no MIDI
	// for.
	ErrUnsupported = errors.New("midi: MIDI plays on Linux and Windows")
	// ErrNotFound is returned by OpenOut and OpenIn for a name that
	// names no port.
	ErrNotFound = errors.New("midi: no port of that name")
	// ErrGone is returned by a port whose device went away, as when a
	// keyboard's cable is pulled. Open it again by its name once it is
	// back.
	ErrGone = errors.New("midi: the port went away")
	// ErrClosed is returned by a port after Close.
	ErrClosed = errors.New("midi: the port is closed")
	// ErrFull is returned by Out.Send while as many messages wait to
	// leave as the system holds.
	ErrFull = errors.New("midi: too many messages wait to leave")
)

// Outputs returns the names of the ports messages can be sent to, as
// hardware synths and other programs' inputs. Two ports of one name
// are told apart by a number after the second's, " 2".
func Outputs() ([]string, error) { return names(true) }

// Inputs returns the names of the ports messages come from, as
// keyboards and other programs' outputs. Two ports of one name are told
// apart by a number after the second's, " 2".
func Inputs() ([]string, error) { return names(false) }

// A sysPort is a port as the system lists it.
type sysPort struct {
	name string
	addr portAddr
}

func names(out bool) ([]string, error) {
	ps, err := listPorts(out)
	if err != nil {
		return nil, err
	}
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = p.name
	}
	return s, nil
}

// listPorts lists the system's ports, each with a name of its own.
func listPorts(out bool) ([]sysPort, error) {
	ps, err := sysList(out)
	if err != nil {
		return nil, err
	}
	unique(ps)
	return ps, nil
}

// unique numbers the second and later ports of one name.
func unique(ps []sysPort) {
	seen := make(map[string]int, len(ps))
	taken := make(map[string]bool, len(ps))
	for _, p := range ps {
		taken[p.name] = true
	}
	for i, p := range ps {
		seen[p.name]++
		if seen[p.name] == 1 {
			continue
		}
		for n := seen[p.name]; ; n++ {
			name := p.name + " " + strconv.Itoa(n)
			if !taken[name] {
				ps[i].name, taken[name] = name, true
				break
			}
		}
	}
}

func find(out bool, name string) (sysPort, error) {
	ps, err := listPorts(out)
	if err != nil {
		return sysPort{}, err
	}
	for _, p := range ps {
		if p.name == name {
			return p, nil
		}
	}
	return sysPort{}, fmt.Errorf("%w: %q", ErrNotFound, name)
}

// changes closes a channel as ports come or go.
var changes struct {
	mu sync.Mutex
	ch chan struct{}
}

// Changed returns a channel that is closed as ports next come or go,
// or change their names. Call it again for the change after that. A
// port open as its device goes away returns [ErrGone] from then on.
func Changed() <-chan struct{} {
	sysWatch()
	changes.mu.Lock()
	defer changes.mu.Unlock()
	if changes.ch == nil {
		changes.ch = make(chan struct{})
	}
	return changes.ch
}

// announce closes the channel Changed returned.
func announce() {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	if changes.ch != nil {
		close(changes.ch)
		changes.ch = nil
	}
}

// open holds the ports open, to tell them when they go away.
var open struct {
	mu   sync.Mutex
	outs map[*Out]struct{}
	ins  map[*In]struct{}
}

// goneWhere calls f with each open port's address, and whether it is
// an output, and marks the port gone where f says so.
func goneWhere(f func(a portAddr, out bool) bool) {
	open.mu.Lock()
	var outs []*Out
	var ins []*In
	for o := range open.outs {
		if f(o.addr, true) {
			outs = append(outs, o)
		}
	}
	for i := range open.ins {
		if f(i.addr, false) {
			ins = append(ins, i)
		}
	}
	open.mu.Unlock()
	for _, o := range outs {
		o.gone()
	}
	for _, i := range ins {
		i.gone()
	}
}

// An Out is an output port, open to send messages to.
type Out struct {
	name  string
	addr  portAddr
	state atomic.Pointer[error] // ErrGone or ErrClosed once either is so
	notes sounding
	sys   outSys
}

// OpenOut opens the output called name, one [Outputs] lists.
func OpenOut(name string) (*Out, error) {
	p, err := find(true, name)
	if err != nil {
		return nil, err
	}
	o := &Out{name: name, addr: p.addr}
	if err := o.sys.open(o); err != nil {
		return nil, fmt.Errorf("midi: opening %q: %w", name, err)
	}
	open.mu.Lock()
	if open.outs == nil {
		open.outs = make(map[*Out]struct{})
	}
	open.outs[o] = struct{}{}
	open.mu.Unlock()
	sysWatch()
	return o, nil
}

// Name returns the name o was opened by.
func (o *Out) Name() string { return o.name }

// Err returns [ErrGone] once o's device went away, and [ErrClosed]
// once o is closed.
func (o *Out) Err() error {
	if e := o.state.Load(); e != nil {
		return *e
	}
	return nil
}

// Send sends m to leave at time at, on the clock [time.Now] reads. A
// time already past, or the zero time, sends m at once. Messages sent
// for one time leave in the order they were sent.
//
// Send is quick, and makes nothing new for a channel or system
// message, so a host can call it from the goroutine that renders its
// sound. On Linux a sysex is handed whole to the sequencer. On Windows
// a sysex due now is sent before Send returns, and one sent ahead is
// copied to wait.
func (o *Out) Send(at time.Time, m Message) error {
	if e := o.state.Load(); e != nil {
		return *e
	}
	if m.Len() == 0 {
		return fmt.Errorf("midi: a message of status %#x is undefined", m.Status)
	}
	if err := o.sys.send(o, at, m); err != nil {
		return err
	}
	o.notes.sent(sinceEpoch(at), sinceEpoch(time.Now()), m)
	return nil
}

// Cancel lets go of the messages sent ahead that have yet to leave,
// NoteOffs among them. The notes already sounding stay on, so a host
// that stops follows Cancel with [Out.NotesOff].
func (o *Out) Cancel() error {
	if e := o.state.Load(); e != nil {
		return *e
	}
	return o.sys.cancel(o)
}

// NotesOff lets go at once of every note o may have left sounding: it
// sends a NoteOff for each note struck whose NoteOff has yet to leave,
// and lifts the sustain pedal, [Control] 64 to 0, on each channel that
// played a note or moved the pedal. A host that stops calls
// [Out.Cancel], then NotesOff. It makes nothing new.
func (o *Out) NotesOff() error {
	if e := o.state.Load(); e != nil {
		return *e
	}
	return o.endNotes()
}

func (o *Out) endNotes() error {
	var err error
	keep := func(e error) {
		if err == nil {
			err = e
		}
	}
	o.notes.end(sinceEpoch(time.Now()),
		func(ch, key int) { keep(o.sys.send(o, time.Time{}, NoteOff(ch, key, 0))) },
		func(ch int) { keep(o.sys.send(o, time.Time{}, Control(ch, 64, 0))) })
	return err
}

// Close lets go of messages yet to leave, lets go of the notes left
// sounding as [Out.NotesOff] does, and closes o.
func (o *Out) Close() error {
	e := o.state.Swap(&ErrClosed)
	if e != nil && errors.Is(*e, ErrClosed) {
		return nil
	}
	open.mu.Lock()
	delete(open.outs, o)
	open.mu.Unlock()
	var err error
	if e == nil {
		err = errors.Join(o.sys.cancel(o), o.endNotes())
	}
	return errors.Join(err, o.sys.close(o))
}

func (o *Out) gone() {
	if o.state.CompareAndSwap(nil, &ErrGone) {
		o.sys.gone(o)
	}
}

// An Event is a message that came in, and when.
type Event struct {
	// Time is when the message arrived, on the clock [time.Now] reads.
	Time time.Time
	Message
}

// An In is an input port, open to messages that come in.
type In struct {
	name  string
	addr  portAddr
	f     func(Event)
	state atomic.Pointer[error]
	mu    sync.Mutex // held while f runs
	sys   inSys
}

// OpenIn opens the input called name, one [Inputs] lists, and calls f
// with each message that comes in, in order, on a goroutine of the
// package's own. f must return quickly, as a host's audio wants the
// next one: it hands a note to its synth and returns. A sysex's bytes
// hold until f returns.
func OpenIn(name string, f func(Event)) (*In, error) {
	p, err := find(false, name)
	if err != nil {
		return nil, err
	}
	i := &In{name: name, addr: p.addr, f: f}
	if err := i.sys.open(i); err != nil {
		return nil, fmt.Errorf("midi: opening %q: %w", name, err)
	}
	open.mu.Lock()
	if open.ins == nil {
		open.ins = make(map[*In]struct{})
	}
	open.ins[i] = struct{}{}
	open.mu.Unlock()
	sysWatch()
	return i, nil
}

// Name returns the name i was opened by.
func (i *In) Name() string { return i.name }

// Err returns [ErrGone] once i's device went away, and [ErrClosed]
// once i is closed.
func (i *In) Err() error {
	if e := i.state.Load(); e != nil {
		return *e
	}
	return nil
}

// Close closes i. It returns once f has returned for the last time, so
// it is called from elsewhere than f.
func (i *In) Close() error {
	if e := i.state.Swap(&ErrClosed); e != nil && errors.Is(*e, ErrClosed) {
		return nil
	}
	open.mu.Lock()
	delete(open.ins, i)
	open.mu.Unlock()
	err := i.sys.close(i)
	i.mu.Lock()
	defer i.mu.Unlock()
	return err
}

func (i *In) gone() {
	if i.state.CompareAndSwap(nil, &ErrGone) {
		i.sys.gone(i)
	}
}

// deliver hands e to f, while i is open.
func (i *In) deliver(e Event) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state.Load() != nil {
		return
	}
	i.f(e)
}
