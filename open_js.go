//go:build js

package gunim

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/web"
)

// openDriver opens the browser driver: one WebGL2 canvas.
func openDriver() (driver.Driver, error) {
	d, err := web.Open()
	if err != nil {
		return nil, err
	}
	return d, nil
}
