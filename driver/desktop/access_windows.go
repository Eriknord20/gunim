//go:build windows && (amd64 || arm64)

package desktop

import (
	"sync/atomic"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/internal/glfw"
)

// driverAccess is empty on Windows, where each window answers UI
// Automation on its own.
type driverAccess struct{} //nolint:unused // used on Linux

// windowAccess is a window's place in UI Automation, set on the main
// thread and read by the engine's.
type windowAccess struct {
	uw atomic.Pointer[uiaWindow]
}

// accessOpen has the window answer UI Automation. It runs on the main
// thread.
func (w *Window) accessOpen() {
	hwnd, err := w.gw.GetWin32Window()
	if err != nil {
		return
	}
	uw := newUIAWindow(w, uintptr(hwnd))
	w.acc.uw.Store(uw)
	_, _ = w.gw.SetGetObjectCallback(func(_ *glfw.Window, wParam, lParam uintptr) (uintptr, bool) {
		return uw.getObject(wParam, lParam)
	})
}

// accessClose stops the window answering UI Automation. It runs on the
// main thread.
func (w *Window) accessClose() {
	if uw := w.acc.uw.Swap(nil); uw != nil {
		_, _ = w.gw.SetGetObjectCallback(nil)
		uw.close()
	}
}

// accessFocus tells screen readers where the focus is when the window
// takes the keyboard. The system tells them the window did.
func (w *Window) accessFocus(in bool) {
	if uw := w.acc.uw.Load(); uw != nil && in {
		uw.focusChanged()
	}
}

// AccessWanted implements [driver.AccessPublisher].
func (w *Window) AccessWanted() bool {
	uw := w.acc.uw.Load()
	return uw != nil && uw.asked.Load()
}

// Announce implements [driver.Announcer].
func (w *Window) Announce(text string) bool {
	uw := w.acc.uw.Load()
	return uw != nil && uw.announce(text)
}

// PublishAccess implements [driver.AccessPublisher].
func (w *Window) PublishAccess(t *access.Tree) {
	if uw := w.acc.uw.Load(); uw != nil {
		uw.publish(t)
	}
}
