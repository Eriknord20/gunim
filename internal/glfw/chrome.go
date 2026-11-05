//go:build !js

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. A chromeless window draws its own
// title bar, and the system goes on doing what a title bar does.

package glfw

// Hit is what a point in a chromeless window is, for the system to act
// on as it would on its own title bar.
type Hit int

const (
	// HitClient is the window's content, which the application handles.
	HitClient Hit = iota
	// HitCaption is the title bar where nothing else is: a press there
	// moves the window, and a double click maximizes it.
	HitCaption
	// HitMaximize is the maximize button, which Windows 11 hangs its
	// snap layouts on.
	HitMaximize
	// HitCaptionOnly is the title bar of a window without a maximize
	// button: a press there moves the window, and a double click leaves
	// it as it is.
	HitCaptionOnly
)

// HitTestCallback says what the point x, y, in the window's client
// coordinates, is. It runs on the main thread, as the system asks, and
// must answer at once.
type HitTestCallback func(w *Window, x, y int) Hit

// Edge is an edge or a corner of a window to resize it by, numbered as
// _NET_WM_MOVERESIZE numbers them.
type Edge int

const (
	EdgeTopLeft Edge = iota
	EdgeTop
	EdgeTopRight
	EdgeRight
	EdgeBottomRight
	EdgeBottom
	EdgeBottomLeft
	EdgeLeft
)

// moveDirection is _NET_WM_MOVERESIZE's number for a move.
const moveDirection = 8

// SetChromeless takes the system's title bar and frame away, or gives
// them back. On Windows the window keeps moving, snapping, sizing and
// its shadow through the hit test; on X11 the application moves and
// sizes it with StartMove and StartResize. macOS keeps its title bar.
func (w *Window) SetChromeless(on bool) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	return w.platformSetChromeless(on)
}

// Chromeless reports whether the window has taken the system's title
// bar away.
func (w *Window) Chromeless() bool { return w.platformChromeless() }

// SetHitTestCallback sets the callback that says what a point in a
// chromeless window is, and returns the one set before.
func (w *Window) SetHitTestCallback(cb HitTestCallback) HitTestCallback {
	old := w.hitTest
	w.hitTest = cb
	return old
}

// StartMove hands the window to the system to move with the pointer,
// for a press on a title bar the application draws. The button must be
// down.
func (w *Window) StartMove() error {
	if !_glfw.initialized {
		return NotInitialized
	}
	return w.platformStartMoveResize(moveDirection)
}

// StartResize hands the window to the system to size by edge with the
// pointer. The button must be down.
func (w *Window) StartResize(edge Edge) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	return w.platformStartMoveResize(int(edge))
}
