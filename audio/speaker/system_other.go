//go:build !darwin || ios

package speaker

import "github.com/marrasen/gunim/audio/driver"

// openSystem opens the system's own sound as a device that asks for its
// sound, where the system has one: oto plays it here.
var openSystem func(driver.Config) (pullDevice, error)
