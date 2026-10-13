package enhancer

import "math"

// The curves a band is driven through, by style: each rises at 1 through
// 0, so a quiet band passes it as it was, and bends as the band grows,
// which is what adds its harmonics. Each comes with its integral, so
// the enhancer can take a curve's mean between two points, which aliases
// far less than the curve read at each point.

// tubeGive is how far the tube's curve gives on its soft side, for
// which it bends as tape does stretched out that far, where its other
// side bends as tape does: unlike sides, which add the even harmonics
// first. crispHard is how much of the crisp curve is a hard clip, the
// rest tape's curve: a clip's corner reaches far up the harmonics.
const (
	tubeGive  = 4
	crispHard = 0.7
)

// curve returns style s's curve at u.
func curve(s int, u float64) float64 {
	switch s {
	case Tape:
		return math.Tanh(u)
	case Tube:
		if u < 0 {
			return tubeGive * math.Tanh(u/tubeGive)
		}
		return math.Tanh(u)
	case Warm:
		return (curve(Tape, u) + curve(Tube, u)) / 2
	default:
		return crispHard*max(-1, min(u, 1)) + (1-crispHard)*math.Tanh(u)
	}
}

// integral returns the integral of style s's curve from 0 to u.
func integral(s int, u float64) float64 {
	switch s {
	case Tape:
		return logCosh(u)
	case Tube:
		if u < 0 {
			return tubeGive * tubeGive * logCosh(u/tubeGive)
		}
		return logCosh(u)
	case Warm:
		return (integral(Tape, u) + integral(Tube, u)) / 2
	default:
		clip := u * u / 2
		if a := math.Abs(u); a > 1 {
			clip = a - 0.5
		}
		return crispHard*clip + (1-crispHard)*logCosh(u)
	}
}

// logCosh is the log of cosh u, worked out so it holds however large u.
func logCosh(u float64) float64 {
	a := math.Abs(u)
	return a + math.Log1p(math.Exp(-2*a)) - math.Ln2
}

// The tones carried is worked out for: from carriedLow to carriedHigh,
// as loud as they are driven, carriedPoints of them, as far apart each
// as the next, in octaves.
const (
	carriedLow    = 1e-3
	carriedHigh   = 1e4
	carriedPoints = 512
)

// carriedOf is, by style, how much of a tone each curve carries, at
// each of the levels carried is worked out for.
var carriedOf = func() (out [styles][carriedPoints]float64) {
	for s := range styles {
		for i := range carriedPoints {
			out[s][i] = fundamental(s, carriedLow*math.Pow(carriedHigh/carriedLow, float64(i)/(carriedPoints-1)))
		}
	}
	return out
}()

// fundamental returns how much of a tone driven to u style s's curve
// carries: the curve's pitch at the tone's, over u.
func fundamental(s int, u float64) float64 {
	const n = 512
	sum := 0.0
	for i := range n {
		sn := math.Sin(2 * math.Pi * (float64(i) + 0.5) / n)
		sum += curve(s, u*sn) * sn
	}
	return 2 * sum / n / u
}

// carried returns how much of a tone driven to u the curves mixed by w
// carry: none of it is lost where the curves still run straight, and as
// much as a tone driven to carriedHigh where it is louder still.
func carried(w *[styles]float64, u float64) float64 {
	if u <= carriedLow {
		return 1
	}
	at := min(math.Log(u/carriedLow)/math.Log(carriedHigh/carriedLow)*(carriedPoints-1), carriedPoints-1)
	i := min(int(at), carriedPoints-2)
	f := at - float64(i)
	g := 0.0
	for s, ws := range w {
		if ws >= 1e-4 {
			g += ws * (carriedOf[s][i]*(1-f) + carriedOf[s][i+1]*f)
		}
	}
	return max(g, 1e-3)
}
