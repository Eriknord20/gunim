package driver

import (
	"errors"
	"time"
)

// Share is what an application hands to other applications through the
// system's share sheet, as a phone's Share button does: text, files, or
// both.
type Share struct {
	// Text is text to share, such as a message or a link.
	Text string
	// Subject is a title for it, as an email's subject line, where the
	// receiving application takes one.
	Subject string
	// Paths are files to share. The receiving application reads them
	// while the share sheet hands them over, and for a while after, so
	// they must stay where they are.
	Paths []string
}

// A Sharer is a [Window] that shows the system's share sheet. Share
// shows the sheet over the window with what s holds, and returns once
// the system has taken it; the user picks where it goes, or closes the
// sheet. It may be called from any goroutine but the main one.
type Sharer interface {
	Share(s Share) error
}

// ErrNoSharer is returned where the platform has no share sheet gunim
// can show.
var ErrNoSharer = errors.New("driver: no share sheet on this platform")

// A Vibrator is a [Window] on a device with a vibration motor, as a
// phone has. Vibrate runs the motor in pattern: on for the first
// duration, off for the next, on for the one after, and so on, as the
// web's navigator.vibrate does. A call stops any pattern still running,
// and one with no pattern only stops it. It returns at once.
type Vibrator interface {
	Vibrate(pattern ...time.Duration) error
}

// ErrNoVibrator is returned where the device has no vibration motor
// gunim can run.
var ErrNoVibrator = errors.New("driver: no vibration motor on this device")
