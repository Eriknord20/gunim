//go:build android

package android

import (
	"math"
	"time"

	"github.com/marrasen/gunim/driver/internal/touch"
	"github.com/marrasen/gunim/geom"
)

// The view's touches go through driver/internal/touch, which driver/kms
// shares, so a touch behaves the same on both. The view decides which
// finger drives the pointer, which is a finger of its own and which two
// pinch; the calls below hand each on in logical pixels.

// gestures returns the touch in progress, set up on first use. It runs
// with mu held.
func (d *Driver) gestures() *touch.Gestures {
	g := &d.gest
	if g.Hit == nil {
		g.Hit = func(p geom.Point) touch.Window {
			if w := d.hitLocked(p); w != nil {
				return w
			}
			return nil
		}
		g.Started = func(w touch.Window) {
			tw, _ := w.(*Window)
			d.takeFingersLocked(tw)
		}
	}
	return g
}

// touchedLocked returns the window the touch in progress started in, or
// nil. It runs with mu held.
func (d *Driver) touchedLocked() *Window {
	w, _ := d.gest.Touched().(*Window)
	return w
}

// logical turns a point in device pixels into the windows' logical pixels.
// The windows are drawn slid up by the pan, so the point touched is that
// much further down them.
func (d *Driver) logical(x, y float32) geom.Point {
	return geom.Pt(x/d.density, (y+float32(math.Round(d.pan)))/d.density)
}

// touch hands the first finger to [touch.Gestures.Touch]: it drives the
// pointer. In a window that takes fingers it reports true, for the view to
// send the rest of the finger through finger. x and y are in device
// pixels.
func (d *Driver) touch(action, id int, x, y float32, now time.Time) (own bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if action == touchDown {
		d.keyed, d.refit = false, false
	}
	return d.gestures().Touch(touch.Action(action), id, d.logical(x, y), now)
}

// finger hands a finger of its own to [touch.Gestures.Finger]. x and y
// are in device pixels.
func (d *Driver) finger(action, id int, x, y float32, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gestures().Finger(touch.Action(action), id, d.logical(x, y), now)
}

// pinch hands two fingers to [touch.Gestures.Pinch]. x0, y0, x1 and y1
// are the fingers, in device pixels.
func (d *Driver) pinch(action int, x0, y0, x1, y1 float32, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gestures().Pinch(touch.PinchAction(action), d.logical(x0, y0), d.logical(x1, y1), now)
}

// takeFingersLocked hands the view the setting of the window the touch
// in progress started in, where it differs from the last it handed.
// changed is the window whose setting may have changed: the view hears
// only the touch's window, and as a touch starts, the window it starts
// in, nil for none. It runs with mu held, so the calls reach Java in
// the order the windows made them.
func (d *Driver) takeFingersLocked(changed *Window) {
	if changed != d.touchedLocked() {
		return
	}
	on := changed != nil && changed.takesFingers
	if on == d.fingersOn {
		return
	}
	d.fingersOn = on
	takeFingers(on)
}

// Push implements [touch.Window].
func (w *Window) Push(ev any) { w.in.Push(ev) }

// Origin implements [touch.Window]: where the window lies on the surface.
// It runs with the driver's mu held.
func (w *Window) Origin() geom.Point { return w.pos }

// TakesFingers implements [touch.Window]. It runs with the driver's mu
// held.
func (w *Window) TakesFingers() bool { return w.takesFingers }
