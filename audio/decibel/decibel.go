// Package decibel turns ratios into decibels and back, fast enough to
// do for every sample: a few multiplications and a division, where
// math.Log10 and math.Pow each cost as much as the rest of a sample's
// work in a plugin. Each is as near the exact value as a float64 holds
// to a few parts in ten billion: a ten-millionth of a decibel, far under
// anything heard or drawn.
package decibel

import "math"

// Of returns v, a ratio, in decibels: 20·log10(v), for v no less than
// 1e-9, which is -180 dB; less reads as 1e-9. It is for real levels: NaN
// and +Inf give a meaningless large number, where math.Log10 gives NaN
// and +Inf.
func Of(v float64) float64 {
	v = max(v, 1e-9)
	// v is m·2^e, m from 1 to 2; m is c·(1+r), c where m's 128th of the
	// octave starts, whose logarithm the table holds; so r is from 0 to
	// 1/128, and the series for ln(1+r) to r⁵ leaves out under 1e-13. A
	// whole power of two has r 0, and its decibels come out exact.
	bits := math.Float64bits(v)
	e := int(bits>>52&0x7ff) - 1023
	m := math.Float64frombits(bits&(1<<52-1) | 1023<<52)
	t := &logs[bits>>(52-logBits)&(1<<logBits-1)]
	r := m*t.inv - 1
	ln := r * (1 - r*(1.0/2-r*(1.0/3-r*(1.0/4-r*(1.0/5)))))
	return (float64(e)*math.Ln2 + t.ln + ln) * (20 / math.Ln10)
}

// logBits is how many of the mantissa's bits pick a row of logs.
const logBits = 7

// logs are, for each 128th of an octave, where it starts, c, as 1/c
// and ln c, of the c whose 1/c is held exactly.
var logs = func() (t [1 << logBits]struct{ inv, ln float64 }) {
	for i := range t {
		c := 1 + float64(i)/(1<<logBits)
		t[i].inv = 1 / c
		t[i].ln = -math.Log(t[i].inv)
	}
	return t
}()

// Gain returns db decibels as a ratio: 10^(db/20). Under about -6020 dB
// it is 0, and over 6000 dB it is as at 6000.
func Gain(db float64) float64 {
	// 2^x, x = n/64 + f, n whole and f from 0 to 1/64: 2^(n/64) by its
	// bits and a table of 64ths of an octave, and 2^f = e^y, y = f·ln 2
	// under 0.011, whose series to y⁵ leaves out under 3e-15.
	x := min(db, 6000) * (math.Ln10 / math.Ln2 / 20 * gainSteps)
	if x < -1000*gainSteps {
		return 0
	}
	n := math.Floor(x)
	y := (x - n) * (math.Ln2 / gainSteps)
	p := 1 + y*(1+y*(1.0/2+y*(1.0/6+y*(1.0/24+y*(1.0/120)))))
	k := int64(n)
	return p * gains[k&(gainSteps-1)] * math.Float64frombits(uint64(k>>6+1023)<<52)
}

// gainSteps is how many steps an octave of gains has.
const gainSteps = 64

// gains are 2 to the power of each 64th of an octave.
var gains = func() (t [gainSteps]float64) {
	for i := range t {
		t[i] = math.Exp2(float64(i) / gainSteps)
	}
	return t
}()
