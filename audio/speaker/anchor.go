package speaker

import (
	"math"
	"sync"
	"time"
)

// An Anchor ties the mixer's frames to the clock [time.Now] reads:
// frame Frame is heard at Time, and the frames after it follow Period
// apart. A host that renders frame f sends a MIDI note for it to leave
// at TimeOf(f), and turns the time a message came in into the frame
// heard then with FrameAt.
type Anchor struct {
	Frame int64
	Time  time.Time
	// Period is how long a frame lasts on the clock, in nanoseconds:
	// 1e9 over the rate, as the sound card's own clock runs.
	Period float64
}

// TimeOf returns when frame f is heard.
func (a Anchor) TimeOf(f int64) time.Time {
	return a.Time.Add(time.Duration(math.Round(float64(f-a.Frame) * a.Period)))
}

// FrameAt returns the frame heard at t.
func (a Anchor) FrameAt(t time.Time) int64 {
	if a.Period == 0 {
		return a.Frame
	}
	return a.Frame + int64(math.Round(float64(t.Sub(a.Time))/a.Period))
}

// Anchor returns where the mixer's frames are heard on the clock
// [time.Now] reads, and the zero Anchor while the speakers are closed.
//
// [Speaker.Heard] steps a buffer at a time, as the system takes the
// sound. Anchor smooths the steps with a delay-locked loop, which
// follows the sound card's clock as it drifts from the computer's. It
// places a frame 200 ms ahead to within a millisecond of the line
// through the middle of the steps, once it has run a second. Each call teaches it, and the speaker
// calls it a few times a second itself. A host calls it once a block,
// from the goroutine that renders its sound: it allocates nothing.
func (s *Speaker) Anchor() Anchor {
	out := s.out.Load()
	if out == nil {
		return Anchor{}
	}
	s.anchor.mu.Lock()
	defer s.anchor.mu.Unlock()
	return s.anchor.sample(s.m.Heard(), time.Now(), out.rate())
}

// The loop's bandwidths: wide as it starts, to find the line, then
// narrowing over settle to keep to it through the steps.
const (
	startBandwidth = 2.0 // Hz
	keepBandwidth  = 0.1 // Hz
	settle         = 3 * time.Second
	// jump is how far a frame may be heard from where the loop placed
	// it before the loop starts again from it: as the speakers reopen,
	// run dry or are suspended.
	jump = 30 * time.Millisecond
	// drift is how far the sound card's clock may run from the
	// computer's.
	drift = 0.01
)

// dll is a delay-locked loop on the frames heard, as Fons Adriaensen's
// "Using a DLL to filter time" has it.
type dll struct {
	mu      sync.Mutex
	base    time.Time // the times below count from it
	rate    int
	frame   int64   // the anchor's frame
	at      float64 // when it is heard, in ns from base
	period  float64 // ns a frame
	last    float64 // when the loop last learned, in ns from base
	started float64 // when it started, in ns from base
	n       int     // samples since it started
}

// sample teaches the loop that frame h is at the speakers at t, and
// returns its anchor.
func (d *dll) sample(h int64, t time.Time, rate int) Anchor {
	if d.base.IsZero() {
		d.base = t
	}
	now := float64(t.Sub(d.base))
	nominal := 1e9 / float64(rate)
	if d.n == 0 || rate != d.rate {
		d.restart(h, now, rate)
		return d.anchor()
	}
	predicted := d.at + float64(h-d.frame)*d.period
	e := now - predicted
	dt := now - d.last
	switch {
	case math.Abs(e) > float64(jump):
		d.restart(h, now, rate)
		return d.anchor()
	case dt < float64(time.Millisecond):
		// Too soon to learn from: the anchor as it is.
		return d.anchor()
	}
	bw := keepBandwidth
	if since := now - d.started; since < float64(settle) {
		bw = startBandwidth * math.Pow(keepBandwidth/startBandwidth, since/float64(settle))
	}
	b := min(2*math.Pi*bw*dt/1e9, 0.5)
	d.frame, d.at = h, predicted+math.Sqrt2*b*e
	d.period += b * b * e / (dt / d.period)
	d.period = min(max(d.period, nominal*(1-drift)), nominal*(1+drift))
	d.last = now
	d.n++
	return d.anchor()
}

func (d *dll) restart(h int64, now float64, rate int) {
	d.rate, d.frame, d.at, d.last, d.started, d.n = rate, h, now, now, now, 1
	d.period = 1e9 / float64(rate)
}

func (d *dll) anchor() Anchor {
	whole := math.Floor(d.at)
	return Anchor{Frame: d.frame, Time: d.base.Add(time.Duration(whole)), Period: d.period}
}
