//go:build gunimkms && linux && (arm64 || amd64)

package gunim

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/kms"
)

// openDriver opens the KMS driver with its default options: the screen
// straight through the kernel, with no window system, as on a Raspberry
// Pi with a touch screen. A program that sets options opens the driver
// itself, with kms.Open, and runs with [MainWith].
//
// driver/kms is for 64-bit Linux. Elsewhere, as on macOS or 32-bit ARM,
// the tag does nothing and Main opens the desktop driver.
func openDriver() (driver.Driver, error) { return kms.Open(kms.Options{}) }
