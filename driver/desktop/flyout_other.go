//go:build linux || darwin

package desktop

// startFlyout lets the window manager give a flyout the keyboard, as it gives any window it shows. It runs on the main
// thread.
func startFlyout(*Window) {}

// stopFlyout has nothing to take down.
func stopFlyout(*Window) {}
