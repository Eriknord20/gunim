package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// writer is a text area in an offscreen window, focused and ready.
type writer struct {
	t    *testing.T
	w    *gunim.Window
	run  func(int)
	area *TextArea
}

func newWriter(t *testing.T, width float32) *writer {
	t.Helper()
	area := NewTextArea()
	w, run := stage(t, &frame{child: area, size: geom.Sz(width, 400)})
	w.Input(input.PointerDown{Pos: geom.Pt(20, 20), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 20)})
	run(1)
	return &writer{t: t, w: w, run: run, area: area}
}

func (wr *writer) typeText(s string) {
	wr.w.Input(input.TextInput{Text: s})
	wr.run(1)
}

func (wr *writer) key(k input.Key, mods input.Mods) {
	wr.w.Input(input.KeyPress{Key: k, Mods: mods})
	wr.run(1)
}

func (wr *writer) want(text string, caret int) {
	wr.t.Helper()
	if got := wr.area.Text(); got != text {
		wr.t.Fatalf("text %q, want %q", got, text)
	}
	if wr.area.caret != caret {
		wr.t.Fatalf("caret at %d, want %d", wr.area.caret, caret)
	}
}

func TestEnterStartsALine(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeText("one")
	wr.key(input.KeyEnter, 0)
	wr.typeText("two")
	wr.want("one\ntwo", 7)
	if n := wr.area.para.count(); n != 2 {
		t.Fatalf("%d lines, want 2", n)
	}
}

func TestUpAndDownKeepTheColumn(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeText("abcdef")
	wr.key(input.KeyEnter, 0)
	wr.typeText("ab")
	wr.key(input.KeyEnter, 0)
	wr.typeText("abcdef")
	wr.want("abcdef\nab\nabcdef", 16)

	wr.key(input.KeyUp, 0)
	wr.want("abcdef\nab\nabcdef", 9) // the short line's end
	wr.key(input.KeyUp, 0)
	wr.want("abcdef\nab\nabcdef", 6) // back in the column it came from
	wr.key(input.KeyDown, input.ModShift)
	if s, e := wr.area.Selection(); s != 6 || e != 9 {
		t.Fatalf("Shift+Down selected %d..%d, want 6..9", s, e)
	}
	wr.key(input.KeyUp, 0)
	wr.key(input.KeyUp, 0)
	wr.want("abcdef\nab\nabcdef", 0) // Up from the first line goes to its start
}

func TestHomeAndEndWorkOnTheLineOnScreen(t *testing.T) {
	wr := newWriter(t, 140)
	wr.typeText("one two three four five six")
	if wr.area.para.count() < 2 {
		t.Fatal("setup: the text should wrap")
	}
	wr.key(input.KeyHome, input.ModControl)
	wr.key(input.KeyEnd, 0)
	first, _, _ := wr.area.para.line(0)
	if wr.area.caret != first.End-1 {
		t.Fatalf("End on a wrapped line put the caret at %d, want %d, before the break", wr.area.caret, first.End-1)
	}
	if line, _ := wr.area.para.Caret(wr.area.caret); line != 0 {
		t.Fatalf("after End, the caret shows on line %d, want the first", line)
	}
	wr.key(input.KeyEnd, input.ModControl)
	wr.want("one two three four five six", 27)
	wr.key(input.KeyHome, 0)
	if last, _, _ := wr.area.para.line(wr.area.para.count() - 1); wr.area.caret != last.Start {
		t.Fatalf("Home put the caret at %d, want the last line's start %d", wr.area.caret, last.Start)
	}
}

func TestPastingKeepsLineBreaksInAnArea(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeText("a\nb")
	wr.key(input.KeyA, input.ModControl)
	wr.key(input.KeyC, input.ModControl)
	wr.key(input.KeyEnd, input.ModControl)
	wr.key(input.KeyV, input.ModControl)
	wr.want("a\nba\nb", 6)

	ty := newTyper(t)
	ty.typeText("a\nb")
	ty.want("a b", 3)
}

func TestAClickInAnAreaFindsItsLine(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeText("abc\ndef")
	pad := FieldPadding.Default()
	lh := wr.area.para.LineHeight
	wr.w.Input(input.PointerDown{Pos: geom.Pt(pad+1000, pad+lh*1.5), Clicks: 1})
	wr.run(1)
	wr.want("abc\ndef", 7)
	wr.w.Input(input.PointerDown{Pos: geom.Pt(pad, pad+lh*0.5), Clicks: 1})
	wr.run(1)
	wr.want("abc\ndef", 0)
}

func TestAnAreaScrollsToKeepTheCaretInView(t *testing.T) {
	wr := newWriter(t, 400)
	for range 12 {
		wr.typeText("line")
		wr.key(input.KeyEnter, 0)
	}
	wr.run(120)
	lh := wr.area.para.LineHeight
	_, at := wr.area.para.Caret(wr.area.caret)
	top := wr.area.scroll.Value()
	if at.Y < top || at.Y+lh > top+wr.area.view+0.5 {
		t.Fatalf("caret line at %v..%v, view %v..%v", at.Y, at.Y+lh, top, top+wr.area.view)
	}
	wr.key(input.KeyHome, input.ModControl)
	wr.run(120)
	if top := wr.area.scroll.Value(); top != 0 {
		t.Fatalf("at the start of the text, the view is scrolled to %v, want 0", top)
	}
}

