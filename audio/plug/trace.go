package plug

import (
	"math"
	"sync/atomic"
)

// TraceFrames is how many frames a [Trace] keeps one value for, and
// traceRuns how many such runs it keeps: over five seconds at 48 kHz.
const (
	TraceFrames = 1 << traceShift
	traceShift  = 8
	traceRuns   = 1024
)

// A Trace is a value a processor keeps by frame as it plays, as a gain
// reduction, for its views to draw along the sound they hear: the
// least of each run of TraceFrames frames, for the last few seconds.
// The frames count as [Position.Frame] does, and as [Sound.Heard]
// counts them, so a view reads the value of the very frames it hears.
// The processor puts, on its goroutine, and views get, on theirs,
// neither waiting for the other.
type Trace struct {
	// runs holds each run's value, with the run's number, plus one, in
	// the high half, so a slot of another run, or none, reads as such.
	runs [traceRuns]atomic.Uint64
	// The processor's: the run being put, and its least so far.
	run   int64
	least float32
	open  bool
}

// Put keeps v for frame f, as Process does, a frame at a time.
//
// It stores only where the run's least changes: a store a view may read
// at once costs the processor as much as the rest of a frame's work, and
// most frames change nothing.
func (t *Trace) Put(f int64, v float32) {
	r := f >> traceShift
	if !t.open || r != t.run {
		t.run, t.least, t.open = r, v, true
	} else {
		if v >= t.least {
			return
		}
		t.least = v
	}
	t.runs[r%traceRuns].Store(uint64(uint32(r)+1)<<32 | uint64(math.Float32bits(t.least)))
}

// Get returns the value kept for the run of frame f, and whether one
// is: none for frames not played, or played too long ago.
func (t *Trace) Get(f int64) (v float32, ok bool) {
	if f < 0 {
		return 0, false
	}
	r := f >> traceShift
	x := t.runs[r%traceRuns].Load()
	if x>>32 != uint64(uint32(r)+1) {
		return 0, false
	}
	return math.Float32frombits(uint32(x)), true
}

// Tracer is a processor that keeps [Trace]s, by the ID of a ReadOnly
// parameter, for [Host.Traced].
type Tracer interface {
	Trace(id uint32) *Trace
}
