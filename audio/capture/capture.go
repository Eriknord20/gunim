// Package capture records sound from the computer's inputs: a
// microphone, a guitar plugged into an audio interface, a synth's
// output. It records through PulseAudio or PipeWire on Linux, WASAPI
// on Windows, and on Windows through the ASIO driver the speakers play
// through too, as package audio/speaker opens it.
//
// Each block recorded carries a stamp: the mixer frame of the speakers
// that was being heard as the block's first frame reached the input.
// A host lines a recording up with what was playing by it:
//
//	spk, _ := speaker.Open(mix, speaker.Options{Name: "My app"})
//	in, err := capture.Open(capture.Options{Speaker: spk})
//	...
//	b := in.Read(buf)
//	// b.Frames came in as mixer frame b.Heard played.
//
// Through ASIO the stamps are exact, from the driver's own buffers.
// Elsewhere they are reckoned from the system's latencies and
// smoothed; [Measure] plays a click and finds it in the recording, to
// tell how far they are off. They settle over a stream's first
// seconds, and then follow the two clocks as they drift apart.
//
// A stream ends as its device goes away: [Stream.Done] closes, and
// [Stream.Err] returns [ErrLost].
//
// A stream hands its blocks to a function on the device's thread, or
// keeps them in a ring a goroutine reads. Either way the recording
// side takes no lock and allocates nothing.
package capture

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/speaker"
)

// A Device is an input to record from.
type Device struct {
	// ID names the device to [Options].Device.
	ID string
	// Name is what people call it.
	Name string
	// Channels is how many channels it has, and ChannelNames what the
	// device calls them, where it names them.
	Channels     int
	ChannelNames []string
	// Rate is the rate it runs at, in frames a second, where it tells.
	Rate int
	// Default marks the system's default input.
	Default bool
	// Monitor marks a device that records what an output plays, as
	// PulseAudio's monitor of each sink.
	Monitor bool
	// Driver is the ASIO driver whose inputs the device is, or empty
	// for the system's own sound.
	Driver string
}

// asioPrefix starts the ID of an ASIO driver's inputs.
const asioPrefix = "asio:"

// Devices lists the inputs: the system's, and the inputs of the ASIO
// driver the speakers play through, where they play through one. To
// record from an ASIO driver, play through it with its inputs open: set
// [speaker.Options].Driver and Record.
func Devices() ([]Device, error) {
	var out []Device
	if t := duplex.Current(); t != nil {
		out = append(out, Device{ID: asioPrefix + t.Driver, Name: t.Driver, Channels: len(t.Names),
			ChannelNames: t.Names, Rate: t.Rate, Driver: t.Driver})
	}
	sys, err := systemDevices()
	return append(out, sys...), err
}

// DriverInputs returns the inputs of the ASIO driver spk plays through,
// as a device to pick before they are open, and false where spk plays
// through the system's own sound, or the driver has no inputs. Its ID
// opens them once spk plays with [speaker.Options].Record set; until
// then the device has every input the driver has, and ChannelNames is
// empty. [Devices] lists it once its inputs are open.
func DriverInputs(spk *speaker.Speaker) (Device, bool) {
	if spk == nil {
		return Device{}, false
	}
	return driverDevice(spk.State(), duplex.Current())
}

// driverDevice is DriverInputs for a speaker playing as st, t the tap
// of the driver whose inputs are open, or nil.
func driverDevice(st speaker.State, t *duplex.Tap) (Device, bool) {
	if st.Driver == "" || st.ASIO == nil {
		return Device{}, false
	}
	if t != nil && t.Driver == st.Driver {
		return Device{ID: asioPrefix + t.Driver, Name: t.Driver, Channels: len(t.Names), ChannelNames: t.Names,
			Rate: t.Rate, Driver: t.Driver}, len(t.Names) > 0
	}
	return Device{ID: asioPrefix + st.Driver, Name: st.Driver, Channels: st.ASIO.Inputs, Rate: st.Rate,
		Driver: st.Driver}, st.ASIO.Inputs > 0
}

