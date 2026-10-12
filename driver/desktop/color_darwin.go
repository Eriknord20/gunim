//go:build darwin

package desktop

// macOS converts every window to its display's colours as it
// composites them, given the colour space the window's content is in.
// gunim says that is sRGB, and converts nothing itself.

// watchColors has nothing to watch: the system converts.
func watchColors(*Driver) {}

// setupColor tells macOS the window's content is sRGB.
func setupColor(w *Window) {
	if w.noColor {
		return
	}
	if err := w.gw.SetSRGBColorSpace(); err != nil {
		w.debugf("colour: setting sRGB: %v", err)
	}
}

// displayKey names the display the window is on by its colour space.
func displayKey(w *Window) any { return w.gw.ColorSpaceName() }

// displayProfile says the system converts.
func displayProfile(w *Window) displayColor {
	return displayColor{bySystem: true, name: w.gw.ColorSpaceName(), detail: "macOS converts the window from sRGB"}
}
