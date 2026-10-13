// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

// gunim change: this file is gunim's. It lets an ordinary window belong to another, in front of it.

package glfw

import "unsafe"

// _XA_WM_TRANSIENT_FOR is the atom ICCCM names a window's owner by.
const _XA_WM_TRANSIENT_FOR = _Atom(68)

// SetOwner makes the window a transient window of owner's, which the window manager keeps in front of owner. It is
// a gunim addition.
func (w *Window) SetOwner(owner *Window) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	xChangePropertyGeneric(_glfw.platformWindow.display, w.platform.handle, _XA_WM_TRANSIENT_FOR, _XA_WINDOW,
		_PropModeReplace, []_Culong{_Culong(owner.platform.handle)})
	xFlush(_glfw.platformWindow.display)
	return nil
}

// Owner returns the X window the window is a transient of, or 0 for none. It is a gunim addition.
func (w *Window) Owner() (uintptr, error) {
	if !_glfw.initialized {
		return 0, NotInitialized
	}
	var data uintptr
	n := getWindowPropertyX11(w.platform.handle, _XA_WM_TRANSIENT_FOR, _XA_WINDOW, &data)
	if data == 0 {
		return 0, nil
	}
	defer xFree(data)
	if n == 0 {
		return 0, nil
	}
	return uintptr(*(*_Culong)(unsafe.Pointer(data))), nil
}
