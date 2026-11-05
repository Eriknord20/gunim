//go:build !js

// SPDX-License-Identifier: Apache-2.0

package glfw

// Depths returns, for each of ws, how deep it lies in the system's
// stack of windows on the screen: 0 for the frontmost, larger further
// back. Only the order of the numbers means anything. A window the
// system cannot place, such as a hidden one, gets -1, and so does
// every window where the system cannot say. Call it on the main
// thread. It is a gunim change.
func Depths(ws []*Window) []int {
	return depths(ws)
}

// Covered reports whether, at the screen point x, y, a window of another
// program lies in front of every one of ws, and ok whether the system
// could say. The application's other windows do not count. Call it on
// the main thread. It is a gunim change.
func Covered(ws []*Window, x, y int) (covered, ok bool) {
	if !_glfw.initialized {
		return false, false
	}
	return platformCovered(ws, x, y)
}

func depths(ws []*Window) []int {
	depths := make([]int, len(ws))
	for i := range depths {
		depths[i] = -1
	}
	if _glfw.initialized {
		platformDepths(ws, depths)
	}
	return depths
}
