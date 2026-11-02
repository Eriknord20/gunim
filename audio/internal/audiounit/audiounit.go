// Package audiounit plays and records through Core Audio on macOS, by
// the HAL output unit (AUHAL): one unit plays through the default output
// device, and another records the default input device. It loads the
// system's frameworks as the program runs, through purego, with no cgo.
//
// Core Audio calls the units' callbacks on its own real-time threads,
// once a buffer. Those callbacks are written in assembly, for amd64 and
// arm64: the output's copies from a ring into the unit's buffers, and
// the input's has the unit render what came in and copies it into
// another ring. They run no Go, so they never allocate, lock or wait,
// and the Go runtime never stops them. A goroutine of the caller's keeps
// the rings filled and emptied: [Client.Write] fills the ring the output
// plays from, and [Client.Read] takes what the input recorded, a buffer
// at a time, each stamped with the output frame heard as it came in.
//
// Each buffer the output takes carries the time its first frame reaches
// the hardware. The client notes it, so [Client.Heard] tells which frame
// is heard at any moment, smoothly between buffers, and a stretch of
// silence played while the ring ran dry or paused counts for no frame.
//
// The listener Core Audio calls as the default device changes, or a
// device goes away or changes its rate, is assembly too: it counts
// changes in a word [Client.Poll] reads, and Poll tells the caller
// whether to open the client again.
//
// The client follows the device: it plays at the device's own rate, as
// another application or Audio MIDI Setup sets it, and opens again
// where that changes.
package audiounit

import (
	"errors"
	"time"
)

// MaxPorts is how many channels a client plays at most, and how many it
// records.
const MaxPorts = 64

// ErrUnsupported is returned by [Open] where Core Audio is not: on a
// system other than macOS, or a processor other than amd64 and arm64.
var ErrUnsupported = errors.New("audiounit: Core Audio plays on macOS, on amd64 and arm64")

// ErrNoOutput is returned by [Open] where the system has no output
// device, as a Mac mini with nothing plugged in, or a build machine.
var ErrNoOutput = errors.New("audiounit: the system has no output device")

// ErrDenied is why a client records nothing where the system lets the
// application record nothing: Core Audio would hand on silence.
var ErrDenied = errors.New("audiounit: this application may not record: allow it in System Settings, " +
	"under Privacy & Security, Microphone")

// Config says how to open a client.
type Config struct {
	// Outputs are the default output device's channels to play on, by
	// their index from 0, as [Ports] lists them. Nil plays on the first
	// two, or on the one of a device of one. A channel named twice, or
	// one the device lacks, plays on none. Outputs must name one at the
	// least.
	Outputs []int
	// Record records the default input device too, each of its
	// channels, up to [MaxPorts].
	Record bool
	// Rate, where set, is the rate to play at, in frames a second: the
	// output device is set to it, as a DAW sets it, where the device
	// offers it, and the other applications playing through the device
	// are converted to it. Info.Rate tells the rate played. Where
	// another application then moves the device to another rate, the
	// client plays at that rate, while asked the same Rate again. Zero
	// plays at the device's own rate.
	Rate int
	// Buffer is how many frames the device hands the client at a time,
	// brought into the range the device allows. Zero keeps the device's
	// size. The size is the process's own: other applications playing
	// through the device keep theirs.
	Buffer int
	// Hold is how much sound the ring the output plays from must hold:
	// how far ahead [Client.Write] may fill it. The ring the input
	// records into holds as much.
	Hold time.Duration
	// Log, where set, hears what the client does.
	Log func(format string, args ...any)
}

// Info says how a client plays.
type Info struct {
	// Device is the output device's name, and Rate its rate, in frames
	// a second, which the client plays at.
	Device string
	Rate   int
	// Buffer is how many frames the device hands the client at a time;
	// MinBuffer and MaxBuffer the sizes it allows.
	Buffer, MinBuffer, MaxBuffer int
	// Latency is how many frames pass from a buffer's first frame
	// reaching the hardware to its being heard: the device's latency
	// and its stream's. Safety is the device's safety offset: how far
	// ahead of the hardware Core Audio writes. A frame written as a
	// buffer starts is heard Buffer+Safety+Latency frames later.
	Latency, Safety int
	// Outputs are the names of the device's channels, and Played the
	// channel each of the client's plays on, as Config.Outputs asked,
	// or -1 where it plays on none.
	Outputs []string
	Played  []int
	// InputDevice is the input device's name, Inputs the names of its
	// channels, and Recorded how many of them the client records, from
	// the first: none where Config.Record is unset, or InputProblem
	// says why.
	InputDevice string
	Inputs      []string
	Recorded    int
	// InputLatency is how many frames pass from a sound at the input to
	// the time stamped on the buffer that holds it: the input device's
	// latency and its stream's. InputBuffer is the frames the input
	// hands on at a time.
	InputLatency, InputBuffer int
	// InputProblem is why the client records nothing, where
	// Config.Record asked it to: no input device, the system's leave to
	// record withheld, or an input at another rate than the output.
	InputProblem error
}

// Events is what [Client.Poll] has learned since it was last called.
type Events struct {
	// Short is how many buffers the output took that the ring could not
	// fill: the sound ran dry. Lost is how many frames the input
	// recorded that found its ring full, and were dropped. Failed is how
	// many times the input unit failed to render what came in.
	Short, Lost, Failed int64
	// Cycles is how many buffers the output has taken, ever.
	Cycles int64
	// Changed says a device's settings changed: Poll has read the
	// buffer size and the latencies again, and Info tells them.
	Changed bool
	// Reopen, where set, says why the client plays otherwise than it
	// opened, so it should be closed and opened again: the default
	// output or input device changed, a device went away, or a device's
	// rate changed.
	Reopen string
}
