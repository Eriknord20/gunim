// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

//go:build freebsd || linux || netbsd

// gunim change: this file is gunim's.

package glfw

import (
	"math"
	"strconv"
	"unsafe"
)

// Display colour profiles on X11, a gunim addition. By the ICC Profiles
// in X Specification, the profile of the first screen is the root
// window's _ICC_PROFILE property, and that of screen n, counted in
// Xinerama's order, is _ICC_PROFILE_n. colord and the desktops set them
// from the profile chosen for each monitor. The root window's
// PropertyNotify for any of them calls the callback set with
// SetICCProfileCallback.

// iccScreens is how many screens' profiles are watched.
const iccScreens = 16

var (
	iccAtoms    [iccScreens]_Atom
	iccCallback func()
)

// iccAtom returns the atom of screen n's profile property, interning
// the lot on first use.
func iccAtom(n int) _Atom {
	if iccAtoms[0] == 0 {
		for i := range iccAtoms {
			name := "_ICC_PROFILE"
			if i > 0 {
				name += "_" + strconv.Itoa(i)
			}
			iccAtoms[i] = xInternAtom(_glfw.platformWindow.display, name, false)
		}
	}
	return iccAtoms[n]
}

// ICCProfile returns the ICC profile of screen n, in Xinerama's order,
// as the root window's property holds it, or nil where there is none.
// It must be called on the main thread.
func ICCProfile(n int) ([]byte, error) {
	if !_glfw.initialized {
		return nil, NotInitialized
	}
	if n < 0 || n >= iccScreens {
		return nil, nil
	}
	var actualType _Atom
	var format int32
	var count, after _Culong
	var data uintptr
	xGetWindowProperty(_glfw.platformWindow.display, _glfw.platformWindow.root, iccAtom(n),
		0, math.MaxInt32/4, false, _AnyPropertyType, &actualType, &format, &count, &after, &data)
	if data == 0 {
		return nil, nil
	}
	defer xFree(data)
	if format != 8 || count == 0 {
		return nil, nil
	}
	return cBytes(data, int(count)), nil
}

// cBytes copies n bytes from memory Xlib returned.
func cBytes(p uintptr, n int) []byte {
	src := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n)
	return append([]byte(nil), src...)
}

// SetICCProfileCallback sets the function called, on the main thread,
// when any screen's profile is set, changed or removed. It must be
// called on the main thread.
func SetICCProfileCallback(f func()) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	iccAtom(0)
	if f != nil {
		// Nothing else of glfw's listens on the root window.
		xSelectInput(_glfw.platformWindow.display, _glfw.platformWindow.root, _PropertyChangeMask)
	}
	iccCallback = f
	return nil
}

// iccChanged reports whether event is a change to a screen's profile,
// and calls the callback for it.
func iccChanged(event *_XEvent) bool {
	if iccCallback == nil || event.EventType() != _PropertyNotify || event.xany().Window != _glfw.platformWindow.root {
		return false
	}
	atom := event.xproperty().Atom
	for _, a := range iccAtoms {
		if a == atom {
			iccCallback()
			return true
		}
	}
	return false
}

// X11Screen returns the monitor's screen number in Xinerama's order,
// which names its profile property.
func (m *Monitor) X11Screen() int { return m.platform.index }

// SetICCProfile sets screen n's profile property on the root window, as
// colord does, or removes it for a nil profile. It is for tests and
// tools: the property belongs to whatever manages the displays. It must
// be called on the main thread.
func SetICCProfile(n int, profile []byte) error {
	if !_glfw.initialized {
		return NotInitialized
	}
	if n < 0 || n >= iccScreens {
		return nil
	}
	display, root := _glfw.platformWindow.display, _glfw.platformWindow.root
	if profile == nil {
		xDeleteProperty(display, root, iccAtom(n))
	} else {
		xChangeProperty(display, root, iccAtom(n), _XA_CARDINAL, 8, _PropModeReplace, unsafe.Pointer(&profile[0]), int32(len(profile)))
	}
	xFlush(display)
	return nil
}
