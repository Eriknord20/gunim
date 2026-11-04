//go:build js && wasm

// Package web is gunim's driver for a browser, with the program built
// for GOOS=js GOARCH=wasm.
//
// The page holds one canvas, by default the element with id "gunim",
// or a new one filling the page. Every gunim window draws into it with
// WebGL2, which is OpenGL ES 3.0, through the same renderer the other
// drivers use. As on Android, the first window fills the canvas and
// popups lie over it, each drawn into a texture of its own and laid on
// the canvas bottom first.
//
// The canvas's CSS size is the windows' logical size, and the page's
// devicePixelRatio their scale, so a page lays the canvas out as it
// likes and gunim follows it. Frames are drawn in requestAnimationFrame,
// and only while a window has a frame to show.
//
// The browser's own pointer, keyboard and focus events become gunim's
// input. Text comes through a hidden textarea, focused while a window
// takes text, so a phone's soft keyboard and input methods work.
//
// Gunim takes every touch on the canvas, and scrolls and pans for itself. A page
// that wants the browser to have some of them, as to scroll the page with
// vertical swipes, sets touch-action in the canvas's own style attribute, and
// that is kept. Elements a page lays over the canvas keep their own events.
package web

import (
	"context"
	"errors"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"
	"unicode"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/internal/gl"
)

// Driver is the browser implementation of [driver.Driver].
type Driver struct {
	mu sync.Mutex

	canvas, text js.Value
	g            gl.Context
	shared       render.Shared
	comp         *compositor

	// scale is the page's devicePixelRatio, and cssW and cssH the
	// canvas's size in CSS pixels, which are logical pixels. surfW and
	// surfH are its drawing buffer's size in device pixels.
	scale        float32
	cssW, cssH   float32
	surfW, surfH int
	// exactW and exactH are the canvas's size in device pixels, from a browser
	// that says.
	exactW, exactH int

	// windows is the stack of open windows, bottom first.
	windows []*Window
	// pressed is the window a press started in, which keeps the
	// pointer until it lifts, and hover the window a hovering mouse is
	// over.
	pressed, hover *Window
	// pointer is the id of the pointer pressing: one drives the pointer at
	// a time, as gunim has one, and another finger is its own or nothing.
	// fingers is the window each finger of its own came down in, by id.
	// touchAction is what the page asked of the browser for touches.
	touchAction string
	pointer     int
	fingers map[int]*Window
	// compass says the browser has sent a heading, and headingTo holds the
	// windows that watch it.
	compass   bool
	headingTo map[*Window]bool
	lastTap   geom.Point
	lastAt    time.Time
	clicks    int
	// typing is the window that takes text.
	typing                 *Window
	composing, syncingText bool
	compStart              int
	compSuffix, clipboard  string
	mods                   input.Mods

	safeProbe      js.Value
	safe           geom.Insets
	keyboard       float32
	viewportHeight float64
	lost           bool
	rate           float64
	lastRAF        float64
	samples        []float64
	stats          frameStats
	frameAsked     bool
	raf            js.Func
	funcs          []js.Func
	quit           chan struct{}
}

var (
	theDriver *Driver
	openErr   error
	openOnce  sync.Once
)

// Open returns the driver, built on the page's canvas the first time.
func Open() (*Driver, error) {
	openOnce.Do(func() { theDriver, openErr = open() })
	return theDriver, openErr
}

func open() (*Driver, error) {
	doc := js.Global().Get("document")
	if doc.IsUndefined() {
		return nil, errors.New("web: no document")
	}
	canvas := doc.Call("getElementById", "gunim")
	if canvas.IsNull() {
		canvas = doc.Call("createElement", "canvas")
		canvas.Set("id", "gunim")
		s := canvas.Get("style")
		s.Set("display", "block")
		s.Set("width", "100vw")
		s.Set("height", "100vh")
		doc.Get("body").Call("appendChild", canvas)
	}
	pinAutoSize(canvas)
	opts := js.ValueOf(map[string]any{
		"alpha": false, "antialias": false, "depth": false, "stencil": false,
		"premultipliedAlpha": true, "preserveDrawingBuffer": false, "powerPreference": "high-performance",
	})
	ctx := canvas.Call("getContext", "webgl2", opts)
	g, err := gl.NewWebGLContext(ctx)
	if err != nil {
		return nil, err
	}
	comp, err := newCompositor(g)
	if err != nil {
		return nil, err
	}
	d := &Driver{canvas: canvas, g: g, comp: comp, quit: make(chan struct{}), rate: 60, fingers: map[int]*Window{}, headingTo: map[*Window]bool{}}
	d.measure()
	d.raf = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer survive("animation frame")
		if len(args) > 0 {
			d.observeRefresh(args[0].Float())
		}
		d.mu.Lock()
		d.frameAsked = false
		d.mu.Unlock()
		d.frame()
		return nil
	})
	d.listen(doc)
	d.listenViewport(doc)
	d.listenContext()
	d.calibrateRefresh()
	return d, nil
}

