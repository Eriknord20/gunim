//go:build linux || windows || darwin

package desktop

import (
	"math"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
)

var _ driver.MinSizer = (*Window)(nil)

// SetMinSize implements [driver.MinSizer].
func (w *Window) SetMinSize(s geom.Size) error {
	return w.d.call(func() error {
		if w.closed {
			return nil
		}
		w.least = noNegative(s)
		return w.applyMinSize(growToLimit)
	})
}

// noNegative returns s with each negative side made zero.
func noNegative(s geom.Size) geom.Size { return geom.Sz(max(s.W, 0), max(s.H, 0)) }

// leastSizing is how applyMinSize sizes the window besides setting its limit.
type leastSizing uint8

const (
	// limitOnly sets the limit and leaves the window's size.
	limitOnly leastSizing = iota
	// growToLimit grows the window to the limit where it is smaller.
	growToLimit
	// fitToLimit grows the window to the limit, and brings it down to the limit on a side where the work area caps it.
	fitToLimit
)

// applyMinSize gives the system the window's least size in screen coordinates at its scale and zoom now, capped at
// its monitor's work area less its frame, and sizes the window as how says. It runs on the main thread.
func (w *Window) applyMinSize(how leastSizing) error {
	if w.closed || w.popup || w.flyout {
		return nil
	}
	if w.least.W <= 0 && w.least.H <= 0 {
		w.leastArea = geom.Rect{}
		return w.gw.SetSizeLimits(glfw.DontCare, glfw.DontCare, glfw.DontCare, glfw.DontCare)
	}
	x, y, err := w.gw.GetPos()
	if err != nil {
		return err
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return err
	}
	f := w.coordsPerLogical()
	mw, mh := int(math.Ceil(float64(w.least.W*f))), int(math.Ceil(float64(w.least.H*f)))
	capped := [2]bool{}
	w.leastArea = workArea(geom.Pt(float32(x+ww/2), float32(y+wh/2)))
	if !w.leastArea.Empty() {
		fl, ft, fr, fb := w.frameSize()
		aw, ah := int(w.leastArea.Size().W)-fl-fr, int(w.leastArea.Size().H)-ft-fb
		capped = [2]bool{mw > aw, mh > ah}
		mw, mh = max(min(mw, aw), 1), max(min(mh, ah), 1)
	}
	free := !w.FullScreen() && !attrib(w.gw, glfw.Maximized) && !attrib(w.gw, glfw.Iconified)
	nw, nh := ww, wh
	if how != limitOnly {
		nw, nh = max(ww, mw), max(wh, mh)
	}
	if how == fitToLimit {
		if capped[0] {
			nw = mw
		}
		if capped[1] {
			nh = mh
		}
	}
	// Sized before it is limited: a limit set first grows the window where it stands, off the work area.
	if free && (nw != ww || nh != wh) {
		w.debugf("sized from %dx%d to %dx%d for its least size", ww, wh, nw, nh)
		if err := w.setBoundsOnScreen(x, y, nw, nh); err != nil {
			return err
		}
	}
	return w.gw.SetSizeLimits(mw, mh, glfw.DontCare, glfw.DontCare)
}

// frameSize returns the frame the system draws round the window, or none for a window without one or where the
// system cannot say. It runs on the main thread.
func (w *Window) frameSize() (left, top, right, bottom int) {
	if w.Chromeless() {
		return 0, 0, 0, 0
	}
	l, t, r, b, err := w.gw.GetFrameSize()
	if err != nil {
		return 0, 0, 0, 0
	}
	return l, t, r, b
}

// minSizeAgain gives the system the least size again once the window's scale or zoom changed, or it was restored,
// and grows the window to it. It runs on the main thread.
func (w *Window) minSizeAgain() {
	if w.least == (geom.Size{}) {
		return
	}
	if err := w.applyMinSize(growToLimit); err != nil {
		w.debugf("least size not set again: %v", err)
	}
}

// minSizeMoved caps the least size again at the work area of the monitor the window moved onto. It runs on the main
// thread.
func (w *Window) minSizeMoved() {
	if w.closed || w.least == (geom.Size{}) {
		return
	}
	x, y, err := w.gw.GetPos()
	if err != nil {
		return
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return
	}
	if workArea(geom.Pt(float32(x+ww/2), float32(y+wh/2))) == w.leastArea {
		return
	}
	if err := w.applyMinSize(limitOnly); err != nil {
		w.debugf("least size not capped again: %v", err)
	}
}
