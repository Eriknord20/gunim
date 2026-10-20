package maximizer

import "math"

// True peaks: the peaks of the sound as it would be between its samples,
// as a converter rebuilds it, which can rise over every sample, as ITU-R
// BS.1770 measures them: the sound four times over, through a filter
// that draws the curve between the samples, its loudest of the four.

// The oversampling filter: tpTaps taps for each of tpPhases phases, a
// windowed sinc; it delays the sound tpDelay frames.
const (
	tpPhases = 4
	tpTaps   = 12
	tpDelay  = tpTaps / 2
)

// tpFilter is the filter's taps, by phase.
var tpFilter = func() [tpPhases][tpTaps]float64 {
	var f [tpPhases][tpTaps]float64
	n := tpPhases * tpTaps
	mid := float64(n-1) / 2
	for i := range n {
		x := (float64(i) - mid) / tpPhases
		sinc := 1.0
		if x != 0 {
			sinc = math.Sin(math.Pi*x) / (math.Pi * x)
		}
		// Blackman, so the filter rings little.
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/float64(n-1))
		f[i%tpPhases][i/tpPhases] = sinc * w
	}
	return f
}()

// truePeak finds a channel's true peaks: the last tpTaps samples, at
// in a ring held twice over, end to end, so the last tpTaps are always
// side by side, from at on.
type truePeak struct {
	ring [2 * tpTaps]float64
	at   int
}

// push takes the next sample, and returns the true peak about the one
// tpDelay before it, a ratio of full scale.
//
// The filter is symmetric: its fourth phase is its first backwards, and
// its third its second. So each pair of phases is worked out from the
// sums and differences of the samples, each a pair of them as far from
// the middle of the window: half the products.
func (t *truePeak) push(x float64) float64 {
	w := t.add(x)
	h := &tpHalves
	var e0, o0, e1, o1 float64
	for k := range tpTaps / 2 {
		s, d := w[k]+w[tpTaps-1-k], w[k]-w[tpTaps-1-k]
		e0 += h[0][k] * s
		o0 += h[1][k] * d
		e1 += h[2][k] * s
		o1 += h[3][k] * d
	}
	return max(math.Abs(e0+o0), math.Abs(e0-o0), math.Abs(e1+o1), math.Abs(e1-o1))
}

// tpHalves are the first and second phases of the filter, halved: each
// tap's mean with the one as far from the other end, and half their
// difference. A phase's sum is the means times the sums of the samples
// they stand for, plus the half differences times their differences;
// its mirror's, minus.
var tpHalves = func() (h [4][tpTaps / 2]float64) {
	for k := range tpTaps / 2 {
		for p := range 2 {
			a, b := tpFilter[p][k], tpFilter[p][tpTaps-1-k]
			h[2*p][k], h[2*p+1][k] = (a+b)/2, (a-b)/2
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
