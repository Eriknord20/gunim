//go:build linux || windows || darwin

package desktop

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/internal/glfw"
)

// SetTitle implements [driver.Titler].
func (w *Window) SetTitle(title string) {
	w.d.post(func() {
		if !w.closed {
			_ = w.gw.SetTitle(title)
		}
	})
}

// SetBorder implements [driver.Borderer].
func (w *Window) SetBorder(b driver.Border) {
	w.mu.Lock()
	w.border = b
	w.mu.Unlock()
	w.d.post(func() {
		if w.closed {
			return
		}
		if err := applyBorder(w); err != nil {
			w.fail(err)
		}
	})
	// The edge the window leaves for the border changes with it
	w.in.Push(driver.Redraw{})
}

// RequestAttention implements [driver.Attender].
func (w *Window) RequestAttention() {
	w.d.post(func() {
		if !w.closed {
			_ = w.gw.RequestAttention()
		}
	})
}

// ToFront implements [driver.Fronter].
func (w *Window) ToFront() {
	w.d.post(func() {
		if w.closed {
			return
		}
		if on, err := w.gw.GetAttrib(glfw.Iconified); err == nil && on != 0 {
			_ = w.gw.Restore()
		}
		_ = w.gw.Show()
		_ = w.gw.Focus()
	})
}

// SetFullScreen implements [driver.FullScreener]. The window fills the
// monitor its middle is on, at that monitor's own mode, and going back
// puts it where it was, at the size it was.
func (w *Window) SetFullScreen(on bool) {
	w.mu.Lock()
	if w.full == on {
		w.mu.Unlock()
		return
	}
	w.full = on
	w.mu.Unlock()
	w.d.post(func() {
		if w.closed {
			return
		}
		if !on {
			r := w.windowed
			_ = w.gw.SetMonitor(nil, r[0], r[1], r[2], r[3], 0)
			w.minSizeAgain()
			return
		}
		x, y, _ := w.gw.GetPos()
		width, height, _ := w.gw.GetSize()
		w.windowed = [4]int{x, y, width, height}
		m := monitorAt(x+width/2, y+height/2)
		if m == nil {
			return
		}
		mode, err := m.GetVideoMode()
		if err != nil {
			return
		}
		_ = w.gw.SetMonitor(m, 0, 0, mode.Width, mode.Height, mode.RefreshRate)
	})
}

// FullScreen implements [driver.FullScreener].
func (w *Window) FullScreen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.full
}

// monitorAt is the monitor showing the point x, y in screen
// coordinates, or the primary one when none does. It runs on the main
// thread.
func monitorAt(x, y int) *glfw.Monitor {
	if ms, err := glfw.GetMonitors(); err == nil {
		for _, m := range ms {
			mx, my, perr := m.GetPos()
			mode, merr := m.GetVideoMode()
			if perr != nil || merr != nil {
				continue
			}
			if x >= mx && y >= my && x < mx+mode.Width && y < my+mode.Height {
				return m
			}
		}
	}
	m, err := glfw.GetPrimaryMonitor()
	if err != nil {
		return nil
	}
	return m
}
