//go:build darwin && !ios

package coreaudio

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/marrasen/gunim/audio/internal/audiounit"
	"github.com/marrasen/gunim/audio/internal/micaccess"
)

// The inputs record by an IOProc of their own: the
// device's thread hands each buffer on as it fills. Each input is a
// slot of inputs, which the one callback, made once, finds by the
// client data Core Audio hands it back.

var (
	// createInputProcID is AudioDeviceCreateIOProcID, and addInputListener
	// and removeInputListener AudioObjectAddPropertyListener and
	// AudioObjectRemovePropertyListener, their client data a slot's number.
	createInputProcID   func(dev uint32, proc uintptr, client uintptr, id *uintptr) int32
	addInputListener    func(id uint32, addr *propertyAddress, proc uintptr, client uintptr) int32
	removeInputListener func(id uint32, addr *propertyAddress, proc uintptr, client uintptr) int32
	// recordProc is the callback the inputs record by, and inputListener
	// the one told of changes to their devices.
	recordProc, inputListener uintptr
)

// maxInputs is how many inputs record at once, at most.
const maxInputs = 64

// inputs are the inputs recording, by slot; slot n is client data n+1.
// openMu guards the slots taken and let go.
var inputs [maxInputs]atomic.Pointer[Input]

// ErrLost is the error of an input whose device went away, stopped
// recording, or moved to another rate or another layout. [Input.Err]
// wraps it with the reason.
var ErrLost = errors.New("coreaudio: the input went away")

// lost is how long a device may hand on nothing before the input counts
// it gone.
const lost = 2 * time.Second

// audioTimeStamp is an AudioTimeStamp.
type audioTimeStamp struct {
	sampleTime    float64
	hostTime      uint64
	rateScalar    float64
	wordClockTime uint64
	smpte         [24]byte
	flags         uint32
	reserved      uint32
}

const timeStampSampleTimeValid = 1 << 0

// An Input is a device recording.
type Input struct {
	c    InputConfig
	dev  uint32
	proc uintptr
	slot int
	info InputInfo
	// chans is how many channels each of the device's buffers holds, and
	// frames the room the buffers are interleaved into, as large as the
	// device's largest buffer.
	chans  []int
	frames []float32
	// latency is the device's own and its stream's, in frames: a sound
	// reached the input that long before the sample time it is stamped
	// with.
	latency int
	// last is when the device last handed on a buffer, in nanoseconds
	// of the Unix clock.
	last atomic.Int64
	// stopped says Record is called no more; movedTo is the rate the
	// device moved to, or zero.
	stopped atomic.Bool
	movedTo atomic.Int64
	// skipped counts the buffers refused since the last taken, and
	// skipFrames and skipChannels say what the last held; untimed says a
	// buffer came without sample times. wake tells watch of either.
	skipped      atomic.Int64
	skipFrames   atomic.Int64
	skipChannels atomic.Int64
	untimed      atomic.Bool
	wake         chan struct{}
	err          atomic.Pointer[error]
	quit         chan struct{}
	done         chan struct{}
	watched      chan struct{}
	ending       sync.Once
	closing      sync.Once
}

// inputFollowed are the devices, and the rates asked of them, whose
// rate moved under an input: OpenInput records them at their own rate
// in place of setting it again, so it fights no other application.
// openMu guards it.
var inputFollowed = map[uint32]int{}

// Inputs lists the devices that record.
func Inputs() ([]InputDevice, error) {
	if err := load(); err != nil {
		return nil, fmt.Errorf("coreaudio: %w", err)
	}
	var def uint32
	if !get(systemObject, propDefaultInputDevice, scopeGlobal, &def) {
		return nil, errors.New("coreaudio: the system reports no default input")
	}
	devs, err := devices()
	if err != nil {
		return nil, err
	}
	var out []InputDevice
	for _, dev := range devs {
		chans, err := bufferChannels(dev, scopeInput)
		if err != nil {
			return nil, fmt.Errorf("%w, reading %s's inputs", err, name(dev))
		}
		ch := sum(chans)
		if ch == 0 {
			continue
		}
		uid, err := deviceUID(dev)
		if err != nil {
			return nil, err
		}
		var rate float64
		if !get(dev, propNominalSampleRate, scopeGlobal, &rate) {
			return nil, fmt.Errorf("coreaudio: %s reports no rate", name(dev))
		}
		out = append(out, InputDevice{UID: uid, Name: name(dev), Channels: ch, ChannelNames: channelNames(dev, ch),
			Rate: int(math.Round(rate)), Default: dev == def})
	}
	return out, nil
}

