//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// Two changes far apart redraw two boxes, each in a pass of its own:
// what lies between them keeps what the canvas held, and a see-through
// op reaching across all three is drawn once over each pixel, not again
// over the part between.
func TestDamageApartRedrawsEachPartAndNothingBetween(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	w, h := int(benchSize.W), int(benchSize.H)
	red := color.NRGBA{R: 0xff, A: 0xff}
	blue := color.NRGBA{B: 0xff, A: 0xff}
	grey := color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	frame := func(left, middle, right color.NRGBA) []paint.Op {
		var p paint.Painter
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
		p.RRect(geom.Rc(50, 100, 100, 100), 0, paint.Solid(left))
		p.RRect(geom.Rc(350, 100, 100, 100), 0, paint.Solid(middle))
		p.RRect(geom.Rc(650, 100, 100, 100), 0, paint.Solid(right))
		// A see-through band across all three.
		p.RRect(geom.Rc(0, 140, 800, 20), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x40}))
		return p.Ops()
	}
	r.canvasOK, r.direct, r.big = false, false, false
	r.Draw(frame(red, grey, red), paint.Everything, w, h, 1)
	read := func() []byte {
		pix := make([]byte, w*h*4)
		r.GL.BindFramebuffer(gl.FRAMEBUFFER, r.WindowFBO)
		r.GL.ReadPixels(pix, 0, 0, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE)
		return pix
	}
	before := read()
	// The middle turns white too, but the damage leaves it out: a
	// middle drawn again would show it.
	r.DrawRects(frame(blue, white, blue), []geom.Rect{geom.Rc(50, 100, 100, 100), geom.Rc(650, 100, 100, 100)}, w, h, 1)
	if len(r.Boxes) != 2 {
		t.Fatalf("the frame redrew %d boxes, %v, want 2", len(r.Boxes), r.Boxes)
	}
	after := read()
	if got := pixelAt(after, 100, 120); got != [4]byte{0, 0, 0xff, 0xff} {
		t.Errorf("the left square is %v, want blue", got)
	}
	if got := pixelAt(after, 700, 120); got != [4]byte{0, 0, 0xff, 0xff} {
		t.Errorf("the right square is %v, want blue", got)
	}
	for _, at := range [][2]int{{400, 120}, {400, 150}, {250, 150}, {550, 150}} {
		if got, was := pixelAt(after, at[0], at[1]), pixelAt(before, at[0], at[1]); got != was {
			t.Errorf("at %v, between the boxes, the frame drew %v over %v", at, got, was)
		}
	}
	// The band over a box is blended once: as bright over blue as a
	// whole frame draws it.
	band := pixelAt(after, 100, 150)
	r.Draw(frame(blue, white, blue), paint.Everything, w, h, 1)
	if whole := pixelAt(read(), 100, 150); !near(band, whole, 1) {
		t.Errorf("the band over the left box is %v, drawn whole %v", band, whole)
	}
}

// Damage boxes that overlap once grown to whole pixels are drawn as
// one, so no pixel is drawn twice.
func TestDamageBoxesThatOverlapMerge(t *testing.T) {
	r := &Renderer{fbW: 800, fbH: 600, scale: 1}
	bs := r.deviceBoxes([]geom.Rect{geom.Rc(10, 10, 10, 10), geom.Rc(21, 10, 10, 10), geom.Rc(400, 400, 10, 10), {}})
	if len(bs) != 2 {
		t.Fatalf("boxes %v, want the two near ones merged and the empty one left out", bs)
	}
}

// A layer that clips to a box outside what a frame redraws is skipped
// whole, its inner layers too, and the replay goes on after it.
func TestALayerOutsideTheRedrawIsSkipped(t *testing.T) {
	var p paint.Painter
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(600, 0, 100, 100), Opacity: 1, Clip: true})
	inner := p.Layer(paint.LayerOpts{Bounds: geom.Rc(600, 0, 50, 50), Opacity: 1, Clip: true})
	p.RRect(geom.Rc(600, 0, 10, 10), 0, paint.Solid(black))
	inner()
	end()
	p.RRect(geom.Rc(0, 0, 10, 10), 0, paint.Solid(black))
	ops := p.Ops()
	r := &Renderer{fbW: 800, fbH: 600, scale: 1, cull: geom.Rc(0, 0, 100, 100), clip: geom.Rc(0, 0, 800, 600)}
	l, ok := ops[0].(*paint.LayerOp)
	if !ok || !r.culled(l) {
		t.Fatal("a clipping layer outside the redraw is not culled")
	}
	if got := layerEnd(ops, 0); got != 4 {
		t.Fatalf("the layer ends at op %d, want 4", got)
	}
	r.cull = geom.Rc(550, 0, 100, 100)
	if r.culled(l) {
		t.Fatal("a layer the redraw touches is culled")
	}
}
