package xover

import (
	"math"
	"testing"
)

// levels plays a second of a sine at hz through a splitter at the
// crossovers x, and returns the level of each band, and of the bands
// added, in decibels, over its last half.
func levels(hz float64, x [Bands - 1]float64) (band [Bands]float64, sum float64) {
	var s Splitter
	s.Set(x, 48000)
	var e [Bands]float64
	var es float64
	for i := range 48000 {
		b := s.Split(math.Sin(2 * math.Pi * hz * float64(i) / 48000))
		if i < 24000 {
			continue
		}
		t := 0.0
		for k, v := range b {
			e[k] += v * v
			t += v
		}
		es += t * t
	}
	for k := range e {
		band[k] = 10 * math.Log10(e[k]/12000+1e-20)
	}
	return band, 10 * math.Log10(es/12000)
}

func TestTheBandsAddBackFlat(t *testing.T) {
	x := [Bands - 1]float64{120, 1200, 6000}
	for _, hz := range []float64{40, 100, 120, 150, 500, 1200, 2500, 6000, 9000, 16000} {
		if _, sum := levels(hz, x); math.Abs(sum) > 0.05 {
			t.Errorf("at %v Hz the bands add back %+.2f dB off", hz, sum)
		}
	}
}

func TestEachToneFallsInItsBand(t *testing.T) {
	x := [Bands - 1]float64{120, 1200, 6000}
	for k, hz := range []float64{40, 380, 2700, 15000} {
		band, _ := levels(hz, x)
		for j, v := range band {
			if j != k && v > band[k]-20 {
				t.Errorf("a tone at %v Hz is at %.1f dB in band %d, and %.1f dB in its own, %d", hz, v, j, band[k], k)
			}
		}
	}
}

func TestOrderKeepsTheCrossoversApart(t *testing.T) {
	got := Order([Bands - 1]float64{900, 300, 5000})
	if got[0] != 900 || got[1] < 900*math.Sqrt2 || got[2] != 5000 {
		t.Errorf("crossovers at 900, 300 and 5000 Hz are put at %v", got)
	}
}

func TestASplitterHoldsNoSubnormalsInTheSilenceAfterSound(t *testing.T) {
	var s Splitter
	s.Set([3]float64{120, 1000, 6000}, 48000)
	for i := range 48000 {
		s.Split(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	// Left alone, the sections decay in silence to subnormals and stick
	// there, many times slower to work.
	for range 48000 * 10 {
		s.Split(0)
	}
	check := func(f lr4) {
		t.Helper()
		for _, q := range f {
			if subnormal(q.z1) || subnormal(q.z2) {
				t.Fatalf("after ten seconds of silence, a section holds %g, %g", q.z1, q.z2)
			}
		}
	}
	for i := range s.lo {
		check(s.lo[i])
		check(s.hi[i])
	}
	for _, a := range s.ap {
		check(a.lo)
		check(a.hi)
	}
}

// subnormal reports whether v is a subnormal number: under the least
// normal one, and not zero.
func subnormal(v float64) bool { return v != 0 && math.Abs(v) < 0x1p-1022 }
