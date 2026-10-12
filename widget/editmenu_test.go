package widget

import (
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
)

// uiOf returns the typer's window's UI, caught by a key no field takes
// as it bubbles up to the frame.
func (ty *typer) uiOf() *gunim.UI {
	ty.t.Helper()
	var got *gunim.UI
	ty.fr.handle = func(e input.Event, u *gunim.UI) bool {
		got = u
		return true
	}
	ty.w.Input(input.KeyPress{Key: input.KeyF5})
	ty.fr.handle = nil
	if got == nil {
		ty.t.Fatal("the key never reached the frame")
	}
	return got
}

// secondary presses the secondary button at x along the field, as a
// right click does, or as a finger held still does when touch is set.
func (ty *typer) secondary(x float32, touch bool) {
	ty.w.Input(input.PointerDown{Pos: geom.Pt(x, 18), Button: input.ButtonSecondary, Clicks: 1, Touch: touch})
	ty.w.Input(input.PointerUp{Pos: geom.Pt(x, 18), Button: input.ButtonSecondary})
	ty.run(2)
}

// pick picks item i of the field's open edit menu.
func (ty *typer) pick(i int) {
	ty.t.Helper()
	m := ty.field.menuItems
	if m == nil {
		ty.t.Fatal("no edit menu is open")
	}
	m.OnPick(i, ty.uiOf())
	ty.run(2)
}

// xOf returns the x of the caret before rune i in the field.
func (ty *typer) xOf(i int) float32 {
	ty.t.Helper()
	for x := float32(0); x < 300; x++ {
		if ty.field.indexAt(geom.Pt(x, 18), ty.uiOf()) >= i {
			return x
		}
	}
	ty.t.Fatalf("no place for rune %d", i)
	return 0
}

func TestARightClickInTheSelectionCopiesIt(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello world")
	ty.key(input.KeyLeft, input.ModShift|input.ModControl) // selects "world"
	ty.secondary(ty.xOf(8), false)
	if s, e := ty.field.Selection(); s != 6 || e != 11 {
		t.Fatalf("a right click in the selection left it at %d–%d, want 6–11", s, e)
	}
	if m := ty.field.menuItems; m == nil || !slices.Equal(disabledOf(m.Items()), []bool{false, false, false, false}) {
		t.Fatalf("the edit menu is %v, want all four items enabled", m)
	}
	ty.pick(editCopy)
	if c, _ := ty.w.Offscreen().Clipboard(); c != "world" {
		t.Fatalf("Copy put %q on the clipboard, want world", c)
	}
	if ty.field.menuItems != nil {
		t.Fatal("the menu stayed open after a pick")
	}
}

func TestAFingerHeldOnAWordSelectsItForTheMenu(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("one two three")
	ty.secondary(ty.xOf(5), true)
	if s, e := ty.field.Selection(); s != 4 || e != 7 {
		t.Fatalf("a long press on two selected %d–%d, want 4–7", s, e)
	}
	ty.pick(editCut)
	ty.want("one  three", 4)
	if c, _ := ty.w.Offscreen().Clipboard(); c != "two" {
		t.Fatalf("Cut put %q on the clipboard, want two", c)
	}
	ty.secondary(ty.xOf(4), false)
	ty.pick(editPaste)
	ty.want("one two three", 7)
}

func TestARightClickOutsideTheSelectionMovesTheCaret(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello world")
	ty.key(input.KeyHome, input.ModShift) // selects everything
	ty.key(input.KeyEnd, 0)
	ty.secondary(ty.xOf(2), false)
	if s, e := ty.field.Selection(); s != e {
		t.Fatalf("the selection is %d–%d after a right click with none, want the caret alone", s, e)
	}
	if m := ty.field.menuItems; m == nil || !slices.Equal(disabledOf(m.Items()), []bool{true, true, false, false}) {
		t.Fatalf("with nothing selected the menu is %v, want Cut and Copy dimmed", m)
	}
	ty.pick(editSelectAll)
	if s, e := ty.field.Selection(); s != 0 || e != 11 {
		t.Fatalf("Select all selected %d–%d, want 0–11", s, e)
	}
	if ty.field.menuItems == nil {
		t.Fatal("the menu closed after Select all, want it kept for what to do with the selection")
	}
}

