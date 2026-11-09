package render

import (
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// An opaque layer clipped to a rounded rectangle draws in place, cut as it draws, as one clipped to an ellipse does,
// and its edges come out as the same layer drawn offscreen and cut as it is composited. One quad deep, that is: see
// inPlace for quads stacked at the rim.
func TestALayerClipsToARoundedRectangleInPlace(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	draw := func(opacity float32) func(p *paint.Painter) {
		return func(p *paint.Painter) {
			end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 300, 200), Opacity: opacity, Clip: true, Radius: 30})
			p.RRect(geom.Rc(0, 0, 800, 600), 0, paint.Solid(white))
			end()
		}
	}
	before := r.Stats.Layers
	pix := drawn(r, draw(1))
	if offscreen := r.Stats.Layers - before; offscreen != 0 {
		t.Errorf("an opaque layer clipped to a rounded rectangle drew %d offscreen passes, want none", offscreen)
	}
	for _, p := range [][2]int{{250, 200}, {250, 101}, {101, 200}, {398, 200}, {250, 298}, {112, 112}, {387, 287}} {
		if got := pixelAt(pix, p[0], p[1]); !near(got, px(white), 2) {
			t.Errorf("inside the rounded rectangle at %v the layer is %v, want white", p, got)
		}
	}
	// Past the rounded corners, in the corners of its box, and outside it.
	for _, p := range [][2]int{{101, 101}, {398, 101}, {101, 298}, {398, 298}, {104, 106}, {250, 97}, {97, 200}} {
		if got := pixelAt(pix, p[0], p[1]); got != px(black) {
			t.Errorf("outside the rounded rectangle at %v the canvas is %v, want it untouched", p, got)
		}
	}
	// The same layer offscreen, all but opaque, has the same edges, to within its opacity and the rounding.
	off := drawn(r, draw(0.999))
	for y := 95; y < 305; y++ {
		for x := 95; x < 405; x++ {
			if a, b := pixelAt(pix, x, y), pixelAt(off, x, y); !near(a, b, 3) {
				t.Fatalf("at %d,%d the layer drawn in place is %v and offscreen %v", x, y, a, b)
			}
		}
	}
}

// One cut at a time: a rounded clip inside another, or inside an ellipse drawn in place, draws offscreen, and so
// does one the layer scales unevenly, whose corners would no longer be round.
func TestARoundedClipDrawsOffscreenWhereItCantCutInPlace(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	rounded := paint.LayerOpts{Bounds: geom.Rc(120, 120, 100, 60), Opacity: 1, Clip: true, Radius: 8}
	for _, c := range []struct {
		name  string
		paint func(p *paint.Painter)
	}{
		{"inside another rounded clip", func(p *paint.Painter) {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 300, 200), Opacity: 1, Clip: true, Radius: 30})()
			defer p.Layer(rounded)()
			p.RRect(geom.Rc(0, 0, 800, 600), 0, paint.Solid(white))
		}},
		{"inside an ellipse", func(p *paint.Painter) {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 300, 200), Opacity: 1, Clip: true, Ellipse: true})()
			defer p.Layer(rounded)()
			p.RRect(geom.Rc(0, 0, 800, 600), 0, paint.Solid(white))
		}},
		{"scaled unevenly", func(p *paint.Painter) {
			defer p.Push(paint.Transform{A: 2, E: 1})()
			defer p.Layer(rounded)()
			p.RRect(geom.Rc(0, 0, 800, 600), 0, paint.Solid(white))
		}},
	} {
		before := r.Stats.Layers
		drawn(r, c.paint)
		if offscreen := r.Stats.Layers - before; offscreen != 1 {
			t.Errorf("a rounded clip %s drew %d offscreen passes, want 1", c.name, offscreen)
		}
	}
}
