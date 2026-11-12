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
		w.least = geom.Sz(max(s.W, 0), max(s.H, 0))
		return w.applyMinSize()
	})
}

// applyMinSize gives the system the window's least size in screen coordinates at its scale and zoom now, and grows
// the window to it where it is smaller. It runs on the main thread.
func (w *Window) applyMinSize() error {
	if w.closed || w.popup || w.flyout {
		return nil
	}
	if w.least.W <= 0 && w.least.H <= 0 {
		return w.gw.SetSizeLimits(glfw.DontCare, glfw.DontCare, glfw.DontCare, glfw.DontCare)
	}
	f := w.coordsPerLogical()
	mw, mh := int(math.Ceil(float64(w.least.W*f))), int(math.Ceil(float64(w.least.H*f)))
	if err := w.gw.SetSizeLimits(mw, mh, glfw.DontCare, glfw.DontCare); err != nil {
		return err
	}
	if w.FullScreen() || attrib(w.gw, glfw.Maximized) || attrib(w.gw, glfw.Iconified) {
		return nil
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return err
	}
	if ww >= mw && wh >= mh {
		return nil
	}
	x, y, err := w.gw.GetPos()
	if err != nil {
		return err
	}
	w.debugf("grown from %dx%d to its least size %dx%d", ww, wh, max(ww, mw), max(wh, mh))
	return w.setBoundsOnScreen(x, y, max(ww, mw), max(wh, mh))
}

// minSizeAgain gives the system the least size again once the window's scale or zoom changed. It runs on the main
// thread.
func (w *Window) minSizeAgain() {
	if w.least == (geom.Size{}) {
		return
	}
	if err := w.applyMinSize(); err != nil {
		w.debugf("least size not set again: %v", err)
	}
}