// Options says how to open an input.
type Options struct {
	// Device is the input to record, by its ID from [Devices]. Empty
	// records the system's default input.
	Device string
	// Name names the application to the system's volume controls.
	Name string
	// Channels are the device's channels to record, from 0, in the
	// order a block holds them. Nil records them all.
	Channels []int
	// Rate is the rate to record at, in frames a second; the system
	// converts to it. Zero records at the speaker's rate, or, without
	// a speaker, the device's own. An ASIO driver records at the rate
	// it plays at.
	Rate int
	// Buffer is how much sound the device hands on at once. Zero means
	// 5 milliseconds. An ASIO driver hands on a buffer of its own size.
	Buffer time.Duration
	// Speaker is the speaker the stamps count the frames of. Where it
	// is nil, a block's Heard is -1. Open the stream again as the
	// speaker opens again, on [speaker.Speaker.Changed].
	Speaker *speaker.Speaker
	// OnBlock, where set, is handed each block as it comes in, on the
	// device's thread: it must be quick, and must not wait or
	// allocate. The block's Frames are its own only during the call.
	// Where it is nil, the stream keeps the blocks in a ring for
	// [Stream.Read].
	OnBlock func(Block)
	// Hold is how much sound the ring holds for Read, unread. Zero
	// means 2 seconds.
	Hold time.Duration
}

// A Block is sound recorded.
type Block struct {
	// Frames holds the frames, of Channels interleaved float32 samples
	// from -1 to 1 each.
	Frames   []float32
	Channels int
	// Frame counts the frames that came in before the block, since the
	// stream opened. Frames the stream had to drop count too, so a jump
	// in Frame is a gap in the sound.
	Frame int64
	// Heard is the speaker's mixer frame that was heard as the block's
	// first frame reached the input: a sound recorded there sounded
	// with that frame. Each frame after is a frame later, at the
	// stream's rate. It is -1 without a speaker.
	Heard int64
}

// Len returns how many frames b holds.
func (b Block) Len() int {
	if b.Channels == 0 {
		return 0
	}
	return len(b.Frames) / b.Channels
}

// Info says how a stream records.
type Info struct {
	// Device is the device's ID, and Name its name.
	Device, Name string
	// Rate is the rate it records at, and Channels how many channels a
	// block holds.
	Rate, Channels int
	// Buffer is how many frames the device hands on at once, about.
	Buffer int
	// Latency is how long a sound takes from the input to the block
	// that holds it, as the device reports it.
	Latency time.Duration
	// Exact says the stamps are exact, from the buffers of an ASIO
	// driver that plays and records in one; otherwise they are
	// reckoned from the latencies the system reports.
	Exact bool
}

// ErrLost is the error of a stream whose device went away: unplugged,
// taken by the system, or an ASIO driver the speakers opened again.
var ErrLost = errors.New("capture: the input went away")

// lost is how long a device may hand on nothing before the stream
// counts it gone.
const lost = 2 * time.Second

// ErrBusy was returned by [Open] for an ASIO driver's inputs that
// another stream recorded. Streams share them now, and none returns it.
//
// Deprecated: Open no longer returns it.
var ErrBusy = errors.New("capture: another stream records these inputs")

// A Stream records an input. Its methods are safe from any goroutine,
// but Read, which one goroutine at a time calls.
type Stream struct {
	o    Options
	info Info
	// clock is the speaker; ratio its mixer frames to a frame of the
	// stream.
	clock clock
	ratio float64
	// sel are the device's channels recorded, of devCh.
	sel   []int
	devCh int
	// The rest of these are the device's thread's: frame counts the
	// frames that came in, offset is Heard less frame, smoothed, and
	// aligned says it is set. buf holds a block's channels for OnBlock.
	// least is the least offset in this window, before in the window
	// before, turn the frame the window ends at, and settled the frame
	// the offset is smoothed from.
	frame         int64
	offset        float64
	least, before float64
	turn, settled int64
	aligned       bool
	buf           []float32
	q             *ring
	b             backend
	// latency is the device's latency in frames, as it reports it now.
	latency  atomic.Int64
	overruns atomic.Int64
	err      atomic.Pointer[error]
	done     chan struct{}
	ending   sync.Once
	closing  sync.Once
}

// A clock is the speaker, as the stream counts its frames.
type clock interface {
	Heard() int64
	Latency() time.Duration
}

