// Package coreaudio records through Core Audio on macOS: [Inputs] lists
// the devices that record, and [OpenInput] records one by an IOProc of
// its own, the device's thread handing on each buffer as it comes in.
// The speaker plays through package audio/internal/audiounit; an input
// of the device it plays through keeps that device's rate and buffer.
//
// An application that records must have NSMicrophoneUsageDescription in
// its Info.plist: without it, macOS ends the application as it asks for
// the microphone. On other systems [OpenInput] returns [ErrUnsupported].
package coreaudio

import "errors"

// ErrUnsupported is returned by [Inputs] and [OpenInput] where Core
// Audio is not.
var ErrUnsupported = errors.New("coreaudio: not supported on this system")

// ErrDenied is returned by [OpenInput] where the system lets the
// application record nothing. Recording would hand on silence.
var ErrDenied = errors.New("coreaudio: this application may not record: allow it in System Settings, " +
	"under Privacy & Security, Microphone")

// An InputDevice is a device that records.
type InputDevice struct {
	// UID names the device to [InputConfig].Device. It stays the same
	// as the device is plugged in again, and the computer starts again.
	UID string
	// Name is what people call it.
	Name string
	// Channels is how many channels it records, and ChannelNames what
	// the device calls them, where it names any.
	Channels     int
	ChannelNames []string
	// Rate is the rate it runs at, in frames a second.
	Rate int
	// Default marks the system's default input.
	Default bool
}

// InputConfig says how to open an input.
type InputConfig struct {
	// Device is the device to record, by its UID. Empty records the
	// system's default input as it opens.
	Device string
	// Rate is the rate to record at. The device is set to it, as for the
	// output, where it is not the device the output plays through: that
	// one keeps the rate the output gave it. [InputInfo].Rate tells the
	// rate it records at, which a rate the device refuses leaves at the
	// device's own. Zero keeps the device's.
	Rate int
	// Buffer is how many frames the device hands on at a time, brought
	// into the range it allows. A device the output plays through keeps
	// the output's buffer, as a process has one buffer of each device.
	// Zero keeps the size it has.
	Buffer int
	// Record takes each buffer as it comes in: frames of
	// [InputInfo].Channels interleaved float32 samples, and how many
	// frames, at the device's rate, came in since the first of them
	// reached the input. The device calls it on its own thread, so it
	// must be quick, and must not wait or allocate. frames is its own
	// only during the call.
	Record func(frames []float32, ago int)
	// Log, where set, tells what the input does.
	Log func(format string, args ...any)
}

// InputInfo says how an input records.
type InputInfo struct {
	// UID and Device are the device's UID and name.
	UID, Device string
	// Rate is the rate it records at, in frames a second, and Channels
	// how many channels each frame holds.
	Rate, Channels int
	// Buffer is how many frames it hands on at a time, about.
	Buffer int
	// Latency is how many frames pass from a sound reaching the input
	// to the buffer that holds it handed on: the buffer, and the
	// device's and its stream's own latency and safety offset.
	Latency int
	// Problem is why the input records otherwise than asked: at another
	// rate, or with another buffer, than [InputConfig] asks for. It is
	// nil where it records as asked.
	Problem error
}
