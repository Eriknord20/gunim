package enhancer

import "math"

// A band is driven at twice the rate it comes at, so what its curve adds
// over half the rate it comes at falls past what twice the rate can hold
// far less often, and is filtered off before the band goes back down,
// not folded back under it. Up and down, the band passes the same
// half-band filter: halfTaps either side of its middle, every other one
// none, a Kaiser window of halfBeta over a sinc, which passes all to
// about 20.6 kHz of 48 and keeps out about 70 dB from 27.4.
const (
	halfTaps = 31
	halfBeta = 7
)

// half is the filter's taps, from -halfTaps to halfTaps.
var half = func() (h [2*halfTaps + 1]float64) {
	for j := -halfTaps; j <= halfTaps; j++ {
		x := float64(j) / halfTaps
		w := bessel0(halfBeta*math.Sqrt(1-x*x)) / bessel0(halfBeta)
		s := 0.5
		if j != 0 {
			s = math.Sin(math.Pi*float64(j)/2) / (math.Pi * float64(j))
		}
		h[j+halfTaps] = s * w
	}
	return h
}()

// bessel0 returns the modified Bessel function of the first kind, of
// order zero, at x, as its series adds up.
func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; term > 1e-12*sum; k++ {
		term *= (x / 2 / float64(k)) * (x / 2 / float64(k))
		sum += term
	}
	return sum
}

// upAhead is how many frames after a frame the half-way point after it
// needs, and downAhead how many points after a point the filter down
// needs, at twice the rate.
const (
	upAhead   = (halfTaps + 1) / 2
	downAhead = halfTaps
)

// up is the filter's taps for the point half-way from a frame to the
// next, doubled, as between takes the frames: from upAhead-1 before the
// frame to upAhead after it.
var up = func() (u [2 * upAhead]float64) {
	for i := range u {
		u[i] = 2 * half[halfTaps-2*(i-upAhead+1)+1]
	}
	return u
}()

// between returns the point half-way from frame n of ring r to the next,
// at twice the rate: r's frames from upAhead-1 before n to upAhead after
// it, through the filter. r is a ring of size frames, kept twice over,
// one after the other, so they run on unbroken from any of them.
func between(r []float64, size, n int64) float64 {
	from := (n - upAhead + 1 + size) % size
	w := r[from : from+2*upAhead]
	sum := 0.0
	for i, u := range up {
		sum += u * w[i]
	}
	return sum
}

// down returns point m of ring r, at twice the rate, filtered for going
// back down: r's points from downAhead before m to downAhead after it.
// r is a ring of harmRing points, kept twice over, as between's is.
func down(r []float64, m int64) float64 {
	from := (m - downAhead + harmRing) % harmRing
	w := r[from : from+2*downAhead+1]
	sum := half[halfTaps] * w[downAhead]
	for j := 1; j <= halfTaps; j += 2 {
		sum += half[halfTaps+j] * (w[downAhead-j] + w[downAhead+j])
	}
	return sum
}