// pinAutoSize fixes a side of the canvas that takes its size from its drawing
// buffer, as one with no CSS size does. Otherwise sizing the buffer to match
// the canvas would grow the canvas, without end at a scale above 1.
func pinAutoSize(c js.Value) {
	w, h := c.Get("clientWidth").Int(), c.Get("clientHeight").Int()
	c.Set("width", w*2+7)
	c.Set("height", h*2+7)
	if c.Get("clientWidth").Int() != w {
		c.Get("style").Set("width", strconv.Itoa(w)+"px")
	}
	if c.Get("clientHeight").Int() != h {
		c.Get("style").Set("height", strconv.Itoa(h)+"px")
	}
}

// measure reads the canvas's size and the page's scale, and sizes the
// drawing buffer to match. It reports whether anything changed.
func (d *Driver) measure() bool {
	w := float32(d.canvas.Get("clientWidth").Float())
	h := float32(d.canvas.Get("clientHeight").Float())
	s := float32(js.Global().Get("devicePixelRatio").Float())
	if s <= 0 || math.IsNaN(float64(s)) {
		s = 1
	}
	pw, ph := int(math.Round(float64(w*s))), int(math.Round(float64(h*s)))
	d.mu.Lock()
	if d.exactW > 0 && d.exactH > 0 {
		// clientWidth rounds, so at a fractional scale the browser's own count
		// of device pixels is the one that fills the canvas exactly.
		pw, ph = d.exactW, d.exactH
	}
	changed := w != d.cssW || h != d.cssH || s != d.scale
	d.cssW, d.cssH, d.scale = w, h, s
	if pw != d.surfW || ph != d.surfH {
		d.surfW, d.surfH = pw, ph
		d.canvas.Set("width", pw)
		d.canvas.Set("height", ph)
		changed = true
	}
	d.mu.Unlock()
	return changed
}

// resized follows the canvas to its new size.
func (d *Driver) resized() {
	if !d.measure() {
		return
	}
	d.mu.Lock()
	var fill []*Window
	for _, w := range d.windows {
		if w.fills {
			fill = append(fill, w)
		}
	}
	d.mu.Unlock()
	for _, w := range fill {
		w.in.Push(driver.Redraw{})
	}
	d.kick()
}

// Run implements [driver.Driver]. The browser pumps the events, so Run
// waits for ctx to end or the last window to close.
func (d *Driver) Run(ctx context.Context, ready func()) error {
	ready()
	select {
	case <-ctx.Done():
	case <-d.quit:
	}
	return nil
}

// Monitors implements [driver.Driver]: the browser's viewport.
func (d *Driver) Monitors() []driver.Monitor {
	d.mu.Lock()
	defer d.mu.Unlock()
	win := js.Global()
	b := geom.Rect{Max: geom.Pt(float32(win.Get("innerWidth").Float())*d.scale, float32(win.Get("innerHeight").Float())*d.scale)}
	return []driver.Monitor{{
		Name: "browser", Bounds: b, WorkArea: b, RefreshRate: d.rate,
		Scale: d.scale, CoordsPerLogical: d.scale, Primary: true,
	}}
}

// NewWindow implements [driver.Driver]. The first window fills the
// canvas. A popup opens by its anchor, a utility window at its anchor,
// and any other window fills the canvas over the ones before it.
func (d *Driver) NewWindow(o driver.Options) (driver.Window, error) {
	w := newWindow(d, o)
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := o.Parent.(*Window); ok {
		w.parent = p
	}
	switch {
	case o.Kind == driver.KindPopup && w.parent != nil:
		w.size = o.Size
		d.placeLocked(w, o.Anchor)
	case o.Kind == driver.KindUtility && w.parent != nil:
		w.size = o.Size
		w.pos = o.Anchor.Min.Add(w.parent.pos)
	default:
		w.fills = true
	}
	w.hidden = o.Hidden
	d.windows = append(d.windows, w)
	// Other platforms show a new window and hear back that it needs drawing.
	// Here nothing would ask, and a Root with no commands queued stays blank.
	w.in.Push(driver.Redraw{})
	d.kickLocked()
	return w, nil
}

