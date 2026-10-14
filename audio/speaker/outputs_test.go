package speaker

import (
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/asio"
)

// TestARingOfFourKeepsItsChannels writes frames of four channels
// across the ring's end, and reads each back whole.
func TestARingOfFourKeepsItsChannels(t *testing.T) {
	q := newRing(5, 4)
	next, want := float32(0), float32(0)
	for range 20 {
		in := make([]float32, 4*3)
		for i := range 3 {
			for ch := range 4 {
				in[4*i+ch] = next + float32(ch)/10
			}
			next++
		}
		if n := q.push(in); n != 3 {
			t.Fatalf("pushed %d frames, want 3", n)
		}
		out := make([]float32, 4*5)
		n := q.pop(out)
		if n != 3 {
			t.Fatalf("read %d frames, want 3", n)
		}
		for i := range n {
			for ch := range 4 {
				if got := out[4*i+ch]; got != want+float32(ch)/10 {
					t.Fatalf("frame %v, channel %d read %v", want, ch, got)
				}
			}
			want++
		}
	}
	if q.push(make([]float32, 4*7)) != 5 {
		t.Error("a ring of 5 took more than 5 frames")
	}
}

// TestInterleavePutsTheMixFirst interleaves a pair and three more
// channels into frames of five.
func TestInterleavePutsTheMixFirst(t *testing.T) {
	main := []float32{1, 2, 11, 12}
	aux := []float32{3, 4, 5, 13, 14, 15}
	dst := make([]float32, 10)
	interleave(dst, main, aux, 5)
	if want := []float32{1, 2, 3, 4, 5, 11, 12, 13, 14, 15}; !slices.Equal(dst, want) {
		t.Errorf("interleaved %v, want %v", dst, want)
	}
}

// frameAux is an Aux that fills each of its channels with the mixer
// frame it is at, a millionth of it, as ramp fills the mixer's, and
// the channel's number, a tenth of it, past that; it counts its calls.
type frameAux struct{ calls atomic.Int64 }

func (a *frameAux) fill(dst []float32, channels int, at int64) {
	a.calls.Add(1)
	for i := range len(dst) / channels {
		for ch := range channels {
			dst[i*channels+ch] = float32(at+int64(i))*1e-6 + float32(ch+1)*0.1
		}
	}
}

// TestAuxPlaysInStepWithTheMix opens a driver on four outputs: the
// mixer plays on the first two, and Aux fills the other two, frame for
// frame in step with the mixer through the one ring.
func TestAuxPlaysInStepWithTheMix(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Latency: 300, Bits: 32, Outputs: 8})
	m := audio.NewMixer()
	m.Play(&ramp{}, audio.Options{})
	var aux frameAux
	s, err := Open(m, Options{Driver: "Fake", Latency: 50 * time.Millisecond, Fixed: true,
		Outputs: []int{0, 1, 4, 5}, Aux: aux.fill})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if f.cfg.FillN == nil || f.cfg.Fill != nil || !slices.Equal(f.cfg.Outputs, []int{0, 1, 4, 5}) {
		t.Fatalf("the driver opened with outputs %v, FillN %v", f.cfg.Outputs, f.cfg.FillN != nil)
	}
	if st := s.State(); st.Aux != 2 {
		t.Errorf("the state tells of %d aux channels, want 2", st.Aux)
	}
	waitFor(t, "the ring to fill", ringOf(t, s).primed.Load)
	// The voice fades in over its first frames: read past them.
	f.take()
	for range 4 {
		out := f.take()
		if len(out) != 4*256 {
			t.Fatalf("the driver took %d samples, want %d", len(out), 4*256)
		}
		for i := range 256 {
			fr := out[4*i : 4*i+4]
			mix := math.Round(float64(fr[0]) * 1e6)
			for ch, v := range fr[2:] {
				got := math.Round((float64(v) - float64(ch+1)*0.1) * 1e6)
				if got != mix {
					t.Fatalf("frame %d: aux channel %d is at mixer frame %v, the mix at %v", i, ch, got, mix)
				}
			}
		}
	}
	if aux.calls.Load() == 0 {
		t.Error("Aux was never called")
	}
}

// TestSetChangesTheOutputs sets other outputs, and finds the driver
// opened again on them; a change of Aux alone plays on without opening
// it again.
func TestSetChangesTheOutputs(t *testing.T) {
	f := useFake(t, asio.Info{Driver: "Fake", Rate: 48000, Buffer: 256, Bits: 32, Outputs: 8})
	m := audio.NewMixer()
	o := Options{Driver: "Fake", Outputs: []int{2, 3}}
	s, err := Open(m, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if f.cfg.Fill == nil || f.cfg.FillN != nil || s.State().Aux != 0 {
		t.Fatal("a driver on two outputs plays more than the mixer's pair")
	}
	o.Outputs = []int{2, 3, 6}
	if err := s.Set(o); err != nil {
		t.Fatal(err)
	}
	if f.opens != 2 || s.State().Aux != 1 {
		t.Fatalf("after Outputs changed the driver opened %d times, with %d aux channels", f.opens, s.State().Aux)
	}
	var aux frameAux
	o.Aux = aux.fill
	if err := s.Set(o); err != nil {
		t.Fatal(err)
	}
	if f.opens != 2 {
		t.Fatalf("a change of Aux opened the driver again")
	}
	waitFor(t, "the new Aux to be called", func() bool {
		f.take()
		return aux.calls.Load() > 0
	})
}
