package plugui

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// The wheel steps a knob as an arrow key does, while the knob has the keyboard, as a number field takes it. Over a
// knob without the keyboard the wheel passes by, so the pane round it scrolls.
func TestTheWheelStepsAKnobWithTheKeyboard(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	width := plug.Param{ID: 2, Name: "Width", Max: 1}
	h := &host{values: map[uint32]float64{1: 0.5, 2: 0.5}}
	a, b := NewKnob(h, mix), NewKnob(h, width)
	w := stageRow(t, a, b)
	overA, overB := geom.Pt(KnobW/2, 20), geom.Pt(KnobW+KnobW/2, 20)
	wheel := func(at geom.Point, dy float32) {
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.Scroll{Pos: at, Delta: geom.Pt(0, dy), Notches: geom.Pt(0, dy/40), Time: time.Now()})
		w.Frame(time.Second / 60)
	}
	wheel(overA, 40)
	if h.values[1] != 0.5 || len(h.log) != 0 {
		t.Fatalf("without the keyboard, the wheel turned the knob to %v", h.values[1])
	}
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Frame(time.Second / 60)
	wheel(overA, 40)
	if got := h.values[1]; got != 0.51 {
		t.Fatalf("a notch up turned the knob with the keyboard to %v, want a step up, 0.51", got)
	}
	wheel(overA, -40)
	wheel(overA, -40)
	if got := h.values[1]; got != 0.49 {
		t.Fatalf("two notches down turned the knob to %v, want 0.49", got)
	}
	if want := []string{"begin", "set", "end", "begin", "set", "end", "begin", "set", "end"}; !slices.Equal(h.log, want) {
		t.Errorf("the notches sent %v, want a gesture each", h.log)
	}
	wheel(overB, 40)
	if h.values[2] != 0.5 {
		t.Errorf("the wheel over the knob beside, without the keyboard, turned it to %v", h.values[2])
	}
	// A knob without the keyboard leaves the wheel to the pane it is in.
	if b.Handle(input.Scroll{Delta: geom.Pt(0, 40)}, nil) {
		t.Error("the knob without the keyboard took the wheel")
	}
}
