package plug

import (
	"math"
	"sync/atomic"
)

// Setup is how the host will run a processor.
type Setup struct {
	// Rate is frames a second.
	Rate float64
	// MaxBlock is the most frames a block will have.
	MaxBlock int
	// Channels is how many channels come out, and go in where sound
	// goes in: an instrument's sound comes out of it alone. An
	// [AudioPort] of zero channels has as many.
	Channels int
	// Offline says the sound is being rendered, as for an export, where
	// a block may take longer than it lasts.
	Offline bool
}

// Processor is the part of a plugin that makes or changes the sound. Prepare
// and Reset run with no block in progress; Process runs on the audio
// thread, and must not allocate, lock or wait. Latency and Tail run on
// the host's own goroutine, while blocks run, as the VST3 host asks
// Latency. A processor answers them from values safe to read there: set
// in Prepare, or stored atomically, as a [Meter] is.
type Processor interface {
	// Prepare readies the processor to run as s says. The first block
	// after it carries every parameter's value as a change at frame 0.
	Prepare(s Setup)
	// Process changes a block of sound, or makes one, as an instrument.
	Process(b *Block)
	// Latency is how many frames late the sound comes out; the host
	// moves the rest of the sound to match.
	Latency() int
	// Tail is how many frames of sound come out after the sound going
	// in stops, as a reverb's ringing.
	Tail() int
	// Reset drops the sound held from before, as playing jumps to
	// another place. An instrument ends every note, lifts every group's
	// pedal, returns each expression to its rest and forgets the notes
	// told of in [Block.Upcoming]; the host then starts the notes and
	// puts down the pedals that sound at the new place, and tells of
	// the notes coming within the look-ahead there.
	Reset()
}

// Reporter is a processor that reports values as it plays, its
// ReadOnly parameters, as a limiter reports how far it turns the sound
// down. Report runs on the window's goroutine as the processor runs on
// its own, so it reads what Process last stored, as a [Meter] keeps it.
type Reporter interface {
	Report(id uint32) float64
}

// A Meter is a value a processor stores as it plays and its views read
// as they draw, without either waiting for the other: a ReadOnly
// parameter's, for [Reporter]. Its zero value is 0.
type Meter struct{ bits atomic.Uint64 }

// Set stores v, as Process does.
func (m *Meter) Set(v float64) { m.bits.Store(math.Float64bits(v)) }

// Get returns the value last stored.
func (m *Meter) Get() float64 { return math.Float64frombits(m.bits.Load()) }

// Block is a block of sound to process.
//
// The ports beyond the main sound, as the [Descriptor] lists them, come
// by their place in its lists, each with Frames frames. A port the host
// leaves unconnected is nil, or past the end of its list, so a host
// that knows no ports runs every plugin: the plugin reads an input as
// its own sound would have it (a compressor keys on what it compresses,
// a control input stands at its Default), and skips an output. The
// connections may change from one block to the next. Read the ports
// with [Block.AuxInput] and its kin, which return nil for one
// unconnected.
type Block struct {
	// In holds each channel's frames, and Out the channels to write.
	// Out may be In, to process in place. An instrument's In is nil,
	// and it writes every frame of Out.
	In, Out [][]float32
	// AuxIn holds each aux input's channels, by [Descriptor.AuxIn].
	AuxIn [][][]float32
	// AuxOut holds each aux output's channels to write, by
	// [Descriptor.AuxOut]. The plugin writes every frame of each one
	// connected.
	AuxOut [][][]float32
	// ControlIn holds each control input's values, a frame each, by
	// [Descriptor.ControlIn], as the port's Min and Max bound them.
	ControlIn [][]float32
	// ControlOut holds each control output's values to write, by
	// [Descriptor.ControlOut]. The plugin writes every frame of each one
	// connected, within the port's Min and Max.
	ControlOut [][]float32
	// Frames is how many frames each channel has.
	Frames int
	// Changes are the parameters changed during the block, in the order
	// of their frames: the user's, the host's automation, and the
	// control signals the host drives parameters with, as
	// [AppendControl] makes them.
	Changes []Change
	// Events are the notes and their expression during the block, in
	// the order of their frames, for a plugin that takes notes.
	Events []Event
	// Upcoming is news of notes that start in a later block, in the
	// order the host sends it, for a plugin with a
	// [Descriptor.LookAhead]. It holds as the block starts.
	Upcoming []Upcoming
	// Position is where the block starts.
	Position Position
}

// AuxInput returns aux input i's channels, or nil where it is
// unconnected.
func (b *Block) AuxInput(i int) [][]float32 { return port(b.AuxIn, i) }

// AuxOutput returns aux output i's channels to write, or nil where it is
// unconnected.
func (b *Block) AuxOutput(i int) [][]float32 { return port(b.AuxOut, i) }

// ControlInput returns control input i's values, or nil where it is
// unconnected.
func (b *Block) ControlInput(i int) []float32 { return port(b.ControlIn, i) }

// ControlOutput returns control output i's values to write, or nil where
// it is unconnected.
func (b *Block) ControlOutput(i int) []float32 { return port(b.ControlOut, i) }

// port returns ports[i], or nil where i is past the end or the port has
// nothing in it.
func port[T any](ports [][]T, i int) []T {
	if i < 0 || i >= len(ports) || len(ports[i]) == 0 {
		return nil
	}
	return ports[i]
}

