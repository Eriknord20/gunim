// Package driver lists the audio drivers the speakers can play
// through, past the system's own sound, and plays through ASIO drivers.
// [Drivers] lists ASIO drivers on Windows, and [JACK] and [PipeWire] on
// Linux; [AAudio]
// names AAudio on Android, and [WASAPI] gunim's own WASAPI output on
// Windows.
//
// An ASIO driver, on Windows, is the driver an audio interface's maker
// ships with it. The interface plays what the driver is given at the
// rate and in the buffers it is set to, sample for sample, past the
// mixing and resampling of Windows' own sound.
//
// The package speaks to ASIO drivers through its own definitions of the
// driver's interface, in Go. ASIO is for 64-bit Windows; elsewhere
// [Drivers] lists no ASIO driver and [Open] returns [ErrUnsupported].
//
// ASIO plays and records together: [Config.Record] takes the driver's
// inputs in step with the buffers it plays, sample for sample.
//
// A device plays on the driver's first two outputs, or on those
// [Config.Outputs] names: with [Config.FillN], on as many as it names,
// all in step. [Channels] lists a driver's inputs and outputs by name.
//
// On Linux, [Drivers] lists JACK too, under the name [JACK], where a
// JACK server answers, or PipeWire's. Package audio/speaker plays and
// records through it as through a driver, and [Channels] lists the
// system's ports JACK connects to. On macOS it lists [CoreAudio], the
// default devices, played and recorded the same way. [Open] opens ASIO
// drivers alone.
//
// A process plays through one driver at a time:
//
//	d, err := driver.Open(driver.Config{Name: drivers[0].Name, Rate: 44100,
//		Fill: func(frames []float32) { mix.Mix(frames) }})
//	...
//	defer d.Close()
package driver

import (
	"errors"
	"fmt"
)

// A Driver is a driver the speakers can play through: an ASIO driver
// installed on the computer, or [JACK].
type Driver struct {
	// Name is the driver's name, as Config.Name and package
	// audio/speaker's Options.Driver take it: for ASIO, as it is listed
	// under HKEY_LOCAL_MACHINE\SOFTWARE\ASIO.
	Name string
	// CLSID is the class ID an ASIO driver is made from.
	CLSID string
}

// JACK is the name [Drivers] lists JACK under, on Linux, where libjack
// loads and a JACK server answers: jackd, or PipeWire through its own
// libjack. Package audio/speaker plays through JACK where its
// Options.Driver names it. The server owns the rate and the buffer
// size, and has no control panel: a patchbay, as qpwgraph or QjackCtl,
// connects its ports and sets them.
const JACK = "JACK"

// AAudio is the name package audio/speaker takes as Options.Driver to
// play through AAudio, Android's own sound API, from Android 11, in
// place of oto: in low-latency mode, with the device to itself where it
// allows. With Options.Record set it records the phone's microphone
// too, or the headset's, for package audio/capture, stamped with the
// mixer frame heard as each block came in, as a driver's inputs are;
// the application needs the microphone permission first, which gunim's
// App.Ask asks for. On another system, or an older Android, the
// speakers play through the system's sound, as through a driver that
// fails to open.
const AAudio = "AAudio"

// CoreAudio is the name [Drivers] lists Core Audio under, on macOS:
// package audio/speaker plays through the default output device by a
// HAL output unit, gunim's own, where its Options.Driver names it. The
// device's own real-time thread takes each buffer from a ring and runs
// no Go as it does. The speaker plays at the device's rate, as Audio
// MIDI Setup sets it, and opens again as the default device changes,
// goes away or changes its rate. With Options.Record set it records the
// default input too, stamped with the mixer frame heard as each buffer
// came in; an application bundle needs NSMicrophoneUsageDescription in
// its Info.plist, as macOS ends one without it as it asks for the
// microphone. Without leave to record, it plays and records nothing.
const CoreAudio = "Core Audio"

// WASAPI is the name package audio/speaker takes as Options.Driver to
// play through WASAPI, Windows' own sound API, through gunim's own code
// in place of oto: the default output, in shared mode, at the smallest
// period the engine offers where Windows 10 or newer lets it. It plays
// at the rate the device is set to in Windows' Sound settings, whatever
// Options.Rate asks, so Windows converts nothing, and the mixer follows
// it. With Options.Record set it records the default input too,
// converted to that rate, for package audio/capture, stamped with the
// mixer frame heard as each packet came in, as a driver's inputs are.
// As the default device changes, or the device goes, it plays on the
// new default. On another system the speakers play through the
// system's sound, as through a driver that fails to open.
const WASAPI = "WASAPI"

// PipeWire is the name [Drivers] lists PipeWire under, on Linux, where
// libpipewire 0.3.50 or newer loads and a PipeWire server listens:
// package audio/speaker plays through it by gunim's own code, in place
// of oto, where its Options.Driver names it. It plays to the default
// output at the rate Options.Rate asks, which PipeWire converts to the
// device's where they differ, and asks for cycles as short as
// Options.Buffer, or a quarter of the latency. PipeWire's real-time
// thread takes each cycle from a ring and runs no Go as it does. The
// session manager moves the sound as the default output changes or
// goes, and the speaker counts the delay PipeWire reports, a Bluetooth
// headset's too. With Options.Record set it records the default input
// too, stamped with the mixer frame heard as each cycle came in, as a
// driver's inputs are.
const PipeWire = "PipeWire"