// screen returns the canvas in logical pixels. It runs with mu held.
func (d *Driver) screen() geom.Rect { return geom.Rect{Max: geom.Pt(d.cssW, d.cssH)} }

// placeLocked puts popup w by anchor, in its parent's logical space:
// below it, or above where there is more room, slid onto the canvas.
func (d *Driver) placeLocked(w *Window, anchor geom.Rect) {
	a := anchor.Add(w.parent.pos)
	if w.over {
		w.pos = a.Min
		return
	}
	s := d.screen()
	below, above := s.Max.Y-a.Max.Y, a.Min.Y-s.Min.Y
	y := a.Max.Y
	if w.above && above >= w.size.H || w.size.H > below && above > below {
		y = a.Min.Y - w.size.H
	}
	x := max(s.Min.X, min(a.Min.X, s.Max.X-w.size.W))
	w.pos = geom.Pt(x, y)
}

// kick asks the browser for an animation frame.
func (d *Driver) kick() {
	d.mu.Lock()
	d.kickLocked()
	d.mu.Unlock()
}

func (d *Driver) kickLocked() {
	if d.frameAsked {
		return
	}
	d.frameAsked = true
	js.Global().Call("requestAnimationFrame", d.raf)
}

// closed takes w out of the stack.
func (d *Driver) closed(w *Window) {
	d.mu.Lock()
	for i, x := range d.windows {
		if x == w {
			d.windows = append(d.windows[:i], d.windows[i+1:]...)
			break
		}
	}
	if d.pressed == w {
		d.pressed = nil
	}
	for id, x := range d.fingers {
		if x == w {
			delete(d.fingers, id)
		}
	}
	delete(d.headingTo, w)
	if d.hover == w {
		d.hover = nil
	}
	if d.typing == w {
		d.typing = nil
	}
	last := len(d.windows) == 0
	d.mu.Unlock()
	if last {
		select {
		case <-d.quit:
		default:
			close(d.quit)
		}
	}
	d.kick()
}

// hitLocked returns the topmost window under p that takes the pointer.
func (d *Driver) hitLocked(p geom.Point) *Window {
	for i := len(d.windows) - 1; i >= 0; i-- {
		w := d.windows[i]
		if w.hidden || w.passthrough || w.closed {
			continue
		}
		if w.rectLocked().Contains(p) {
			return w
		}
	}
	return nil
}

// on listens for a DOM event.
func (d *Driver) on(target js.Value, name string, passive bool, fn func(e js.Value)) {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer survive(name)
		fn(args[0])
		return nil
	})
	d.funcs = append(d.funcs, f)
	target.Call("addEventListener", name, f, js.ValueOf(map[string]any{"passive": passive}))
}

func modsOf(e js.Value) input.Mods {
	var m input.Mods
	if e.Get("shiftKey").Bool() {
		m |= input.ModShift
	}
	if e.Get("ctrlKey").Bool() {
		m |= input.ModControl
	}
	if e.Get("altKey").Bool() {
		m |= input.ModAlt
	}
	if e.Get("metaKey").Bool() {
		m |= input.ModSuper
	}
	return m
}

// touchOf reports whether a pointer event reaches gunim as a touch.
func (d *Driver) touchOf(e js.Value) bool {
	return e.Get("pointerType").String() != "mouse"
}

// at returns where a pointer event is on the canvas, in logical pixels.
func (d *Driver) at(e js.Value) geom.Point {
	r := d.canvas.Call("getBoundingClientRect")
	return geom.Pt(float32(e.Get("clientX").Float()-r.Get("left").Float()), float32(e.Get("clientY").Float()-r.Get("top").Float()))
}

func buttonOf(e js.Value) input.Button {
	switch e.Get("button").Int() {
	case 1:
		return input.ButtonMiddle
	case 2:
		return input.ButtonSecondary
	}
	return input.ButtonPrimary
}

const (
	doubleTapTime  = 400 * time.Millisecond
	doubleTapSpace = 24
)

