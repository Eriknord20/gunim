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
