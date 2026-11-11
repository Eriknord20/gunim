package gunim

import (
	"testing"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// revealer stacks its children 40 tall each, laying out none of those
// in skip. A reveal sets its scroll spring going toward the rect.
type revealer struct {
	anim.Group
	kids    []Node
	skip    map[Node]bool
	reveals []geom.Rect
	scroll  *anim.Float
}

func newRevealer(n int) *revealer {
	r := &revealer{skip: map[Node]bool{}, scroll: anim.NewFloat(0)}
	r.Add(r.scroll)
	for range n {
		r.kids = append(r.kids, newFadePanel())
	}
	return r
}

func (r *revealer) Children() []Node { return r.kids }

func (r *revealer) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	y := float32(0)
	for k := range kids.All {
		if r.skip[k.Node()] {
			continue
		}
		k.Layout(Tight(geom.Sz(c.Max.W, 40)))
		k.Place(geom.Pt(0, y))
		y += 40
	}
	return c.Max
}

func (r *revealer) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		if !r.skip[k.Node()] {
			k.Paint(p)
		}
	}
}

func (r *revealer) Reveal(rect geom.Rect, _ *UI) {
	r.reveals = append(r.reveals, rect)
	r.scroll.Animate(rect.Min.Y, anim.Snappy)
}

// mountRevealer puts a revealer of n rows in a window, and runs it to
// rest.
func mountRevealer(t *testing.T, n int) (*Window, *revealer) {
	t.Helper()
	w := newTestWindow()
	r := newRevealer(n)
	w.ui.Insert(w.ui.Root(), r)
	run(w, 120)
	if w.ui.needsFrame() {
		t.Fatal("setup: the window should be idle")
	}
	return w, r
}

func TestARevealWakesAnIdleWindowAndScrolls(t *testing.T) {
	w, r := mountRevealer(t, 3)
	w.ui.Reveal(r.kids[2])
	if !w.ui.needsFrame() {
		t.Fatal("a reveal left the window idle: it would wait for the next input")
	}
	run(w, 1)
	if len(r.reveals) != 1 || r.reveals[0].Min.Y != 80 {
		t.Fatalf("the revealer was asked %v, want the third row's box, from 80", r.reveals)
	}
	if !w.ui.needsFrame() {
		t.Fatal("the window went idle with the reveal's scroll just started")
	}
	run(w, 120)
	if got := r.scroll.Value(); got < 79.5 || got > 80.5 {
		t.Fatalf("the scroll settled at %v, want 80", got)
	}
}

func TestARevealWaitsForTheFrameThatLaysTheNodeOut(t *testing.T) {
	w, r := mountRevealer(t, 3)
	r.skip[r.kids[1]] = true
	w.ui.Invalidate()
	run(w, 1)
	w.ui.Reveal(r.kids[1])
	run(w, 5)
	if len(r.reveals) != 0 {
		t.Fatalf("a row left out of the layout was revealed at %v", r.reveals)
	}
	delete(r.skip, r.kids[1])
	w.ui.Invalidate()
	run(w, 1)
	if len(r.reveals) != 1 || r.reveals[0].Min.Y != 40 {
		t.Fatalf("once laid out the row was revealed at %v, want once at 40", r.reveals)
	}
}

func TestARevealOfANodeLeavingIsDropped(t *testing.T) {
	w, r := mountRevealer(t, 3)
	gone := r.kids[2]
	w.ui.Remove(gone)
	w.ui.Reveal(gone)
	run(w, 1)
	if len(r.reveals) != 0 {
		t.Fatalf("a row on its way out was revealed at %v", r.reveals)
	}
	run(w, 120)
	if inTree(w.ui, gone) {
		t.Fatal("setup: the row should have left")
	}
	if len(r.reveals) != 0 || len(w.ui.revealing) != 0 {
		t.Fatalf("after the row left, %d reveals happened and %d wait", len(r.reveals), len(w.ui.revealing))
	}
}
