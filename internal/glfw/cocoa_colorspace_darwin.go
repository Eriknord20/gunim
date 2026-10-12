// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's.

package glfw

import (
	"github.com/ebitengine/purego/objc"

	"github.com/marrasen/gunim/internal/cocoa"
)

// SetSRGBColorSpace tells macOS the window's content is in sRGB, so the
// system converts it to each display's colours as it composites, as it
// does for a browser's pages. Without it, an OpenGL view's pixels go to
// the display as they are. It must be called on the main thread.
func (w *Window) SetSRGBColorSpace() error {
	if !_glfw.initialized {
		return NotInitialized
	}
	pool := cocoa.NSAutoreleasePool_new()
	defer pool.Release()
	cs := objc.ID(objc.GetClass("NSColorSpace")).Send(objc.RegisterName("sRGBColorSpace"))
	if cs == 0 {
		return nil
	}
	w.platform.object.Send(objc.RegisterName("setColorSpace:"), cs)
	return nil
}

// ColorSpaceName returns the localized name of the colour space of the
// screen the window is on, such as "Display P3", or "" where it has
// none. It must be called on the main thread.
func (w *Window) ColorSpaceName() string {
	if !_glfw.initialized {
		return ""
	}
	pool := cocoa.NSAutoreleasePool_new()
	defer pool.Release()
	screen := w.platform.object.Send(objc.RegisterName("screen"))
	if screen == 0 {
		return ""
	}
	cs := screen.Send(objc.RegisterName("colorSpace"))
	if cs == 0 {
		return ""
	}
	name := cs.Send(objc.RegisterName("localizedName"))
	if name == 0 {
		return ""
	}
	return cocoa.NSString{ID: name}.String()
}
