// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. It tells a window when the
// colours of its display may have changed, so the window can read its
// monitor's profile and Windows' colour state again.

package glfw

const _WM_DEVICECHANGE = 0x0219

// SetColorChangeCallback sets the function called, on the main thread,
// when the colours of the window's display may have changed: the
// displays or their modes change, a setting changes, as when a colour
// profile or Auto Color Management is switched, a device comes or goes,
// or the window moves to a monitor of another scale. Moving to another
// monitor of the same scale comes as the position callback.
func (w *Window) SetColorChangeCallback(f func(w *Window)) { w.platform.colorChange = f }

// colorMessage calls the colour change callback for the messages that
// may mean one.
func (w *Window) colorMessage(uMsg uint32) {
	if w.platform.colorChange == nil {
		return
	}
	switch uMsg {
	case _WM_DISPLAYCHANGE, _WM_SETTINGCHANGE, _WM_DEVICECHANGE, _WM_DPICHANGED:
		w.platform.colorChange(w)
	}
}
