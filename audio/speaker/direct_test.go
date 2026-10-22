package speaker

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
)

// TestADirectDriverMixesAsItTakes opens a driver with Direct set: each
// buffer it takes is the mixer's next frames, mixed then, with no ring
// to fill first, and the speaker works the driver's latency ahead and
// no further, whatever Latency asks.
func TestADirectDriverMixesAsItTakes(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 64, Latency: 180, Bits: 32})
	m := audio.NewMixer()
	m.Play(&ramp{}, audio.Options{})
	s, err := Open(m, Options{Driver: "Fake", Buffer: 64, Direct: true, Latency: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r := ringOf(t, s)
	if r.q.Load() != nil {
		t.Error("a direct output made a ring")
	}
	if want := audio.DurationAt(180, 48000); s.Latency() != want || s.State().Latency != want {
		t.Errorf("working %v ahead, want the driver's %v", s.Latency(), want)
	}
	// The voice fades in over its first frames: read past them.
	f.take()
	mixed := m.Mixed()
	for k := range 4 {
		out := f.take()
		first := int64(math.Round(float64(out[0]) * 1e6))
		if want := mixed + int64(k*64); first != want || m.Mixed() != want+64 {
			t.Fatalf("buffer %d starts at mixer frame %d, the mixer at %d after it: want %d and %d", k, first, m.Mixed(), want, want+64)
		}
		for i := range 64 {
			if got := int64(math.Round(float64(out[2*i]) * 1e6)); got != first+int64(i) {
				t.Fatalf("buffer %d, frame %d is mixer frame %d, want %d", k, i, got, first+int64(i))
			}
		}
	}
}

// TestADirectDriverIsSilentSuspended suspends a direct output: the
// buffers the driver takes are silent, and the mixer is left unread
// until it resumes.
func TestADirectDriverIsSilentSuspended(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 64, Bits: 32})
	m := audio.NewMixer()
	m.Play(steady(0.5), audio.Options{})
	s, err := Open(m, Options{Driver: "Fake", Direct: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.take()
	r := ringOf(t, s)
	if err := r.suspend(); err != nil {
		t.Fatal(err)
	}
	was := m.Mixed()
	if out := f.take(); slices.ContainsFunc(out, func(v float32) bool { return v != 0 }) || m.Mixed() != was {
		t.Fatal("a suspended direct output played, or read the mixer")
	}
	if err := r.resume(); err != nil {
		t.Fatal(err)
	}
	if out := f.take(); out[len(out)-1] == 0 {
		t.Fatal("a resumed direct output stays silent")
	}
}

// TestADirectDriverStampsItsInputs records a direct driver's inputs:
// they come stamped as the ring's do, with the first frame of the
// buffer the driver takes next, less its output and input latencies.
func TestADirectDriverStampsItsInputs(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 64, Latency: 180, Bits: 32,
		Inputs: 2, Recorded: 2, InputNames: []string{"Mic", "Guitar"}, InputLatency: 107})
	m := audio.NewMixer()
	m.Play(&ramp{}, audio.Options{})
	s, err := Open(m, Options{Driver: "Fake", Buffer: 64, Direct: true, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tap := duplex.Current()
	if tap == nil {
		t.Fatal("a direct driver offers no tap")
	}
	var heard []int64
	tap.Attach(func(in []float32, channels int, h int64) { heard = append(heard, h) })
	f.take()
	for range 4 {
		out := f.take()
		first := int64(math.Round(float64(out[0]) * 1e6))
		if h := heard[len(heard)-1]; h != first-287 {
			t.Fatalf("the inputs were stamped %d, before a buffer starting at frame %d: want %d", h, first, first-287)
		}
	}
}

// TestADirectDriverPlaysAux opens a direct driver on four outputs: Aux
// fills the two past the mixer's, frame for frame in step with it.
func TestADirectDriverPlaysAux(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 64, Bits: 32, Outputs: 8})
	m := audio.NewMixer()
	m.Play(&ramp{}, audio.Options{})
	var aux frameAux
	s, err := Open(m, Options{Driver: "Fake", Direct: true, Outputs: []int{0, 1, 4, 5}, Aux: aux.fill})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.take()
	for range 4 {
		out := f.take()
		if len(out) != 4*64 {
			t.Fatalf("the driver took %d samples, want %d", len(out), 4*64)
		}
		for i := range 64 {
			fr := out[4*i : 4*i+4]
			mix := math.Round(float64(fr[0]) * 1e6)
			for ch, v := range fr[2:] {
				if got := math.Round((float64(v) - float64(ch+1)*0.1) * 1e6); got != mix {
					t.Fatalf("frame %d: aux channel %d is at mixer frame %v, the mix at %v", i, ch, got, mix)
				}
			}
		}
	}
}

// TestSetChangesDirect sets Direct on a driver playing through the
// ring, and finds it opened again, mixing on its own thread; and back.
func TestSetChangesDirect(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 64, Latency: 180, Bits: 32})
	m := audio.NewMixer()
	o := Options{Driver: "Fake", Buffer: 64, Latency: 20 * time.Millisecond, Fixed: true}
	s, err := Open(m, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if ringOf(t, s).direct {
		t.Fatal("the output mixes directly unasked")
	}
	o.Direct = true
	if err := s.Set(o); err != nil {
		t.Fatal(err)
	}
	if f.opens != 2 || !ringOf(t, s).direct {
		t.Fatalf("after Direct was set the driver opened %d times, direct %v", f.opens, ringOf(t, s).direct)
	}
	o.Direct = false
	if err := s.Set(o); err != nil {
		t.Fatal(err)
	}
	if f.opens != 3 || ringOf(t, s).direct {
		t.Fatalf("after Direct was cleared the driver opened %d times, direct %v", f.opens, ringOf(t, s).direct)
	}
	waitFor(t, "the ring to fill", ringOf(t, s).primed.Load)
}
