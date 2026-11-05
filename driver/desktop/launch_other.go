//go:build !windows && !linux && !js

package desktop

import "github.com/marrasen/gunim/driver"

// Open implements [driver.Launcher]; this platform cannot open files in
// gunim yet.
func (w *Window) Open(string) error { return driver.ErrNoLauncher }

// Reveal implements [driver.Launcher]; this platform cannot show files in
// gunim yet.
func (w *Window) Reveal(string) error { return driver.ErrNoLauncher }

// OpenLink implements [driver.LinkOpener] as nothing, for now.
func (w *Window) OpenLink(string) error { return driver.ErrNoLinkOpener }