// devices returns the system's devices.
func devices() ([]uint32, error) {
	b, err := getBytes(systemObject, propDevices, scopeGlobal)
	if err != nil {
		return nil, fmt.Errorf("%w, listing the devices", err)
	}
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = *(*uint32)(unsafe.Pointer(&b[4*i]))
	}
	return out, nil
}

// deviceUID returns the device's UID.
func deviceUID(dev uint32) (string, error) {
	uid, err := readString(dev, propDeviceUID, scopeGlobal, elementMain)
	if err == nil && uid == "" {
		err = errors.New("coreaudio: the UID is empty")
	}
	if err != nil {
		return "", fmt.Errorf("%w, reading %s's UID", err, name(dev))
	}
	return uid, nil
}

// channelNames returns what device dev calls its ch input channels, or
// nil where it names none.
func channelNames(dev uint32, ch int) []string {
	names := make([]string, ch)
	named := false
	for i := range names {
		names[i] = stringOf(dev, propElementName, scopeInput, uint32(i+1))
		named = named || names[i] != ""
	}
	if !named {
		return nil
	}
	return names
}

func sum(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

// findInput returns the device of UID uid, or the default input for "".
func findInput(uid string) (uint32, error) {
	if uid == "" {
		var dev uint32
		if !get(systemObject, propDefaultInputDevice, scopeGlobal, &dev) || dev == 0 {
			return 0, errors.New("coreaudio: there is no input device")
		}
		return dev, nil
	}
	devs, err := devices()
	if err != nil {
		return 0, err
	}
	for _, dev := range devs {
		u, err := deviceUID(dev)
		if err != nil {
			return 0, err
		}
		if u == uid {
			return dev, nil
		}
	}
	return 0, fmt.Errorf("coreaudio: there is no input %q", uid)
}

// OpenInput opens a device that records, and starts it, as c says.
// Several inputs record at once, of one device or of several.
func OpenInput(c InputConfig) (*Input, error) {
	if c.Record == nil {
		return nil, errors.New("coreaudio: no Record")
	}
	if err := load(); err != nil {
		return nil, fmt.Errorf("coreaudio: %w", err)
	}
	if err := mayRecord(); err != nil {
		return nil, err
	}
	openMu.Lock()
	defer openMu.Unlock()
	dev, err := findInput(c.Device)
	if err != nil {
		return nil, err
	}
	in := &Input{c: c, dev: dev, slot: -1, wake: make(chan struct{}, 1), quit: make(chan struct{}),
		done: make(chan struct{}), watched: make(chan struct{})}
	if in.info.UID, err = deviceUID(dev); err != nil {
		return nil, err
	}
	in.info.Device = name(dev)
	for i := range inputs {
		if inputs[i].Load() == nil {
			in.slot = i
			break
		}
	}
	if in.slot < 0 {
		return nil, fmt.Errorf("coreaudio: %d inputs record already", maxInputs)
	}

	var f streamDescription
	if !get(dev, propStreamFormat, scopeInput, &f) {
		return nil, fmt.Errorf("coreaudio: %s reports no input format", in.info.Device)
	}
	if f.formatID != formatLinearPCM || f.formatFlags&formatIsFloat == 0 || f.bitsPerChannel != 32 {
		return nil, fmt.Errorf("coreaudio: %s records no 32-bit float samples", in.info.Device)
	}
	if in.chans, err = bufferChannels(dev, scopeInput); err != nil {
		return nil, fmt.Errorf("%w, reading %s's inputs", err, in.info.Device)
	}
	in.info.Channels = sum(in.chans)
	if in.info.Channels == 0 {
		return nil, fmt.Errorf("coreaudio: %s has no inputs", in.info.Device)
	}

	// The device the speaker plays through keeps its rate and its
	// buffer: either set here would open the speaker again.
	playing := audiounit.Playing() == dev
	followed := c.Rate > 0 && inputFollowed[dev] == c.Rate
	var refused error
	if c.Rate > 0 && !playing && !followed {
		delete(inputFollowed, dev)
		refused = setRate(dev, c.Rate, in.info.Device)
	}
	var rate float64
	if !get(dev, propNominalSampleRate, scopeGlobal, &rate) || rate <= 0 {
		return nil, fmt.Errorf("coreaudio: %s reports no rate", in.info.Device)
	}
	in.info.Rate = int(math.Round(rate))
	var problems []error
	if c.Rate > 0 && in.info.Rate != c.Rate {
		switch {
		case playing:
			refused = fmt.Errorf("coreaudio: %s keeps the %d Hz the output plays at", in.info.Device, in.info.Rate)
		case followed:
			refused = fmt.Errorf("coreaudio: %s keeps the %d Hz another application set", in.info.Device, in.info.Rate)
		case refused == nil:
			refused = fmt.Errorf("coreaudio: %s stays at %d Hz", in.info.Device, in.info.Rate)
		}
		problems = append(problems, refused)
	}

	var r valueRange
	if !get(dev, propBufferFrameRange, scopeGlobal, &r) {
		return nil, fmt.Errorf("coreaudio: %s reports no buffer sizes", in.info.Device)
	}
	if c.Buffer > 0 && !playing {
		want := c.Buffer
		if r.max > 0 {
			want = min(max(want, int(r.min)), int(r.max))
		}
		if st := set(dev, propBufferFrameSize, scopeGlobal, uint32(want)); st != noErr {
			problems = append(problems, fmt.Errorf("coreaudio: %s refuses a buffer of %d frames: %d", in.info.Device,
				want, st))
		}
	}
	in.info.Problem = errors.Join(problems...)
	if c.Log != nil {
		for _, p := range problems {
			c.Log("%v", p)
		}
	}
	var size uint32
	if !get(dev, propBufferFrameSize, scopeGlobal, &size) || size == 0 {
		return nil, fmt.Errorf("coreaudio: %s reports no buffer size", in.info.Device)
	}
	in.info.Buffer = int(size)
	var lat, safety uint32
	if !get(dev, propLatency, scopeInput, &lat) || !get(dev, propSafetyOffset, scopeInput, &safety) {
		return nil, fmt.Errorf("coreaudio: %s reports no input latency", in.info.Device)
	}
	stream, ok := firstStreamLatency(dev, scopeInput)
	if !ok {
		return nil, fmt.Errorf("coreaudio: %s reports no input stream latency", in.info.Device)
	}
	in.latency = int(lat) + stream
	in.info.Latency = in.info.Buffer + in.latency + int(safety)
	// The buffer may grow as it plays, as the output sets its own, to
	// the most the device allows.
	in.frames = make([]float32, in.info.Channels*max(in.info.Buffer, int(r.max), 4096))

	inputs[in.slot].Store(in)
	if st := createInputProcID(dev, recordProc, uintptr(in.slot+1), &in.proc); st != noErr {
		inputs[in.slot].Store(nil)
		return nil, fmt.Errorf("coreaudio: AudioDeviceCreateIOProcID: %d", st)
	}
	in.last.Store(time.Now().UnixNano())
	if st := deviceStart(dev, in.proc); st != noErr {
		err := fmt.Errorf("coreaudio: AudioDeviceStart: %d", st)
		if st := destroyIOProcID(dev, in.proc); st != noErr {
			err = errors.Join(err, fmt.Errorf("coreaudio: AudioDeviceDestroyIOProcID: %d", st))
		}
		inputs[in.slot].Store(nil)
		return nil, err
	}
	if err := in.listen(); err != nil {
		return nil, errors.Join(err, in.stop())
	}
	// A change before the listener was added is told of by none.
	in.check()
	go in.watch()
	in.logf("%s records %d channels at %d Hz, %d frames at a time, %d frames of latency",
		in.info.Device, in.info.Channels, in.info.Rate, in.info.Buffer, in.info.Latency)
	return in, nil
}

// record hands the device's buffers on, on the device's thread.
func record(dev uint32, now, data, inTime, out, outTime unsafe.Pointer, client uintptr) int32 {
	if client == 0 || client > maxInputs || data == nil {
		return noErr
	}
	in := inputs[client-1].Load()
	if in == nil || in.dev != dev || in.stopped.Load() {
		return noErr
	}
	n := int(*(*uint32)(data))
	bufs := unsafe.Slice((*audioBuffer)(unsafe.Add(data, bufferListHeader)), n)
	frames := -1
	ch := 0
	for _, b := range bufs {
		if b.channels == 0 {
			continue
		}
		k := int(b.bytes) / 4 / int(b.channels)
		if frames < 0 || k < frames {
			frames = k
		}
		ch += int(b.channels)
	}
	if frames <= 0 || ch != in.info.Channels || frames*ch > len(in.frames) {
		in.skipFrames.Store(int64(max(frames, 0)))
		in.skipChannels.Store(int64(ch))
		in.skipped.Add(1)
		return noErr
	}
	if now == nil || inTime == nil {
		in.untime()
		return noErr
	}
	t, nt := (*audioTimeStamp)(inTime), (*audioTimeStamp)(now)
	if t.flags&nt.flags&timeStampSampleTimeValid == 0 {
		in.untime()
		return noErr
	}
	in.skipped.Store(0)
	in.last.Store(time.Now().UnixNano())
	dst := in.frames[:frames*ch]
	// The buffers' channels, one after another, interleaved into one
	// frame; a buffer with no data records silence.
	at := 0
	for _, b := range bufs {
		k := int(b.channels)
		if k == 0 {
			continue
		}
		if b.data == nil {
			for i := range frames {
				clear(dst[i*ch+at : i*ch+at+k])
			}
		} else {
			src := unsafe.Slice((*float32)(b.data), int(b.bytes)/4)
			if k == ch {
				copy(dst, src[:frames*ch])
			} else {
				for i := range frames {
					copy(dst[i*ch+at:i*ch+at+k], src[i*k:i*k+k])
				}
			}
		}
		at += k
	}
	// The first frame was stamped with its sample time as the device
	// took it in, the device's and its stream's latency after the sound
	// reached the input; now is the device's time as it handed it on.
	in.c.Record(dst, in.latency+int(math.Round(nt.sampleTime-t.sampleTime)))
	return noErr
}

// untime stops the input, as the device handed on a buffer without
// sample times, and tells watch. It runs on the device's thread.
func (in *Input) untime() {
	in.stopped.Store(true)
	in.untimed.Store(true)
	select {
	case in.wake <- struct{}{}:
	default:
	}
}

// inputWatched are the properties of its device an input listens to.
var inputWatched = []propertyAddress{
	{propDeviceIsAlive, scopeGlobal, elementMain},
	{propNominalSampleRate, scopeGlobal, elementMain},
	{propStreamConfiguration, scopeInput, elementMain},
}

// listen adds inputListener to the properties watched, removing it from
// those it was added to where one fails.
func (in *Input) listen() error {
	for i := range inputWatched {
		if st := addInputListener(in.dev, &inputWatched[i], inputListener, uintptr(in.slot+1)); st != noErr {
			err := fmt.Errorf("coreaudio: AudioObjectAddPropertyListener: %d", st)
			return errors.Join(err, in.unlisten(inputWatched[:i]))
		}
	}
	return nil
}

// unlisten removes inputListener from the properties as.
func (in *Input) unlisten(as []propertyAddress) error {
	var errs []error
	for i := range as {
		if st := removeInputListener(in.dev, &as[i], inputListener, uintptr(in.slot+1)); st != noErr {
			errs = append(errs, fmt.Errorf("coreaudio: AudioObjectRemovePropertyListener: %d", st))
		}
	}
	return errors.Join(errs...)
}

// inputChanged is told of a change of a property an input listens to.
func inputChanged(id, n uint32, addrs unsafe.Pointer, client uintptr) int32 {
	if client == 0 || client > maxInputs {
		return noErr
	}
	if in := inputs[client-1].Load(); in != nil && in.dev == id {
		in.check()
	}
	return noErr
}

// check ends the input where its device went away, or records at
// another rate or other channels now.
func (in *Input) check() {
	var alive uint32
	var rate float64
	switch {
	case !get(in.dev, propDeviceIsAlive, scopeGlobal, &alive):
		in.lose("reports no longer whether it is there")
	case alive == 0:
		in.lose("went away")
	case !get(in.dev, propNominalSampleRate, scopeGlobal, &rate):
		in.lose("reports no rate")
	case int(math.Round(rate)) != in.info.Rate:
		in.movedTo.Store(int64(math.Round(rate)))
		in.lose(fmt.Sprintf("moved to %v Hz", rate))
	default:
		chans, err := bufferChannels(in.dev, scopeInput)
		switch {
		case err != nil:
			in.lose(fmt.Sprintf("reports no channels: %v", err))
		case !slices.Equal(chans, in.chans):
			in.lose("records other channels now")
		}
	}
}

// watch ends the input as its device stops handing on buffers, or
// hands on one without sample times, until Close.
func (in *Input) watch() {
	defer close(in.watched)
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-in.quit:
			return
		case <-t.C:
		case <-in.wake:
		}
		switch {
		case in.untimed.Load():
			in.lose("hands on a buffer without sample times")
		case time.Since(time.Unix(0, in.last.Load())) <= lost:
			continue
		case in.skipped.Load() > 0:
			in.lose(fmt.Sprintf("hands on only buffers it cannot take, %d of them, the last of %d frames of %d "+
				"channels, where it records %d channels, %d frames at most", in.skipped.Load(), in.skipFrames.Load(),
				in.skipChannels.Load(), in.info.Channels, len(in.frames)/in.info.Channels))
		default:
			in.lose("hands on nothing")
		}
		return
	}
}

