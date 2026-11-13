package plugui

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// roomy is a view of 60 px to scroll round a knob at its top, which stays under over however far the view is scrolled.
type roomy struct {
	h    *host
	w    *gunim.Window
	sv   *widget.Scroll
	over geom.Point
}

func newRoomy(t *testing.T) *roomy {
	t.Helper()
	h := &host{values: map[uint32]float64{1: 0.5}}
	w, sv := stageInScroll(t, NewKnob(h, plug.Param{ID: 1, Name: "Mix", Max: 1}), 160)
	return &roomy{h: h, w: w, sv: sv, over: geom.Pt(KnobW/2, 10)}
}

// at jumps the view to share of the way from its start to its end.
func (r *roomy) at(share float32) {
	r.sv.JumpTo(share * 60)
	frames(r.w, 2)
}

// wheel turns the wheel by notches over the knob, lets the view settle, and returns how far the knob turned and the
// view scrolled.
func (r *roomy) wheel(notches float32) (turned float64, scrolled float32) {
	v, at := r.h.values[1], r.sv.Offset()
	wheelAt(r.w, r.over, notches, 0)
	frames(r.w, 30)
	return r.h.values[1] - v, r.sv.Offset() - at
}

// dropFocus is a patch that takes the keyboard from whatever has it, as a host does after a gesture.
type dropFocus struct{}

