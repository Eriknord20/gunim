//go:build js && wasm

package web

import (
	"errors"
	"math"
	"syscall/js"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/input"
)

// Buzz implements [driver.Buzzer]: the short buzz of a long press, where
// the browser has a vibration motor to run.
func (w *Window) Buzz() { _ = w.Vibrate(10 * time.Millisecond) }

// Vibrate implements [driver.Vibrator] with navigator.vibrate. The browser
// runs a pattern only after the user has touched the page, and otherwise
// leaves the motor still without an error.
func (w *Window) Vibrate(pattern ...time.Duration) error {
	nav := js.Global().Get("navigator")
	if nav.Get("vibrate").IsUndefined() {
		return driver.ErrNoVibrator
	}
	ms := make([]any, len(pattern))
	for i, p := range pattern {
		ms[i] = p.Milliseconds()
	}
	nav.Call("vibrate", ms)
	return nil
}

// Share implements [driver.Sharer] with navigator.share, for text. A browser
// program has no files at paths to hand over, so a share of files is
// [driver.ErrNoSharer]. A user who closes the share sheet is no error.
func (w *Window) Share(s driver.Share) error {
	nav := js.Global().Get("navigator")
	if nav.Get("share").IsUndefined() || len(s.Paths) > 0 {
		return driver.ErrNoSharer
	}
	_, err := await(nav.Call("share", map[string]any{"title": s.Subject, "text": s.Text}))
	var jsErr js.Error
	if errors.As(err, &jsErr) && jsErr.Value.Get("name").String() == "AbortError" {
		return nil
	}
	return err
}

// TakeFingers implements [driver.FingerTaker]: each finger of a touch that
// starts in w is a finger of its own. The browser's own gestures stop over the
// canvas while any window takes fingers, whatever touch-action the page set.
func (w *Window) TakeFingers(on bool) error {
	d := w.d
	d.mu.Lock()
	if w.closed {
		d.mu.Unlock()
		return nil
	}
	w.takesFingers = on
	taking := false
	for _, x := range d.windows {
		taking = taking || x.takesFingers
	}
	d.mu.Unlock()
	action := d.touchAction
	if taking {
		action = "none"
	}
	d.canvas.Get("style").Set("touchAction", action)
	return nil
}

// fingerEvent hands pointer id's event to the window its finger came down
// in, if it is a finger of its own.
func (d *Driver) fingerEvent(id int, e js.Value, phase input.FingerPhase) {
	p, now := d.at(e), stamp(e)
	d.mu.Lock()
	w, ok := d.fingers[id]
	if !ok {
		d.mu.Unlock()
		return
	}
	pos := p.Sub(w.rectLocked().Min)
	if phase == input.FingerUp {
		delete(d.fingers, id)
		if e.Get("type").String() == "pointercancel" {
			pos = input.Away
		}
	}
	d.mu.Unlock()
	w.in.Push(input.Finger{ID: id, Pos: pos, Phase: phase, Time: now})
}

// HasCompass implements [driver.Compass]. The browser says only by sending a
// reading, so this is false until the first one has come, a moment after the
// page opens.
func (w *Window) HasCompass() bool {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.compass
}

// WatchHeading implements [driver.Compass].
func (w *Window) WatchHeading(on bool) {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if on {
		d.headingTo[w] = true
	} else {
		delete(d.headingTo, w)
	}
}

// heading takes a deviceorientationabsolute event. Its alpha turns
// anticlockwise from north, and a heading clockwise.
func (d *Driver) heading(e js.Value) {
	alpha := e.Get("alpha")
	if alpha.Type() != js.TypeNumber {
		return
	}
	reading := input.Heading{Degrees: headingOf(alpha.Float()), Accuracy: input.HeadingMedium, Time: time.Now()}
	d.mu.Lock()
	d.compass = true
	to := make([]*Window, 0, len(d.headingTo))
	for w := range d.headingTo {
		to = append(to, w)
	}
	d.mu.Unlock()
	for _, w := range to {
		w.in.Push(reading)
	}
}

// headingOf turns a deviceorientation alpha into degrees clockwise from north.
func headingOf(alpha float64) float32 {
	deg := math.Mod(360-alpha, 360)
	if deg < 0 {
		deg += 360
	}
	return float32(deg)
}

var (
	_ driver.Buzzer      = (*Window)(nil)
	_ driver.Vibrator    = (*Window)(nil)
	_ driver.Sharer      = (*Window)(nil)
	_ driver.FingerTaker = (*Window)(nil)
	_ driver.Compass     = (*Window)(nil)
)
