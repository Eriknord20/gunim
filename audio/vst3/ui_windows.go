package vst3

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	registerClassEx  = user32.NewProc("RegisterClassExW")
	createWindowEx   = user32.NewProc("CreateWindowExW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	showWindow       = user32.NewProc("ShowWindow")
	setForeground    = user32.NewProc("SetForegroundWindow")
	setWindowPos     = user32.NewProc("SetWindowPos")
	adjustWindowRect = user32.NewProc("AdjustWindowRectEx")
	getMessage       = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postMessage      = user32.NewProc("PostMessageW")
	postQuitMessage  = user32.NewProc("PostQuitMessage")
	setTimer         = user32.NewProc("SetTimer")
	loadCursor       = user32.NewProc("LoadCursorW")
	getModuleHandle  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

const (
	wmDestroy = 0x0002
	wmSize    = 0x0005
	wmClose   = 0x0010
	wmTimer   = 0x0113
	wmApp     = 0x8000
)

type wndClassEx struct {
	size, style            uint32
	proc                   uintptr
	clsExtra, wndExtra     int32
	instance, icon, cursor uintptr
	background             uintptr
	menuName, className    *uint16
	iconSm                 uintptr
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
	private uint32
}

// msgWindow takes the work given to the plugins' thread, and its timer:
// a window, so they go on in the loops of a window's move and of a
// plugin's dialogs, as a thread's own messages would not.
var msgWindow uintptr

// windows are the editors' windows, by handle.
var windowsByHandle = map[uintptr]*window{}

var wndProcCallback = syscall.NewCallback(wndProc)

func wndProc(hwnd, m, wParam, lParam uintptr) uintptr {
	if hwnd == msgWindow {
		switch m {
		case wmApp:
			runWork()
			return 0
		case wmTimer:
			runTickers()
			return 0
		}
	}
	if w := windowsByHandle[hwnd]; w != nil {
		switch m {
		case wmClose:
			w.e.close()
			return 0
		case wmSize:
			if !w.sizing {
				w.e.resized(int(lParam&0xffff), int(lParam>>16&0xffff))
			}
		}
	}
	r, _, _ := defWindowProc.Call(hwnd, m, wParam, lParam)
	return r
}

var instance uintptr

// register registers the window class name.
func register(name string) *uint16 {
	cls, _ := windows.UTF16PtrFromString(name)
	cursor, _, _ := loadCursor.Call(0, 32512)
	wc := wndClassEx{proc: wndProcCallback, instance: instance, cursor: cursor, className: cls}
	wc.size = uint32(unsafe.Sizeof(wc))
	_, _, _ = registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	return cls
}

var editorClass *uint16

// uiRun runs the plugins' thread: a loop of its windows' messages.
func uiRun(ready chan struct{}) {
	instance, _, _ = getModuleHandle.Call(0)
	cls := register("gunimVST3Work")
	editorClass = register("gunimVST3Editor")
	const hwndMessage = ^uintptr(2) // HWND_MESSAGE, -3
	msgWindow, _, _ = createWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, instance, 0)
	_, _, _ = setTimer.Call(msgWindow, 1, uintptr(tickEvery.Milliseconds()), 0)
	close(ready)
	var m msg
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		_, _, _ = translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// quitUI ends the thread, run on it: its window goes, with its timer,
// and its loop ends.
func quitUI() {
	_, _, _ = destroyWindow.Call(msgWindow)
	msgWindow = 0
	_, _, _ = postQuitMessage.Call(0)
}

// wake tells the thread work waits.
func wake() { _, _, _ = postMessage.Call(msgWindow, wmApp, 0, 0) }

// platformType is the kind of window an editor attaches to here.
const platformType = "HWND"

// window is an editor's window.
type window struct {
	hwnd          uintptr
	style, styleX uintptr
	e             *editor
	sizing        bool
}

const (
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsThickFrame   = 0x00040000
	wsMinimizeBox  = 0x00020000
	wsMaximizeBox  = 0x00010000
	wsClipChildren = 0x02000000
	cwUseDefault   = 0x80000000
)

// newWindow makes a window of a client w×h, titled title, for e.
func newWindow(title string, w, h int, e *editor) (*window, error) {
	win := &window{e: e, style: wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren}
	if e.resizable {
		win.style |= wsThickFrame | wsMaximizeBox
	}
	t, _ := windows.UTF16PtrFromString(title)
	ww, wh := win.outer(w, h)
	hwnd, _, err := createWindowEx.Call(win.styleX, uintptr(unsafe.Pointer(editorClass)), uintptr(unsafe.Pointer(t)),
		win.style, cwUseDefault, cwUseDefault, uintptr(ww), uintptr(wh), 0, 0, instance, 0)
	if hwnd == 0 {
		return nil, err
	}
	win.hwnd = hwnd
	windowsByHandle[hwnd] = win
	return win, nil
}

// outer is the window's size of a client w×h.
func (w *window) outer(cw, ch int) (ww, wh int) {
	r := [4]int32{0, 0, int32(cw), int32(ch)}
	_, _, _ = adjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), w.style, 0, w.styleX)
	return int(r[2] - r[0]), int(r[3] - r[1])
}

func (w *window) handle() uintptr { return w.hwnd }

func (w *window) show() {
	_, _, _ = showWindow.Call(w.hwnd, 5)
	_, _, _ = setForeground.Call(w.hwnd)
}

func (w *window) raise() {
	_, _, _ = showWindow.Call(w.hwnd, 9) // SW_RESTORE
	_, _, _ = setForeground.Call(w.hwnd)
}

func (w *window) resize(cw, ch int) {
	ww, wh := w.outer(cw, ch)
	w.sizing = true
	_, _, _ = setWindowPos.Call(w.hwnd, 0, 0, 0, uintptr(ww), uintptr(wh), 0x2|0x4|0x10)
	w.sizing = false
}

func (w *window) destroy() {
	delete(windowsByHandle, w.hwnd)
	_, _, _ = destroyWindow.Call(w.hwnd)
}

// runLoop is for Linux, where a plugin's editor runs on the host's loop.
func runLoop() uintptr { return 0 }

// runWork runs the work waiting, without waiting for more.
func runWork() {
	for {
		select {
		case f := <-uiWork:
			f()
		default:
			return
		}
	}
}
