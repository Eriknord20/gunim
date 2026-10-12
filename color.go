package gunim

import "github.com/marrasen/gunim/driver"

// ColorProfile says how the window's colours reach its display: the description of the display's ICC profile, and
// whether the window converts its frames from sRGB to it, or why not, such as a display with no profile, an sRGB one,
// or a system that converts windows itself. It follows the window to another monitor. It is safe from any goroutine.
// On a driver that does no colour management, the state is [driver.ColorUnknown].
func (c Client) ColorProfile() driver.ColorInfo { return colorProfile(c.w.dw) }

// ColorProfile says how the window's colours reach its display, as [Client.ColorProfile] does. It is the zero
// ColorInfo on a nil UI.
func (u *UI) ColorProfile() driver.ColorInfo {
	if u == nil {
		return driver.ColorInfo{}
	}
	return colorProfile(u.w.dw)
}

func colorProfile(dw driver.Window) driver.ColorInfo {
	if c, ok := dw.(driver.ColorManaged); ok {
		return c.ColorProfile()
	}
	return driver.ColorInfo{}
}
