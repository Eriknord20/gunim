package gunim

import (
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
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
	d := func(a, b uint8) bool { return int(a)-int(b) <= 2 && int(b)-int(a) <= 2 }
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

func TestCloseToTellsColoursApart(t *testing.T) {
	if closeTo(pxRed, pxBlue) || closeTo(color.RGBA{}, pxRed) || !closeTo(pxRed, color.RGBA{R: 0xfd, B: 2, A: 0xff}) {
		t.Fatal("closeTo takes colours apart for the same, or the same for apart")
	}
}

// A window resized since its last frame draws that frame at the size it was laid out in.
func TestAFrameDrawsAtTheSizeItWasLaidOutIn(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 100), &flag{})
	pixelsOf(t, w)
	w.Offscreen().Resize(geom.Sz(300, 150))
	img, err := w.Pixels()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b != image.Rect(0, 0, 200, 100) {
		t.Fatalf("resized after its frame, the picture is %v", b)
	}
}

// A zoomed window's picture is the window's own size, its zoom rounding it no larger.
func TestAZoomedWindowsPictureIsItsOwnSize(t *testing.T) {
	w := NewOffscreen(geom.Sz(79, 41), &flag{})
	w.ui.SetZoom(1.1)
	if b := pixelsOf(t, w).Bounds(); b != image.Rect(0, 0, 79, 41) {
		t.Fatalf("zoomed 1.1, a window 79 by 41 draws %v", b)
	}
}

// What no op covers shows the window's background.
func TestTheWindowsBackgroundShowsWhereNothingDraws(t *testing.T) {
	w := NewOffscreen(geom.Sz(60, 40), nil)
	w.Frame(time.Second / 60)
	green := color.NRGBA{G: 0xff, A: 0xff}
	w.Offscreen().SetBackground(green)
	img, err := w.Pixels()
	if errors.Is(err, ErrNoPixels) {
		t.Skipf("no pixels to look at: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := img.RGBAAt(30, 20); !closeTo(got, color.RGBA{G: 0xff, A: 0xff}) {
		t.Fatalf("over a green background, an empty frame holds %v", got)
	}
}

// A window that has drawn nothing fails to draw, and the failure is no skip.
func TestPixelsBeforeAFrameFailsWithoutSkipping(t *testing.T) {
	w := NewOffscreen(geom.Sz(60, 40), nil)
	if _, err := w.Pixels(); err == nil || errors.Is(err, ErrNoPixels) {
		t.Fatalf("before a frame, Pixels fails with %v", err)
	}
}

// Once offscreen windows draw with GLFW, no window opens on a display in the same run.
func TestPixelsKeepGLFWFromTheDesktop(t *testing.T) {
	pixelsOf(t, NewOffscreen(geom.Sz(60, 40), &flag{}))
	if err := driver.ClaimGLFWForDesktop(); err == nil {
		t.Fatal("the desktop takes GLFW from the offscreen windows")
	}
}
