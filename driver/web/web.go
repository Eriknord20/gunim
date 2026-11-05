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
// input. Text comes through a hidden textarea, or a password field for a
// secret, focused while a window takes text, so a phone's soft keyboard and
// input methods work.
//
// Each key has one owner. The browser keeps a short list, as reloading and
// zooming, and gunim never hears those. Every other key with Control or Command
// is gunim's alone, so the browser's own find and print don't run in a program.
// Cut, copy and paste in a text field stay the browser's, which edits the mirror.
//
// Gunim takes every touch on the canvas, and scrolls and pans for itself. A page
// that wants the browser to have some of them, as to scroll the page with
// vertical swipes, sets touch-action on the canvas, in its style attribute or a
// stylesheet, and that is kept. Elements a page lays over the canvas keep their
// own events.
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
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/internal/gl"
)

// Driver is the browser implementation of [driver.Driver].
type Driver struct {
	mu sync.Mutex

	// text is whichever of plain and secret takes text just now.
	canvas, text, plain, secret js.Value
	g                           gl.Context
	comp                        *compose.Compositor
	// ctx is the WebGL context behind g, which says what the GPU can draw.
	ctx js.Value

	// scale is the page's devicePixelRatio, and cssW and cssH the
	// canvas's size in CSS pixels, which are logical pixels. surfW and
	// surfH are its drawing buffer's size in device pixels.
	// lastTouch says the last press was a finger or a pen, not a mouse.
	lastTouch bool
	// kept holds the keys down that stay the browser\'s, by their place.
	kept         map[string]bool
	scale        float32
	cssW, cssH   float32
	surfW, surfH int
	// exactW and exactH are the canvas's size in device pixels, from a browser
	// that says. pinned says the canvas has been through pinAutoSize.
	exactW, exactH int
	pinned         bool
	// bufferLimit is the longest side of a drawing buffer the GPU and browser can
	// draw on, or 0 for not known, and limitSaid says the log has told of it.
	bufferLimit int
	limitSaid   bool

	// windows is the stack of open windows, bottom first, and gone the panes of
	// windows closed, for the next frame to free.
	windows []*Window
	gone    []*compose.Pane
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
	fingers     map[int]*Window
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

	// scaleQuery is the media query that tells of the page's scale changing, and
	// scaleChanged what hears it.
	scaleQuery   js.Value
	scaleChanged js.Func

	safeProbe      js.Value
	safe           geom.Insets
	keyboard       float32
	viewportHeight float64
	lost           bool
	// lostTimer runs out when a lost context has been gone too long, and lostNote
	// is what then tells the user.
	lostTimer *time.Timer
	lostNote  js.Value
	rate      float64
	lastRAF   float64
	// gaps is a ring of the last gaps between animation frames, gapCount of them
	// so far and the next at gapAt, and sortedGaps room to sort them in.
	gaps            [60]float64
	sortedGaps      [60]float64
	gapAt, gapCount int
	stats           frameStats
	frameAsked      bool
	raf             js.Func
	funcs           []js.Func
	quit            chan struct{}
}

var (
	theDriver *Driver
	openErr   error
	openOnce  sync.Once
)

// Open returns the driver, built on the page's canvas the first time.
func Open() (*Driver, error) {
	openOnce.Do(func() { theDriver, openErr = openDriver() })
	return theDriver, openErr
}

