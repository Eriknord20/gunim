//go:build js && wasm

package web

import (
	"context"
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
	t.Run("events carry when they happened, however late they run", func(t *testing.T) {
		drain(w)
		r := d.canvas.Call("getBoundingClientRect")
		x, y := r.Get("left").Float()+40, r.Get("top").Float()+40
		// A burst, all run now, that a hand made 16 ms apart.
		base := js.Global().Get("performance").Call("now").Float() - 200
		event := func(class, kind string, ms float64, init map[string]any) {
			init["bubbles"], init["cancelable"], init["clientX"], init["clientY"] = true, true, x, y
			d.canvas.Call("dispatchEvent", stamped(js.Global().Get(class).New(kind, init), ms))
		}
		touch := func(kind string, ms float64) {
			event("PointerEvent", kind, ms, map[string]any{"pointerId": 5, "pointerType": "touch", "button": 0, "buttons": 1})
		}
		var times []time.Time
		note := func() {
			for _, e := range drain(w) {
				switch e := e.(type) {
				case input.PointerDown:
					times = append(times, e.Time)
				case input.PointerMove:
					times = append(times, e.Time)
				case input.PointerUp:
					times = append(times, e.Time)
				case input.Scroll:
					times = append(times, e.Time)
				case input.KeyPress:
					times = append(times, e.Time)
				}
			}
		}
		touch("pointerdown", base)
		note()
		touch("pointermove", base+16)
		note()
		touch("pointerup", base+32)
		note()
		event("WheelEvent", "wheel", base+48, map[string]any{"deltaY": 10})
		note()
		event("KeyboardEvent", "keydown", base+64, map[string]any{"key": "g", "code": "KeyG"})
		note()
		// The press and the lift each bring a move of their own.
		ms := time.Millisecond
		want := []time.Duration{0, 0, 16 * ms, 32 * ms, 32 * ms, 48 * ms, 64 * ms}
		if len(times) != len(want) {
			t.Fatalf("heard %d events, want %d", len(times), len(want))
		}
		for i, at := range want {
			gap := times[i].Sub(times[0])
			if off := gap - at; off < -2*ms || off > 2*ms {
				t.Errorf("event %d came %v after the first, want %v", i, gap, at)
			}
		}
		if age := time.Since(times[0]); age < 100*time.Millisecond {
			t.Errorf("the first was stamped %v ago, want about 200 ms", age)
		}
		// A stamp ahead of the clock is no time to come: it reads as now.
		event("KeyboardEvent", "keydown", base+100000, map[string]any{"key": "h", "code": "KeyH"})
		times = nil
		note()
		if len(times) != 1 || times[0].After(time.Now()) {
			t.Errorf("a stamp from the future reads %v", times)
		}
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
	t.Run("a mouse's side buttons are its own, and the browser keeps out", func(t *testing.T) {
		for _, c := range []struct {
			button, bit int
			want        input.Button
		}{{3, 8, input.ButtonBack}, {4, 16, input.ButtonForward}} {
			drain(w)
			mouse("pointerdown", c.button, c.bit)
			mouse("pointerup", c.button, 0)
			var got []input.Button
			for _, e := range drain(w) {
				switch e := e.(type) {
				case input.PointerDown:
					got = append(got, e.Button)
				case input.PointerUp:
					got = append(got, e.Button)
				}
			}
			if !slices.Equal(got, []input.Button{c.want, c.want}) {
				t.Errorf("button %d came as %v, want %v twice", c.button, got, c.want)
			}
			up := js.Global().Get("MouseEvent").New("mouseup", map[string]any{"bubbles": true, "cancelable": true, "button": c.button})
			if d.canvas.Call("dispatchEvent", up).Bool() {
				t.Errorf("button %d's mouseup was left for the browser, which would go back a page", c.button)
			}
		}
		up := js.Global().Get("MouseEvent").New("mouseup", map[string]any{"bubbles": true, "cancelable": true, "button": 0})
		if !d.canvas.Call("dispatchEvent", up).Bool() {
			t.Error("the primary button's mouseup was cancelled, which Web Audio's wait for a gesture hangs on")
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
	t.Run("a share the user closes is no error, and others keep their words", func(t *testing.T) {
		nav := js.Global().Get("navigator")
		defer define(nav, "share", js.Undefined())()
		for _, c := range []struct {
			page string
			want []string
		}{
			{`()=>Promise.reject(new DOMException("Share canceled","AbortError"))`, nil},
			{`()=>Promise.reject(new DOMException("Must be handling a user gesture","NotAllowedError"))`, []string{"NotAllowedError", "Must be handling a user gesture"}},
			{`()=>{throw new TypeError("no share")}`, []string{"TypeError", "no share"}},
			{`()=>Promise.reject("just words")`, []string{"just words"}},
			{`()=>Promise.reject()`, []string{"undefined"}},
			{`()=>Promise.reject({odd:1})`, []string{"object"}},
		} {
			define(nav, "share", js.Global().Call("eval", c.page))
			err := w.Share(driver.Share{Text: "hej"})
			switch {
			case c.want == nil && err != nil:
				t.Errorf("%s: %v", c.page, err)
			case c.want != nil && err == nil:
				t.Errorf("%s: no error", c.page)
			}
			for _, word := range c.want {
				if err != nil && !strings.Contains(err.Error(), word) {
					t.Errorf("%s: %q leaves out %q", c.page, err, word)
				}
			}
		}
	})
	t.Run("a malformed link is an error", func(t *testing.T) {
		defer define(js.Global(), "open", js.Undefined())()
		var opened []string
		spy := js.FuncOf(func(_ js.Value, a []js.Value) any {
			opened = append(opened, a[0].String())
			return js.Null()
		})
		defer spy.Release()
		define(js.Global(), "open", spy.Value)
		for _, bad := range []string{"http://[bad", "javascript:alert(1)", "data:text/html,hi", "no scheme"} {
			if err := w.OpenLink(bad); err == nil {
				t.Errorf("%q gave no error", bad)
			}
		}
		if len(opened) != 0 {
			t.Errorf("the browser was asked to open %q", opened)
		}
		if err := w.OpenLink("https://example.com/a?b=c"); err != nil || !slices.Equal(opened, []string{"https://example.com/a?b=c"}) {
			t.Errorf("a good link: %v, opened %q", err, opened)
		}
		// What the browser itself throws comes back the same way.
		define(js.Global(), "open", js.Global().Call("eval", `()=>{throw new DOMException("no","SyntaxError")}`))
		if err := w.OpenLink("https://example.com"); err == nil || !strings.Contains(err.Error(), "SyntaxError") {
			t.Errorf("a browser that throws: %v", err)
		}
	})
	t.Run("a clipboard the browser never answers lets the window go on", func(t *testing.T) {
		nav := js.Global().Get("navigator")
		defer define(nav, "clipboard", js.Undefined())()
		settle := js.Global().Get("Object").New()
		mock := js.Global().Call("eval", `(s=>({readText:()=>new Promise(r=>{s.go=r}),writeText:()=>new Promise(r=>{s.go=r})}))`).Invoke(settle)
		define(nav, "clipboard", mock)
		was := clipboardWait
		defer func() { clipboardWait = was }()
		clipboardWait = 50 * time.Millisecond
		errs := spyConsole()
		defer errs.stop()
		start := time.Now()
		if _, err := w.Clipboard(); !errors.Is(err, errNoAnswer) {
			t.Errorf("reading: %v", err)
		}
		if err := w.SetClipboard("x"); !errors.Is(err, errNoAnswer) {
			t.Errorf("writing: %v", err)
		}
		if took := time.Since(start); took > 10*time.Second {
			t.Errorf("it took %v", took)
		}
		// The browser answers after all, and finds what it calls still there.
		settle.Call("go", "late")
		time.Sleep(20 * time.Millisecond)
		if got := errs.stop(); len(got) != 0 {
			t.Errorf("the page logged %q", got)
		}
	})
	t.Run("the keyboard covers only what the canvas has down there", func(t *testing.T) {
		win := js.Global()
		height := win.Get("innerHeight").Float()
		bottom := d.canvas.Call("getBoundingClientRect").Get("bottom").Float()
		if bottom >= height-50 {
			t.Skip("the page has no room under the canvas")
		}
		defer func() { d.viewportChanged() }()
		defer define(win, "visualViewport", js.Undefined())()
		seen := func(h float64) float32 {
			define(win, "visualViewport", js.ValueOf(map[string]any{"scale": 1, "offsetTop": 0, "height": h}))
			d.viewportChanged()
			return w.KeyboardCover()
		}
		// The page's bottom is covered, but the canvas ends above the keyboard.
		if got := seen(bottom + 20); got != 0 {
			t.Errorf("a keyboard below the canvas covers %v of it", got)
		}
		if got, want := seen(bottom-30), float32(30); math.Abs(float64(got-want)) > .01 {
			t.Errorf("a keyboard over the canvas's last 30 px covers %v of it", got)
		}
		if got := seen(height); got != 0 {
			t.Errorf("with the keyboard down it covers %v", got)
		}
	})
	t.Run("compass", func(t *testing.T) {
		if got := headingOf(90); got != 270 {
			t.Fatalf("heading of alpha 90 = %v", got)
		}
		send := func(alpha any) {
			e := js.Global().Get("Event").New("deviceorientationabsolute")
			e.Set("alpha", alpha)
			js.Global().Call("dispatchEvent", e)
		}
		headings := func() (got []input.Heading) {
			for _, x := range drain(w) {
				if h, ok := x.(input.Heading); ok {
					got = append(got, h)
				}
			}
			return got
		}
		// Nothing has asked: the page doesn't listen, which would run a phone's sensors.
		send(30)
		if d.compass || d.compassOn {
			t.Fatalf("with nothing watching, the page listens: %v, and heard: %v", d.compassOn, d.compass)
		}
		// Asking listens for a first reading, and a browser with no sensors ends that.
		if w.HasCompass() {
			t.Fatal("a compass before any reading")
		}
		if !d.compassOn {
			t.Fatal("asking for a compass doesn't listen for one")
		}
		send(js.Null())
		if d.compassOn || w.HasCompass() {
			t.Fatalf("a reading of nothing: listening %v, compass %v", d.compassOn, d.compass)
		}
		send(30)
		if w.HasCompass() {
			t.Fatal("a reading came in after the page stopped listening")
		}
		// Watching listens, and stops with the last window that watches.
		w.WatchHeading(true)
		drain(w)
		send(30)
		if !w.HasCompass() {
			t.Fatal("no compass after a reading")
		}
		if got := headings(); len(got) != 1 || got[0].Degrees != 330 {
			t.Fatalf("headings %v", got)
		}
		w.WatchHeading(false)
		if d.compassOn {
			t.Fatal("still listening with nothing watching")
		}
		send(40)
		if got := headings(); len(got) != 0 {
			t.Fatalf("headings with nothing watching: %v", got)
		}
		// A window that closes watches no more.
		p, _ := d.NewWindow(driver.Options{})
		p.(*Window).WatchHeading(true)
		if !d.compassOn {
			t.Fatal("a second window's watch doesn't listen")
		}
		_ = p.Close()
		if d.compassOn {
			t.Fatal("still listening after the window that watched closed")
		}
	})
	_ = w.Close()
	t.Run("the driver leaves the page as it ends", func(t *testing.T) {
		doc := js.Global().Get("document")
		count := func() int { return doc.Get("body").Get("children").Length() }
		errs := spyConsole()
		defer errs.stop()
		if err := d.Run(context.Background(), func() {}); err != nil {
			t.Fatal(err)
		}
		if len(d.listening) != 0 || !d.plain.Get("isConnected").IsUndefined() && d.plain.Get("isConnected").Bool() {
			t.Errorf("%d listeners left, text mirror on the page: %v", len(d.listening), d.plain.Get("isConnected"))
		}
		// What the page sends now reaches nothing, and logs nothing.
		pointer(d, "pointerdown", 1, 40, 40)
		pointer(d, "pointerup", 1, 40, 40)
		key(d.canvas, "g", "KeyG")
		js.Global().Call("dispatchEvent", js.Global().Get("Event").New("resize"))
		doc.Call("dispatchEvent", js.Global().Get("Event").New("visibilitychange"))
		time.Sleep(50 * time.Millisecond)
		if got := errs.stop(); len(got) != 0 {
			t.Errorf("the page logged %q", got)
		}
		// A program can open it again, and each event is heard once.
		before := count()
		again, err := Open()
		if err != nil || again == d {
			t.Fatalf("opened again: %v, the same: %v", err, again == d)
		}
		raw, err := again.NewWindow(driver.Options{})
		if err != nil {
			t.Fatal(err)
		}
		next := raw.(*Window)
		drain(next)
		pointer(again, "pointerdown", 1, 40, 40)
		downs := 0
		for _, e := range drain(next) {
			if _, ok := e.(input.PointerDown); ok {
				downs++
			}
		}
		if downs != 1 {
			t.Errorf("a press came %d times", downs)
		}
		pointer(again, "pointerup", 1, 40, 40)
		_ = next.Close()
		_ = again.Run(context.Background(), func() {})
		if got := count(); got != before {
			t.Errorf("the page holds %d elements, from %d", got, before)
		}
	})
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

// define sets a property of a browser object, and returns what puts it back.
func define(target js.Value, name string, value js.Value) (restore func()) {
	obj := js.Global().Get("Object")
	prior := obj.Call("getOwnPropertyDescriptor", target, name)
	obj.Call("defineProperty", target, name, map[string]any{"configurable": true, "writable": true, "value": value})
	return func() {
		if prior.IsUndefined() {
			js.Global().Get("Reflect").Call("deleteProperty", target, name)
		} else {
			obj.Call("defineProperty", target, name, prior)
		}
	}
}

// consoleSpy keeps what the page logs as errors, as syscall/js does for a call to a
// released function.
type consoleSpy struct {
	got     []string
	spy     js.Func
	restore func()
}

func spyConsole() *consoleSpy {
	s := &consoleSpy{}
	s.spy = js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			s.got = append(s.got, js.Global().Call("String", a[0]).String())
		}
		return nil
	})
	s.restore = define(js.Global().Get("console"), "error", s.spy.Value)
	return s
}

// stop ends the spying, and returns what it kept. It can be called twice.
func (s *consoleSpy) stop() []string {
	if s.restore != nil {
		s.restore()
		s.restore = nil
		s.spy.Release()
	}
	return s.got
}

// fence marks the end of what drain waits for.
type fence struct{}

// drain returns the events pushed for w so far: its inbox keeps their order, so the
// fence pushed now comes after every one of them, however busy the machine is.
func drain(w *Window) []any {
	var out []any
	w.in.Push(fence{})
	for {
		select {
		case e, ok := <-w.Input():
			if _, end := e.(fence); end || !ok {
				return out
			}
			out = append(out, e)
		case <-time.After(30 * time.Second):
			panic("the inbox never fed its fence through")
		}
	}
}

// stamped gives a browser event the time it says it happened, in the browser's own
// clock, as an event a busy page runs late carries.
func stamped(e js.Value, ms float64) js.Value {
	js.Global().Get("Object").Call("defineProperty", e, "timeStamp", map[string]any{"value": ms})
	return e
}
