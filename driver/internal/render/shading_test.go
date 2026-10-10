//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

var (
	red   = color.NRGBA{R: 0xff, A: 0xff}
	green = color.NRGBA{G: 0xff, A: 0xff}
	blue  = color.NRGBA{B: 0xff, A: 0xff}
)

// drawn draws what paint paints on black, whole, and returns the canvas,
// its rows from the bottom. A frame that changes most of the window
// draws straight to it, so a second frame, changing a pixel, draws on
// the canvas, as drawMask does.
func drawn(r *Renderer, paint1 func(p *paint.Painter)) []byte {
	var p paint.Painter
	p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
	paint1(&p)
	r.canvasOK, r.direct = false, false
	w, h := int(benchSize.W), int(benchSize.H)
	r.Draw(p.Ops(), paint.Everything, w, h, 1)
	r.Draw(p.Ops(), geom.Rc(0, 0, 1, 1), w, h, 1)
	return canvas(r)
}

// near reports whether two pixels differ by at most tol in each channel.
func near(a, b [4]byte, tol int) bool {
	for i := range a {
		if d := int(a[i]) - int(b[i]); d > tol || d < -tol {
			return false
		}
	}
	return true
}

func px(c color.NRGBA) [4]byte { return [4]byte{c.R, c.G, c.B, c.A} }

func TestInsetShadowsShadeInsideTheShapeAlone(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.DrawRRect(paint.RRectOp{
			Rect: geom.Rc(100, 100, 200, 100), Radius: 20, Fill: paint.Solid(color.NRGBA{0x80, 0x80, 0x80, 0xff}),
			Inset: [2]paint.Shadow{
				// A core shadow along the top, and a light rim along the
				// bottom.
				{Offset: geom.Pt(0, 12), Blur: 4, Color: black},
				{Offset: geom.Pt(0, -12), Blur: 4, Color: white},
			},
		})
	})
	if got := pixelAt(pix, 200, 102); got[0] > 0x30 {
		t.Errorf("just inside the top the shape is %v, want the core shadow's dark", got)
	}
	if got := pixelAt(pix, 200, 197); got[0] < 0xd0 {
		t.Errorf("just inside the bottom the shape is %v, want the rim's light", got)
	}
	if got := pixelAt(pix, 200, 150); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 2) {
		t.Errorf("in the middle the shape is %v, want its own grey", got)
	}
	for _, p := range [][2]int{{200, 96}, {200, 204}, {96, 150}, {101, 101}} {
		if got := pixelAt(pix, p[0], p[1]); got != px(black) {
			t.Errorf("outside the shape at %v the canvas is %v, want it untouched", p, got)
		}
	}
}

func TestAShadowOutsideLeavesTheShapeClear(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	// Grey under a see-through shape with a dark shadow kept outside it:
	// the grey shows through the shape undarkened, and the shadow darkens
	// the grey around it.
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(50, 50, 300, 300), 0, paint.Solid(color.NRGBA{0x80, 0x80, 0x80, 0xff}))
		p.ShadowRRect(geom.Rc(100, 100, 200, 100), 12, paint.Solid(color.NRGBA{A: 0}),
			paint.Shadow{Offset: geom.Pt(0, 10), Blur: 8, Color: black, Outside: true})
	})
	if got := pixelAt(pix, 200, 150); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 2) {
		t.Errorf("under the shape the grey is %v, want it undarkened", got)
	}
	if got := pixelAt(pix, 200, 204); got[0] > 0x60 {
		t.Errorf("just below the shape the grey is %v, want the shadow's dark", got)
	}
	// Without Outside the shadow darkens under the shape too.
	pix = drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(50, 50, 300, 300), 0, paint.Solid(color.NRGBA{0x80, 0x80, 0x80, 0xff}))
		p.ShadowRRect(geom.Rc(100, 100, 200, 100), 12, paint.Solid(color.NRGBA{A: 0}),
			paint.Shadow{Offset: geom.Pt(0, 10), Blur: 8, Color: black})
	})
	if got := pixelAt(pix, 200, 150); got[0] > 0x20 {
		t.Errorf("under a shape with a plain shadow the grey is %v, want the shadow's dark", got)
	}
}

func TestARadialGradientRunsOutFromItsCentre(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(100, 100, 200, 200), 0, paint.Fill{Gradient: &paint.Gradient{
			From: geom.Pt(200, 200), To: geom.Pt(300, 200), Start: red, End: blue, Radial: true,
		}})
	})
	if got := pixelAt(pix, 200, 200); !near(got, px(red), 4) {
		t.Errorf("at the centre the gradient is %v, want red", got)
	}
	// Halfway out, in any direction, it is halfway along.
	for _, p := range [][2]int{{250, 200}, {200, 150}, {165, 235}} {
		if got := pixelAt(pix, p[0], p[1]); !near(got, [4]byte{0x80, 0, 0x7f, 0xff}, 6) {
			t.Errorf("halfway out at %v the gradient is %v, want halfway from red to blue", p, got)
		}
	}
	if got := pixelAt(pix, 101, 101); !near(got, px(blue), 2) {
		t.Errorf("past the edge, in the corner, the gradient is %v, want blue", got)
	}
}

func TestAGradientBlendsThroughItsStops(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	grad := &paint.Gradient{From: geom.Pt(100, 0), To: geom.Pt(500, 0), Start: red, End: blue,
		Stops: []paint.Stop{{At: 0.25, Color: green}, {At: 0.75, Color: white}}}
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(100, 100, 400, 50), 0, paint.Fill{Gradient: grad})
	})
	for _, c := range []struct {
		x    int
		want color.NRGBA
	}{{100, red}, {200, green}, {300, grad.At(0.5)}, {400, white}, {499, blue}} {
		if got := pixelAt(pix, c.x, 120); !near(got, px(c.want), 6) {
			t.Errorf("at x %d the gradient is %v, want %v", c.x, got, c.want)
		}
	}
}

