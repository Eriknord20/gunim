//go:build !js

// SPDX-License-Identifier: Apache-2.0

package glfw

// HeldModifiers returns the modifier keys held now, asked of the system
// rather than taken from w's last event, which may be old when w does
// not have the keyboard. It is a gunim change.
func (w *Window) HeldModifiers() ModifierKey {
	if !_glfw.initialized {
		return 0
	}
	return w.platformHeldModifiers()
}
