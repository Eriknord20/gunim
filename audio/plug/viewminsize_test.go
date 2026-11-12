package plug

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// foldingView is a window view whose least size grows as its extra section folds out.
type foldingView struct{ open bool }

var _ ViewMinSizer = (*foldingView)(nil)

// ViewMinSize implements [ViewMinSizer]: 640×520 with the section out, and the descriptor's least size without.
func (v *foldingView) ViewMinSize() geom.Size {
	if v.open {
		return geom.Sz(640, 520)
	}
	return geom.Size{}
}

func (v *foldingView) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

func (v *foldingView) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

// viewLeast returns the least size a host keeps a plugin's window view's window at: the view's own where it is a
// [ViewMinSizer] that says one, and d's otherwise.
func viewLeast(d *Descriptor, v gunim.Node) geom.Size {
	if m, ok := v.(ViewMinSizer); ok {
		if s := m.ViewMinSize(); s != (geom.Size{}) {
			return s
		}
	}
	return d.ViewMinSize
}

// windowHost shows a plugin's window view as a host does, keeping its window no smaller than the view's least size
// each time it lays the view out.
type windowHost struct {
	d    *Descriptor
	view gunim.Node
	err  error
}

func (h *windowHost) Children() []gunim.Node { return []gunim.Node{h.view} }

func (h *windowHost) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	if err := f.UI().SetMinSize(viewLeast(h.d, h.view)); err != nil {
		h.err = err
	}
	kids.At(0).Layout(gunim.Tight(c.Max))
	kids.At(0).Place(geom.Point{})
	return c.Max
}

func (h *windowHost) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// A host opens a plugin's window view at the descriptor's sizes, and keeps the window no smaller than the least size
// the view reports as its state changes.
func TestAHostKeepsAWindowViewNoSmallerThanItsLeastSize(t *testing.T) {
	d := &Descriptor{ID: "test.folding", Name: "Folding", ViewSize: geom.Sz(500, 400), ViewMinSize: geom.Sz(400, 300)}
	v := &foldingView{}
	h := &windowHost{d: d, view: v}
	w := gunim.NewOffscreenWith(gunim.WindowOptions{Size: d.ViewSize, MinSize: viewLeast(d, v)})
	gunim.RegisterView(w, "host", func(struct{}) gunim.Node { return h }, nil)
	if err := w.Client().Mount(gunim.Root, "host", "host", nil); err != nil {
		t.Fatal(err)
	}
	run := func() {
		for range 3 {
			w.Frame(time.Second / 60)
		}
		if h.err != nil {
			t.Fatal(h.err)
		}
	}
	run()
	o := w.Offscreen()
	if got := o.Size(); got != d.ViewSize {
		t.Errorf("the window view's window opened at %v, want %v", got, d.ViewSize)
	}
	o.Resize(geom.Sz(100, 100))
	run()
	if got := o.Size(); got != d.ViewMinSize {
		t.Errorf("sized to 100×100, the window is %v, want the descriptor's least size %v", got, d.ViewMinSize)
	}
	v.open = true
	run()
	if got := o.Size(); got != geom.Sz(640, 520) {
		t.Errorf("with the section folded out, the window is %v, want the view's least size 640×520", got)
	}
}
