package plugui

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

func TestASelectorPicksByLampWheelAndKeys(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, []string{"one", "two", "three"})
	var picked []int
	s.OnPick = func(i int, _ *gunim.UI) { picked = append(picked, i) }
	s.Layout(gunim.Constraints{Max: geom.Sz(72, 100)}, gunim.Frame{}, gunim.Children{})
	s.Handle(input.PointerDown{Pos: s.cells[2].Center(), Button: input.ButtonPrimary, Clicks: 1}, nil)
	// The wheel down steps back.
	s.Handle(input.Scroll{Delta: geom.Pt(0, 1)}, nil)
	if s.Selected() != 1 {
		t.Fatalf("the wheel down took the selector from 2 to %d, want 1", s.Selected())
	}
	s.Handle(input.KeyPress{Key: input.KeyLeft}, nil)
	// Past the first, nothing.
	s.Handle(input.KeyPress{Key: input.KeyLeft}, nil)
	if want := []int{2, 1, 0}; len(picked) != 3 || picked[0] != 2 || picked[1] != 1 || picked[2] != 0 {
		t.Fatalf("the selector picked %v, want %v", picked, want)
	}
	if a := s.Access(); a.Role != access.RoleTabList || a.Value != "A" || a.Parts[2].Description != "three" {
		t.Errorf("the selector reads as %+v", a)
	}
	s.AccessAct(access.Request{Part: 1, Action: access.ActionPress}, nil)
	if s.Selected() != 1 {
		t.Errorf("pressed by a screen reader, the selector picked %d, want 1", s.Selected())
	}
	s.Locked = true
	s.Handle(input.KeyPress{Key: input.KeyRight}, nil)
	if s.Selected() != 1 {
		t.Error("locked, the selector picked")
	}
}

func TestAKnobBoundToItsOwnValueTurnsItAsAGesture(t *testing.T) {
	var log []string
	v := 0.0
	k := NewKnobFunc(plug.Param{Name: "Mix", Max: 1, Default: 0.5}, 0,
		func(*gunim.UI) { log = append(log, "begin") },
		func(x float64, _ *gunim.UI) { v = x; log = append(log, "set") },
		func(*gunim.UI) { log = append(log, "end") })
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 1}, nil)
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 10)}, nil)
	k.Handle(input.PointerUp{Pos: geom.Pt(20, 10), Button: input.ButtonPrimary}, nil)
	if v != 0.5 || len(log) != 3 || log[0] != "begin" || log[2] != "end" {
		t.Fatalf("dragged half its throw, the value is %v, by %v", v, log)
	}
	// Locked, it turns not.
	k.Locked = true
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 2}, nil)
	k.Handle(input.KeyPress{Key: input.KeyEnd}, nil)
	if v != 0.5 || len(log) != 3 {
		t.Errorf("locked, the knob turned to %v", v)
	}
	// Another value to turn, in its place.
	k.Locked = false
	k.SetParam(plug.Param{Name: "Slope", Min: 12, Max: 48, Steps: 3}, 24, nil)
	k.Handle(input.KeyPress{Key: input.KeyUp}, nil)
	if v != 36 || k.String() != "Slope 36" {
		t.Errorf("turning a slope a step up from 24 set %v (%s), want 36", v, k)
	}
}

func TestFourLampsInTwoColumnsStandTwoByTwo(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C", "D"}, []string{"a", "b", "c", "d"})
	s.Columns = 2
	size := s.Layout(gunim.Constraints{Min: geom.Sz(56, 0), Max: geom.Sz(56, 100)}, gunim.Frame{}, gunim.Children{})
	if size.W != 56 || size.H != KnobH {
		t.Errorf("four lamps two by two stand %v, want as tall as a knob", size)
	}
	// Two rows, lampRow apart, about where a knob's cap's middle is.
	top, foot := s.lampsAt()
	if foot-top != 32 || (top+foot)/2 != lampMid || lampMid != 4+17 {
		t.Errorf("the lamps run from %v to %v: want two rows 16 apart about a knob's cap's middle, 21", top, foot)
	}
	// The columns 16 apart about the middle, 28: lamps at 20 and 36.
	if c := s.cells; c[0].Center().X != 20 || c[1].Center().X != 36 {
		t.Errorf("the lamps stand at %v and %v across, want 20 and 36", c[0].Center().X, c[1].Center().X)
	}
	// Each lamp where it stands: the top row first, then the second.
	for i, at := range []geom.Point{{X: 20, Y: lampMid - 8}, {X: 36, Y: lampMid - 8}, {X: 20, Y: lampMid + 8},
		{X: 36, Y: lampMid + 8}} {
		if got := s.at(at); got != i {
			t.Errorf("at %v is option %d, want %d", at, got, i)
		}
	}
	// Under the lamps, on the title and the name, the lamp of the last
	// row above.
	if got := s.at(geom.Pt(36, size.H-4)); got != 3 {
		t.Errorf("under the right column, option %d", got)
	}
}

func TestSectionsSplitIntoEvenRows(t *testing.T) {
	// Five that fit in one row of 500, and a sixth: two rows, three and
	// three, not five and one.
	w := []float32{100, 100, 100, 100, 50, 100}
	rows := balancedRows(w, 560)
	if len(rows) != 2 || rows[0] != [2]int{0, 3} || rows[1] != [2]int{3, 6} {
		t.Errorf("six sections split %v", rows)
	}
	if rows := balancedRows(w[:3], 560); len(rows) != 1 || rows[0] != [2]int{0, 3} {
		t.Errorf("three that fit split %v", rows)
	}
	for _, r := range balancedRows([]float32{300, 300, 300, 300, 300}, 700) {
		if rowWidth([]float32{300, 300, 300, 300, 300}[r[0]:r[1]]) > 700 {
			t.Errorf("a row %v runs past the room", r)
		}
	}
}
