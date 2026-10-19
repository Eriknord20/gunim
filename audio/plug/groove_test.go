package plug

import (
	"math"
	"testing"
)

func TestAGrooveMovesTheSecondStepOfEachPair(t *testing.T) {
	g := Groove{Grid: 0.25, Swing: 0.58, From: 4}
	for _, c := range []struct{ written, played float64 }{
		{4, 4},
		{4.25, 4.29},
		{4.5, 4.5},
		{4.125, 4.145},
		{4.375, 4.29 + 0.105},
		// Before From, the pairs run on back.
		{3.75, 3.79},
	} {
		if got := g.Played(c.written); math.Abs(got-c.played) > 1e-9 {
			t.Errorf("%v plays at %v, want %v", c.written, got, c.played)
		}
		if got := g.Written(c.played); math.Abs(got-c.written) > 1e-9 {
			t.Errorf("%v is written at %v, want %v", c.played, got, c.written)
		}
	}
}

func TestTheZeroGrooveIsStraight(t *testing.T) {
	var g Groove
	for _, b := range []float64{0, 0.3, 7.77} {
		if g.Played(b) != b || g.Written(b) != b {
			t.Errorf("the zero groove moves %v", b)
		}
	}
	if s := (Groove{Grid: 0.5, Swing: 0.5}); s.Played(0.5) != 0.5 {
		t.Error("a swing of a half moves the off-beat")
	}
}
