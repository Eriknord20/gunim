package gunim

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// steadyRow is a SteadyNode that counts its layouts and paints, and draws a
// rectangle in its colour round its child, a steadyLeaf.
type steadyRow struct {
	key           any
	ok            bool
	laid, painted int
	colour        color.NRGBA
	clipsAChild   bool
}

func (r *steadyRow) SteadyKey(Frame) (any, bool) { return r.key, r.ok }
func (r *steadyRow) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	r.laid++
	for k := range kids.All {
		k.Layout(Tight(geom.Sz(20, 20)))
		k.Place(geom.Pt(10, 10))
	}
	return geom.Sz(200, 40)
}
func (r *steadyRow) Paint(p *paint.Painter, _ Frame, box geom.Size, kids Children) {
	r.painted++
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(r.colour))
	if r.clipsAChild {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Clip: true, Opacity: 1})()
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// steadyLeaf counts its paints, and moves while moving says.
type steadyLeaf struct {
	painted int
	moving  bool
}

func (l *steadyLeaf) Step(time.Duration) bool                             { return l.moving }
func (l *steadyLeaf) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (l *steadyLeaf) Paint(p *paint.Painter, _ Frame, box geom.Size, _ Children) {
	l.painted++
	p.RRect(geom.Rect{Max: box.Point()}, 2, paint.Solid(color.NRGBA{G: 0xff, A: 0xff}))
}

// ticker animates for good, beside the row, as a playhead does: every
// frame draws.
type ticker struct{ n int }

func (t *ticker) Step(time.Duration) bool                             { t.n++; return true }
func (t *ticker) Layout(c Constraints, _ Frame, _ Children) geom.Size { return geom.Sz(4, 40) }
func (t *ticker) Paint(p *paint.Painter, _ Frame, box geom.Size, _ Children) {
	p.RRect(geom.Rc(float32(t.n%100), 0, 4, 40), 0, paint.Solid(color.NRGBA{R: 0xff, A: 0xff}))
}

// steadyStage mounts a steadyRow, with a steadyLeaf in it, and a ticker beside
// it, and runs a few frames for the window to settle.
func steadyStage(t *testing.T, row *steadyRow) (*Window, *steadyLeaf) {
	t.Helper()
	w := newTestWindow()
	leaf := &steadyLeaf{}
	w.ui.Insert(w.ui.Root(), row)
	w.ui.Insert(row, leaf)
	w.ui.Insert(w.ui.Root(), &ticker{})
	run(w, 60)
	return w, leaf
}

// hasColour reports whether the window's last frame drew a shape of c.
func hasColour(t *testing.T, w *Window, c color.NRGBA) bool {
	t.Helper()
	for _, op := range w.mustOffscreen(t).Ops() {
		if r, ok := op.(*paint.RRectOp); ok && r.Fill == paint.Solid(c) {
			return true
		}
	}
	return false
}

var rowBlue = color.NRGBA{B: 0xff, A: 0xff}

func TestAStillSteadyNodeIsNeitherLaidOutNorPaintedAgain(t *testing.T) {
	row := &steadyRow{key: 1, ok: true, colour: rowBlue, clipsAChild: true}
	w, leaf := steadyStage(t, row)
	laid, painted, leafPainted := row.laid, row.painted, leaf.painted
	run(w, 10)
	if row.laid != laid || row.painted != painted || leaf.painted != leafPainted {
		t.Fatalf("ten frames with its key held, the row was laid out %d times more, painted %d, its child painted %d",
			row.laid-laid, row.painted-painted, leaf.painted-leafPainted)
	}
	if !hasColour(t, w, rowBlue) {
		t.Fatal("the carried row is missing from the frame")
	}
	ls := w.ui.index[leaf]
	if ls.drawn != w.ui.seq || ls.laid != w.ui.seq {
		t.Fatal("the carried row's child is not marked drawn in the frame, so input would pass it by")
	}
}

func TestASteadyNodeIsDrawnAnew(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(w *Window, row *steadyRow, leaf *steadyLeaf)
	}{
		{"once its key changes", func(_ *Window, row *steadyRow, _ *steadyLeaf) { row.key = 2 }},
		{"saying ok false", func(_ *Window, row *steadyRow, _ *steadyLeaf) { row.ok = false }},
		{"after Invalidate", func(w *Window, _ *steadyRow, _ *steadyLeaf) { w.ui.Invalidate() }},
		{"after input", func(w *Window, _ *steadyRow, _ *steadyLeaf) { w.ui.stirred = true }},
		{"while a node in it moves", func(_ *Window, _ *steadyRow, leaf *steadyLeaf) { leaf.moving = true }},
	} {
		row := &steadyRow{key: 1, ok: true, colour: rowBlue}
		w, leaf := steadyStage(t, row)
		painted := row.painted
		c.do(w, row, leaf)
		run(w, 1)
		if row.painted == painted {
			t.Errorf("%s, the row was not painted anew", c.name)
		}
	}
}

func TestASteadyNodeChangingItsKeyIsCarriedOnceItHoldsAgain(t *testing.T) {
	row := &steadyRow{key: 1, ok: true, colour: rowBlue}
	w, _ := steadyStage(t, row)
	row.key = 2
	run(w, 1)
	painted := row.painted
	run(w, 5)
	if row.painted != painted {
		t.Fatalf("its new key held, the row was painted %d times more", row.painted-painted)
	}
}

// BenchmarkASteadyRowBesideAPlayhead draws frames of a row of a thousand
// cells beside a ticker, the row steady or not.
func BenchmarkASteadyRowBesideAPlayhead(b *testing.B) {
	for _, steady := range []bool{false, true} {
		b.Run(map[bool]string{false: "drawn", true: "steady"}[steady], func(b *testing.B) {
			w := newTestWindow()
			row := &steadyRow{key: 1, ok: steady, colour: rowBlue}
			w.ui.Insert(w.ui.Root(), row)
			for range 1000 {
				w.ui.Insert(row, &steadyLeaf{})
			}
			w.ui.Insert(w.ui.Root(), &ticker{})
			run(w, 10)
			for b.Loop() {
				w.Frame(time.Second / 60)
			}
		})
	}
}