func TestAMaskTakesAGradient(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	n := 0
	pix := drawn(r, func(p *paint.Painter) {
		p.MaskFill(countedSquare{n: &n, settled: true}, geom.Rc(100, 100, 64, 64), paint.Fill{Gradient: &paint.Gradient{
			From: geom.Pt(0, 100), To: geom.Pt(0, 164), Start: red, End: blue,
		}})
	})
	if got := pixelAt(pix, 130, 100); !near(got, px(red), 8) {
		t.Errorf("at the mask's top it is %v, want red", got)
	}
	if got := pixelAt(pix, 130, 163); !near(got, px(blue), 8) {
		t.Errorf("at the mask's foot it is %v, want blue", got)
	}
	if got := pixelAt(pix, 130, 170); got != px(black) {
		t.Errorf("below the mask the canvas is %v, want it untouched", got)
	}
}

func TestALayerClipsToAnEllipseInPlace(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	for _, opacity := range []float32{1, 0.5} {
		before := r.Stats.Layers
		pix := drawn(r, func(p *paint.Painter) {
			end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 300, 200), Opacity: opacity, Clip: true, Ellipse: true})
			p.RRect(geom.Rc(0, 0, 800, 600), 0, paint.Solid(white))
			end()
		})
		offscreen := r.Stats.Layers - before
		if opacity == 1 && offscreen != 0 {
			t.Errorf("an opaque layer clipped to an ellipse drew %d offscreen passes, want none", offscreen)
		}
		in := px(white)
		if opacity < 1 {
			in = [4]byte{0x80, 0x80, 0x80, 0xff}
		}
		if got := pixelAt(pix, 250, 200); !near(got, in, 2) {
			t.Errorf("at opacity %v, in the middle of the ellipse the layer is %v, want %v", opacity, got, in)
		}
		// On the ellipse, a quarter of the way along each axis, and just
		// outside it at the corners of its box.
		if got := pixelAt(pix, 250, 102); !near(got, in, 2) {
			t.Errorf("at opacity %v, just inside the ellipse's top the layer is %v, want %v", opacity, got, in)
		}
		for _, p := range [][2]int{{104, 104}, {395, 104}, {104, 295}, {395, 295}, {250, 97}, {98, 200}} {
			if got := pixelAt(pix, p[0], p[1]); got != px(black) {
				t.Errorf("at opacity %v, outside the ellipse at %v the canvas is %v, want it untouched", opacity, p, got)
			}
		}
	}
}

func TestAGradientsColoursMatchWhatItBlends(t *testing.T) {
	g := &paint.Gradient{Start: red, End: blue, Stops: []paint.Stop{{At: 0.5, Color: green}}}
	for _, c := range []struct {
		t    float32
		want color.NRGBA
	}{{-1, red}, {0, red}, {0.25, color.NRGBA{0x80, 0x80, 0, 0xff}}, {0.5, green}, {1, blue}, {2, blue}} {
		if got := g.At(c.t); got != c.want {
			t.Errorf("at %v the gradient is %v, want %v", c.t, got, c.want)
		}
	}
}

func TestAFadedLayerFadesTowardItsEdges(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	for _, clip := range []bool{false, true} {
		pix := drawn(r, func(p *paint.Painter) {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 200, 100), Opacity: 1, Clip: clip,
				Fade: geom.Insets{Left: 40, Right: 40}})()
			// The layer's contents reach past its bounds on every side.
			p.RRect(geom.Rc(50, 50, 300, 200), 0, paint.Solid(white))
		})
		for _, c := range []struct {
			x, y int
			want byte
		}{
			{200, 150, 0xff}, // the middle, past the fades
			{150, 150, 0xff}, // just inside the left fade's end
			{120, 150, 0x80}, // halfway into the left fade
			{280, 150, 0x80}, // and the right
			{101, 150, 0x06}, // all but gone at the left edge
			{200, 102, 0xff}, // the top, which does not fade
			{90, 150, 0},     // outside the bounds
			{200, 90, 0},
			{200, 210, 0},
		} {
			if got := pixelAt(pix, c.x, c.y); !near(got, [4]byte{c.want, c.want, c.want, 0xff}, 6) {
				t.Errorf("clip %v: at (%d, %d) the canvas is %v, want %#x", clip, c.x, c.y, got, c.want)
			}
		}
	}
}

func TestAnEllipticalGradientReachesEachAxisAtItsLength(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		p.RRect(geom.Rc(100, 100, 200, 200), 0, paint.Fill{Gradient: &paint.Gradient{
			From: geom.Pt(200, 200), To: geom.Pt(300, 200), Start: red, End: blue, Radial: true, Aspect: 0.5,
		}})
	})
	// Halfway out is 50 across, along the line to To, and 25 down.
	for _, p := range [][2]int{{250, 200}, {150, 200}, {200, 175}, {200, 225}} {
		if got := pixelAt(pix, p[0], p[1]); !near(got, [4]byte{0x80, 0, 0x7f, 0xff}, 6) {
			t.Errorf("halfway out at %v the ellipse is %v, want halfway from red to blue", p, got)
		}
	}
	if got := pixelAt(pix, 200, 140); !near(got, px(blue), 2) {
		t.Errorf("60 down, past the ellipse's short axis, it is %v, want blue", got)
	}
}
