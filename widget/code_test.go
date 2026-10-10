package widget

import (
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/syntax"
)

// codeStage shows a code editor holding src, 600 by 300, and gives it
// the keyboard.
func codeStage(t *testing.T, src string) (*CodeEditor, *gunim.Window, func(int)) {
	t.Helper()
	c := NewCodeEditor()
	c.SetText(src, nil)
	w, run := stage(t, &frame{child: c, size: geom.Sz(600, 300)})
	click(w, 300, 150)
	run(1)
	return c, w, run
}

// typeKeys presses each key in turn.
func typeKeys(w *gunim.Window, run func(int), mods input.Mods, keys ...input.Key) {
	for _, k := range keys {
		w.Input(input.KeyPress{Key: k, Mods: mods})
		run(1)
	}
}

func TestCodeTabsReachTheNextTabStop(t *testing.T) {
	c, _, _ := codeStage(t, "ab\tc\n\td\n    e")
	col := c.tabW / 4
	for _, tc := range []struct {
		i    int
		want float32
	}{
		{3, 4 * col},  // c, after a tab from column 2
		{6, 4 * col},  // d, after a tab from column 0
		{12, 4 * col}, // e, after four spaces
	} {
		if got := c.xOf(tc.i); abs32(got-tc.want) > 0.01 {
			t.Errorf("rune %d sits at %v, want %v", tc.i, got, tc.want)
		}
	}
	// A click in the second half of a tab puts the caret after it.
	if i := c.hit(1, 3*col); i != 6 {
		t.Errorf("a click three columns into the tab put the caret before rune %d, want 6", i)
	}
}

func TestCodeColoursEachKindOfToken(t *testing.T) {
	c, _, _ := codeStage(t, "func f() int { return 1 // one\n}")
	var got []string
	for _, s := range c.lines[0].segs {
		if s.kind != syntax.Plain {
			got = append(got, s.kind.String())
		}
	}
	if want := "keyword function type keyword number comment"; strings.Join(got, " ") != want {
		t.Fatalf("the line's kinds are %v, want %s", got, want)
	}
	c.Highlight = nil
	c.relayout(nil)
	for _, s := range c.lines[0].segs {
		if s.kind != syntax.Plain {
			t.Fatalf("with no highlighter a %s token is coloured", s.kind)
		}
	}
}

func TestCodeEnterKeepsTheIndentAndABraceTakesItBack(t *testing.T) {
	c, w, run := codeStage(t, "func f() {")
	typeKeys(w, run, 0, input.KeyEnd, input.KeyEnter)
	if got := c.Text(); got != "func f() {\n\t" {
		t.Fatalf("Enter after a brace made %q", got)
	}
	w.Input(input.TextInput{Text: "x"})
	typeKeys(w, run, 0, input.KeyEnter)
	if got := c.Text(); got != "func f() {\n\tx\n\t" {
		t.Fatalf("Enter on an indented line made %q", got)
	}
	w.Input(input.TextInput{Text: "}"})
	run(1)
	if got := c.Text(); got != "func f() {\n\tx\n}" {
		t.Fatalf("a closing brace made %q", got)
	}
}

func TestCodeTabIndentsTheLinesSelectedAsOneStep(t *testing.T) {
	src := "a\n\tb\nc"
	c, w, run := codeStage(t, src)
	typeKeys(w, run, input.ModControl, input.KeyA)
	typeKeys(w, run, 0, input.KeyTab)
	if got := c.Text(); got != "\ta\n\t\tb\n\tc" {
		t.Fatalf("Tab over three lines made %q", got)
	}
	typeKeys(w, run, input.ModShift, input.KeyTab, input.KeyTab)
	if got := c.Text(); got != "a\nb\nc" {
		t.Fatalf("Shift+Tab twice made %q", got)
	}
	typeKeys(w, run, input.ModControl, input.KeyZ)
	typeKeys(w, run, input.ModControl, input.KeyZ)
	if got := c.Text(); got != "\ta\n\t\tb\n\tc" {
		t.Fatalf("two undos made %q, want the first Shift+Tab undone and the second too", got)
	}
	typeKeys(w, run, input.ModControl, input.KeyZ)
	if got := c.Text(); got != src {
		t.Fatalf("a third undo made %q, want the code as it was", got)
	}
	// With no selection across lines, Tab types a tab.
	typeKeys(w, run, input.ModControl, input.KeyHome)
	typeKeys(w, run, 0, input.KeyTab)
	if got := c.Text(); got != "\t"+src {
		t.Fatalf("Tab at the start made %q", got)
	}
}

