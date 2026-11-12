package plugui

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A value set from outside, as a patch picked or an undo, glides the knob's needle there; at rest it asks for no
// frames.
func TestAKnobGlidesToAValueSetElsewhere(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	w := stageNode(t, k)
	if got := k.turned.Value(); got != 0 {
		t.Fatalf("laid out at its least, the needle stands %v round", got)
	}
	gunim.RegisterPatch(w, "n", func(k *Knob, v float64, u *gunim.UI) { k.Follow(v, u) })
	if err := w.Client().Patch("n", 1000.0); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := k.turned.Value(); got <= 0.05 || got >= 0.95 {
		t.Fatalf("a frame after a patch change the needle stands %v round, want part way to 1", got)
	}
	frames(w, 60)
	if got := k.turned.Value(); got != 1 {
		t.Fatalf("settled, the needle stands %v round, want 1", got)
	}
	if k.Step(time.Second / 60) {
		t.Error("settled, the knob still asks for frames")
	}
}

// A drag moves the needle with the pointer on the same frame, with no glide behind it.
func TestADragMovesTheNeedleWithNoLag(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	w := stageNode(t, k)
	w.Input(input.PointerMove{Pos: geom.Pt(50, 90)})
	w.Input(input.PointerDown{Pos: geom.Pt(50, 90), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Frame(time.Second / 60)
	for _, y := range []float32{70, 40, 10} {
		w.Input(input.PointerMove{Pos: geom.Pt(50, y)})
		w.Frame(time.Second / 60)
		if want := float32(k.turnOf(k.Value())); math.Abs(float64(k.turned.Value()-want)) > 1e-6 {
			t.Fatalf("dragged to %v, the needle stands %v round, want %v at once", y, k.turned.Value(), want)
		}
	}
	w.Input(input.PointerUp{Pos: geom.Pt(50, 10), Button: input.ButtonPrimary, Time: time.Now()})
	w.Frame(time.Second / 60)
}

// Set with no UI, as a view builds a knob, the needle stands at the value at once.
func TestAKnobFollowedWithNoUIJumps(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	tp := NewTrimPot(h, release)
	tp.Layout(gunim.Tight(geom.Sz(TrimPotSize, TrimPotSize)), gunim.Frame{}, gunim.Children{})
	tp.Follow(1000, nil)
	if got := tp.turned.Value(); got != 1 {
		t.Errorf("followed with no UI, the slot stands %v round, want 1 at once", got)
	}
}

// A selector's lamp and a power lamp set from outside glide too.
func TestLampsSetElsewhereGlide(t *testing.T) {
	s := NewSelector("Mode", []string{"A", "B", "C"}, nil)
	w := stageNode(t, s)
	gunim.RegisterPatch(w, "n", func(s *Selector, i int, u *gunim.UI) { s.SetSelected(i, u) })
	if err := w.Client().Patch("n", 2); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := s.lit[2].Value(); got <= 0 || got >= 1 {
		t.Errorf("a frame after it is set, C's lamp is %v lit, want part way", got)
	}
	p := NewPower("gate")
	w = stageNode(t, p)
	gunim.RegisterPatch(w, "n", func(p *Power, on bool, u *gunim.UI) { p.SetChecked(on, u) })
	if err := w.Client().Patch("n", true); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if got := p.on.Value(); got <= 0 || got >= 1 {
		t.Errorf("a frame after it is set on, the power lamp is %v lit, want part way", got)
	}
}
