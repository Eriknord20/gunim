package speaker

import (
	"math"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
)

// card is a sound card whose clock runs ppm parts in a million fast,
// taking step frames at a time, each step up to burst early or late:
// the frame heard at a time steps as Speaker.Heard does.
type card struct {
	start time.Time
	rate  float64 // frames a second, as the card's clock runs them
	step  int64
	burst time.Duration
}

func newCard(ppm float64, step int64) *card {
	return &card{start: time.Unix(1000, 0), rate: 48000 * (1 + ppm/1e6), step: step}
}

// edge is when step k lands.
func (c *card) edge(k int64) time.Time {
	t := c.start.Add(time.Duration(float64(k*c.step) / c.rate * 1e9))
	if c.burst > 0 {
		// A late or early burst, the same for k each time.
		j := rand.New(rand.NewPCG(uint64(k), 9)).Float64()*2 - 1
		t = t.Add(time.Duration(j * float64(c.burst)))
	}
	return t
}

// heard is the frame Heard reports at t: whole steps.
func (c *card) heard(t time.Time) int64 {
	k := int64(t.Sub(c.start).Seconds() * c.rate / float64(c.step))
	for k > 0 && t.Before(c.edge(k)) {
		k--
	}
	for !t.Before(c.edge(k + 1)) {
		k++
	}
	return k * c.step
}

// at is when frame f is heard: the line through the steps.
func (c *card) at(f int64) time.Time {
	return c.start.Add(time.Duration(float64(f) / c.rate * 1e9))
}

// ahead is a frame 200 ms ahead of t, as a host sends a note for, a
// latency ahead.
func (c *card) ahead(t time.Time) int64 { return c.heard(t) + 9600 }

// run samples c as a host would, at uneven times from t for dur, and
// returns the last anchor and the time it was taken. It fails the test
// where a frame's time moves earlier than slew lets it.
func run(t *testing.T, d *dll, c *card, at time.Time, dur time.Duration, r *rand.Rand) (Anchor, time.Time) {
	t.Helper()
	var a, was Anchor
	var then time.Time
	for end := at.Add(dur); at.Before(end); at = at.Add(time.Duration(2+r.IntN(7)) * time.Millisecond) {
		a = d.sample(c.heard(at), at, 48000)
		if was.Period != 0 {
			f := c.ahead(at)
			back := was.TimeOf(f).Sub(a.TimeOf(f))
			if allowed := time.Duration(slew*float64(at.Sub(then))) + time.Microsecond; back > allowed {
				t.Fatalf("frame %d moved %v earlier in %v, past the %v slew lets it", f, back, at.Sub(then), allowed)
			}
		}
		was, then = a, at
	}
	return a, at
}

func placed(c *card, a Anchor, now time.Time) time.Duration {
	f := c.ahead(now)
	return a.TimeOf(f).Sub(c.at(f))
}

func TestTheAnchorFindsTheCardsClockThroughTheSteps(t *testing.T) {
	for _, step := range []int64{256, 1024, 4096} {
		for _, ppm := range []float64{0, 80, -150} {
			var d dll
			c := newCard(ppm, step)
			r := rand.New(rand.NewPCG(1, uint64(step)))
			_, now := run(t, &d, c, c.start, time.Second, r)
			started := d.started
			a, now := run(t, &d, c, now, 20*time.Second, r)
			if d.started != started {
				t.Errorf("%d-frame steps, %+g ppm: the loop started again while locked", step, ppm)
			}
			if off := placed(c, a, now).Abs(); off > time.Millisecond {
				t.Errorf("%d-frame steps, %+g ppm: a frame 200 ms ahead placed %v off, want within 1 ms", step, ppm, off)
			}
			// The steps leave the period a little rough: 300 ppm moves
			// a frame 200 ms ahead by 0.06 ms.
			want := 1e9 / c.rate
			if e := math.Abs(a.Period-want) / want * 1e6; e > 300 {
				t.Errorf("%d-frame steps, %+g ppm: period %.1f ppm off", step, ppm, e)
			}
			if f := c.ahead(now); a.FrameAt(a.TimeOf(f)) != f {
				t.Errorf("FrameAt(TimeOf(%d)) = %d", f, a.FrameAt(a.TimeOf(f)))
			}
		}
	}
}

func TestTheAnchorStaysLockedThroughBursts(t *testing.T) {
	var d dll
	c := newCard(40, 4096)
	c.burst = 20 * time.Millisecond
	r := rand.New(rand.NewPCG(2, 2))
	_, now := run(t, &d, c, c.start, 2*time.Second, r)
	started := d.started
	a, now := run(t, &d, c, now, 20*time.Second, r)
	if d.started != started {
		t.Error("the loop started again through bursts 20 ms early or late")
	}
	if off := placed(c, a, now).Abs(); off > 2*time.Millisecond {
		t.Errorf("a frame 200 ms ahead placed %v off through bursts, want within 2 ms", off)
	}
}

