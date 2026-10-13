// Package tonal holds the balances a mastering plugin can bring a track
// to, by pitch: how loud a track sounds even at each pitch, as one
// measured in bands a fraction of an octave wide reads it, where pink
// noise reads level.
package tonal

import "math"

// Target is a balance a track is brought to: Below is how many decibels
// it rises each octave up, under Knee, and Above how many over it, as
// bands a fraction of an octave wide read it, past pink noise's, which
// reads level. A target where both are the same has no knee to speak
// of, and Kneed is false.
type Target struct {
	// Name names it in a sentence, as "pink noise"; Short names it where
	// there is little room; Hint says its slope, short; and Tip says
	// what it is.
	Name, Short, Hint, Tip string
	Below, Above           float64
	Knee                   float64
	Kneed                  bool
}

// The knee's bounds, and where it stands at first: about where recorded
// music, by the hertz, stops running level and starts to fall.
const (
	KneeLow  = 200.0
	KneeHigh = 2000.0
	Knee     = 550.0
)

// The targets: pink noise, as much power in each octave as in any
// other, falling 3 dB an octave by the hertz; and the slope recorded
// music commonly takes, by the hertz level up to its knee and falling
// 6 dB an octave over it: against pink, rising 3 dB an octave under the
// knee and falling 3 dB an octave over it.
var (
	Pink = Target{Name: "pink noise", Short: "Pink", Hint: "-3 dB/octave", Knee: Knee,
		Tip: "Pink noise: as much in each octave as in any other, falling 3 dB an octave by the hertz"}
	Music = Target{Name: "music slope", Short: "Music", Hint: "level, then -6 dB/octave", Below: 3, Above: -3,
		Knee: Knee, Kneed: true,
		Tip: "The slope recorded music commonly takes: level by the hertz up to the knee, falling 6 dB an octave over it"}
)

// Targets are the targets, in the order a choice of them shows them.
var Targets = []Target{Pink, Music}

// TargetAt returns the target at i in Targets, rounded, and pink where
// i is out of them.
func TargetAt(i float64) Target {
	if k := int(math.Round(i)); k >= 0 && k < len(Targets) {
		return Targets[k]
	}
	return Pink
}

// At returns t with its knee at knee hertz, kept within its bounds.
func (t Target) At(knee float64) Target {
	t.Knee = max(KneeLow, min(knee, KneeHigh))
	return t
}

// Level returns how loud t is at f, in decibels, against pink noise, as
// bands a fraction of an octave wide read it: none at its knee.
func (t Target) Level(f float64) float64 {
	o := math.Log2(f / t.Knee)
	if o < 0 {
		return t.Below * o
	}
	return t.Above * o
}
