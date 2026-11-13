//go:build linux && (arm64 || amd64)

package kms

import (
	"encoding/json"
	"io"
	"slices"
	"sync"
	"time"
)

// Stats are the driver's figures since it opened, or since the last
// ResetStats: what tools/kmsbench checks against the goals.
type Stats struct {
	// Frames is the frames shown, and Missed the vblanks a frame missed
	// while the program drew one frame straight after another.
	Frames, Missed int
	// Frame is the time from the flip before to the commit, for frames
	// drawn straight after the one before; where the driver held the flip's
	// report back to start the frame later, from when it let it go. It is
	// the whole frame, from the
	// engine's step and layout through paint to the GPU's draw. Draw is
	// the driver's part alone: from the frame handed over to its commit.
	// Touch is from a touch to the flip of the first frame after it.
	Frame, Draw, Touch Percentiles
	// Last is the newest frame's time, as Frame measures it.
	Last time.Duration
	// Late is the frames handed over a refresh or more after the flip
	// before, and LateMissed the vblanks they missed. While something moves
	// on every frame, as in tools/kmsbench, each one is a slow frame on the
	// CPU's side; otherwise the first frame after a rest is one too. All
	// is Frame for every frame with a flip before it, late ones too.
	Late, LateMissed int
	All              Percentiles
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
	mu     sync.Mutex
	frames int
	missed int
	// frame, draw and touch keep the last statsKept frames' times.
	frame, draw, touch ring
	all                ring
	late, lateMissed   int
	last               time.Duration
	since              time.Time

	// The second's figures, for GUNIM_DEBUG_FRAMES, slow frames among
	// them.
	secFrames, secMissed, secSlow int
	secFrame, secTouch            ring

	trace *json.Encoder
}

// traceFrame is one line of GUNIM_KMS_TRACE: one frame's times, in
// microseconds from the flip before it.
type traceFrame struct {
	Seq        uint32 `json:"seq"`
	Handed     int64  `json:"handed_us"`
	Commit     int64  `json:"commit_us"`
	Flip       int64  `json:"flip_us"`
	Touch      int64  `json:"touch_us,omitempty"`
	Straight   bool   `json:"straight"`
	MissedHere int    `json:"missed,omitempty"`
}

func newTimeline(trace io.Writer) *timeline {
	t := &timeline{since: time.Now(),
		frame: newRing(statsKept), draw: newRing(statsKept), touch: newRing(statsKept), all: newRing(statsKept),
		secFrame: newRing(240), secTouch: newRing(240)}
	if trace != nil {
		t.trace = json.NewEncoder(trace)
	}
	return t
}

// shown records one frame: the flip before it at prev, the frame handed
// over at handed, committed at commit, and flipped at flip. straight says
// it was handed over within a refresh of the flip before, as while
// something moves. missed is the vblanks between the two flips past the
// first. touch is the oldest touch it answers, or zero.
func (t *timeline) shown(seq uint32, prev, handed, commit, flip, touch time.Time, straight bool, missed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frames++
	t.secFrames++
	if !prev.IsZero() {
		t.all.add(commit.Sub(prev))
		t.secFrame.add(commit.Sub(prev))
	}
	switch {
	case straight:
		t.missed += missed
		t.secMissed += missed
		if !prev.IsZero() {
			d := commit.Sub(prev)
			t.frame.add(d)
			t.last = d
		}
	case !prev.IsZero():
		t.late++
		t.lateMissed += missed
		t.secSlow++
		t.secMissed += missed
	}
	t.draw.add(commit.Sub(handed))
	if !touch.IsZero() {
		d := flip.Sub(touch)
		t.touch.add(d)
		t.secTouch.add(d)
	}
	if t.trace != nil {
		us := func(at time.Time) int64 {
			if at.IsZero() || prev.IsZero() {
				return 0
			}
			return at.Sub(prev).Microseconds()
		}
		f := traceFrame{Seq: seq, Handed: us(handed), Commit: us(commit), Flip: us(flip), Straight: straight, MissedHere: missed}
		if !touch.IsZero() {
			f.Touch = flip.Sub(touch).Microseconds()
		}
		_ = t.trace.Encode(f) // a trace that fails to write loses lines, no more
	}
}

// stats returns the figures so far.
func (t *timeline) stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Stats{
		Frames: t.frames, Missed: t.missed,
		Frame: percentiles(t.frame.times()), Draw: percentiles(t.draw.times()), Touch: percentiles(t.touch.times()),
		Last: t.last, Since: t.since,
		Late: t.late, LateMissed: t.lateMissed, All: percentiles(t.all.times()),
	}
}

// reset starts the figures again.
func (t *timeline) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frames, t.missed = 0, 0
	t.frame.reset()
	t.draw.reset()
	t.touch.reset()
	t.all.reset()
	t.late, t.lateMissed = 0, 0
	t.since = time.Now()
}

// second returns the last second's figures, slow frames among them, and
// starts the next.
func (t *timeline) second() (frames, missed, slow int, frame, touch Percentiles) {
	t.mu.Lock()
	defer t.mu.Unlock()
	frames, missed, slow = t.secFrames, t.secMissed, t.secSlow
	frame, touch = percentiles(t.secFrame.times()), percentiles(t.secTouch.times())
	t.secFrames, t.secMissed, t.secSlow = 0, 0, 0
	t.secFrame.reset()
	t.secTouch.reset()
	return frames, missed, slow, frame, touch
}
