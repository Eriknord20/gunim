//go:build js && wasm

package web

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"net/url"
	"sync"
	"syscall/js"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/driver/internal/inbox"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

var errClosed = errors.New("web: window closed")

// Window is the browser implementation of [driver.Window]: a rectangle
// of the canvas, drawn straight onto it when it fills it alone, and
// into a texture of its own otherwise.
type Window struct {
	d         *Driver
	in        *inbox.Inbox
	quit      chan struct{}
	presented chan driver.Frame
	closeOnce sync.Once

	parent                   *Window
	over, above, passthrough bool
	transparent              bool

	// The fields below are guarded by the driver's mu.

	fills  bool
	pos    geom.Point
	size   geom.Size
	hidden bool
	closed bool
	next   *frame
	under  color.NRGBA

	// The fields below belong to the frame loop.

	pane              compose.Pane
	state             *input.TextState
	stateSeq, editSeq uint64
	textBox           geom.Rect
	shot              func(*image.RGBA)
	sharpAt           time.Time
	sharpTimer        *time.Timer
	sharpen           bool
	takesFingers      bool
}

type frame struct {
	ops    []paint.Op
	damage []geom.Rect
}

func newWindow(d *Driver, o driver.Options) *Window {
	w := &Window{
		d:           d,
		quit:        make(chan struct{}),
		presented:   make(chan driver.Frame, 1),
		over:        o.Over,
		above:       o.Above,
		passthrough: o.Passthrough,
		transparent: o.Kind == driver.KindPopup,
	}
	w.in = inbox.New(w.quit)
	w.pane.Transparent = w.transparent
	w.pane.Sharp = func(at time.Time) { d.sharpenAt(w, at) }
	return w
}

// rectLocked returns where the window is on the canvas, in logical
// pixels. It runs with the driver's mu held.
func (w *Window) rectLocked() geom.Rect {
	if w.fills {
		return w.d.screen()
	}
	return geom.Rect{Min: w.pos, Max: w.pos.Add(geom.Pt(w.size.W, w.size.H))}
}

// Presented implements [driver.Window].
func (w *Window) Presented() <-chan driver.Frame { return w.presented }

// Input implements [driver.Window].
func (w *Window) Input() <-chan any { return w.in.Out() }

// Present implements [driver.Window]: the frame waits for the next
// animation frame.
func (w *Window) Present(ops []paint.Op, damage []geom.Rect) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.closed {
		return errClosed
	}
	w.next = &frame{ops: ops, damage: damage}
	w.d.kickLocked()
	return nil
}

// Size implements [driver.Window].
func (w *Window) Size() geom.Size {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.rectLocked().Size()
}

// Scale implements [driver.Window].
func (w *Window) Scale() float32 {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.d.scale
}

// RefreshRate implements [driver.Window], estimated from consecutive animation frames.
func (w *Window) RefreshRate() float64 { w.d.mu.Lock(); defer w.d.mu.Unlock(); return w.d.rate }

// Close implements [driver.Window].
func (w *Window) Close() error {
	w.closeOnce.Do(func() {
		w.d.mu.Lock()
		w.closed = true
		if w.sharpTimer != nil {
			w.sharpTimer.Stop()
		}
		w.next = nil
		w.d.mu.Unlock()
		w.d.closed(w)
		close(w.quit)
		w.in.Close()
	})
	return nil
}

// Place implements [driver.Placer].
func (w *Window) Place(anchor geom.Rect, size geom.Size) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.parent == nil || w.fills {
		return nil
	}
	w.size = size
	w.d.placeLocked(w, anchor)
	w.d.kickLocked()
	return nil
}

// PopupRoom implements [driver.PopupRoomer].
func (w *Window) PopupRoom(anchor geom.Rect) driver.Room {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return compose.PopupRoom(anchor.Add(w.rectLocked().Min), w.d.screen())
}

// ToScreen implements [driver.Screener]: the screen is the canvas.
func (w *Window) ToScreen(p geom.Point) geom.Point {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return p.Add(w.rectLocked().Min)
}

// FromScreen implements [driver.Screener].
func (w *Window) FromScreen(p geom.Point) geom.Point {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return p.Sub(w.rectLocked().Min)
}

