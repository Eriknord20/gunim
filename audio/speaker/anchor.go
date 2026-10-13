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
// sound. Anchor learns where each step lands, and runs a delay-locked
// loop through the steps, which follows the sound card's clock as it
// drifts from the computer's. It places a frame 200 ms ahead to within
// a millisecond, once it has run a second, through steps of 256 frames
// to 4096. Each call teaches it, and the speaker calls it four times a
// second itself. A host calls it once a block, from the goroutine that
// renders its sound: it allocates nothing.
//
// The time of a frame heard within a second moves earlier by 5% of
// the time passed at most, so notes a host sent for later frames leave
// after those it sent for earlier ones. After a leap, as when the sound ran dry or the speakers
// reopened, the anchor comes back to the frames heard at that pace.
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
	// jump is how far a step may land from where the loop placed it
	// before the loop starts again from it, at least: as the speakers
	// run dry or reopen.
	jump = 30 * time.Millisecond
	// drift is how far the sound card's clock may run from the
	// computer's.
	drift = 0.01
	// slew is the most a frame's time moves earlier, as a part of the
	// time passed.
	slew = 0.05
)

// dll is a delay-locked loop on the steps of the frames heard, as Fons
// Adriaensen's "Using a DLL to filter time" has it, and the anchor it
// hands out.
type dll struct {
	mu   sync.Mutex
	base time.Time // the times below count from it
	rate int
	// The loop: frame is heard at at, and frames follow period apart.
	frame  int64
	at     float64 // ns from base
	period float64 // ns a frame
	// heard is the frame Heard said last, at sampled.
	heard   int64
	sampled float64
	edge    float64 // when the loop last learned from a step
	started float64 // when it started
	spread  float64 // the mean distance of a step from the line, ns
	n       int     // the steps it learned from since it started
	// The anchor handed out last, at handed.
	out    Anchor
	outAt  float64
	handed float64
}

// sample teaches the loop that frame h is at the speakers at t, and
// returns its anchor.
func (d *dll) sample(h int64, t time.Time, rate int) Anchor {
	if d.base.IsZero() {
		d.base = t
	}
	now := float64(t.Sub(d.base))
	gap := now - d.sampled
	moved := h != d.heard
	d.heard, d.sampled = h, now
	switch {
	case d.n == 0 || rate != d.rate:
		d.restart(h, now, rate)
	case moved:
		// The step came between the last sample and this one.
		d.learn(h, now-gap/2, gap)
	}
	return d.hand(now)
}

// learn moves the loop towards frame h being heard at edge, a step's
// time, known to within gap.
func (d *dll) learn(h int64, edge, gap float64) {
	predicted := d.at + float64(h-d.frame)*d.period
	e := edge - predicted
	if math.Abs(e) > max(float64(jump), gap, 4*d.spread) {
		d.restart(h, edge, d.rate)
		return
	}
	d.spread += (math.Abs(e) - d.spread) / 20
	bw := keepBandwidth
	if since := edge - d.started; since < float64(settle) {
		bw = startBandwidth * math.Pow(keepBandwidth/startBandwidth, since/float64(settle))
	}
	dt := max(edge-d.edge, float64(time.Millisecond))
	b := min(2*math.Pi*bw*dt/1e9, 0.5)
	nominal := 1e9 / float64(d.rate)
	d.frame, d.at = h, predicted+math.Sqrt2*b*e
	d.period += b * b * e / (dt / d.period)
	d.period = min(max(d.period, nominal*(1-drift)), nominal*(1+drift))
	d.edge = edge
	d.n++
}

func (d *dll) restart(h int64, at float64, rate int) {
	d.rate, d.frame, d.at, d.edge, d.started, d.n = rate, h, at, at, at, 1
	d.period = 1e9 / float64(rate)
}

// hand returns the loop's anchor, held where it would move a frame
// heard within a second earlier than slew lets it: the line is held at
// both ends of that second, and so all along it.
func (d *dll) hand(now float64) Anchor {
	at := d.at
	if d.out.Period != 0 {
		was := func(f int64) float64 { return d.outAt + float64(f-d.out.Frame)*d.out.Period }
		back := slew * (now - d.handed)
		far := int64(d.rate)
		at = max(at, was(d.frame)-back, was(d.frame+far)-back-float64(far)*d.period)
	}
	d.outAt, d.handed = at, now
	whole := math.Floor(at)
	d.out = Anchor{Frame: d.frame, Time: d.base.Add(time.Duration(whole)), Period: d.period}
	return d.out
}

// reset starts the loop again at the next sample, as the speakers
// reopen. The anchor it hands out keeps to slew.
func (d *dll) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n = 0
}

// clear forgets the loop and its anchor, as the speakers close.
func (d *dll) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.base, d.n, d.spread = time.Time{}, 0, 0
	d.out, d.outAt, d.handed = Anchor{}, 0, 0
}
