//go:build linux && (arm64 || amd64)

package kms

import (
	"encoding/json"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/gunim/driver/internal/render"
)

// Stats are the driver's figures since it opened, or since the last
// ResetStats: what tools/kmsbench checks against the goals.
type Stats struct {
	// Frames is the frames shown, and Missed the vblanks they missed. A
	// frame the engine began as the windows heard of the flip before it,
	// as while something moves, misses each vblank between that flip and
	// its own past the first. A frame begun after a rest, as on a touch,
	// had the time from its start to the vblank after next: it misses
	// each vblank past those.
	Frames, Missed int
	// Straight is the frames begun as the windows heard of the flip
	// before, and StraightMissed the vblanks they missed: while something
	// moves on every frame, the frame rate is the panel's times Straight
	// over Straight plus StraightMissed.
	Straight, StraightMissed int
	// Frame is each frame's time from when the engine began it to its
	// commit: the whole frame, from the engine's step and layout through
	// paint to the GPU's draw. Where the engine did not say when it began,
	// as for a popup's own frames, it counts from the frame handed over.
	// Draw is the driver's part alone: from the frame handed over to its
	// commit. Touch is from a touch to the flip of the first frame after
	// it.
	Frame, Draw, Touch Percentiles
	// Last is the newest frame's time, as Frame measures it.
	Last time.Duration
	// Late is the frames that took a refresh or more, as Frame measures
	// them.
	Late int
	// Since is when the figures started.
	Since time.Time
}

// Percentiles are a set of times' middle, 99th percentile and largest.
type Percentiles struct {
	N             int
	P50, P99, Max time.Duration
}

// ring keeps the last len(buf) times, so a driver that runs for months
// keeps its figures in fixed memory.
type ring struct {
	buf  []time.Duration
	next int
	full bool
}

func newRing(n int) ring { return ring{buf: make([]time.Duration, n)} }

func (r *ring) add(d time.Duration) {
	r.buf[r.next] = d
	r.next++
	if r.next == len(r.buf) {
		r.next, r.full = 0, true
	}
}

func (r *ring) times() []time.Duration {
	if r.full {
		return r.buf
	}
	return r.buf[:r.next]
}

func (r *ring) reset() { r.next, r.full = 0, false }

