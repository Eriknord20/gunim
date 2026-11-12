package plugui

import (
	"testing"

	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
)

// A run of the wheel that scrolls the view round a knob to its end goes on scrolling nothing, and leaves the knob
// be; a notch after the run has rested turns the knob.
func TestAWheelRunThatScrollsAViewToItsEndLeavesTheKnobBe(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	for _, c := range []struct {
		what  string
		notch float32
	}{{"down", -1}, {"up", 1}} {
		t.Run(c.what, func(t *testing.T) {
			h := &host{values: map[uint32]float64{1: 0.5}}
			k := NewKnob(h, mix)
			// 60 px to scroll, the pointer on the knob all the way
			w := stageScroll(t, k, 160)
			w.TimeByFrames(true)
			over := geom.Pt(KnobW/2, 5)
			if c.notch > 0 {
				wheelAt(w, over, -2, 0)
				frames(w, 60)
				over = geom.Pt(KnobW/2, 10)
			}
			for range 5 {
				wheelAt(w, over, c.notch, 0)
			}
			if h.values[1] != 0.5 {
				t.Fatalf("five notches %s scrolled the view to its end and turned the knob to %v", c.what, h.values[1])
			}
			frames(w, 60)
			wheelAt(w, over, c.notch, 0)
			if h.values[1] == 0.5 {
				t.Errorf("a notch %s after the run rested, the view at its end, left the knob be", c.what)
			}
		})
	}
}