// Change is a parameter's new value, from a frame in a block on.
//
// A control signal reaches a parameter as changes too: the host takes
// the signal's value every few frames, with [AppendControl], and sends
// each as a change. So every plugin's parameters take control signals,
// through the code that takes their changes already, as a VST3 plugin
// takes sample-accurate automation as points at frames in its block.
// The host picks how often, and so bounds the work a plugin does per
// change, as working out a filter again. A plugin that needs a value
// every frame, as an FM synth's pitch, declares a control input for it.
type Change struct {
	Frame int
	ID    uint32
	Value float64
}

// ControlStep is how many frames apart a host takes a control signal's
// value for a parameter, by default: under a
// millisecond at 48 kHz, so an LFO moves a parameter smoothly, and 16
// changes in a block of 512 frames.
const ControlStep = 32

// AppendControl appends to dst the changes that drive parameter p by a
// control signal over a block, and returns the longer slice. values
// holds p's value at each of the block's frames, in p's own units, as
// the host maps the signal into them. It takes the value at frame 0 and
// every step frames after, within p's range, and leaves out a value the
// same as the one before. It allocates nothing where dst has room: a
// host makes dst room enough before the sound starts, a change for every
// step frames of its longest block.
//
// A host sending changes of several parameters in one block sorts them
// by frame after, as with slices.SortStableFunc, which allocates nothing.
func AppendControl(dst []Change, p Param, values []float32, step int) []Change {
	step = max(step, 1)
	last := math.NaN()
	for f := 0; f < len(values); f += step {
		v := p.Clamp(float64(values[f]))
		if v == last {
			continue
		}
		last = v
		dst = append(dst, Change{Frame: f, ID: p.ID, Value: v})
	}
	return dst
}

// Event is a note starting or ending, or changing as it sounds, at a
// frame in a block.
type Event struct {
	// Frame is where in the block it happens.
	Frame int
	Kind  EventKind
	// Note is the note's ID, the note's alone from its NoteOn to its
	// NoteOff, and from the news of it in [Block.Upcoming] on, where
	// the host sends some.
	Note int32
	// Group is the lane the note plays in: a Pedal holds only the notes
	// of its own group.
	Group int32
	// Pitch is a NoteOn's MIDI note number, as 60 for middle C. It may
	// fall between two, as 60.5.
	Pitch float64
	// Velocity is how hard a NoteOn strikes, or a NoteOff lets go, from
	// 0 to 1.
	Velocity float64
	// Expr is which expression an Expression changes.
	Expr ExprKind
	// Value is an Expression's new value, as Expr counts it, or a
	// Pedal's, from 0, up, to 1, down, with half pedal between.
	Value float64
}

// EventKind is what an [Event] does.
type EventKind uint8

// The kinds of event.
const (
	// NoteOn starts a note, at Pitch and Velocity.
	NoteOn EventKind = iota
	// NoteOff lets a note go, as a key lifted: it may ring on, held by
	// a Pedal or in its own release.
	NoteOff
	// Expression changes one sounding note's Expr to Value, from its
	// frame on. One at a NoteOn's frame, after it, sets where the note
	// starts.
	Expression
	// Pedal moves the sustain pedal of Group to Value. While it is down,
	// the group's notes let go ring on until it lifts.
	Pedal
)

// ExprKind is an expression a note changes by as it sounds.
type ExprKind uint8

// The expressions.
const (
	// Bend moves the note's pitch, in semitones up, or down below 0.
	Bend ExprKind = iota
	// Pressure is how hard the key is pressed as it is held, from 0 to 1.
	Pressure
	// Mod is the modulation, as a mod wheel's, from 0 to 1.
	Mod
)

// Position is where a block falls in the music.
type Position struct {
	// Frame is the block's first frame, counted from the start of the
	// track.
	Frame int64
	// Playing is set while the sound plays, and clear as it renders.
	Playing bool
	// Tempo is beats a minute, or zero where none is known.
	Tempo float64
}

// Upcoming is news of a note the host starts in a later block, for a
// plugin that sounds ahead of its notes, as its [Descriptor.LookAhead]
// asks.
//
// The host tells of each note it plays from the music once, with
// Coming, as the note comes within the look-ahead, and with Moved or
// Removed as an edit changes it. The NoteOn that starts it, in the
// block it lands in, carries the same Note. As playing starts or jumps,
// as a loop runs on from its start, and after a Reset, the host tells
// of the notes coming within the look-ahead from there. A note started
// as it happens, as a key played live, comes unannounced, as does one
// landing in the block where playing starts or jumps.
type Upcoming struct {
	Kind UpcomingKind
	// Frame is where the note lands, counted from the block's start:
	// Frames or more, in a later block, where its NoteOn comes. A
	// Removed's is 0.
	Frame int
	// Note is the note's ID, which its NoteOn carries.
	Note int32
	// Group is the lane it plays in, as an [Event]'s.
	Group int32
	// Pitch and Velocity are its NoteOn's.
	Pitch    float64
	Velocity float64
}

// UpcomingKind is what an [Upcoming] tells.
type UpcomingKind uint8

// The kinds of news.
const (
	// Coming tells of a note that lands at Frame, at Pitch and
	// Velocity.
	Coming UpcomingKind = iota
	// Moved tells that a note told of lands at Frame now, at Pitch and
	// Velocity, as an edit moved or changed it.
	Moved
	// Removed takes back a note told of, as an edit took it out of the
	// music: its NoteOn is called off. A plugin passes over a Removed
	// for a note it never heard of: a host that took back its own news
	// late sends one to be sure.
	Removed
)