// Config says how to open a driver.
type Config struct {
	// Name is the driver's name, from [Drivers].
	Name string
	// Rate is the rate to play at, in frames a second. Zero keeps the
	// rate the driver is at. A rate the driver refuses keeps its own
	// too: [Device.Rate] tells the rate it plays at.
	Rate int
	// Buffer is how many frames the driver takes at a time. Zero takes
	// the size the driver prefers, as its control panel sets it; another
	// is brought to the nearest size the driver allows.
	Buffer int
	// Fill fills frames, stereo frames of interleaved float32 samples
	// from -1 to 1, with what plays next. The driver calls it on a
	// thread of its own, each time it takes a buffer, so it must be
	// quick and return at once. The pair plays on the first two
	// outputs Outputs names; one more named plays silence, and where it
	// names one alone, the pair is averaged onto it.
	Fill func(frames []float32)
	// FillN, where set, fills in place of Fill: frames of channels
	// interleaved samples, one for each output Outputs names, in its
	// order, or two where it names none. It runs as Fill does.
	FillN func(frames []float32, channels int)
	// Outputs are the driver's outputs to play on, by their index from
	// 0, as [Channels] lists them, up to [MaxOutputs] of them. Nil plays
	// on the first two, or on the one of a driver of one. An output the
	// driver lacks, or one named twice, plays nothing: its channel is
	// filled and dropped, and [Info].Played tells it.
	Outputs []int
	// Record, where set, records the driver's inputs, up to
	// [MaxInputs] of them: each time the driver takes a buffer, it is
	// called with what came in over the same period, just before Fill.
	// in holds frames of channels interleaved float32 samples from -1
	// to 1, the inputs in order. A sound in the input buffer of one
	// call was at the inputs [Info].InputLatency frames before the
	// call, and the first frame Fill fills then is heard
	// [Info].Latency frames after it. Record runs on the driver's
	// thread, as Fill does, so it must be as quick.
	Record func(in []float32, channels int)
	// Recording, where set, says whether Record wants the inputs now.
	// The driver's thread asks it each buffer, before it reads them:
	// where it returns false the inputs are left unread and Record
	// uncalled. It must be as quick as Record.
	Recording func() bool
	// Reset, where set, is called once, on a goroutine of its own, as
	// the driver asks to be opened again: after its rate, buffer size
	// or latency changes, as in its control panel. Close the device and
	// open it again to play on.
	Reset func()
	// Overrun, where set, is called each time the driver says it lost
	// sound while playing: by asking to resynchronize, or by telling of
	// an overload, as ASIO 2.3 lets it. It runs on the driver's thread,
	// and must be quick. Not every driver tells: most only play on.
	Overrun func()
	// Log, where set, hears what the driver tells and what is done with
	// it: its messages, its asks to be opened again, and a driver let go
	// as it stopped answering. It is called on any goroutine, but never
	// as the driver takes a buffer.
	Log func(format string, args ...any)
}

// MaxInputs is how many of a driver's inputs [Config.Record] records
// at most: the first ones.
const MaxInputs = 64

// MaxOutputs is how many of a driver's outputs a device plays on at
// most.
const MaxOutputs = 64

// ErrUnsupported is returned by [Open] on a system without ASIO.
var ErrUnsupported = errors.New("asio: ASIO plays on 64-bit Windows")

// ErrOpen is returned by [Open] and [ControlPanel] while a driver is
// open, and by [Channels] while another driver is.
var ErrOpen = errors.New("asio: a driver is open already")

// ErrNotAnswering is returned by [Open] and [Device.Close] for a driver
// that did not return in time from starting or stopping. It is let go
// as it is: the thread that called it waits on in the driver, and the
// process may not end until the driver lets it go, as when the
// computer restarts.
var ErrNotAnswering = errors.New("asio: the driver stopped answering")

// Error is an error an ASIO driver returned.
type Error struct {
	// Op is what was asked of the driver.
	Op string
	// Code is the driver's error code, and Message what the driver
	// says of it, where it says anything.
	Code    int32
	Message string
	// refused says the driver would not start at all, which can pass by
	// itself: see retryRefused.
	refused bool
}

func (e *Error) Error() string {
	name := codeNames[e.Code]
	if name == "" {
		name = fmt.Sprintf("error %d", e.Code)
	}
	if e.Message != "" && e.Code == codeOK {
		return fmt.Sprintf("asio: %s: %s", e.Op, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("asio: %s: %s (%s)", e.Op, e.Message, name)
	}
	return fmt.Sprintf("asio: %s: %s", e.Op, name)
}

// The error codes of an ASIO driver.
const (
	codeOK               = 0
	codeSuccess          = 0x3f4847a0
	codeNotPresent       = -1000
	codeHWMalfunction    = -999
	codeInvalidParameter = -998
	codeInvalidMode      = -997
	codeSPNotAdvancing   = -996
	codeNoClock          = -995
	codeNoMemory         = -994
)

var codeNames = map[int32]string{
	codeNotPresent:       "the hardware is missing",
	codeHWMalfunction:    "the hardware is malfunctioning",
	codeInvalidParameter: "a value was out of range",
	codeInvalidMode:      "the hardware is in a bad mode or used in a bad mode",
	codeSPNotAdvancing:   "the hardware is not running",
	codeNoClock:          "the sample clock or rate cannot be found",
	codeNoMemory:         "the driver ran out of memory",
}