func TestASecretFieldOffersNoCopy(t *testing.T) {
	ty := newTyperWith(t, func(f *TextField) { f.Secret = true })
	ty.typeText("hunter2")
	ty.secondary(ty.xOf(3), true)
	if m := ty.field.menuItems; m == nil || !m.Items()[editCut].Disabled || !m.Items()[editCopy].Disabled {
		t.Fatalf("a secret field's menu is %v, want Cut and Copy dimmed", m)
	}
}

func TestALongPressPastTheLastWordSelectsOnlyThatWord(t *testing.T) {
	// Blank lines after the last word stay out of the selection, as the
	// space round a word does.
	for _, c := range []struct {
		text       string
		i          int
		start, end int
	}{
		{"Hej hej\nTre\n\n\n", 11, 8, 11},  // just after the last word
		{"Hej hej\nTre\n\n\n", 14, 14, 14}, // on the blank lines
		{"one two", 5, 4, 7},               // inside a word
		{"one two", 3, 0, 3},               // just after a word, before a space
		{"", 0, 0, 0},
	} {
		s, e := wordAt([]rune(c.text), c.i)
		if s != c.start || e != c.end {
			t.Errorf("wordAt(%q, %d) = %d–%d, want %d–%d", c.text, c.i, s, e, c.start, c.end)
		}
	}
}

func TestAFingerHeldOnAWordDragsOverMoreWords(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("one two three four five")
	hold := func(from, to int) {
		ty.w.Input(input.PointerDown{Pos: geom.Pt(ty.xOf(from), 18), Button: input.ButtonSecondary, Clicks: 1, Touch: true})
		if ty.field.menuItems != nil {
			t.Fatal("the menu opened under the finger, before it lifted")
		}
		ty.w.Input(input.PointerMove{Pos: geom.Pt(ty.xOf(to), 18), Touch: true})
		ty.w.Input(input.PointerUp{Pos: geom.Pt(ty.xOf(to), 18), Button: input.ButtonSecondary, Touch: true})
		ty.run(2)
	}
	hold(5, 15) // from two on to four
	if s, e := ty.field.Selection(); s != 4 || e != 18 {
		t.Fatalf("dragging from two to four selected %d–%d, want 4–18", s, e)
	}
	if ty.field.menuItems == nil {
		t.Fatal("no menu opened as the finger lifted")
	}
	hold(15, 1) // from four back to one
	if s, e := ty.field.Selection(); s != 0 || e != 18 {
		t.Fatalf("dragging from four back to one selected %d–%d, want 0–18", s, e)
	}
}

// menuLabels are the labels of the field's open edit menu.
func (ty *typer) menuLabels() []string {
	ty.t.Helper()
	m := ty.field.menuItems
	if m == nil {
		ty.t.Fatal("no edit menu is open")
	}
	out := make([]string, 0, len(m.list.items))
	for _, it := range m.list.items {
		out = append(out, it.Label)
	}
	return out
}

func TestTheEditMenuSaysCutCopyPasteAndSelectAll(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello")
	ty.secondary(20, false)
	if got, want := ty.menuLabels(), []string{"Cut", "Copy", "Paste", "Select all"}; !slices.Equal(got, want) {
		t.Fatalf("the edit menu says %q, want %q", got, want)
	}
}

func TestTheEditMenuSaysWhatItsThemeSays(t *testing.T) {
	sv := theme.Make("sv",
		theme.Set(FieldMenuCut, "Klipp ut"),
		theme.Set(FieldMenuCopy, "Kopiera"),
		theme.Set(FieldMenuPaste, "Klistra in"),
		theme.Set(FieldMenuSelectAll, "Markera allt"),
	)
	field := NewTextField()
	fr := &frame{child: NewThemed(field, sv), size: geom.Sz(300, 36)}
	w, run := stage(t, fr)
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18)})
	run(1)
	ty := &typer{t: t, w: w, run: run, field: field, fr: fr}
	ty.typeText("hej")
	ty.secondary(20, false)
	if got, want := ty.menuLabels(), []string{"Klipp ut", "Kopiera", "Klistra in", "Markera allt"}; !slices.Equal(got, want) {
		t.Fatalf("under a Swedish theme the edit menu says %q, want %q", got, want)
	}
}
