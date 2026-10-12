package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
)

// pile lays its children one under another, each 40 tall.
type pile struct{ kids []gunim.Node }

func (p *pile) Children() []gunim.Node { return p.kids }

func (p *pile) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	y := float32(0)
	for k := range kids.All {
		k.Layout(gunim.Tight(geom.Sz(c.Max.W, 40)))
		k.Place(geom.Pt(0, y))
		y += 40
	}
	return geom.Sz(c.Max.W, y)
}

func (p *pile) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// brick is a row of a pile; its byte gives it a size, so each has an
// address of its own.
type brick struct{ _ byte }

func (*brick) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }
func (*brick) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children)          {}

// grow asks the pile's view to add a row, at the end or at the top, and
// reveal a row: the new one, or the last.
type grow struct{ top, last bool }

// newRevealPile mounts five rows in a scroll view 200 tall, all in view.
func newRevealPile(t *testing.T) (*gunim.Window, *Scroll, *pile, func(int)) {
	t.Helper()
	p := &pile{}
	for range 5 {
		p.kids = append(p.kids, &brick{})
	}
	s := NewScroll(p)
	w := gunimtest.New(t, geom.Sz(300, 200), nil)
	gunim.RegisterView(w, "p", func(struct{}) gunim.Node { return s }, func(gunim.Node, struct{}, *gunim.UI) {})
	gunim.RegisterPatch(w, "p", func(_ gunim.Node, g grow, u *gunim.UI) {
		b := &brick{}
		if g.top {
			p.kids = append([]gunim.Node{b}, p.kids...)
			u.InsertAt(p, 0, b)
		} else {
			p.kids = append(p.kids, b)
			u.Insert(p, b)
		}
		if g.last {
			u.Reveal(p.kids[len(p.kids)-1])
			return
		}
		u.Reveal(b)
	})
	if err := w.Client().Mount(gunim.Root, "p", "p", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(10)
	return w, s, p, run
}

// inView reports whether n lies wholly inside the scroll's view.
func inView(t *testing.T, w *gunim.Window, run func(int), s *Scroll, n gunim.Node) bool {
	t.Helper()
	var view, b geom.Rect
	gunim.RegisterPatch(w, "p", func(_ gunim.Node, _ probeFocus, u *gunim.UI) {
		view, _ = u.Bounds(s)
		b, _ = u.Bounds(n)
	})
	if err := w.Client().Patch("p", probeFocus{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	return b.Min.Y >= view.Min.Y-0.5 && b.Max.Y <= view.Max.Y+0.5
}

func TestARowRevealedAsItIsAddedScrollsIntoView(t *testing.T) {
	for _, c := range []struct {
		name string
		g    grow
	}{
		{"a row added at the end", grow{}},
		{"the last row, pushed down by one added at the top", grow{top: true, last: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, s, p, run := newRevealPile(t)
			// Each new row lands under the view's foot; each is revealed in
			// the handler that adds it, before any frame lays it out.
			for i := range 4 {
				if err := w.Client().Patch("p", c.g); err != nil {
					t.Fatal(err)
				}
				// The furthest the view scrolls; its spring may settle a
				// fraction past it and back.
				want := float32(40*len(p.kids)+40) - 200
				last := s.Offset()
				for f := range 40 {
					run(1)
					now := s.Offset()
					if now < last-0.5 || now > want+0.5 {
						t.Fatalf("add %d, frame %d: the offset went from %.2f to %.2f, the end is %.2f", i, f, last, now, want)
					}
					last = now
				}
				if !inView(t, w, run, s, p.kids[len(p.kids)-1]) {
					t.Fatalf("add %d: the last row is out of view, the offset %.2f", i, s.Offset())
				}
			}
		})
	}
}
