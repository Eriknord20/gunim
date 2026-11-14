package glfw

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A flyout's ways out, a gunim addition: it takes the foreground as it shows, and a low-level mouse hook, up while a
// flyout is open, hears a press outside it, whether or not it ever had the keyboard. The hook runs on the main thread,
// which installs it, as the event loop pumps.

var (
	user32flyout          = windows.NewLazySystemDLL("user32.dll")
	procSetWindowsHookExW = user32flyout.NewProc("SetWindowsHookExW")
	procUnhookWindowsHook = user32flyout.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx    = user32flyout.NewProc("CallNextHookEx")
	outsideHook           uintptr
	outsideProcPtr        = windows.NewCallback(outsideProc)
	outsideWatch          = map[uintptr]outsideWatcher{}
	outsideNext           uintptr
)

const (
	whMouseLL       = 14
	wmLButtonDown   = 0x0201
	wmRButtonDown   = 0x0204
	wmMButtonDown   = 0x0207
	wmXButtonDown   = 0x020b
	wmNCLButtonDown = 0x00a1
)

// outsideWatcher is a window watched for a press outside it, and what such a press calls.
type outsideWatcher struct {
	hwnd windows.HWND
	fn   func()
}

// TakeForeground brings w to the front and gives it the keyboard, where Windows lets the program take the
// foreground, as for a while after the user's click on its tray icon. It must be called on the main thread.
func (w *Window) TakeForeground() error {
	if !_glfw.initialized {
		return NotInitialized
	}
	_, _, _ = procBringWindowToTop.Call(uintptr(w.platform.handle))
	if !_SetForegroundWindow(w.platform.handle) {
		return errors.New("glfw: Windows kept the foreground for another program")
	}
	return nil
}

// WatchOutsidePress calls fn on each press of a mouse button outside w, anywhere on the screen, and returns its number
// for UnwatchOutsidePress. It must be called on the main thread, and fn is called there.
func WatchOutsidePress(w *Window, fn func()) (uintptr, error) {
	if !_glfw.initialized {
		return 0, NotInitialized
	}
	if outsideHook == 0 {
		h, _, err := procSetWindowsHookExW.Call(whMouseLL, outsideProcPtr, uintptr(_glfw.platformWindow.instance), 0)
		if h == 0 {
			return 0, err
		}
		outsideHook = h
	}
	outsideNext++
	outsideWatch[outsideNext] = outsideWatcher{hwnd: w.platform.handle, fn: fn}
	return outsideNext, nil
}

// UnwatchOutsidePress stops the calls WatchOutsidePress started under id, and takes the hook down with the last. It
// must be called on the main thread.
func UnwatchOutsidePress(id uintptr) {
	delete(outsideWatch, id)
	if len(outsideWatch) == 0 && outsideHook != 0 {
		_, _, _ = procUnhookWindowsHook.Call(outsideHook)
		outsideHook = 0
	}
}

// msllHookStruct is MSLLHOOKSTRUCT, as far as its point.
type msllHookStruct struct {
	x, y int32
}

// outsideProc is the low-level mouse hook. It hands on every message, as a hook must, and keeps its own work short.
func outsideProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode >= 0 {
		switch wParam {
		case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmXButtonDown, wmNCLButtonDown:
			pt := (*msllHookStruct)(unsafe.Pointer(lParam))
			outsidePress(pt.x, pt.y)
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}

// OutsidePressAt does what the hook does for a press at x, y on the screen, for a test, which makes no real press. It
// must be called on the main thread.
func OutsidePressAt(x, y int32) { outsidePress(x, y) }

// outsidePress calls the watchers of every window a press at x, y on the screen lies outside.
func outsidePress(x, y int32) {
	for _, w := range outsideWatch {
		r, err := _GetWindowRect(w.hwnd)
		if err != nil {
			continue
		}
		if x < r.left || x >= r.right || y < r.top || y >= r.bottom {
			w.fn()
		}
	}
}
