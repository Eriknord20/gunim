package eq

import (
	"math"
	"testing"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug/tonal"
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
	for _, b := range autoBands(levelsOf(func(float64) float64 { return 0 }), tonal.Pink) {
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
		bands := autoBands(levels, tonal.Pink)
		target := autoTarget(levels, tonal.Pink)
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
	bands := autoBands(levelsOf(func(f float64) float64 { return -40 * max(0, math.Log2(60/f)) }), tonal.Pink)
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

func TestAutoBringsATrackToItsTarget(t *testing.T) {
	music := tonal.Music.At(tonal.Knee)
	shaped := levelsOf(music.Level)
	for _, b := range autoBands(shaped, music) {
		if math.Abs(float64(b.Gain)) > 0.05 {
			t.Errorf("for a track on the music slope, matched to it, band %d at %g Hz is set to %+.1f dB", b.ID, b.Freq,
				b.Gain)
		}
	}
	// Pink noise, matched to the music slope, leans down from the knee
	// up and from the knee down; a track on the slope, matched to pink,
	// leans the other way: the knee against an octave and a half either
	// side.
	bend := func(bands []audio.Band) float64 {
		k := audio.Response(bands, tonal.Knee)
		return k - (audio.Response(bands, tonal.Knee/3)+audio.Response(bands, tonal.Knee*3))/2
	}
	pinkToMusic := bend(autoBands(levelsOf(func(float64) float64 { return 0 }), music))
	musicToPink := bend(autoBands(shaped, tonal.Pink))
	if !(pinkToMusic > 3 && musicToPink < -3) {
		t.Errorf("the knee against either side: pink matched to the music slope bends %+.1f dB, the slope matched "+
			"to pink %+.1f dB; want 3 dB up and down", pinkToMusic, musicToPink)
	}
	// The knee moved up, the slope runs level further: pink matched to
	// it lifts 1 kHz more.
	low, high := autoBands(levelsOf(func(float64) float64 { return 0 }), tonal.Music.At(300)),
		autoBands(levelsOf(func(float64) float64 { return 0 }), tonal.Music.At(1500))
	if d := audio.Response(high, 1000) - audio.Response(low, 1000); !(d > 2) {
		t.Errorf("with the knee at 1.5 kHz, 1 kHz is lifted %+.1f dB more than with it at 300 Hz, want 2", d)
	}
}
