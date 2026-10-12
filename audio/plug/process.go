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
	// goes in: an instrument's sound comes out of it alone.
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
	// pedal and returns each expression to its rest; the host then
	// starts the notes and puts down the pedals that sound at the new
	// place.
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
type Block struct {
	// In holds each channel's frames, and Out the channels to write.
	// Out may be In, to process in place. An instrument's In is nil,
	// and it writes every frame of Out.
	In, Out [][]float32
	// Frames is how many frames each channel has.
	Frames int
	// Changes are the parameters changed during the block, in the order
	// of their frames.
	Changes []Change
	// Events are the notes and their expression during the block, in
	// the order of their frames, for a plugin that takes notes.
	Events []Event
	// Position is where the block starts.
	Position Position
}

// Change is a parameter's new value, from a frame in a block on.
type Change struct {
	Frame int
	ID    uint32
	Value float64
}

// Event is a note starting or ending, or changing as it sounds, at a
// frame in a block.
type Event struct {
	// Frame is where in the block it happens.
	Frame int
	Kind  EventKind
	// Note is the note's ID, the note's alone from its NoteOn to its
	// NoteOff.
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