func TestLeftAndRightStepInAField(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("abc")
	ty.key(input.KeyLeft, 0)
	ty.key(input.KeyLeft, 0)
	ty.want("abc", 1)
	ty.key(input.KeyRight, input.ModShift)
	if s, e := ty.field.Selection(); s != 1 || e != 2 {
		t.Fatalf("Shift+Right selected %d..%d, want 1..2", s, e)
	}
	ty.key(input.KeyLeft, 0)
	ty.want("abc", 1) // collapses to the selection's start
}

func TestTheWheelScrollsAnAreaAwayFromItsCaret(t *testing.T) {
	wr := newWriter(t, 400)
	for range 30 {
		wr.typeText("line")
		wr.key(input.KeyEnter, 0)
	}
	wr.run(120)
	bottom := wr.area.scroll.Value()
	if bottom <= 0 {
		t.Fatal("setup: the caret at the end should have scrolled the area")
	}

	// Scroll up past where the caret could be seen, and let it settle.
	wr.w.Input(input.Scroll{Pos: geom.Pt(50, 50), Delta: geom.Pt(0, 1000)})
	wr.run(120)
	if top := wr.area.scroll.Value(); top != 0 {
		t.Fatalf("after scrolling up, the area sits at %v, want 0: it went back to the caret", top)
	}

	// Typing brings the caret back into view.
	wr.typeText("x")
	wr.run(120)
	if got := wr.area.scroll.Value(); got != bottom {
		t.Fatalf("after typing, the area sits at %v, want back at the caret, %v", got, bottom)
	}
}

func TestEnterSubmitsAndShiftEnterStartsALine(t *testing.T) {
	wr := newWriter(t, 400)
	wr.area.OnCommit = func(s string, u *gunim.UI) gunim.Intent { return submitted{s} }
	wr.typeText("one")
	wr.key(input.KeyEnter, input.ModShift)
	wr.typeText("two")
	wr.want("one\ntwo", 7)
	sent(wr.w)
	wr.key(input.KeyEnter, 0)
	wr.want("one\ntwo", 7)
	got := sent(wr.w)
	if len(got) != 1 || got[0] != (submitted{"one\ntwo"}) {
		t.Fatalf("intents %v, want a submit of the two lines", got)
	}
}

// shownLines returns how many lines tall the area shows.
func (wr *writer) shownLines() float32 { return wr.area.view / wr.area.para.LineHeight }

func TestAnAreaGrowsWithItsTextUpToMaxRows(t *testing.T) {
	area := NewTextArea()
	area.Rows, area.MaxRows = 1, 3
	col := Column(area)
	col.Cross = CrossStretch
	w, run := stage(t, col)
	w.Input(input.PointerDown{Pos: geom.Pt(20, 10), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 10)})
	run(60)
	wr := &writer{t: t, w: w, run: run, area: area}
	if n := wr.shownLines(); abs32(n-1) > 0.01 {
		t.Fatalf("an empty area shows %v lines, want 1", n)
	}
	wr.typeText("one")
	wr.key(input.KeyEnter, input.ModShift)
	wr.typeText("two")
	wr.run(60)
	if n := wr.shownLines(); abs32(n-2) > 0.01 {
		t.Fatalf("two lines of text show %v lines, want 2", n)
	}
	for range 4 {
		wr.key(input.KeyEnter, input.ModShift)
		wr.typeText("more")
	}
	wr.run(60)
	if n := wr.shownLines(); abs32(n-3) > 0.01 {
		t.Fatalf("six lines of text show %v lines, want MaxRows, 3", n)
	}
	wr.area.SetText("", nil)
	wr.run(60)
	if n := wr.shownLines(); abs32(n-1) > 0.01 {
		t.Fatalf("emptied, the area shows %v lines, want 1", n)
	}
}

// pastedImage is the intent a test's area sends for a pasted picture.
type pastedImage struct{ PNG string }

func TestCtrlVPastesAPictureWhenTheClipboardHasOne(t *testing.T) {
	wr := newWriter(t, 400)
	wr.area.OnPasteImage = func(png []byte, u *gunim.UI) gunim.Intent { return pastedImage{string(png)} }
	if err := wr.w.Offscreen().SetClipboard("words"); err != nil {
		t.Fatal(err)
	}
	_ = wr.w.Offscreen().SetClipboardImage([]byte("\x89PNG..."))
	sent(wr.w)
	wr.key(input.KeyV, input.ModControl)
	if got := sent(wr.w); len(got) != 1 || got[0] != (pastedImage{"\x89PNG..."}) {
		t.Fatalf("intents %v, want the picture", got)
	}
	wr.want("", 0)
	_ = wr.w.Offscreen().SetClipboardImage(nil)
	wr.key(input.KeyV, input.ModControl)
	wr.want("words", 5)
}

func TestPlaceholdersTakeTurns(t *testing.T) {
	const hold = 15 * time.Second
	cases := []struct {
		since     time.Duration
		was, now  int
		turning   bool
		nextAfter time.Duration
	}{
		{0, 0, 0, false, hold},
		{10 * time.Second, 0, 0, false, hold},
		{hold + placeholderTurn/2, 0, 1, true, hold + placeholderTurn/2},
		{hold + placeholderTurn, 1, 1, false, 2 * hold},
		{2*hold + time.Millisecond, 1, 0, true, 2*hold + time.Millisecond},
	}
	for _, c := range cases {
		was, now, turn, next := placeholderTurnAt(2, hold, c.since)
		if was != c.was || now != c.now || (turn < 1) != c.turning || next != c.nextAfter {
			t.Errorf("at %v: %d to %d at %v, next at %v; want %d to %d, turning %v, next at %v",
				c.since, was, now, turn, next, c.was, c.now, c.turning, c.nextAfter)
		}
	}
}
