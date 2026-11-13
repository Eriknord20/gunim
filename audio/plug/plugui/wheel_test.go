package plugui

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// wheelAt moves the pointer to at and turns the wheel there by notches, as many pixels as the desktop gives.
func wheelAt(w *gunim.Window, at geom.Point, notches float32, mods input.Mods) {
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.Scroll{Pos: at, Delta: geom.Pt(0, 40*notches), Notches: geom.Pt(0, notches), Mods: mods,
		Time: time.Now()})
	w.Frame(time.Second / 60)
}

// near reports whether a and b differ by less than a millionth.
func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// The wheel turns the knob under the pointer, with no click first, and leaves the one beside it be.
func TestTheWheelTurnsTheKnobUnderThePointer(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	width := plug.Param{ID: 2, Name: "Width", Max: 1}
	h := &host{values: map[uint32]float64{1: 0.5, 2: 0.5}}
	a, b := NewKnob(h, mix), NewKnob(h, width)
	w := stageRow(t, a, b)
	overA, overB := geom.Pt(KnobW/2, 20), geom.Pt(KnobW+KnobW/2, 20)
	wheelAt(w, overA, 1, 0)
	if got := h.values[1]; !near(got, 0.525) {
		t.Fatalf("a notch up over the knob turned it to %v, want 0.525", got)
	}
	// The last knob clicked, or with the keyboard, does not turn as the wheel turns over another.
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Frame(time.Second / 60)
	wheelAt(w, overB, -2, 0)
	if !near(h.values[2], 0.45) || !near(h.values[1], 0.525) {
		t.Errorf("two notches down over the second knob set it to %v and the first to %v, want 0.45 and 0.525",
			h.values[2], h.values[1])
	}
}

// A wheel event turns the knob by how far the wheel turned: three notches three times one, and a quarter as far with
// Shift. A touchpad's small steps turn it as a drag of the same length does.
func TestTheWheelTurnsByHowFarItTurned(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	for _, c := range []struct {
		what  string
		steps []float32
		mods  input.Mods
		want  float64
	}{
		{"one notch", []float32{1}, 0, 0.025},
		{"three notches at once", []float32{3}, 0, 0.075},
		{"a touchpad's ten tenths, 10 px", []float32{0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1}, 0, 10.0 / 180},
		{"one notch with Shift", []float32{1}, input.ModShift, 0.025 / 4},
		{"two notches down", []float32{-2}, 0, -0.05},
	} {
		t.Run(c.what, func(t *testing.T) {
			h := &host{values: map[uint32]float64{1: 0.5}}
			k := NewKnob(h, mix)
			w := stageNode(t, k)
			for _, n := range c.steps {
				wheelAt(w, geom.Pt(KnobW/2, 20), n, c.mods)
			}
			if got := h.values[1] - 0.5; !near(got, c.want) {
				t.Errorf("the wheel turned the knob by %v, want %v", got, c.want)
			}
		})
	}
}

// A stepped knob takes a step for each notch, and a touchpad takes one for as far as a drag takes one: a third of
// 180 px for three steps.
func TestTheWheelStepsASteppedKnob(t *testing.T) {
	slope := plug.Param{ID: 1, Name: "Slope", Min: 12, Max: 48, Steps: 3}
	h := &host{values: map[uint32]float64{1: 12}}
	k := NewKnob(h, slope)
	w := stageNode(t, k)
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	if h.values[1] != 24 {
		t.Fatalf("a notch up turned the slope to %v, want 24", h.values[1])
	}
	for range 61 {
		wheelAt(w, geom.Pt(KnobW/2, 20), 0.1, 0)
	}
	if h.values[1] != 36 {
		t.Errorf("61 px of a touchpad turned the slope to %v, want 36", h.values[1])
	}
}

