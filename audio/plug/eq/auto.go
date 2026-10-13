package eq

import (
	"math"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug/tonal"
)

// Auto: the bands that bring the whole track, as it reaches the EQ, to
// the balance of a target: pink noise, which has as much power in each
// octave as in any other, as a mix's long-term spectrum leans toward,
// or brown noise, darker. The host measures the track's power in
// sixth-octave bands, where pink noise reads level; the EQ lifts where
// the track, against the target, falls under its average and lowers
// where it rises over, with a low shelf, six bells starting
// an octave apart and a high shelf: their gains fitted to the
// difference, and each bell's frequency too, within half an octave of
// where it starts.

// autoFreqs are the frequencies the track is measured at: a sixth of an
// octave apart, from autoLow to autoHigh.
var autoFreqs = func() []float64 {
	var out []float64
	for f := autoLow; f <= autoHigh*1.0001; f *= math.Pow(2, 1.0/6) {
		out = append(out, f)
	}
	return out
}()

// The span measured, and the span it is weighed over: outside it, the
// weight falls, so a track with little deep bass or air is not lifted
// there far past what the rest asks.
const (
	autoLow, autoHigh   = 25.0, 16000.0
	autoFullLow         = 50.0
	autoFullHigh        = 10000.0
	autoMostLift        = 6.0
	autoMostCut         = 9.0
	autoMostBand        = 9.0
	autoSmoothing       = 2   // sixth-octaves either side
	autoBellRoam        = 0.5 // octaves either side
	autoRidge           = 0.005
	autoGaussNewtonRuns = 8
)

// autoShape is the bands Auto fits, their gains to find.
var autoShape = []audio.Band{
	{ID: 1, Kind: audio.LowShelf, Freq: 80, Q: 0.707, On: true},
	{ID: 2, Kind: audio.Bell, Freq: 160, Q: 1.4, On: true},
	{ID: 3, Kind: audio.Bell, Freq: 320, Q: 1.4, On: true},
	{ID: 4, Kind: audio.Bell, Freq: 640, Q: 1.4, On: true},
	{ID: 5, Kind: audio.Bell, Freq: 1280, Q: 1.4, On: true},
	{ID: 6, Kind: audio.Bell, Freq: 2560, Q: 1.4, On: true},
	{ID: 7, Kind: audio.Bell, Freq: 5120, Q: 1.4, On: true},
	{ID: 8, Kind: audio.HighShelf, Freq: 10000, Q: 0.707, On: true},
}

// autoWeight is how much the fit heeds frequency f.
func autoWeight(f float64) float64 {
	switch {
	case f < autoFullLow:
		return 0.3 + 0.7*math.Log2(f/autoLow)/math.Log2(autoFullLow/autoLow)
	case f > autoFullHigh:
		return 0.3 + 0.7*math.Log2(autoHigh/f)/math.Log2(autoHigh/autoFullHigh)
	}
	return 1
}

