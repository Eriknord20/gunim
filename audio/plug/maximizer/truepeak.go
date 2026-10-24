package maximizer

import "math"

// True peaks: the peaks of the sound as it would be between its samples,
// as a converter rebuilds it, which can rise over every sample, as ITU-R
// BS.1770 measures them: the sound eight times over, through a filter
// that draws the curve between the samples, its loudest of the eight.

// The oversampling filter: tpTaps taps for each of tpPhases phases, a
// Kaiser-windowed sinc; it delays the sound tpDelay frames. push is
// written out for eight phases.
const (
	tpPhases = 8
	tpTaps   = 64
	tpBeta   = 8.0
	tpDelay  = tpTaps / 2
)

// tpFilter is the filter's taps, by phase: phase p draws the sound
// p/tpPhases of a frame after the middle sample, each phase scaled to
// pass a steady level unchanged.
var tpFilter = func() [tpPhases][tpTaps]float64 {
	var f [tpPhases][tpTaps]float64
	mid := float64(tpTaps/2 - 1)
	for p := range tpPhases {
		sum := 0.0
		for k := range tpTaps {
			d := mid + float64(p)/tpPhases - float64(k)
			sinc := 1.0
			if d != 0 {
				sinc = math.Sin(math.Pi*d) / (math.Pi * d)
			}
			r := d / (tpTaps / 2)
			f[p][k] = sinc * bessel0(tpBeta*math.Sqrt(max(0, 1-r*r))) / bessel0(tpBeta)
			sum += f[p][k]
		}
		for k := range tpTaps {
			f[p][k] /= sum
		}
	}
	return f
}()

// bessel0 is the modified Bessel function of the first kind, of order 0.
func bessel0(x float64) float64 {
	s, term := 1.0, 1.0
	for k := 1; k < 40; k++ {
		term *= (x / 2 / float64(k)) * (x / 2 / float64(k))
		s += term
	}
	return s
}

// truePeak finds a channel's true peaks: the last tpTaps samples, at
// in a ring held twice over, end to end, so the last tpTaps are always
// side by side, from at on.
type truePeak struct {
	ring [2 * tpTaps]float64
	at   int
}

// push takes the next sample, and returns the true peak from the sample
// tpDelay before it to the next, a ratio of full scale.
//
// The filter is symmetric: phase tpPhases-q is phase q backwards, and
// phase 0 is the middle sample itself. So each pair of phases is worked
// out from the sums and differences of the samples, each a pair of them
// as far from the middle of the window: half the products.
func (t *truePeak) push(x float64) float64 {
	w := t.add(x)
	var e1, o1, e2, o2, e3, o3, e4 float64
	for k := range tpTaps / 2 {
		s, d := w[k]+w[tpTaps-1-k], w[k]-w[tpTaps-1-k]
		h := &tpHalves[k]
		e1 += h[0] * s
		o1 += h[1] * d
		e2 += h[2] * s
		o2 += h[3] * d
		e3 += h[4] * s
		o3 += h[5] * d
		e4 += h[6] * s
	}
	return max(math.Abs(w[tpTaps/2-1]), math.Abs(e1+o1), math.Abs(e1-o1), math.Abs(e2+o2), math.Abs(e2-o2),
		math.Abs(e3+o3), math.Abs(e3-o3), math.Abs(e4))
}

// tpHalves are phases 1 to tpPhases/2 of the filter, halved, by tap: for
// each phase in turn, the tap's mean with the one as far from the other
// end, and half their difference, but for the middle phase, whose taps
// are their own mirror. A phase's sum is the means times the sums of the
// samples they stand for, plus the half differences times their
// differences; its mirror's, minus.
var tpHalves = func() (h [tpTaps / 2][tpPhases - 1]float64) {
	for k := range tpTaps / 2 {
		for q := range tpPhases - 1 {
			a, b := tpFilter[q/2+1][k], tpFilter[q/2+1][tpTaps-1-k]
			h[k][q] = (a + b) / 2
			if q%2 == 1 {
				h[k][q] = (a - b) / 2
			}
		}
	}
	return h
}()

// add takes the next sample in, and returns the last tpTaps samples,
// oldest first.
func (t *truePeak) add(x float64) *[tpTaps]float64 {
	t.ring[t.at], t.ring[t.at+tpTaps] = x, x
	t.at++
	if t.at == tpTaps {
		t.at = 0
	}
	return (*[tpTaps]float64)(t.ring[t.at : t.at+tpTaps])
}

// run pushes each of x in turn, and puts the true peak each returns in
// out, as long as x.
func (t *truePeak) run(x, out []float64) {
	out = out[:len(x)]
	for i, v := range x {
		out[i] = t.push(v)
	}
}

// reset forgets the samples before.
func (t *truePeak) reset() { *t = truePeak{} }
