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
	// Channels is how many channels go in and come out.
	Channels int
	// Offline says the sound is being rendered, as for an export, where
	// a block may take longer than it lasts.
	Offline bool
}

// Processor is the part of a plugin that changes the sound. Prepare
// and Reset run with no block in progress; Process runs on the audio
// thread, and must not allocate, lock or wait.
type Processor interface {
	// Prepare readies the processor to run as s says. The first block
	// after it carries every parameter's value as a change at frame 0.
	Prepare(s Setup)
	// Process changes a block of sound.
	Process(b *Block)
	// Latency is how many frames late the sound comes out; the host
	// moves the rest of the sound to match.
	Latency() int
	// Tail is how many frames of sound come out after the sound going
	// in stops, as a reverb's ringing.
	Tail() int
	// Reset drops the sound held from before, as playing jumps to
	// another place.
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
	// Out may be In, to process in place.
	In, Out [][]float32
	// Frames is how many frames each channel has.
	Frames int
	// Changes are the parameters changed during the block, in the order
	// of their frames.
	Changes []Change
	// Position is where the block starts.
	Position Position
}

// Change is a parameter's new value, from a frame in a block on.
type Change struct {
	Frame int
	ID    uint32
	Value float64
}

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
