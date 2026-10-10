package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// tallDialog opens a dialog whose form, in a Scroll, is taller than the
// window.
func tallDialog(t *testing.T) (w *gunim.Window, s *Scroll, fields []*TextField, run func(int)) {
	t.Helper()
	form := NewForm()
	for range 30 {
		f := NewTextField()
		fields = append(fields, f)
		form.Add("Field", f)
	}
	s = NewScroll(form)
	d := NewDialog("Tall")
	d.Body = focusScroll{s, form}
	d.SetButtons("OK", "Cancel")
	w = gunimtest.New(t, geom.Sz(800, 500), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("d"); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(40)
	return w, s, fields, run
}

// focusScroll is a form in a scroll that hands the dialog its fields.
type focusScroll struct {
	*Scroll
	form *Form
}

func (f focusScroll) Focusables() []gunim.Node { return f.form.Focusables() }

// The wheel over a dialog's form taller than the window scrolls it, and
// it stays scrolled: the dialog measuring its body every frame does not
// take it back to the top.
func TestTheWheelScrollsATallDialog(t *testing.T) {
	w, _, fields, run := tallDialog(t)
	var u *gunim.UI
	gunim.RegisterPatch(w, "d", func(_ gunim.Node, _ probeFocus, ui *gunim.UI) { u = ui })
	if err := w.Client().Patch("d", probeFocus{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	before, _ := u.Bounds(fields[0])
	w.Input(input.PointerMove{Pos: geom.Pt(400, 250)})
	w.Input(input.Scroll{Pos: geom.Pt(400, 250), Delta: geom.Pt(0, -120)})
	run(60)
	after, _ := u.Bounds(fields[0])
	if after.Min.Y > before.Min.Y-100 {
		t.Fatalf("the wheel moved the first field from %v to %v", before.Min.Y, after.Min.Y)
	}
}

// Tab onto a field out of sight scrolls it into view.
func TestTabScrollsATallDialogToTheField(t *testing.T) {
	w, _, fields, run := tallDialog(t)
	for range 20 {
		w.Input(input.KeyPress{Key: input.KeyTab})
	}
	run(60)
	var u *gunim.UI
	gunim.RegisterPatch(w, "d", func(_ gunim.Node, _ probeFocus, ui *gunim.UI) { u = ui })
	if err := w.Client().Patch("d", probeFocus{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	at, ok := u.Bounds(fields[19])
	if !ok || at.Min.Y < 0 || at.Max.Y > 500 {
		t.Fatalf("Tab to field 20 left it at %v, out of the window 500 high", at)
	}
}

// A dialog's form taller than the window, of no scroll of its own,
// scrolls in one of the dialog's: it ends above the buttons, which stay
// in the window, and the wheel moves it.
func TestATallFormScrollsInItsDialog(t *testing.T) {
	form := NewForm()
	fields := make([]*TextField, 0, 30)
	for range 30 {
		f := NewTextField()
		fields = append(fields, f)
		form.Add("Field", f)
	}
	d := NewDialog("Tall")
	d.Body = form
	d.SetButtons("OK", "Cancel")
	w := gunimtest.New(t, geom.Sz(800, 500), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(40)
	var u *gunim.UI
	gunim.RegisterPatch(w, "d", func(_ gunim.Node, _ probeFocus, ui *gunim.UI) { u = ui })
	if err := w.Client().Patch("d", probeFocus{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	held, _ := u.Bounds(d.scroll)
	ok, _ := u.Bounds(d.ok)
	if ok.Max.Y > 500 || ok.Min.Y < 0 || held.Max.Y > ok.Min.Y {
		t.Fatalf("the form runs to %v, the OK button is at %v, in a window 500 high", held.Max.Y, ok)
	}
	before, _ := u.Bounds(fields[0])
	w.Input(input.PointerMove{Pos: geom.Pt(400, 200)})
	w.Input(input.Scroll{Pos: geom.Pt(400, 200), Delta: geom.Pt(0, -120)})
	run(60)
	after, _ := u.Bounds(fields[0])
	if after.Min.Y > before.Min.Y-100 {
		t.Fatalf("the wheel moved the first field from %v to %v", before.Min.Y, after.Min.Y)
	}
}

// A dialog that would only just fit in the window, in its margin but not
// past the window's edge, scrolls too: it keeps its margin rather than
// running up to the top of the window, under a title bar drawn there.
func TestADialogThatOnlyJustFitsKeepsItsMargin(t *testing.T) {
	open := func(h float32) (*Dialog, func(int)) {
		form := NewForm()
		for range 6 {
			form.Add("Field", NewTextField())
		}
		d := NewDialog("Snug")
		d.Body = form
		d.SetButtons("OK", "Cancel")
		w := gunimtest.New(t, geom.Sz(800, h), nil)
		gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
		if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
			t.Fatal(err)
		}
		run := func(n int) {
			for range n {
				w.Frame(time.Second / 60)
			}
		}
		run(40)
		return d, run
	}
	roomy, _ := open(2000)
	natural := roomy.height
	margin := DialogMargin.Get(nil)
	h := natural + margin/2
	d, _ := open(h)
	if d.height > h-margin {
		t.Fatalf("a dialog %v high in a window %v high is %v high, past its margin of %v", natural, h, d.height, margin)
	}
}