// A backend is a device recording into a stream. Open opens it, and
// start has it hand its blocks to the stream, once the stream is ready
// for them.
type backend interface {
	start() error
	close() error
}

// A format is how a device records, as it opens.
type format struct {
	name     string
	channels int
	rate     int
	buffer   int
	latency  int
}

// Open opens an input and starts recording.
func Open(o Options) (*Stream, error) {
	s := &Stream{o: o, done: make(chan struct{}), ratio: 1}
	rate, mixRate := o.Rate, 0
	if o.Speaker != nil {
		s.clock = o.Speaker
		mixRate = o.Speaker.State().Rate
		if rate == 0 {
			rate = mixRate
		}
	}
	buffer := o.Buffer
	if buffer <= 0 {
		buffer = 5 * time.Millisecond
	}
	var (
		f   format
		err error
	)
	if len(o.Device) > len(asioPrefix) && o.Device[:len(asioPrefix)] == asioPrefix {
		f, err = openTap(s, o.Device[len(asioPrefix):])
	} else {
		f, err = openSystem(s, o.Device, rate, buffer)
	}
	if err != nil {
		return nil, err
	}
	if err := s.ready(f, mixRate); err != nil {
		return nil, err
	}
	return s, nil
}

// ready readies s to record from its backend, which opened as f says,
// and starts the backend; or closes it, and returns why. mixRate is the
// speaker's rate.
func (s *Stream) ready(f format, mixRate int) error {
	o := s.o
	if mixRate > 0 && f.rate > 0 {
		s.ratio = float64(mixRate) / float64(f.rate)
	}
	s.devCh = f.channels
	s.sel = o.Channels
	if s.sel == nil {
		s.sel = make([]int, f.channels)
		for i := range s.sel {
			s.sel[i] = i
		}
	}
	if len(s.sel) == 0 {
		_ = s.b.close()
		return errors.New("capture: Options.Channels names no channel")
	}
	for _, ch := range s.sel {
		if ch < 0 || ch >= f.channels {
			_ = s.b.close()
			return fmt.Errorf("capture: %s has channels 0 to %d, no channel %d", f.name, f.channels-1, ch)
		}
	}
	s.info.Device, s.info.Name = o.Device, f.name
	s.info.Rate, s.info.Channels, s.info.Buffer = f.rate, len(s.sel), f.buffer
	s.latency.Store(int64(f.latency))
	s.info.Latency = audio.DurationAt(int64(f.latency), f.rate)
	if o.OnBlock != nil {
		s.buf = make([]float32, len(s.sel)*maxBlock)
	} else {
		hold := o.Hold
		if hold <= 0 {
			hold = 2 * time.Second
		}
		s.q = newRing(int(max(audio.FramesAt(hold, f.rate), int64(4*f.buffer), 1024)), len(s.sel), s.ratio)
	}
	if err := s.b.start(); err != nil {
		_ = s.b.close()
		return err
	}
	return nil
}

// maxBlock is the most frames OnBlock is handed at once.
const maxBlock = 4096

// putAgo takes a block from the device, in of devCh channels, whose
// first frame reached the input ago of the stream's frames before now,
// and stamps it from the speaker's clock. It runs on the device's
// thread.
func (s *Stream) putAgo(in []float32, ago int64) {
	heard := int64(-1)
	if s.clock != nil {
		raw := float64(s.clock.Heard()) - float64(ago)*s.ratio
		heard = s.align(raw, len(in)/s.devCh)
	}
	s.put(in, heard)
}

// putAt takes a block from the device whose first frame sounded with
// mixer frame heard, exactly. It runs on the device's thread.
func (s *Stream) putAt(in []float32, heard int64) {
	if s.clock == nil {
		heard = -1
	}
	s.put(in, heard)
}

// smoothing is how long the offset between the stream's frames and the
// speaker's takes to follow a change; window how long the least delay
// is looked for over; and jump how far a stamp may be off before the
// offset is taken anew, as after the speaker opens again.
const (
	smoothing = time.Second
	window    = 2 * time.Second
	jump      = 100 * time.Millisecond
)