// TestALeapForwardComesBackAtTheSlew: the frames heard leap 200 ms
// ahead, as after the sound ran dry. No frame's time moves earlier
// than the slew lets it, and the anchor comes back to the card.
func TestALeapForwardComesBackAtTheSlew(t *testing.T) {
	var d dll
	c := newCard(50, 512)
	r := rand.New(rand.NewPCG(3, 3))
	_, now := run(t, &d, c, c.start, 3*time.Second, r)
	c.start = c.start.Add(-200 * time.Millisecond)
	a, now := run(t, &d, c, now, time.Second, r)
	if off := placed(c, a, now); off < 100*time.Millisecond {
		t.Errorf("a second after the leap the anchor is %v off, want it coming back at the slew, near 150 ms", off)
	}
	a, now = run(t, &d, c, now, 5*time.Second, r)
	if off := placed(c, a, now).Abs(); off > time.Millisecond {
		t.Errorf("after the leap the anchor stayed %v off", off)
	}
}

// TestAStallMovesTheAnchorLaterAtOnce: the frames heard fall 200 ms
// behind, as the speakers reopen with a longer buffer. Later is safe,
// so the anchor follows at once, and settles within a second or so.
func TestAStallMovesTheAnchorLaterAtOnce(t *testing.T) {
	var d dll
	c := newCard(0, 512)
	r := rand.New(rand.NewPCG(4, 4))
	_, now := run(t, &d, c, c.start, 3*time.Second, r)
	c.start = c.start.Add(200 * time.Millisecond)
	a, now := run(t, &d, c, now, 100*time.Millisecond, r)
	if off := placed(c, a, now); off < -2*time.Millisecond || off > 20*time.Millisecond {
		t.Errorf("just after the stall the anchor is %v off, want it moved the 200 ms at once", off)
	}
	a, now = run(t, &d, c, now, 1500*time.Millisecond, r)
	if off := placed(c, a, now).Abs(); off > time.Millisecond {
		t.Errorf("after the stall the anchor stayed %v off", off)
	}
}

func TestAResetStartsTheLoopAgainAndKeepsToTheSlew(t *testing.T) {
	var d dll
	c := newCard(0, 512)
	r := rand.New(rand.NewPCG(5, 5))
	_, now := run(t, &d, c, c.start, 2*time.Second, r)
	d.reset()
	c.start = c.start.Add(-50 * time.Millisecond)
	a, now := run(t, &d, c, now, 4*time.Second, r)
	if off := placed(c, a, now).Abs(); off > time.Millisecond {
		t.Errorf("after the reset the anchor is %v off", off)
	}
	d.clear()
	if a := d.sample(c.heard(now), now, 48000); !a.Time.Equal(now) {
		t.Errorf("after clear the anchor starts at %v from now, want now", a.Time.Sub(now))
	}
}

func TestTheAnchorStartsAgainAtANewRate(t *testing.T) {
	var d dll
	c := newCard(0, 256)
	_, now := run(t, &d, c, c.start, time.Second, rand.New(rand.NewPCG(6, 6)))
	if a := d.sample(c.heard(now), now, 44100); a.Period != 1e9/44100.0 {
		t.Errorf("at a new rate the period is %v, want 1e9/44100", a.Period)
	}
}

func TestSamplesBetweenStepsLeaveTheAnchor(t *testing.T) {
	var d dll
	c := newCard(0, 4096)
	_, now := run(t, &d, c, c.start, time.Second, rand.New(rand.NewPCG(7, 7)))
	a := d.sample(c.heard(now), now, 48000)
	for range 100 {
		now = now.Add(100 * time.Microsecond)
		if c.heard(now) != a.Frame && c.heard(now) != c.heard(now.Add(-100*time.Microsecond)) {
			break
		}
		if b := d.sample(c.heard(now), now, 48000); b != a && b.Frame == a.Frame {
			t.Fatalf("a sample between steps moved the anchor from %+v to %+v", a, b)
		}
	}
}

func TestAnchoringMakesNothing(t *testing.T) {
	var d dll
	c := newCard(0, 256)
	now := c.start
	if n := testing.AllocsPerRun(1000, func() {
		now = now.Add(3 * time.Millisecond)
		d.sample(c.heard(now), now, 48000)
	}); n != 0 {
		t.Errorf("sample made %v things a run, want none", n)
	}
}

