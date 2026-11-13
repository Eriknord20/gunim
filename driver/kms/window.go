//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"image"
	"image/color"
	"math"
	"sync"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/driver/internal/inbox"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// errClosed is returned by a closed window.
var errClosed = errors.New("kms: window closed")

// Window is the KMS implementation of [driver.Window]: a rectangle of the
// one screen, drawn into a texture of its own, or straight onto the
// screen where it fills it alone and the picture is not turned.
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

	// fills says the window covers the screen, as the first one does.
	// pos and size place any other, in logical pixels of the screen.
	fills  bool
	pos    geom.Point
	size   geom.Size
	hidden bool
	closed bool
	// stale says the window was hidden and has presented nothing since.
	stale bool
	// held keeps the window where its frame showed it, until its frame at
	// a new size comes.
	held compose.Held
	// next is the frame waiting for the render thread, while hasNext, and
	// under the colour under the window's frames.
	next    frame
	hasNext bool
	under   color.NRGBA
	// shot takes the pixels of the next frame drawn; see Shoot.
	shot func(*image.RGBA)
	// sharpAt is when the last frame, which drew a big mask stretched, is
	// to be drawn again, sharp; sharpen says it is due.
	sharpAt time.Time
	sharpen bool
	// takesFingers is what TakeFingers last said.
	takesFingers bool

	// pane belongs to the render thread.
	pane compose.Pane
}

// frame is one frame for the render thread.
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

// rectLocked returns where the window is on the screen, in logical pixels.
// It runs with the driver's mu held.
func (w *Window) rectLocked() geom.Rect {
	if w.fills {
		return w.d.screenLocked()
	}
	return geom.Rect{Min: w.pos, Max: w.pos.Add(geom.Pt(w.size.W, w.size.H))}
}

// Presented implements [driver.Window].
func (w *Window) Presented() <-chan driver.Frame { return w.presented }

// Input implements [driver.Window].
func (w *Window) Input() <-chan any { return w.in.Out() }

// Present implements [driver.Window]. It leaves the frame for the render
// thread, which draws it once the frame before has flipped.
func (w *Window) Present(ops []paint.Op, damage []geom.Rect) error {
	d := w.d
	d.mu.Lock()
	if w.closed {
		d.mu.Unlock()
		return errClosed
	}
	w.next, w.hasNext = frame{ops: ops, damage: damage}, true
	w.stale = false
	if d.handed.IsZero() {
		d.handed = time.Now()
	}
	d.mu.Unlock()
	d.kick()
	return nil
}

// Size implements [driver.Window].
func (w *Window) Size() geom.Size {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.rectLocked().Size()
}

// Scale implements [driver.Window].
func (w *Window) Scale() float32 { return w.d.density }

// RefreshRate implements [driver.Window]: the mode's rate, from its
// timings.
func (w *Window) RefreshRate() float64 { return w.d.rate }

// Clipboard implements [driver.Window]. There is no system to share a
// clipboard with, so it is the process's own.
func (w *Window) Clipboard() (string, error) {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.d.clipboard, nil
}

// SetClipboard implements [driver.Window].
func (w *Window) SetClipboard(s string) error {
	w.d.mu.Lock()
	w.d.clipboard = s
	w.d.mu.Unlock()
	return nil
}

// Close implements [driver.Window].
func (w *Window) Close() error {
	w.closeOnce.Do(func() {
		w.d.mu.Lock()
		w.closed = true
		w.next, w.hasNext = frame{}, false
		w.d.mu.Unlock()
		w.d.windowClosed(w)
		close(w.quit)
		w.in.Close()
	})
	return nil
}

// Shoot implements [driver.Shooter]: fn takes the window's next frame as
// the render thread draws it.
func (w *Window) Shoot(fn func(*image.RGBA)) {
	w.d.mu.Lock()
	w.shot = fn
	w.d.mu.Unlock()
	w.d.kick()
}

// keepShot puts back a shot whose frame could not draw, for a later frame.
func (w *Window) keepShot(fn func(*image.RGBA)) {
	w.d.mu.Lock()
	if w.shot == nil {
		w.shot = fn
	}
	w.d.mu.Unlock()
}

// TakeFingers implements [driver.FingerTaker].
func (w *Window) TakeFingers(on bool) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	w.takesFingers = on
	return nil
}

// ContentOrigin implements [driver.Positioner]: where the window lies on
// the picture, in device pixels.
func (w *Window) ContentOrigin() (image.Point, error) {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	at, f := w.rectLocked().Min, float64(w.d.density)
	return image.Pt(int(math.Round(float64(at.X)*f)), int(math.Round(float64(at.Y)*f))), nil
}

// Place implements [driver.Placer]. A new size takes effect with the
// frame the engine presents next.
func (w *Window) Place(anchor geom.Rect, size geom.Size) error {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if w.parent == nil || w.fills {
		return nil
	}
	was, resized := w.rectLocked(), size != w.size
	w.size = size
	w.pos = compose.Place(anchor.Add(w.parent.pos), w.size, w.over, w.above, d.screenLocked())
	if resized {
		w.held.Resized(was)
	} else {
		d.kick()
	}
	return nil
}

// PopupRoom implements [driver.PopupRoomer]: the room from anchor, in w's
// logical space, to the screen's edges.
func (w *Window) PopupRoom(anchor geom.Rect) driver.Room {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return compose.PopupRoom(anchor.Add(w.rectLocked().Min), w.d.screenLocked())
}

// ToScreen implements [driver.Screener]. The screen's coordinates are
// logical pixels here.
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

// Transparent implements [driver.Transparent]: a popup shows the windows
// under it where it paints nothing.
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
	w.hidden, w.stale = true, true
	w.d.mu.Unlock()
	w.d.kick()
	return nil
}

// Show implements [driver.Recycler]. The window shows from its next frame.
func (w *Window) Show() error {
	w.d.mu.Lock()
	w.hidden = false
	w.d.mu.Unlock()
	w.d.kick()
	return nil
}

// SetBrightness sets the backlight, from 0 to 1. The backlight has steps,
// 32 on the Touch Display 2, and takes the nearest step at or above v.
// It returns the error the backlight's file gave, and an error where there
// is no backlight.
func (w *Window) SetBrightness(v float32) error {
	if w.d.light == nil {
		return errNoBacklight
	}
	w.d.lightMu.Lock()
	defer w.d.lightMu.Unlock()
	return w.d.light.set(v)
}

// Brightness returns the backlight's level, from 0 to 1, and 1 where there
// is no backlight.
func (w *Window) Brightness() float32 {
	if w.d.light == nil {
		return 1
	}
	w.d.lightMu.Lock()
	defer w.d.lightMu.Unlock()
	return w.d.light.brightness()
}

// Software reports whether the CPU draws, as where the GPU failed: an
// application may show a small notice, or leave out what draws slowly
// there, such as blurs.
func (w *Window) Software() bool { return w.d.software }

// errNoBacklight is returned by SetBrightness where the screen has no
// backlight the driver can set.
var errNoBacklight = errors.New("kms: no backlight under /sys/class/backlight")

// sharpenAt has w draw its last frame again at the time at, the zero time
// for never, so a big mask the frame drew stretched is drawn sharp.
func (d *Driver) sharpenAt(w *Window, at time.Time) {
	d.mu.Lock()
	w.sharpAt = at
	d.mu.Unlock()
	if at.IsZero() {
		return
	}
	time.AfterFunc(time.Until(at), func() {
		d.mu.Lock()
		w.sharpen = w.sharpen || w.sharpAt.Equal(at)
		d.mu.Unlock()
		d.kick()
	})
}
