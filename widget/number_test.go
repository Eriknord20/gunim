package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/text"
)

type numbered struct{ V float64 }

func numberStage(t *testing.T, n *NumberField) (*gunim.Window, func(int)) {
	t.Helper()
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	// The number sits at the right, so a click there puts the caret
	// after it.
	click(w, 195, 16)
	run(1)
	return w, run
}

func TestNumberFieldStepsWithTheArrows(t *testing.T) {
	n := NewNumberField(0, 512)
	n.SetValue(10, nil)
	n.OnChange = func(v float64, u *gunim.UI) gunim.Intent { return numbered{v} }
	w, run := numberStage(t, n)

	w.Input(input.KeyPress{Key: input.KeyUp})
	run(1)
	if n.Value() != 11 || n.Text() != "11" {
		t.Fatalf("up gave %v and %q, want 11", n.Value(), n.Text())
	}
	w.Input(input.KeyPress{Key: input.KeyPageDown})
	run(1)
	if n.Value() != 1 {
		t.Fatalf("page down gave %v, want 1", n.Value())
	}
	got := sent(w)
	if last := got[len(got)-1]; last != (numbered{1}) {
		t.Fatalf("last intent %v, want numbered{1}", last)
	}
}

func TestNumberFieldHoldsToItsBounds(t *testing.T) {
	n := NewNumberField(1, 16)
	n.SetValue(16, nil)
	w, run := numberStage(t, n)
	for range 3 {
		w.Input(input.KeyPress{Key: input.KeyUp})
	}
	run(1)
	if n.Value() != 16 {
		t.Fatalf("stepping past the top gave %v, want 16", n.Value())
	}
	n.SetValue(99, nil)
	if n.Value() != 16 {
		t.Fatalf("SetValue past the top gave %v, want 16", n.Value())
	}
}

func TestNumberFieldLeavesHalfTypedTextAlone(t *testing.T) {
	// Clearing the field to retype it must not snap back to the
	// minimum under the fingers.
	n := NewNumberField(1, 512)
	n.SetValue(120, nil)
	w, run := numberStage(t, n)
	for range 3 {
		w.Input(input.KeyPress{Key: input.KeyBackspace})
	}
	run(1)
	if n.Text() != "" {
		t.Fatalf("the text reads %q after clearing it, want it empty", n.Text())
	}
	w.Input(input.TextInput{Text: "8"})
	run(1)
	if n.Value() != 8 {
		t.Fatalf("typing 8 gave %v", n.Value())
	}
}

func TestNumberFieldSettlesWhenItLosesFocus(t *testing.T) {
	n := NewNumberField(10, 100)
	n.SetValue(50, nil)
	w, run := numberStage(t, n)
	for range 2 {
		w.Input(input.KeyPress{Key: input.KeyBackspace})
	}
	w.Input(input.TextInput{Text: "5"})
	run(1)
	if n.Value() != 5 {
		t.Fatalf("typing 5 gave %v; below the minimum is allowed while typing", n.Value())
	}
	// Leaving the field holds it to the bounds and rewrites the text.
	if err := w.Client().Focus(""); err != nil {
		t.Fatal(err)
	}
	run(1)
	if n.Value() != 10 || n.Text() != "10" {
		t.Fatalf("leaving the field gave %v and %q, want 10", n.Value(), n.Text())
	}
}

func TestNumberFieldTurnsWithTheWheel(t *testing.T) {
	n := NewNumberField(0, 255)
	n.SetValue(100, nil)
	n.Increment = 5
	w, run := numberStage(t, n)
	// The wheel turned up scrolls toward the top, a positive Delta.Y.
	w.Input(input.Scroll{Pos: geom.Pt(100, 16), Delta: geom.Pt(0, 1)})
	run(1)
	if n.Value() != 105 {
		t.Fatalf("a wheel turn up gave %v, want 105", n.Value())
	}
	w.Input(input.Scroll{Pos: geom.Pt(100, 16), Delta: geom.Pt(0, -2)})
	run(1)
	if n.Value() != 100 {
		t.Fatalf("a wheel turn down gave %v, want 100", n.Value())
	}
}