// TestABusyMomentLeavesTheAnchor: a host takes a buffer each buffer's
// time and asks for the anchor at once, as the fake driver below does,
// and is held up a moment: the buffers that came due meanwhile come at
// once after. The anchor keeps to the buffers' pace through it, with a
// sample the speaker takes itself in the moment or without.
func TestABusyMomentLeavesTheAnchor(t *testing.T) {
	period := 256 * time.Second / 48000
	for _, ms := range []int{20, 30, 40, 100, 200} {
		for _, watched := range []bool{false, true} {
			held := time.Duration(ms) * time.Millisecond
			var d dll
			start := time.Unix(1000, 0)
			busy := start.Add(150 * period)
			for k := range int64(300) {
				due := start.Add(time.Duration(k) * period)
				at := due
				if !due.Before(busy) && due.Before(busy.Add(held)) {
					if watched && at.Equal(busy) {
						d.sample((k-1)*256, busy.Add(held/2), 48000)
					}
					at = busy.Add(held)
				}
				a := d.sample(k*256, at.Add(time.Duration(k%7)*time.Microsecond), 48000)
				// Half a second in, the loop has found the line.
				if off := a.TimeOf(k * 256).Sub(due).Abs(); k >= 100 && off > 4*time.Millisecond {
					t.Fatalf("held %v, watched %v: buffer %d placed %v from when it was due", held, watched, k, off)
				}
			}
			if d.started != 0 {
				t.Errorf("held %v, watched %v: the loop started again", held, watched)
			}
		}
	}
}

// TestTheAnchorKeepsToACoarseClock: Windows may read [time.Now] from a
// clock that ticks each 15.6 ms, so a host taking a buffer each 5.3 ms
// sees three at a time, and a hold-up as well. The anchor places them
// within a tick still.
func TestTheAnchorKeepsToACoarseClock(t *testing.T) {
	period := 256 * time.Second / 48000
	const tick = 15625 * time.Microsecond
	start := time.Unix(1000, 0)
	coarse := func(t time.Time) time.Time { return start.Add(t.Sub(start).Truncate(tick)) }
	var d dll
	busy := start.Add(150 * period)
	for k := range int64(300) {
		due := start.Add(time.Duration(k) * period)
		at := due
		if !due.Before(busy) && due.Before(busy.Add(50*time.Millisecond)) {
			at = busy.Add(50 * time.Millisecond)
		}
		a := d.sample(k*256, coarse(at), 48000)
		if off := a.TimeOf(k * 256).Sub(due).Abs(); k >= 100 && off > tick {
			t.Fatalf("buffer %d placed %v from when it was due, want within a tick", k, off)
		}
	}
}

// TestASpeakersAnchorFollowsItsDriver plays through a fake ASIO driver
// that takes a buffer each buffer's time, on a clock of the test's own,
// and checks the frame heard is placed when the driver took it. The
// fake driver's pace is the test's: were the test held up a moment, as
// on a busy runner, its buffers would come late and then at once, and
// the frames heard would not keep to any one line. So the test keeps
// the clock, and lets the ring fill before each buffer; the two tests
// above follow the anchor through such moments, and Windows' clock.
func TestASpeakersAnchorFollowsItsDriver(t *testing.T) {
	var at atomic.Int64
	start := time.Unix(1000, 0)
	was := clock
	clock = func() time.Time { return start.Add(time.Duration(at.Load())) }
	t.Cleanup(func() { clock = was })
	f := useFake(t, driver.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Latency: 300, Bits: 32})
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: "Fake", Latency: 30 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	r := ringOf(t, s)
	full := func() bool { return r.q.Load().len() >= r.target.Load() }
	waitFor(t, "the ring to fill", r.primed.Load)
	period := 256 * time.Second / 48000
	var a Anchor
	for k := range 300 {
		at.Store(int64(time.Duration(k) * period))
		f.take()
		waitFor(t, "the ring to fill again", full)
		a = s.Anchor()
	}
	took := clock()
	if off := a.TimeOf(s.Heard()).Sub(took).Abs(); off > 4*time.Millisecond {
		t.Errorf("the frame heard last is placed %v from when the driver took it, want within 4 ms", off)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if a := s.Anchor(); a != (Anchor{}) {
		t.Errorf("a closed speaker's anchor is %+v, want the zero Anchor", a)
	}
}

