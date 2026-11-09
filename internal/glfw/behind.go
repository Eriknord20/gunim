//go:build !js

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. A window can be dragged from while
// another is in front of it, as Explorer's windows can on Windows.

package glfw

// SetDragFromBehind sets whether a press of the left button on the
// window's content, while another window is active, leaves the window
// where it is: neither active nor raised. A drag can then start from
// the window behind, and the application brings the window to the front
// itself when the press turns out to be a click. Only Windows does so.
// On X11 the window manager raises and focuses a window on a click, and
// on macOS the system does; both go on as before.
//
// The title bar, the edges, and a chromeless window's caption and
// maximize button still activate the window as they always do, as do
// the other buttons.
func (w *Window) SetDragFromBehind(on bool) { w.dragFromBehind = on }

// TakePressedBehind reports whether the last press of a button left the
// window behind, as SetDragFromBehind lets it, and forgets it. Call it
// from the mouse button callback, for a press.
func (w *Window) TakePressedBehind() bool {
	b := w.pressedBehind
	w.pressedBehind = false
	return b
}

// EscapeHeld reports whether Escape is held now, asked of the system,
// for a window that is pressed from behind and so hears no keys. It is
// false where the system cannot say, as on X11 and macOS, where a press
// raises the window and it hears Escape itself.
func (w *Window) EscapeHeld() bool {
	if !_glfw.initialized {
		return false
	}
	return w.platformEscapeHeld()
}
