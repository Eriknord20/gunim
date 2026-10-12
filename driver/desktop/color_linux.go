//go:build linux

package desktop

import (
	"os"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
)

// On X11 a display's profile is a property of the root window, one for
// each screen in Xinerama's order; see glfw.ICCProfile. A Wayland
// session's compositor decides how a window's colours reach the
// display, and some convert sRGB windows themselves, so there gunim
// leaves them alone.

// waylandSession says the X server is a Wayland compositor's.
var waylandSession = os.Getenv("WAYLAND_DISPLAY") != ""

// watchColors reads every window's profile again as one changes.
func watchColors(d *Driver) {
	if waylandSession {
		return
	}
	_ = glfw.SetICCProfileCallback(func() { d.post(d.colorsChanged) })
}

// setupColor readies a new window for colour management.
func setupColor(*Window) {}

// displayKey names the display the window is on: its screen number.
func displayKey(w *Window) any {
	if m := w.windowMonitor(); m != nil {
		return m.X11Screen()
	}
	return 0
}

// displayProfile reads the profile of the display the window is on.
func displayProfile(w *Window) displayColor {
	if waylandSession {
		return displayColor{unmanaged: "a Wayland session: the compositor decides"}
	}
	screen := 0
	if m := w.windowMonitor(); m != nil {
		screen = m.X11Screen()
	}
	b, err := glfw.ICCProfile(screen)
	if err == nil && b == nil && screen > 0 {
		// Only the first screen's profile is set, as by a tool that
		// knows one display.
		b, err = glfw.ICCProfile(0)
	}
	return displayColor{profile: b, err: err}
}

// windowMonitor returns the monitor holding the window's centre, or
// the primary one. It runs on the main thread.
func (w *Window) windowMonitor() *glfw.Monitor {
	x, y, err := w.gw.GetPos()
	if err != nil {
		return nil
	}
	ww, wh, err := w.gw.GetSize()
	if err != nil {
		return nil
	}
	centre := geom.Pt(float32(x+ww/2), float32(y+wh/2))
	ms, err := glfw.GetMonitors()
	if err != nil {
		return nil
	}
	for _, m := range ms {
		if info, ok := monitorInfo(m); ok && info.Bounds.Contains(centre) {
			return m
		}
	}
	m, _ := glfw.GetPrimaryMonitor()
	return m
}
