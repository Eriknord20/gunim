package plugui

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
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

// padAt moves the pointer to at and sends a touchpad's run of fractions of a notch, a frame each.
func padAt(w *gunim.Window, at geom.Point, steps []float32, mods input.Mods) {
	for _, n := range steps {
		wheelAt(w, at, n, mods)
	}
}

// tenths returns n tenths of a notch, as a touchpad gives them.
func tenths(n int, sign float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = sign * 0.1
	}
	return out
}

// A touchpad turns the knob by how far the fingers move, as far as a drag of that length, ten times finer with Shift.
func TestATouchpadTurnsTheKnobAsADragDoes(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	at := geom.Pt(KnobW/2, 20)
	// A drag of 10 px
	dh := &host{values: map[uint32]float64{1: 0.25}}
	dk := NewKnob(dh, mix)
	dw := stageNode(t, dk)
	dw.Input(input.PointerMove{Pos: at})
	dw.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	dw.Input(input.PointerMove{Pos: at.Sub(geom.Pt(0, 10))})
	dw.Input(input.PointerUp{Pos: at.Sub(geom.Pt(0, 10)), Button: input.ButtonPrimary})
	drag := dh.values[1] - 0.25
	for _, c := range []struct {
		what string
		mods input.Mods
		want float64
	}{{"plain", 0, drag}, {"with Shift", input.ModShift, drag / 10}} {
		t.Run(c.what, func(t *testing.T) {
			h := &host{values: map[uint32]float64{1: 0.25}}
			w := stageNode(t, NewKnob(h, mix))
			// Ten tenths of a notch: 10 px of the fingers, as the desktop counts them
			padAt(w, at, tenths(10, 1), c.mods)
			if got := h.values[1] - 0.25; !near(got, c.want) {
				t.Errorf("a touchpad's 10 px turned the knob by %v, want %v as a drag of 10 px", got, c.want)
			}
		})
	}
}

// One run of a touchpad, its glide after the fingers lift included, turns the knob at most half way round.
func TestATouchpadRunTurnsTheKnobHalfWayAtMost(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	at := geom.Pt(KnobW/2, 20)
	h := &host{values: map[uint32]float64{1: 0}}
	w := stageNode(t, NewKnob(h, mix))
	// 400 px of swipe
	padAt(w, at, tenths(400, 1), 0)
	if got := h.values[1]; !near(got, 0.5) {
		t.Errorf("a 400 px swipe turned the knob from 0 to %v, want 0.5", got)
	}
	frames(w, 40)
	padAt(w, at, tenths(400, 1), 0)
	if got := h.values[1]; !near(got, 1) {
		t.Errorf("a second swipe, after a rest, turned the knob to %v, want 1", got)
	}
}

// Ctrl or Super with the wheel is the window's, as for zoom: the knob leaves it be, as it leaves Ctrl with a key.
func TestCtrlWithTheWheelPassesTheKnobBy(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	for _, m := range []input.Mods{input.ModControl, input.ModSuper} {
		h := &host{values: map[uint32]float64{1: 0.5}}
		w := stageNode(t, NewKnob(h, mix))
		wheelAt(w, geom.Pt(KnobW/2, 20), 1, m)
		if h.values[1] != 0.5 {
			t.Errorf("the wheel with %v turned the knob to %v", m, h.values[1])
		}
	}
}
