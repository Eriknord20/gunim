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
// taking step frames at a time: the frame heard at a time steps as
// Speaker.Heard does.
type card struct {
	start time.Time
	rate  float64 // frames a second, as the card's clock runs them
	step  int64
}

func newCard(ppm float64, step int64) card {
	return card{start: time.Unix(1000, 0), rate: 48000 * (1 + ppm/1e6), step: step}
}

// heard is the frame Heard reports at t: whole steps.
func (c card) heard(t time.Time) int64 {
	f := int64(t.Sub(c.start).Seconds() * c.rate)
	return f / c.step * c.step
}

// at is when frame f is heard, halfway up the steps: the line the loop
// finds through them.
func (c card) at(f int64) time.Time {
	return c.start.Add(time.Duration((float64(f) + float64(c.step)/2) / c.rate * 1e9))
}

// run samples c as a host would, at uneven times from t for d, and
// returns the last anchor and the time it was taken.
func run(d *dll, c card, t time.Time, dur time.Duration, r *rand.Rand) (Anchor, time.Time) {
	var a Anchor
	for end := t.Add(dur); t.Before(end); t = t.Add(time.Duration(2+r.IntN(7)) * time.Millisecond) {
		a = d.sample(c.heard(t), t, 48000)
	}
	return a, t
}

func TestTheAnchorFindsTheCardsClockThroughTheSteps(t *testing.T) {
	for _, ppm := range []float64{0, 80, -150} {
		var d dll
		c := newCard(ppm, 512)
		r := rand.New(rand.NewPCG(1, uint64(ppm+1000)))
		a, now := run(&d, c, c.start.Add(time.Second), 20*time.Second, r)
		// Where a frame 200 ms ahead is heard: the time a host sends a
		// note for, a latency ahead.
		f := c.heard(now) + 9600
		if off := a.TimeOf(f).Sub(c.at(f)).Abs(); off > time.Millisecond {
			t.Errorf("%+g ppm: frame %d placed %v from where it is heard, want within 1 ms", ppm, f, off)
		}
		// The steps leave the period a little rough: 300 ppm moves a
		// frame 200 ms ahead by 0.06 ms.
		want := 1e9 / c.rate
		if e := math.Abs(a.Period-want) / want * 1e6; e > 300 {
			t.Errorf("%+g ppm: period %v ns, %.1f ppm from the card's %v", ppm, a.Period, e, want)
		}
		if g := a.FrameAt(a.TimeOf(f)); g != f {
			t.Errorf("FrameAt(TimeOf(%d)) = %d", f, g)
		}
	}
}

func TestTheAnchorStartsAgainAfterAJump(t *testing.T) {
	var d dll
	c := newCard(50, 256)
	r := rand.New(rand.NewPCG(2, 3))
	_, now := run(&d, c, c.start, 5*time.Second, r)
	// The speakers reopen, and the frames heard leap 200 ms.
	c.start = c.start.Add(-200 * time.Millisecond)
	a := d.sample(c.heard(now), now, 48000)
	if a.Frame != c.heard(now) || !a.Time.Equal(now) {
		t.Errorf("after the jump the anchor is frame %d at %v, want it started again at frame %d now",
			a.Frame, a.Time.Sub(now), c.heard(now))
	}
	// It settles again within a second or two.
	a, now = run(&d, c, now, 2*time.Second, r)
	if f := c.heard(now); a.TimeOf(f).Sub(c.at(f)).Abs() > time.Millisecond {
		t.Errorf("after the jump, frame %d is placed %v from where it is heard", f, a.TimeOf(f).Sub(c.at(f)))
	}
}

func TestTheAnchorStartsAgainAtANewRate(t *testing.T) {
	var d dll
	c := newCard(0, 256)
	_, now := run(&d, c, c.start, time.Second, rand.New(rand.NewPCG(4, 5)))
	a := d.sample(c.heard(now), now, 44100)
	if a.Period != 1e9/44100.0 {
		t.Errorf("at a new rate the period is %v, want 1e9/44100", a.Period)
	}
}

func TestSamplesCloseTogetherLeaveTheAnchor(t *testing.T) {
	var d dll
	c := newCard(0, 256)
	_, now := run(&d, c, c.start, time.Second, rand.New(rand.NewPCG(6, 7)))
	a := d.sample(c.heard(now), now, 48000)
	for range 100 {
		now = now.Add(time.Microsecond)
		if b := d.sample(c.heard(now), now, 48000); b != a {
			t.Fatalf("a sample a microsecond on moved the anchor from %+v to %+v", a, b)
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
