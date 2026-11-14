//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

var (
	white = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	black = color.NRGBA{A: 0xff}
	lcd   = text.Rendering{Smoothing: text.SubpixelRGB, Hinting: text.HintingLight, Gamma: 1.8}
)

// textOps records black text on white, drawn through wrap, which opens
// and closes whatever the text sits in.
func textOps(wrap func(p *paint.Painter, draw func())) []paint.Op {
	var p paint.Painter
	p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(white))
	run := text.Default().Shape("Hinted text on subpixels", 13)
	wrap(&p, func() { run.Paint(&p, geom.Pt(20, 20), black) })
	return p.Ops()
}

// drewGlyphs draws ops and reports whether the frame drew greyscale
// glyphs and glyphs on subpixels.
func drewGlyphs(r *Renderer, ops []paint.Op) (grey, sub bool) {
	clear(r.glyphs.have)
	clear(r.lcdGlyphs.have)
	r.canvasOK = false
	r.Draw(ops, paint.Everything, int(benchSize.W), int(benchSize.H), 1)
	return len(r.glyphs.have) > 0, len(r.lcdGlyphs.have) > 0
}

func TestSubpixelTextFallsBackToGreyscale(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	if !r.dual {
		t.Skip("the context has no dual-source blending")
	}
	plain := func(_ *paint.Painter, draw func()) { draw() }
	layer := func(o paint.LayerOpts) func(*paint.Painter, func()) {
		return func(p *paint.Painter, draw func()) {
			o.Bounds = geom.Rc(10, 10, 400, 60)
			defer p.Layer(o)()
			draw()
		}
	}
	under := func(tr paint.Transform) func(*paint.Painter, func()) {
		return func(p *paint.Painter, draw func()) {
			defer p.Push(tr)()
			draw()
		}
	}
	for _, c := range []struct {
		name        string
		wrap        func(*paint.Painter, func())
		transparent bool
		sub         bool
	}{
		{"at rest", plain, false, true},
		{"moved", under(paint.Translate(geom.Pt(3.5, 7))), false, true},
		{"in a clipping layer", layer(paint.LayerOpts{Opacity: 1, Clip: true}), false, true},
		{"in an opaque layer", layer(paint.LayerOpts{Opacity: 1}), false, true},
		{"scaled", under(paint.Scale(1.2, geom.Pt(20, 20))), false, false},
		{"rotated", under(paint.Rotate(0.1, geom.Pt(20, 20))), false, false},
		{"fading", layer(paint.LayerOpts{Opacity: 0.5}), false, false},
		// A rounded clip draws in place, cut as it draws, onto what is beneath.
		{"in a rounded clip", layer(paint.LayerOpts{Opacity: 1, Clip: true, Radius: 6}), false, true},
		{"blurred", layer(paint.LayerOpts{Opacity: 1, Blur: 2}), false, false},
		{"on a transparent window", plain, true, false},
	} {
		r.SetText(lcd, c.transparent)
		grey, sub := drewGlyphs(r, textOps(c.wrap))
		if sub != c.sub || grey == c.sub {
			t.Errorf("%s: drew greyscale glyphs %v and subpixel glyphs %v, want subpixels %v", c.name, grey, sub, c.sub)
		}
	}

	r.dual = false
	r.SetText(lcd, false)
	if grey, sub := drewGlyphs(r, textOps(plain)); !grey || sub {
		t.Errorf("without dual-source blending: drew greyscale %v and subpixels %v, want greyscale alone", grey, sub)
	}
}

func TestSubpixelTextHasColourFringes(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	if !r.dual {
		t.Skip("the context has no dual-source blending")
	}
	fringes := func(tr text.Rendering) int {
		r.SetText(tr, false)
		r.canvasOK, r.direct = false, false
		ops := textOps(func(_ *paint.Painter, draw func()) { draw() })
		// The first frame goes straight to the window; the second fills
		// the canvas, which a test can read.
		r.Draw(ops, paint.Everything, int(benchSize.W), int(benchSize.H), 1)
		r.Draw(ops, geom.Rc(0, 0, 1, 1), int(benchSize.W), int(benchSize.H), 1)
		pix := canvas(r)
		n := 0
		for i := 0; i < len(pix); i += 4 {
			if d := int(pix[i]) - int(pix[i+2]); d > 8 || d < -8 {
				n++
			}
		}
		return n
	}
	if n := fringes(lcd); n < 20 {
		t.Errorf("subpixel text has %d pixels whose red and blue differ, want its edges coloured", n)
	}
	if n := fringes(text.Rendering{Smoothing: text.Greyscale, Gamma: 1.8}); n > 0 {
		t.Errorf("greyscale text has %d coloured pixels, want none", n)
	}
}

func TestAnOpaqueClipDrawsInPlace(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	red := color.NRGBA{R: 0xff, A: 0xff}
	var p paint.Painter
	p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(white))
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(10, 10, 40, 40), Opacity: 1, Clip: true})
	p.RRect(geom.Rc(0, 0, 100, 100), 0, paint.Solid(red))
	end()
	// A big frame after a big frame goes straight to the window, with no copy to count.
	r.Draw(p.Ops(), paint.Everything, int(benchSize.W), int(benchSize.H), 1)
	r.draws = 0
	r.Draw(p.Ops(), paint.Everything, int(benchSize.W), int(benchSize.H), 1)
	// One batch before the clip and one inside it, with no composite.
	if r.draws > 2 {
		t.Errorf("a frame with an opaque clip drew in %d calls, want 2", r.draws)
	}
	pix := make([]byte, 4)
	at := func(x, y int) [3]byte {
		r.GL.BindFramebuffer(0x8D40, r.fbo(0))
		r.GL.ReadPixels(pix, int32(x), int32(r.fbH-1-y), 1, 1, 0x1908, 0x1401)
		return [3]byte{pix[0], pix[1], pix[2]}
	}
	if got := at(30, 30); got != [3]byte{0xff, 0, 0} {
		t.Errorf("inside the clip is %v, want red", got)
	}
	for _, pt := range [][2]int{{5, 5}, {60, 30}, {30, 60}} {
		if got := at(pt[0], pt[1]); got != [3]byte{0xff, 0xff, 0xff} {
			t.Errorf("outside the clip at %v is %v, want white", pt, got)
		}
	}
}
