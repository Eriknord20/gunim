// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. It draws a chromeless window's
// shadow and border in four windows of its own, one along each edge:
// click-through layered windows, owned by the chromeless window, that
// follow it from the window's own messages.

package glfw

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	_WM_SHOWWINDOW       = 0x0018
	_WM_TIMER            = 0x0113
	_WM_SETTINGCHANGE    = 0x001A
	_WM_WINDOWPOSCHANGED = 0x0047

	_SW_PARENTOPENING  = 3
	_SW_SHOWNOACTIVATE = 4
	_HTTRANSPARENT     = ^uintptr(0)
	_ULW_ALPHA         = 2
	_AC_SRC_ALPHA      = 1

	shadowClassName = "gunim shadow"

	// shadowTimer is the main window's timer that fades the shadow back in once the window has restored.
	shadowTimer = 0x67756e69
	// shadowRestoreDelay is how long the shadow waits for the restore animation, in milliseconds, and
	// shadowRedrawDelay for the window's first frame where Windows does not animate.
	shadowRestoreDelay = 150
	shadowRedrawDelay  = 50
	// shadowFadeIn is how long the shadow takes to come back after it, in milliseconds.
	shadowFadeIn = 80
	shadowTick   = 16
)

var (
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteDC            = gdi32.NewProc("DeleteDC")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procBeginDeferWindowPos = user32.NewProc("BeginDeferWindowPos")
	procDeferWindowPos      = user32.NewProc("DeferWindowPos")
	procEndDeferWindowPos   = user32.NewProc("EndDeferWindowPos")

	shadowClassRegistered bool
)

// drawnShadow is the windows a chromeless window's shadow is drawn in: strips along its top, bottom, left and right
// edges, which leave the middle of the window uncovered for tools that pick a window by its rectangle.
type drawnShadow struct {
	hwnds [4]windows.HWND
	dc    _HDC
	bmp   _HBITMAP
	// measure draws solid green bands instead of a shadow, for measuring how closely it follows, and corner is the
	// radius of the window's corners, in logical pixels.
	measure bool
	corner  float64

	// size, dpi and the window's size are what the bitmap was drawn for.
	w, h, dpi int32
	winW      int32
	winH      int32
	// left and top are how far the shadow reaches past the window's left and top edges, and inX and inY how far the
	// bitmap draws inside them, where the strips reach.
	left, top int32
	inX, inY  int32
	x, y      int32
	alpha     uint8

	// opacity is the application's, restore the fade back in after the window restores, and scale how large the
	// window is drawn, about its middle, as it comes and goes.
	opacity float32
	restore float32
	scale   float32
	// bits are the bitmap's pixels.
	bits []uint32

	minimized bool
	// active says the window is active, which draws a stronger shadow, and drawnActive what the bitmap was drawn for.
	active      bool
	drawnActive bool
	// restyled says the border changed since the last drawing.
	restyled bool
}

// SetDrawnShadow draws the window's shadow and border in a window of gunim's own, or with measure set, green bands
// for measuring, cut round at corners of radius corner, in logical pixels; 0 cuts none. The window's own corners stay
// square, so Windows draws no shadow of its own. The shadow stays hidden
// until SetShadowOpacity shows it. It is a gunim addition, on Windows alone.
func (w *Window) SetDrawnShadow(measure bool, corner float64) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	if !w.platform.chromeless || w.platform.shadow != nil {
		return nil
	}
	if !shadowClassRegistered {
		var wc _WNDCLASSEXW
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		wc.lpfnWndProc = _WNDPROC(windows.NewCallback(shadowProc))
		wc.hInstance = _glfw.platformWindow.instance
		name, err := windows.UTF16PtrFromString(shadowClassName)
		if err != nil {
			return err
		}
		wc.lpszClassName = name
		if _, err := _RegisterClassExW(&wc); err != nil {
			return err
		}
		shadowClassRegistered = true
	}
	s := &drawnShadow{measure: measure, corner: corner, restore: 1, scale: 1}
	for i := range s.hwnds {
		h, err := _CreateWindowExW(_WS_EX_LAYERED|_WS_EX_TRANSPARENT|_WS_EX_TOOLWINDOW|_WS_EX_NOACTIVATE, shadowClassName,
			"", _WS_POPUP, 0, 0, 1, 1, w.platform.handle, 0, _glfw.platformWindow.instance, nil)
		if err != nil {
			_ = s.destroy()
			return err
		}
		s.hwnds[i] = h
	}
	dc, _, e := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		_ = s.destroy()
		return e
	}
	s.dc = _HDC(dc)
	w.platform.shadow = s
	w.roundCorners(false)
	return nil
}

// SetShadowFade shows the drawn shadow and border at opacity o, or hides them at 0, round the window drawn scale times
// its size about its middle, as it comes and goes.
func (w *Window) SetShadowFade(o, scale float32) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	s := w.platform.shadow
	if s == nil {
		return nil
	}
	s.opacity = o
	if scale != s.scale {
		s.scale, s.restyled = scale, true
	}
	return w.placeShadow()
}

