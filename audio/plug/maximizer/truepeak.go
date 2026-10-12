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
// in a ring.
type truePeak struct {
	ring [tpTaps]float64
	at   int
}

// push takes the next sample, and returns the true peak about the one
// tpDelay before it, a ratio of full scale.
func (t *truePeak) push(x float64) float64 {
	t.ring[t.at] = x
	t.at = (t.at + 1) % tpTaps
	peak := 0.0
	for p := range tpPhases {
		var y float64
		taps := &tpFilter[p]
		for k := range tpTaps {
			// The newest sample under the last tap.
			y += taps[tpTaps-1-k] * t.ring[(t.at-1-k+2*tpTaps)%tpTaps]
		}
		peak = max(peak, math.Abs(y))
	}
	return peak
}

// reset forgets the samples before.
func (t *truePeak) reset() { *t = truePeak{} }