// percentiles works out d's figures; it sorts a copy, off the frame's path.
func percentiles(d []time.Duration) Percentiles {
	if len(d) == 0 {
		return Percentiles{}
	}
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(q float64) time.Duration { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return Percentiles{N: len(s), P50: at(0.5), P99: at(0.99), Max: s[len(s)-1]}
}

// statsKept is how many frames' times the figures cover: over two minutes
// at 60 Hz.
const statsKept = 8192

// timeline keeps a record of each frame, for the figures and the trace.
type timeline struct {
	mu                sync.Mutex
	frames            int
	missed            int
	late              int
	straight, sMissed int
	// frame, draw and touch keep the last statsKept frames' times.
	frame, draw, touch ring
	last               time.Duration
	since              time.Time

	// The second's figures, for GUNIM_DEBUG_FRAMES, slow frames among
	// them.
	secFrames, secMissed, secSlow int
	secFrame, secTouch            ring
	// sent is what the renderers sent the GPU in the second, and drawn
	// the frames they drew, for GUNIM_DEBUG_FRAMES.
	sent  render.Stats
	drawn int

	trace *json.Encoder
}

// traceFrame is one line of GUNIM_KMS_TRACE: one frame's times, in
// microseconds from the flip before it.
type traceFrame struct {
	Seq        uint32 `json:"seq"`
	Began      int64  `json:"began_us"`
	Handed     int64  `json:"handed_us"`
	Commit     int64  `json:"commit_us"`
	Flip       int64  `json:"flip_us"`
	Touch      int64  `json:"touch_us,omitempty"`
	Straight   bool   `json:"straight"`
	MissedHere int    `json:"missed,omitempty"`
}

func newTimeline(trace io.Writer) *timeline {
	t := &timeline{since: time.Now(),
		frame: newRing(statsKept), draw: newRing(statsKept), touch: newRing(statsKept),
		secFrame: newRing(240), secTouch: newRing(240)}
	if trace != nil {
		t.trace = json.NewEncoder(trace)
	}
	return t
}

// shownFrame is one frame's times, from the flip before it, or the zero
// time where there was none, to its own flip.
type shownFrame struct {
	seq uint32
	// prev is the flip before. began is when the engine began the frame,
	// or the zero time where it did not say, handed when the frame was
	// handed over, commit when it was committed, flip when it flipped,
	// and touch the oldest touch it answers, or zero.
	prev, began, handed, commit, flip, touch time.Time
	// straight says the engine began it as the windows heard of the flip
	// before, as while something moves, and missed is then the vblanks
	// between the two flips past the first.
	straight bool
	missed   int
	period   time.Duration
}

// shown records one frame.
func (t *timeline) shown(f shownFrame) {
	t.mu.Lock()
	defer t.mu.Unlock()
	began := f.began
	if began.IsZero() {
		began = f.handed
	}
	d := f.commit.Sub(began)
	missed := f.missed
	if !f.straight {
		// Begun after a rest: it had until the vblank after next.
		missed = 0
		if f.period > 0 {
			missed = max(0, int(f.flip.Sub(began)/f.period)-1)
		}
	}
	t.frames++
	t.secFrames++
	if f.straight {
		t.straight++
		t.sMissed += missed
	}
	t.missed += missed
	t.secMissed += missed
	t.frame.add(d)
	t.secFrame.add(d)
	t.last = d
	if f.period > 0 && d >= f.period {
		t.late++
		t.secSlow++
	}
	t.draw.add(f.commit.Sub(f.handed))
	if !f.touch.IsZero() {
		d := f.flip.Sub(f.touch)
		t.touch.add(d)
		t.secTouch.add(d)
	}
	if t.trace != nil {
		us := func(at time.Time) int64 {
			if at.IsZero() || f.prev.IsZero() {
				return 0
			}
			return at.Sub(f.prev).Microseconds()
		}
		tf := traceFrame{Seq: f.seq, Began: us(began), Handed: us(f.handed), Commit: us(f.commit), Flip: us(f.flip),
			Straight: f.straight, MissedHere: missed}
		if !f.touch.IsZero() {
			tf.Touch = f.flip.Sub(f.touch).Microseconds()
		}
		_ = t.trace.Encode(tf) // a trace that fails to write loses lines, no more
	}
}

// drew adds what a renderer sent the GPU for one frame, for
// GUNIM_DEBUG_FRAMES.
func (t *timeline) drew(st *render.Stats) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent.Flushes += st.Flushes
	t.sent.Quads += st.Quads
	t.sent.Bytes += st.Bytes
	t.sent.Layers += st.Layers
	t.sent.Boxes += st.Boxes
	t.sent.Redrawn += st.Redrawn
	t.sent.Glyphs += st.Glyphs
	t.sent.Scratches += st.Scratches
	t.sent.Ramps += st.Ramps
	t.sent.BigMasks += st.BigMasks
	t.sent.FadesInPlace += st.FadesInPlace
	t.drawn++
}

// stats returns the figures so far.
func (t *timeline) stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Stats{
		Frames: t.frames, Missed: t.missed, Late: t.late, Straight: t.straight, StraightMissed: t.sMissed,
		Frame: percentiles(t.frame.times()), Draw: percentiles(t.draw.times()), Touch: percentiles(t.touch.times()),
		Last: t.last, Since: t.since,
	}
}

// reset starts the figures again.
func (t *timeline) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frames, t.missed, t.late, t.straight, t.sMissed = 0, 0, 0, 0, 0
	t.frame.reset()
	t.draw.reset()
	t.touch.reset()
	t.since = time.Now()
}

// secondFigures are one second's figures, for GUNIM_DEBUG_FRAMES: frames,
// missed vblanks and slow frames, their times, and what the renderers sent
// the GPU over drawn frames.
type secondFigures struct {
	frames, missed, slow int
	frame, touch         Percentiles
	sent                 render.Stats
	drawn                int
}

// second returns the last second's figures, and starts the next.
func (t *timeline) second() secondFigures {
	t.mu.Lock()
	defer t.mu.Unlock()
	sent, drawn := t.sent, t.drawn
	t.sent, t.drawn = render.Stats{}, 0
	f := secondFigures{frames: t.secFrames, missed: t.secMissed, slow: t.secSlow,
		frame: percentiles(t.secFrame.times()), touch: percentiles(t.secTouch.times()), sent: sent, drawn: drawn}
	t.secFrames, t.secMissed, t.secSlow = 0, 0, 0
	t.secFrame.reset()
	t.secTouch.reset()
	return f
}