// shadowProc lets the pointer through the shadow, and keeps it hidden as its window restores; see shadowMessage.
func shadowProc(hWnd windows.HWND, uMsg uint32, wParam _WPARAM, lParam _LPARAM) uintptr {
	switch uMsg {
	case _WM_NCHITTEST:
		return _HTTRANSPARENT
	case _WM_MOUSEACTIVATE:
		return _MA_NOACTIVATE
	case _WM_SHOWWINDOW:
		if wParam != 0 && lParam == _SW_PARENTOPENING {
			return 0
		}
	}
	return uintptr(_DefWindowProcW(hWnd, uMsg, wParam, lParam))
}

// shadowMessage keeps the drawn shadow with its window as it moves, sizes, is activated, minimizes and restores.
func (w *Window) shadowMessage(uMsg uint32, wParam _WPARAM) {
	s := w.platform.shadow
	if s == nil {
		return
	}
	switch uMsg {
	case _WM_WINDOWPOSCHANGED:
	case _WM_NCACTIVATE:
		s.active = wParam != 0
	case _WM_SIZE:
		switch {
		case wParam == _SIZE_MINIMIZED:
			s.minimized = true
		case s.minimized:
			// Held back until the window is back on the screen, then faded in
			s.minimized = false
			s.restore = 0
			delay := uintptr(shadowRedrawDelay)
			if minimizeAnimates() {
				delay = shadowRestoreDelay
			}
			procSetTimer.Call(uintptr(w.platform.handle), shadowTimer, delay, 0)
		}
	case _WM_TIMER:
		if wParam != shadowTimer {
			return
		}
		s.restore = min(s.restore+float32(shadowTick)/shadowFadeIn, 1)
		if s.restore >= 1 {
			procKillTimer.Call(uintptr(w.platform.handle), shadowTimer)
		} else {
			procSetTimer.Call(uintptr(w.platform.handle), shadowTimer, shadowTick, 0)
		}
	default:
		return
	}
	if err := w.placeShadow(); err != nil {
		_glfw.errors = append(_glfw.errors, err)
	}
}

// minimizeAnimates reports whether Windows animates a window as it minimizes and restores.
func minimizeAnimates() bool {
	const spiGetAnimation = 0x0048
	info := struct {
		size    uint32
		animate int32
	}{size: 8}
	// Where Windows will not say, the shadow waits, which is safe either way
	if err := _SystemParametersInfoW(spiGetAnimation, 8, uintptr(unsafe.Pointer(&info)), 0); err != nil {
		return true
	}
	return info.animate != 0
}

// placeShadow puts the drawn shadow round the window, drawing it again where the window's size has changed, or hides
// it where the window is hidden, minimized, maximized or fills its monitor.
func (w *Window) placeShadow() error {
	s := w.platform.shadow
	main := w.platform.handle
	alpha := s.opacity * s.restore
	if alpha <= 0 || !_IsWindowVisible(main) || _IsIconic(main) || _IsZoomed(main) || w.monitor != nil {
		s.hide()
		return nil
	}
	r, err := _GetWindowRect(main)
	if err != nil {
		return err
	}
	dpi := int32(_GetDpiForWindow(main))
	winW, winH := r.right-r.left, r.bottom-r.top
	redraw := winW != s.winW || winH != s.winH || dpi != s.dpi || s.active != s.drawnActive || s.restyled
	if redraw {
		if err := s.draw(winW, winH, dpi, w.platform.border); err != nil {
			return err
		}
	}
	x, y := r.left-s.left, r.top-s.top
	a := uint8(alpha*255 + 0.5)
	strips := s.strips()
	if redraw || a != s.alpha {
		if err := s.update(x, y, a, strips); err != nil {
			return err
		}
		s.alpha = a
	} else if x != s.x || y != s.y {
		if err := s.move(x, y, strips); err != nil {
			return err
		}
	}
	s.x, s.y = x, y
	for i, st := range strips {
		if st.w > 0 && st.h > 0 && !_IsWindowVisible(s.hwnds[i]) {
			_ShowWindow(s.hwnds[i], _SW_SHOWNOACTIVATE)
		}
	}
	return nil
}

// A strip is the part of the bitmap one of the shadow's windows shows, in bitmap pixels.
type strip struct{ x, y, w, h int32 }

// strips are the parts of the bitmap along the window's top, bottom, left and right edges: everything the bitmap draws.
func (s *drawnShadow) strips() [4]strip {
	top, bottom := s.top+s.inY, s.top+s.winH-s.inY
	right := s.left + s.winW - s.inX
	return [4]strip{
		{0, 0, s.w, top},
		{0, bottom, s.w, s.h - bottom},
		{0, top, s.left + s.inX, bottom - top},
		{right, top, s.w - right, bottom - top},
	}
}

