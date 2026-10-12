package eq

import (
	"math"
	"testing"

	"github.com/marrasen/gunim/audio"
)

// levelsOf returns levels at autoFreqs, a pink level of -20 dB with
// shape added.
func levelsOf(shape func(f float64) float64) []float64 {
	out := make([]float64, len(autoFreqs))
	for i, f := range autoFreqs {
		out[i] = -20 + shape(f)
	}
	return out
}

// fitError returns how far, in decibels, the curve of bands misses the
// lift target asks, the worst at a fully weighed frequency.
func fitError(bands []audio.Band, target []float64) float64 {
	worst := 0.0
	for i, f := range autoFreqs {
		if autoWeight(f) < 1 {
			continue
		}
		worst = max(worst, math.Abs(audio.Response(bands, f)-target[i]))
	}
	return worst
}

func TestAutoLeavesPinkAlone(t *testing.T) {
	for _, b := range autoBands(levelsOf(func(float64) float64 { return 0 })) {
		if math.Abs(float64(b.Gain)) > 0.05 {
			t.Errorf("for pink noise, band %d at %g Hz is set to %+.1f dB", b.ID, b.Freq, b.Gain)
		}
	}
}

func TestAutoEvensABumpAndATilt(t *testing.T) {
	for name, shape := range map[string]func(f float64) float64{
		// 6 dB too much about 1 kHz, an octave wide.
		"a bump": func(f float64) float64 { o := math.Log2(f / 1000); return 6 * math.Exp(-o*o) },
		// Brighter and brighter over 1 kHz, 1.5 dB an octave.
		"a tilt": func(f float64) float64 { return 1.5 * max(0, math.Log2(f/1000)) },
		// Too little low end under 200 Hz.
		"thin": func(f float64) float64 { return -4 * max(0, math.Log2(200/f)) / 3 },
	} {
		levels := levelsOf(shape)
		bands := autoBands(levels)
		target := autoTarget(levels)
		if e := fitError(bands, target); e > 1 {
			t.Errorf("for %s, the bands miss what it asks by as much as %.2f dB", name, e)
		}
		// Evened: what comes out, the track and the bands together, is
		// flatter than what went in.
		spread := func(lift bool) float64 {
			lo, hi := math.Inf(1), math.Inf(-1)
			for i, f := range autoFreqs {
				if autoWeight(f) < 1 {
					continue
				}
				l := levels[i]
				if lift {
					l += audio.Response(bands, f)
				}
				lo, hi = min(lo, l), max(hi, l)
			}
			return hi - lo
		}
		if before, after := spread(false), spread(true); after > before/3 {
			t.Errorf("for %s, the levels spread %.1f dB, and %.1f dB after", name, before, after)
		}
	}
}

func TestAutoHoldsBackWhereTheTrackHasLittle(t *testing.T) {
	// Next to nothing in the deep bass: the shelf lifts no further than
	// autoMostLift.
	bands := autoBands(levelsOf(func(f float64) float64 { return -40 * max(0, math.Log2(60/f)) }))
	if g := audio.Response(bands, 30); g > autoMostLift+0.5 {
		t.Errorf("for a track with no deep bass, the EQ lifts 30 Hz by %+.1f dB", g)
	}
	for _, b := range bands {
		if math.Abs(float64(b.Gain)) > autoMostBand {
			t.Errorf("band %d is set to %+.1f dB, past %g", b.ID, b.Gain, autoMostBand)
		}
		if err := checkBand(b); err != nil {
			t.Errorf("band %d %v", b.ID, err)
		}
	}
	if len(bands) > maxBands {
		t.Errorf("Auto makes %d bands, more than the %d an EQ has", len(bands), maxBands)
	}
}
