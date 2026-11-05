//go:build js && wasm

package web

import (
	"image"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
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

// brokenGL refuses to compile shaders, as a context that has run out of memory
// might, so no renderer can be made. It lets the compositor's own compile first.
type brokenGL struct {
	gl.Context
	broken bool
}

func (g *brokenGL) GetShaderi(shader, name uint32) int {
	if g.broken {
		return gl.FALSE
	}
	return g.Context.GetShaderi(shader, name)
}

// A frame that could not draw keeps its shot for one that can, on the shot's
// own window and past any number of tries.
func TestAShotOutlivesARendererThatCantBeMade(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	d.frame()
	broken := &brokenGL{Context: d.g}
	comp, err := compose.New(broken, "test")
	if err != nil {
		t.Fatal(err)
	}
	broken.broken = true
	was := d.comp
	d.comp = comp
	p := testPopup(t, d, w)
	got := make(chan *image.RGBA, 1)
	defer func() { d.comp = was; _ = p.Close(); d.frame() }()
	p.Shoot(func(m *image.RGBA) { got <- m })
	for range 3 {
		present(t, p, testBlue)
		d.frame()
		select {
		case <-got:
			t.Fatal("a window that could not draw gave a shot")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if p.shot == nil {
		t.Fatal("the frame took the shot with it")
	}
	d.comp = was
	present(t, p, testBlue)
	d.frame()
	select {
	case m := <-got:
		if m.RGBAAt(4, 4).B < 240 {
			t.Fatalf("the shot shows %v", m.RGBAAt(4, 4))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the shot never came")
	}
	if p.shot != nil {
		t.Fatal("the shot is still waiting")
	}
}

// A newer shot asked for while the frame failed stays the one that waits.
func TestAShotAskedForMeanwhileStays(t *testing.T) {
	_, w := testWindow(t)
	older, newer := 0, 0
	w.Shoot(func(*image.RGBA) { newer++ })
	w.keepShot(func(*image.RGBA) { older++ })
	w.shot(nil)
	if older != 0 || newer != 1 {
		t.Fatalf("the older ran %d times, the newer %d", older, newer)
	}
}

// A spare popup shown again has its last frame from the popup before. That frame
// stays out of sight, however the frames fall, until its own arrives.
func TestAReusedPopupNeverShowsThePopupBefore(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	p := testPopup(t, d, w)
	defer func() { _ = p.Close(); d.frame() }()
	present(t, p, testBlue)
	d.frame()
	s := float64(d.scale)
	x, y := int(30*s), int(30*s)
	if got := pixelAt(d, x, y); got[2] < 240 {
		t.Fatalf("the popup shows %v", got)
	}
	_ = p.Hide()
	d.frame()
	if got := pixelAt(d, x, y); got[0] < 240 {
		t.Fatalf("hidden, it shows %v", got)
	}
	// Shown, with the frames running before its own comes.
	_ = p.Show()
	for i := range 3 {
		if i > 0 {
			present(t, w, testRed)
		}
		d.frame()
		if got := pixelAt(d, x, y); got[0] < 240 {
			t.Fatalf("frame %d after Show shows %v, the popup before", i, got)
		}
	}
	present(t, p, testGreen)
	d.frame()
	if got := pixelAt(d, x, y); got[1] < 240 {
		t.Fatalf("with its own frame it shows %v", got)
	}
	// A frame sent while hidden is its own, and shows with Show.
	_ = p.Hide()
	d.frame()
	present(t, p, testBlue)
	d.frame()
	_ = p.Show()
	d.frame()
	if got := pixelAt(d, x, y); got[2] < 240 {
		t.Fatalf("with a frame sent while hidden it shows %v", got)
	}
	noGLError(t, d)
}

// A popup told a new size waits for its frame at that size: the frames between
// lay its last frame where it was, with no draw of that frame at the new size.
func TestAPopupChangingSizeDrawsOnce(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	p := testPopup(t, d, w)
	defer func() { _ = p.Close(); d.frame() }()
	present(t, p, testBlue)
	d.frame()
	s := float64(d.scale)
	wasW := int(40 * s)
	anchor := geom.Rect{Min: geom.Pt(20, 20), Max: geom.Pt(20, 20)}
	if err := p.Place(anchor, geom.Sz(80, 60)); err != nil {
		t.Fatal(err)
	}
	if got := p.Size(); got != geom.Sz(80, 60) {
		t.Fatalf("the window says its size is %v", got)
	}
	// Another window's frame comes first, as it does on Android's render thread.
	present(t, w, testRed)
	d.frame()
	if p.pane.LastW != wasW {
		t.Fatalf("with no frame at its new size, it drew at %d wide, was %d", p.pane.LastW, wasW)
	}
	// The frame still shows where it did, and only there.
	if got := pixelAt(d, int(30*s), int(30*s)); got[2] < 240 {
		t.Fatalf("in the old size it shows %v", got)
	}
	if got := pixelAt(d, int(80*s), int(30*s)); got[0] < 240 {
		t.Fatalf("past the old size it shows %v", got)
	}
	present(t, p, testGreen)
	d.frame()
	if p.pane.LastW != int(80*s) {
		t.Fatalf("with its frame it drew at %d wide, want %d", p.pane.LastW, int(80*s))
	}
	if got := pixelAt(d, int(80*s), int(30*s)); got[1] < 240 {
		t.Fatalf("at its new size it shows %v", got)
	}
	noGLError(t, d)
}

// A move at the same size needs no new frame, and shows at once.
func TestAPopupMovingShowsAtOnce(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	p := testPopup(t, d, w)
	defer func() { _ = p.Close(); d.frame() }()
	present(t, p, testBlue)
	d.frame()
	s := float64(d.scale)
	if err := p.Place(geom.Rect{Min: geom.Pt(100, 100), Max: geom.Pt(100, 100)}, geom.Sz(40, 40)); err != nil {
		t.Fatal(err)
	}
	d.frame()
	if got := pixelAt(d, int(110*s), int(110*s)); got[2] < 240 {
		t.Fatalf("at its new place it shows %v", got)
	}
	if got := pixelAt(d, int(30*s), int(30*s)); got[0] < 240 {
		t.Fatalf("at its old place it shows %v", got)
	}
}

// setCanvasSize gives the driver's canvas a CSS size for the test, and puts it
// back after.
func setCanvasSize(t *testing.T, d *Driver, w, h string) {
	t.Helper()
	st := d.canvas.Get("style")
	oldW, oldH := st.Get("width").String(), st.Get("height").String()
	t.Cleanup(func() {
		st.Set("width", oldW)
		st.Set("height", oldH)
		d.observedSize()
	})
	st.Set("width", w)
	st.Set("height", h)
	d.observedSize()
}

// keepLimit puts the driver's limit and what the log was told back after a test.
func keepLimit(t *testing.T, d *Driver) {
	t.Helper()
	limit, said := d.bufferLimit, d.limitSaid
	t.Cleanup(func() {
		d.bufferLimit, d.limitSaid = limit, said
		d.observedSize()
	})
}

// A canvas far past any GPU's limit, here on the real limit of this one, draws
// whole, at a lower scale.
func TestACanvasPastTheGPUsLimitStillDraws(t *testing.T) {
	d, w := testWindow(t)
	keepLimit(t, d)
	limit := gpuLimit(d.ctx)
	wantW := float32(limit) * 3 / pageRatio()
	setCanvasSize(t, d, strconv.Itoa(int(wantW))+"px", "300px")
	if d.surfW != limit {
		t.Fatalf("the canvas is %v wide at scale %v, its buffer %d, on a limit of %d", wantW, d.scale, d.surfW, limit)
	}
	if bw := d.ctx.Get("drawingBufferWidth").Int(); bw != d.surfW || d.canvas.Get("width").Int() != bw {
		t.Fatalf("the drawing buffer is %d, the canvas %d, the driver thinks %d", bw, d.canvas.Get("width").Int(), d.surfW)
	}
	if got := w.Size(); got.W != float32(int(wantW)) {
		t.Fatalf("the window's logical size is %v", got)
	}
	if got := w.Scale(); got >= pageRatio() || got <= 0 {
		t.Fatalf("the window's scale is %v, the page's %v", got, pageRatio())
	}
	present(t, w, testRed)
	d.frame()
	noGLError(t, d)
	for _, at := range [][2]int{{4, 4}, {d.surfW - 4, 4}, {4, d.surfH - 4}, {d.surfW - 4, d.surfH - 4}} {
		if got := pixelAt(d, at[0], at[1]); got[0] < 240 {
			t.Errorf("at %v it shows %v", at, got)
		}
	}
	// Under a popup too, which goes through textures of those sizes.
	p := testPopup(t, d, w)
	defer func() { _ = p.Close(); d.frame() }()
	present(t, p, testBlue)
	d.frame()
	noGLError(t, d)
	s := float64(d.scale)
	if got := pixelAt(d, int(30*s), int(30*s)); got[2] < 240 {
		t.Errorf("the popup shows %v at scale %v", got, s)
	}
	if got := pixelAt(d, d.surfW-4, d.surfH-4); got[0] < 240 {
		t.Errorf("the far corner shows %v under a popup", got)
	}
}

// A browser can give less than it was asked for, even under the GPU's limit, and
// says so only in drawingBufferWidth and drawingBufferHeight.
func TestABufferSmallerThanAskedForStillDraws(t *testing.T) {
	d, w := testWindow(t)
	keepLimit(t, d)
	js.Global().Call("eval", `ctx => {
		Object.defineProperty(ctx, "drawingBufferWidth", {configurable: true, get() { return Math.min(ctx.canvas.width, 400); }});
		Object.defineProperty(ctx, "drawingBufferHeight", {configurable: true, get() { return Math.min(ctx.canvas.height, 400); }});
	}`).Invoke(d.ctx)
	t.Cleanup(func() {
		r := js.Global().Get("Reflect")
		r.Call("deleteProperty", d.ctx, "drawingBufferWidth")
		r.Call("deleteProperty", d.ctx, "drawingBufferHeight")
	})
	cssW := 1000 / pageRatio()
	setCanvasSize(t, d, strconv.Itoa(int(cssW))+"px", strconv.Itoa(int(cssW/2))+"px")
	if d.surfW != 400 || d.surfH > 400 || d.canvas.Get("width").Int() != 400 {
		t.Fatalf("the buffer is %dx%d, the canvas %d wide, on 400 given", d.surfW, d.surfH, d.canvas.Get("width").Int())
	}
	if got, want := d.scale, float32(400)/float32(int(cssW)); got != want {
		t.Fatalf("the scale is %v, want %v", got, want)
	}
	// The next look at the same size asks for no big buffer again, which would
	// clear what shows.
	sets := js.Global().Call("eval", `c => {
		const count = {n: 0};
		const size = Object.getOwnPropertyDescriptor(HTMLCanvasElement.prototype, "width");
		Object.defineProperty(c, "width", {configurable: true, get() { return size.get.call(c); }, set(v) { count.n++; size.set.call(c, v); }});
		return count;
	}`).Invoke(d.canvas)
	t.Cleanup(func() { js.Global().Get("Reflect").Call("deleteProperty", d.canvas, "width") })
	d.measure()
	if n := sets.Get("n").Int(); n != 0 {
		t.Fatalf("the next look set the buffer's size %d times", n)
	}
	present(t, w, testRed)
	d.frame()
	if got := pixelAt(d, d.surfW-4, d.surfH-4); got[0] < 240 {
		t.Fatalf("the far corner shows %v", got)
	}
}

// pageRatio is the page's device pixel ratio.
func pageRatio() float32 { return float32(js.Global().Get("devicePixelRatio").Float()) }

func TestFitLimit(t *testing.T) {
	for _, c := range []struct {
		w, h, scale float32
		pw, ph      int
		limit       int
		lw, lh      int
		ls          float32
	}{
		{100, 200, 2, 200, 400, 0, 200, 400, 2},
		{100, 200, 2, 200, 400, 400, 200, 400, 2},
		{100, 200, 2, 200, 400, 100, 50, 100, 0.5},
		// The README's page on a phone, with no viewport tag.
		{980, 2002.3, 2.625, 2572, 5256, 4096, 2005, 4096, 2.0456},
		{4000, 10, 1, 4000, 10, 1000, 1000, 3, 0.25},
	} {
		lw, lh, ls := fitLimit(c.w, c.h, c.scale, c.pw, c.ph, c.limit)
		if lw != c.lw || lh != c.lh || math.Abs(float64(ls-c.ls)) > 0.001 {
			t.Errorf("%+v: got %dx%d at %v", c, lw, lh, ls)
		}
	}
}

// The canvas the driver makes for a page fills it, and the page has nothing to
// scroll, whatever margins it would have had.
func TestAMadeCanvasFillsThePage(t *testing.T) {
	doc := js.Global().Get("document")
	frame := doc.Call("createElement", "iframe")
	frame.Get("style").Set("cssText", "width:300px;height:200px;border:0")
	doc.Get("body").Call("appendChild", frame)
	defer frame.Call("remove")
	inner := frame.Get("contentDocument")
	c := makeCanvas(inner)
	if c.Get("id").String() != "gunim" || !inner.Call("getElementById", "gunim").Equal(c) {
		t.Fatal("the page does not hold the canvas")
	}
	if got := c.Call("getBoundingClientRect"); got.Get("left").Int() != 0 || got.Get("top").Int() != 0 ||
		got.Get("width").Int() != 300 || got.Get("height").Int() != 200 {
		t.Errorf("the canvas lies at %v, %v, %v by %v", got.Get("left"), got.Get("top"), got.Get("width"), got.Get("height"))
	}
	root := inner.Get("documentElement")
	if sw, cw := root.Get("scrollWidth").Int(), root.Get("clientWidth").Int(); sw > cw {
		t.Errorf("the page scrolls sideways: %d in %d", sw, cw)
	}
	if sh, ch := root.Get("scrollHeight").Int(), root.Get("clientHeight").Int(); sh > ch {
		t.Errorf("the page scrolls: %d in %d", sh, ch)
	}
	want := "100vh"
	if js.Global().Get("CSS").Call("supports", "height", "100dvh").Bool() {
		want = "100dvh"
	}
	if got := c.Get("style").Get("height").String(); got != want {
		t.Errorf("the canvas's height is %q, want %q", got, want)
	}
}

// A refresh rate reads the same at any speed of panel, and through missed frames.
func TestTheRefreshEstimate(t *testing.T) {
	estimate := func(hz float64, frames int, gapAt func(i int) float64) float64 {
		x := &Driver{rate: 60}
		now := 1000.0
		for i := range frames {
			now += gapAt(i) * 1000 / hz
			x.observeRefresh(now)
			// The first frames have a second callback with the same timestamp.
			x.observeRefresh(now)
		}
		return x.rate
	}
	even := func(int) float64 { return 1 }
	for _, c := range []struct {
		name string
		hz   float64
		gaps func(int) float64
		want float64
	}{
		{"60 Hz", 60, even, 60},
		{"120 Hz", 120, even, 120},
		{"240 Hz", 240, even, 240},
		{"300 Hz", 300, even, 300},
		{"360 Hz", 360, even, 360},
		{"500 Hz", 500, even, 500},
		{"one frame in two misses at 60 Hz", 60, func(i int) float64 { return float64(1 + i%2) }, 60},
		{"two frames in three miss at 120 Hz", 120, func(i int) float64 { return float64(1 + (i%3+1)/2) }, 120},
		{"four frames in five miss at 60 Hz", 60, func(i int) float64 {
			if i%5 == 0 {
				return 1
			}
			return 2
		}, 60},
		{"a wrong short gap in twenty at 60 Hz", 60, func(i int) float64 {
			if i%20 == 0 {
				return 0.5
			}
			return 1
		}, 60},
		{"the page rests between some frames", 60, func(i int) float64 {
			if i%3 == 0 {
				return 100
			}
			return 1
		}, 60},
	} {
		if got := estimate(c.hz, 100, c.gaps); math.Abs(got-c.want) > 0.01 {
			t.Errorf("%s: read %v Hz", c.name, got)
		}
	}
	// A move to a slower panel shows once the last one's gaps have gone by.
	x := &Driver{rate: 60}
	now := 1000.0
	for range 100 {
		now += 1000.0 / 120
		x.observeRefresh(now)
	}
	for range 100 {
		now += 1000.0 / 60
		x.observeRefresh(now)
	}
	if math.Abs(x.rate-60) > 0.01 {
		t.Errorf("after moving from 120 Hz to 60 Hz it reads %v", x.rate)
	}
	// With too few gaps to go by, the rate stays as it was.
	if got := estimate(120, 5, even); got != 60 {
		t.Errorf("after 5 frames it reads %v", got)
	}
}

// A frame of the estimate allocates nothing, as it runs on every animation frame.
func TestTheRefreshEstimateAllocatesNothing(t *testing.T) {
	x := &Driver{rate: 60}
	now := 1000.0
	if n := testing.AllocsPerRun(100, func() {
		now += 16.7
		x.observeRefresh(now)
	}); n != 0 {
		t.Fatalf("%v allocations a frame", n)
	}
}

// DevTools' emulation changes the page's scale with no event. An animation frame
// finds it.
func TestAScaleChangeWithNoEventIsFoundByAFrame(t *testing.T) {
	d, w := testWindow(t)
	f := fakePageScale(t, d, 1)
	present(t, w, testRed)
	d.resized()
	d.frame()
	d.checkScale()
	if got := f.lastQuery(); got != "(resolution: 1dppx)" {
		t.Fatalf("listening for %q with no change", got)
	}
	f.setQuietly(2)
	d.checkScale()
	if got := w.Scale(); got != 2 {
		t.Fatalf("the window's scale is %v", got)
	}
	if got := f.lastQuery(); got != "(resolution: 2dppx)" {
		t.Fatalf("listening for %q", got)
	}
}

// The first click focuses the canvas from code, which has the browser draw its
// focus ring round all of it, unless told not to.
func TestTheCanvasHasNoFocusRing(t *testing.T) {
	d, _ := testWindow(t)
	d.canvas.Call("focus", map[string]any{"preventScroll": true})
	if !d.canvas.Equal(js.Global().Get("document").Get("activeElement")) {
		t.Fatal("the canvas took no focus")
	}
	if !d.canvas.Call("matches", ":focus-visible").Bool() {
		t.Log("the browser does not count this focus as visible")
	}
	style := js.Global().Call("getComputedStyle", d.canvas)
	if got := style.Get("outlineStyle").String(); got != "none" {
		t.Errorf("focused, the canvas has an outline of %s, %s", got, style.Get("outlineWidth"))
	}
}

// A page's own touch-action stays, from its style attribute or a stylesheet.
func TestAPagesOwnTouchActionStays(t *testing.T) {
	doc := js.Global().Get("document")
	sheet := doc.Call("createElement", "style")
	sheet.Set("textContent", ".swipes { touch-action: pan-y; }")
	doc.Get("head").Call("appendChild", sheet)
	defer sheet.Call("remove")
	for _, c := range []struct {
		name, class, inline, want string
	}{
		{"nothing set", "", "", "none"},
		{"a stylesheet", "swipes", "", "pan-y"},
		{"the style attribute", "", "pan-x", "pan-x"},
		{"both, where the attribute wins", "swipes", "pan-x", "pan-x"},
		{"auto in the style attribute", "", "auto", "auto"},
	} {
		el := doc.Call("createElement", "canvas")
		el.Set("className", c.class)
		el.Get("style").Set("touchAction", c.inline)
		doc.Get("body").Call("appendChild", el)
		if got := pageTouchAction(el); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		el.Call("remove")
	}
}

// fakeScale stands in for the browser's page scale and its media queries, as a
// test can't move the page to another screen.
type fakeScale struct {
	state js.Value
}

func fakePageScale(t *testing.T, d *Driver, was float64) *fakeScale {
	t.Helper()
	state := js.Global().Call("eval", `was => {
		const state = {queries: [], live: null};
		state.put = {};
		for (const k of ["matchMedia", "devicePixelRatio"]) state.put[k] = Object.getOwnPropertyDescriptor(window, k);
		window.matchMedia = q => {
			const t = new EventTarget();
			state.queries.push(q);
			state.live = t;
			return t;
		};
		Object.defineProperty(window, "devicePixelRatio", {configurable: true, writable: true, value: was});
		return state;
	}`).Invoke(was)
	d.armScale()
	t.Cleanup(func() {
		o := js.Global().Get("Object")
		o.Call("defineProperty", js.Global(), "matchMedia", state.Get("put").Get("matchMedia"))
		o.Call("defineProperty", js.Global(), "devicePixelRatio", state.Get("put").Get("devicePixelRatio"))
		d.armScale()
		d.resized()
	})
	return &fakeScale{state}
}

// set changes the page's scale, and tells the query listening last.
func (f *fakeScale) set(to float64) {
	js.Global().Set("devicePixelRatio", to)
	f.state.Get("live").Call("dispatchEvent", js.Global().Get("Event").New("change"))
}

// setQuietly changes the page's scale with no event, as DevTools' emulation does.
func (f *fakeScale) setQuietly(to float64) { js.Global().Set("devicePixelRatio", to) }

func (f *fakeScale) lastQuery() string {
	q := f.state.Get("queries")
	return q.Index(q.Length() - 1).String()
}

// A change of the page's scale with no resize reaches the windows, popups too.
func TestAScaleChangeWithNoResizeIsSeen(t *testing.T) {
	d, w := testWindow(t)
	f := fakePageScale(t, d, 1)
	present(t, w, testRed)
	p := testPopup(t, d, w)
	defer func() { _ = p.Close(); d.frame() }()
	present(t, p, testBlue)
	d.resized()
	d.frame()
	cssW, cssH := d.cssW, d.cssH
	if got := f.lastQuery(); got != "(resolution: 1dppx)" {
		t.Fatalf("listening for %q", got)
	}
	for _, to := range []float64{2, 1.25, 3} {
		drain(w)
		drain(p)
		f.set(to)
		if got := w.Scale(); got != float32(to) {
			t.Fatalf("the window's scale is %v, want %v", got, to)
		}
		if d.cssW != cssW || d.cssH != cssH {
			t.Fatalf("the logical size went from %vx%v to %vx%v", cssW, cssH, d.cssW, d.cssH)
		}
		want := int(math.Round(float64(cssW) * to))
		if d.surfW != want || d.canvas.Get("width").Int() != want {
			t.Fatalf("at scale %v the buffer is %d wide, the driver thinks %d, want %d", to, d.canvas.Get("width").Int(), d.surfW, want)
		}
		if got, want := f.lastQuery(), "(resolution: "+strconv.FormatFloat(to, 'f', -1, 64)+"dppx)"; got != want {
			t.Fatalf("after a change, listening for %q, want %q", got, want)
		}
		// The window's last frame draws at once, and fills the buffer.
		d.frame()
		if got := pixelAt(d, d.surfW-4, d.surfH-4); got[0] < 240 {
			t.Errorf("at scale %v the far corner shows %v", to, got)
		}
		s := float64(d.scale)
		if got := pixelAt(d, int(30*s), int(30*s)); got[2] < 240 {
			t.Errorf("at scale %v the popup shows %v", to, got)
		}
		noGLError(t, d)
		for name, x := range map[string]*Window{"window": w, "popup": p} {
			if !slices.Contains(drain(x), any(driver.Redraw{})) {
				t.Errorf("the %s heard no Redraw at scale %v", name, to)
			}
		}
	}
}

// resizeStep calls fn in the browser's next resize step of the canvas, after the
// driver has heard of the size and before the browser shows the canvas, each time
// the canvas's size changes. The function it returns waits for the next one.
func resizeStep(t *testing.T, d *Driver, fn func()) (next func()) {
	t.Helper()
	seen := make(chan struct{}, 8)
	f := js.FuncOf(func(js.Value, []js.Value) any {
		fn()
		seen <- struct{}{}
		return nil
	})
	ro := js.Global().Get("ResizeObserver").New(f)
	t.Cleanup(func() {
		ro.Call("disconnect")
		f.Release()
	})
	ro.Call("observe", d.canvas)
	return func() {
		t.Helper()
		select {
		case <-seen:
		case <-time.After(10 * time.Second):
			t.Fatal("no resize step came")
		}
	}
}

// loopErrors counts what the browser reports of a ResizeObserver loop.
func loopErrors(t *testing.T) func() int {
	t.Helper()
	count := js.Global().Call("eval", `(() => {
		const count = {n: 0, listen: e => { if (String(e.message).includes("ResizeObserver")) count.n++; }};
		addEventListener("error", count.listen);
		return count;
	})()`)
	t.Cleanup(func() { js.Global().Call("removeEventListener", "error", count.Get("listen")) })
	return func() int { return count.Get("n").Int() }
}

// canvasStyle sets the driver's canvas's style for the test, and puts all of it
// back after.
func canvasStyle(t *testing.T, d *Driver, style map[string]string) {
	t.Helper()
	st := d.canvas.Get("style")
	was := st.Get("cssText").String()
	attrW, attrH := d.canvas.Get("width").Int(), d.canvas.Get("height").Int()
	t.Cleanup(func() {
		st.Set("cssText", was)
		d.canvas.Set("width", attrW)
		d.canvas.Set("height", attrH)
		d.mu.Lock()
		d.surfW, d.surfH = attrW, attrH
		d.mu.Unlock()
		d.observedSize()
	})
	for k, v := range style {
		st.Set(k, v)
	}
}

// A canvas hidden at first has no frame to show as it first shows. What shows
// then is the window's background, where a canvas with nothing drawn is black.
func TestACanvasHiddenAtFirstShowsNoBlack(t *testing.T) {
	d, _ := Open()
	canvasStyle(t, d, map[string]string{"display": "none"})
	for give := time.Now().Add(5 * time.Second); d.surfW != 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(give) {
			t.Fatal("the driver never heard the canvas was hidden")
		}
	}
	_, w := testWindow(t)
	w.SetBackground(testGreen)
	// The engine presents at no size while the canvas is hidden.
	present(t, w, testRed)
	d.frame()
	var first [4]byte
	var drew bool
	next := resizeStep(t, d, func() {
		if !drew && d.surfW > 0 {
			drew = true
			first = pixelAt(d, d.surfW/2, d.surfH/2)
		}
	})
	next() // Every observer hears once as it starts.
	// Out of the browser's resize step, as a size set inside it is what it reports.
	time.Sleep(50 * time.Millisecond)
	d.canvas.Get("style").Set("display", "block")
	next()
	if !drew {
		t.Fatal("the resize step saw no size")
	}
	if first[1] < 240 || first[0] > 10 {
		t.Errorf("the first frame shown is %v, want the window's background", first)
	}
	if d.g.GetError() != 0 {
		t.Error("WebGL error")
	}
	// The engine's own frame comes next, over it.
	present(t, w, testRed)
	d.frame()
	if got := pixelAt(d, 4, 4); got[0] < 240 {
		t.Errorf("the engine's frame shows %v", got)
	}
}

// A canvas with height:auto follows its width, whatever the buffer's size, with
// nothing pinned, shown from the start or hidden first.
func TestAutoHeightFollowsTheWidth(t *testing.T) {
	doc := js.Global().Get("document")
	for _, hidden := range []bool{false, true} {
		box := doc.Call("createElement", "div")
		box.Get("style").Set("width", "400px")
		doc.Get("body").Call("appendChild", box)
		t.Cleanup(func() { box.Call("remove") })
		c := doc.Call("createElement", "canvas")
		c.Set("width", 400)
		c.Set("height", 100)
		c.Get("style").Set("cssText", "display:block;width:100%;height:auto")
		box.Call("appendChild", c)
		x := &Driver{canvas: c}
		if hidden {
			c.Get("style").Set("display", "none")
			x.measure()
			c.Get("style").Set("display", "block")
		}
		x.measure()
		if got := c.Get("style").Get("height").String(); got != "auto" {
			t.Errorf("hidden %v: the height became %q", hidden, got)
		}
		if got := c.Get("style").Get("width").String(); got != "100%" {
			t.Errorf("hidden %v: the width became %q", hidden, got)
		}
		if got := c.Get("clientHeight").Int(); got != 100 {
			t.Errorf("hidden %v: it is %d tall at 400 wide", hidden, got)
		}
		box.Get("style").Set("width", "200px")
		x.measure()
		if got := c.Get("clientHeight").Int(); got != 50 {
			t.Errorf("hidden %v: it is %d tall at 200 wide", hidden, got)
		}
		// The scale above 1 and the rounding of the buffer leave the ratio be.
		box.Get("style").Set("width", "333px")
		for range 5 {
			x.measure()
		}
		if got := c.Get("clientHeight").Int(); got != 83 {
			t.Errorf("hidden %v: it is %d tall at 333 wide", hidden, got)
		}
		// With no rule of its own for the width either, a page's aspect-ratio stands.
		x2 := &Driver{canvas: c}
		c.Get("style").Set("aspectRatio", "2 / 1")
		x2.measure()
		if got := c.Get("style").Get("aspectRatio").String(); !strings.HasPrefix(got, "2") {
			t.Errorf("hidden %v: a page's aspect ratio became %q", hidden, got)
		}
	}
}

// The canvas settling into its size on the first look leaves nothing for the
// browser to report as a ResizeObserver loop. Each case is a page's new canvas,
// hidden at first, heard as the driver hears its own.
func TestACanvasShownLeavesNoResizeObserverLoop(t *testing.T) {
	doc := js.Global().Get("document")
	// What the test before left settles first.
	time.Sleep(200 * time.Millisecond)
	errs := loopErrors(t)
	for _, c := range []struct {
		name, css string
	}{
		{"no size in CSS", ""},
		{"a percent width and auto height", "width:33.3%;height:auto"},
		{"auto width and a height", "width:auto;height:121px"},
		{"both sides given", "width:50.5%;height:120.5px"},
		{"auto both ways with a max width", "max-width:50.5%;height:auto"},
	} {
		t.Run(c.name, func(t *testing.T) {
			el := doc.Call("createElement", "canvas")
			el.Get("style").Set("cssText", "display:none;"+c.css)
			doc.Get("body").Call("appendChild", el)
			defer el.Call("remove")
			x := &Driver{canvas: el}
			x.measure()
			seen := make(chan struct{}, 8)
			f := js.FuncOf(func(_ js.Value, args []js.Value) any {
				x.readExactSize(args[0])
				x.measure()
				seen <- struct{}{}
				return nil
			})
			defer f.Release()
			ro := js.Global().Get("ResizeObserver").New(f)
			defer ro.Call("disconnect")
			ro.Call("observe", el)
			// A page's own observer hears after the driver's.
			pages := js.FuncOf(func(js.Value, []js.Value) any { return nil })
			defer pages.Release()
			own := js.Global().Get("ResizeObserver").New(pages)
			defer own.Call("disconnect")
			own.Call("observe", el)
			time.Sleep(50 * time.Millisecond)
			el.Get("style").Set("display", "block")
			select {
			case <-seen:
			case <-time.After(10 * time.Second):
				t.Fatal("no resize step came")
			}
			// Some more animation frames, for the report to come.
			time.Sleep(200 * time.Millisecond)
			if n := errs(); n != 0 {
				t.Errorf("%d reports of a ResizeObserver loop", n)
			}
			if el.Get("clientWidth").Int() == 0 || el.Get("clientHeight").Int() == 0 {
				t.Errorf("it shows at %vx%v", el.Get("clientWidth"), el.Get("clientHeight"))
			}
		})
	}
}

// A context lost for good would leave the bare page, with no word why. A context
// that comes back after that takes the note away, and draws again.
func TestALostContextSaysSo(t *testing.T) {
	d, w := testWindow(t)
	present(t, w, testRed)
	d.frame()
	was := contextWait
	defer func() { contextWait = was }()
	doc := js.Global().Get("document")
	ext := d.ctx.Call("getExtension", "WEBGL_lose_context")
	if ext.IsNull() {
		t.Fatal("no way to lose the context here")
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for give := time.Now().Add(5 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(give) {
				t.Fatalf("%s never came", what)
			}
		}
	}
	lost := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.lost
	}
	note := func() js.Value { return doc.Call("getElementById", "gunim-context-lost") }
	attr := func() string { return d.canvas.Call("getAttribute", "data-gunim-context").String() }

	// Back soon, the user hears nothing.
	contextWait = 300 * time.Millisecond
	ext.Call("loseContext")
	waitFor("the loss", lost)
	time.Sleep(20 * time.Millisecond)
	ext.Call("restoreContext")
	waitFor("the context's return", func() bool { return !lost() })
	time.Sleep(400 * time.Millisecond)
	if !note().IsNull() {
		t.Fatal("a context back in time left a note")
	}
	if got := attr(); got != "restored" {
		t.Fatalf("the attribute is %q", got)
	}

	// Gone too long, the user does.
	contextWait = 20 * time.Millisecond
	ext.Call("loseContext")
	waitFor("the loss", lost)
	waitFor("the note", func() bool { return !note().IsNull() })
	if got := note().Get("textContent").String(); !strings.Contains(got, "Reload the page") {
		t.Errorf("the note says %q", got)
	}
	if note().Call("querySelector", "button").IsNull() {
		t.Error("the note has no button")
	}
	if got := js.Global().Call("getComputedStyle", note()).Get("position").String(); got != "fixed" {
		t.Errorf("the note is %s", got)
	}
	if got := attr(); got != "gone" {
		t.Errorf("the attribute is %q", got)
	}
	// The frames meanwhile wait.
	present(t, w, testBlue)
	d.frame()
	ext.Call("restoreContext")
	waitFor("the context's return", func() bool { return !lost() })
	if !note().IsNull() {
		t.Error("the note stayed after the context came back")
	}
	if got := attr(); got != "restored" {
		t.Errorf("the attribute is %q", got)
	}
	present(t, w, testGreen)
	d.frame()
	if got := pixelAt(d, 4, 4); got[1] < 240 {
		t.Errorf("after coming back it shows %v", got)
	}
	noGLError(t, d)

	// And once more, the same note serves.
	ext.Call("loseContext")
	waitFor("the loss", lost)
	waitFor("the note", func() bool { return !note().IsNull() })
	ext.Call("restoreContext")
	waitFor("the context's return", func() bool { return !lost() })
	if n := doc.Call("querySelectorAll", "#gunim-context-lost").Length(); n != 0 {
		t.Errorf("%d notes stayed", n)
	}
}
