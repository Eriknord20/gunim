//go:build js && wasm

package web

import (
	"errors"
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// browserError is what the browser threw or rejected a promise with. Name is the
// kind, as AbortError for a share the user closed.
type browserError struct{ name, message string }

func (e *browserError) Error() string {
	switch {
	case e.name == "":
		return "web: " + e.message
	case e.message == "":
		return "web: " + e.name
	}
	return "web: " + e.name + ": " + e.message
}

// errorOf reads what the browser threw. It is usually an Error or a DOMException,
// but a promise can reject with anything.
func errorOf(v js.Value) *browserError {
	if v.Type() == js.TypeObject {
		name, message := v.Get("name"), v.Get("message")
		if name.Type() == js.TypeString || message.Type() == js.TypeString {
			e := &browserError{}
			if name.Type() == js.TypeString {
				e.name = name.String()
			}
			if message.Type() == js.TypeString {
				e.message = message.String()
			}
			return e
		}
	}
	return &browserError{message: js.Global().Call("String", v).String()}
}

// call calls a browser method, and returns what it throws as an error, where
// syscall/js would raise a panic that ends the program.
func call(target js.Value, method string, args ...any) (value js.Value, err error) {
	defer func() {
		switch r := recover().(type) {
		case nil:
		case js.Error:
			err = errorOf(r.Value)
		default:
			err = fmt.Errorf("web: %s: %v", method, r)
		}
	}()
	return target.Call(method, args...), nil
}

// errNoAnswer is the error from a browser that has not settled a promise in time.
var errNoAnswer = errors.New("web: the browser has not answered")

// await waits on a browser promise without blocking JavaScript's event loop. A
// limit above zero gives up after that long.
func await(p js.Value, limit time.Duration) (js.Value, error) {
	type answer struct {
		value js.Value
		err   error
	}
	done := make(chan answer, 1)
	var resolve, reject js.Func
	// They are let go as the promise settles, however long after giving up: the
	// browser may yet call one of them.
	settle := func(a answer) {
		done <- a
		resolve.Release()
		reject.Release()
	}
	resolve = js.FuncOf(func(_ js.Value, a []js.Value) any {
		v := js.Undefined()
		if len(a) > 0 {
			v = a[0]
		}
		settle(answer{value: v})
		return nil
	})
	reject = js.FuncOf(func(_ js.Value, a []js.Value) any {
		v := js.Undefined()
		if len(a) > 0 {
			v = a[0]
		}
		settle(answer{err: errorOf(v)})
		return nil
	})
	p.Call("then", resolve, reject)
	var giveUp <-chan time.Time
	if limit > 0 {
		t := time.NewTimer(limit)
		defer t.Stop()
		giveUp = t.C
	}
	select {
	case a := <-done:
		return a.value, a.err
	case <-giveUp:
		return js.Undefined(), errNoAnswer
	}
}

// clipboardWait is how long the clipboard may hold up its caller, which is the
// window's own goroutine: the window draws nothing meanwhile. A browser can ask the
// user first, with a permission prompt or Firefox's Paste button, and the user may
// never answer.
var clipboardWait = 3 * time.Second

func clipboardCall(method string, args ...any) (js.Value, error) {
	c := js.Global().Get("navigator").Get("clipboard")
	if c.IsUndefined() || c.Get(method).IsUndefined() {
		return js.Undefined(), errors.ErrUnsupported
	}
	p, err := call(c, method, args...)
	if err != nil {
		return js.Undefined(), err
	}
	return await(p, clipboardWait)
}

// Clipboard reads the system's clipboard, if the browser allows it. It waits
// clipboardWait at most for the browser's answer.
func (w *Window) Clipboard() (string, error) {
	v, err := clipboardCall("readText")
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// SetClipboard writes the system's clipboard, if the browser allows it. It waits
// clipboardWait at most for the browser's answer.
func (w *Window) SetClipboard(s string) error { _, err := clipboardCall("writeText", s); return err }

// Shoot has fn called with the next frame drawn.
func (w *Window) Shoot(fn func(*image.RGBA)) { w.d.mu.Lock(); w.shot = fn; w.d.mu.Unlock(); w.d.kick() }

// SetTitle sets the page's title, for a window that fills the canvas.
func (w *Window) SetTitle(title string) {
	if w.fills {
		js.Global().Get("document").Set("title", title)
	}
}

// ShowKeyboard focuses what takes text, which brings up a phone's keyboard.
func (w *Window) ShowKeyboard() {
	w.d.syncText(w)
	w.d.text.Call("focus", map[string]any{"preventScroll": true})
}

// SetTextBox says where the text being edited is.
func (w *Window) SetTextBox(r geom.Rect) { w.d.mu.Lock(); w.textBox = r; w.d.mu.Unlock() }

// SafeArea returns what a phone's notch and corners take from the canvas.
func (w *Window) SafeArea() geom.Insets {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if !w.fills {
		return geom.Insets{}
	}
	return w.d.safe
}

// KeyboardCover returns how much of the canvas a phone's keyboard covers.
func (w *Window) KeyboardCover() float32 {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if !w.fills {
		return 0
	}
	return w.d.keyboard
}

// ContentOrigin returns where the window is on the canvas.
func (w *Window) ContentOrigin() (image.Point, error) {
	w.d.mu.Lock()
	at, scale := w.rectLocked().Min, w.d.scale
	w.d.mu.Unlock()
	r := w.d.canvas.Call("getBoundingClientRect")
	return image.Pt(int(math.Round((float64(at.X)+r.Get("left").Float())*float64(scale))), int(math.Round((float64(at.Y)+r.Get("top").Float())*float64(scale)))), nil
}

// SetTextCaret puts what takes text at the caret, so input methods open beside it.
func (w *Window) SetTextCaret(r geom.Rect) {
	w.d.mu.Lock()
	at := w.rectLocked().Min
	w.d.mu.Unlock()
	cr := w.d.canvas.Call("getBoundingClientRect")
	style := w.d.text.Get("style")
	style.Set("left", strconv.FormatFloat(cr.Get("left").Float()+float64(r.Min.X+at.X), 'f', -1, 64)+"px")
	style.Set("top", strconv.FormatFloat(cr.Get("top").Float()+float64(r.Min.Y+at.Y), 'f', -1, 64)+"px")
}
func (d *Driver) viewportChanged() {
	win := js.Global()
	height := win.Get("innerHeight").Float()
	vv := win.Get("visualViewport")
	bottom := height
	if !vv.IsUndefined() && !vv.IsNull() {
		if math.Abs(vv.Get("scale").Float()-1) < .05 {
			bottom = vv.Get("offsetTop").Float() + vv.Get("height").Float()
		}
	}
	// The keyboard covers the canvas only where the canvas reaches down to it, and
	// a canvas in the middle of a page has its own bottom edge.
	edge := d.canvas.Call("getBoundingClientRect").Get("bottom").Float()
	d.mu.Lock()
	if d.typing == nil || d.viewportHeight == 0 {
		d.viewportHeight = height
	}
	var cover float32
	if d.viewportHeight-bottom > 0 {
		cover = float32(max(0, edge-bottom))
	}
	d.mu.Unlock()
	style := win.Call("getComputedStyle", d.safeProbe)
	read := func(name string) float32 {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(style.Get(name).String(), "px"), 32)
		return float32(v)
	}
	safe := geom.Insets{Top: read("paddingTop"), Right: read("paddingRight"), Bottom: read("paddingBottom"), Left: read("paddingLeft")}
	d.mu.Lock()
	changed := safe != d.safe || cover != d.keyboard
	d.safe, d.keyboard = safe, cover
	ws := append([]*Window(nil), d.windows...)
	typing := d.typing
	d.mu.Unlock()
	if changed {
		for _, w := range ws {
			w.in.Push(driver.Redraw{})
		}
	}
	if cover > 0 && typing != nil {
		d.mu.Lock()
		box := typing.textBox.Add(typing.rectLocked().Min)
		d.mu.Unlock()
		cr := d.canvas.Call("getBoundingClientRect")
		if delta := cr.Get("top").Float() + float64(box.Max.Y) - bottom + 8; delta > 0 {
			win.Call("scrollBy", 0, delta)
		}
	}
	d.resized()
}
func (d *Driver) listenViewport(doc js.Value) {
	probe := doc.Call("createElement", "div")
	probe.Get("style").Set("cssText", "position:fixed;visibility:hidden;pointer-events:none;padding:env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left)")
	doc.Get("body").Call("appendChild", probe)
	d.safeProbe = probe
	vv := js.Global().Get("visualViewport")
	if !vv.IsUndefined() && !vv.IsNull() {
		d.on(vv, "resize", true, func(js.Value) { d.viewportChanged() })
		d.on(vv, "scroll", true, func(js.Value) { d.viewportChanged() })
	}
	d.on(js.Global(), "orientationchange", true, func(js.Value) { d.mu.Lock(); d.viewportHeight = 0; d.mu.Unlock(); d.viewportChanged() })
	d.on(js.Global(), "resize", true, func(js.Value) { d.viewportChanged() })
	// The page scrolling moves the canvas against the keyboard.
	d.on(js.Global(), "scroll", true, func(js.Value) { d.viewportChanged() })
	d.viewportChanged()
}

var (
	_ driver.TextStater      = (*Window)(nil)
	_ driver.TextBoxPlacer   = (*Window)(nil)
	_ driver.KeyboardShower  = (*Window)(nil)
	_ driver.SafeAreaer      = (*Window)(nil)
	_ driver.KeyboardCoverer = (*Window)(nil)
	_ driver.Shooter         = (*Window)(nil)
	_ driver.Positioner      = (*Window)(nil)
	_ driver.Titler          = (*Window)(nil)
)
