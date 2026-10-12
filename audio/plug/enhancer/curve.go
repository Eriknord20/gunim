package enhancer

import "math"

// The curves a band is driven through, by style: each rises at 1 through
// 0, so a quiet band passes it as it was, and bends as the band grows,
// which is what adds its harmonics. Each comes with its integral, so
// the enhancer can take a curve's mean between two frames, which aliases
// far less than the curve read at each frame.

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

// mean returns style s's curve's mean from u0 to u1: its integral's
// rise over the run, or, where they are too near for that to hold, the
// curve halfway.
func mean(s int, u0, u1 float64) float64 {
	if d := u1 - u0; math.Abs(d) > 1e-6 {
		return (integral(s, u1) - integral(s, u0)) / d
	}
	return curve(s, (u0+u1)/2)
}