// lose ends the input, as its device did why.
func (in *Input) lose(why string) {
	in.stopped.Store(true)
	in.ending.Do(func() {
		in.logf("%s %s", in.info.Device, why)
		err := fmt.Errorf("%w: %s %s", ErrLost, in.info.Device, why)
		in.err.Store(&err)
		close(in.done)
	})
}

func (in *Input) logf(format string, args ...any) {
	if in.c.Log != nil {
		in.c.Log("coreaudio: "+format, args...)
	}
}

// Info says how the input records.
func (in *Input) Info() InputInfo { return in.info }

// Done returns a channel closed once the input stops: closed, or its
// device lost. [Input.Err] tells which.
func (in *Input) Done() <-chan struct{} { return in.done }

// Err returns an error wrapping [ErrLost] once the input stopped on its
// own, and nil before then or after Close.
func (in *Input) Err() error {
	if e := in.err.Load(); e != nil {
		return *e
	}
	return nil
}

// Close stops the input and lets the device go. Record is not called
// once it returns.
func (in *Input) Close() error {
	var err error
	in.closing.Do(func() {
		close(in.quit)
		<-in.watched
		openMu.Lock()
		defer openMu.Unlock()
		// An output opening on the device at another rate moves it too,
		// which ends the input with ErrLost.
		if moved := int(in.movedTo.Load()); moved != 0 && in.c.Rate > 0 && rateSet[in.dev] != moved {
			inputFollowed[in.dev] = in.c.Rate
		}
		err = errors.Join(in.unlisten(inputWatched), in.stop())
		in.ending.Do(func() { close(in.done) })
	})
	return err
}

// stop stops the device, lets its IOProc go and frees the slot, each
// step taken where one before it fails.
func (in *Input) stop() error {
	// Stopped from outside its thread, the device stops before
	// AudioDeviceStop returns.
	var errs []error
	if st := deviceStop(in.dev, in.proc); st != noErr {
		errs = append(errs, fmt.Errorf("coreaudio: AudioDeviceStop: %d", st))
	}
	if st := destroyIOProcID(in.dev, in.proc); st != noErr {
		errs = append(errs, fmt.Errorf("coreaudio: AudioDeviceDestroyIOProcID: %d", st))
	}
	inputs[in.slot].Store(nil)
	return errors.Join(errs...)
}

// mayRecord returns ErrDenied where the system lets the application
// record nothing; Core Audio would hand on silence. Where it has not
// been asked yet, it asks, as micaccess.Check does.
func mayRecord() error {
	denied, err := micaccess.Check()
	if err != nil {
		return fmt.Errorf("coreaudio: %w", err)
	}
	if denied {
		return ErrDenied
	}
	return nil
}