// TestARareCallerFindsTheCardsClock: a host that asks for the anchor
// rarely, at uneven times, sees Heard move many steps between calls.
// The newest step came no longer ago than a step takes, so the anchor
// places the frames heard as well as Heard's steps let it: within a
// millisecond where Heard moves frame by frame, as where the device
// tells when its frames play, once it has run a second; within 3 ms,
// or 40% of a step, where it moves a buffer at a time, once it has run
// 8 s. Each case starts at a dozen offsets, as a caller at a steady
// pace sees the steps at a phase that changes slowly.
func TestARareCallerFindsTheCardsClock(t *testing.T) {
	for _, step := range []int64{1, 256, 1024, 4096} {
		for _, gap := range []time.Duration{16 * time.Millisecond, 50 * time.Millisecond, 250 * time.Millisecond} {
			c := newCard(100, step)
			want, from := time.Millisecond, time.Second
			if step > 1 {
				want, from = max(3*time.Millisecond, time.Duration(float64(step)/c.rate*1e9*0.4)), 8*time.Second
			}
			for off := range 12 {
				var d dll
				r := rand.New(rand.NewPCG(11, uint64(step)))
				now := c.start.Add(time.Duration(off) * 9 * time.Millisecond)
				for end := c.start.Add(16 * time.Second); now.Before(end); now = now.Add(gap + time.Duration(r.IntN(300))*time.Microsecond) {
					a := d.sample(c.heard(now), now, 48000)
					if now.Sub(c.start) < from {
						continue
					}
					if e := placed(c, a, now).Abs(); e > want {
						t.Fatalf("%d-frame steps, asked each %v from %v: a frame 200 ms ahead placed %v off at %v, want within %v",
							step, gap, time.Duration(off)*9*time.Millisecond, e, now.Sub(c.start), want)
					}
				}
			}
		}
	}
}

// TestAShortStepLeavesTheAnchor: as the sound runs dry a moment, Heard
// moves 300 frames once among steps of 512, and the frames after are
// heard 212 frames later. A move that size would have the loop take
// Heard to move 4 frames at a time; the steps a host calling once a
// block sees Heard stand still for keep it to the steps' own size, so
// it does as well as it did before it learned how far Heard moves. A
// host calling rarely keeps within a few milliseconds.
func TestAShortStepLeavesTheAnchor(t *testing.T) {
	const step, short = 512, 300
	start := time.Unix(1000, 0)
	period := float64(step) / 48000 * 1e9
	cut := start.Add(5 * time.Second)
	kc := int64(float64(cut.Sub(start)) / period)
	heard := func(at time.Time) int64 {
		k := int64(float64(at.Sub(start)) / period)
		if k > kc {
			return step*k - (step - short)
		}
		return step * k
	}
	when := func(f int64) time.Time {
		if f > step*kc {
			f += step - short
		}
		return start.Add(time.Duration(float64(f) / 48000 * 1e9))
	}
	for _, c := range []struct {
		name     string
		gap, jit time.Duration
		want     time.Duration
	}{
		{"once a block", 2 * time.Millisecond, 2 * time.Millisecond, 1500 * time.Microsecond},
		{"four times a second", 250 * time.Millisecond, 300 * time.Microsecond, 10 * time.Millisecond},
	} {
		var d dll
		r := rand.New(rand.NewPCG(5, 5))
		for now := start; now.Before(start.Add(16 * time.Second)); now = now.Add(c.gap + time.Duration(r.Int64N(int64(c.jit)))) {
			a := d.sample(heard(now), now, 48000)
			if now.Before(cut.Add(3 * time.Second)) {
				continue
			}
			f := heard(now) + 9600
			if e := a.TimeOf(f).Sub(when(f)).Abs(); e > c.want {
				t.Fatalf("asked %s: a frame 200 ms ahead placed %v off at %v, want within %v", c.name, e, now.Sub(start), c.want)
			}
		}
	}
}

// TestARareCallerFollowsTheSoundThroughAStall: the sound stalls 400 ms,
// as when it runs dry or the speakers are suspended, and Heard stands
// still through it; after, the frames are heard 400 ms later. A host
// calling four times a second sees Heard stand still longer than any
// step lasts, which says nothing of the steps, so the anchor goes on
// learning and follows the frames to their new times.
func TestARareCallerFollowsTheSoundThroughAStall(t *testing.T) {
	for _, step := range []int64{1, 512, 1024} {
		var d dll
		c := newCard(100, step)
		r := rand.New(rand.NewPCG(9, uint64(step)))
		stall := c.start.Add(4 * time.Second)
		var top int64
		for now := c.start; now.Before(c.start.Add(30 * time.Second)); now = now.Add(250*time.Millisecond + time.Duration(r.IntN(300))*time.Microsecond) {
			if !now.Before(stall) && c.start.Before(stall) {
				c.start = c.start.Add(400 * time.Millisecond)
			}
			// Heard never goes back: it stands still until the
			// frames catch up.
			top = max(top, c.heard(now))
			a := d.sample(top, now, 48000)
			if now.Sub(stall) < 12*time.Second {
				continue
			}
			want := max(3*time.Millisecond, time.Duration(float64(step)/c.rate*1e9*0.4))
			if e := placed(c, a, now).Abs(); e > want {
				t.Fatalf("%d-frame steps: %v after the stall a frame 200 ms ahead is placed %v off, want within %v",
					step, now.Sub(stall), e, want)
			}
		}
	}
}
