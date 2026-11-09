//go:build !darwin && !js

package glfw

// SystemFullScreen reports whether the window is in the system's own
// full screen, which only macOS has: false everywhere else.
func (w *Window) SystemFullScreen() bool { return false }
