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

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
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
			// The system's own on a Mac, and the browser's settings.
			{"h", "KeyH", []string{cmd}, false},
			{"m", "KeyM", []string{cmd}, false},
			{"`", "Backquote", []string{cmd}, false},
			{"~", "Backquote", []string{cmd, shift}, false},
			{"<", "IntlBackslash", []string{cmd}, false},
			{",", "Comma", []string{cmd}, false},
			{"h", "KeyH", []string{cmd, alt}, false},
			{"m", "KeyM", []string{cmd, alt}, false},
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
			// The same with Shift: a shortcut of its own, and the program's.
			{"H", "KeyH", []string{cmd, shift}, true},
			{"M", "KeyM", []string{cmd, shift}, true},
			{"<", "Comma", []string{cmd, shift}, true},
			{"Р", "KeyH", []string{cmd, shift}, true},
			// The same letters as above, with Control: those are the program's.
			{"h", "KeyH", []string{ctrl}, true},
			{"m", "KeyM", []string{ctrl}, true},
			{",", "Comma", []string{ctrl}, true},
			{"`", "Backquote", []string{ctrl}, true},
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
	t.Run("the system's Command keys with Option and Shift", func(t *testing.T) {
		super, option, shift := input.ModSuper, input.ModAlt, input.ModShift
		for _, c := range []struct {
			name, code string
			mods       input.Mods
			kept       bool
		}{
			{"h", "KeyH", super | option, true},
			{"m", "KeyM", super | option, true},
			{"˙", "KeyH", super | option, true},
			{"H", "KeyH", super | option | shift, false},
			{"M", "KeyM", super | option | shift, false},
			// Moving between windows backward is the system's too.
			{"~", "Backquote", super | shift, true},
			{">", "IntlBackslash", super | shift, true},
		} {
			if got := browserKey(c.name, c.code, c.mods); got != c.kept {
				t.Errorf("%q on %s with %v: kept %v, want %v", c.name, c.code, c.mods, got, c.kept)
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
		if !key(d.text, "Insert", "Insert", "ctrlKey") || len(heard()) != 1 {
			t.Error("Control+Insert with no text state stayed the browser's")
		}
		if !key(d.text, "Delete", "Delete", "shiftKey") || len(heard()) != 1 {
			t.Error("Shift+Delete with no text state stayed the browser's")
		}
		if key(d.text, "Insert", "Insert", "shiftKey") || len(heard()) != 0 {
			t.Error("Shift+Insert with no text state left the browser, which brings the text")
		}
		w.SetTextState(&input.TextState{Text: "abc"}, w.editSeq)
		drain(w)
		for _, c := range [][2]string{{"c", "KeyC"}, {"x", "KeyX"}, {"v", "KeyV"}} {
			if key(d.text, c[0], c[1], "ctrlKey") || len(heard()) != 0 {
				t.Errorf("%s in a field left the browser", c[0])
			}
		}
		w.SetTextState(&input.TextState{Text: "abc", Selection: [2]int{0, 3}}, w.editSeq)
		drain(w)
		for _, c := range []struct{ name, mod string }{{"Insert", "ctrlKey"}, {"Insert", "shiftKey"}, {"Delete", "shiftKey"}} {
			if key(d.text, c.name, c.name, c.mod) || len(heard()) != 0 {
				t.Errorf("%s with %s in a field left the browser", c.name, c.mod)
			}
		}
		// Those alone stay gunim's.
		if !key(d.text, "Delete", "Delete") || len(heard()) != 1 {
			t.Error("Delete in a field left gunim")
		}
		// Shift+Delete cuts only what the browser can cut: a selection in a plain field.
		// The browser would do nothing with the others.
		w.SetTextState(&input.TextState{Text: "abc", Selection: [2]int{1, 1}}, w.editSeq)
		drain(w)
		if !key(d.text, "Delete", "Delete", "shiftKey") || len(heard()) != 1 {
			t.Error("Shift+Delete with nothing selected was left to the browser")
		}
		w.SetTextState(&input.TextState{Text: "abc", Selection: [2]int{0, 3}, Secret: true}, w.editSeq)
		drain(w)
		if !key(d.text, "Delete", "Delete", "shiftKey") || len(heard()) != 1 {
			t.Error("Shift+Delete in a password field was left to the browser")
		}
		w.SetTextState(&input.TextState{Text: "abc", Selection: [2]int{3, 0}}, w.editSeq)
		drain(w)
		if key(d.text, "Delete", "Delete", "shiftKey") || len(heard()) != 0 {
			t.Error("Shift+Delete on a backward selection was not left to the browser")
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
	t.Run("Control and Alt with a key's own character type nothing", func(t *testing.T) {
		d.canvas.Call("focus")
		drain(w)
		for _, c := range []struct {
			name, code string
			mods       []string
			altGr      bool
		}{
			{"1", "Digit1", []string{"ctrlKey", "altKey"}, false},
			{"!", "Digit1", []string{"ctrlKey", "altKey", "shiftKey"}, false},
			{"-", "Minus", []string{"ctrlKey", "altKey"}, false},
			{",", "Comma", []string{"ctrlKey", "altKey"}, false},
			{"1", "Numpad1", []string{"ctrlKey", "altKey"}, false},
			// What AltGr types on other layouts, from a browser that doesn't say it's AltGr.
			{"@", "Digit2", []string{"ctrlKey", "altKey"}, true},
			{"{", "Digit7", []string{"ctrlKey", "altKey"}, true},
			{"€", "KeyE", []string{"ctrlKey", "altKey"}, true},
			// The same character, from a browser that says.
			{"1", "Digit1", []string{"ctrlKey", "altKey", "modifierAltGraph"}, true},
		} {
			key(d.canvas, c.name, c.code, c.mods...)
			var press []input.KeyPress
			var typed []string
			for _, e := range drain(w) {
				switch e := e.(type) {
				case input.KeyPress:
					press = append(press, e)
				case input.TextInput:
					typed = append(typed, e.Text)
				}
			}
			if len(press) != 1 {
				t.Errorf("%q on %s reached gunim as %v", c.name, c.code, press)
				continue
			}
			if c.altGr {
				if !press[0].Typed || press[0].Mods&(input.ModControl|input.ModAlt) != 0 || !slices.Equal(typed, []string{c.name}) {
					t.Errorf("AltGr %q on %s: %+v typed %q", c.name, c.code, press[0], typed)
				}
			} else if press[0].Typed || press[0].Mods&input.ModControl == 0 || press[0].Mods&input.ModAlt == 0 || len(typed) != 0 {
				t.Errorf("%q on %s with Control and Alt: %+v typed %q", c.name, c.code, press[0], typed)
			}
		}
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
	t.Run("a key after a tap brings the keyboard back to the field", func(t *testing.T) {
		doc := js.Global().Get("document")
		tap := func() {
			pointer(d, "pointerdown", 7, 5, 5)
			pointer(d, "pointerup", 7, 5, 5)
			drain(w)
		}
		d.canvas.Call("focus")
		tap()
		// Tab, on the canvas, moves the engine's focus to a field: a physical key
		// means no finger's tap is needed for it to take the keyboard.
		if !key(d.canvas, "Tab", "Tab") {
			t.Error("Tab on the canvas was left to the browser")
		}
		w.SetTextInput(true)
		if !d.text.Equal(doc.Get("activeElement")) {
			t.Errorf("after a tap and Tab, %v has the keyboard", doc.Get("activeElement").Get("tagName"))
		}
		// A dead key now reaches the mirror, which is what finishes it.
		if key(d.text, "Dead", "BracketRight") {
			t.Error("a dead key in the field was cancelled")
		}
		w.SetTextInput(false)
		drain(w)
		// The field is on already, and the finger's tap left the keyboard away.
		d.canvas.Call("focus")
		tap()
		w.SetTextInput(true)
		if d.text.Equal(doc.Get("activeElement")) {
			t.Fatal("a finger's field took the keyboard")
		}
		key(d.canvas, "Dead", "BracketRight")
		if !d.text.Equal(doc.Get("activeElement")) {
			t.Errorf("a dead key on the canvas left %v with the keyboard", doc.Get("activeElement").Get("tagName"))
		}
		w.SetTextInput(false)
		drain(w)
		// A soft keyboard's key has no place, and says nothing of a keyboard at hand.
		tap()
		d.text.Call("focus")
		key(d.text, "Backspace", "")
		d.canvas.Call("focus")
		w.SetTextInput(true)
		if d.text.Equal(doc.Get("activeElement")) {
			t.Error("a soft keyboard's key let a later field bring the keyboard up with no tap")
		}
		w.SetTextInput(false)
		d.mu.Lock()
		d.lastTouch = false
		d.mu.Unlock()
		drain(w)
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
		// What has no text to follow with stays the browser's.
		key(d.text, "Backspace", "Backspace")
		e := js.Global().Get("InputEvent").New("beforeinput", map[string]any{"bubbles": true, "cancelable": true, "inputType": "insertFromPaste"})
		if !d.text.Call("dispatchEvent", e).Bool() {
			t.Error("a paste with no data to queue was cancelled")
		}
		drain(w)
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
	t.Run("a second finger leaves a mouse's press alone", func(t *testing.T) {
		drain(w)
		mouse("pointerdown", 0, 1)
		pointer(d, "pointerdown", 2, 200, 200)
		pointer(d, "pointermove", 2, 210, 210)
		pointer(d, "pointerup", 2, 210, 210)
		mouse("pointerup", 0, 0)
		var downs, ups int
		for _, e := range drain(w) {
			switch e := e.(type) {
			case input.PointerDown:
				downs++
			case input.PointerUp:
				ups++
				if e.Pos == input.Away {
					t.Error("the finger called off the mouse's press")
				}
			case input.PointerMove:
				if e.Pos == geom.Pt(210, 210) {
					t.Error("the finger moved the pointer")
				}
			case input.Pinch:
				t.Errorf("a mouse and a finger pinched: %+v", e)
			}
		}
		if downs != 1 || ups != 1 {
			t.Fatalf("downs=%d ups=%d", downs, ups)
		}
	})
	// pinches lists what a touch's events came to, as words.
	pinches := func() (got []string) {
		for _, e := range drain(w) {
			switch e := e.(type) {
			case input.PointerDown:
				got = append(got, "down")
			case input.PointerUp:
				if e.Pos == input.Away {
					got = append(got, "called off")
				} else {
					got = append(got, "up")
				}
			case input.PointerLeave:
				got = append(got, "leave")
			case input.Pinch:
				got = append(got, fmt.Sprintf("pinch %d at %v by %v scale %.2f", e.Phase, e.Pos, e.Delta, e.Scale))
			}
		}
		return got
	}
	t.Run("two fingers pinch, as on Android", func(t *testing.T) {
		drain(w)
		start, move, end := input.PinchStart, input.PinchMove, input.PinchEnd
		pointer(d, "pointerdown", 1, 40, 100)
		pointer(d, "pointermove", 1, 40, 100)
		if got := pinches(); !slices.Equal(got, []string{"down"}) {
			t.Fatalf("one finger came to %q", got)
		}
		// The second calls off the first's press, and the pinch starts between them.
		pointer(d, "pointerdown", 2, 80, 100)
		want := []string{"called off", fmt.Sprintf("pinch %d at %v by %v scale 1.00", start, geom.Pt(60, 100), geom.Point{})}
		if got := pinches(); !slices.Equal(got, want) {
			t.Fatalf("the second finger came to %q, want %q", got, want)
		}
		// The second moves out: twice as far apart, and the middle moves half as far.
		pointer(d, "pointermove", 2, 120, 100)
		want = []string{fmt.Sprintf("pinch %d at %v by %v scale 2.00", move, geom.Pt(80, 100), geom.Pt(20, 0))}
		if got := pinches(); !slices.Equal(got, want) {
			t.Fatalf("the move came to %q, want %q", got, want)
		}
		// A third finger is nothing.
		pointer(d, "pointerdown", 3, 200, 200)
		pointer(d, "pointermove", 3, 210, 210)
		pointer(d, "pointerup", 3, 210, 210)
		if got := pinches(); len(got) != 0 {
			t.Fatalf("a third finger came to %q", got)
		}
		// The first moves in: the fingers come back to half as far apart.
		pointer(d, "pointermove", 1, 80, 100)
		want = []string{fmt.Sprintf("pinch %d at %v by %v scale 0.50", move, geom.Pt(100, 100), geom.Pt(20, 0))}
		if got := pinches(); !slices.Equal(got, want) {
			t.Fatalf("the first's move came to %q, want %q", got, want)
		}
		// The second lifts, which ends it. The first only lifts after.
		pointer(d, "pointerup", 2, 120, 100)
		want = []string{fmt.Sprintf("pinch %d at %v by %v scale 1.00", end, geom.Pt(100, 100), geom.Point{})}
		if got := pinches(); !slices.Equal(got, want) {
			t.Fatalf("the lift came to %q, want %q", got, want)
		}
		pointer(d, "pointermove", 1, 90, 100)
		pointer(d, "pointerup", 1, 90, 100)
		if got := pinches(); !slices.Equal(got, []string{"leave"}) {
			t.Fatalf("the first finger left came to %q", got)
		}
	})
	t.Run("a pinch ends when either finger lifts, and can start again", func(t *testing.T) {
		drain(w)
		start, end := input.PinchStart, input.PinchEnd
		first := func(got []string) string {
			if len(got) == 0 {
				return ""
			}
			return got[len(got)-1]
		}
		// The first lifts first: the second, left down, does nothing more.
		pointer(d, "pointerdown", 1, 40, 100)
		pointer(d, "pointerdown", 2, 80, 100)
		drain(w)
		pointer(d, "pointerup", 1, 40, 100)
		want := []string{fmt.Sprintf("pinch %d at %v by %v scale 1.00", end, geom.Pt(60, 100), geom.Point{}), "leave"}
		if got := pinches(); !slices.Equal(got, want) {
			t.Fatalf("the first finger's lift came to %q, want %q", got, want)
		}
		pointer(d, "pointermove", 2, 90, 100)
		pointer(d, "pointerup", 2, 90, 100)
		if got := pinches(); len(got) != 0 {
			t.Fatalf("the finger left over came to %q", got)
		}
		// A new second finger while the first stays down pinches again, with no second
		// press to call off.
		pointer(d, "pointerdown", 1, 40, 100)
		pointer(d, "pointerdown", 2, 80, 100)
		pointer(d, "pointerup", 2, 80, 100)
		drain(w)
		pointer(d, "pointerdown", 4, 120, 100)
		got := pinches()
		if want := fmt.Sprintf("pinch %d at %v by %v scale 1.00", start, geom.Pt(80, 100), geom.Point{}); len(got) != 1 || first(got) != want {
			t.Fatalf("the new finger came to %q, want %q", got, want)
		}
		// A cancel ends it too.
		pointer(d, "pointercancel", 4, 120, 100)
		pointer(d, "pointercancel", 1, 40, 100)
		want = []string{fmt.Sprintf("pinch %d at %v by %v scale 1.00", end, geom.Pt(80, 100), geom.Point{}), "leave"}
		if got := pinches(); !slices.Equal(got, want) {
			t.Fatalf("the cancels came to %q, want %q", got, want)
		}
		// And the next touch is a press again.
		pointer(d, "pointerdown", 6, 50, 50)
		pointer(d, "pointerup", 6, 50, 50)
		if got := pinches(); !slices.Equal(got, []string{"down", "up", "leave"}) {
			t.Fatalf("the next touch came to %q", got)
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
		for _, bad := range []string{"http://[bad", "javascript:alert(1)", "JavaScript:alert(1)", "data:text/html,hi"} {
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
		// Whatever else a link on the page can hold opens too.
		for _, good := range []string{"mailto:a@example.com", "tel:+4612345", "/docs/page"} {
			opened = nil
			if err := w.OpenLink(good); err != nil || !slices.Equal(opened, []string{good}) {
				t.Errorf("%q: %v, opened %q", good, err, opened)
			}
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
	t.Run("sound opens before the first gesture", func(t *testing.T) {
		// A browser holds sound back until a click, a key or a touch. Open can't wait
		// for that: what the first click plays is set up after it.
		opened := make(chan error, 1)
		go func() {
			_, err := speaker.Open(audio.NewMixer(), speaker.Options{Name: "gunim test"})
			opened <- err
		}()
		select {
		case err := <-opened:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("speaker.Open waits for a gesture nothing here will make")
		}
	})
	t.Run("the user's scroll stays with the keyboard up, and a covered field comes into view", func(t *testing.T) {
		win := js.Global()
		doc := win.Get("document")
		spacer := doc.Call("createElement", "div")
		spacer.Get("style").Set("height", "5000px")
		doc.Get("body").Call("appendChild", spacer)
		defer func() {
			spacer.Call("remove")
			win.Call("scrollTo", 0, 0)
			d.mu.Lock()
			d.typing = nil
			w.textBox = geom.Rect{}
			d.mu.Unlock()
			d.viewportChanged()
		}()
		defer define(win, "visualViewport", js.Undefined())()
		win.Call("scrollTo", 0, 0)
		d.mu.Lock()
		d.typing = w
		w.textBox = geom.Rect{Min: geom.Pt(0, 200), Max: geom.Pt(100, 230)}
		d.mu.Unlock()
		// A keyboard over the canvas's last 40 px, and so over the field.
		edge := d.canvas.Call("getBoundingClientRect").Get("bottom").Float()
		define(win, "visualViewport", js.ValueOf(map[string]any{"scale": 1, "offsetTop": 0, "height": edge - 40}))
		win.Call("dispatchEvent", win.Get("Event").New("scroll"))
		if got := win.Get("scrollY").Float(); got != 0 {
			t.Errorf("a scroll by the user moved the page on to %v", got)
		}
		if w.KeyboardCover() == 0 {
			t.Error("a scroll doesn't take in the keyboard's cover")
		}
		d.viewportChanged()
		if got := win.Get("scrollY").Float(); got == 0 {
			t.Error("the keyboard opening left the field covered")
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
		// Asking listens for a first reading, and one with no alpha isn't that.
		if w.HasCompass() {
			t.Fatal("a compass before any reading")
		}
		if !d.compassOn {
			t.Fatal("asking for a compass doesn't listen for one")
		}
		send(js.Null())
		if !d.compassOn || w.HasCompass() {
			t.Fatalf("a reading of nothing: listening %v, compass %v", d.compassOn, d.compass)
		}
		// A first reading comes however late it is. It ends the listening and asks for
		// a frame, after which the engine starts a window's watch.
		drain(w)
		send(30)
		if !w.HasCompass() {
			t.Fatal("no compass after a first reading")
		}
		if d.compassOn {
			t.Fatal("still listening after a first reading, with nothing watching")
		}
		if !slices.ContainsFunc(drain(w), func(x any) bool { _, ok := x.(driver.Redraw); return ok }) {
			t.Fatal("a first reading asks for no frame")
		}
		// Watching listens, and stops with the last window that watches.
		w.WatchHeading(true)
		drain(w)
		send(30)
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
