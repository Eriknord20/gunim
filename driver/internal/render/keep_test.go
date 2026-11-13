//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// A renderer that keeps its frames in its canvas draws even big frames
// there, never straight to the window, and never copies the canvas to the
// window: a compositor lays the canvas itself.
func TestKeepInCanvasLeavesTheWindowAlone(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	w, h := int(benchSize.W), int(benchSize.H)
	// A window's framebuffer of its own, filled with magenta, which no draw
	// may touch.
	g := r.GL
	win := target{}
	r.fitSize(&win, w, h)
	g.BindFramebuffer(gl.FRAMEBUFFER, win.fbo)
	g.Viewport(0, 0, int32(w), int32(h))
	g.ClearColor(1, 0, 1, 1)
	g.Clear(glColorBufferBit)
	g.ClearColor(0, 0, 0, 0)
	defer func() {
		g.DeleteFramebuffer(win.fbo)
		g.DeleteTexture(win.tex)
	}()
	r.WindowFBO, r.KeepInCanvas = win.fbo, true
	green := color.NRGBA{G: 0xff, A: 0xff}
	frame := func(c color.NRGBA) []paint.Op {
		var p paint.Painter
		p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(c))
		return p.Ops()
	}
	// Two big frames in a row: the second would go straight to the window
	// without KeepInCanvas.
	r.Draw(frame(green), paint.Everything, w, h, 1)
	r.Draw(frame(ember), paint.Everything, w, h, 1)
	if r.direct {
		t.Fatal("a big frame went straight to the window")
	}
	if r.CanvasTexture() == 0 {
		t.Fatal("no canvas texture")
	}
	pix := canvas(r)
	at := (h/2*w + w/2) * 4
	if got := [4]byte(pix[at : at+4]); got != px(ember) {
		t.Fatalf("the canvas holds %v, want the last frame's %v", got, px(ember))
	}
	g.BindFramebuffer(gl.FRAMEBUFFER, win.fbo)
	got := make([]byte, 4)
	g.ReadPixels(got, int32(w/2), int32(h/2), 1, 1, gl.RGBA, gl.UNSIGNED_BYTE)
	if [4]byte(got) != [4]byte{0xff, 0, 0xff, 0xff} {
		t.Fatalf("the window holds %v, want it untouched, magenta", got)
	}
}
