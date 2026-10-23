package gunim

import (
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// flag is a node that fills its box blue, with a red bar from (20, 30) to (60, 40).
type flag struct{ _ int }

func (flag) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (flag) Paint(p *paint.Painter, _ Frame, box geom.Size, _ Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(color.NRGBA{B: 0xff, A: 0xff}))
	p.RRect(geom.Rc(20, 30, 40, 10), 0, paint.Solid(color.NRGBA{R: 0xff, A: 0xff}))
}

// pixelsOf draws w's next frame and returns it, skipping t where nothing can draw it.
func pixelsOf(t *testing.T, w *Window) *image.RGBA {
	t.Helper()
	w.Frame(time.Second / 60)
	img, err := w.Pixels()
	if errors.Is(err, ErrNoPixels) {
		t.Skipf("no pixels to look at: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// closeTo reports whether c is within a little of want, as a GPU may round.
func closeTo(c, want color.RGBA) bool {
	d := func(a, b uint8) bool { return a-b < 3 || b-a < 3 }
	return d(c.R, want.R) && d(c.G, want.G) && d(c.B, want.B) && d(c.A, want.A)
}

var (
	pxRed  = color.RGBA{R: 0xff, A: 0xff}
	pxBlue = color.RGBA{B: 0xff, A: 0xff}
)

// An offscreen window draws its frame as a window on a display does, the right way up, the window's size.
func TestAnOffscreenWindowDrawsItsPixels(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 100), &flag{})
	img := pixelsOf(t, w)
	if b := img.Bounds(); b != image.Rect(0, 0, 200, 100) {
		t.Fatalf("the picture is %v", b)
	}
	for _, c := range []struct {
		x, y int
		want color.RGBA
	}{
		{40, 35, pxRed}, {21, 31, pxRed}, {58, 38, pxRed},
		{10, 10, pxBlue}, {40, 25, pxBlue}, {40, 45, pxBlue}, {65, 35, pxBlue}, {199, 99, pxBlue},
	} {
		if got := img.RGBAAt(c.x, c.y); !closeTo(got, c.want) {
			t.Errorf("at %d, %d the picture holds %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

// A window resized or zoomed draws at its new size and scale.
func TestAnOffscreenWindowsPixelsFollowItsSizeAndZoom(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 100), &flag{})
	pixelsOf(t, w)
	w.Offscreen().Resize(geom.Sz(300, 150))
	if b := pixelsOf(t, w).Bounds(); b != image.Rect(0, 0, 300, 150) {
		t.Fatalf("resized, the picture is %v", b)
	}
	w.ui.SetZoom(2)
	img := pixelsOf(t, w)
	if b := img.Bounds(); b != image.Rect(0, 0, 300, 150) {
		t.Fatalf("zoomed, the picture is %v", b)
	}
	if got := img.RGBAAt(80, 70); !closeTo(got, pxRed) {
		t.Errorf("zoomed twice, the bar's middle holds %v", got)
	}
	if got := img.RGBAAt(40, 35); !closeTo(got, pxBlue) {
		t.Errorf("zoomed twice, where the bar was holds %v", got)
	}
}
