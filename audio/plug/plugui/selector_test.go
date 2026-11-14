package plugui

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

func TestASelectorPicksByLampAndKeys(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, []string{"one", "two", "three"})
	var picked []int
	s.OnPick = func(i int, _ *gunim.UI) { picked = append(picked, i) }
	s.Layout(gunim.Constraints{Max: geom.Sz(72, 100)}, gunim.Frame{}, gunim.Children{})
	s.Handle(input.PointerDown{Pos: s.cells[2].Center(), Button: input.ButtonPrimary, Clicks: 1}, nil)
	s.Handle(input.PointerUp{Pos: s.cells[2].Center(), Button: input.ButtonPrimary}, nil)
	// The wheel passes it by, to scroll the pane it is in.
	if s.Handle(input.Scroll{Delta: geom.Pt(0, 1)}, nil) || s.Selected() != 2 {
		t.Fatalf("the wheel took the selector from 2 to %d", s.Selected())
	}
	s.Handle(input.KeyPress{Key: input.KeyLeft}, nil)
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

func TestASelectorSetWithNoUIIsLitAtOnce(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, nil)
	s.SetSelected(2, nil)
	if s.lit[0].Value() != 0 || s.lit[2].Value() != 1 || s.shown.Value() != 2 || s.shown.Active() {
		t.Errorf("set as it is made, the lamps stand at %v and %v, the name at %v", s.lit[0].Value(), s.lit[2].Value(), s.shown.Value())
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

// TestArrowsWithCtrlAltOrSuperPassTheSelectorBy checks the selector leaves
// those keys to the window's shortcuts.
func TestArrowsWithCtrlAltOrSuperPassTheSelectorBy(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, nil)
	for _, m := range []input.Mods{input.ModAlt, input.ModControl, input.ModSuper} {
		if s.Handle(input.KeyPress{Key: input.KeyRight, Mods: m}, nil) || s.Selected() != 0 {
			t.Errorf("Right with %v took the selector to %d", m, s.Selected())
		}
	}
}

// A click on a lamp picks its option on the release. A press dragged off the lamp, or onto another, picks nothing.
func TestASelectorPicksOnTheRelease(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, nil)
	var picked []int
	s.OnPick = func(i int, _ *gunim.UI) { picked = append(picked, i) }
	w := stageNode(t, s)
	at := func(i int) geom.Point { return s.Cell(i).Center() }
	down := func(p geom.Point) {
		w.Input(input.PointerMove{Pos: p})
		w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
		w.Frame(time.Second / 60)
	}
	up := func(p geom.Point) {
		w.Input(input.PointerMove{Pos: p})
		w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Time: time.Now()})
		frames(w, 30)
	}
	down(at(2))
	if s.Selected() != 0 || len(picked) != 0 {
		t.Fatalf("the press alone picked %d", s.Selected())
	}
	w.Frame(time.Second / 60)
	if v := s.press.Value(); v <= 0 {
		t.Errorf("pressed, the lamp shows no press: %v", v)
	}
	up(geom.Pt(at(2).X, 300))
	if s.Selected() != 0 || len(picked) != 0 {
		t.Fatalf("a press dragged away picked %d", s.Selected())
	}
	if v := s.press.Value(); v != 0 {
		t.Errorf("let go away, the lamp still shows a press of %v", v)
	}
	down(at(2))
	up(at(1))
	if s.Selected() != 0 || len(picked) != 0 {
		t.Fatalf("a press dragged onto another lamp picked %d", s.Selected())
	}
	down(at(2))
	up(at(2))
	if s.Selected() != 2 || len(picked) != 1 || picked[0] != 2 {
		t.Fatalf("a click on C picked %v, want [2]", picked)
	}
}

// A second button clicked while the first is held leaves the click to land on the first's release.
func TestASecondButtonLeavesTheSelectorsClick(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, nil)
	w := stageNode(t, s)
	at := s.Cell(2).Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonSecondary, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	w.Frame(time.Second / 60)
	if s.Selected() != 2 {
		t.Errorf("with a second button clicked mid-press, the release picked %d, want 2", s.Selected())
	}
}

