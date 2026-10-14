//go:build linux || windows || darwin

package desktop

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
)

var _ driver.Reshaper = (*Window)(nil)

// Reshape implements [driver.Reshaper].
func (w *Window) Reshape(by geom.Insets) error {
	return w.d.call(func() error {
		if w.closed || w.popup || w.FullScreen() || attrib(w.gw, glfw.Maximized) {
			return nil
		}
		f := w.coordsPerLogical()
		x, y, err := w.gw.GetPos()
		if err != nil {
			return err
		}
		ww, wh, err := w.gw.GetSize()
		if err != nil {
			return err
		}
		left, top := int(by.Left*f+0.5*sign(by.Left)), int(by.Top*f+0.5*sign(by.Top))
		right, bottom := int(by.Right*f+0.5*sign(by.Right)), int(by.Bottom*f+0.5*sign(by.Bottom))
		x, y = x-left, y-top
		ww, wh = max(ww+left+right, 1), max(wh+top+bottom, 1)
		// The frame the system draws round a window that keeps it counts
		// as the window keeps on its monitor.
		fl, ft, fr, fb := 0, 0, 0, 0
		if !w.Chromeless() {
			if fl, ft, fr, fb, err = w.gw.GetFrameSize(); err != nil {
				fl, ft, fr, fb = 0, 0, 0, 0
			}
		}
		area := workArea(geom.Pt(float32(x+ww/2), float32(y+wh/2)))
		if !area.Empty() {
			x = keepOn(x, ww, fl, fr, int(area.Min.X), int(area.Max.X))
			y = keepOn(y, wh, ft, fb, int(area.Min.Y), int(area.Max.Y))
		}
		// The engine waits on this call, and draws the frame at the new
		// size after it.
		w.placing = true
		err = w.gw.SetBounds(x, y, ww, wh)
		w.placing = false
		return err
	})
}

// keepOn moves a span at at, n long with a frame lo and hi round it,
// onto lo..hi: from the far end in, and then from the near end, so a
// span too long for it shows its start.
func keepOn(at, n, before, after, lo, hi int) int {
	if at+n+after > hi {
		at = hi - n - after
	}
	if at-before < lo {
		at = lo + before
	}
	return at
}

// sign is 1 for a positive v, -1 for a negative one, and 0 for zero.
func sign(v float32) float32 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
