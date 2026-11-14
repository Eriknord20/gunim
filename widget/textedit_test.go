package widget

import (
	"slices"
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// keyboard plays a phone's keyboard: it keeps a copy of the field's
// state, as a driver does, edits the copy, and sends each edit.
type keyboard struct {
	t    *testing.T
	ty   *typer
	copy input.TextState
}

func newTestKeyboard(ty *typer) *keyboard {
	return &keyboard{t: ty.t, ty: ty, copy: ty.field.TextState()}
}

// send applies e to the copy and sends it, then checks the field's
// state agrees with the copy, as it must for the driver to see no
// change it has to start the keyboard over for.
func (k *keyboard) send(e input.TextEdit) {
	k.t.Helper()
	next, ok := k.copy.Apply(e)
	if !ok {
		k.t.Fatalf("edit %+v outside the copy %+v", e, k.copy)
	}
	k.copy = next
	k.ty.w.Input(e)
	k.ty.run(1)
	if got := k.ty.field.TextState(); got != k.copy {
		k.t.Fatalf("after %+v the field's state is %+v, the keyboard's copy %+v", e, got, k.copy)
	}
}

func TestAKeyboardFixesAWordBehindTheCaret(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hte cat ")
	k := newTestKeyboard(ty)
	k.send(input.TextEdit{Replace: [2]int{0, 3}, With: "the", Selection: [2]int{8, 8}, Composing: [2]int{8, 8}})
	ty.want("the cat ", 8)
	if ty.field.caret != 8 {
		t.Fatalf("caret at %d after the fix, want it left at the end, 8", ty.field.caret)
	}
	ty.key(input.KeyZ, input.ModControl)
	ty.want("hte cat ", 8)
}

func TestAKeyboardBackspaceDeletesWithAnEdit(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("abc")
	k := newTestKeyboard(ty)
	k.send(input.TextEdit{Replace: [2]int{2, 3}, Selection: [2]int{2, 2}, Composing: [2]int{2, 2}})
	ty.want("ab", 2)
	k.send(input.TextEdit{Replace: [2]int{1, 2}, Selection: [2]int{1, 1}, Composing: [2]int{1, 1}})
	ty.want("a", 1)
	ty.key(input.KeyZ, input.ModControl)
	ty.want("abc", 3) // deletes one after another undo as one step
}

func TestAWordTakenUpToComposeStaysInTheText(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello world")
	ty.intents()
	k := newTestKeyboard(ty)
	// The keyboard takes "world" up again, as Gboard does when the
	// caret comes back into a word.
	k.send(input.TextEdit{Replace: [2]int{6, 11}, With: "world", Selection: [2]int{11, 11}, Composing: [2]int{6, 11}})
	k.send(k.copy.Compose("worlds", [2]int{6, 6}))
	ty.want("hello world", 11)
	if got := ty.intents(); len(got) != 0 {
		t.Fatalf("composing over a word sent %v, want nothing until it commits", got)
	}
	k.send(k.copy.Commit("worlds"))
	ty.want("hello worlds", 12)
	if got := ty.intents(); !slices.Equal(got, []gunim.Intent{changed{"hello worlds"}}) {
		t.Fatalf("the commit sent %v, want one change", got)
	}
	ty.key(input.KeyZ, input.ModControl)
	ty.want("hello world", 11)
}

func TestSwipeTypingCommitsAWordAtATime(t *testing.T) {
	ty := newTyper(t)
	k := newTestKeyboard(ty)
	for _, w := range []string{"swipe", " typing"} {
		k.send(k.copy.Compose(w, [2]int{len(w), len(w)}))
		k.send(k.copy.Commit(w))
	}
	ty.want("swipe typing", 12)
}

func TestAnEditTheTextCleansMovesTheKeyboardOn(t *testing.T) {
	// A single line keeps no newline: the edit goes in as a space, and
	// the field's state differs from the keyboard's copy, which is how
	// the driver learns to start the keyboard over.
	ty := newTyper(t)
	ty.w.Input(input.TextEdit{Replace: [2]int{0, 0}, With: "a\nb", Selection: [2]int{3, 3}, Composing: [2]int{3, 3}})
	ty.run(1)
	ty.want("a b", 3)
	if s := ty.field.TextState(); s.Text != "a b" || s.Selection != [2]int{3, 3} {
		t.Fatalf("state %+v, want a b with the caret at its end", s)
	}
}

func TestALongTextShowsTheKeyboardAStretchAroundTheCaret(t *testing.T) {
	wr := newWriter(t, 400)
	text := strings.Repeat("å", 3*textWindow) // two bytes each
	wr.area.SetText(text, nil)
	wr.area.set(textWindow+10, false)
	s := wr.area.TextState()
	if s.Start != 2*10 || len(s.Text) != 2*2*textWindow {
		t.Fatalf("stretch from %d, %d bytes; want from 20, %d bytes", s.Start, len(s.Text), 4*textWindow)
	}
	at := 2 * (textWindow + 10)
	if s.Selection != [2]int{at, at} {
		t.Fatalf("selection %v, want the caret at byte %d of the whole text", s.Selection, at)
	}
	wr.w.Input(s.Commit("x"))
	wr.run(1)
	if got := wr.area.Text(); got != text[:at]+"x"+text[at:] {
		t.Fatalf("the edit landed elsewhere: %q", got[at-4:at+5])
	}
}

func TestAnEmptyCompositionKeepsTheSelection(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello")
	ty.key(input.KeyA, input.ModControl)
	ty.w.Input(input.Composing{})
	ty.run(1)
	ty.want("hello", 5)
	if got := ty.field.TextState().Selection; got != [2]int{0, 5} {
		t.Fatalf("selection %v after an empty composition, want it kept, [0 5]", got)
	}
}

func TestACancelledCompositionLeavesTheSelectionItCovered(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello")
	ty.key(input.KeyA, input.ModControl)
	ty.w.Input(input.Composing{Text: "k", Selected: [2]int{1, 1}})
	ty.run(1)
	ty.w.Input(input.Composing{})
	ty.run(1)
	ty.want("hello", 5)
}

func TestACompositionCommittedOverASelectionUndoesInOneStep(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("hello")
	ty.key(input.KeyA, input.ModControl)
	ty.w.Input(input.Composing{Text: "nihon", Selected: [2]int{5, 5}})
	ty.run(1)
	// Windows ends the composition, then sends what it committed.
	ty.w.Input(input.Composing{})
	ty.w.Input(input.TextInput{Text: "日本"})
	ty.run(1)
	ty.want("日本", 2)
	ty.key(input.KeyZ, input.ModControl)
	ty.want("hello", 5)
}

func TestTypingARuneLikeTheNextUndoesAsOneStep(t *testing.T) {
	ty := newTyper(t)
	ty.typeText("ab")
	ty.key(input.KeyLeft, 0)
	for _, r := range "bbx" {
		ty.typeText(string(r))
	}
	ty.want("abbxb", 4)
	ty.key(input.KeyZ, input.ModControl)
	ty.want("ab", 1)
}

// keysFasterThanFrames sends what a fast typist's keys bring, with no frame between,
// so nothing has laid the text out since the first of them.
func keysFasterThanFrames(t *testing.T, node gunim.Node, text func() string) {
	t.Helper()
	fr := &frame{child: node, size: geom.Sz(300, 200)}
	w, run := stage(t, fr)
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18)})
	run(1)
	press := func(k input.Key) { w.Input(input.KeyPress{Key: k}) }
	w.Input(input.TextInput{Text: "hello"})
	press(input.KeyLeft)
	w.Input(input.TextInput{Text: "x"})
	if got := text(); got != "hellxo" {
		t.Fatalf("hello, Left, x with no frame gave %q", got)
	}
	press(input.KeyRight)
	press(input.KeyLeft)
	press(input.KeyLeft)
	press(input.KeyBackspace)
	w.Input(input.TextInput{Text: "y"})
	if got := text(); got != "helyxo" {
		t.Fatalf("Right, two Lefts, Backspace, y with no frame gave %q", got)
	}
	press(input.KeyEnd)
	w.Input(input.TextInput{Text: "z"})
	press(input.KeyHome)
	w.Input(input.TextInput{Text: "a"})
	if got := text(); got != "ahelyxoz" {
		t.Fatalf("End, z, Home, a with no frame gave %q", got)
	}
}

func TestKeysFasterThanFramesInAField(t *testing.T) {
	f := NewTextField()
	keysFasterThanFrames(t, f, f.Text)
}

func TestKeysFasterThanFramesInAnArea(t *testing.T) {
	a := NewTextArea()
	keysFasterThanFrames(t, a, a.Text)
}

func TestKeysFasterThanFramesInCode(t *testing.T) {
	c := NewCodeEditor()
	keysFasterThanFrames(t, c, c.Text)
}
