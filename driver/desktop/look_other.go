//go:build linux || darwin

package desktop

import "github.com/marrasen/gunim/driver"

// SystemLook implements [driver.LookReader]: only Windows' settings are read so far.
func (d *Driver) SystemLook() (driver.SystemLook, error) {
	return driver.SystemLook{}, driver.ErrNoSystemLook
}

// WatchSystemLook implements [driver.LookReader]: only Windows' settings are read so far.
func (d *Driver) WatchSystemLook(func(driver.SystemLook, error)) (func(), error) {
	return func() {}, driver.ErrNoSystemLook
}
