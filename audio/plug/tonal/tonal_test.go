package tonal

import (
	"math"
	"testing"
)

func TestTheMusicSlopeRunsLevelThenFallsSixDecibelsAnOctave(t *testing.T) {
	// By the hertz, a band an octave up holds twice the hertz, 3 dB
	// more: what reads level by the hertz rises 3 dB an octave against
	// pink, and what falls 6 dB falls 3 against it.
	m := Music.At(550)
	for _, c := range []struct{ f, want float64 }{{50, 3}, {200, 3}, {1100, -3}, {4000, -3}} {
		if d := m.Level(2*c.f) - m.Level(c.f); !(math.Abs(d-c.want) < 1e-9) {
			t.Errorf("the music slope moves %+.2f dB from %g Hz to the octave up, against pink, want %+g", d, c.f, c.want)
		}
	}
	if m.Level(550) != 0 || Pink.Level(123) != 0 || Pink.At(900).Level(5000) != 0 {
		t.Error("a target is not level where it should be")
	}
	if Music.At(800).Level(800) != 0 || !(Music.At(800).Level(400) < 0) {
		t.Error("the knee did not move with At")
	}
	if Music.At(10).Knee != KneeLow || Music.At(1e5).Knee != KneeHigh {
		t.Error("At let the knee out of its bounds")
	}
	if TargetAt(1) != Music || TargetAt(0) != Pink || TargetAt(7) != Pink {
		t.Error("TargetAt picks the wrong target")
	}
}