func TestANumberFieldWithoutTheKeyboardLeavesTheWheelToWhatScrolls(t *testing.T) {
	n := NewNumberField(0, 255)
	n.SetValue(100, nil)
	n.OnChange = func(v float64, u *gunim.UI) gunim.Intent { return numbered{v} }
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	w.Input(input.Scroll{Pos: geom.Pt(100, 16), Delta: geom.Pt(0, 1)})
	run(1)
	if got := sent(w); n.Value() != 100 || len(got) != 0 {
		t.Fatalf("the wheel over a field without the keyboard made %v and sent %v, want 100 and nothing", n.Value(), got)
	}
}

func TestADisabledNumberFieldTakesNoWheelOrKeys(t *testing.T) {
	n := NewNumberField(0, 255)
	n.SetValue(100, nil)
	n.OnChange = func(v float64, u *gunim.UI) gunim.Intent { return numbered{v} }
	w, run := numberStage(t, n)
	n.Disabled = true
	run(1)
	w.Input(input.Scroll{Pos: geom.Pt(100, 16), Delta: geom.Pt(0, 1)})
	for _, k := range []input.Key{input.KeyUp, input.KeyDown, input.KeyPageUp, input.KeyPageDown} {
		w.Input(input.KeyPress{Key: k})
		w.Input(input.KeyPress{Key: k})
	}
	n.SetText("300", nil)
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if got := sent(w); n.Value() != 100 || len(got) != 0 {
		t.Fatalf("disabled, the field made %v and sent %v, want 100 and nothing", n.Value(), got)
	}
}

func TestNumberFieldWritesDecimalsAndASuffix(t *testing.T) {
	n := NewNumberField(20, 999)
	n.Decimals, n.Suffix, n.Increment = 2, " BPM", 0.5
	n.SetValue(128, nil)
	if n.Text() != "128.00 BPM" {
		t.Fatalf("the text reads %q", n.Text())
	}
	// The suffix is not part of what is read back.
	if v, ok := parseNumber("128.00 BPM"); !ok || v != 128 {
		t.Fatalf("parseNumber gave %v, %v", v, ok)
	}
}

func TestParseNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"42", 42, true},
		{" 42 ", 42, true},
		{"-7", -7, true},
		{"1.5", 1.5, true},
		{"1,5", 1.5, true}, // a European keyboard's decimal point
		{"250 ms", 250, true},
		{"", 0, false},
		{"ms", 0, false},
		{"--", 0, false},
	}
	for _, c := range cases {
		got, ok := parseNumber(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseNumber(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// dragNumber presses the field at from, moves the pointer through each
// of to with mods held, and lets go at the last.
func dragNumber(w *gunim.Window, run func(int), mods input.Mods, from geom.Point, to ...geom.Point) {
	w.Input(input.PointerMove{Pos: from, Time: time.Now()})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	run(1)
	for _, p := range to {
		w.Input(input.PointerMove{Pos: p, Mods: mods, Time: time.Now()})
		run(1)
	}
	w.Input(input.PointerUp{Pos: to[len(to)-1], Button: input.ButtonPrimary, Mods: mods, Time: time.Now()})
	run(1)
}

type numberCommitted struct{ V float64 }

func TestNumberFieldDragsUpAndDown(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	var live []float64
	n.OnChange = func(v float64, u *gunim.UI) gunim.Intent {
		if !n.Dragging() {
			t.Errorf("OnChange heard %v outside the drag", v)
		}
		live = append(live, v)
		return nil
	}
	n.OnCommit = func(v float64, u *gunim.UI) gunim.Intent { return numberCommitted{v} }
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	// 40 pixels up is ten steps of one, at four pixels a step, the
	// range being too long to cross.
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(100, 6), geom.Pt(101, -24))
	if n.Value() != 110 || n.Text() != "110" {
		t.Fatalf("a drag 40 pixels up gave %v and %q, want 110", n.Value(), n.Text())
	}
	if len(live) < 2 {
		t.Fatalf("OnChange heard %v, want the values on the way", live)
	}
	if got := sent(w); len(got) != 1 || got[0] != (numberCommitted{110}) {
		t.Fatalf("the drag sent %v, want numberCommitted{110} once, on letting go", got)
	}
	if n.Dragging() {
		t.Fatal("the field still drags after letting go")
	}
	// Right raises it too, and down and left lower it.
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(120, 17))
	if n.Value() != 115 {
		t.Fatalf("a drag 20 pixels right gave %v, want 115", n.Value())
	}
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(100, 56))
	if n.Value() != 105 {
		t.Fatalf("a drag 40 pixels down gave %v, want 105", n.Value())
	}
}

