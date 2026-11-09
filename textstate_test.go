package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// stateWindow is an offscreen window that keeps the text states the
// engine sends, as a phone's driver does.
type stateWindow struct {
	*driver.OffscreenWindow
	sent []sentState
}

type sentState struct {
	state *input.TextState
	seq   uint64
}

func (w *stateWindow) SetTextState(s *input.TextState, seq uint64) {
	w.sent = append(w.sent, sentState{s, seq})
}

// take returns what the engine has sent since the last take.
func (w *stateWindow) take() []sentState {
	s := w.sent
	w.sent = nil
	return s
}

// editsText is a node that keeps a text and makes the edits it is sent.
type editsText struct {
	recorder
	text string
}

func (*editsText) TakesText() bool { return true }

func (n *editsText) TextState() input.TextState {
	c := len(n.text)
	return input.TextState{Text: n.text, Selection: [2]int{c, c}, Composing: [2]int{c, c}}
}

func (n *editsText) Handle(e input.Event, u *UI) bool {
	if te, ok := e.(input.TextEdit); ok {
		n.text = n.text[:te.Replace[0]] + te.With + n.text[te.Replace[1]:]
	}
	return n.recorder.Handle(e, u)
}

func TestTheDriverHearsTheFocusedTextAndTheEditsItHolds(t *testing.T) {
	sw := &stateWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(sw, nil)
	field, other := &editsText{text: "hi"}, &recorder{}
	w.ui.Insert(w.ui.Root(), field)
	w.ui.Insert(w.ui.Root(), other)
	frame := func() { w.Frame(time.Second / 60) }

	w.ui.Focus(field)
	got := sw.take()
	if len(got) != 1 || got[0].state == nil || got[0].state.Text != "hi" || got[0].seq != 0 {
		t.Fatalf("on focus the driver heard %+v, want hi at seq 0", got)
	}
	frame()
	if got := sw.take(); len(got) != 0 {
		t.Fatalf("a frame with nothing changed sent %+v", got)
	}

	// Two edits in flight: the state after the first is behind the
	// driver, and says so by its seq.
	s, _ := field.TextState().Apply(field.TextState().Commit("!"))
	w.Input(input.TextEdit{Replace: [2]int{2, 2}, With: "!", Selection: [2]int{3, 3}, Composing: [2]int{3, 3}, Seq: 1})
	frame()
	if got := sw.take(); len(got) != 1 || *got[0].state != s || got[0].seq != 1 {
		t.Fatalf("after edit 1 the driver heard %+v, want %+v at seq 1", got, s)
	}
	w.Input(input.TextEdit{Replace: [2]int{3, 3}, Selection: [2]int{3, 3}, Composing: [2]int{3, 3}, Seq: 2})
	frame()
	if got := sw.take(); len(got) != 1 || *got[0].state != s || got[0].seq != 2 {
		t.Fatalf("an edit that changed nothing told the driver %+v, want the same state at seq 2", got)
	}

	// Text the program sets reaches the driver at the seq it already has.
	field.text = "set"
	w.ui.Invalidate()
	frame()
	if got := sw.take(); len(got) != 1 || got[0].state.Text != "set" || got[0].seq != 2 {
		t.Fatalf("text set by the program reached the driver as %+v", got)
	}

	w.ui.Focus(other)
	if got := sw.take(); len(got) != 1 || got[0].state != nil {
		t.Fatalf("focus leaving the text told the driver %+v, want nil", got)
	}
}

// keysText is a text node that a key changes, with what keys it heard.
type keysText struct {
	recorder
	text  string
	caret int
}

func (*keysText) TakesText() bool { return true }

func (n *keysText) TextState() input.TextState {
	return input.TextState{Text: n.text, Selection: [2]int{n.caret, n.caret}, Composing: [2]int{n.caret, n.caret}}
}

func (n *keysText) Handle(e input.Event, u *UI) bool {
	switch e := e.(type) {
	case input.KeyPress:
		if e.Key == input.KeyBackspace {
			n.text = n.text[:len(n.text)-1]
			n.caret = len(n.text)
		}
		if e.Key == input.KeyLeft {
			n.caret--
		}
	case input.TextInput:
		n.text += e.Text
		n.caret = len(n.text)
	case input.PointerDown:
		n.caret = 0
	}
	return n.recorder.Handle(e, u)
}

// TestTheDriverHearsAChangeBeforeTheNextFrame: a browser's mirror of the text stands
// in for it to what the browser types next, and that can come before any frame.
func TestTheDriverHearsAChangeBeforeTheNextFrame(t *testing.T) {
	sw := &stateWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(sw, nil)
	field := &keysText{text: "abc", caret: 3}
	w.ui.Insert(w.ui.Root(), field)
	w.Frame(time.Second / 60)
	w.ui.Focus(field)
	sw.take()
	check := func(what, text string, caret int) {
		t.Helper()
		got := sw.take()
		if len(got) != 1 || got[0].state == nil || got[0].state.Text != text || got[0].state.Selection != [2]int{caret, caret} {
			t.Fatalf("%s, with no frame, told the driver %+v, want %q at %d", what, got, text, caret)
		}
	}
	w.Input(input.KeyPress{Key: input.KeyBackspace})
	check("a backspace", "ab", 2)
	w.Input(input.KeyPress{Key: input.KeyLeft})
	check("an arrow", "ab", 1)
	w.Input(input.TextInput{Text: "d"})
	check("typing", "abd", 3)
	w.Input(input.KeyPress{Key: input.KeyEnd})
	if got := sw.take(); len(got) != 0 {
		t.Fatalf("a key that changed nothing told the driver %+v", got)
	}
	// A pointer's moves don't cost the state of the text each.
	w.Input(input.PointerMove{Pos: geom.Pt(5, 5)})
	if got := sw.take(); len(got) != 0 {
		t.Fatalf("a pointer's move told the driver %+v", got)
	}
}

