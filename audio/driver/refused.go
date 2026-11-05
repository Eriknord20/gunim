package driver

import (
	"errors"
	"time"
)

// A driver can refuse to start for a moment after it was let go, and start
// when asked again: a MOTU M6 refused 6 of 14 times that an application opened
// it again about a second after opening it, and started on the next try each
// time. Where the output falls back to the system's sound on an error, one such
// refusal leaves the application playing there, and its inputs gone, until
// something opens the driver again.
const (
	refusedTries = 3
	refusedGap   = 100 * time.Millisecond
)

// retryRefused calls open until it returns anything but a driver's refusal to
// start, refusedTries times at most, sleeping for refusedGap between them.
func retryRefused(open func() (*Device, error), sleep func(time.Duration)) (*Device, error) {
	for try := 1; ; try++ {
		d, err := open()
		var e *Error
		if try == refusedTries || !errors.As(err, &e) || !e.refused {
			return d, err
		}
		sleep(refusedGap)
	}
}
