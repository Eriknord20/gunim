//go:build linux || windows || darwin

package render

import (
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// zoomFrame draws ops twice, as a damaged frame fills the canvas, and returns the canvas.
func zoomFrame(r *Renderer, ops []paint.Op) []byte {
	r.canvasOK, r.direct = false, false
	r.Draw(ops, paint.Everything, int(benchSize.W), int(benchSize.H), 1)
	r.Draw(ops, geom.Rc(0, 0, 1, 1), int(benchSize.W), int(benchSize.H), 1)
	return canvas(r)
}

// TestZoomedTextIsSharp draws text at 13 px under a scale of 2.5, as a zoomed view does, and the same text at
// 32.5 px with no scale. The two match: the zoomed glyphs rasterize at the size they show at, where stretching
// the 13 px glyphs blurs every edge.
func TestZoomedTextIsSharp(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	r.SetText(text.Rendering{Smoothing: text.Greyscale, Gamma: 1.8}, false)
	const k = 2.5
	draw := func(size float32, zoom bool) []byte {
		var p paint.Painter
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(white))
		end := func() {}
		at := geom.Pt(20, 40)
		if zoom {
			end = p.Push(paint.Scale(k, geom.Point{}))
		} else {
			at = at.Mul(k)
		}
		run := text.Default().Shape("Zoomed in, sharp", size)
		run.Paint(&p, at, black)
		end()
		return zoomFrame(r, p.Ops())
	}
	zoomed := draw(13, true)
	sharp := draw(13*k, false)
	ink, off := 0, 0
	for i := 0; i < len(sharp); i += 4 {
		if sharp[i] < 128 {
			ink++
		}
		if d := int(sharp[i]) - int(zoomed[i]); d > 48 || d < -48 {
			off++
		}
	}
	if ink < 500 {
		t.Fatalf("the text drew %d dark pixels, want it drawn", ink)
	}
	if off > ink/50 {
		t.Errorf("zoomed text differs from text drawn at its size in %d pixels of %d inked, want under %d", off, ink, ink/50)
	}
}

// sizeShape is a filled square that records the device size it is asked for.
type sizeShape struct{ w, h int }

func (s *sizeShape) Coverage(w, h int) []byte {
	s.w, s.h = w, h
	pix := make([]byte, w*h)
	for i := range pix {
		pix[i] = 255
	}
	return pix
}

func (s *sizeShape) Settled() bool { return true }

// TestZoomedMaskRasterizesAtItsSize checks a mask under a zoom asks for its coverage at the size it shows at.
func TestZoomedMaskRasterizesAtItsSize(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	for _, c := range []struct {
		name string
		tr   paint.Transform
		w, h int
	}{
		{"at rest", paint.Translate(geom.Pt(3, 4)), 20, 10},
		{"zoomed in", paint.Scale(2.5, geom.Pt(7, 7)), 50, 25},
		{"zoomed out", paint.Scale(0.4, geom.Point{}), 8, 4},
		{"turned", paint.Rotate(0.2, geom.Point{}), 20, 10},
	} {
		s := &sizeShape{}
		var p paint.Painter
		end := p.Push(c.tr)
		p.Mask(s, geom.Rc(30, 30, 20, 10), black)
		end()
		r.Draw(p.Ops(), paint.Everything, int(benchSize.W), int(benchSize.H), 1)
		if s.w != c.w || s.h != c.h {
			t.Errorf("%s: the mask rasterized at %dx%d, want %dx%d", c.name, s.w, s.h, c.w, c.h)
		}
	}
}