func openDriver() (*Driver, error) {
	doc := js.Global().Get("document")
	if doc.IsUndefined() {
		return nil, errors.New("web: no document")
	}
	canvas := doc.Call("getElementById", "gunim")
	if canvas.IsNull() {
		canvas = makeCanvas(doc)
	}
	opts := js.ValueOf(map[string]any{
		"alpha": false, "antialias": false, "depth": false, "stencil": false,
		"premultipliedAlpha": true, "preserveDrawingBuffer": false, "powerPreference": "high-performance",
	})
	ctx := canvas.Call("getContext", "webgl2", opts)
	g, err := gl.NewWebGLContext(ctx)
	if err != nil {
		return nil, err
	}
	comp, err := compose.New(g, "web")
	if err != nil {
		return nil, err
	}
	d := &Driver{canvas: canvas, g: g, ctx: ctx, comp: comp, quit: make(chan struct{}), rate: 60, fingers: map[int]*Window{}, headingTo: map[*Window]bool{}, kept: map[string]bool{}}
	d.bufferLimit = gpuLimit(ctx)
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

// makeCanvas makes the canvas for a page with none, filling the page, and takes
// off the body's default margins, which would have the page scroll by them. Its
// height follows the phone's address bar as that comes and goes, where 100vh would
// leave the bottom of the canvas out of reach. A browser without dvh refuses the
// second height and keeps the first.
func makeCanvas(doc js.Value) js.Value {
	canvas := doc.Call("createElement", "canvas")
	canvas.Set("id", "gunim")
	s := canvas.Get("style")
	s.Set("display", "block")
	s.Set("width", "100vw")
	s.Set("height", "100vh")
	s.Set("height", "100dvh")
	body := doc.Get("body")
	body.Get("style").Set("margin", "0")
	body.Call("appendChild", canvas)
	return canvas
}

// gpuLimit returns the longest side the GPU can draw on, going by the textures,
// the renderbuffers and the viewport it has.
func gpuLimit(ctx js.Value) int {
	param := func(name string) int { return ctx.Call("getParameter", ctx.Get(name)).Int() }
	dims := ctx.Call("getParameter", ctx.Get("MAX_VIEWPORT_DIMS"))
	return min(param("MAX_TEXTURE_SIZE"), param("MAX_RENDERBUFFER_SIZE"), dims.Index(0).Int(), dims.Index(1).Int())
}

// pinAutoSize keeps the canvas's CSS size from following its drawing buffer, which
// is about to take the canvas's size in device pixels. It runs once, as the canvas
// first has a size, and leaves the buffer as it found it.
//
// A side with no CSS size at all follows the buffer alone, and would grow without
// end at a scale above 1: that side is fixed where it is. A side given as auto
// while the other side has a size, as height:auto with width:100%, follows the
// other side at the buffer's ratio, and stays free to. The ratio then goes into
// CSS, so the buffer's rounding can't nudge the canvas, which the browser would
// report as a ResizeObserver loop.
func pinAutoSize(c js.Value) {
	w, h := c.Get("clientWidth").Int(), c.Get("clientHeight").Int()
	bw, bh := c.Get("width").Int(), c.Get("height").Int()
	moved := func() bool { return c.Get("clientWidth").Int() != w || c.Get("clientHeight").Int() != h }
	// Twice the buffer at the same ratio moves only a side that follows it alone.
	c.Set("width", bw*2)
	c.Set("height", bh*2)
	if c.Get("clientWidth").Int() != w {
		c.Get("style").Set("width", strconv.Itoa(w)+"px")
	}
	if c.Get("clientHeight").Int() != h {
		c.Get("style").Set("height", strconv.Itoa(h)+"px")
	}
	// Another ratio moves a side that follows the other.
	c.Set("width", bw*2+7)
	if moved() && bw > 0 && bh > 0 {
		c.Get("style").Set("aspectRatio", strconv.Itoa(bw)+" / "+strconv.Itoa(bh))
	}
	c.Set("width", bw)
	c.Set("height", bh)
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
	if w == 0 || h == 0 {
		// Hidden, or given no room. Its drawing buffer stays as it is, as a canvas
		// with no CSS size takes its size from that once it shows.
		d.mu.Lock()
		changed := w != d.cssW || h != d.cssH || s != d.scale
		d.cssW, d.cssH, d.scale, d.surfW, d.surfH = w, h, s, 0, 0
		d.mu.Unlock()
		return changed
	}
	d.mu.Lock()
	pin := !d.pinned
	d.pinned = true
	d.mu.Unlock()
	if pin {
		pinAutoSize(d.canvas)
	}
	pw, ph := int(math.Round(float64(w*s))), int(math.Round(float64(h*s)))
	d.mu.Lock()
	defer d.mu.Unlock()
	// clientWidth rounds, so at a fractional scale the browser's own count of
	// device pixels is the one that fills the canvas exactly. Further off than
	// rounding explains, it is out of date, or the scale is emulated.
	if near(d.exactW, pw, s) && near(d.exactH, ph, s) {
		pw, ph = d.exactW, d.exactH
	}
	changed := w != d.cssW || h != d.cssH
	d.cssW, d.cssH = w, h
	if lw, lh, ls := fitLimit(w, h, s, pw, ph, d.bufferLimit); lw != pw || lh != ph {
		d.sayLimitLocked(pw, ph, s, ls)
		pw, ph, s = lw, lh, ls
	}
	if pw != d.surfW || ph != d.surfH {
		d.canvas.Set("width", pw)
		d.canvas.Set("height", ph)
		// The browser can give a smaller buffer than it was asked for, as it does
		// past what it counts as the GPU's limit or its own, and says so only here.
		if !d.lost && d.ctx.Truthy() {
			bw, bh := d.ctx.Get("drawingBufferWidth").Int(), d.ctx.Get("drawingBufferHeight").Int()
			if bw > 0 && bh > 0 && (bw < pw || bh < ph) {
				// A new limit holds from here on, so the next look at the same size
				// doesn't ask for the big buffer again.
				d.bufferLimit = max(bw, bh)
				lw, lh, ls := fitLimit(w, h, s, pw, ph, d.bufferLimit)
				d.sayLimitLocked(pw, ph, s, ls)
				pw, ph, s = lw, lh, ls
				d.canvas.Set("width", pw)
				d.canvas.Set("height", ph)
			}
		}
		d.surfW, d.surfH = pw, ph
		changed = true
	}
	if s != d.scale {
		d.scale = s
		changed = true
	}
	return changed
}

// fitLimit returns the drawing buffer's size and the scale for a canvas of w×h
// logical pixels with no side past limit: the canvas stays whole and in its place,
// drawn at a lower scale, and the browser stretches it to fit. It is pw×ph at scale
// when that fits, or there is no limit known.
func fitLimit(w, h, scale float32, pw, ph, limit int) (lw, lh int, ls float32) {
	if limit <= 0 || (pw <= limit && ph <= limit) {
		return pw, ph, scale
	}
	ls = min(float32(limit)/w, float32(limit)/h)
	lw = min(limit, int(math.Round(float64(w*ls))))
	lh = min(limit, int(math.Round(float64(h*ls))))
	return max(lw, 1), max(lh, 1), ls
}

// sayLimitLocked tells the log once why a canvas draws at less than the page's
// scale.
func (d *Driver) sayLimitLocked(pw, ph int, wanted, got float32) {
	if d.limitSaid {
		return
	}
	d.limitSaid = true
	log.Printf("gunim: web: a canvas of %dx%d device pixels is past the longest side of %d the GPU can draw, so it draws at scale %.3g, not %.3g. "+
		`On a phone, a page with no <meta name="viewport" content="width=device-width, initial-scale=1"> lays out about 980 px wide, which does this`,
		pw, ph, d.bufferLimit, got, wanted)
}

// near says a count of device pixels is within rounding of what was worked out
// from logical pixels at a scale.
func near(exact, worked int, scale float32) bool {
	return exact > 0 && math.Abs(float64(exact-worked)) <= float64(scale)+1
}

// resized follows the canvas to its new size, and reports whether it changed.
func (d *Driver) resized() bool {
	d.mu.Lock()
	scale := d.scale
	d.mu.Unlock()
	if !d.measure() {
		return false
	}
	d.mu.Lock()
	var fill []*Window
	for _, w := range d.windows {
		// At a new scale, every window draws afresh for it.
		if w.fills || d.scale != scale {
			fill = append(fill, w)
		}
	}
	d.mu.Unlock()
	for _, w := range fill {
		w.in.Push(driver.Redraw{})
	}
	d.kick()
	return true
}

// observedSize follows a size the ResizeObserver reports. It runs after the
// animation frame and before the browser shows the canvas, so a size set here
// would show an empty canvas unless it is drawn on again now.
func (d *Driver) observedSize() {
	if d.resized() {
		d.frame()
	}
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
	w.pos = compose.Place(anchor.Add(w.parent.pos), w.size, w.over, w.above, d.screen())
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
	d.gone = append(d.gone, &w.pane)
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

// pageTouchAction returns what the page asks of the browser for touches on the
// canvas, in its style attribute or a stylesheet, or "none", for gunim to take them
// all, if the page asks nothing. A stylesheet's "auto" can't be told from no
// rule at all, and counts as none.
func pageTouchAction(c js.Value) string {
	if t := c.Get("style").Get("touchAction").String(); t != "" {
		return t
	}
	t := js.Global().Call("getComputedStyle", c).Get("touchAction")
	if !t.Truthy() || t.String() == "auto" {
		return "none"
	}
	return t.String()
}

// buttonBit is where a button is in a PointerEvent's buttons, which numbers them
// differently from its button.
func buttonBit(button int) int {
	switch button {
	case 1:
		return 4
	case 2:
		return 2
	}
	return 1 << button
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
	d.touchAction = pageTouchAction(c)
	c.Get("style").Set("touchAction", d.touchAction)
	// The driver's focus call on a press would have the browser draw its focus
	// ring round the whole canvas. Windows draw focus in their own way.
	if c.Get("style").Get("outline").String() == "" {
		c.Get("style").Set("outline", "none")
	}
	c.Get("style").Set("userSelect", "none")
	c.Get("style").Set("webkitUserSelect", "none")
	c.Get("style").Set("webkitTapHighlightColor", "transparent")
	c.Set("tabIndex", 0)

	// What takes text, out of sight, but focusable, so a phone shows its
	// keyboard for it: a textarea, and for a secret a password field, which
	// tells a phone's keyboard not to learn it.
	mirror := func(tag string) js.Value {
		el := doc.Call("createElement", tag)
		for k, v := range map[string]string{
			"position": "fixed", "left": "0", "top": "0", "width": "1px", "height": "1px",
			"opacity": "0", "border": "0", "padding": "0", "resize": "none", "pointerEvents": "none",
		} {
			el.Get("style").Set(k, v)
		}
		el.Call("setAttribute", "autocapitalize", "none")
		el.Call("setAttribute", "spellcheck", "false")
		el.Call("setAttribute", "aria-hidden", "true")
		doc.Get("body").Call("appendChild", el)
		return el
	}
	d.plain, d.secret = mirror("textarea"), mirror("input")
	d.plain.Call("setAttribute", "autocomplete", "off")
	d.plain.Call("setAttribute", "autocorrect", "on")
	d.secret.Set("type", "password")
	d.secret.Call("setAttribute", "autocomplete", "new-password")
	d.secret.Call("setAttribute", "autocorrect", "off")
	d.text = d.plain

	d.on(c, "contextmenu", false, func(e js.Value) { e.Call("preventDefault") })
	d.on(c, "pointerdown", false, func(e js.Value) {
		p, now := d.at(e), time.Now()
		touch, id := d.touchOf(e), e.Get("pointerId").Int()
		d.mu.Lock()
		d.lastTouch = e.Get("pointerType").String() != "mouse"
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
		// The driver moves focus itself. A mouse's press is cancelled at its
		// mousedown, below, so the page still gets its mouseup, which Web Audio's
		// wait for a gesture may hang on.
		if e.Get("pointerType").String() != "mouse" {
			e.Call("preventDefault")
		}
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
	// The browser's own focus change on a mouse press would undo a text field's
	// focus.
	d.on(c, "mousedown", false, func(e js.Value) { e.Call("preventDefault") })
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
		held := w != nil
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
			// One button going down or up while another is held comes as a move.
			if held && !finger && e.Get("button").Int() >= 0 {
				if b := buttonOf(e); e.Get("buttons").Int()&buttonBit(e.Get("button").Int()) != 0 {
					w.in.Push(input.PointerDown{Pos: local, Button: b, Mods: modsOf(e), Clicks: 1, Time: now})
				} else {
					w.in.Push(input.PointerUp{Pos: local, Button: b, Mods: modsOf(e), Time: now})
				}
			}
		}
	})
	up := func(e js.Value, cancel bool) {
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
		if d.text.Equal(doc.Get("activeElement")) && d.editKeyOwnedByDOM(e) {
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
		// A key's owner is settled as it goes down, whatever is held as it comes up.
		name, code := e.Get("key").String(), e.Get("code").String()
		d.mu.Lock()
		if !down {
			kept := d.kept[code]
			delete(d.kept, code)
			d.mu.Unlock()
			if !kept {
				w.in.Push(input.KeyRelease{Key: k, Mods: m, Time: now})
			}
			return
		}
		kept := browserKey(name, code, m)
		if kept {
			d.kept[code] = true
		} else {
			delete(d.kept, code)
		}
		d.mu.Unlock()
		if kept {
			return
		}
		typed := len([]rune(name)) == 1 && m&(input.ModControl|input.ModSuper) == 0
		var char rune
		if r := []rune(strings.ToLower(name)); len(r) == 1 {
			char = r[0]
		}
		w.in.Push(input.KeyPress{Key: k, Mods: m, Repeat: e.Get("repeat").Bool(), Typed: typed, Char: char, Time: now})
		if typed && !d.text.Equal(doc.Get("activeElement")) {
			// No textarea to type into: the key's text goes along. The key is
			// cancelled, as the handler may move focus to the mirror before the
			// browser ends the key, and the browser would type it there too.
			w.in.Push(input.TextInput{Text: name, Time: now})
			e.Call("preventDefault")
		}
		active := doc.Get("activeElement")
		if inText := d.text.Equal(active); (inText || c.Equal(active)) && gunimKey(name, m, inText) {
			e.Call("preventDefault")
		}
	}
	d.on(c, "keydown", false, func(e js.Value) { key(e, true) })
	d.on(c, "keyup", false, func(e js.Value) { key(e, false) })
	for _, t := range []js.Value{d.plain, d.secret} {
		d.on(t, "keydown", false, func(e js.Value) { key(e, true) })
		d.on(t, "keyup", false, func(e js.Value) { key(e, false) })
	}
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
	d.watchScale()
	if ro := win.Get("ResizeObserver"); !ro.IsUndefined() {
		f := js.FuncOf(func(_ js.Value, args []js.Value) any {
			defer survive("resize")
			d.readExactSize(args[0])
			d.observedSize()
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

// watchScale listens for the page's scale to change. That can come with no resize,
// as on a move to another screen, and a media query is the one thing that tells.
func (d *Driver) watchScale() {
	d.scaleChanged = js.FuncOf(func(js.Value, []js.Value) any {
		defer survive("scale")
		d.armScale()
		d.resized()
		return nil
	})
	d.funcs = append(d.funcs, d.scaleChanged)
	d.armScale()
}

// armScale listens for the page's scale to leave what it is now. A query says only
// whether the scale matches, so each change needs one of its own.
func (d *Driver) armScale() {
	win := js.Global()
	if d.scaleQuery.Truthy() {
		d.scaleQuery.Call("removeEventListener", "change", d.scaleChanged)
	}
	d.scaleQuery = win.Call("matchMedia", "(resolution: "+strconv.FormatFloat(win.Get("devicePixelRatio").Float(), 'f', -1, 64)+"dppx)")
	d.scaleQuery.Call("addEventListener", "change", d.scaleChanged)
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
	defer func() { _ = recover() }()
	el.Call("setPointerCapture", id)
}

// altGr says a key was pressed with AltGr. Windows reports that as Control and
// Alt together, which with a letter would be a shortcut, and with anything else
// is the character the layout puts there, as @ on a Swedish keyboard.
func altGr(e js.Value) bool {
	if e.Call("getModifierState", "AltGraph").Bool() {
		return true
	}
	r := []rune(e.Get("key").String())
	if !e.Get("ctrlKey").Bool() || !e.Get("altKey").Bool() || len(r) != 1 {
		return false
	}
	return !unicode.IsLetter(r[0]) || r[0] > unicode.MaxASCII
}

// browserKey says a key stays the browser's alone, and gunim never hears it:
// reloading, zooming, the developer tools, going back and forward, tabs, the
// address bar and full screen. name is the key's value and code its place.
func browserKey(name, code string, m input.Mods) bool {
	command := m&(input.ModControl|input.ModSuper) != 0
	alt := m&input.ModAlt != 0
	lower := shortcutName(name, code)
	arrow := name == "ArrowLeft" || name == "ArrowRight"
	switch {
	case !command && !alt:
		switch name {
		case "F5", "F6", "F11", "F12":
			return true
		}
		return false
	case !command:
		return arrow || name == "Home" || lower == "d"
	case alt:
		// On a Mac: the developer tools, and moving along the tabs.
		return m&input.ModSuper != 0 && (lower == "i" || lower == "j" || lower == "c" || arrow)
	}
	if m&input.ModControl != 0 && m&input.ModSuper != 0 {
		// Full screen on a Mac.
		return lower == "f"
	}
	switch lower {
	case "r", "t", "n", "w", "l", "f5", "tab", "pageup", "pagedown",
		"+", "=", "-", "_", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return true
	case "i", "j", "c", "a":
		// The developer tools, and the search of the tabs.
		return m&input.ModShift != 0
	case "[", "]", "{", "}":
		// Going back and forward, and along the tabs, on a Mac.
		return m&input.ModSuper != 0
	}
	// The row of digits picks a tab by place, whatever a layout has on it.
	return m&input.ModShift == 0 && strings.HasPrefix(code, "Digit")
}

// shortcutName is a key's name for matching a shortcut: its value in lower case,
// or for a character past ASCII, as on a Russian layout, the one its place has on
// an American keyboard, which is how browsers read shortcuts there.
func shortcutName(name, code string) string {
	r := []rune(name)
	if len(r) != 1 || r[0] <= unicode.MaxASCII {
		return strings.ToLower(name)
	}
	switch {
	case len(code) == 4 && strings.HasPrefix(code, "Key"):
		return strings.ToLower(code[3:])
	case code == "Minus":
		return "-"
	case code == "Equal":
		return "="
	case code == "BracketLeft":
		return "["
	case code == "BracketRight":
		return "]"
	}
	return strings.ToLower(name)
}

// gunimKey says a key the browser would otherwise act on is gunim's, so the page
// neither scrolls on the arrows nor moves focus on Tab: every key with Control or
// Command that the browser doesn't keep, moving about and editing, and the
// function keys. inText says the text mirror has focus, which types a space
// itself.
func gunimKey(name string, m input.Mods, inText bool) bool {
	if m&input.ModAlt != 0 {
		return false
	}
	if m&(input.ModControl|input.ModSuper) != 0 {
		return true
	}
	switch name {
	case " ":
		return !inText
	case "Tab", "Enter", "Escape", "Backspace", "Delete", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown":
		return true
	}
	return len(name) >= 2 && name[0] == 'F' && name[1] >= '0' && name[1] <= '9'
}

// keyWindow returns the window keys go to: the one taking text, or the
// topmost that fills the canvas.
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