// align returns the stamp of the next block, of n frames, from raw, the
// stamp the clocks give it now. The device hands blocks on late, by a
// delay that varies, as a server hands on several at once: each makes
// raw that much late. So the offset between the stream's frames and the
// speaker's follows the block handed on soonest, the least raw over the
// last one or two windows, slowly after the first two; and steps to
// raw where raw is far off.
func (s *Stream) align(raw float64, n int) int64 {
	frame := float64(s.frame)
	off := raw - frame*s.ratio
	rate := float64(max(1, s.info.Rate))
	if !s.aligned || math.Abs(off-s.offset) > jump.Seconds()*rate*s.ratio {
		s.offset, s.aligned = off, true
		s.least, s.before = off, off
		s.turn = s.frame + int64(window.Seconds()*rate)
		s.settled = s.frame + int64(2*window.Seconds()*rate)
	} else {
		if s.frame >= s.turn {
			s.before, s.least = s.least, off
			s.turn = s.frame + int64(window.Seconds()*rate)
		}
		s.least = min(s.least, off)
		k := min(1, float64(n)/(smoothing.Seconds()*rate))
		if s.frame < s.settled {
			// The first second takes the least as it comes, to settle
			// at once on the stream's start.
			k = 1
		}
		s.offset += (min(s.least, s.before) - s.offset) * k
	}
	return int64(math.Round(frame*s.ratio + s.offset))
}

// put hands a stamped block on, to OnBlock or the ring. It runs on the
// device's thread.
func (s *Stream) put(in []float32, heard int64) {
	n := len(in) / s.devCh
	in = in[:n*s.devCh]
	if s.o.OnBlock != nil {
		ch := len(s.sel)
		for at := 0; at < n; {
			k := min(n-at, maxBlock)
			out := s.buf[:k*ch]
			pick(out, in[at*s.devCh:(at+k)*s.devCh], s.devCh, s.sel)
			h := heard
			if h >= 0 {
				h += int64(math.Round(float64(at) * s.ratio))
			}
			s.o.OnBlock(Block{Frames: out, Channels: ch, Frame: s.frame + int64(at), Heard: h})
			at += k
		}
	} else if !s.q.push(in, s.devCh, s.sel, s.frame, heard) {
		s.overruns.Add(1)
	}
	s.frame += int64(n)
}

// pick copies channels sel of in, frames of devCh channels, into out.
func pick(out, in []float32, devCh int, sel []int) {
	ch := len(sel)
	for i := range len(in) / devCh {
		src := in[i*devCh:]
		dst := out[i*ch:]
		for j, c := range sel {
			dst[j] = src[c]
		}
	}
}

// lose ends the stream with err, as its device went away.
func (s *Stream) lose(err error) {
	s.ending.Do(func() {
		s.err.Store(&err)
		close(s.done)
	})
}

// Read copies the frames recorded since the last Read into dst, as
// many as fit, and returns them as a block: dst's first frames. It
// returns the frames of one stamp at a time, so a block runs to a gap,
// and waits for nothing: an empty block says nothing more has come in.
// It is for a stream without [Options].OnBlock.
func (s *Stream) Read(dst []float32) Block {
	if s.q == nil {
		return Block{Channels: len(s.sel), Heard: -1}
	}
	return s.q.pop(dst)
}

// Info returns how the stream records.
func (s *Stream) Info() Info { return s.info }

// Latency returns how long a sound takes from the input to the block
// that holds it, as the device reports it now.
func (s *Stream) Latency() time.Duration {
	return audio.DurationAt(s.latency.Load(), s.info.Rate)
}

// Overruns counts the blocks dropped as the ring was full: Read was
// called too seldom.
func (s *Stream) Overruns() int64 { return s.overruns.Load() }

// Done returns a channel closed once the stream stops: closed, or its
// device gone. [Stream.Err] tells which.
func (s *Stream) Done() <-chan struct{} { return s.done }

// Err returns [ErrLost], or another error, once the stream stopped
// recording on its own, and nil before then or after Close.
func (s *Stream) Err() error {
	if e := s.err.Load(); e != nil {
		return *e
	}
	return nil
}

// Close stops recording and lets the device go.
func (s *Stream) Close() error {
	var err error
	s.closing.Do(func() {
		err = s.b.close()
		s.ending.Do(func() { close(s.done) })
	})
	return err
}