// A run of wheel events on one knob is one step to undo; a turn after it has rested a while is another.
func TestOneTurnOfTheWheelIsOneUndoStep(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	h := &host{values: map[uint32]float64{1: 0.5}}
	k := NewKnob(h, mix)
	w := stageNode(t, k)
	for range 5 {
		wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	}
	frames(w, 6)
	wheelAt(w, geom.Pt(KnobW/2, 20), 1, 0)
	if want := []string{"begin", "set", "set", "set", "set", "set", "set"}; !slices.Equal(h.log, want) {
		t.Fatalf("a run of notches sent %v, want %v", h.log, want)
	}
	frames(w, 40)
	if h.log[len(h.log)-1] != "end" {
		t.Fatalf("half a second after the last notch, the turn is still open: %v", h.log)
	}
	h.log = nil
	wheelAt(w, geom.Pt(KnobW/2, 20), -1, 0)
	frames(w, 40)
	if want := []string{"begin", "set", "end"}; !slices.Equal(h.log, want) {
		t.Errorf("a notch after the knob rested sent %v, want a gesture of its own", h.log)
	}
}

// stageScroll mounts a knob at the top of a scroll view of content tall, the view 100 tall, and returns the window.
func stageScroll(t *testing.T, k *Knob, tall float32) *gunim.Window {
	t.Helper()
	w, _ := stageInScroll(t, k, tall)
	return w
}

// stageInScroll mounts n at the top of a scroll view of content tall, the view 100 tall, as the view "s", and returns
// the window and the view.
func stageInScroll(t *testing.T, n gunim.Node, tall float32) (*gunim.Window, *widget.Scroll) {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(200, 100), nil)
	content := &column{kids: []gunim.Node{n}, tall: tall}
	sv := widget.NewScroll(content)
	gunim.RegisterView(w, "s", func(struct{}) gunim.Node { return sv }, nil)
	if err := w.Client().Mount(gunim.Root, "s", "s", nil); err != nil {
		t.Fatal(err)
	}
	frames(w, 2)
	return w, sv
}

// column lays its children out from the top, in a box tall high.
type column struct {
	kids []gunim.Node
	tall float32
}

func (c *column) Children() []gunim.Node { return c.kids }

func (c *column) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	y := float32(0)
	for i := range kids.Len() {
		s := kids.At(i).Layout(gunim.Constraints{Max: cs.Max})
		kids.At(i).Place(geom.Pt(0, y))
		y += s.H
	}
	return geom.Sz(cs.Max.W, c.tall)
}

func (c *column) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for i := range kids.Len() {
		kids.At(i).Paint(p)
	}
}

// Inside a view with room to scroll, the knob leaves the wheel to the view unless it has the keyboard, at the view's
// top too.
func TestAKnobInAScrollingViewLeavesItTheWheel(t *testing.T) {
	mix := plug.Param{ID: 1, Name: "Mix", Max: 1}
	h := &host{values: map[uint32]float64{1: 0.5}}
	k := NewKnob(h, mix)
	w := stageScroll(t, k, 400)
	over := geom.Pt(KnobW/2, 20)
	// At the top, the wheel turned down scrolls the view on, and the knob is left be.
	wheelAt(w, over, -1, 0)
	if h.values[1] != 0.5 {
		t.Fatalf("in a view that scrolls, the wheel turned the knob without the keyboard to %v", h.values[1])
	}
	frames(w, 30)
	// At the view's top, a wheel turned up is still the view's, and leaves the knob be.
	h2 := &host{values: map[uint32]float64{1: 0.5}}
	k2 := NewKnob(h2, mix)
	w2 := stageScroll(t, k2, 400)
	wheelAt(w2, over, 1, 0)
	if h2.values[1] != 0.5 {
		t.Errorf("at the view's top, a notch up turned the knob to %v", h2.values[1])
	}
	// With the keyboard, the knob takes the wheel the view would scroll by.
	h3 := &host{values: map[uint32]float64{1: 0.5}}
	k3 := NewKnob(h3, mix)
	w3 := stageScroll(t, k3, 400)
	w3.Input(input.KeyPress{Key: input.KeyTab})
	w3.Frame(time.Second / 60)
	wheelAt(w3, over, -1, 0)
	if !near(h3.values[1], 0.475) {
		t.Errorf("with the keyboard, a notch down turned the knob to %v, want 0.475", h3.values[1])
	}
}

// The tooltip says the wheel turns the knob.
func TestTheKnobsTooltipMentionsTheWheel(t *testing.T) {
	k := NewKnob(&host{values: map[uint32]float64{3: 100}}, release)
	if tip := k.tooltip(); !strings.Contains(tip, "wheel") {
		t.Errorf("the tooltip reads %q, with no word of the wheel", tip)
	}
}
