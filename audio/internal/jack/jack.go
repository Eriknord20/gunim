// Package jack plays and records through a JACK server, on Linux: the
// server pro audio applications share, or PipeWire, which ships a
// libjack of its own. It loads libjack.so.0 as the program runs, through
// purego, with no cgo, and plays where neither library nor server is:
// [Open] returns an error there, and [Available] reports false.
//
// A [Client] has a port for each channel it plays and one for each
// input it records, connected to the system's own ports, those JACK
// marks as physical. The server owns the rate and the buffer size: the
// client follows them.
//
// The server calls the client's process function on a real-time thread
// of its own, once a buffer. That function is written in assembly, for
// amd64 and arm64: it copies between the server's port buffers and two
// rings, and runs no Go, so it never allocates, locks or waits, and the
// Go runtime never stops it. A goroutine of the caller's keeps the rings
// filled and emptied: [Client.Write] fills the ring the outputs play
// from, and [Client.Read] takes what the inputs recorded, a buffer at a
// time, each with the output frame taken as it came in.
//
// The server's other callbacks, as the buffer size or the rate changes,
// as a buffer runs late anywhere in the graph (an xrun), and as the
// server shuts down, are assembly too: each sets a word that
// [Client.Poll] reads.
package jack

import (
	"errors"
	"time"
)

// MaxPorts is how many ports a client opens of each kind at most: as
// many outputs, and as many inputs.
const MaxPorts = 64

// ErrUnsupported is returned by [Open] and [Ports] where JACK is not:
// on a system other than Linux, or on a processor other than amd64 and
// arm64.
var ErrUnsupported = errors.New("jack: JACK plays on Linux, on amd64 and arm64")

// ErrShutdown is returned by [Client.Err] once the server has shut
// down, or dropped the client.
var ErrShutdown = errors.New("jack: the server shut down")

// Config says how to open a client.
type Config struct {
	// Name names the client to the server, and to the patchbays that
	// show it. Empty takes the program's name. The server adds a number
	// to a name in use.
	Name string
	// Outputs are the system's outputs to play on, by their index in
	// [Ports]' list. Nil plays on the first two, or on the one of a
	// system of one. The client has a port for each, which plays on
	// the output named; a port whose output is missing, or named
	// before, is left unconnected, for a patchbay to connect.
	Outputs []int
	// Record opens a port for each of the system's inputs, up to
	// [MaxPorts], connected to it.
	Record bool
	// Hold is how much sound the ring the outputs play from must hold:
	// how far ahead [Client.Write] may fill it. The ring the inputs
	// record into holds as much.
	Hold time.Duration
	// Log, where set, hears what the client does.
	Log func(format string, args ...any)
}

// Info says how a client plays.
type Info struct {
	// Client is the name the server gave the client.
	Client string
	// Library is the libjack the client was opened through.
	Library string
	// Rate is the server's rate, in frames a second, and Buffer the
	// frames it hands the client at a time.
	Rate, Buffer int
	// Latency is how many frames pass from the start of the buffer the
	// server asks for to the first of its frames heard, as the server
	// reports it for the ports the outputs play on: the largest of them.
	// InputLatency is how many frames pass from a sound at the inputs
	// to the start of the buffer that hands it on, the largest of the
	// inputs'.
	Latency, InputLatency int
	// Outputs are the names of the system's outputs, and Played the
	// output each channel plays on, as Config.Outputs asked, or -1 where
	// its port is unconnected.
	Outputs []string
	Played  []int
	// Inputs are the names of the system's inputs. Recorded is how many
	// of them the client records, from the first, where Config.Record
	// is set.
	Inputs   []string
	Recorded int
}

// Events is what [Client.Poll] has learned since it was last called.
type Events struct {
	// Short is how many buffers the server took that the ring could not
	// fill: the sound ran dry. Xruns is how many times the server said a
	// buffer ran late, anywhere in its graph. Lost is how many frames
	// the inputs recorded that found the ring full, and were dropped.
	Short, Xruns, Lost int64
	// Cycles is how many buffers the server has asked for, ever.
	Cycles int64
	// Buffer and Rate are the server's buffer size and rate now; Info
	// tells the new buffer size once Poll has seen it. Rate changes only
	// as the client opens again.
	Buffer, Rate int
	// Graph says ports were connected or disconnected, anywhere, so
	// latencies may have changed: Poll has read them again.
	Graph bool
	// Shutdown says the server shut down, or dropped the client.
	Shutdown bool
}