// autoTarget returns how far to lift each of autoFreqs, in decibels,
// to bring levels, measured there, to to: their weighted average, each
// against to, less each, smoothed over a third of an octave either way,
// no further than autoMostLift up and autoMostCut down.
func autoTarget(levels []float64, to tonal.Target) []float64 {
	var sum, weights float64
	against := make([]float64, len(levels))
	for i, l := range levels {
		against[i] = l - to.Level(autoFreqs[i])
		w := autoWeight(autoFreqs[i])
		sum += w * against[i]
		weights += w
	}
	mean := sum / weights
	raw := make([]float64, len(levels))
	for i, l := range against {
		raw[i] = mean - l
	}
	out := make([]float64, len(raw))
	for i := range raw {
		var s, n float64
		for j := max(0, i-autoSmoothing); j <= min(len(raw)-1, i+autoSmoothing); j++ {
			w := float64(autoSmoothing + 1 - abs(i-j))
			s += w * raw[j]
			n += w
		}
		out[i] = max(-autoMostCut, min(s/n, autoMostLift))
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// autoBands returns the bands that bring levels, measured at autoFreqs,
// to to: autoShape, fitted, by weighted least squares, to the lift
// autoTarget asks over the bands' true curves, a few steps of
// Gauss-Newton: the gains, and the bells' frequencies.
func autoBands(levels []float64, to tonal.Target) []audio.Band {
	target := autoTarget(levels, to)
	bands := make([]audio.Band, len(autoShape))
	copy(bands, autoShape)
	// The parameters: each band's gain, then each bell's frequency, in
	// octaves from where it starts.
	type param struct {
		band  int
		octav bool
	}
	var params []param
	for j := range bands {
		params = append(params, param{band: j})
	}
	for j, b := range bands {
		if b.Kind == audio.Bell {
			params = append(params, param{band: j, octav: true})
		}
	}
	nudge := func(b audio.Band, p param, by float64) audio.Band {
		if p.octav {
			b.Freq = float32(float64(b.Freq) * math.Exp2(by))
		} else {
			b.Gain += float32(by)
		}
		return b
	}
	n := len(params)
	for range autoGaussNewtonRuns {
		// The curve the bands make now, and how each parameter moves it,
		// per decibel or octave.
		now := make([]float64, len(autoFreqs))
		for i, f := range autoFreqs {
			now[i] = audio.Response(bands, f)
		}
		slope := make([][]float64, n)
		for j, p := range params {
			slope[j] = make([]float64, len(autoFreqs))
			h := 0.5
			if p.octav {
				h = 0.05
			}
			up, down := nudge(bands[p.band], p, h), nudge(bands[p.band], p, -h)
			for i, f := range autoFreqs {
				slope[j][i] = (up.Response(f) - down.Response(f)) / (2 * h)
			}
		}
		// The normal equations, with a ridge so parameters that overlap
		// share the work rather than fight over it.
		a := make([][]float64, n)
		rhs := make([]float64, n)
		for j := range n {
			a[j] = make([]float64, n)
			for i, f := range autoFreqs {
				w := autoWeight(f)
				rhs[j] += w * slope[j][i] * (target[i] - now[i])
				for k := range n {
					a[j][k] += w * slope[j][i] * slope[k][i]
				}
			}
			a[j][j] += autoRidge*float64(len(autoFreqs)) + 1e-9
		}
		step := solve(a, rhs)
		for j, p := range params {
			b := &bands[p.band]
			if p.octav {
				home := float64(autoShape[p.band].Freq)
				o := math.Log2(float64(b.Freq)/home) + max(-0.25, min(step[j], 0.25))
				b.Freq = float32(home * math.Exp2(max(-autoBellRoam, min(o, autoBellRoam))))
			} else {
				b.Gain = float32(max(-autoMostBand, min(float64(b.Gain)+step[j], autoMostBand)))
			}
		}
	}
	for j := range bands {
		// Tenths of a decibel, and whole hertz, are as fine as an ear,
		// or the strip, tells.
		bands[j].Gain = float32(math.Round(float64(bands[j].Gain)*10) / 10)
		bands[j].Freq = float32(math.Round(float64(bands[j].Freq)))
	}
	return bands
}

// solve solves a x = b by Gaussian elimination with partial pivoting,
// in place, and returns x; a column with nothing to pivot on is zero.
func solve(a [][]float64, b []float64) []float64 {
	n := len(b)
	for c := range n {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		a[c], a[p] = a[p], a[c]
		b[c], b[p] = b[p], b[c]
		if math.Abs(a[c][c]) < 1e-12 {
			continue
		}
		for r := c + 1; r < n; r++ {
			f := a[r][c] / a[c][c]
			for k := c; k < n; k++ {
				a[r][k] -= f * a[c][k]
			}
			b[r] -= f * b[c]
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		if math.Abs(a[r][r]) < 1e-12 {
			continue
		}
		s := b[r]
		for k := r + 1; k < n; k++ {
			s -= a[r][k] * x[k]
		}
		x[r] = s / a[r][r]
	}
	return x
}