func (d *Driver) listen(doc js.Value) {
	c := d.canvas
	d.touchAction = c.Get("style").Get("touchAction").String()
	if d.touchAction == "" {
		d.touchAction = "none"
	}
	c.Get("style").Set("touchAction", d.touchAction)
	c.Get("style").Set("userSelect", "none")
	c.Get("style").Set("webkitUserSelect", "none")
	c.Get("style").Set("webkitTapHighlightColor", "transparent")
	c.Set("tabIndex", 0)

	// The textarea that takes text, out of sight, but focusable, so a
	// phone shows its keyboard for it.
	t := doc.Call("createElement", "textarea")
	for k, v := range map[string]string{
		"position": "fixed", "left": "0", "top": "0", "width": "1px", "height": "1px",
		"opacity": "0", "border": "0", "padding": "0", "resize": "none", "pointerEvents": "none",
	} {
		t.Get("style").Set(k, v)
	}
	t.Call("setAttribute", "autocomplete", "off")
	t.Call("setAttribute", "autocapitalize", "none")
	t.Call("setAttribute", "spellcheck", "false")
	t.Call("setAttribute", "aria-hidden", "true")
	doc.Get("body").Call("appendChild", t)
	d.text = t

	d.on(c, "contextmenu", false, func(e js.Value) { e.Call("preventDefault") })
	d.on(c, "pointerdown", false, func(e js.Value) {
		p, now := d.at(e), time.Now()
		touch, id := d.touchOf(e), e.Get("pointerId").Int()
		d.mu.Lock()
		if d.pressed != nil && id != d.pointer {
			// A second finger: its own in a window that takes fingers, and
			// otherwise it leaves the press the first one holds alone.
			w := d.pressed
			if w.takesFingers {
				d.fingers[id] = w
			}
			d.mu.Unlock()
			e.Call("preventDefault")
			if w.takesFingers {
				capture(c, e.Get("pointerId"))
				d.fingerEvent(id, e, input.FingerDown)
			}
			return
		}
		w := d.hitLocked(p)
		if w == nil {
			d.mu.Unlock()
			return
		}
		d.pointer = id
		if w.takesFingers && e.Get("pointerType").String() != "mouse" {
			d.fingers[id] = w
		}
		if now.Sub(d.lastAt) < doubleTapTime && math.Hypot(float64(p.X-d.lastTap.X), float64(p.Y-d.lastTap.Y)) < doubleTapSpace {
			d.clicks++
		} else {
			d.clicks = 1
		}
		d.lastAt, d.lastTap = now, p
		d.pressed = w
		clicks, local := d.clicks, p.Sub(w.rectLocked().Min)
		d.mu.Unlock()
		// The browser's own focus change on the mouse events that follow
		// would undo a textarea focused for a text field: the driver
		// moves focus itself. A touch still scrolls the page, which
		// touch-action decides.
		e.Call("preventDefault")
		capture(c, e.Get("pointerId"))
		d.fingerEvent(id, e, input.FingerDown)
		// The canvas keeps the keyboard unless a window takes text;
		// a press that turns text on focuses the textarea after.
		if d.typingWindow() == nil {
			c.Call("focus", map[string]any{"preventScroll": true})
		}
		m := modsOf(e)
		w.in.Push(input.PointerMove{Pos: local, Mods: m, Touch: touch, Time: now})
		w.in.Push(input.PointerDown{Pos: local, Button: buttonOf(e), Mods: m, Clicks: clicks, Touch: touch, Time: now})
	})
	d.on(c, "pointermove", true, func(e js.Value) {
		p, now := d.at(e), time.Now()
		touch, finger, id := d.touchOf(e), e.Get("pointerType").String() != "mouse", e.Get("pointerId").Int()
		d.fingerEvent(id, e, input.FingerMove)
		d.mu.Lock()
		if d.pressed != nil && id != d.pointer {
			d.mu.Unlock()
			return
		}
		w := d.pressed
		var left *Window
		if w == nil {
			if finger {
				d.mu.Unlock()
				return
			}
			w = d.hitLocked(p)
			if d.hover != w {
				left, d.hover = d.hover, w
			}
		}
		var local geom.Point
		if w != nil {
			local = p.Sub(w.rectLocked().Min)
		}
		d.mu.Unlock()
		if left != nil {
			left.in.Push(input.PointerLeave{Time: now})
		}
		if w != nil {
			w.in.Push(input.PointerMove{Pos: local, Mods: modsOf(e), Touch: touch, Time: now})
		}
	})
	up := func(e js.Value, cancel bool) {
		if !cancel && e.Get("pointerType").String() == "mouse" {
			// Cancelling pointerdown, above, stops the browser's own mouseup,
			// and a page's audio may be waiting for one to start: Web Audio
			// needs a user's gesture, and oto listens on the document for it.
			doc.Call("dispatchEvent", js.Global().Get("MouseEvent").New("mouseup", map[string]any{"bubbles": true}))
		}
		p, now := d.at(e), time.Now()
		touch, finger, id := d.touchOf(e), e.Get("pointerType").String() != "mouse", e.Get("pointerId").Int()
		d.fingerEvent(id, e, input.FingerUp)
		d.mu.Lock()
		if d.pressed != nil && id != d.pointer {
			d.mu.Unlock()
			return
		}
		w := d.pressed
		d.pressed = nil
		var local geom.Point
		if w != nil {
			local = p.Sub(w.rectLocked().Min)
		}
		d.mu.Unlock()
		if w == nil {
			return
		}
		if cancel {
			w.in.Push(input.PointerUp{Pos: input.Away, Button: input.ButtonPrimary, Touch: touch, Time: now})
			w.in.Push(input.PointerLeave{Time: now})
			return
		}
		m := modsOf(e)
		w.in.Push(input.PointerMove{Pos: local, Mods: m, Touch: touch, Time: now})
		w.in.Push(input.PointerUp{Pos: local, Button: buttonOf(e), Mods: m, Touch: touch, Time: now})
		if finger {
			w.in.Push(input.PointerLeave{Time: now})
		}
	}
	d.on(c, "pointerup", true, func(e js.Value) { up(e, false) })
	d.on(c, "pointercancel", true, func(e js.Value) { up(e, true) })
	d.on(c, "pointerleave", true, func(e js.Value) {
		if e.Get("pointerType").String() != "mouse" {
			return
		}
		d.mu.Lock()
		w := d.hover
		if d.pressed == nil {
			d.hover = nil
		} else {
			w = nil
		}
		d.mu.Unlock()
		if w != nil {
			w.in.Push(input.PointerLeave{Time: time.Now()})
		}
	})
	d.on(c, "wheel", false, func(e js.Value) {
		p := d.at(e)
		d.mu.Lock()
		w := d.hitLocked(p)
		var local geom.Point
		if w != nil {
			local = p.Sub(w.rectLocked().Min)
		}
		d.mu.Unlock()
		if w == nil {
			return
		}
		dx, dy := e.Get("deltaX").Float(), e.Get("deltaY").Float()
		switch e.Get("deltaMode").Int() {
		case 1: // lines
			dx, dy = dx*40, dy*40
		case 2: // pages
			dx, dy = dx*400, dy*400
		}
		e.Call("preventDefault")
		w.in.Push(input.Scroll{
			Pos: local, Delta: geom.Pt(float32(-dx), float32(-dy)),
			Notches: geom.Pt(float32(-dx/100), float32(-dy/100)), Mods: modsOf(e), Time: time.Now(),
		})
	})

	key := func(e js.Value, down bool) {
		// A dead key is half a character, which the browser finishes.
		if e.Get("key").String() == "Dead" {
			return
		}
		if d.text.Equal(doc.Get("activeElement")) && editKeyOwnedByDOM(e) {
			return
		}
		w := d.keyWindow()
		if w == nil {
			return
		}
		k := keyOf(e.Get("code").String(), e.Get("key").String())
		m, now := modsOf(e), time.Now()
		if altGr(e) {
			m &^= input.ModControl | input.ModAlt
		}
		if !down {
			w.in.Push(input.KeyRelease{Key: k, Mods: m, Time: now})
			return
		}
		name := e.Get("key").String()
		typed := len([]rune(name)) == 1 && m&(input.ModControl|input.ModSuper) == 0
		var char rune
		if r := []rune(strings.ToLower(name)); len(r) == 1 {
			char = r[0]
		}
		w.in.Push(input.KeyPress{Key: k, Mods: m, Repeat: e.Get("repeat").Bool(), Typed: typed, Char: char, Time: now})
		if typed && !d.text.Equal(doc.Get("activeElement")) {
			// No textarea to type into: the key's text goes along.
			w.in.Push(input.TextInput{Text: name, Time: now})
		}
		active := doc.Get("activeElement")
		if inText := d.text.Equal(active); (inText || c.Equal(active)) && gunimKey(name, m, inText) {
			e.Call("preventDefault")
		}
	}
	d.on(c, "keydown", false, func(e js.Value) { key(e, true) })
	d.on(c, "keyup", false, func(e js.Value) { key(e, false) })
	d.on(t, "keydown", false, func(e js.Value) { key(e, true) })
	d.on(t, "keyup", false, func(e js.Value) { key(e, false) })
	d.listenText(doc)

	focus := func(on bool) {
		d.mu.Lock()
		var top *Window
		for _, w := range d.windows {
			if w.fills {
				top = w
			}
		}
		d.mu.Unlock()
		if top != nil {
			top.in.Push(driver.WindowFocus{Focused: on})
		}
	}
	win := js.Global()
	d.on(win, "focus", true, func(js.Value) { focus(true) })
	d.on(win, "blur", true, func(js.Value) { focus(false) })
	d.on(doc, "visibilitychange", true, func(js.Value) {
		shown := doc.Get("visibilityState").String() == "visible"
		d.mu.Lock()
		ws := append([]*Window(nil), d.windows...)
		d.mu.Unlock()
		for _, w := range ws {
			w.in.Push(driver.WindowShown{Shown: shown})
		}
		if shown {
			d.kick()
		}
	})
	d.on(win, "deviceorientationabsolute", true, d.heading)
	d.on(win, "resize", true, func(js.Value) { d.resized() })
	if ro := win.Get("ResizeObserver"); !ro.IsUndefined() {
		f := js.FuncOf(func(_ js.Value, args []js.Value) any {
			d.readExactSize(args[0])
			d.resized()
			return nil
		})
		d.funcs = append(d.funcs, f)
		observer := ro.New(f)
		// A browser without the box refuses to observe it.
		entry := win.Get("ResizeObserverEntry")
		if !entry.IsUndefined() && js.Global().Get("Reflect").Call("has", entry.Get("prototype"), "devicePixelContentBoxSize").Bool() {
			observer.Call("observe", c, map[string]any{"box": "device-pixel-content-box"})
		} else {
			observer.Call("observe", c)
		}
	}
}

