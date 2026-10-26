// SPDX-License-Identifier: Apache-2.0

package glfw

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file is a gunim change: telling a window it is out of sight while
// it stays open, so it can stop drawing. A window is out of sight when
// the screen is off or the session locked, which each window hears from
// the system; when the shell cloaks it, as it does the windows of
// another virtual desktop; and when other windows cover it whole, which
// the system never says. That last is worked out here, as Chromium does
// it: as windows anywhere move, show, hide or change order, which a
// WinEvent hook hears, the windows above each of ours are taken from its
// area, a moment later and once for a burst, and an area left empty is
// covered. Windows that let the pointer through, or that are see-through
// in part, cover nothing.

const (
	_WM_POWERBROADCAST       = 0x0218
	_PBT_POWERSETTINGCHANGE  = 0x8013
	_WM_WTSSESSION_CHANGE    = 0x02B1
	_WTS_SESSION_LOCK        = 0x7
	_WTS_SESSION_UNLOCK      = 0x8
	_DWM_CLOAKED_SHELL       = 0x2
	_OBJID_WINDOW            = 0
	_WINEVENT_OUTOFCONTEXT   = 0
	_GA_ROOT                 = 2
	_RGN_DIFF                = 4
	_NULLREGION              = 1
	_LWA_COLORKEY            = 0x1
	_DEVICE_NOTIFY_WINDOW    = 0
	_EVENT_OBJECT_DESTROY    = 0x8001
	_EVENT_OBJECT_HIDE       = 0x8003
	_NOTIFY_FOR_THIS_SESSION = 0
	// coveredDelay is how long after windows move the check waits, in
	// milliseconds, so a burst of moves costs one.
	coveredDelay = 100
)

// guidConsoleDisplayState is GUID_CONSOLE_DISPLAY_STATE: the screen
// going off, dim or on.
var guidConsoleDisplayState = windows.GUID{Data1: 0x6fe69556, Data2: 0x704a, Data3: 0x47a0,
	Data4: [8]byte{0x8f, 0x24, 0xc2, 0x8d, 0x93, 0x6f, 0xda, 0x47}}

var (
	procSetWinEventHook                    = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent                     = user32.NewProc("UnhookWinEvent")
	procGetAncestor                        = user32.NewProc("GetAncestor")
	procIsWindow                           = user32.NewProc("IsWindow")
	procRegisterPowerSettingNotification   = user32.NewProc("RegisterPowerSettingNotification")
	procUnregisterPowerSettingNotification = user32.NewProc("UnregisterPowerSettingNotification")
	procCombineRgn                         = gdi32.NewProc("CombineRgn")
	wtsapi32                               = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSRegisterSessionNotification     = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification   = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
)

// The window events that may cover or uncover a window, as ranges.
var coveringEvents = [][2]uint32{
	{0x0003, 0x0003}, // EVENT_SYSTEM_FOREGROUND
	{0x000B, 0x000B}, // EVENT_SYSTEM_MOVESIZEEND
	{0x0016, 0x0017}, // EVENT_SYSTEM_MINIMIZESTART, MINIMIZEEND
	{0x0020, 0x0020}, // EVENT_SYSTEM_DESKTOPSWITCH
	{0x8001, 0x8004}, // EVENT_OBJECT_DESTROY, SHOW, HIDE, REORDER
	{0x800B, 0x800B}, // EVENT_OBJECT_LOCATIONCHANGE
	{0x8017, 0x8018}, // EVENT_OBJECT_CLOAKED, UNCLOAKED
}

// covering is what the windows share: the hooks, the screen's state,
// and the check waiting on its timer. It is used on the main thread.
var covering struct {
	hooks             []uintptr
	screenOff, locked bool
	timer             uintptr
	hookProc          uintptr
	timerProc         uintptr
}

// coveringState is what a window keeps of its own.
type coveringState struct {
	power    uintptr
	session  bool
	occluded bool
}