func TestCodeHomeGoesToTheCodeThenTheLineStart(t *testing.T) {
	c, w, run := codeStage(t, "x\n\t\tret")
	typeKeys(w, run, input.ModControl, input.KeyEnd)
	typeKeys(w, run, 0, input.KeyHome)
	if c.caret != 4 {
		t.Fatalf("Home put the caret at %d, want 4, after the indent", c.caret)
	}
	typeKeys(w, run, 0, input.KeyHome)
	if c.caret != 2 {
		t.Fatalf("Home again put the caret at %d, want 2, the line's start", c.caret)
	}
}

func TestReadOnlyCodeSelectsAndCopiesOnly(t *testing.T) {
	c, w, run := codeStage(t, "abc")
	c.SetReadOnly(true)
	w.Input(input.TextInput{Text: "x"})
	typeKeys(w, run, 0, input.KeyBackspace, input.KeyEnter)
	typeKeys(w, run, input.ModControl, input.KeyA)
	typeKeys(w, run, input.ModControl, input.KeyX)
	if got := c.Text(); got != "abc" {
		t.Fatalf("read only code became %q", got)
	}
	if s, e := c.Selection(); s != 0 || e != 3 {
		t.Fatalf("Ctrl+A in read only code selected %d to %d", s, e)
	}
}

func TestCodeReplaceKeepsTheCaretsLineAndColumn(t *testing.T) {
	c, w, run := codeStage(t, "a\nbcd\ne")
	typeKeys(w, run, input.ModControl, input.KeyHome)
	typeKeys(w, run, 0, input.KeyDown, input.KeyEnd)
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, s string, u *gunim.UI) { c.Replace(s, u) })
	if err := w.Client().Patch("stage", "a\n\tbcd!\ne"); err != nil {
		t.Fatal(err)
	}
	run(1)
	if line, col := c.lineCol(c.caret); line != 1 || col != 3 {
		t.Fatalf("after Replace the caret is on line %d column %d, want 1 and 3", line, col)
	}
	typeKeys(w, run, input.ModControl, input.KeyZ)
	if got := c.Text(); got != "a\nbcd\ne" {
		t.Fatalf("undo after Replace made %q", got)
	}
}

// The caret and the band on its line glide down a line, frame by frame,
// never past where they are going and never back.
func TestCodeCaretAndBandGlideBetweenLines(t *testing.T) {
	c, w, run := codeStage(t, "one\ntwo\nthree")
	typeKeys(w, run, input.ModControl, input.KeyHome)
	run(30)
	from := c.caretAt.Value().Y
	w.Input(input.KeyPress{Key: input.KeyDown})
	to := from + c.lineH
	lastCaret, lastBand := from, c.band.Value()
	moved := false
	for range 40 {
		run(1)
		y, b := c.caretAt.Value().Y, c.band.Value()
		if y < lastCaret-0.01 || y > to+0.5 || b < lastBand-0.01 || b > to+0.5 {
			t.Fatalf("the caret went %v to %v and the band %v to %v, heading for %v", lastCaret, y, lastBand, b, to)
		}
		if y > from && y < to {
			moved = true
		}
		lastCaret, lastBand = y, b
	}
	if !moved {
		t.Fatal("the caret jumped a line instead of gliding")
	}
	if abs32(lastCaret-to) > 0.01 || abs32(lastBand-to) > 0.01 {
		t.Fatalf("settled, the caret is at %v and the band at %v, want %v", lastCaret, lastBand, to)
	}
}

// Ctrl+End in long code scrolls to the end on a spring, from a scroll
// left part way, frame by frame towards it.
func TestCodeScrollFollowsTheCaret(t *testing.T) {
	src := strings.Repeat("line\n", 200) + "end"
	c, w, run := codeStage(t, src)
	w.Input(input.Scroll{Pos: geom.Pt(300, 150), Delta: geom.Pt(0, -137)})
	run(5)
	typeKeys(w, run, input.ModControl, input.KeyEnd)
	want := c.maxScrollY()
	if want <= 0 {
		t.Fatal("200 lines fit in 300 pixels")
	}
	last := c.scrollY.Value()
	for range 60 {
		run(1)
		y := c.scrollY.Value()
		if y < last-0.01 || y > want+0.5 {
			t.Fatalf("the scroll went from %v to %v, heading for %v", last, y, want)
		}
		last = y
	}
	if abs32(last-want) > 0.5 {
		t.Fatalf("the scroll settled at %v, want %v", last, want)
	}
	if line := c.lineOf(c.caret); line != 200 {
		t.Fatalf("the caret is on line %d, want the last, 200", line)
	}
}