// readExactSize takes the canvas's size in device pixels from a ResizeObserver's
// entries, if the browser gives it.
func (d *Driver) readExactSize(entries js.Value) {
	if entries.Length() == 0 {
		return
	}
	box := entries.Index(0).Get("devicePixelContentBoxSize")
	if box.IsUndefined() || box.Length() == 0 {
		return
	}
	d.mu.Lock()
	d.exactW, d.exactH = box.Index(0).Get("inlineSize").Int(), box.Index(0).Get("blockSize").Int()
	d.mu.Unlock()
}

// survive keeps a JavaScript exception thrown into a callback, which
// syscall/js raises as a panic, from ending the program: the event is
// lost, and the page goes on. It runs deferred, with no lock held.
func survive(what string) {
	if r := recover(); r != nil {
		log.Printf("gunim: web: %s: %v", what, r)
	}
}

// capture keeps a pointer's events coming to el while it is down. The
// browser refuses a pointer it no longer counts as active, as one a
// touch cancelled already; the press goes on without capture then.
func capture(el, id js.Value) {
	defer func() { recover() }()
	el.Call("setPointerCapture", id)
}

// keyWindow returns the window keys go to: the one taking text, or the
// topmost that fills the canvas.
// altGr says a key was pressed with AltGr. Windows reports that as Control and
// Alt together, which with a letter would be a shortcut, and with anything else
// is the character the layout puts there, as @ on a Swedish keyboard.
func altGr(e js.Value) bool {
	if e.Call("getModifierState", "AltGraph").Bool() {
		return true
	}
	name := e.Get("key").String()
	r := []rune(name)
	return e.Get("ctrlKey").Bool() && e.Get("altKey").Bool() && len(r) == 1 && !unicode.IsLetter(r[0]) || e.Get("ctrlKey").Bool() && e.Get("altKey").Bool() && len(r) == 1 && r[0] > unicode.MaxASCII
}

