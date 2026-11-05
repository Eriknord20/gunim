//go:build !js

// SPDX-License-Identifier: Apache-2.0

package glfw

// PointerOnScreen returns where the pointer is on the screen, in the
// screen coordinates monitors are given in, whatever window it is over,
// and false where the system cannot say. It is a gunim change.
func PointerOnScreen() (x, y int, ok bool) {
	if !_glfw.initialized {
		return 0, 0, false
	}
	return platformPointerOnScreen()
}
