// Package xover splits a sound into four bands at three crossovers, for
// plugins that work on each band apart, so that the bands add back to
// the sound shifted in phase alone, and flat.
//
// The crossovers are Linkwitz-Riley, of the fourth order, each two
// Butterworth sections, whose low and high halves add back to the sound
// shifted in phase alone. The bands under a crossover pass through the
// same shift, as its halves added, so all four add back alike.
package xover

import "math"

// Bands is how many bands a splitter splits a sound into.
const Bands = 4

// Order returns the crossovers x in order, as a splitter works them:
// each at least half an octave over the one under it.
func Order(x [Bands - 1]float64) [Bands - 1]float64 {
	for i := 1; i < len(x); i++ {
		x[i] = max(x[i], x[i-1]*math.Sqrt2)
	}
	return x
}

// biquad is one second-order section and its state, in transposed direct
// form II.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             float64
}

// run filters x. What the section holds rounds to zero under tiny:
// left alone, it decays in silence to a subnormal number and sticks
// there, and sums on subnormals run many times slower, for as long as
// the silence lasts.
func (q *biquad) run(x float64) float64 {
	y := q.b0*x + q.z1
	q.z1 = q.b1*x - q.a1*y + q.z2
	q.z2 = q.b2*x - q.a2*y
	if math.Abs(q.z1) < tiny {
		q.z1 = 0
	}
	if math.Abs(q.z2) < tiny {
		q.z2 = 0
	}
	return y
}

// tiny is the level under which a section's state rounds to zero: some
// 600 decibels under full scale.
const tiny = 1e-30

// setPass sets the section to pass the pitches under hz, or, high, over
// it, Butterworth, as Robert Bristow-Johnson's cookbook has it; its
// state stays.
func (q *biquad) setPass(hz, rate float64, high bool) {
	w := 2 * math.Pi * min(hz, rate*0.45) / rate
	// Q of 1/√2: alpha is sin(w)/(2Q).
	cos, alpha := math.Cos(w), math.Sin(w)*math.Sqrt2/2
	a0 := 1 + alpha
	if high {
		q.b0, q.b1, q.b2 = (1+cos)/2/a0, -(1+cos)/a0, (1+cos)/2/a0
	} else {
		q.b0, q.b1, q.b2 = (1-cos)/2/a0, (1-cos)/a0, (1-cos)/2/a0
	}
	q.a1, q.a2 = -2*cos/a0, (1-alpha)/a0
}

// lr4 is a fourth-order Linkwitz-Riley half: two Butterworth sections.
type lr4 [2]biquad

func (f *lr4) set(hz, rate float64, high bool) {
	f[0].setPass(hz, rate, high)
	f[1].setPass(hz, rate, high)
}

func (f *lr4) run(x float64) float64 { return f[1].run(f[0].run(x)) }

func (f *lr4) reset() {
	for i := range f {
		f[i].z1, f[i].z2 = 0, 0
	}
}

// allpass shifts the phase as a crossover's halves added do: its low
// half and its high half, added.
type allpass struct{ lo, hi lr4 }

func (a *allpass) set(hz, rate float64) {
	a.lo.set(hz, rate, false)
	a.hi.set(hz, rate, true)
}

func (a *allpass) run(x float64) float64 { return a.lo.run(x) + a.hi.run(x) }

func (a *allpass) reset() {
	a.lo.reset()
	a.hi.reset()
}

// Splitter splits a signal into four bands at three crossovers: each
// crossover splits what is over the one under it, and each band under a
// crossover passes through its phase too. Its zero value passes nothing
// until it is set.
type Splitter struct {
	lo, hi [Bands - 1]lr4
	// ap are the phase shifts: the lowest band's at the second and third
	// crossovers, the second band's at the third.
	ap [3]allpass
}

// Set sets the crossovers at x, in hertz, at the sample rate rate; what
// the filters hold stays, so a crossover can glide.
func (s *Splitter) Set(x [Bands - 1]float64, rate float64) {
	for i, hz := range x {
		s.lo[i].set(hz, rate, false)
		s.hi[i].set(hz, rate, true)
	}
	s.ap[0].set(x[1], rate)
	s.ap[1].set(x[2], rate)
	s.ap[2].set(x[2], rate)
}

// Split returns x's four bands, low to high.
func (s *Splitter) Split(x float64) [Bands]float64 {
	var out [Bands]float64
	rest := x
	for i := range Bands - 1 {
		out[i] = s.lo[i].run(rest)
		rest = s.hi[i].run(rest)
	}
	out[Bands-1] = rest
	out[0] = s.ap[1].run(s.ap[0].run(out[0]))
	out[1] = s.ap[2].run(out[1])
	return out
}

// Reset lets go what the filters hold.
func (s *Splitter) Reset() {
	for i := range s.lo {
		s.lo[i].reset()
		s.hi[i].reset()
	}
	for i := range s.ap {
		s.ap[i].reset()
	}
}
