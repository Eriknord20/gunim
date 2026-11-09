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
	p, err := call(nav, "share", map[string]any{"title": s.Subject, "text": s.Text})
	if err == nil {
		// The share sheet stays until the user closes it.
		_, err = await(p, 0)
	}
	var closed *browserError
	if errors.As(err, &closed) && closed.name == "AbortError" {
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
// reading, so the first call listens until one comes, and this is false until it
// does. A page gets no readings while hidden, and a device with no sensors sends
// none, or one with no alpha, so the listening costs nothing until a reading ends it.
func (w *Window) HasCompass() bool {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.compassAsked {
		d.compassAsked, d.probing = true, true
		d.runCompassLocked()
	}
	return d.compass
}

// WatchHeading implements [driver.Compass]. The browser runs the phone's sensors
// while the page listens for them, so it listens only while a window watches.
func (w *Window) WatchHeading(on bool) {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if on && !w.closed {
		d.headingTo[w] = true
	} else {
		delete(d.headingTo, w)
	}
	d.runCompassLocked()
}

// runCompassLocked listens for readings while a window watches them, or while the
// driver looks for a first one. It runs with mu held.
func (d *Driver) runCompassLocked() {
	want := (d.probing || len(d.headingTo) > 0) && !d.stopped
	if want == d.compassOn {
		return
	}
	d.compassOn = want
	win := js.Global()
	if want {
		win.Call("addEventListener", "deviceorientationabsolute", d.compassEvent, map[string]any{"passive": true})
	} else {
		win.Call("removeEventListener", "deviceorientationabsolute", d.compassEvent)
	}
}

// heading takes a deviceorientationabsolute event. Its alpha turns
// anticlockwise from north, and a heading clockwise. A browser can send one with
// no alpha, with no sensors or before its first reading, and that answers nothing.
func (d *Driver) heading(e js.Value) {
	alpha := e.Get("alpha")
	if alpha.Type() != js.TypeNumber {
		return
	}
	d.mu.Lock()
	first := !d.compass
	d.probing, d.compass = false, true
	to := make([]*Window, 0, len(d.headingTo))
	for w := range d.headingTo {
		to = append(to, w)
	}
	ws := append([]*Window(nil), d.windows...)
	d.runCompassLocked()
	d.mu.Unlock()
	// The engine starts a window's watch after a frame, so a first reading asks for one.
	if first {
		for _, w := range ws {
			w.in.Push(driver.Redraw{})
		}
	}
	reading := input.Heading{Degrees: headingOf(alpha.Float()), Accuracy: input.HeadingMedium, Time: stamp(e)}
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