func TestNumberFieldDragFollowsTheAxisItFirstMovesAlong(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	// It starts up, so the sideways drift after that counts for
	// nothing.
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(100, 12), geom.Pt(160, 12))
	if n.Value() != 101 {
		t.Fatalf("a drag 4 up then 60 right gave %v, want 101", n.Value())
	}
}

func TestNumberFieldDragSpeedsWithCtrlAndSlowsWithShift(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	dragNumber(w, run, input.ModControl, geom.Pt(100, 16), geom.Pt(100, -24))
	if n.Value() != 200 {
		t.Fatalf("a drag 40 up with Ctrl gave %v, want 200", n.Value())
	}
	// A tenth as fast: 40 pixels is one step.
	dragNumber(w, run, input.ModShift, geom.Pt(100, 16), geom.Pt(100, -24))
	if n.Value() != 201 {
		t.Fatalf("a drag 40 up with Shift gave %v, want 201", n.Value())
	}
}

func TestNumberFieldDragKeepsToStepsDecimalsAndBounds(t *testing.T) {
	n := NewNumberField(0, 1)
	n.Decimals, n.Increment = 2, 0.05
	n.SetValue(0.5, nil)
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	// A step every four pixels crosses the range in 80: 9 pixels is
	// 0.1125, which lands on a step of 0.05.
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(109, 16))
	if n.Value() != 0.6 || n.Text() != "0.60" {
		t.Fatalf("a drag 30 right gave %v and %q, want 0.6", n.Value(), n.Text())
	}
	// Far past the top it holds to Max, and coming back moves at once.
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(100, -2000), geom.Pt(100, -1980))
	if n.Value() != 0.75 {
		t.Fatalf("a drag past the top and 20 back gave %v, want 0.75", n.Value())
	}
}

func TestNumberFieldShortRangeCrossesInAComfortableDrag(t *testing.T) {
	n := NewNumberField(0, 255)
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(300, 16), geom.Pt(500, 16))
	if n.Value() != 255 {
		t.Fatalf("a drag of 400 pixels across 0 to 255 gave %v, want 255", n.Value())
	}
}

func TestNumberFieldPressMovingLessThanTheSlopIsAClick(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	n.OnChange = func(v float64, u *gunim.UI) gunim.Intent { return numbered{v} }
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	dragNumber(w, run, 0, geom.Pt(195, 16), geom.Pt(196, 14))
	if n.Value() != 100 || len(sent(w)) != 0 {
		t.Fatalf("a press that moved 2 pixels made %v and sent %v, want 100 and nothing", n.Value(), sent(w))
	}
	// The click put the caret in, after the number, to type.
	w.Input(input.TextInput{Text: "5"})
	run(1)
	if n.Value() != 1005 {
		t.Fatalf("typing 5 after the click gave %v, want 1005", n.Value())
	}
	// Now a press drags out a selection, as in any text field, and
	// changes no value.
	dragNumber(w, run, 0, geom.Pt(195, 16), geom.Pt(100, 16))
	if n.Value() != 1005 || n.Dragging() {
		t.Fatalf("a drag while typing gave %v, want 1005 left alone", n.Value())
	}
	if a, b := n.anchor, n.caret; a == b {
		t.Fatal("a drag while typing selected nothing")
	}
}

func TestNumberFieldDragSelectsNoText(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	dragNumber(w, run, 0, geom.Pt(195, 16), geom.Pt(120, 16))
	if n.anchor != n.caret {
		t.Fatalf("a drag selected runes %d to %d", n.anchor, n.caret)
	}
	// Leaving the field and coming back by a press drags again.
	if err := w.Client().Focus(""); err != nil {
		t.Fatal(err)
	}
	run(1)
	was := n.Value()
	dragNumber(w, run, 0, geom.Pt(100, 16), geom.Pt(100, 4))
	if n.Value() != was+3 {
		t.Fatalf("a drag 12 up after the field was left gave %v, want %v", n.Value(), was+3)
	}
}

