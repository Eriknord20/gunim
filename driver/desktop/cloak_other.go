//go:build linux || darwin

package desktop

// cloak does nothing where a window's surface starts out opaque.
func cloak(*Window, bool) {}

// setCloaked takes w off the screen while it stays open, hidden, or
// shows it again. It runs on the main thread.
func setCloaked(w *Window, on bool) {
	if on {
		_ = w.gw.Hide()
		return
	}
	_ = w.gw.Show()
}

// showFrame does nothing where the system draws no shadow round a
// chromeless window.
func showFrame(*Window, bool) {}
