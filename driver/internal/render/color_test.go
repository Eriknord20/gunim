//go:build linux || windows || darwin

package render

import (
	"image/color"
	"math"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/icc"
	"github.com/marrasen/gunim/paint"
)

// shown draws the frame twice, as drawn does, and returns the window's
// framebuffer, its rows from the bottom, and the draw calls the second
// frame took.
func shown(r *Renderer, paint1 func(p *paint.Painter)) ([]byte, int) {
	var p paint.Painter
	p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
	paint1(&p)
	r.canvasOK, r.direct = false, false
	w, h := int(benchSize.W), int(benchSize.H)
	r.Draw(p.Ops(), paint.Everything, w, h, 1)
	before := r.draws
	r.Draw(p.Ops(), geom.Rc(0, 0, 1, 1), w, h, 1)
	draws := r.draws - before
	pix := make([]byte, w*h*4)
	r.GL.BindFramebuffer(gl.FRAMEBUFFER, r.WindowFBO)
	r.GL.ReadPixels(pix, 0, 0, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE)
	return pix, draws
}

func redAndGrey(p *paint.Painter) {
	p.RRect(geom.Rc(100, 100, 100, 100), 0, paint.Solid(color.NRGBA{R: 0xff, A: 0xff}))
	p.RRect(geom.Rc(300, 100, 100, 100), 0, paint.Solid(color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}))
	p.RRect(geom.Rc(500, 100, 100, 100), 0, paint.Solid(color.NRGBA{R: 0x20, G: 0xa0, B: 0x60, A: 0xff}))
}

func TestTheWindowShowsTheFrameInTheDisplaysColours(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	plain, plainDraws := shown(r, redAndGrey)
	if got := pixelAt(plain, 150, 150); got != [4]byte{0xff, 0, 0, 0xff} {
		t.Fatalf("red with no profile is %v", got)
	}

	lut := icc.DisplayP3().LUT(icc.LUTSize)
	if err := r.SetColor(lut); err != nil {
		t.Fatal(err)
	}
	if !r.Converting() {
		t.Fatal("not converting")
	}
	conv, convDraws := shown(r, redAndGrey)
	if got := pixelAt(conv, 150, 150); !near(got, [4]byte{234, 51, 35, 0xff}, 1) {
		t.Errorf("sRGB red on a Display P3 screen is %v, want 234,51,35", got)
	}
	if got := pixelAt(conv, 350, 150); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 1) {
		t.Errorf("grey on a Display P3 screen is %v, want it grey", got)
	}
	// An arbitrary colour, between the table's steps, comes out as the
	// CPU's conversion says.
	want := lut.Apply([3]float32{0x20 / 255.0, 0xa0 / 255.0, 0x60 / 255.0})
	got := pixelAt(conv, 550, 150)
	for c := range 3 {
		if d := math.Abs(float64(got[c]) - float64(want[c])*255); d > 1 {
			t.Errorf("a green on a Display P3 screen is %v, want %v", got, want)
			break
		}
	}
	// The canvas stays in sRGB, for backdrops and partial redraws.
	if got := pixelAt(canvas(r), 150, 150); got != [4]byte{0xff, 0, 0, 0xff} {
		t.Errorf("the canvas holds %v, want sRGB red", got)
	}
	t.Logf("draw calls for a small frame: %d plain, %d converting", plainDraws, convDraws)
	if convDraws != plainDraws {
		t.Errorf("converting took %d draw calls, plain %d: the conversion should ride on the copy", convDraws, plainDraws)
	}

	if err := r.SetColor(nil); err != nil {
		t.Fatal(err)
	}
	back, _ := shown(r, redAndGrey)
	if got := pixelAt(back, 150, 150); got != [4]byte{0xff, 0, 0, 0xff} {
		t.Errorf("red once the profile is gone is %v", got)
	}
}

func TestAConvertingWindowNeverDrawsStraightToTheWindow(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	var p paint.Painter
	p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(color.NRGBA{R: 0xff, A: 0xff}))
	w, h := int(benchSize.W), int(benchSize.H)
	for range 3 {
		r.Draw(p.Ops(), paint.Everything, w, h, 1)
	}
	if !r.direct {
		t.Fatal("big frames with no profile should go straight to the window")
	}
	if err := r.SetColor(icc.DisplayP3().LUT(icc.LUTSize)); err != nil {
		t.Fatal(err)
	}
	r.Draw(p.Ops(), paint.Everything, w, h, 1)
	if r.direct {
		t.Fatal("a converting frame went straight to the window")
	}
	if r.Redrawn != (geom.Rect{Max: geom.Pt(float32(w), float32(h))}) {
		t.Errorf("the frame after the table changed redrew %v, want the whole window", r.Redrawn)
	}
	pix := make([]byte, 4)
	r.GL.BindFramebuffer(gl.FRAMEBUFFER, 0)
	r.GL.ReadPixels(pix, 10, 10, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE)
	if got := [4]byte(pix); !near(got, [4]byte{234, 51, 35, 0xff}, 1) {
		t.Errorf("a whole red window on a Display P3 screen is %v, want 234,51,35", got)
	}
}

func TestHalfFloats(t *testing.T) {
	for _, c := range []struct {
		v    float32
		want uint16
	}{{0, 0}, {1, 0x3c00}, {0.5, 0x3800}, {0.333333, 0x3555}, {1e-5, 0x00a8}, {65504, 0x7bff}} {
		if got := half(c.v); got != c.want {
			t.Errorf("half(%v) = %#x, want %#x", c.v, got, c.want)
		}
	}
}