func TestEscapeCallsANumberDragOff(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	var live []float64
	n.OnChange = func(v float64, u *gunim.UI) gunim.Intent { live = append(live, v); return nil }
	n.OnCommit = func(v float64, u *gunim.UI) gunim.Intent { return numberCommitted{v} }
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	w.Input(input.PointerDown{Pos: geom.Pt(100, 16), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerMove{Pos: geom.Pt(100, -24), Time: time.Now()})
	run(1)
	if n.Value() != 110 {
		t.Fatalf("the drag gave %v, want 110", n.Value())
	}
	// Escape with Shift held for a fine drag still calls it off.
	w.Input(input.KeyPress{Key: input.KeyEscape, Mods: input.ModShift})
	w.Input(input.PointerMove{Pos: geom.Pt(100, -64), Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(100, -64), Button: input.ButtonPrimary, Time: time.Now()})
	run(1)
	if n.Value() != 100 || n.Text() != "100" {
		t.Fatalf("Escape left %v and %q, want 100", n.Value(), n.Text())
	}
	if live[len(live)-1] != 100 {
		t.Fatalf("OnChange last heard %v, want 100 put back", live)
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("a drag called off sent %v, want nothing", got)
	}
}

func TestNumberFieldDragShowsItsCursorAndPressedLook(t *testing.T) {
	n := NewNumberField(0, 10)
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	if c := n.Cursor(geom.Pt(100, 16)); c != input.CursorResizeV {
		t.Fatalf("over the field the pointer is %v, want an up and down arrow", c)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(100, 16), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	run(30)
	if n.pressed.Value() < 0.9 || !n.caretless {
		t.Fatalf("pressed the field shows %v of its pressed look, caretless %v", n.pressed.Value(), n.caretless)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(195, 16), Button: input.ButtonPrimary, Time: time.Now()})
	run(60)
	if n.pressed.Value() > 0.1 || n.caretless {
		t.Fatalf("let go the field shows %v of its pressed look", n.pressed.Value())
	}
	if c := n.Cursor(geom.Pt(100, 16)); c != input.CursorText {
		t.Fatalf("typing, the pointer is %v, want the I-beam", c)
	}
	n.NoDrag = true
	n.typing = false
	if c := n.Cursor(geom.Pt(100, 16)); c != input.CursorText {
		t.Fatalf("with NoDrag the pointer is %v, want the I-beam", c)
	}
}

func TestANumberFieldWithNoDragTakesAPressAsText(t *testing.T) {
	n := NewNumberField(0, 1e6)
	n.SetValue(100, nil)
	n.NoDrag = true
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	dragNumber(w, run, 0, geom.Pt(195, 16), geom.Pt(100, -40))
	if n.Value() != 100 || n.anchor == n.caret {
		t.Fatalf("a drag with NoDrag gave %v and selected %d to %d, want 100 and a selection", n.Value(), n.anchor, n.caret)
	}
}

func TestANumberFieldTellsAScreenReaderItsRange(t *testing.T) {
	n := NewNumberField(2, 40)
	n.Increment = 2
	n.SetValue(12, nil)
	n.OnCommit = func(v float64, u *gunim.UI) gunim.Intent { return numberCommitted{v} }
	w, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	w.Offscreen().ListenForAccess()
	run(2)
	info := find(w.Offscreen().AccessTree().Root, access.RoleTextField, "")
	if info == nil {
		t.Fatal("no field in the tree")
	}
	if r := info.Range; r == nil || r.Min != 2 || r.Max != 40 || r.Value != 12 || r.Step != 2 {
		t.Fatalf("the field says its range is %+v", info.Range)
	}
	if info.Value != "12" {
		t.Fatalf("the field says its value is %q", info.Value)
	}
	w.Input(access.Request{ID: info.ID, SetValue: true, Value: 99})
	run(1)
	if n.Value() != 40 {
		t.Fatalf("setting 99 gave %v, want 40", n.Value())
	}
	if got := sent(w); len(got) != 1 || got[0] != (numberCommitted{40}) {
		t.Fatalf("setting a value sent %v, want numberCommitted{40}", got)
	}
}

func TestANumberFieldSitsAtTheRight(t *testing.T) {
	n := NewNumberField(0, 100)
	n.SetValue(7, nil)
	_, run := stage(t, &frame{child: n, size: geom.Sz(200, 32)})
	run(1)
	if r := n.caretRect(len(n.text)); r.Min.X < 180 {
		t.Fatalf("the end of the number is at %v, want it against the right", r.Min.X)
	}
	n.Align = text.AlignStart
	run(1)
	if r := n.caretRect(0); r.Min.X > 20 {
		t.Fatalf("aligned to the start the number begins at %v", r.Min.X)
	}
}
