//go:build js && wasm

package web

import (
	"image/color"
	"syscall/js"
	"testing"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// Colours the tests paint with.
var (
	testRed   = color.NRGBA{R: 255, A: 255}
	testGreen = color.NRGBA{G: 255, A: 255}
	testBlue  = color.NRGBA{B: 255, A: 255}
)

// testWindow opens a window that fills the canvas, closed again as the test
// ends.
func testWindow(t *testing.T) (*Driver, *Window) {
	t.Helper()
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.NewWindow(driver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := raw.(*Window)
	t.Cleanup(func() {
		_ = w.Close()
		d.frame()
	})
	return d, w
}

// testPopup opens a popup over w, 40×40, at 20, 20.
func testPopup(t *testing.T, d *Driver, w *Window) *Window {
	t.Helper()
	raw, err := d.NewWindow(driver.Options{
		Kind: driver.KindPopup, Parent: w, Size: geom.Sz(40, 40), Over: true,
		Anchor: geom.Rect{Min: geom.Pt(20, 20), Max: geom.Pt(20, 20)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw.(*Window)
}

// solidFrame is a frame of one colour over the window.
func solidFrame(c color.NRGBA, w *Window) []paint.Op {
	sz := w.Size()
	return []paint.Op{&paint.RRectOp{Rect: geom.Rect{Max: geom.Pt(sz.W, sz.H)}, Fill: paint.Solid(c), Transform: paint.Identity}}
}

// present sends w a whole frame of one colour.
func present(t *testing.T, w *Window, c color.NRGBA) {
	t.Helper()
	if err := w.Present(solidFrame(c, w), []geom.Rect{paint.Everything}); err != nil {
		t.Fatal(err)
	}
}

// pixelAt reads the canvas at x, y, in device pixels from the top left. Call it
// in the task that drew, before the browser shows the canvas and clears it.
func pixelAt(d *Driver, x, y int) [4]byte {
	var p [4]byte
	d.g.BindFramebuffer(gl.FRAMEBUFFER, 0)
	d.g.ReadPixels(p[:], int32(x), int32(d.surfH-1-y), 1, 1, gl.RGBA, gl.UNSIGNED_BYTE)
	return p
}

// noGLError fails the test if WebGL has recorded an error.
func noGLError(t *testing.T, d *Driver) {
	t.Helper()
	if e := d.g.GetError(); e != 0 {
		t.Errorf("WebGL error %#x", e)
	}
}

// countGL counts the GL objects alive that are made from now on. Calling the
// function it returns gives the count of each kind.
func countGL(t *testing.T, d *Driver) func() map[string]int {
	t.Helper()
	ctx := d.canvas.Call("getContext", "webgl2")
	kinds := []string{"Texture", "Framebuffer", "Buffer", "VertexArray", "Renderbuffer"}
	live := js.Global().Call("eval", `(ctx, kinds) => {
		const live = {};
		for (const k of kinds) {
			const make = ctx["create" + k], free = ctx["delete" + k];
			live[k] = 0;
			ctx["create" + k] = function () { live[k]++; return make.apply(this, arguments); };
			ctx["delete" + k] = function (o) { if (o) live[k]--; return free.apply(this, arguments); };
		}
		return live;
	}`).Invoke(ctx, js.ValueOf([]any{kinds[0], kinds[1], kinds[2], kinds[3], kinds[4]}))
	t.Cleanup(func() {
		for _, k := range kinds {
			js.Global().Get("Reflect").Call("deleteProperty", ctx, "create"+k)
			js.Global().Get("Reflect").Call("deleteProperty", ctx, "delete"+k)
		}
	})
	return func() map[string]int {
		m := map[string]int{}
		for _, k := range kinds {
			m[k] = live.Get(k).Int()
		}
		return m
	}
}

// TestClosedPopupsFreeTheirGPUMemory is issue 315's first item: a popup held
// on to about 2.5 MB of textures for the life of the page.
func TestClosedPopupsFreeTheirGPUMemory(t *testing.T) {
	d, w := testWindow(t)
	open := func() *Window {
		p := testPopup(t, d, w)
		present(t, p, testBlue)
		return p
	}
	present(t, w, testRed)
	// The first popup has the window under it make its texture, which stays.
	first := open()
	d.frame()
	_ = first.Close()
	d.frame()
	live := countGL(t, d)
	for i := range 40 {
		p := open()
		d.frame()
		switch i % 4 {
		case 0: // Closes between frames.
			_ = p.Close()
			d.frame()
		case 1: // Closes with a frame waiting, which is never drawn.
			present(t, p, testGreen)
			_ = p.Close()
			d.frame()
		case 2: // Closes hidden, so the stack the compositor laid stays as it is.
			_ = p.Hide()
			d.frame()
			_ = p.Close()
			d.frame()
		case 3: // Opens and closes between frames, so it never draws.
			_ = p.Close()
			q := open()
			_ = q.Close()
			d.frame()
		}
		for k, n := range live() {
			if n != 0 {
				t.Fatalf("after popup %d, %d more of %s are alive", i+1, n, k)
			}
		}
		if len(d.gone) != 0 {
			t.Fatalf("after popup %d, %d panes wait to be freed", i+1, len(d.gone))
		}
	}
	noGLError(t, d)
	// The window under them goes on drawing.
	present(t, w, testGreen)
	d.frame()
	if p := pixelAt(d, 4, 4); p[1] < 240 {
		t.Fatalf("the window under them shows %v", p)
	}
	noGLError(t, d)
}

// The other windows draw with GL state a closing window's renderer freed, in
// the same frame.
func TestAPopupClosingLeavesTheOtherPopupDrawing(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	a, b := testPopup(t, d, w), testPopup(t, d, w)
	present(t, a, testBlue)
	present(t, b, testGreen)
	d.frame()
	_ = b.Close()
	present(t, a, testBlue)
	present(t, w, testRed)
	d.frame()
	s := float64(d.scale)
	if p := pixelAt(d, int(30*s), int(30*s)); p[2] < 240 || p[1] > 10 {
		t.Fatalf("the popup left shows %v", p)
	}
	if p := pixelAt(d, 4, 4); p[0] < 240 {
		t.Fatalf("the window shows %v", p)
	}
	noGLError(t, d)
	_ = a.Close()
	d.frame()
}

// Alone on the canvas the window draws straight onto it, whatever size the
// canvas takes, and a popup opening after a resize has it make a texture.
func TestAWindowAloneMakesNoTextureAcrossResizes(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	d.frame()
	st := d.canvas.Get("style")
	oldW, oldH := st.Get("width").String(), st.Get("height").String()
	t.Cleanup(func() {
		st.Set("width", oldW)
		st.Set("height", oldH)
		d.observedSize()
	})
	// The texture calls of the size of the canvas.
	made := js.Global().Call("eval", `ctx => {
		const count = {n: 0}, make = ctx.texImage2D;
		ctx.texImage2D = function (target, level, format, w, h) {
			if (w === ctx.canvas.width && h === ctx.canvas.height) count.n++;
			return make.apply(this, arguments);
		};
		return count;
	}`).Invoke(d.canvas.Call("getContext", "webgl2"))
	t.Cleanup(func() {
		js.Global().Get("Reflect").Call("deleteProperty", d.canvas.Call("getContext", "webgl2"), "texImage2D")
	})
	for _, sz := range [][2]string{{"330px", "250px"}, {"340px", "260px"}, {"350px", "270px"}} {
		st.Set("width", sz[0])
		st.Set("height", sz[1])
		d.observedSize()
		made.Set("n", 0)
		present(t, w, testRed)
		d.frame()
		if w.pane.OnTex {
			t.Fatal("alone, it drew on its texture")
		}
		// A whole frame draws straight onto the canvas, with no texture of its size.
		if n := made.Get("n").Int(); n != 0 {
			t.Errorf("alone, at %v, %d textures took the canvas's size", sz, n)
		}
	}
	p := testPopup(t, d, w)
	defer func() { _ = p.Close(); d.frame() }()
	present(t, p, testBlue)
	d.frame()
	s := float64(d.scale)
	if got := pixelAt(d, int(30*s), int(30*s)); got[2] < 240 {
		t.Errorf("the popup shows %v", got)
	}
	if got := pixelAt(d, 4, 4); got[0] < 240 {
		t.Errorf("the window under it shows %v", got)
	}
	noGLError(t, d)
}
