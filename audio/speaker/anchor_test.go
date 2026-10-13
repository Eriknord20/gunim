package speaker

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
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

// TestASpeakersAnchorFollowsItsDriver plays through a fake ASIO driver
// that takes a buffer each buffer's time, and checks the frame heard
// now is placed now.
func TestASpeakersAnchorFollowsItsDriver(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Latency: 300, Bits: 32})
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: "Fake", Latency: 30 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the ring to fill", ringOf(t, s).primed.Load)
	period := 256 * time.Second / 48000
	start := time.Now()
	var a Anchor
	for k := 1; k <= 300; k++ {
		f.take()
		a = s.Anchor()
		time.Sleep(time.Until(start.Add(time.Duration(k) * period)))
	}
	now := time.Now()
	if off := a.TimeOf(s.Heard()).Sub(now).Abs(); off > 20*time.Millisecond {
		t.Errorf("the frame heard now is placed %v from now, want within 20 ms", off)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if a := s.Anchor(); a != (Anchor{}) {
		t.Errorf("a closed speaker's anchor is %+v, want the zero Anchor", a)
	}
}
