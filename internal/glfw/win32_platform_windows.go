// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2002-2006 Marcus Geelnard
// SPDX-FileCopyrightText: 2006-2019 Camilla Löwy <elmindreda@glfw.org>
// SPDX-FileCopyrightText: 2022 The Ebitengine Authors

package glfw

import (
	"golang.org/x/sys/windows"
)

const (
	_GLFW_WNDCLASSNAME = "GLFW30"
)

type platformWindowState struct {
	handle windows.HWND
	// covering is what the window keeps to hear it is out of sight; a
	// gunim change.
	covering  coveringState
	bigIcon   _HICON
	smallIcon _HICON

	cursorTracked  bool
	frameAction    bool
	iconified      bool
	maximized      bool
	transparent    bool // Whether to enable framebuffer transparency on DWM
	scaleToMonitor bool

	// noRedirectionBitmap is true when the window is created without a redirection surface
	// (WS_EX_NOREDIRECTIONBITMAP), so DWM does not stretch stale content while the window is being
	// resized (#3477). It is only enabled for windows that present through DirectComposition.
	noRedirectionBitmap bool

	// popup is set for a window made with the Popup hint. A gunim change.
	popup bool

	// owner is the window a popup stays just above, set by SetOwner, or 0 for a popup above every window. A gunim
	// change.
	owner windows.HWND

	// Cached size used to filter out duplicate events
	width  int
	height int

	// The last received cursor position, regardless of source
	lastCursorPosX int
	lastCursorPosY int

	// The last received high surrogate when decoding pairs of UTF-16 messages
	highSurrogate uint16

	// gunim change: the input method's state (win32_ime_windows.go). The
	// preedit fields hold the composition reported last, and imeCaret the
	// text caret in client-area pixels.
	preeditText             string
	preeditStart            int
	preeditEnd              int
	imeCaret                _RECT
	preeditCallback         PreeditCallback
	textInputCallback       TextInputCallback
	textInputActiveCallback TextInputActiveCallback
	getObjectCallback       GetObjectCallback

	// gunim change: chromeless says the window draws its own title bar,
	// and overMaximize that the pointer is on its maximize button, which
	// Windows sees as outside the client area (win32_chrome_windows.go).
	chromeless bool
	// frameHidden says SetFrameHidden took the frame away (gunim).
	frameHidden bool
	// shadow is the window a drawn shadow is drawn in, or nil (gunim).
	shadow *drawnShadow
	// moveSize is told when the user starts moving or sizing the window by its frame (gunim).
	moveSize func(w *Window)
	// colorChange is told when the colours of the window's display may have changed (gunim); see
	// win32_color_windows.go.
	colorChange func(w *Window)
	// history is told of a Browser Back or Forward command (gunim).
	history func(w *Window, forward bool)
	// border is the window's border, or nil, and ncActive whether the window is drawn active (gunim).
	border       *windowBorder
	ncActive     bool
	overMaximize bool
}

type platformMonitorState struct {
	handle _HMONITOR

	// This size matches the static size of DISPLAY_DEVICE.DeviceName
	adapterName string
	displayName string
	modesPruned bool
	modeChanged bool
}

type platformCursorState struct {
	handle _HCURSOR
}

type platformLibraryWindowState struct {
	instance                 _HINSTANCE
	helperWindowHandle       windows.HWND
	deviceNotificationHandle _HDEVNOTIFY
	acquiredMonitorCount     int
	clipboardString          string
	keycodes                 [512]Key
	scancodes                [KeyLast + 1]int
	keynames                 [KeyLast + 1]string

	// restoreCursorPosX and restoreCursorPosY indicates where to place the cursor when re-enabled
	restoreCursorPosX float64
	restoreCursorPosY float64

	// disabledCursorWindow is the window whose disabled cursor mode is active
	disabledCursorWindow *Window
	// capturedCursorWindow is the window the cursor is captured in
	capturedCursorWindow *Window
	rawInput             []byte
	mouseTrailSize       uint32
	// isRemoteSession indicates if the process was started behind Remote Destop
	isRemoteSession bool
	// blankCursor is an invisible cursor, needed for special cases (see WM_INPUT handler)
	blankCursor _HCURSOR
}
