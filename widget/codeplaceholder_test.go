package widget

import (
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

const codeHint = "One command per line, for example npm run dev"

// placeholderStage shows an empty code editor with a placeholder, 600 by
// 300, and gives it the keyboard.
func placeholderStage(t *testing.T) (*CodeEditor, *gunim.Window, func(int)) {
	t.Helper()
	c := NewCodeEditor()
	c.Highlight = nil
	c.Placeholder = codeHint
	w, run := stage(t, &frame{child: c, size: geom.Sz(600, 300)})
	click(w, 300, 150)
	run(1)
	return c, w, run
}

// hintOp returns the text op in the placeholder's colour that w drew
// last, or nil.
func hintOp(w *gunim.Window) *paint.TextOp {
	ph := Placeholder.Default()
	for _, op := range textOps(w.Offscreen().Ops()) {
		if c := op.Color; c.R == ph.R && c.G == ph.G && c.B == ph.B && c.A > 0 {
			return op
		}
	}
	return nil
}

// goneAt runs the frame an edit shows in and the frames after it, and
// fails on any of them that draws the placeholder or leaves its spring
// above nothing, as the editor holds code or a composition on all of
// them. The placeholder must never be drawn under the code, not even
// fading out.
func goneAt(t *testing.T, c *CodeEditor, w *gunim.Window, run func(int), what string) {
	t.Helper()
	for frame := range 30 {
		run(1)
		if len(c.text) == 0 && len(c.preedit) == 0 {
			t.Fatalf("%s: frame %d has no code and no composition", what, frame)
		}
		if v := c.hint.Value(); v != 0 || c.hint.Active() {
			t.Fatalf("%s: frame %d has the placeholder at %v, moving %v, under the code; want it gone at once",
				what, frame, v, c.hint.Active())
		}
		if hintOp(w) != nil {
			t.Fatalf("%s: frame %d drew the placeholder under the code", what, frame)
		}
	}
}

// fadeTo runs frames until the placeholder rests at want, and fails on
// a frame that moves it the wrong way or by a jump. The first frame, the
// one the edit shows in, must already move it. What shows is the spring
// held between 0 and 1, as it may swing a little past its end.
func fadeTo(t *testing.T, c *CodeEditor, w *gunim.Window, run func(int), want float32, what string) {
	t.Helper()
	const fadeStep = 0.3
	shown := func() float32 { return min(max(c.hint.Value(), 0), 1) }
	last := shown()
	if last == want {
		t.Fatalf("%s: the placeholder is already at %v, want it to fade there", what, want)
	}
	for frames := 0; last != want || c.hint.Active(); frames++ {
		if frames > 120 {
			t.Fatalf("%s: the placeholder is still at %v after %d frames, want %v", what, last, frames, want)
		}
		run(1)
		v := shown()
		toward := (v - last) * (want - last)
		if toward < 0 || (toward == 0 && v != want) || abs32(v-last) > fadeStep {
			t.Fatalf("%s: frame %d took the placeholder from %v to %v, want a step of at most %v toward %v",
				what, frames, last, v, fadeStep, want)
		}
		// What shows matches the spring on every frame: drawn while it
		// shows at all, faded by it, and gone at nothing.
		op := hintOp(w)
		switch {
		case v <= 0 && op != nil:
			t.Fatalf("%s: frame %d drew the placeholder at nothing", what, frames)
		case v > 0.01 && op == nil:
			t.Fatalf("%s: frame %d drew no placeholder at %v", what, frames, v)
		case op != nil && abs32(float32(op.Color.A)-float32(Placeholder.Default().A)*v) > 1:
			t.Fatalf("%s: frame %d drew the placeholder at alpha %d, want %v of %d",
				what, frames, op.Color.A, v, Placeholder.Default().A)
		}
		last = v
	}
}

func TestCodePlaceholderShowsInAnEmptyEditor(t *testing.T) {
	c, w, _ := placeholderStage(t)
	op := hintOp(w)
	if op == nil {
		t.Fatal("an empty editor drew no placeholder")
	}
	if op.Color != Placeholder.Default() {
		t.Fatalf("the placeholder is drawn in %v, want %v", op.Color, Placeholder.Default())
	}
	if op.Size != CodeSize.Default() {
		t.Fatalf("the placeholder is drawn at size %v, want the code's %v", op.Size, CodeSize.Default())
	}
	if c.Text() != "" || len(c.lines) != 1 || c.widest != 0 || c.maxScrollX() != 0 {
		t.Fatalf("the placeholder became code: text %q, %d lines, widest %v, scroll room %v",
			c.Text(), len(c.lines), c.widest, c.maxScrollX())
	}
	// Without the keyboard, it shows too.
	c2 := NewCodeEditor()
	c2.Placeholder = codeHint
	w2, _ := stage(t, &frame{child: c2, size: geom.Sz(600, 300)})
	if hintOp(w2) == nil {
		t.Fatal("an empty editor without the keyboard drew no placeholder")
	}
}

// The placeholder sits where the first line's code is drawn, in the
// code's face and size.
func TestCodePlaceholderSitsWhereTheCodeWouldBe(t *testing.T) {
	for _, numbers := range []bool{true, false} {
		c := NewCodeEditor()
		c.Highlight, c.Numbers, c.Placeholder = nil, numbers, codeHint
		w, run := stage(t, &frame{child: c, size: geom.Sz(600, 300)})
		click(w, 300, 150)
		run(1)
		ph := hintOp(w)
		if ph == nil {
			t.Fatal("an empty editor drew no placeholder")
		}
		w.Input(input.TextInput{Text: "O"})
		run(60)
		var code *paint.TextOp
		for _, op := range textOps(w.Offscreen().Ops()) {
			if op.Color == Ink.Default() && len(op.Glyphs) == 1 && op.Glyphs[0].ID == ph.Glyphs[0].ID {
				code = op
			}
		}
		if code == nil {
			t.Fatal("the typed code drew no text")
		}
		want := code.Transform.Apply(code.Glyphs[0].At)
		got := ph.Transform.Apply(ph.Glyphs[0].At)
		if d := got.Sub(want); abs32(d.X) > 0.01 || abs32(d.Y) > 0.01 {
			t.Errorf("numbers %v: the placeholder starts at %v, the first line's code at %v", numbers, got, want)
		}
		if ph.Size != code.Size || ph.Glyphs[0].ID != code.Glyphs[0].ID {
			t.Errorf("numbers %v: the placeholder's O is glyph %d at %v, the code's glyph %d at %v",
				numbers, ph.Glyphs[0].ID, ph.Size, code.Glyphs[0].ID, code.Size)
		}
	}
}

// Typing, pasting, composing, undo and redo each put the placeholder
// away on the very frame the first character shows, and keep it away
// while there is code. Emptying the code, by deleting, undoing or
// cancelling, fades it back from nothing, on every frame.
func TestCodePlaceholderGoesAtOnceAndFadesBack(t *testing.T) {
	c, w, run := placeholderStage(t)
	if c.hint.Value() != 1 {
		t.Fatalf("the placeholder starts at %v, want 1 from the first frame", c.hint.Value())
	}

	w.Input(input.TextInput{Text: "l"})
	goneAt(t, c, w, run, "typing")
	w.Input(input.KeyPress{Key: input.KeyBackspace})
	if c.Text() != "" {
		t.Fatalf("a backspace left %q", c.Text())
	}
	fadeTo(t, c, w, run, 1, "deleting all")

	if err := w.Offscreen().SetClipboard("make"); err != nil {
		t.Fatal(err)
	}
	w.Input(input.KeyPress{Key: input.KeyV, Mods: input.ModControl})
	goneAt(t, c, w, run, "pasting")
	w.Input(input.KeyPress{Key: input.KeyZ, Mods: input.ModControl})
	fadeTo(t, c, w, run, 1, "undoing the paste")
	w.Input(input.KeyPress{Key: input.KeyZ, Mods: input.ModControl | input.ModShift})
	if c.Text() != "make" {
		t.Fatalf("redo made %q, want the paste back", c.Text())
	}
	goneAt(t, c, w, run, "redo")
	typeKeys(w, run, input.ModControl, input.KeyA)
	w.Input(input.KeyPress{Key: input.KeyDelete})
	fadeTo(t, c, w, run, 1, "deleting the selection")
	w.Input(input.KeyPress{Key: input.KeyZ, Mods: input.ModControl})
	if c.Text() != "make" {
		t.Fatalf("undo made %q, want the deleted code back", c.Text())
	}
	goneAt(t, c, w, run, "undoing the delete")
	typeKeys(w, run, input.ModControl, input.KeyA)
	w.Input(input.KeyPress{Key: input.KeyDelete})
	fadeTo(t, c, w, run, 1, "deleting the selection again")

	w.Input(input.Composing{Text: "に", Selected: [2]int{3, 3}})
	goneAt(t, c, w, run, "composing")
	if c.Text() != "" {
		t.Fatalf("a composition put %q in the code", c.Text())
	}
	w.Input(input.Composing{})
	fadeTo(t, c, w, run, 1, "cancelling the composition")
}

// Emptying the code and typing again while the placeholder is still
// fading in puts it away at once, from wherever the fade had got to.
func TestCodePlaceholderGoesAtOnceMidFade(t *testing.T) {
	for _, edit := range []struct {
		what string
		in   input.Event
	}{
		{"typing", input.TextInput{Text: "x"}},
		{"composing", input.Composing{Text: "に", Selected: [2]int{3, 3}}},
	} {
		c, w, run := placeholderStage(t)
		w.Input(input.TextInput{Text: "l"})
		goneAt(t, c, w, run, edit.what+" first")
		typeKeys(w, run, 0, input.KeyBackspace)
		run(2)
		if v := c.hint.Value(); v <= 0 || v >= 1 || hintOp(w) == nil {
			t.Fatalf("%s: three frames after emptying, the placeholder is at %v, want part-way in", edit.what, v)
		}
		w.Input(edit.in)
		goneAt(t, c, w, run, edit.what+" mid-fade")
	}
}

// The placeholder is never code: the text getter, a selection, Copy and
// a screen reader's value all leave it out.
func TestCodePlaceholderIsNotCode(t *testing.T) {
	c, w, run := placeholderStage(t)
	if err := w.Offscreen().SetClipboard("kept"); err != nil {
		t.Fatal(err)
	}
	typeKeys(w, run, input.ModControl, input.KeyA, input.KeyC)
	if got, _ := w.Offscreen().Clipboard(); got != "kept" {
		t.Fatalf("Ctrl+A, Ctrl+C on the empty editor copied %q", got)
	}
	if s, e := c.Selection(); s != 0 || e != 0 {
		t.Fatalf("Ctrl+A selected %d to %d of the empty editor", s, e)
	}
	in := c.Access()
	if in.Value != "" || c.Text() != "" {
		t.Fatalf("the screen reader's value is %q and the text %q, want both empty", in.Value, c.Text())
	}
	// A screen reader names the editor by its Label, and by the
	// placeholder without one, as TextField and TextArea are named.
	if in.Name != codeHint {
		t.Fatalf("the editor is named %q without a Label, want the placeholder", in.Name)
	}
	c.Label = "Start lines"
	if in := c.Access(); in.Name != "Start lines" {
		t.Fatalf("the editor is named %q with a Label, want the Label", in.Name)
	}
	if in.Role != access.RoleTextField {
		t.Fatalf("the role is %v", in.Role)
	}
}

// A placeholder wider than the editor is cut with an ellipsis inside the
// view, and leaves the editor nothing to scroll.
func TestCodePlaceholderIsCutToTheView(t *testing.T) {
	c := NewCodeEditor()
	c.Placeholder = strings.Repeat("wide ", 40)
	w, run := stage(t, &frame{child: c, size: geom.Sz(200, 100)})
	run(1)
	op := hintOp(w)
	if op == nil {
		t.Fatal("a narrow empty editor drew no placeholder")
	}
	full := c.hintRun.shape(c.laidFace, c.Placeholder, c.laidSize)
	if len(op.Glyphs) >= len(full.Glyphs) {
		t.Fatalf("the placeholder drew %d glyphs of %d, want it cut", len(op.Glyphs), len(full.Glyphs))
	}
	ell := c.hintEll.shape(c.laidFace, "…", c.laidSize)
	if lastID := op.Glyphs[len(op.Glyphs)-1].ID; lastID != ell.Glyphs[0].ID {
		t.Fatalf("the cut placeholder ends in glyph %d, want the ellipsis %d", lastID, ell.Glyphs[0].ID)
	}
	start := op.Transform.Apply(op.Glyphs[0].At).X
	if end := start + fitRun(full, &c.hintEll, c.view.W).Advance; end > start+c.view.W+0.01 {
		t.Fatalf("the cut placeholder runs to %v, past the view's end at %v", end, start+c.view.W)
	}
	if c.maxScrollX() != 0 || c.maxScrollY() != 0 {
		t.Fatalf("the placeholder gave the editor %v by %v to scroll", c.maxScrollX(), c.maxScrollY())
	}
}

// With no placeholder set, nothing is drawn in its colour, and its
// spring rests, typing or not.
func TestCodeWithoutAPlaceholderDrawsNone(t *testing.T) {
	c, w, run := codeStage(t, "")
	if hintOp(w) != nil {
		t.Fatal("an editor without a placeholder drew one")
	}
	w.Input(input.TextInput{Text: "x"})
	run(1)
	if c.hint.Value() != 0 || c.hint.Active() {
		t.Fatalf("the placeholder's spring is at %v, moving %v, with none set", c.hint.Value(), c.hint.Active())
	}
	if in := c.Access(); in.Name != "" {
		t.Fatalf("the editor is named %q with no Label or placeholder", in.Name)
	}
}
