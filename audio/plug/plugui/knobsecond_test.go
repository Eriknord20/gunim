package plugui

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// A second button pressed mid-turn ends the turn where it got to: the
// gesture ends once, and the pointer moving on turns nothing.
func TestASecondButtonEndsAKnobTurn(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 100}}
	k := NewKnob(h, release)
	w := gunimtest.New(t, geom.Sz(KnobW, KnobH), nil)
	gunim.RegisterView(w, "knob", func(struct{}) gunim.Node { return k }, nil)
	if err := w.Client().Mount(gunim.Root, "knob", "knob", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	at := geom.Pt(KnobW/2, 20)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: at.Sub(geom.Pt(0, 10))})
	w.Frame(time.Second / 60)
	w.Input(input.PointerDown{Pos: at.Sub(geom.Pt(0, 10)), Button: input.ButtonSecondary, Clicks: 1})
	w.Frame(time.Second / 60)
	if k.Dragging() || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Fatalf("after a second button the turn went %v, dragging %v; want it ended", h.log, k.Dragging())
	}
	v := h.values[3]
	for y := float32(10); y > -40; y -= 8 {
		w.Input(input.PointerMove{Pos: geom.Pt(at.X, y)})
		w.Frame(time.Second / 60)
		if h.values[3] != v {
			t.Fatalf("after a second button, the pointer at %v turned the knob from %v to %v", y, v, h.values[3])
		}
	}
	w.Input(input.PointerUp{Pos: geom.Pt(at.X, -40), Button: input.ButtonSecondary})
	w.Input(input.PointerUp{Pos: geom.Pt(at.X, -40), Button: input.ButtonPrimary})
	w.Frame(time.Second / 60)
	if len(h.log) != 3 {
		t.Fatalf("letting go after the turn ended went on with %v", h.log[3:])
	}
}
