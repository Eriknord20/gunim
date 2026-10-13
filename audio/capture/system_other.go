//go:build !((linux && !android) || freebsd || netbsd || openbsd || windows)

package capture

import (
	"errors"
	"time"
)

// errUnsupported is returned where the system has no inputs this
// package records from.
var errUnsupported = errors.New("capture: this system has no inputs to record from")

func systemDevices() ([]Device, error) { return nil, nil }

func openSystem(*Stream, string, int, time.Duration) (format, error) {
	return format{}, errUnsupported
}
