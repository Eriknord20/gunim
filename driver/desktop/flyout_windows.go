//go:build windows

package desktop

import (
	"time"

	"github.com/marrasen/gunim/internal/glfw"
)

// trayClick wraps click, the application's OnClick for the tray icon: a click that closed a flyout, as it took the
// keyboard or pressed outside it, closes it and no more, and opens no flyout again.
func (d *Driver) trayClick(click func()) func() {
	return func() {
		if d.flyoutJustLeft() {
			return
		}
		go click()
	}
}

// flyoutGrace is how long after a flyout closes as the user turns away a click on the tray icon is taken as part of
// closing it, not as a click of its own.
const flyoutGrace = 400 * time.Millisecond

// flyoutJustLeft reports whether a flyout closed as the user turned away within flyoutGrace.
func (d *Driver) flyoutJustLeft() bool {
	at := d.flyoutGone.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < flyoutGrace
}

// startFlyout gives a flyout on the screen its ways out: it takes the foreground, while the tray click that opened it
// lets the program, and a press anywhere outside it asks it to close, whether or not it ever had the keyboard. It runs
// on the main thread.
func startFlyout(w *Window) {
	if err := w.gw.TakeForeground(); err != nil {
		// Windows refused the foreground; the press outside still closes the flyout.
		w.debugf("flyout could not take the foreground: %v", err)
	}
	if w.outside != 0 {
		return
	}
	id, err := glfw.WatchOutsidePress(w.gw, w.flyoutLeft)
	if err != nil {
		w.debugf("flyout hears no press outside it: %v", err)
		return
	}
	w.outside = id
}

// stopFlyout takes down what startFlyout set up. It runs on the main thread.
func stopFlyout(w *Window) {
	if w.outside != 0 {
		glfw.UnwatchOutsidePress(w.outside)
		w.outside = 0
	}
}
