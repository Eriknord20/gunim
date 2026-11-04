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
		for i := 0; i < 24; i++ {
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
	w.Present(solid(red, w.Size()), []geom.Rect{paint.Everything})
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
		p.Present(solid(blue, p.Size()), []geom.Rect{paint.Everything})
		d.frame()
		a, b := pixel(5, 5), pixel(int(30*d.scale), int(30*d.scale))
		if a[0] < 240 || b[2] < 240 {
			t.Fatalf("base=%v popup=%v", a, b)
		}
		p.Hide()
		d.frame()
		if p := pixel(30, 30); p[0] < 240 {
			t.Fatalf("hide=%v", p)
		}
		p.Show()
		d.frame()
		if p := pixel(int(30*d.scale), int(30*d.scale)); p[2] < 240 {
			t.Fatalf("show=%v", p)
		}
		p.Close()
		d.frame()
		if e := d.canvas.Call("getContext", "webgl2").Call("getError").Int(); e != 0 {
			t.Fatalf("WebGL error=%x", e)
		}
	})
	t.Run("screenshot orientation", func(t *testing.T) {
		got := make(chan *image.RGBA, 1)
		w.Shoot(func(img *image.RGBA) { got <- img })
		d.frame()
		select {
		case img := <-got:
			if img.RGBAAt(4, 4).R < 240 {
				t.Fatal("screenshot missing pixels")
			}
		case <-time.After(time.Second):
			t.Fatal("screenshot timeout")
		}
		img := upright([]byte{1, 0, 0, 255, 2, 0, 0, 255}, 1, 2)
		if img.RGBAAt(0, 0).R != 2 {
			t.Fatal("screenshot inverted")
		}
	})
	t.Run("deferred sharpening", func(t *testing.T) {
		for len(w.presented) > 0 {
			<-w.presented
		}
		at := time.Now().Add(50 * time.Millisecond)
		d.sharpenAt(w, at)
		if w.sharpTimer == nil || !w.sharpAt.Equal(at) {
			t.Fatal("sharpen timer was not armed")
		}
		select {
		case <-w.presented:
		case <-time.After(2 * time.Second):
			t.Fatal("sharpen did not present a frame")
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
		w.Present(solid(blue, w.Size()), []geom.Rect{paint.Everything})
		d.frame()
		if p := pixel(4, 4); p[2] < 240 {
			t.Fatalf("restored pixels=%v", p)
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
		defer w.TakeFingers(false)
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
	w.Close()
}

// pointer sends a touch's event to the canvas's parent, as the browser would.
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