// watchCovered starts telling w when it goes out of sight: the screen
// and the session tell it, and the hooks, set as the first window opens,
// tell of the windows about it.
func (w *Window) watchCovered() {
	h := w.platform.handle
	w.platform.covering.power, _, _ = procRegisterPowerSettingNotification.Call(uintptr(h),
		uintptr(unsafe.Pointer(&guidConsoleDisplayState)), _DEVICE_NOTIFY_WINDOW)
	r, _, _ := procWTSRegisterSessionNotification.Call(uintptr(h), _NOTIFY_FOR_THIS_SESSION)
	w.platform.covering.session = r != 0
	if covering.hooks == nil {
		if covering.hookProc == 0 {
			covering.hookProc = syscall.NewCallback(func(hook, event, hwnd, object, child, thread, at uintptr) uintptr {
				if int32(object) == _OBJID_WINDOW && child == 0 && (isTopLevel(windows.HWND(hwnd)) || gone(event, windows.HWND(hwnd))) {
					checkCoveredSoon()
				}
				return 0
			})
			covering.timerProc = syscall.NewCallback(func(hwnd, msg, id, at uintptr) uintptr {
				_, _, _ = procKillTimer.Call(0, covering.timer)
				covering.timer = 0
				checkCovered()
				return 0
			})
		}
		for _, e := range coveringEvents {
			hook, _, _ := procSetWinEventHook.Call(uintptr(e[0]), uintptr(e[1]), 0, covering.hookProc, 0, 0, _WINEVENT_OUTOFCONTEXT)
			if hook != 0 {
				covering.hooks = append(covering.hooks, hook)
			}
		}
	}
	checkCoveredSoon()
}

// stopCovered stops telling w, as it closes; the last window takes the
// hooks with it.
func (w *Window) stopCovered() {
	if p := w.platform.covering.power; p != 0 {
		_, _, _ = procUnregisterPowerSettingNotification.Call(p)
		w.platform.covering.power = 0
	}
	if w.platform.covering.session {
		_, _, _ = procWTSUnRegisterSessionNotification.Call(uintptr(w.platform.handle))
		w.platform.covering.session = false
	}
	if len(_glfw.windows) > 1 {
		// The windows it covered show again once it has gone.
		checkCoveredSoon()
		return
	}
	for _, hook := range covering.hooks {
		_, _, _ = procUnhookWinEvent.Call(hook)
	}
	covering.hooks = nil
	if covering.timer != 0 {
		_, _, _ = procKillTimer.Call(0, covering.timer)
		covering.timer = 0
	}
}

// coveredMessage hears the screen go off and on, and the session lock
// and unlock.
func (w *Window) coveredMessage(uMsg uint32, wParam _WPARAM, lParam _LPARAM) {
	switch uMsg {
	case _WM_POWERBROADCAST:
		if wParam != _PBT_POWERSETTINGCHANGE || lParam == 0 {
			return
		}
		// POWERBROADCAST_SETTING: the setting, the data's length, and
		// the data: 0 off, 1 on, 2 dimmed.
		s := (*struct {
			setting windows.GUID
			length  uint32
			data    uint32
		})(unsafe.Pointer(lParam))
		if s.setting != guidConsoleDisplayState {
			return
		}
		covering.screenOff = s.data == 0
	case _WM_WTSSESSION_CHANGE:
		switch wParam {
		case _WTS_SESSION_LOCK:
			covering.locked = true
		case _WTS_SESSION_UNLOCK:
			covering.locked = false
		default:
			return
		}
	default:
		return
	}
	tellCovered()
}

// isTopLevel reports whether h is a top-level window, the only kind
// that covers another program's.
func isTopLevel(h windows.HWND) bool {
	if h == 0 {
		return false
	}
	root, _, _ := procGetAncestor.Call(uintptr(h), _GA_ROOT)
	return windows.HWND(root) == h
}