// Transparent implements [driver.Transparent].
func (w *Window) Transparent() bool { return w.transparent }

// SetBackground implements [driver.Backgrounder].
func (w *Window) SetBackground(c color.NRGBA) {
	w.d.mu.Lock()
	w.under = c
	w.d.mu.Unlock()
}

// Hide implements [driver.Recycler].
func (w *Window) Hide() error {
	w.d.mu.Lock()
	w.hidden = true
	w.d.kickLocked()
	w.d.mu.Unlock()
	return nil
}

// Show implements [driver.Recycler].
func (w *Window) Show() error {
	w.d.mu.Lock()
	w.hidden = false
	w.d.kickLocked()
	w.d.mu.Unlock()
	return nil
}

// SetTextInput implements [driver.TextInputter]: the text mirror takes the
// keyboard while the window takes text, so dead keys and input methods reach it
// in a field reached by Tab or from code. A finger's field waits for
// [Window.ShowKeyboard] instead, as focus here would bring up a phone's keyboard
// with no tap to ask for it.
func (w *Window) SetTextInput(active bool) {
	d := w.d
	d.mu.Lock()
	switch {
	case active:
		if d.typing != w {
			d.composing = false
		}
		d.typing = w
	case d.typing == w:
		d.typing = nil
		d.composing = false
	default:
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	if active {
		d.syncText(w)
		d.focusMirror()
	} else {
		d.text.Call("blur")
		d.canvas.Call("focus", map[string]any{"preventScroll": true})
	}
}

// focusMirror gives the text mirror the keyboard, unless a finger was last at the
// input or the page's own element has it.
func (d *Driver) focusMirror() {
	d.mu.Lock()
	touch := d.lastTouch
	d.mu.Unlock()
	if touch {
		return
	}
	doc := js.Global().Get("document")
	active := doc.Get("activeElement")
	if active.IsNull() || active.Equal(d.canvas) || active.Equal(d.text) || active.Equal(doc.Get("body")) {
		d.text.Call("focus", map[string]any{"preventScroll": true})
	}
}

// SetCursor implements [driver.CursorSetter] with CSS cursors.
func (w *Window) SetCursor(c input.Cursor) {
	name := map[input.Cursor]string{
		input.CursorText: "text", input.CursorHand: "pointer", input.CursorCrosshair: "crosshair",
		input.CursorResizeH: "ew-resize", input.CursorResizeV: "ns-resize", input.CursorMove: "move",
		input.CursorNotAllowed: "not-allowed", input.CursorResizeNWSE: "nwse-resize",
		input.CursorResizeNESW: "nesw-resize", input.CursorNone: "none",
	}[c]
	if name == "" {
		name = "default"
	}
	w.d.canvas.Get("style").Set("cursor", name)
}

// OpenLink implements [driver.LinkOpener] with a new tab. It takes an http or https
// address, and returns an error for anything else. It can't tell a popup blocker
// stopped the tab, as a tab opened with noopener says nothing back.
func (w *Window) OpenLink(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("web: opening a link: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("web: opening %q: only http and https addresses open", address)
	}
	_, err = call(js.Global(), "open", address, "_blank", "noopener")
	return err
}

var (
	_ driver.Placer       = (*Window)(nil)
	_ driver.PopupRoomer  = (*Window)(nil)
	_ driver.Screener     = (*Window)(nil)
	_ driver.Transparent  = (*Window)(nil)
	_ driver.Backgrounder = (*Window)(nil)
	_ driver.Recycler     = (*Window)(nil)
	_ driver.TextInputter = (*Window)(nil)
	_ driver.CaretPlacer  = (*Window)(nil)
	_ driver.LinkOpener   = (*Window)(nil)
	_ driver.CursorSetter = (*Window)(nil)
)

// SetTextState mirrors text and selection for predictive keyboards, including
// UTF-16/UTF-8 selection conversion and acknowledgement of in-flight edits.
func (w *Window) SetTextState(state *input.TextState, seq uint64) {
	w.d.mu.Lock()
	if seq < w.editSeq {
		w.d.mu.Unlock()
		return
	}
	if state == nil {
		w.state = nil
	} else {
		kept := *state
		w.state = &kept
	}
	w.stateSeq = seq
	w.d.mu.Unlock()
	w.d.syncText(w)
}
