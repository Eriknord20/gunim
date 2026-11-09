//go:build !js

package desktop

import (
	"fmt"
	"sync"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
)

// chrome is what a chromeless window knows of the title bar its
// application draws, for the system to ask about on the main thread.
type chrome struct {
	mu        sync.Mutex
	on        bool
	caption   []geom.Rect
	maximize  geom.Rect
	maximized bool
}

// setChromeless takes the system's title bar away, and answers the
// system's questions about points from the title bar the application
// reports. It runs on the main thread.
func (w *Window) setChromeless() {
	if err := w.gw.SetChromeless(true); err != nil || !w.gw.Chromeless() {
		return
	}
	w.chrome.on = true
	startShadow(w)
	w.gw.SetHitTestCallback(func(_ *glfw.Window, x, y int) glfw.Hit {
		f := w.coordsPerLogical()
		if f <= 0 {
			f = 1
		}
		p := geom.Pt(float32(x)/f, float32(y)/f)
		w.chrome.mu.Lock()
		defer w.chrome.mu.Unlock()
		if w.chrome.maximize.Contains(p) {
			return glfw.HitMaximize
		}
		for _, r := range w.chrome.caption {
			if !r.Contains(p) {
				continue
			}
			if w.chrome.maximize.Empty() {
				// No maximize button: a double click does not maximize.
				return glfw.HitCaptionOnly
			}
			return glfw.HitCaption
		}
		return glfw.HitClient
	})
}

// Chromeless implements [driver.Framer].
func (w *Window) Chromeless() bool {
	w.chrome.mu.Lock()
	defer w.chrome.mu.Unlock()
	return w.chrome.on
}

// SetTitleBar implements [driver.Framer].
func (w *Window) SetTitleBar(caption []geom.Rect, maximize geom.Rect) {
	w.chrome.mu.Lock()
	defer w.chrome.mu.Unlock()
	w.chrome.caption = append(w.chrome.caption[:0], caption...)
	w.chrome.maximize = maximize
}

// NativeFrame implements [driver.Framer]: Windows moves and sizes a
// window from its hit test.
func (w *Window) NativeFrame() bool { return nativeFrame }

// StartMove implements [driver.Framer].
func (w *Window) StartMove() error {
	return w.d.call(func() error { return w.gw.StartMove() })
}

// StartResize implements [driver.Framer].
func (w *Window) StartResize(e driver.Edge) error {
	return w.d.call(func() error { return w.gw.StartResize(glfw.Edge(e)) })
}

// Minimize implements [driver.Framer].
func (w *Window) Minimize() error {
	return w.d.call(func() error { return w.gw.Iconify() })
}

// SetPinned implements [driver.Pinner].
func (w *Window) SetPinned(on bool) error {
	v := glfw.False
	if on {
		v = glfw.True
	}
	return w.d.call(func() error { return w.gw.SetAttrib(glfw.Floating, v) })
}

// SetMaximized implements [driver.Framer]. It returns before the window has changed size, so the caller can draw the
// window at its new size meanwhile; an error fails the window.
func (w *Window) SetMaximized(on bool) error {
	posted := w.d.post(func() {
		var err error
		if on {
			err = w.gw.Maximize()
		} else {
			err = w.gw.Restore()
		}
		if err != nil {
			w.fail(fmt.Errorf("desktop: maximize: %w", err))
		}
	})
	if !posted {
		return errStopped
	}
	return nil
}

// Maximized implements [driver.Framer].
func (w *Window) Maximized() bool {
	w.chrome.mu.Lock()
	defer w.chrome.mu.Unlock()
	return w.chrome.maximized
}
