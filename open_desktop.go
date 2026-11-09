//go:build !android && !js

package gunim

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/desktop"
)

// openDriver opens the desktop driver: X11, Win32 or Cocoa.
func openDriver() (driver.Driver, error) { return desktop.Open() }
