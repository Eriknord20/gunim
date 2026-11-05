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

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// await waits on a browser promise without blocking JavaScript's event loop.
func await(p js.Value) (value js.Value, err error) {
	done := make(chan struct{}, 1)
	resolve := js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			value = a[0]
		}
		done <- struct{}{}
		return nil
	})
	reject := js.FuncOf(func(_ js.Value, a []js.Value) any {
		err = errors.New("web: browser operation rejected")
		if len(a) > 0 {
			err = fmt.Errorf("web: %s", a[0].String())
		}
		done <- struct{}{}
		return nil
	})
	defer resolve.Release()
	defer reject.Release()
	p.Call("then", resolve, reject)
	<-done
	return value, err
}
func clipboardCall(method string, args ...any) (value js.Value, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("web clipboard: %v", v)
		}
	}()
	c := js.Global().Get("navigator").Get("clipboard")
	if c.IsUndefined() || c.Get(method).IsUndefined() {
		return js.Undefined(), errors.ErrUnsupported
	}
	return await(c.Call(method, args...))
}

// Clipboard reads the system's clipboard, if the browser allows it.
func (w *Window) Clipboard() (string, error) {
	v, err := clipboardCall("readText")
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// SetClipboard writes the system's clipboard, if the browser allows it.
func (w *Window) SetClipboard(s string) error { _, err := clipboardCall("writeText", s); return err }

// Shoot has fn called with the next frame drawn.
func (w *Window) Shoot(fn func(*image.RGBA)) { w.d.mu.Lock(); w.shot = fn; w.d.mu.Unlock(); w.d.kick() }

// keepShot puts back a shot whose frame could not draw, for a later frame, unless
// a newer shot has taken its place.
func (w *Window) keepShot(fn func(*image.RGBA)) {
	w.d.mu.Lock()
	if w.shot == nil {
		w.shot = fn
	}
	w.d.mu.Unlock()
}

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
	d.mu.Lock()
	if d.typing == nil || d.viewportHeight == 0 {
		d.viewportHeight = height
	}
	cover := float32(max(0, d.viewportHeight-bottom))
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