// update shows the bitmap in the shadow's windows at opacity a, with the bitmap's top left at x, y on the screen.
func (s *drawnShadow) update(x, y int32, a uint8, strips [4]strip) error {
	screen, err := _GetDC(0)
	if err != nil {
		return err
	}
	defer _ReleaseDC(0, screen)
	blend := [4]byte{0, 0, a, _AC_SRC_ALPHA}
	for i, st := range strips {
		if st.w <= 0 || st.h <= 0 {
			if _IsWindowVisible(s.hwnds[i]) {
				_ShowWindow(s.hwnds[i], _SW_HIDE)
			}
			continue
		}
		pos := _POINT{x: x + st.x, y: y + st.y}
		size := [2]int32{st.w, st.h}
		src := _POINT{x: st.x, y: st.y}
		ok, _, e := procUpdateLayeredWindow.Call(uintptr(s.hwnds[i]), uintptr(screen), uintptr(unsafe.Pointer(&pos)),
			uintptr(unsafe.Pointer(&size)), uintptr(s.dc), uintptr(unsafe.Pointer(&src)), 0,
			uintptr(unsafe.Pointer(&blend)), _ULW_ALPHA)
		if ok == 0 {
			return e
		}
	}
	return nil
}

// move moves the shadow's windows together, with the bitmap's top left to x, y on the screen.
func (s *drawnShadow) move(x, y int32, strips [4]strip) error {
	dwp, _, e := procBeginDeferWindowPos.Call(uintptr(len(strips)))
	if dwp == 0 {
		return e
	}
	for i, st := range strips {
		dwp, _, e = procDeferWindowPos.Call(dwp, uintptr(s.hwnds[i]), 0, uintptr(x+st.x), uintptr(y+st.y), 0, 0,
			_SWP_NOSIZE|_SWP_NOZORDER|_SWP_NOACTIVATE)
		if dwp == 0 {
			return e
		}
	}
	if ok, _, e := procEndDeferWindowPos.Call(dwp); ok == 0 {
		return e
	}
	return nil
}

// hide hides the shadow's windows.
func (s *drawnShadow) hide() {
	for _, h := range s.hwnds {
		if _IsWindowVisible(h) {
			_ShowWindow(h, _SW_HIDE)
		}
	}
}

// destroy destroys the shadow's windows.
func (s *drawnShadow) destroy() error {
	var first error
	for _, h := range s.hwnds {
		if h == 0 {
			continue
		}
		if err := _DestroyWindow(h); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// draw draws the shadow and border for a window of winW by winH pixels at dpi, into a premultiplied bitmap; see
// paintShadow.
func (s *drawnShadow) draw(winW, winH, dpi int32, b *windowBorder) error {
	k := float64(dpi) / 96
	side, top, bottom := shadowMargins(k)
	if s.measure {
		side, top, bottom = 24, 24, 24
	}
	W, H := winW+2*side, winH+top+bottom
	// A bitmap of the same size is drawn again in place, as it is on every frame of a fade
	if s.bmp == 0 || W != s.w || H != s.h {
		var bi _BITMAPV5HEADER
		bi.bV5Size = uint32(unsafe.Sizeof(bi))
		bi.bV5Width = W
		bi.bV5Height = -H
		bi.bV5Planes = 1
		bi.bV5BitCount = 32
		bmp, bits, err := _CreateDIBSection(0, &bi, _DIB_RGB_COLORS, 0, 0)
		if err != nil {
			return err
		}
		procSelectObject.Call(uintptr(s.dc), uintptr(bmp))
		if s.bmp != 0 {
			_ = _DeleteObject(_HGDIOBJ(s.bmp))
		}
		s.bmp = bmp
		s.bits = unsafe.Slice((*uint32)(unsafe.Pointer(bits)), int(W)*int(H))
	}
	s.w, s.h, s.dpi, s.winW, s.winH, s.left, s.top, s.drawnActive = W, H, dpi, winW, winH, side, top, s.active
	s.restyled = false
	s.inX, s.inY = 17, 17
	px := s.bits

	if s.measure {
		// The window's rectangle in the bitmap
		x0, y0, x1, y1 := side, top, side+winW, top+winH
		clear(px)
		for y := int32(0); y < H; y++ {
			for x := int32(0); x < W; x++ {
				d := max(x0-x, x-x1+1, y0-y, y-y1+1)
				if d > 0 && d <= 16 {
					px[y*W+x] = 0xFF00FF00
				}
			}
		}
		return nil
	}
	l := shadowLook{winW: winW, winH: winH, k: k, corner: s.corner, scale: s.scale, active: s.active}
	if b != nil {
		l.edge, l.bc = b.drawnWidth(), b.now.color
	}
	s.inX, s.inY = paintShadow(px, W, H, side, top, l)
	return nil
}

// dropShadow destroys the drawn shadow's window and bitmap.
func (w *Window) dropShadow() error {
	s := w.platform.shadow
	if s == nil {
		return nil
	}
	w.platform.shadow = nil
	procKillTimer.Call(uintptr(w.platform.handle), shadowTimer)
	procKillTimer.Call(uintptr(w.platform.handle), borderTimer)
	if s.bmp != 0 {
		_ = _DeleteObject(_HGDIOBJ(s.bmp))
	}
	procDeleteDC.Call(uintptr(s.dc))
	return s.destroy()
}
