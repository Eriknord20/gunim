//go:build linux || windows || darwin

package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// paintStripes paints one-pixel columns, white and black in turn, over r.
func paintStripes(p *paint.Painter, r geom.Rect) {
	for x := r.Min.X; x < r.Max.X; x += 2 {
		p.RRect(geom.Rc(x, r.Min.Y, 1, r.Max.Y-r.Min.Y), 0, paint.Solid(white))
	}
}

func TestAWideBlurOfFineDetailIsEven(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	// Wide enough to run at an eighth of the window's resolution.
	pix := drawn(r, func(p *paint.Painter) {
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: benchSize.Point()}, Opacity: 1, Blur: 80})
		paintStripes(p, geom.Rect{Max: benchSize.Point()})
		end()
	})
	for x := 300; x < 500; x += 7 {
		if got := pixelAt(pix, x, 300); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 6) {
			t.Fatalf("at %d,300 the blurred stripes are %v, want an even grey", x, got)
		}
	}
}

func TestAWideBlurSpreadsAnEdgeBothWays(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	pix := drawn(r, func(p *paint.Painter) {
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: benchSize.Point()}, Opacity: 1, Blur: 40})
		p.RRect(geom.Rc(0, 0, 400, 600), 0, paint.Solid(white))
		end()
	})
	left, mid, right := pixelAt(pix, 250, 300)[0], pixelAt(pix, 400, 300)[0], pixelAt(pix, 550, 300)[0]
	if left < 0xf0 || right > 0x10 || mid < 0x60 || mid > 0xa0 {
		t.Errorf("across the blurred edge the canvas runs %#x, %#x, %#x, want white, half and black", left, mid, right)
	}
}

// stripedImage is a 256 by 256 image of one-pixel columns, white and
// black in turn.
func stripedImage() *paint.Image {
	src := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := range 256 {
		for x := 0; x < 256; x += 2 {
			src.Set(x, y, color.White)
			src.Set(x+1, y, color.Black)
		}
	}
	return paint.NewImage(src)
}

func TestAnImageDrawnSmallAveragesItsPixels(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	img := stripedImage()
	pix := drawn(r, func(p *paint.Painter) {
		p.Image(img, geom.Rc(100, 100, 32, 32), paint.ImageOpts{Opacity: 1})
	})
	for x := 104; x < 128; x += 5 {
		if got := pixelAt(pix, x, 116); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 6) {
			t.Fatalf("at %d,116 the image drawn at an eighth is %v, want an even grey", x, got)
		}
	}
}

func TestAnImageUploadedInALayerLeavesTheRestAsItWas(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	img := stripedImage()
	teal := color.NRGBA{G: 0x80, B: 0x80, A: 0xff}
	olive := color.NRGBA{R: 0x80, G: 0x80, A: 0xff}
	// Its mipmaps are made in the middle of the layer, between the two
	// rectangles: the second must still go into the layer, clipped.
	pix := drawn(r, func(p *paint.Painter) {
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(100, 100, 300, 200), Opacity: 1, Clip: true})
		p.RRect(geom.Rc(50, 120, 150, 40), 0, paint.Solid(teal))
		p.Image(img, geom.Rc(200, 200, 32, 32), paint.ImageOpts{Opacity: 1})
		p.RRect(geom.Rc(300, 240, 150, 40), 0, paint.Solid(olive))
		end()
	})
	for _, c := range []struct {
		x, y int
		want [4]byte
		what string
	}{
		{120, 140, px(teal), "the teal rectangle inside the layer"},
		{60, 140, px(black), "the teal rectangle outside the clip"},
		{216, 216, [4]byte{0x80, 0x80, 0x80, 0xff}, "the image drawn at an eighth"},
		{350, 260, px(olive), "the olive rectangle inside the layer"},
		{430, 260, px(black), "the olive rectangle outside the clip"},
	} {
		if got := pixelAt(pix, c.x, c.y); !near(got, c.want, 6) {
			t.Errorf("at %d,%d %s is %v, want %v", c.x, c.y, c.what, got, c.want)
		}
	}
}
