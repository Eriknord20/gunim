// Package pipewire plays and records through PipeWire, the sound server
// most Linux desktops run. It loads libpipewire-0.3.so.0 as the program
// runs, through purego, with no cgo, and plays where neither library nor
// server is: [Open] returns an error there, and [Available] reports
// false. It needs PipeWire 0.3.50 or newer.
//
// A [Client] plays a stream to the default output, and where
// Config.Record says, records a stream from the default input. Both are
// 32-bit float stereo at the rate Config.Rate asks; PipeWire converts
// to the device's rate and channels where they differ. The session
// manager links the streams, and moves them as the default device
// changes or goes.
//
// PipeWire calls each stream's process function on a real-time thread
// of its own, once a graph cycle. That function is written in assembly,
// for amd64 and arm64: it copies between the stream's buffer and a ring,
// and runs no Go, so it never allocates, locks or waits, and the Go
// runtime never stops it. A goroutine of the caller's keeps the rings
// filled and emptied: [Client.Write] fills the ring the output plays
// from, and [Client.Read] takes what the input recorded.
//
// The output's process function also notes PipeWire's time report for
// the cycle, pw_stream_get_time_n's: when the cycle began, and the delay
// from the stream to the device. [Client.Held] reads it, so the frame
// heard moves on through a cycle, and counts a Bluetooth sink's delay.
// The input's notes the same for each cycle it records, and
// [Client.Read] stamps what it records with the output frame heard as
// the sound reached the input.
package pipewire

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by [Open] where PipeWire is not: on a
// system other than Linux, or on a processor other than amd64 and
// arm64.
var ErrUnsupported = errors.New("pipewire: PipeWire plays on Linux, on amd64 and arm64")

// ErrStopped is returned by [Client.Err] once a stream has failed, as
// when the server shut down.
var ErrStopped = errors.New("pipewire: the stream stopped")

// Channels is how many channels a frame holds, played and recorded, where
// a Config names none: left and right.
const Channels = 2

// Config says how to open a client.
type Config struct {
	// Name names the streams to the server, and to the volume controls
	// that show them. Empty takes the program's name.
	Name string
	// Rate is the rate both streams run at, in frames a second.
	Rate int
	// Quantum is how many frames a graph cycle should hold, as the
	// streams ask through node.latency. The server runs at the smallest
	// any stream asks, within its own limits. Zero asks for 256.
	Quantum int
	// Record opens a stream from the default input too.
	Record bool
	// Hold is how much sound the ring the output plays from must hold:
	// how far ahead [Client.Write] may fill it. The ring the input
	// records into holds as much.
	Hold time.Duration
	// Output and Input are the node names of the devices to play on and
	// record from, as [Devices] lists them. Empty is the default device,
	// which PipeWire converts to; and named, PipeWire passes the channels
	// as they are, one for one.
	Output, Input string
	// OutPositions and InPositions name each channel of the streams, by
	// the numbers of SPA's enum spa_audio_channel, as [Device] tells them.
	// Empty is left and right.
	OutPositions, InPositions []uint32
	// Log, where set, hears what the client does.
	Log func(format string, args ...any)
}

// Info says how a client plays.
type Info struct {
	// Library is the version of libpipewire the client loaded.
	Library string
	// Rate is the rate the streams run at, and Quantum the frames a
	// cycle the client asked for.
	Rate, Quantum int
	// Recording says the client records from the default input.
	Recording bool
	// OutChannels and InChannels are how many channels a frame holds,
	// played and recorded.
	OutChannels, InChannels int
}

// State is a stream's state, as PipeWire's enum pw_stream_state counts
// them.
type State int

// The states a stream passes through.
const (
	StateError       State = -1
	StateUnconnected State = 0
	StateConnecting  State = 1
	StatePaused      State = 2
	StateStreaming   State = 3
)

func (s State) String() string {
	switch s {
	case StateError:
		return "error"
	case StateUnconnected:
		return "unconnected"
	case StateConnecting:
		return "connecting"
	case StatePaused:
		return "paused"
	case StateStreaming:
		return "streaming"
	}
	return "unknown"
}

// Events is what [Client.Poll] has learned since it was last called.
type Events struct {
	// Short is how many cycles the ring could not fill: the sound ran
	// dry. Empty is how many cycles found no buffer to fill. Lost is how
	// many frames the input recorded that found its ring full, and were
	// dropped.
	Short, Empty, Lost int64
	// Cycles is how many cycles the output has played, ever.
	Cycles int64
	// Out and In are the streams' states now, and Moved says either
	// changed since the last Poll: a stream pauses and streams again as
	// the server moves it to another device.
	Out, In State
	Moved   bool
	// Error is what PipeWire said as a stream failed, where one did.
	Error string
	// Quantum is how many frames the output's last cycle took, and
	// DeviceRate the rate the graph runs at: the device's, which
	// PipeWire converts the stream to. Delay is the frames from the
	// first frame of the next cycle to its being heard, as the last
	// cycle's time report tells, at the stream's rate. All are zero
	// before the first cycle.
	Quantum, DeviceRate, Delay int
}