// A mark fades in, frame by frame, and fades out and leaves when it is
// left out.
func TestCodeMarksFadeInAndOut(t *testing.T) {
	c, w, run := codeStage(t, "a\nb\nc")
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, ms []CodeMark, u *gunim.UI) { c.SetMarks(ms, u) })
	mark := CodeMark{Line: 2, Col: 1, Message: "undefined: b"}
	if err := w.Client().Patch("stage", []CodeMark{mark}); err != nil {
		t.Fatal(err)
	}
	last := float32(0)
	for range 40 {
		run(1)
		v := c.marks[0].shown.Value()
		if v < last-0.01 {
			t.Fatalf("the mark went from %v to %v as it came in", last, v)
		}
		last = v
	}
	if abs32(last-1) > 0.01 {
		t.Fatalf("the mark settled at %v, want 1", last)
	}
	if got := c.Marks(); len(got) != 1 || got[0] != mark {
		t.Fatalf("the marks are %v", got)
	}
	if err := w.Client().Patch("stage", []CodeMark(nil)); err != nil {
		t.Fatal(err)
	}
	run(1)
	if len(c.Marks()) != 0 {
		t.Fatal("a mark left out still counts as shown")
	}
	for range 60 {
		run(1)
	}
	if len(c.marks) != 0 {
		t.Fatalf("%d marks are still drawn after fading out", len(c.marks))
	}
}

// A paste keeps its tabs, as code needs.
func TestCodeKeepsPastedTabs(t *testing.T) {
	c, w, run := codeStage(t, "")
	w.Input(input.TextInput{Text: "if x {\n\ty()\n}"})
	run(1)
	if got := c.Text(); got != "if x {\n\ty()\n}" {
		t.Fatalf("typed code became %q", got)
	}
}

// The caret stays in view while the editor shrinks under it, as when a
// panel slides open below, frame by frame.
func TestCodeKeepsTheCaretInViewAsItShrinks(t *testing.T) {
	c := NewCodeEditor()
	c.SetText(strings.Repeat("line\n", 100), nil)
	f := &frame{child: c, size: geom.Sz(600, 300)}
	w, run := stage(t, f)
	click(w, 300, 150)
	run(1)
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, line int, u *gunim.UI) { c.GoTo(line, 1, u) })
	if err := w.Client().Patch("stage", 12); err != nil {
		t.Fatal(err)
	}
	run(30)
	// Shrink by 10 pixels a frame, as a panel opening would.
	for h := float32(300); h >= 150; h -= 10 {
		f.size.H = h
		run(1)
	}
	run(40)
	top := c.scrollY.Value()
	y := float32(11) * c.lineH
	if y < top || y+c.lineH > top+c.view.H+0.5 {
		t.Fatalf("line 12 spans %v to %v, outside the view from %v to %v", y, y+c.lineH, top, top+c.view.H)
	}
}

func TestCodeEnterCommitsAndShiftEnterStartsALine(t *testing.T) {
	c, w, run := codeStage(t, "")
	c.OnCommit = func(s string, u *gunim.UI) gunim.Intent { return submitted{s} }
	w.Input(input.TextInput{Text: "if x {"})
	run(1)
	typeKeys(w, run, input.ModShift, input.KeyEnter)
	if got := c.Text(); got != "if x {\n\t" {
		t.Fatalf("Shift+Enter left %q, want a new line indented", got)
	}
	sent(w)
	typeKeys(w, run, 0, input.KeyEnter)
	if got := c.Text(); got != "if x {\n\t" {
		t.Fatalf("Enter changed the code to %q", got)
	}
	if got := sent(w); len(got) != 1 || got[0] != (submitted{"if x {\n\t"}) {
		t.Fatalf("intents %v, want the code committed", got)
	}
}

func TestCodeGrowsWithItsLinesUpToMaxRows(t *testing.T) {
	c := NewCodeEditor()
	c.MaxRows = 3
	c.SetText("one", nil)
	col := Column(c)
	col.Cross = CrossStretch
	_, run := stage(t, col)
	run(60)
	shown := func() float32 { return c.view.H / c.lineH }
	if n := shown(); abs32(n-1) > 0.01 {
		t.Fatalf("one line shows %v lines, want 1", n)
	}
	c.SetText("one\ntwo", nil)
	run(60)
	if n := shown(); abs32(n-2) > 0.01 {
		t.Fatalf("two lines show %v lines, want 2", n)
	}
	c.SetText(strings.Repeat("line\n", 9), nil)
	run(60)
	if n := shown(); abs32(n-3) > 0.01 {
		t.Fatalf("ten lines show %v lines, want MaxRows, 3", n)
	}
}