// pressKnob presses and lets go over the knob, and then takes the keyboard from it.
func (r *roomy) pressKnob(t *testing.T) {
	t.Helper()
	r.w.Input(input.PointerMove{Pos: r.over})
	r.w.Input(input.PointerDown{Pos: r.over, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	r.w.Input(input.PointerUp{Pos: r.over, Button: input.ButtonPrimary, Time: time.Now()})
	r.drop(t)
}

// drop takes the keyboard from whatever has it.
func (r *roomy) drop(t *testing.T) {
	t.Helper()
	gunim.RegisterPatch(r.w, "s", func(_ *widget.Scroll, _ dropFocus, u *gunim.UI) { u.Focus(nil) })
	if err := r.w.Client().Patch("s", dropFocus{}); err != nil {
		t.Fatal(err)
	}
	frames(r.w, 2)
}

// A knob in a view with room to scroll leaves the wheel to the view, at either end and in the middle: at an end the
// wheel turned on toward it does nothing, and in the middle it scrolls the view.
func TestAKnobInAViewWithRoomLeavesItTheWheelAtEitherEnd(t *testing.T) {
	for _, c := range []struct {
		what    string
		share   float32
		notches float32
		scrolls bool
	}{
		{"up at the start", 0, 1, false},
		{"down at the end", 1, -1, false},
		{"up in the middle", 0.5, 1, true},
		{"down in the middle", 0.5, -1, true},
	} {
		t.Run(c.what, func(t *testing.T) {
			r := newRoomy(t)
			r.at(c.share)
			turned, scrolled := r.wheel(c.notches)
			if turned != 0 {
				t.Errorf("the wheel turned the knob by %v", turned)
			}
			if c.scrolls != (scrolled != 0) {
				t.Errorf("the view scrolled by %v", scrolled)
			}
		})
	}
	// A run to the end and a rest, and more notches on
	r := newRoomy(t)
	r.w.TimeByFrames(true)
	for range 5 {
		r.wheel(-1)
	}
	frames(r.w, 60)
	if turned, _ := r.wheel(-3); turned != 0 {
		t.Errorf("three notches past the end after a rest turned the knob by %v", turned)
	}
}

// A knob pressed and let go keeps the wheel, wherever the view stands and with no keyboard, until the pointer
// leaves it; back over it, the view has the wheel again.
func TestAPressedKnobKeepsTheWheelUntilThePointerLeaves(t *testing.T) {
	r := newRoomy(t)
	r.pressKnob(t)
	for _, c := range []struct {
		share   float32
		notches float32
	}{{0, 1}, {0.5, 1}, {0.5, -1}, {1, -1}} {
		r.at(c.share)
		turned, scrolled := r.wheel(c.notches)
		if !near(turned, float64(c.notches)*wheelNotch) || scrolled != 0 {
			t.Errorf("%v of the way along, a pressed knob turned by %v for %v notches and the view scrolled by %v",
				c.share, turned, c.notches, scrolled)
		}
	}
	r.at(0.5)
	r.w.Input(input.PointerMove{Pos: geom.Pt(150, 50)})
	frames(r.w, 2)
	if turned, scrolled := r.wheel(-1); turned != 0 || scrolled == 0 {
		t.Errorf("after the pointer left and came back, the wheel turned the knob by %v and scrolled the view by %v",
			turned, scrolled)
	}
}

// A press on the knob that drags off it and lets go outside leaves the wheel to the view, once the knob has no
// keyboard.
func TestAKnobDraggedOffLeavesTheWheel(t *testing.T) {
	r := newRoomy(t)
	r.at(0.5)
	r.w.Input(input.PointerMove{Pos: r.over})
	r.w.Input(input.PointerDown{Pos: r.over, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	r.w.Input(input.PointerMove{Pos: geom.Pt(150, 10)})
	r.w.Input(input.PointerUp{Pos: geom.Pt(150, 10), Button: input.ButtonPrimary, Time: time.Now()})
	r.drop(t)
	r.w.Input(input.PointerMove{Pos: r.over})
	frames(r.w, 2)
	at := r.h.values[1]
	if turned, scrolled := r.wheel(1); turned != 0 || scrolled == 0 || r.h.values[1] != at {
		t.Errorf("after a drag off the knob, the wheel turned it by %v and scrolled the view by %v", turned, scrolled)
	}
}

// A knob with the keyboard takes the wheel in a view with room, at its ends too.
func TestAKnobWithTheKeyboardTakesTheWheelAtAViewsEnd(t *testing.T) {
	for _, c := range []struct {
		share   float32
		notches float32
	}{{0, 1}, {0.5, -1}, {1, -1}} {
		r := newRoomy(t)
		r.w.Input(input.KeyPress{Key: input.KeyTab})
		r.at(c.share)
		if turned, scrolled := r.wheel(c.notches); !near(turned, float64(c.notches)*wheelNotch) || scrolled != 0 {
			t.Errorf("%v of the way along, the knob with the keyboard turned by %v and the view scrolled by %v",
				c.share, turned, scrolled)
		}
	}
}

// A knob in a view whose content fits takes the wheel under the pointer, with no press, as with no view round it.
func TestAKnobInAViewThatFitsTakesTheWheel(t *testing.T) {
	h := &host{values: map[uint32]float64{1: 0.5}}
	w, _ := stageInScroll(t, NewKnob(h, plug.Param{ID: 1, Name: "Mix", Max: 1}), 80)
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	wheelAt(w, geom.Pt(KnobW/2, 20), -1, 0)
	if got := h.values[1]; !near(got, 0.525) {
		t.Errorf("two notches up and one down turned the knob in a view that fits to %v, want 0.525", got)
	}
	h2 := &host{values: map[uint32]float64{1: 0.5}}
	w2 := stageNode(t, NewKnob(h2, plug.Param{ID: 1, Name: "Mix", Max: 1}))
	wheelAt(w2, geom.Pt(KnobW/2, 20), -1, 0)
	if got := h2.values[1]; !near(got, 0.475) {
		t.Errorf("a notch down turned the knob with no view round it to %v, want 0.475", got)
	}
}

// boxed lays its child out h tall.
type boxed struct {
	kid gunim.Node
	h   float32
}

func (s *boxed) Children() []gunim.Node { return []gunim.Node{s.kid} }

func (s *boxed) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := geom.Sz(cs.Max.W, s.h)
	kids.At(0).Layout(gunim.Constraints{Min: size, Max: size})
	kids.At(0).Place(geom.Point{})
	return size
}

func (s *boxed) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// A knob in a view that fits, inside a view with room, leaves the wheel to the outer view.
func TestAKnobInAViewThatFitsInsideOneWithRoomLeavesItTheWheel(t *testing.T) {
	h := &host{values: map[uint32]float64{1: 0.5}}
	k := NewKnob(h, plug.Param{ID: 1, Name: "Mix", Max: 1})
	inner := widget.NewScroll(&column{kids: []gunim.Node{k}, tall: 80})
	w, outer := stageInScroll(t, &boxed{kid: inner, h: 90}, 400)
	over := geom.Pt(KnobW/2, 20)
	wheelAt(w, over, 1, 0)
	frames(w, 30)
	if h.values[1] != 0.5 || outer.Offset() != 0 {
		t.Errorf("a notch up at the outer view's start turned the knob to %v and scrolled the view to %v",
			h.values[1], outer.Offset())
	}
	wheelAt(w, over, -1, 0)
	frames(w, 30)
	if h.values[1] != 0.5 || outer.Offset() == 0 {
		t.Errorf("a notch down turned the knob to %v and scrolled the outer view to %v", h.values[1], outer.Offset())
	}
}
