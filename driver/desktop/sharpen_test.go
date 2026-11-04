//go:build !js

package desktop

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// stripes is a settled Shape of one-pixel columns, covered and clear by turns: drawn stretched, its columns blur.
type stripes struct{}

func (stripes) Coverage(w, h int) []byte {
	b := make([]byte, w*h)
	for i := range b {
		if i%w%2 == 0 {
			b[i] = 255
		}
	}
	return b
}

func (stripes) Settled() bool { return true }

// A frame that resizes a big mask draws it stretched; the window then draws that frame again by itself, with the
// mask sharp, though the engine sends no frame more. The redraw reports no frame shown, since the engine waits for
// none.
func TestABigMaskDrawnStretchedSharpensWithNoNewFrame(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	dw, err := display.NewWindow(driver.Options{Title: "gunim sharpen", Size: geom.Sz(600, 400)})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := dw.(*Window)
	if !ok {
		t.Fatalf("NewWindow returned a %T", dw)
	}
	defer func() { _ = w.Close() }()

	type frame struct {
		pix  []byte
		w, h int
	}
	got := make(chan frame, 4)
	w.mu.Lock()
	w.readback = func(pix []byte, fw, fh int) {
		select {
		case got <- frame{pix, fw, fh}:
		default:
		}
	}
	w.mu.Unlock()
	// sharp reports whether the frame's row 100 from the top runs red, black, red for the mask's first 40 pixels.
	sharp := func(f frame) bool {
		row := (f.h - 1 - 100) * f.w * 4
		for x := range 40 {
			want := x%2 == 0
			if r := f.pix[row+x*4]; want && r != 0xff || !want && r != 0 {
				return false
			}
		}
		return true
	}
	ops := func(width float32) []paint.Op {
		return []paint.Op{
			&paint.RRectOp{Rect: geom.Rect{Max: w.Size().Point()}, Fill: paint.Solid(color.NRGBA{A: 0xff}),
				Transform: paint.Identity},
			&paint.MaskOp{Shape: stripes{}, Rect: geom.Rc(0, 0, width, 300), Color: color.NRGBA{R: 0xff, A: 0xff},
				Transform: paint.Identity},
		}
	}
	next := func() frame {
		t.Helper()
		select {
		case f := <-got:
			return f
		case <-time.After(5 * time.Second):
			t.Fatal("no frame drawn")
		}
		return frame{}
	}
	for i, width := range []float32{400, 420} {
		if err := w.Present(ops(width), []geom.Rect{paint.Everything}); err != nil {
			t.Fatal(err)
		}
		f := next()
		<-w.Presented()
		if s := sharp(f); s != (i == 0) {
			t.Fatalf("frame %d, the mask %v wide, drew sharp: %v, want %v", i, width, s, i == 0)
		}
	}
	if !sharp(next()) {
		t.Error("the window drew the resized mask again, still stretched")
	}
	select {
	case <-w.Presented():
		t.Error("the window reported its own redraw as a frame shown")
	case <-time.After(100 * time.Millisecond):
	}
}
