//go:build !(linux || windows || darwin) && !js

// Package desktop is gunim's driver for desktop operating systems. This
// operating system has none yet.
package desktop

import "github.com/marrasen/gunim/driver"

// Open reports that no driver is built for this operating system.
func Open() (driver.Driver, error) { return nil, driver.ErrNoDriver }