// gunimKey says a key is one gunim acts on and the browser should not: text
// editing and moving about, so the page neither scrolls on the arrows nor moves
// focus on Tab. Everything else stays the browser's, as reloading, zooming,
// going back and the developer tools. inText says the text mirror has focus,
// which types a space itself.
func gunimKey(name string, m input.Mods, inText bool) bool {
	if m&input.ModAlt != 0 {
		return false
	}
	lower := strings.ToLower(name)
	if m&(input.ModControl|input.ModSuper) != 0 {
		switch lower {
		case "a", "y", "z":
			return true
		}
		if m&input.ModSuper != 0 {
			return false
		}
		switch name {
		case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "Backspace", "Delete":
			return true
		}
		return false
	}
	switch name {
	case " ":
		return !inText
	case "Tab", "Enter", "Escape", "Backspace", "Delete", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown":
		return true
	}
	return false
}

func (d *Driver) keyWindow() *Window {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.typing != nil {
		return d.typing
	}
	for i := len(d.windows) - 1; i >= 0; i-- {
		if w := d.windows[i]; !w.hidden && !w.closed {
			return w
		}
	}
	return nil
}

func (d *Driver) typingWindow() *Window {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.typing
}

// keyOf maps a KeyboardEvent's code, the key's place, to gunim's key,
// with its key value for what code leaves out.
func keyOf(code, key string) input.Key {
	if len(code) == 4 && strings.HasPrefix(code, "Key") && code[3] >= 'A' && code[3] <= 'Z' {
		return input.KeyA + input.Key(code[3]-'A')
	}
	if len(code) == 6 && strings.HasPrefix(code, "Digit") {
		return input.Key0 + input.Key(code[5]-'0')
	}
	if len(code) == 7 && strings.HasPrefix(code, "Numpad") && code[6] >= '0' && code[6] <= '9' {
		return input.KeyKP0 + input.Key(code[6]-'0')
	}
	if len(code) >= 2 && code[0] == 'F' {
		n := 0
		for _, r := range code[1:] {
			if r < '0' || r > '9' {
				n = -1
				break
			}
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 12 {
			return input.KeyF1 + input.Key(n-1)
		}
	}
	if k, ok := codes[code]; ok {
		return k
	}
	return codes[key]
}

var codes = map[string]input.Key{
	"Escape": input.KeyEscape, "Enter": input.KeyEnter, "Tab": input.KeyTab, "Space": input.KeySpace,
	"Backspace": input.KeyBackspace, "Delete": input.KeyDelete, "Insert": input.KeyInsert,
	"ArrowLeft": input.KeyLeft, "ArrowRight": input.KeyRight, "ArrowUp": input.KeyUp, "ArrowDown": input.KeyDown,
	"Home": input.KeyHome, "End": input.KeyEnd, "PageUp": input.KeyPageUp, "PageDown": input.KeyPageDown,
	"Minus": input.KeyMinus, "Equal": input.KeyEqual, "BracketLeft": input.KeyLeftBracket,
	"BracketRight": input.KeyRightBracket, "Backslash": input.KeyBackslash, "Semicolon": input.KeySemicolon,
	"Quote": input.KeyApostrophe, "Backquote": input.KeyGraveAccent, "Comma": input.KeyComma,
	"Period": input.KeyPeriod, "Slash": input.KeySlash, "ShiftLeft": input.KeyLeftShift,
	"ShiftRight": input.KeyRightShift, "ControlLeft": input.KeyLeftControl, "ControlRight": input.KeyRightControl,
	"AltLeft": input.KeyLeftAlt, "AltRight": input.KeyRightAlt, "MetaLeft": input.KeyLeftSuper,
	"MetaRight": input.KeyRightSuper, "CapsLock": input.KeyCapsLock, "ContextMenu": input.KeyMenu,
	"NumpadDecimal": input.KeyKPDecimal, "NumpadDivide": input.KeyKPDivide, "NumpadMultiply": input.KeyKPMultiply,
	"NumpadSubtract": input.KeyKPSubtract, "NumpadAdd": input.KeyKPAdd, "NumpadEnter": input.KeyKPEnter,
	"NumpadEqual": input.KeyKPEqual, "Pause": input.KeyPause, "PrintScreen": input.KeyPrintScreen,
	"ScrollLock": input.KeyScrollLock, "NumLock": input.KeyNumLock,
	"MediaPlayPause": input.KeyMediaPlayPause, "MediaStop": input.KeyMediaStop,
	"MediaTrackNext": input.KeyMediaNext, "MediaTrackPrevious": input.KeyMediaPrevious,
	// Key values, for a soft keyboard that leaves code empty.
	" ": input.KeySpace,
}
