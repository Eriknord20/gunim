package touch

import (
	"time"

	"github.com/marrasen/gunim/geom"
)

// Resampling's times, as phones use them.
const (
	// ResampleLatency is how far before a frame's start the fingers are
	// sampled, at least: late enough to be fresh, early enough that a
	// report after that time has usually come. A screen that reports less
	// often than every ResampleLatency plus MaxPredict is sampled earlier,
	// by its report gap less MaxPredict, so a move is never carried on
	// further than MaxPredict: 9 ms on the Touch Display 2, which reports
	// every 17 ms.
	ResampleLatency = 5 * time.Millisecond
	// MaxPredict is the furthest past a finger's newest report a move is
	// carried on.
	MaxPredict = 8 * time.Millisecond
	// staleGap is a gap between two reports too long to tell a speed by:
	// the finger stopped in between.
	staleGap = 50 * time.Millisecond
)

// Resampler gives each frame one move per finger, at an even step. A touch
// screen reports on a clock of its own, which drifts past the screen's: the
// Touch Display 2 every 17 ms against 16.7 ms frames, so some frames would
// get two reports and some none, and a dragged list would move in uneven
// steps. At each frame the Resampler works out where each finger was at one
// time, the frame's start less [ResampleLatency]: between the two reports
// either side of that time, or, where the newest is older, carried on from
// the last two by at most [MaxPredict].
//
// A finger with no report for two report gaps has stopped: it goes back to
// where it was last reported.
//
// Landings and lifts don't pass through it: they go at once, so a tap is
// never late.
type Resampler struct {
	fingers map[int]*track
}

// sample is a finger's place at a time.
type sample struct {
	t time.Time
	p geom.Point
}

// track is one finger's newest reports, oldest first, and the move last
// sent for it.
type track struct {
	s    [3]sample
	n    int
	sent sample
}

func (k *track) add(s sample) {
	if k.n == len(k.s) {
		copy(k.s[:], k.s[1:])
		k.n--
	}
	k.s[k.n] = s
	k.n++
}

// Down starts a finger at p, landed at t: the landing went to the window
// as it came, so it counts as sent.
func (r *Resampler) Down(id int, t time.Time, p geom.Point) {
	if r.fingers == nil {
		r.fingers = map[int]*track{}
	}
	k := &track{}
	k.add(sample{t, p})
	k.sent = sample{t, p}
	r.fingers[id] = k
}

// Move adds a report of finger id at p, at t.
func (r *Resampler) Move(id int, t time.Time, p geom.Point) {
	k := r.fingers[id]
	if k == nil {
		r.Down(id, t, p)
		return
	}
	if t.Before(k.s[k.n-1].t) {
		t = k.s[k.n-1].t
	}
	k.add(sample{t, p})
}

// Up forgets finger id.
func (r *Resampler) Up(id int) { delete(r.fingers, id) }

// Pending reports whether a finger has a report not yet sent.
func (r *Resampler) Pending() bool {
	for _, k := range r.fingers {
		if s := k.s[k.n-1]; s.t.After(k.sent.t) || s.p != k.sent.p && !s.t.Before(k.sent.t) {
			return true
		}
	}
	return false
}

// At appends to out a move for each finger to where it was a little
// before start, the frame's start, and returns out: [ResampleLatency]
// before, or more on a screen that reports slowly. A finger whose place
// then is no later than the move sent last, or no different, gets none.
func (r *Resampler) At(start time.Time, out []Contact) []Contact {
	for id, k := range r.fingers {
		s := k.at(start.Add(-k.latency()))
		if !s.t.After(k.sent.t) || s.p == k.sent.p {
			continue
		}
		k.sent = s
		out = append(out, Contact{ID: id, Pos: s.p, Phase: Move})
	}
	return out
}

// Newest appends to out a move for each finger to its newest report, not
// yet sent, as for a frame drawn at once, and returns out.
func (r *Resampler) Newest(out []Contact) []Contact {
	for id, k := range r.fingers {
		s := k.s[k.n-1]
		if s.t.Before(k.sent.t) || s.p == k.sent.p {
			continue
		}
		k.sent = s
		out = append(out, Contact{ID: id, Pos: s.p, Phase: Move})
	}
	return out
}

// latency is how long before a frame's start the finger is sampled.
func (k *track) latency() time.Duration {
	if k.n < 2 {
		return ResampleLatency
	}
	gap := k.s[k.n-1].t.Sub(k.s[k.n-2].t)
	if gap > staleGap {
		return ResampleLatency
	}
	return max(ResampleLatency, gap-MaxPredict)
}

// at works out where the finger was at t.
func (k *track) at(t time.Time) sample {
	newest := k.s[k.n-1]
	if !t.Before(newest.t) {
		if k.n < 2 {
			return newest
		}
		prev := k.s[k.n-2]
		gap := newest.t.Sub(prev.t)
		if gap <= 0 || gap > staleGap {
			return newest
		}
		if t.Sub(newest.t) > 2*gap {
			// No report for two gaps: the finger stopped, where it was
			// last reported, behind the move carried on past it.
			return sample{t, newest.p}
		}
		ahead := min(t.Sub(newest.t), MaxPredict, gap/2)
		f := float32(ahead) / float32(gap)
		return sample{newest.t.Add(ahead), lerp(newest.p, newest.p.Sub(prev.p), f)}
	}
	for i := k.n - 1; i > 0; i-- {
		a, b := k.s[i-1], k.s[i]
		if !t.Before(a.t) {
			f := float32(t.Sub(a.t)) / float32(b.t.Sub(a.t))
			return sample{t, lerp(a.p, b.p.Sub(a.p), f)}
		}
	}
	// Older than every report kept: the oldest is the nearest.
	return k.s[0]
}

// lerp returns p moved by f of d.
func lerp(p, d geom.Point, f float32) geom.Point {
	return geom.Pt(p.X+d.X*f, p.Y+d.Y*f)
}
