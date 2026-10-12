package markdown

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// stage shows v in an offscreen window, 400 wide, and returns the window and a way to draw frames.
func stage(t *testing.T, v *View) (w *gunim.Window, run func(int)) {
	t.Helper()
	w = gunimtest.New(t, geom.Sz(400, 600), nil)
	gunim.RegisterView(w, "md", func(struct{}) gunim.Node { return v }, nil)
	if err := w.Client().Mount(gunim.Root, "md", "md", nil); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, run
}

// sent returns the intents the window has sent so far.
func sent(w *gunim.Window) []gunim.Intent {
	var out []gunim.Intent
	for {
		select {
		case e := <-w.Client().Intents():
			out = append(out, e.Intent)
		default:
			return out
		}
	}
}

// centre returns the middle of the first piece of text in paragraph i.
func centre(v *View, i int) geom.Point {
	lp := v.paras[i]
	pc := lp.p.Lines[0].Pieces[0]
	return pc.At.Add(lp.at).Add(geom.Pt(pc.Run.Advance/2, pc.Run.Height()/2))
}

func TestClickingALinkSendsIt(t *testing.T) {
	v := New("[the plan](https://example.com/plan)")
	w, run := stage(t, v)
	at := centre(v, 0)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	got := sent(w)
	if len(got) != 1 || got[0] != (Link{URL: "https://example.com/plan"}) {
		t.Fatalf("intents %v, want the link", got)
	}
}

func TestDraggingSelectsAcrossParagraphsAndCtrlCCopies(t *testing.T) {
	v := New("First **part**.\n\n- second\n- third")
	w, run := stage(t, v)
	first, last := v.paras[0], v.paras[2]
	from := first.at.Add(geom.Pt(0.5, 2))
	to := last.at.Add(geom.Pt(last.p.Size.W+20, last.p.Size.H-2))
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: to})
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	want := "First part.\nsecond\nthird"
	if got := v.SelectedText(); got != want {
		t.Fatalf("selected %q, want %q", got, want)
	}
	if c, _ := w.Offscreen().Clipboard(); c != want {
		t.Fatalf("clipboard %q, want %q", c, want)
	}
}

func TestDoubleAndTripleClicksSelectAWordAndALine(t *testing.T) {
	v := New("alpha beta\ngamma")
	v.Breaks = true
	w, run := stage(t, v)
	at := centre(v, 0)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if got := v.SelectedText(); got != "alpha" {
		t.Fatalf("a double click selected %q, want the word", got)
	}
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 3})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if got := v.SelectedText(); got != "alpha beta" {
		t.Fatalf("a triple click selected %q, want the line", got)
	}
}

func TestNewTextDropsTheSelection(t *testing.T) {
	v := New("one two")
	w, run := stage(t, v)
	at := centre(v, 0)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 3})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if v.SelectedText() != "one two" {
		t.Fatalf("a triple click selected %q", v.SelectedText())
	}
	v.SetText("three")
	run(1)
	if s, e := v.Selection(); s != e {
		t.Fatalf("selection %d..%d after new text, want none", s, e)
	}
}

func TestInlineCodeHasOneFillAcrossItsSpaces(t *testing.T) {
	w, _ := stage(t, New("Try `/poll Question | option | option` now"))
	fill := widget.CodeFill.Default()
	n := 0
	for _, op := range w.Offscreen().Ops() {
		if r, ok := op.(*paint.RRectOp); ok && r.Fill.Solid == fill {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the code has %d fills, want one", n)
	}
}
