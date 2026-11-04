//go:build android

package aaudio

/*
#cgo LDFLAGS: -laaudio
#include "aaudio_android.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"time"
	"unsafe"
)

// minAPI is the oldest Android AAudio plays on here: 11. Before it
// AAudio did not always tell of a stream whose device went away
// (google/oboe#893), so a headset unplugged could leave the sound
// silent for good.
const minAPI = 30

// Supported returns nil where AAudio plays, and [ErrUnsupported]
// where it does not.
func Supported() error {
	if C.gunim_aa_api() < minAPI {
		return ErrUnsupported
	}
	return nil
}

// Config says how to open a stream.
type Config struct {
	// Input opens a stream that records, into Data, its blocks into
	// Blocks; otherwise it plays from Data.
	Input bool
	// Rate is the rate to play or record at; AAudio converts to the
	// device's.
	Rate int
	// Channels is how many channels a frame holds, as Data's frames do.
	Channels int
	// Exclusive asks for the device to itself, for the lowest latency.
	// A device that refuses shares it.
	Exclusive bool
	// Quick marks an output's sound as a game's, for the shortest delay
	// Android offers on Bluetooth; else it is media's.
	Quick bool
	// Device is the device to ask for, by its ID; at 0 Android picks.
	Device int
	Data   *Ring[float32]
	Blocks *Ring[int64]
}

// A Stream is an AAudio stream, playing or recording.
type Stream struct {
	s     *C.AAudioStream
	sh    *Shared
	info  Info
	input bool
	quick bool
}

// Open opens a stream, stopped; Start starts it.
func Open(c Config) (*Stream, error) {
	if err := Supported(); err != nil {
		return nil, err
	}
	if c.Data == nil || c.Data.Chans() != c.Channels || (c.Input && c.Blocks == nil) {
		return nil, errors.New("aaudio: a stream's rings do not fit it")
	}
	sh := NewShared(c.Data, c.Blocks)
	st := &Stream{sh: sh, input: c.Input, quick: c.Quick}
	if msg := C.gunim_aa_open(&st.s, (*C.gunim_shared)(unsafe.Pointer(sh)), cbool(c.Input), C.int(c.Rate),
		C.int(c.Channels), cbool(c.Exclusive), cbool(c.Quick), C.int(c.Device)); msg != nil {
		FreeShared(sh)
		return nil, fmt.Errorf("aaudio: opening a stream: %s", C.GoString(msg))
	}
	st.info = Info{
		Rate:       int(C.AAudioStream_getSampleRate(st.s)),
		Channels:   int(C.AAudioStream_getChannelCount(st.s)),
		Burst:      int(C.AAudioStream_getFramesPerBurst(st.s)),
		Buffer:     int(C.AAudioStream_getBufferSizeInFrames(st.s)),
		Capacity:   int(C.AAudioStream_getBufferCapacityInFrames(st.s)),
		Device:     int(C.AAudioStream_getDeviceId(st.s)),
		Exclusive:  C.AAudioStream_getSharingMode(st.s) == C.AAUDIO_SHARING_MODE_EXCLUSIVE,
		LowLatency: C.AAudioStream_getPerformanceMode(st.s) == C.AAUDIO_PERFORMANCE_MODE_LOW_LATENCY,
	}
	return st, nil
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func result(op string, r C.aaudio_result_t) error {
	if r == C.AAUDIO_OK {
		return nil
	}
	return fmt.Errorf("aaudio: %s: %s", op, C.GoString(C.AAudio_convertResultToText(r)))
}

// Start asks the stream to start, and returns at once.
func (s *Stream) Start() error { return result("starting", C.AAudioStream_requestStart(s.s)) }

// Pause asks the stream to stop for a while, and returns at once: an
// output pauses, keeping what it holds; an input stops.
func (s *Stream) Pause() error {
	if s.input {
		return result("stopping", C.AAudioStream_requestStop(s.s))
	}
	return result("pausing", C.AAudioStream_requestPause(s.s))
}

// Close stops the stream and lets it go, once its callback has
// returned for the last time.
func (s *Stream) Close() error {
	C.AAudioStream_requestStop(s.s)
	err := result("closing", C.AAudioStream_close(s.s))
	FreeShared(s.sh)
	s.sh = nil
	return err
}

// Timestamp returns the frame at the device at time at, as Shared's
// Frames count them, and the time now, both on CLOCK_MONOTONIC in
// nanoseconds; ok is false while the stream has not said yet.
func (s *Stream) Timestamp() (pos, at, now int64, ok bool) {
	var p, a, n C.int64_t
	r := C.gunim_aa_timestamp(s.s, &p, &a, &n)
	return int64(p), int64(a), int64(n), r == C.AAUDIO_OK
}

// Shared returns what the stream's callbacks tell.
func (s *Stream) Shared() *Shared { return s.sh }

// Info returns how the stream plays or records.
func (s *Stream) Info() Info {
	i := s.info
	i.Buffer = int(C.AAudioStream_getBufferSizeInFrames(s.s))
	return i
}

// XRuns counts the times the device ran dry or overflowed, as AAudio
// counts them, which a callback too late makes.
func (s *Stream) XRuns() int64 { return int64(C.AAudioStream_getXRunCount(s.s)) }

// Route asks Android's Java side of the device the stream plays or
// records on. It
// calls into Java, and may take a few milliseconds: call it from
// neither AAudio's thread nor one that must keep time.
func (s *Stream) Route() Route {
	var r [3]C.int
	if C.gunim_aa_route(C.AAudioStream_getDeviceId(s.s), &r[0], 3) < 3 {
		return Route{}
	}
	lat := time.Duration(-1)
	if r[1] >= 0 {
		lat = time.Duration(r[1]) * time.Millisecond
	}
	return Route{Known: true, Type: int(r[0]), Latency: lat, Mic: int(r[2])}
}

// Probe plays a second of silence through a Java AudioTrack, for as
// long, and tells what it reports; ok is false where the Android driver
// is missing.
func (s *Stream) Probe() (p Probe, ok bool) {
	var r [3]C.int
	if C.gunim_aa_probe(cbool(s.quick), &r[0], 3) < 3 {
		return Probe{}, false
	}
	p = Probe{Behind: -1, Latency: -1, Buffer: int(r[2])}
	if r[0] >= 0 {
		p.Behind = time.Duration(r[0]) * time.Microsecond
	}
	if r[1] >= 0 {
		p.Latency = time.Duration(r[1]) * time.Millisecond
	}
	return p, true
}
