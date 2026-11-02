// Package wasapi speaks to WASAPI, Windows' own sound API, through its
// own definitions of the COM interfaces, in Go, with no cgo.
//
// It holds what package audio/capture and package audio/speaker share:
// the COM calls, the device enumerator and its notifications, and a
// [Device] that plays the default output in shared mode, event-driven,
// at the rate of the device's own mix format, and records the default
// input beside it.
//
// A Device belongs to the thread that opened it: every call into it,
// and its Close, come from that one thread, locked, as package audio/rt
// locks it. Nothing else touches its COM objects, so nothing can call
// into a stream another goroutine has closed.
//
// On another system [Open] returns [ErrUnsupported].
package wasapi

import "errors"

// ErrUnsupported is returned by [Open] on a system other than Windows.
var ErrUnsupported = errors.New("wasapi: WASAPI plays on Windows")

// ErrLost is returned by a device whose endpoint went away: unplugged,
// disabled, or set to another format. Open the default one again.
var ErrLost = errors.New("wasapi: the device went away")

// Config says how to open a [Device].
type Config struct {
	// Record opens the default input too, converted to the output's
	// rate, its packets read with [Device.Read].
	Record bool
}

// Info says how a device plays.
type Info struct {
	// Name is the output's name, as people know it, and ID the
	// endpoint's ID.
	Name, ID string
	// Rate is the rate it plays at, in frames a second: the rate of the
	// device's mix format, which Windows mixes at, so nothing converts
	// it. Channels is how many channels the mix format has: the sound
	// plays on the first two, or is averaged onto the one.
	Rate, Channels int
	// Period is how many frames the engine takes at a time: the event
	// fires once a period. Buffer is how many frames the stream's
	// buffer holds. Latency is the stream's latency past the buffer, in
	// frames, as WASAPI reports it.
	Period, Buffer, Latency int
	// LowLatency says the stream runs at the smallest period the
	// engine offers, through IAudioClient3, which Windows 10 and newer
	// have where the driver allows it. False plays at the engine's
	// default period, through IAudioClient.
	LowLatency bool
	// InputName and InputID are the input's, where Config.Record
	// opened one, and Inputs how many channels it records, converted to
	// Rate. InputBuffer is how many frames a packet holds at most, and
	// InputLatency the input stream's latency and a period, in frames.
	// InputErr is why the input did not open, where it did not.
	InputName, InputID  string
	Inputs, InputBuffer int
	InputLatency        int
	InputErr            error
}