// gone reports whether event tells of window h hiding or going that is
// gone already: the hooks hear of a window late, and one destroyed by
// then is no longer anyone's, top-level or not. It may have covered one
// of ours.
func gone(event uintptr, h windows.HWND) bool {
	if event != _EVENT_OBJECT_DESTROY && event != _EVENT_OBJECT_HIDE {
		return false
	}
	ok, _, _ := procIsWindow.Call(uintptr(h))
	return ok == 0
}

// checkCoveredSoon checks which windows are covered in a moment, once
// for however many changes come before.
func checkCoveredSoon() {
	if covering.timer != 0 {
		return
	}
	covering.timer, _, _ = procSetTimer.Call(0, 0, coveredDelay, covering.timerProc)
}

// checkCovered works out which of the application's windows other
// windows cover whole, walking the top-level windows from the front,
// and tells each window whether it is out of sight.
func checkCovered() {
	mine := map[windows.HWND]*Window{}
	for _, w := range _glfw.windows {
		if w.platform.handle != 0 {
			mine[w.platform.handle] = w
			w.platform.covering.occluded = false
		}
	}
	var above []_RECT
	h := _GetTopWindow()
	for step := 0; h != 0 && step < stackWalk; step++ {
		if w, ok := mine[h]; ok && _IsWindowVisible(h) && !_IsIconic(h) {
			if r, ok := frameBounds(h); ok {
				w.platform.covering.occluded = coveredBy(r, above)
			}
		}
		if covers(h) {
			if r, ok := frameBounds(h); ok && r.right > r.left && r.bottom > r.top {
				above = append(above, r)
			}
		}
		h = _GetWindow(h, _GW_HWNDNEXT)
	}
	tellCovered()
}

// covers reports whether window h hides what is under it: shown,
// opaque, and taking the pointer.
func covers(h windows.HWND) bool {
	if !_IsWindowVisible(h) || _IsIconic(h) || cloaked(h) || passesPointer(h) {
		return false
	}
	ex, err := _GetWindowLongW(h, _GWL_EXSTYLE)
	if err != nil || ex&_WS_EX_LAYERED == 0 {
		return err == nil
	}
	// A layered window covers only where it is opaque throughout; one
	// that draws its own alpha, as a shadow does, says nothing here and
	// covers nothing.
	_, alpha, flags, err := _GetLayeredWindowAttributes(h)
	return err == nil && flags&_LWA_COLORKEY == 0 && (flags&_LWA_ALPHA == 0 || alpha == 255)
}

// coveredBy reports whether the rectangles above cover r whole.
func coveredBy(r _RECT, above []_RECT) bool {
	if len(above) == 0 {
		return false
	}
	rgn, err := _CreateRectRgn(r.left, r.top, r.right, r.bottom)
	if err != nil {
		return false
	}
	defer func() { _ = _DeleteObject(_HGDIOBJ(rgn)) }()
	for _, a := range above {
		cut, err := _CreateRectRgn(a.left, a.top, a.right, a.bottom)
		if err != nil {
			return false
		}
		kind, _, _ := procCombineRgn.Call(uintptr(rgn), uintptr(rgn), uintptr(cut), _RGN_DIFF)
		_ = _DeleteObject(_HGDIOBJ(cut))
		if kind == _NULLREGION {
			return true
		}
	}
	return false
}

// shellCloaked reports whether the shell hides window h, as it hides
// the windows of another virtual desktop; the application's own cloak,
// held while a window waits on its first frame, is not the shell's.
func shellCloaked(h windows.HWND) bool {
	var v uint32
	r, _, _ := procDwmGetWindowAttribute.Call(uintptr(h), _DWMWA_CLOAKED, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	return r == 0 && v&_DWM_CLOAKED_SHELL != 0
}

// tellCovered tells each window whether it is out of sight.
func tellCovered() {
	for _, w := range _glfw.windows {
		if w.platform.handle == 0 {
			continue
		}
		w.inputCovered(covering.screenOff || covering.locked || w.platform.covering.occluded || shellCloaked(w.platform.handle))
	}
}
