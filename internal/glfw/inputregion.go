//go:build !js

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. A window can take the pointer on part of itself only, so the pointer goes to
// the window under the rest: a popup that draws a shadow round its card, over the window that opened it.

package glfw

import (
	"image"
	"slices"
)

// SetInputRegion limits where the window takes the pointer to rects, in the window's client coordinates, as the
// cursor's position is given. A nil rects lifts the limit, and the whole window takes the pointer again. Elsewhere
// the pointer goes to the window under it, as if this one were not there.
//
// On X11 it is the window's input shape. On Windows the window answers WM_NCHITTEST with HTTRANSPARENT there, which
// hands the pointer only to a window of the same thread under it; over another program's window, the pointer stays
// with this one. macOS has no such region, and the whole window goes on taking the pointer. A window the pointer
// passes through whole, by MousePassthrough, stays so.
func (w *Window) SetInputRegion(rects []image.Rectangle) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	set := rects != nil
	if set == w.inputRegionSet && slices.Equal(rects, w.inputRegion) {
		return nil
	}
	w.inputRegion, w.inputRegionSet = slices.Clone(rects), set
	return w.platformSetInputRegion()
}

// inInputRegion reports whether the point x, y, in client coordinates, is where the window takes the pointer.
func (w *Window) inInputRegion(x, y int) bool {
	if !w.inputRegionSet {
		return true
	}
	p := image.Pt(x, y)
	for _, r := range w.inputRegion {
		if p.In(r) {
			return true
		}
	}
	return false
}
