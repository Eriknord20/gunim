//go:build js && wasm

package web

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

func TestUnicodeSelections(t *testing.T) {
	s := "a😀é日"
	for _, n := range []int{0, 1, 5, 7, 10} {
		if got := byteOffset(s, utf16Offset(s, n)); got != n {
			t.Fatalf("roundtrip %d => %d", n, got)
		}
	}
}
func TestBrowser(t *testing.T) {
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	win, err := d.NewWindow(driver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := win.(*Window)
	t.Run("symbols", func(t *testing.T) {
		for _, g := range text.Default().Shape("▶◀⏪⏩⚙", 16).Glyphs {
			if g.ID == 0 {
				t.Fatal("missing symbol")
			}
		}
	})
	t.Run("text mirror composition prediction and backspace", func(t *testing.T) {
		w.SetTextState(&input.TextState{Text: "ab😀", Selection: [2]int{6, 6}}, 0)
		w.SetTextInput(true)
		w.ShowKeyboard()
		if got := d.text.Get("selectionStart").Int(); got != 4 {
			t.Fatalf("UTF16 caret=%d", got)
		}
		fire := func(name string, opts map[string]any) {
			e := js.Global().Get("Event").New(name, map[string]any{"bubbles": true})
			for k, v := range opts {
				e.Set(k, v)
			}
			d.text.Call("dispatchEvent", e)
		}
		fire("compositionstart", nil)
		d.text.Set("value", "ab😀日")
		d.text.Call("setSelectionRange", 5, 5)
		fire("input", map[string]any{"isComposing": true})
		if w.state.Text != "ab😀日" || w.state.Composing != [2]int{6, 9} {
			t.Fatalf("composition %+v", w.state)
		}
		w.SetTextState(&input.TextState{Text: "stale"}, 0)
		if w.state.Text == "stale" {
			t.Fatal("stale acknowledgement replaced live edit")
		}
		fire("compositionend", nil)
		if w.state.Composing[0] != w.state.Composing[1] {
			t.Fatal("composition did not end")
		}
		d.text.Set("value", "ab😀日本")
		d.text.Call("setSelectionRange", 6, 6)
		fire("input", nil)
		d.text.Set("value", "ab😀日")
		d.text.Call("setSelectionRange", 5, 5)
		fire("input", nil)
		if w.state.Text != "ab😀日" || d.text.Get("value").String() != w.state.Text {
			t.Fatal("prediction/backspace lost text")
		}
		w.SetTextCaret(geom.Rect{Min: geom.Pt(12.5, 24)})
		if !strings.HasSuffix(d.text.Get("style").Get("left").String(), "px") {
			t.Fatal("invalid caret CSS")
		}
		w.SetTextInput(false)
	})
	t.Run("clipboard success and denied permission", func(t *testing.T) {
		nav := js.Global().Get("navigator")
		obj := js.Global().Get("Object")
		prior := obj.Call("getOwnPropertyDescriptor", nav, "clipboard")
		defer func() {
			if prior.IsUndefined() {
				js.Global().Get("Reflect").Call("deleteProperty", nav, "clipboard")
			} else {
				obj.Call("defineProperty", nav, "clipboard", prior)
			}
		}()
		mock := js.Global().Call("eval", `({readText:()=>Promise.resolve('héllo'),writeText:s=>Promise.resolve()})`)
		obj.Call("defineProperty", nav, "clipboard", map[string]any{"configurable": true, "value": mock})
		if got, err := w.Clipboard(); err != nil || got != "héllo" {
			t.Fatalf("read=%q err=%v", got, err)
		}
		if err := w.SetClipboard("copy"); err != nil {
			t.Fatal(err)
		}
		mock.Set("readText", js.Global().Call("eval", `(()=>Promise.reject(new Error('denied')))`))
		if _, err := w.Clipboard(); err == nil {
			t.Fatal("denied clipboard succeeded")
		}
	})
	t.Run("safe area and keyboard viewport", func(t *testing.T) {
		d.safeProbe.Get("style").Set("padding", "11px 12px 13px 14px")
		d.viewportChanged()
		if w.SafeArea() != (geom.Insets{Top: 11, Right: 12, Bottom: 13, Left: 14}) {
			t.Fatalf("safe area=%v", w.SafeArea())
		}
		d.safeProbe.Get("style").Set("padding", "0px")
		d.viewportChanged()
		if w.KeyboardCover() != 0 {
			t.Fatalf("keyboard cover while closed=%f", w.KeyboardCover())
		}
	})
	t.Run("refresh estimate", func(t *testing.T) {
		x := &Driver{}
		for i := range 24 {
			x.observeRefresh(float64(i) * 1000 / 120)
		}
		if math.Abs(x.rate-120) > .01 {
			t.Fatalf("rate=%f", x.rate)
		}
	})
	solid := func(c color.NRGBA, sz geom.Size) []paint.Op {
		return []paint.Op{&paint.RRectOp{Rect: geom.Rect{Max: geom.Pt(sz.W, sz.H)}, Fill: paint.Solid(c), Transform: paint.Identity}}
	}
	red := color.NRGBA{R: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	_ = w.Present(solid(red, w.Size()), []geom.Rect{paint.Everything})
	d.frame()
	pixel := func(x, y int) []byte {
		p := make([]byte, 4)
		d.g.BindFramebuffer(gl.FRAMEBUFFER, 0)
		d.g.ReadPixels(p, int32(x), int32(d.surfH-1-y), 1, 1, gl.RGBA, gl.UNSIGNED_BYTE)
		return p
	}
	t.Run("popup composition and texture feedback", func(t *testing.T) {
		pw, _ := d.NewWindow(driver.Options{Kind: driver.KindPopup, Parent: w, Size: geom.Sz(40, 40), Anchor: geom.Rect{Min: geom.Pt(20, 20), Max: geom.Pt(20, 20)}, Over: true})
		p := pw.(*Window)
		_ = p.Present(solid(blue, p.Size()), []geom.Rect{paint.Everything})
		d.frame()
		a, b := pixel(5, 5), pixel(int(30*d.scale), int(30*d.scale))
		if a[0] < 240 || b[2] < 240 {
			t.Fatalf("base=%v popup=%v", a, b)
		}
		_ = p.Hide()
		d.frame()
		if p := pixel(30, 30); p[0] < 240 {
			t.Fatalf("hide=%v", p)
		}
		_ = p.Show()
		d.frame()
		if p := pixel(int(30*d.scale), int(30*d.scale)); p[2] < 240 {
			t.Fatalf("show=%v", p)
		}
		_ = p.Close()
		d.frame()
		if e := d.canvas.Call("getContext", "webgl2").Call("getError").Int(); e != 0 {
			t.Fatalf("WebGL error=%x", e)
		}
	})
	t.Run("screenshot orientation", func(t *testing.T) {
		got := make(chan *image.RGBA, 1)
		w.Shoot(func(img *image.RGBA) { got <- img })
		// A shot is of the next frame the engine sends, so it waits for one.
		d.frame()
		select {
		case <-got:
			t.Fatal("the shot took the last frame, with no new one sent")
		case <-time.After(50 * time.Millisecond):
		}
		_ = w.Present(solid(red, w.Size()), []geom.Rect{paint.Everything})
		d.frame()
		select {
		case img := <-got:
			if img.RGBAAt(4, 4).R < 240 {
				t.Fatal("screenshot missing pixels")
			}
		case <-time.After(time.Second):
			t.Fatal("screenshot timeout")
		}
	})
	t.Run("deferred sharpening", func(t *testing.T) {
		for len(w.presented) > 0 {
			<-w.presented
		}
		// Hold back the browser's next animation frame, which would take the
		// sharpening away unseen, and on a slow machine can be seconds off.
		asked := func() bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return d.frameAsked
		}
		for give := time.Now().Add(5 * time.Second); asked(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(give) {
				t.Fatal("the last frame never came")
			}
		}
		d.mu.Lock()
		d.frameAsked = true
		d.mu.Unlock()
		defer func() {
			d.mu.Lock()
			d.frameAsked = false
			d.mu.Unlock()
		}()
		at := time.Now().Add(50 * time.Millisecond)
		d.sharpenAt(w, at)
		if w.sharpTimer == nil || !w.sharpAt.Equal(at) {
			t.Fatal("sharpen timer was not armed")
		}
		sharpen := func() bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return w.sharpen
		}
		for give := time.Now().Add(5 * time.Second); !sharpen(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(give) {
				t.Fatal("the sharpen timer never fired")
			}
		}
		// What the frame draws is the last frame again, where it drew stretched, so a
		// change to that shows it drew.
		was := w.pane.Last
		defer func() { w.pane.Last = was }()
		w.pane.Last = solid(blue, w.Size())
		w.pane.R.Stretched = geom.Rect{Max: w.Size().Point()}
		d.frame()
		if sharpen() {
			t.Fatal("the frame did not sharpen")
		}
		g := d.g
		g.BindFramebuffer(gl.FRAMEBUFFER, w.pane.R.WindowFBO)
		var px [4]byte
		g.ReadPixels(px[:], 2, 2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE)
		g.BindFramebuffer(gl.FRAMEBUFFER, 0)
		if px[2] < 240 {
			t.Fatalf("the sharpening drew nothing: %v", px)
		}
		if len(w.presented) != 0 {
			t.Fatal("a sharpened frame said it presented one the engine never sent")
		}
		d.sharpenAt(w, time.Time{})
		if w.sharpTimer != nil {
			t.Fatal("timer not cancelled")
		}
	})
	t.Run("context restoration", func(t *testing.T) {
		ext := d.canvas.Call("getContext", "webgl2").Call("getExtension", "WEBGL_lose_context")
		if ext.IsNull() {
			t.Fatal("context loss extension unavailable")
		}
		ext.Call("loseContext")
		deadline := time.Now().Add(2 * time.Second)
		for !d.lost && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !d.lost {
			t.Fatal("loss not detected")
		}
		// Restoration is allowed only after the contextlost event has returned.
		time.Sleep(100 * time.Millisecond)
		ext.Call("restoreContext")
		deadline = time.Now().Add(3 * time.Second)
		for d.lost && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if d.lost {
			t.Fatal("restore timeout")
		}
		_ = w.Present(solid(blue, w.Size()), []geom.Rect{paint.Everything})
		d.frame()
		if p := pixel(4, 4); p[2] < 240 {
			t.Fatalf("restored pixels=%v", p)
		}
	})
	t.Run("a new window asks for its first frame", func(t *testing.T) {
		// A Root with no commands queued draws only if the driver asks.
		raw, err := d.NewWindow(driver.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = raw.Close() }()
		if !slices.Contains(drain(raw.(*Window)), any(driver.Redraw{})) {
			t.Fatal("no Redraw from a new window")
		}
	})
	mouse := func(kind string, button, buttons int) {
		r := d.canvas.Call("getBoundingClientRect")
		d.canvas.Call("dispatchEvent", js.Global().Get("PointerEvent").New(kind, map[string]any{
			"bubbles": true, "cancelable": true, "pointerId": 1, "pointerType": "mouse", "button": button, "buttons": buttons,
			"clientX": r.Get("left").Float() + 40, "clientY": r.Get("top").Float() + 40,
		}))
	}
	t.Run("a mouse press is cancelled at mousedown, not pointerdown", func(t *testing.T) {
		// A cancelled pointerdown would stop the browser's own mouseup, which
		// Web Audio's wait for a gesture hangs on. Only a real click makes the
		// browser send it, so this checks what decides that.
		r := d.canvas.Call("getBoundingClientRect")
		event := func(kind, class string) js.Value {
			return js.Global().Get(class).New(kind, map[string]any{
				"bubbles": true, "cancelable": true, "pointerId": 1, "pointerType": "mouse", "button": 0, "buttons": 1,
				"clientX": r.Get("left").Float() + 40, "clientY": r.Get("top").Float() + 40,
			})
		}
		if !d.canvas.Call("dispatchEvent", event("pointerdown", "PointerEvent")).Bool() {
			t.Error("a mouse's pointerdown was cancelled")
		}
		if d.canvas.Call("dispatchEvent", event("mousedown", "MouseEvent")).Bool() {
			t.Error("a mouse's mousedown was not cancelled")
		}
		mouse("pointerup", 0, 0)
		drain(w)
	})
	t.Run("chorded buttons each press and let go", func(t *testing.T) {
		drain(w)
		mouse("pointerdown", 0, 1)
		mouse("pointermove", 2, 3)
		mouse("pointermove", 0, 2)
		mouse("pointerup", 2, 0)
		var got []string
		for _, e := range drain(w) {
			switch e := e.(type) {
			case input.PointerDown:
				got = append(got, fmt.Sprint("down ", e.Button))
			case input.PointerUp:
				got = append(got, fmt.Sprint("up ", e.Button))
			}
		}
		want := []string{fmt.Sprint("down ", input.ButtonPrimary), fmt.Sprint("down ", input.ButtonSecondary), fmt.Sprint("up ", input.ButtonPrimary), fmt.Sprint("up ", input.ButtonSecondary)}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("only the canvas's own events count", func(t *testing.T) {
		drain(w)
		parent := d.canvas.Get("parentElement")
		r := d.canvas.Call("getBoundingClientRect")
		parent.Call("dispatchEvent", js.Global().Get("PointerEvent").New("pointerdown", map[string]any{
			"bubbles": true, "cancelable": true, "pointerId": 3, "pointerType": "mouse", "button": 0, "buttons": 1,
			"clientX": r.Get("left").Float() + 40, "clientY": r.Get("top").Float() + 40,
		}))
		for _, e := range drain(w) {
			if _, ok := e.(input.PointerDown); ok {
				t.Fatal("a press on the canvas's parent reached gunim")
			}
		}
	})
	t.Run("touches are gunim's unless the page says", func(t *testing.T) {
		if got := d.canvas.Get("style").Get("touchAction").String(); got != "none" {
			t.Fatalf("touch-action is %q", got)
		}
		was := d.touchAction
		defer func() { d.touchAction = was; _ = w.TakeFingers(false) }()
		d.touchAction = "pan-y"
		_ = w.TakeFingers(true)
		if got := d.canvas.Get("style").Get("touchAction").String(); got != "none" {
			t.Errorf("taking fingers left touch-action at %q", got)
		}
		_ = w.TakeFingers(false)
		if got := d.canvas.Get("style").Get("touchAction").String(); got != "pan-y" {
			t.Errorf("the page's touch-action came back as %q", got)
		}
	})
	sized := func(style map[string]string) (*Driver, js.Value) {
		doc := js.Global().Get("document")
		c := doc.Call("createElement", "canvas")
		for k, v := range style {
			c.Get("style").Set(k, v)
		}
		doc.Get("body").Call("appendChild", c)
		return &Driver{canvas: c}, c
	}
	t.Run("a canvas with no CSS size stays that size", func(t *testing.T) {
		x, c := sized(nil)
		defer c.Call("remove")
		w0, h0 := c.Get("clientWidth").Int(), c.Get("clientHeight").Int()
		x.measure()
		c.Set("width", 2400)
		c.Set("height", 1600)
		if gw, gh := c.Get("clientWidth").Int(), c.Get("clientHeight").Int(); gw != w0 || gh != h0 {
			t.Fatalf("%dx%d grew to %dx%d", w0, h0, gw, gh)
		}
	})
	t.Run("a canvas hidden at first stays its size once shown", func(t *testing.T) {
		x, c := sized(map[string]string{"display": "none"})
		defer c.Call("remove")
		x.measure()
		c.Get("style").Set("display", "block")
		w0, h0 := c.Get("clientWidth").Int(), c.Get("clientHeight").Int()
		if w0 == 0 || h0 == 0 {
			t.Fatalf("shown, it measures %dx%d", w0, h0)
		}
		x.measure()
		if c.Get("width").Int() != int(math.Round(float64(float32(w0)*x.scale))) {
			t.Errorf("its buffer is %d wide for %d at scale %v", c.Get("width").Int(), w0, x.scale)
		}
		c.Set("width", 2400)
		c.Set("height", 1600)
		if gw, gh := c.Get("clientWidth").Int(), c.Get("clientHeight").Int(); gw != w0 || gh != h0 {
			t.Fatalf("%dx%d grew to %dx%d", w0, h0, gw, gh)
		}
	})
	t.Run("the browser's exact size sets the buffer", func(t *testing.T) {
		x, c := sized(map[string]string{"display": "block", "width": "200px", "height": "100px"})
		defer c.Call("remove")
		x.measure()
		x.exactW, x.exactH = x.surfW+1, x.surfH+1
		want := x.surfW + 1
		x.measure()
		if got := c.Get("width").Int(); got != want || x.surfW != want {
			t.Errorf("the buffer is %d wide, surfW %d, want %d", got, x.surfW, want)
		}
	})
	t.Run("a new size draws at once, to fill the canvas", func(t *testing.T) {
		st := d.canvas.Get("style")
		oldW, oldH := st.Get("width").String(), st.Get("height").String()
		defer func() {
			st.Set("width", oldW)
			st.Set("height", oldH)
			d.observedSize()
			drain(w)
		}()
		// A small square at the top left, on a background of another colour.
		var pt paint.Painter
		pt.RRect(geom.Rect{Min: geom.Pt(2, 2), Max: geom.Pt(20, 20)}, 0, paint.Solid(blue))
		ops := pt.Ops()
		w.SetBackground(color.NRGBA{R: 255, A: 255})
		_ = w.Present(ops, []geom.Rect{paint.Everything})
		d.frame()
		st.Set("width", "400px")
		st.Set("height", "300px")
		// As the ResizeObserver does: the browser shows the canvas straight after.
		d.observedSize()
		if d.surfW != d.canvas.Get("width").Int() {
			t.Fatalf("surfW %d, buffer %d", d.surfW, d.canvas.Get("width").Int())
		}
		// The square stays at the top, and what the new size added shows the background.
		// The square starts 2 logical pixels in, so a device pixel in it depends on the scale.
		inSquare := int(10 * d.scale)
		if p := pixel(inSquare, inSquare); p[2] < 240 {
			t.Errorf("the square at the top left shows %v", p)
		}
		if p := pixel(d.surfW-4, d.surfH-4); p[0] < 240 {
			t.Errorf("the far corner shows %v", p)
		}
		// A window that fills the canvas draws at the browser's count of its pixels.
		d.exactW, d.exactH = d.surfW+1, d.surfH+1
		d.observedSize()
		_ = w.Present(ops, []geom.Rect{paint.Everything})
		d.frame()
		if w.pane.LastW != d.surfW || w.pane.LastH != d.surfH || w.pane.OnTex {
			t.Errorf("it drew at %dx%d on a canvas of %dx%d, on a texture %v", w.pane.LastW, w.pane.LastH, d.surfW, d.surfH, w.pane.OnTex)
		}
		d.exactW, d.exactH = 0, 0
		d.observedSize()
		// With a popup open, the window goes on a texture, which keeps up the same way.
		pw, err := d.NewWindow(driver.Options{Kind: driver.KindPopup, Parent: w, Size: geom.Sz(30, 30), Anchor: geom.Rect{Min: geom.Pt(100, 100), Max: geom.Pt(100, 100)}, Over: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = pw.Close(); d.frame() }()
		_ = pw.Present(solid(blue, pw.Size()), []geom.Rect{paint.Everything})
		d.frame()
		st.Set("width", "440px")
		st.Set("height", "330px")
		d.observedSize()
		if p := pixel(inSquare, inSquare); p[2] < 240 {
			t.Errorf("under a popup, the square at the top left shows %v", p)
		}
		if p := pixel(d.surfW-4, d.surfH-4); p[0] < 240 {
			t.Errorf("under a popup, the far corner shows %v", p)
		}
	})
	t.Run("what the ResizeObserver sets is drawn before the browser shows it", func(t *testing.T) {
		doc := js.Global().Get("document")
		if doc.Get("visibilityState").String() != "visible" {
			t.Skip("a hidden page gets no resize steps")
		}
		st := d.canvas.Get("style")
		oldW, oldH := st.Get("width").String(), st.Get("height").String()
		defer func() {
			st.Set("width", oldW)
			st.Set("height", oldH)
		}()
		var pt paint.Painter
		pt.RRect(geom.Rect{Min: geom.Pt(2, 2), Max: geom.Pt(20, 20)}, 0, paint.Solid(blue))
		w.SetBackground(color.NRGBA{R: 255, A: 255})
		_ = w.Present(pt.Ops(), []geom.Rect{paint.Everything})
		d.frame()
		// An observer made after the driver's runs just after it, before the browser
		// shows the canvas, so it reads what is about to show.
		seen := make(chan []byte, 4)
		f := js.FuncOf(func(js.Value, []js.Value) any {
			select {
			case seen <- pixel(d.surfW-4, d.surfH-4):
			default:
			}
			return nil
		})
		defer f.Release()
		ro := js.Global().Get("ResizeObserver").New(f)
		defer ro.Call("disconnect")
		ro.Call("observe", d.canvas)
		next := func() []byte {
			select {
			case p := <-seen:
				return p
			case <-time.After(10 * time.Second):
				t.Fatal("no resize step came")
				return nil
			}
		}
		next() // Every observer hears once as it starts.
		st.Set("width", "380px")
		st.Set("height", "280px")
		if p := next(); p[0] < 240 {
			t.Errorf("the browser would show %v", p)
		}
		drain(w)
	})
	t.Run("the browser's exact size counts only within rounding", func(t *testing.T) {
		for _, c := range []struct {
			exact, worked int
			scale         float32
			want          bool
		}{{0, 600, 2, false}, {601, 600, 2, true}, {300, 600, 2, false}, {1501, 1500, 1.25, true}} {
			if got := near(c.exact, c.worked, c.scale); got != c.want {
				t.Errorf("%+v: got %v", c, got)
			}
		}
	})
	t.Run("a canvas with a CSS size keeps it free", func(t *testing.T) {
		doc := js.Global().Get("document")
		c := doc.Call("createElement", "canvas")
		c.Get("style").Set("width", "50%")
		c.Get("style").Set("height", "90px")
		doc.Get("body").Call("appendChild", c)
		defer c.Call("remove")
		(&Driver{canvas: c}).measure()
		if got := c.Get("style").Get("width").String(); got != "50%" {
			t.Errorf("width became %q", got)
		}
	})
	key := func(target js.Value, name, code string, mods ...string) (cancelled bool) {
		init := map[string]any{"bubbles": true, "cancelable": true, "key": name, "code": code}
		for _, m := range mods {
			init[m] = true
		}
		return !target.Call("dispatchEvent", js.Global().Get("KeyboardEvent").New("keydown", init)).Bool()
	}
	heard := func() (presses []input.KeyPress) {
		for _, e := range drain(w) {
			if e, ok := e.(input.KeyPress); ok {
				presses = append(presses, e)
			}
		}
		return presses
	}
	t.Run("each key has one owner", func(t *testing.T) {
		d.canvas.Call("focus")
		ctrl, cmd, shift, alt := "ctrlKey", "metaKey", "shiftKey", "altKey"
		for _, c := range []struct {
			name, code string
			mods       []string
			gunim      bool
		}{
			// The browser's alone.
			{"F5", "F5", nil, false},
			{"F6", "F6", nil, false},
			{"F11", "F11", nil, false},
			{"F12", "F12", nil, false},
			{"r", "KeyR", []string{ctrl}, false},
			{"R", "KeyR", []string{ctrl, shift}, false},
			{"r", "KeyR", []string{cmd}, false},
			{"-", "Minus", []string{ctrl}, false},
			{"+", "Equal", []string{ctrl, shift}, false},
			{"=", "Equal", []string{cmd}, false},
			{"0", "Digit0", []string{ctrl}, false},
			{"I", "KeyI", []string{ctrl, shift}, false},
			{"J", "KeyJ", []string{ctrl, shift}, false},
			{"i", "KeyI", []string{cmd, alt}, false},
			{"ArrowLeft", "ArrowLeft", []string{alt}, false},
			{"[", "BracketLeft", []string{cmd}, false},
			{"t", "KeyT", []string{ctrl}, false},
			{"w", "KeyW", []string{ctrl}, false},
			{"Tab", "Tab", []string{ctrl}, false},
			{"PageDown", "PageDown", []string{ctrl}, false},
			{"1", "Digit1", []string{ctrl}, false},
			{"l", "KeyL", []string{ctrl}, false},
			{"A", "KeyA", []string{ctrl, shift}, false},
			{"f", "KeyF", []string{ctrl, cmd}, false},
			{"d", "KeyD", []string{alt}, false},
			{"ArrowRight", "ArrowRight", []string{cmd, alt}, false},
			{"{", "BracketLeft", []string{cmd, shift}, false},
			// The same places on other layouts.
			{"к", "KeyR", []string{ctrl}, false},
			{"д", "KeyL", []string{ctrl}, false},
			{"Ш", "KeyI", []string{ctrl, shift}, false},
			{"&", "Digit1", []string{ctrl}, false},
			{"à", "Digit0", []string{ctrl}, false},
			// Gunim's alone.
			{"s", "KeyS", []string{ctrl}, true},
			{"f", "KeyF", []string{ctrl}, true},
			{"p", "KeyP", []string{ctrl}, true},
			{"k", "KeyK", []string{cmd}, true},
			{"a", "KeyA", []string{ctrl}, true},
			{"z", "KeyZ", []string{ctrl}, true},
			{"ArrowLeft", "ArrowLeft", []string{ctrl}, true},
			{"ArrowLeft", "ArrowLeft", []string{cmd}, true},
			{"ы", "KeyS", []string{ctrl}, true},
			{"!", "Digit1", []string{ctrl, shift}, true},
			// Typed on the canvas: the handler may move focus to the mirror before
			// the browser ends the key, which must not type it there too.
			{"g", "KeyG", nil, true},
			{"F1", "F1", nil, true},
			{"F3", "F3", nil, true},
			{"Tab", "Tab", nil, true},
			{"ArrowDown", "ArrowDown", nil, true},
			{" ", "Space", nil, true},
		} {
			if !d.canvas.Equal(js.Global().Get("document").Get("activeElement")) {
				t.Skip("this browser would not focus the canvas")
			}
			if got := key(d.canvas, c.name, c.code, c.mods...); got != c.gunim {
				t.Errorf("%q with %v: cancelled %v, want %v", c.name, c.mods, got, c.gunim)
			}
			if got := len(heard()) > 0; got != c.gunim {
				t.Errorf("%q with %v: gunim heard it %v, want %v", c.name, c.mods, got, c.gunim)
			}
		}
	})
	t.Run("a key's owner is settled as it goes down", func(t *testing.T) {
		d.canvas.Call("focus")
		up := func(name, code string, mods ...string) (released bool) {
			init := map[string]any{"bubbles": true, "cancelable": true, "key": name, "code": code}
			for _, m := range mods {
				init[m] = true
			}
			d.canvas.Call("dispatchEvent", js.Global().Get("KeyboardEvent").New("keyup", init))
			for _, e := range drain(w) {
				if _, ok := e.(input.KeyRelease); ok {
					released = true
				}
			}
			return released
		}
		// Gunim's as it went down, and Control held as it comes up.
		key(d.canvas, "w", "KeyW")
		drain(w)
		if !up("w", "KeyW", "ctrlKey") {
			t.Error("a key gunim heard go down never came up")
		}
		// The browser's as it went down, and Control let go first.
		key(d.canvas, "0", "Digit0", "ctrlKey")
		drain(w)
		if up("0", "Digit0") {
			t.Error("gunim heard a key come up that the browser kept")
		}
		// A key the browser kept whose release it swallowed, as for a new tab, starts afresh.
		key(d.canvas, "t", "KeyT", "ctrlKey")
		drain(w)
		key(d.canvas, "t", "KeyT")
		if len(heard()) != 1 {
			t.Error("a key the browser last kept was not heard going down again")
		}
		if !up("t", "KeyT") {
			t.Error("a key the browser last kept was not heard coming up")
		}
	})
	t.Run("cut and copy with no text state are heard", func(t *testing.T) {
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		w.SetTextState(nil, w.editSeq)
		d.text.Call("focus")
		drain(w)
		for _, c := range [][2]string{{"c", "KeyC"}, {"x", "KeyX"}} {
			if !key(d.text, c[0], c[1], "ctrlKey") || len(heard()) != 1 {
				t.Errorf("%s with no text state stayed the browser's", c[0])
			}
		}
		if key(d.text, "v", "KeyV", "ctrlKey") || len(heard()) != 0 {
			t.Error("paste with no text state left the browser, which brings the text")
		}
		w.SetTextState(&input.TextState{Text: "abc"}, w.editSeq)
		drain(w)
		for _, c := range [][2]string{{"c", "KeyC"}, {"x", "KeyX"}, {"v", "KeyV"}} {
			if key(d.text, c[0], c[1], "ctrlKey") || len(heard()) != 0 {
				t.Errorf("%s in a field left the browser", c[0])
			}
		}
	})
	t.Run("dead keys and AltGr stay the browser's", func(t *testing.T) {
		w.SetTextInput(true)
		w.SetTextState(&input.TextState{}, w.editSeq)
		defer w.SetTextInput(false)
		for _, target := range []js.Value{d.canvas, d.text} {
			target.Call("focus")
			if key(target, "Dead", "Equal") {
				t.Errorf("a dead key on %v was cancelled", target.Get("tagName"))
			}
			if got := heard(); len(got) != 0 {
				t.Errorf("a dead key on %v reached gunim: %v", target.Get("tagName"), got)
			}
		}
		// A character AltGr types, in a field: no shortcut, and the mods go.
		for _, mods := range [][]string{{"ctrlKey", "altKey"}, {"modifierAltGraph"}} {
			if key(d.text, "@", "Digit2", mods...) {
				t.Errorf("@ with %v was cancelled", mods)
			}
			got := heard()
			if len(got) != 1 || got[0].Mods&(input.ModControl|input.ModAlt) != 0 {
				t.Errorf("@ with %v reached gunim as %v", mods, got)
			}
		}
		// With AltGr on, what looks like a cut is a letter, and stays gunim's to hear.
		key(d.text, "x", "KeyX", "ctrlKey", "altKey", "modifierAltGraph")
		if got := heard(); len(got) != 1 || got[0].Mods&input.ModControl != 0 {
			t.Errorf("x with AltGr reached gunim as %v", got)
		}
		if key(d.text, " ", "Space") {
			t.Error("a space in the mirror was cancelled, which is what types it")
		}
		drain(w)
	})
	t.Run("a field reached by keys takes the keyboard, and a finger's waits", func(t *testing.T) {
		doc := js.Global().Get("document")
		d.mu.Lock()
		d.lastTouch = false
		d.mu.Unlock()
		d.canvas.Call("focus")
		if !d.canvas.Equal(doc.Get("activeElement")) {
			t.Skip("this browser would not focus the canvas")
		}
		w.SetTextInput(true)
		if !d.text.Equal(doc.Get("activeElement")) {
			t.Errorf("with no press first, %v has the keyboard", doc.Get("activeElement").Get("tagName"))
		}
		w.SetTextInput(false)
		pointer(d, "pointerdown", 7, 5, 5)
		pointer(d, "pointerup", 7, 5, 5)
		drain(w)
		w.SetTextInput(true)
		if d.text.Equal(doc.Get("activeElement")) {
			t.Error("a finger's field brought up the keyboard with no tap to ask")
		}
		w.SetTextInput(false)
		// The page's own field keeps the keyboard.
		mine := doc.Call("createElement", "input")
		doc.Get("body").Call("appendChild", mine)
		defer mine.Call("remove")
		d.mu.Lock()
		d.lastTouch = false
		d.mu.Unlock()
		mine.Call("focus")
		w.SetTextInput(true)
		if !mine.Equal(doc.Get("activeElement")) {
			t.Error("the page's own field lost the keyboard")
		}
		w.SetTextInput(false)
	})
	t.Run("what is held comes up as focus leaves", func(t *testing.T) {
		released := func() (keys []input.Key, buttons []input.Button, fingers []int) {
			for _, e := range drain(w) {
				switch e := e.(type) {
				case input.KeyRelease:
					keys = append(keys, e.Key)
				case input.PointerUp:
					if e.Pos != input.Away {
						t.Errorf("a button let go at %v, where it might click", e.Pos)
					}
					buttons = append(buttons, e.Button)
				case input.Finger:
					if e.Phase == input.FingerUp {
						if e.Pos != input.Away {
							t.Errorf("a finger let go at %v", e.Pos)
						}
						fingers = append(fingers, e.ID)
					}
				}
			}
			return keys, buttons, fingers
		}
		up := func(name, code string) {
			d.canvas.Call("dispatchEvent", js.Global().Get("KeyboardEvent").New("keyup", map[string]any{"bubbles": true, "cancelable": true, "key": name, "code": code}))
		}
		leave := map[string]func(){
			"the window": func() { js.Global().Call("dispatchEvent", js.Global().Get("Event").New("blur")) },
		}
		// With no window focus, focus moves and no blur fires.
		if js.Global().Get("document").Call("hasFocus").Bool() {
			leave["the page's own element"] = func() {
				mine := js.Global().Get("document").Call("createElement", "input")
				js.Global().Get("document").Get("body").Call("appendChild", mine)
				mine.Call("focus")
				mine.Call("remove")
			}
		}
		// The tests before pressed keys without letting go.
		d.releaseHeld()
		for name, away := range leave {
			d.canvas.Call("focus")
			drain(w)
			key(d.canvas, "w", "KeyW")
			key(d.canvas, "Shift", "ShiftLeft", "shiftKey")
			key(d.canvas, "r", "KeyR", "ctrlKey") // the browser's: gunim never heard it go down
			drain(w)
			away()
			keys, _, _ := released()
			slices.Sort(keys)
			want := []input.Key{input.KeyW, input.KeyLeftShift}
			slices.Sort(want)
			if !slices.Equal(keys, want) {
				t.Errorf("%s: released %v, want %v", name, keys, want)
			}
			// Its real release comes when focus is back, and is no second one.
			d.canvas.Call("focus")
			up("w", "KeyW")
			if keys, _, _ := released(); len(keys) != 0 {
				t.Errorf("%s: a key came up twice: %v", name, keys)
			}
		}
		// Moving between what the driver owns holds on to them.
		d.canvas.Call("focus")
		key(d.canvas, "w", "KeyW")
		drain(w)
		w.SetTextState(&input.TextState{}, w.editSeq)
		w.SetTextInput(true)
		w.SetTextInput(false)
		if keys, _, _ := released(); len(keys) != 0 {
			t.Errorf("the keyboard moving to the field and back let go of %v", keys)
		}
		up("w", "KeyW")
		if keys, _, _ := released(); !slices.Equal(keys, []input.Key{input.KeyW}) {
			t.Errorf("after that, its own release gave %v", keys)
		}
		// A key whose release says it is composing still comes up.
		key(d.canvas, "w", "KeyW")
		drain(w)
		d.canvas.Call("dispatchEvent", js.Global().Get("KeyboardEvent").New("keyup", map[string]any{"bubbles": true, "cancelable": true, "key": "Process", "code": "KeyW", "isComposing": true}))
		if keys, _, _ := released(); !slices.Equal(keys, []input.Key{input.KeyW}) {
			t.Errorf("a composing release gave %v", keys)
		}
		// Buttons, each one held.
		blur := leave["the window"]
		mouse("pointerdown", 0, 1)
		mouse("pointermove", 2, 3)
		drain(w)
		blur()
		_, buttons, _ := released()
		if !slices.Equal(buttons, []input.Button{input.ButtonPrimary, input.ButtonSecondary}) {
			t.Errorf("buttons released %v", buttons)
		}
		mouse("pointerup", 2, 0)
		if _, buttons, _ := released(); len(buttons) != 0 {
			t.Errorf("a button came up twice: %v", buttons)
		}
		if d.pressed != nil {
			t.Error("the press is still held")
		}
		// A finger of its own.
		_ = w.TakeFingers(true)
		pointer(d, "pointerdown", 1, 40, 40)
		drain(w)
		blur()
		if _, _, fingers := released(); !slices.Equal(fingers, []int{1}) {
			t.Errorf("fingers released %v", fingers)
		}
		pointer(d, "pointerup", 1, 40, 40)
		_ = w.TakeFingers(false)
		d.mu.Lock()
		d.lastTouch = false
		d.mu.Unlock()
		drain(w)
		// Hiding the tab does the same.
		d.canvas.Call("focus")
		key(d.canvas, "w", "KeyW")
		drain(w)
		hidden := js.Global().Get("Object").Call("getOwnPropertyDescriptor", js.Global().Get("document"), "visibilityState")
		js.Global().Get("Object").Call("defineProperty", js.Global().Get("document"), "visibilityState", map[string]any{"configurable": true, "value": "hidden"})
		js.Global().Get("document").Call("dispatchEvent", js.Global().Get("Event").New("visibilitychange"))
		if hidden.IsUndefined() {
			js.Global().Get("Reflect").Call("deleteProperty", js.Global().Get("document"), "visibilityState")
		} else {
			js.Global().Get("Object").Call("defineProperty", js.Global().Get("document"), "visibilityState", hidden)
		}
		if keys, _, _ := released(); !slices.Equal(keys, []input.Key{input.KeyW}) {
			t.Errorf("hiding the tab released %v", keys)
		}
	})
	t.Run("a secret lives in a password field, only while focused", func(t *testing.T) {
		if !js.Global().Get("document").Call("hasFocus").Bool() {
			t.Skip("the page has no window focus, so focus and blur don't fire")
		}
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		w.SetTextState(&input.TextState{Text: "abc"}, w.editSeq)
		if got := d.plain.Get("value").String(); got != "abc" {
			t.Fatalf("the textarea holds %q, want abc", got)
		}
		w.SetTextState(&input.TextState{Text: "hunter2", Secret: true}, w.editSeq)
		if got := d.plain.Get("value").String(); got != "" {
			t.Errorf("the textarea kept %q past a secret", got)
		}
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		if !d.text.Equal(d.secret) || d.secret.Get("type").String() != "password" {
			t.Fatalf("typing in a %v", d.text.Get("tagName"))
		}
		d.secret.Call("focus")
		if got := d.secret.Get("value").String(); got != "hunter2" {
			t.Fatalf("focused, it holds %q", got)
		}
		if got := d.plain.Get("value").String(); got != "" {
			t.Errorf("the textarea holds %q", got)
		}
		for _, name := range []string{"copy", "cut"} {
			event := js.Global().Get("Event").New(name, map[string]any{"bubbles": true, "cancelable": true})
			if d.secret.Call("dispatchEvent", event).Bool() {
				t.Errorf("%s was not cancelled", name)
			}
		}
		d.secret.Call("blur")
		if got := d.secret.Get("value").String(); got != "" {
			t.Errorf("after focus left, it holds %q", got)
		}
		w.SetTextState(&input.TextState{Text: "hunter2", Secret: true}, w.editSeq)
		if got := d.secret.Get("value").String(); got != "" {
			t.Errorf("a new state unfocused put back %q", got)
		}
		d.secret.Call("focus")
		if got := d.secret.Get("value").String(); got != "hunter2" {
			t.Errorf("focused again, it holds %q", got)
		}
		w.SetTextState(&input.TextState{Text: "plain"}, w.editSeq)
		if !d.text.Equal(d.plain) || d.secret.Get("value").String() != "" {
			t.Errorf("a plain field kept the password field: %v, %q", d.text.Get("tagName"), d.secret.Get("value"))
		}
	})
	t.Run("switching windows leaves a secret alone", func(t *testing.T) {
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		st := input.TextState{Text: "hunter2", Selection: [2]int{7, 7}, Composing: [2]int{7, 7}, Secret: true}
		w.SetTextState(&st, w.editSeq)
		d.secret.Call("focus")
		if got := d.secret.Get("value").String(); got != "hunter2" {
			t.Fatalf("focused, it holds %q", got)
		}
		drain(w)
		doc := js.Global().Get("document")
		// Switching windows, in Firefox above all: the field hears blur, and stays the
		// active element, and a selectionchange comes after.
		d.secret.Call("dispatchEvent", js.Global().Get("FocusEvent").New("blur"))
		if !d.secret.Equal(doc.Get("activeElement")) {
			t.Fatal("this test wants the field active")
		}
		if got := d.secret.Get("value").String(); got != "" {
			t.Errorf("away, it holds %q", got)
		}
		// A real one, if this browser sends it for the field emptied, and one to be sure.
		time.Sleep(50 * time.Millisecond)
		doc.Call("dispatchEvent", js.Global().Get("Event").New("selectionchange"))
		d.secret.Call("dispatchEvent", js.Global().Get("InputEvent").New("input"))
		for _, e := range drain(w) {
			if e, ok := e.(input.TextEdit); ok {
				t.Errorf("the window going away sent an edit: %+v", e)
			}
		}
		if w.state.Text != "hunter2" {
			t.Errorf("the driver's copy holds %q", w.state.Text)
		}
		// A state that comes while away stays out of the page.
		w.SetTextState(&st, w.editSeq)
		if got := d.secret.Get("value").String(); got != "" {
			t.Errorf("a state put %q back with the window away", got)
		}
		d.secret.Call("dispatchEvent", js.Global().Get("FocusEvent").New("focus"))
		if got := d.secret.Get("value").String(); got != "hunter2" {
			t.Errorf("back again, it holds %q", got)
		}
		drain(w)
	})
	t.Run("closing a window from its field leaves nothing behind", func(t *testing.T) {
		doc := js.Global().Get("document")
		for _, st := range []input.TextState{{Text: "hunter2", Secret: true}, {Text: "plain"}} {
			raw, err := d.NewWindow(driver.Options{})
			if err != nil {
				t.Fatal(err)
			}
			x := raw.(*Window)
			x.SetTextState(&st, 0)
			x.SetTextInput(true)
			// A secret goes in on focus, and with no window focus that never fires.
			if got := d.text.Get("value").String(); got != st.Text && doc.Call("hasFocus").Bool() {
				t.Errorf("the mirror holds %q", got)
			}
			_ = x.Close()
			for name, m := range map[string]js.Value{"textarea": d.plain, "password field": d.secret} {
				if got := m.Get("value").String(); got != "" {
					t.Errorf("after %q, the %s holds %q", st.Text, name, got)
				}
			}
			if !d.canvas.Equal(doc.Get("activeElement")) {
				t.Errorf("after %q, %v has the keyboard", st.Text, doc.Get("activeElement").Get("tagName"))
			}
			// Typing reaches the window underneath.
			drain(w)
			key(d.canvas, "g", "KeyG")
			found := false
			for _, e := range drain(w) {
				if e, ok := e.(input.TextInput); ok && e.Text == "g" {
					found = true
				}
			}
			if !found {
				t.Errorf("after %q, typing went nowhere", st.Text)
			}
		}
		// The page's own element keeps the keyboard.
		mine := doc.Call("createElement", "input")
		doc.Get("body").Call("appendChild", mine)
		defer mine.Call("remove")
		raw, _ := d.NewWindow(driver.Options{})
		raw.(*Window).SetTextInput(true)
		mine.Call("focus")
		_ = raw.Close()
		if !mine.Equal(doc.Get("activeElement")) {
			t.Error("closing a window took the keyboard from the page's own element")
		}
		d.canvas.Call("focus")
		drain(w)
	})
	t.Run("a text's line endings stay as they are", func(t *testing.T) {
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		doc := js.Global().Get("document")
		selection := func() { doc.Call("dispatchEvent", js.Global().Get("Event").New("selectionchange")) }
		edits := func() (got []input.TextEdit) {
			for _, e := range drain(w) {
				if e, ok := e.(input.TextEdit); ok {
					got = append(got, e)
				}
			}
			return got
		}
		// "a\r\nb\r\nc" holds its bytes at 0 a, 1 \r, 2 \n, 3 b, 4 \r, 5 \n, 6 c.
		const crlf = "a\r\nb\r\nc"
		set := func(caret int) {
			w.SetTextState(&input.TextState{Text: crlf, Multiline: true, Selection: [2]int{caret, caret}, Composing: [2]int{caret, caret}}, w.editSeq)
		}
		for _, c := range []struct{ bytes, field int }{{0, 0}, {1, 1}, {3, 2}, {4, 3}, {6, 4}, {7, 5}, {2, 1}} {
			set(c.bytes)
			if got := d.text.Get("value").String(); got != "a\nb\nc" {
				t.Fatalf("the textarea holds %q", got)
			}
			if got := d.text.Get("selectionStart").Int(); got != c.field {
				t.Errorf("a caret at byte %d is at %d in the field, want %d", c.bytes, got, c.field)
			}
			// The click into the text, and the selectionchange it brings, change nothing:
			// that includes a caret between the \r and the \n.
			selection()
			if got := edits(); len(got) != 0 {
				t.Errorf("a caret at byte %d sent %+v", c.bytes, got)
			}
			if w.state.Text != crlf || w.state.Selection != [2]int{c.bytes, c.bytes} {
				t.Errorf("a caret at byte %d left %+v", c.bytes, w.state)
			}
		}
		// A caret moved in the field lands on the bytes after the line ending.
		set(0)
		d.text.Call("setSelectionRange", 2, 2)
		selection()
		if got := edits(); len(got) != 1 || got[0].Selection != [2]int{3, 3} || got[0].With != "" || got[0].Replace[0] != got[0].Replace[1] {
			t.Errorf("a caret moved after a line ending sent %+v", got)
		}
		// Typing changes what is typed, and leaves the line endings around it.
		set(4)
		d.text.Call("setRangeText", "X", 3, 3, "end")
		d.text.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		got := edits()
		if len(got) != 1 || got[0].Replace != [2]int{4, 4} || got[0].With != "X" || got[0].Selection != [2]int{5, 5} {
			t.Fatalf("typing X sent %+v", got)
		}
		if w.state.Text != "a\r\nbX\r\nc" {
			t.Errorf("after typing, the copy holds %q", w.state.Text)
		}
		// A deleted line ending goes whole.
		w.SetTextState(&input.TextState{Text: crlf, Multiline: true, Selection: [2]int{3, 3}, Composing: [2]int{3, 3}}, w.editSeq)
		d.text.Set("value", "ab\nc")
		d.text.Call("setSelectionRange", 1, 1)
		d.text.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		if w.state.Text != "ab\r\nc" || w.state.Selection != [2]int{1, 1} {
			t.Errorf("after deleting a line ending, the copy holds %+v", w.state)
		}
		drain(w)
		// A long text's stretch counts from where it starts.
		w.SetTextState(&input.TextState{Text: crlf, Start: 100, Multiline: true, Selection: [2]int{103, 103}, Composing: [2]int{103, 103}}, w.editSeq)
		if got := d.text.Get("selectionStart").Int(); got != 2 {
			t.Errorf("a caret in a stretch is at %d in the field", got)
		}
		d.text.Call("setRangeText", "Y", 2, 2, "end")
		d.text.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		got = edits()
		if len(got) != 1 || got[0].Replace != [2]int{103, 103} || got[0].With != "Y" || got[0].Selection != [2]int{104, 104} {
			t.Errorf("typing in a stretch sent %+v", got)
		}
	})
	t.Run("text follows in order when the engine is held up", func(t *testing.T) {
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		w.SetTextState(&input.TextState{Text: "abc", Selection: [2]int{3, 3}, Composing: [2]int{3, 3}}, w.editSeq)
		d.text.Call("focus")
		drain(w)
		before := func(data string) (cancelled bool) {
			e := js.Global().Get("InputEvent").New("beforeinput", map[string]any{"bubbles": true, "cancelable": true, "inputType": "insertText", "data": data})
			return !d.text.Call("dispatchEvent", e).Bool()
		}
		// With the engine level, the browser types in the mirror.
		if before("x") {
			t.Fatal("typing with the engine level was cancelled")
		}
		// A key the engine has yet to take: nothing reads the inbox in this test.
		key(d.text, "Backspace", "Backspace")
		if !before("d") {
			t.Fatal("typing into a mirror out of date was left to the browser")
		}
		var got []string
		for _, e := range drain(w) {
			switch e := e.(type) {
			case input.KeyPress:
				got = append(got, fmt.Sprint(e.Key))
			case input.TextInput:
				got = append(got, e.Text)
			}
		}
		if want := []string{fmt.Sprint(input.KeyBackspace), "d"}; !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
		if got := d.text.Get("value").String(); got != "abc" {
			t.Errorf("the mirror holds %q", got)
		}
	})
	t.Run("typing with no text state still arrives", func(t *testing.T) {
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		w.SetTextState(nil, w.editSeq)
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		drain(w)
		d.text.Set("value", "hej")
		d.text.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		found := false
		for _, e := range drain(w) {
			if e, ok := e.(input.TextInput); ok && e.Text == "hej" {
				found = true
			}
		}
		if !found || d.text.Get("value").String() != "" {
			t.Errorf("found %v, mirror holds %q", found, d.text.Get("value"))
		}
	})
	t.Run("typing with no text state starts clean after a field", func(t *testing.T) {
		w.SetTextInput(true)
		defer w.SetTextInput(false)
		defer w.SetTextState(&input.TextState{}, w.editSeq)
		for _, was := range []input.TextState{{Text: "abc"}, {Text: "hunter2", Secret: true}} {
			w.SetTextState(&was, w.editSeq)
			w.SetTextState(nil, w.editSeq)
			if !d.text.Equal(d.plain) {
				t.Errorf("after %+v, typing goes to a %v", was, d.text.Get("tagName"))
			}
			if got := d.text.Get("value").String(); got != "" {
				t.Errorf("after %+v, the mirror holds %q", was, got)
			}
			drain(w)
			d.text.Set("value", "h")
			d.text.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
			var got []string
			for _, e := range drain(w) {
				if e, ok := e.(input.TextInput); ok {
					got = append(got, e.Text)
				}
			}
			if len(got) != 1 || got[0] != "h" {
				t.Errorf("after %+v, typing h arrives as %q", was, got)
			}
		}
	})
	t.Run("a second finger leaves the first one's press alone", func(t *testing.T) {
		seen := drain(w)
		pointer(d, "pointerdown", 1, 40, 40)
		pointer(d, "pointerdown", 2, 200, 200)
		pointer(d, "pointermove", 2, 210, 210)
		pointer(d, "pointermove", 1, 50, 50)
		pointer(d, "pointerup", 2, 210, 210)
		pointer(d, "pointerup", 1, 50, 50)
		seen = append(seen[:0], drain(w)...)
		var downs, ups int
		for _, e := range seen {
			switch e := e.(type) {
			case input.PointerDown:
				downs++
				if e.Pos != geom.Pt(40, 40) {
					t.Fatalf("pressed at %v", e.Pos)
				}
			case input.PointerUp:
				ups++
				if e.Pos != geom.Pt(50, 50) {
					t.Fatalf("let go at %v", e.Pos)
				}
			case input.PointerMove:
				if e.Pos == geom.Pt(210, 210) {
					t.Fatal("the second finger moved the pointer")
				}
			}
		}
		if downs != 1 || ups != 1 {
			t.Fatalf("downs=%d ups=%d, in %v", downs, ups, seen)
		}
	})
	t.Run("a window that takes fingers hears each one", func(t *testing.T) {
		if err := w.TakeFingers(true); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = w.TakeFingers(false) }()
		if got := d.canvas.Get("style").Get("touchAction").String(); got != "none" {
			t.Fatalf("touch-action %q", got)
		}
		drain(w)
		pointer(d, "pointerdown", 1, 40, 40)
		pointer(d, "pointerdown", 2, 200, 200)
		pointer(d, "pointermove", 2, 210, 210)
		pointer(d, "pointercancel", 2, 210, 210)
		pointer(d, "pointerup", 1, 40, 40)
		var phases []string
		for _, e := range drain(w) {
			if f, ok := e.(input.Finger); ok {
				phases = append(phases, fmt.Sprint(f.ID, ":", f.Phase, f.Pos == input.Away))
			}
		}
		want := []string{
			fmt.Sprint(1, ":", input.FingerDown, false), fmt.Sprint(2, ":", input.FingerDown, false),
			fmt.Sprint(2, ":", input.FingerMove, false), fmt.Sprint(2, ":", input.FingerUp, true),
			fmt.Sprint(1, ":", input.FingerUp, false),
		}
		if !slices.Equal(phases, want) {
			t.Fatalf("fingers %v, want %v", phases, want)
		}
		if len(d.fingers) != 0 {
			t.Fatalf("fingers left %v", d.fingers)
		}
	})
	t.Run("a new vertex store holds just what it was given", func(t *testing.T) {
		g := d.g
		b := g.CreateBuffer()
		defer g.DeleteBuffer(b)
		g.BindBuffer(gl.ARRAY_BUFFER, b)
		defer g.BindBuffer(gl.ARRAY_BUFFER, 0)
		g.BufferData(gl.ARRAY_BUFFER, []byte{9, 8, 7}, gl.STREAM_DRAW)
		ctx := d.canvas.Call("getContext", "webgl2")
		if n := ctx.Call("getBufferParameter", gl.ARRAY_BUFFER, ctx.Get("BUFFER_SIZE")).Int(); n != 3 {
			t.Fatalf("the store holds %d bytes, want 3", n)
		}
		back := js.Global().Get("Uint8Array").New(3)
		ctx.Call("getBufferSubData", gl.ARRAY_BUFFER, 0, back)
		if got := [3]int{back.Index(0).Int(), back.Index(1).Int(), back.Index(2).Int()}; got != [3]int{9, 8, 7} {
			t.Fatalf("the store holds %v", got)
		}
	})
	t.Run("vibration and sharing", func(t *testing.T) {
		nav := js.Global().Get("navigator")
		calls := []any{}
		f := js.FuncOf(func(_ js.Value, a []js.Value) any {
			calls = append(calls, a[0].Length())
			return true
		})
		defer f.Release()
		js.Global().Get("Object").Call("defineProperty", nav, "vibrate", map[string]any{"value": f, "configurable": true})
		if err := w.Vibrate(10*time.Millisecond, 20*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		w.Buzz()
		if len(calls) != 2 || calls[0] != 2 || calls[1] != 1 {
			t.Fatalf("vibrate calls %v", calls)
		}
		if err := w.Share(driver.Share{Paths: []string{"/a"}}); !errors.Is(err, driver.ErrNoSharer) {
			t.Fatalf("sharing files: %v", err)
		}
	})
	t.Run("compass", func(t *testing.T) {
		if got := headingOf(90); got != 270 {
			t.Fatalf("heading of alpha 90 = %v", got)
		}
		w.WatchHeading(true)
		defer w.WatchHeading(false)
		drain(w)
		e := js.Global().Get("Event").New("deviceorientationabsolute")
		e.Set("alpha", 30)
		js.Global().Call("dispatchEvent", e)
		if !w.HasCompass() {
			t.Fatal("no compass after a reading")
		}
		var got []input.Heading
		for _, x := range drain(w) {
			if h, ok := x.(input.Heading); ok {
				got = append(got, h)
			}
		}
		if len(got) != 1 || got[0].Degrees != 330 {
			t.Fatalf("headings %v", got)
		}
	})
	_ = w.Close()
}

// pointer sends a touch's event to the canvas, as the browser would.
func pointer(d *Driver, kind string, id int, x, y float64) {
	r := d.canvas.Call("getBoundingClientRect")
	buttons := 1
	if kind == "pointerup" || kind == "pointercancel" {
		buttons = 0
	}
	e := js.Global().Get("PointerEvent").New(kind, map[string]any{
		"bubbles": true, "cancelable": true, "pointerId": id, "pointerType": "touch", "button": 0, "buttons": buttons,
		"clientX": r.Get("left").Float() + x, "clientY": r.Get("top").Float() + y,
	})
	d.canvas.Call("dispatchEvent", e)
}

// drain returns the events waiting for w, once its inbox has fed them through.
func drain(w *Window) []any {
	var out []any
	for {
		select {
		case e := <-w.Input():
			out = append(out, e)
		case <-time.After(30 * time.Millisecond):
			return out
		}
	}
}
