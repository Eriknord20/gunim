package plugui

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
)

// stageNode mounts n alone in a 100 by 100 window and returns the window.
func stageNode(t *testing.T, n gunim.Node) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(100, 100), nil)
	gunim.RegisterView(w, "n", func(struct{}) gunim.Node { return n }, nil)
	if err := w.Client().Mount(gunim.Root, "n", "n", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return w
}

// press presses the primary button at the window's middle, and lets it go at up.
func press(w *gunim.Window, up geom.Point) (down, release func()) {
	mid := geom.Pt(50, 50)
	return func() {
			w.Input(input.PointerMove{Pos: mid})
			w.Input(input.PointerDown{Pos: mid, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
			w.Frame(time.Second / 60)
		}, func() {
			w.Input(input.PointerMove{Pos: up})
			w.Input(input.PointerUp{Pos: up, Button: input.ButtonPrimary, Time: time.Now()})
			w.Frame(time.Second / 60)
		}
}

func TestThePowerLampSwitchesOnTheReleaseOverIt(t *testing.T) {
	p := NewPower("gate")
	switched := 0
	p.OnChange = func(bool, *gunim.UI) gunim.Intent { switched++; return nil }
	w := stageNode(t, p)
	down, up := press(w, geom.Pt(50, 50))
	down()
	if p.Checked() || switched != 0 {
		t.Fatal("the press alone switched the lamp")
	}
	if p.squash.Target() != 1 {
		t.Error("the press did not press the lamp in")
	}
	up()
	if !p.Checked() || switched != 1 {
		t.Fatal("the release over the lamp did not switch it")
	}
	down, away := press(w, geom.Pt(300, 300))
	down()
	away()
	if !p.Checked() || switched != 1 {
		t.Fatal("a press dragged away switched the lamp")
	}
}

func TestTheActionDialActsOnTheReleaseOverIt(t *testing.T) {
	d := NewActionDial("All", icon.Wand)
	clicks := 0
	d.OnClick = func(*gunim.UI) { clicks++ }
	w := stageNode(t, d)
	down, up := press(w, geom.Pt(50, 50))
	down()
	if clicks != 0 {
		t.Fatal("the press alone clicked the dial")
	}
	up()
	if clicks != 1 {
		t.Fatalf("the release over the dial clicked it %d times, want once", clicks)
	}
	down, away := press(w, geom.Pt(300, 300))
	down()
	away()
	if clicks != 1 {
		t.Fatal("a press dragged away clicked the dial")
	}
}