func TestEachTextNodeTakingTheFocusStartsTheKeyboardOver(t *testing.T) {
	// Two empty fields have the same state, and the second still has to
	// reach the driver, as a field of its own.
	sw := &stateWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(sw, nil)
	a, b := &editsText{}, &editsText{}
	w.ui.Insert(w.ui.Root(), a)
	w.ui.Insert(w.ui.Root(), b)
	w.ui.Focus(a)
	w.ui.Focus(b)
	if got := sw.take(); len(got) != 2 || got[0].state == nil || got[1].state == nil {
		t.Fatalf("the driver heard %+v, want a state for each field", got)
	}
}

// keyboardWindow is an offscreen window with a keyboard on the screen,
// that counts the times the engine asks for it.
type keyboardWindow struct {
	*driver.OffscreenWindow
	asked int
}

func (w *keyboardWindow) ShowKeyboard() { w.asked++ }

// box is a recorder of a fixed size, so presses can land on it or miss.
type box struct {
	editsText
	size geom.Size
}

func (b *box) Layout(Constraints, Frame, Children) geom.Size { return b.size }
func (*box) Focusable() bool                                 { return true }

func TestATapOnTheFocusedTextAsksForTheKeyboard(t *testing.T) {
	kw := &keyboardWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(kw, nil)
	field := &box{size: geom.Sz(200, 40)}
	w.ui.Insert(w.ui.Root(), field)
	w.ui.Focus(field)
	w.Frame(time.Second / 60)
	if kw.asked != 0 {
		t.Fatalf("focus alone asked for the keyboard %d times, want none until a tap", kw.asked)
	}

	tap := func(p geom.Point) {
		w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary})
		w.Frame(time.Second / 60)
	}
	tap(geom.Pt(20, 20))
	if kw.asked != 1 {
		t.Fatalf("a tap on the focused field asked %d times, want once", kw.asked)
	}
	tap(geom.Pt(500, 500))
	if kw.asked != 1 {
		t.Fatalf("a tap beside the field asked for the keyboard, %d times in all", kw.asked)
	}

	// A press on the field let go off it, as a finger that turns into a
	// scroll lets go, leaves the keyboard as it was.
	w.Input(input.PointerDown{Pos: geom.Pt(20, 20), Button: input.ButtonPrimary, Clicks: 1})
	if kw.asked != 1 {
		t.Fatalf("a press on the field asked for the keyboard before it was let go, %d times in all", kw.asked)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(-1e6, -1e6), Button: input.ButtonPrimary})
	w.Frame(time.Second / 60)
	if kw.asked != 1 {
		t.Fatalf("a press let go away from the field asked for the keyboard, %d times in all", kw.asked)
	}
}

// boxWindow is an offscreen window that hears the text caret and the
// bounds of the node it is in.
type boxWindow struct {
	*driver.OffscreenWindow
	carets, boxes []geom.Rect
}

func (w *boxWindow) SetTextCaret(r geom.Rect) { w.carets = append(w.carets, r) }
func (w *boxWindow) SetTextBox(r geom.Rect)   { w.boxes = append(w.boxes, r) }

// caretBox is a text node of a fixed size with its caret on its
// second line.
type caretBox struct{ box }

func (*caretBox) TextCaret() geom.Rect { return geom.Rc(10, 20, 1, 16) }

func TestTheDriverHearsWhereTheFocusedTextBoxIs(t *testing.T) {
	bw := &boxWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(bw, nil)
	field := &caretBox{box{size: geom.Sz(300, 60)}}
	w.ui.Insert(w.ui.Root(), field)
	w.ui.Focus(field)
	w.Frame(time.Second / 60)
	if len(bw.boxes) != 1 || bw.boxes[0].Size() != geom.Sz(300, 60) {
		t.Fatalf("the driver heard boxes %v, want one 300×60", bw.boxes)
	}
	if len(bw.carets) != 1 || bw.carets[0].Min.Sub(bw.boxes[0].Min) != geom.Pt(10, 20) {
		t.Fatalf("the driver heard carets %v in box %v, want one at 10,20 in it", bw.carets, bw.boxes)
	}
	w.Frame(time.Second / 60)
	if len(bw.boxes) != 1 {
		t.Fatalf("a frame with nothing moved told the driver the box again: %v", bw.boxes)
	}
}

func TestTheTextBoxIsToldAgainAsFocusComesBack(t *testing.T) {
	// The driver forgets the box as focus leaves it; focus coming back
	// to the same box, where it was, tells the driver again.
	bw := &boxWindow{OffscreenWindow: driver.Offscreen(geom.Sz(800, 600))}
	w := newWindow(bw, nil)
	field, other := &caretBox{box{size: geom.Sz(300, 60)}}, &recorder{}
	w.ui.Insert(w.ui.Root(), field)
	w.ui.Insert(w.ui.Root(), other)
	w.ui.Focus(field)
	w.Frame(time.Second / 60)
	w.ui.Focus(other)
	w.Frame(time.Second / 60)
	w.ui.Focus(field)
	w.Frame(time.Second / 60)
	if len(bw.boxes) != 2 || bw.boxes[0] != bw.boxes[1] {
		t.Fatalf("the driver heard boxes %v, want the same box twice", bw.boxes)
	}
	if len(bw.carets) != 2 {
		t.Fatalf("the driver heard carets %v, want the caret twice", bw.carets)
	}
}