// A press in the selector's box beside its lamps stays with the selector and reaches nothing behind it.
func TestAPressBesideTheLampsStaysWithTheSelector(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B"}, nil)
	s.Layout(gunim.Constraints{Max: geom.Sz(72, 100)}, gunim.Frame{}, gunim.Children{})
	beside := geom.Pt(2, 2)
	if s.at(beside) >= 0 {
		t.Fatalf("%v is on option %d, want beside the lamps", beside, s.at(beside))
	}
	if !s.Handle(input.PointerDown{Pos: beside, Button: input.ButtonPrimary, Clicks: 1}, nil) {
		t.Error("a press beside the lamps passed to what is behind the selector")
	}
	s.Handle(input.PointerUp{Pos: beside, Button: input.ButtonPrimary}, nil)
	if s.Selected() != 0 {
		t.Errorf("a click beside the lamps picked %d", s.Selected())
	}
}

// The arrows move through the lamps as they stand in the grid, and Home and End go to the first and the last.
func TestTheArrowsFollowTheGrid(t *testing.T) {
	grid := NewSelector("Test", []string{"A", "B", "C", "D"}, nil)
	grid.Columns = 2
	for _, step := range []struct {
		key  input.Key
		want int
	}{
		{input.KeyDown, 2}, {input.KeyDown, 2}, {input.KeyRight, 3}, {input.KeyUp, 1}, {input.KeyUp, 1},
		{input.KeyLeft, 0}, {input.KeyEnd, 3}, {input.KeyHome, 0},
	} {
		if !grid.Handle(input.KeyPress{Key: step.key}, nil) || grid.Selected() != step.want {
			t.Fatalf("%v took the grid to %d, want %d", step.key, grid.Selected(), step.want)
		}
	}
	line := NewSelector("Test", []string{"A", "B", "C"}, nil)
	for _, step := range []struct {
		key  input.Key
		want int
	}{{input.KeyDown, 1}, {input.KeyEnd, 2}, {input.KeyUp, 1}, {input.KeyHome, 0}} {
		if !line.Handle(input.KeyPress{Key: step.key}, nil) || line.Selected() != step.want {
			t.Fatalf("%v took the row to %d, want %d", step.key, line.Selected(), step.want)
		}
	}
}

// A finger that goes on to scroll lets go at input.Away: it picks nothing, and the lamp lets go of its press.
func TestATouchReleasedAwayPicksNothing(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, nil)
	w := stageNode(t, s)
	at := s.Cell(2).Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: time.Now()})
	w.Frame(time.Second / 60)
	w.Input(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: true, Time: time.Now()})
	frames(w, 90)
	if s.Selected() != 0 {
		t.Errorf("a touch let go away picked %d", s.Selected())
	}
	if v := s.press.Value(); v != 0 {
		t.Errorf("a touch let go away left the lamp pressed %v", v)
	}
}

// A locked selector under the pointer shows no press and picks nothing.
func TestALockedSelectorUnderThePointerPicksNothing(t *testing.T) {
	s := NewSelector("Test", []string{"A", "B", "C"}, nil)
	s.Locked = true
	picked := false
	s.OnPick = func(int, *gunim.UI) { picked = true }
	w := stageNode(t, s)
	at := s.Cell(1).Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Frame(time.Second / 60)
	if v := s.press.Value(); v != 0 {
		t.Errorf("locked, the lamp pressed in %v", v)
	}
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	frames(w, 10)
	if picked || s.Selected() != 0 {
		t.Errorf("locked, a click picked %d", s.Selected())
	}
}

// Label, where set, names the selector for a screen reader, its title still written under the lamps.
func TestASelectorsLabelReplacesItsTitleForAccess(t *testing.T) {
	s := NewSelector("Style", []string{"A", "B"}, nil)
	s.Label = "Low style"
	if got := s.Access().Name; got != "Low style" {
		t.Errorf("the selector reads as %q, want %q", got, "Low style")
	}
}
