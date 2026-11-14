package glfw

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Settings changes, a gunim addition: a hidden window of the top level hears WM_SETTINGCHANGE and the colour and
// theme changes that come with a contrast theme, on the main thread. It is on Windows alone.

var (
	lookClassRegistered bool
	lookWindow          windows.HWND
	lookProcPtr         = windows.NewCallback(lookProc)
	lookCalls           = map[uintptr]func(){}
	lookNext            uintptr
	lookClassName       = "GunimSettings"
)

const (
	wmSysColorChange = 0x0015
	wmThemeChanged   = 0x031a
)

// WatchSettings calls fn each time the user changes a system setting, and returns its number for UnwatchSettings.
// It must be called on the main thread, and fn is called there.
func WatchSettings(fn func()) (uintptr, error) {
	if !_glfw.initialized {
		return 0, NotInitialized
	}
	if lookWindow == 0 {
		if !lookClassRegistered {
			var wc _WNDCLASSEXW
			wc.cbSize = uint32(unsafe.Sizeof(wc))
			wc.lpfnWndProc = _WNDPROC(lookProcPtr)
			wc.hInstance = _glfw.platformWindow.instance
			name, err := windows.UTF16PtrFromString(lookClassName)
			if err != nil {
				return 0, err
			}
			wc.lpszClassName = name
			if _, err := _RegisterClassExW(&wc); err != nil {
				return 0, err
			}
			lookClassRegistered = true
		}
		// Of the top level, as a broadcast of a setting reaches no window for messages alone.
		h, err := _CreateWindowExW(wsExTool, lookClassName, "", wsPopup, 0, 0, 0, 0, 0, 0, _glfw.platformWindow.instance, nil)
		if err != nil {
			return 0, err
		}
		lookWindow = h
	}
	lookNext++
	lookCalls[lookNext] = fn
	return lookNext, nil
}

// UnwatchSettings stops the calls WatchSettings started under id. It must be called on the main thread.
func UnwatchSettings(id uintptr) {
	delete(lookCalls, id)
	if len(lookCalls) == 0 && lookWindow != 0 {
		_ = _DestroyWindow(lookWindow)
		lookWindow = 0
	}
}

// SettingsWindow returns the window WatchSettings hears changes on, or 0 while nothing watches, for a test to send
// it a change.
func SettingsWindow() uintptr { return uintptr(lookWindow) }

// lookProc hears the settings window's messages.
func lookProc(hWnd windows.HWND, uMsg uint32, wParam _WPARAM, lParam _LPARAM) uintptr {
	switch uMsg {
	case _WM_SETTINGCHANGE, wmSysColorChange, wmThemeChanged:
		if hWnd == lookWindow {
			for _, fn := range lookCalls {
				fn()
			}
		}
		return 0
	}
	return uintptr(_DefWindowProcW(hWnd, uMsg, wParam, lParam))
}
