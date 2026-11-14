//go:build linux || darwin

package desktop

import (
	"errors"

	"github.com/marrasen/gunim/geom"
)

// TrayRect implements [driver.TrayLocator]: only Windows says where its tray icon is.
func (d *Driver) TrayRect() (geom.Rect, error) {
	return geom.Rect{}, errors.New("desktop: the system does not say where the tray icon is")
}
