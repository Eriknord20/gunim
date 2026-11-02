package wasapi

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/audio"
)

// A Device plays the system's default output, in shared mode, and
// records its default input where Config.Record says. It belongs to
// the thread that opened it, as the package says.
type Device struct {
	info     Info
	cfg      Config
	leaveCOM func()
	// The COM objects: the enumerator, the output's endpoint, client,
	// render client and clock, and the input's endpoint, client and
	// capture client.
	enum, dev, client, render, clock uintptr
	inDev, inClient, capture         uintptr
	// events are the output's event, then the input's, as many as nev.
	events [2]windows.Handle
	nev    int
	// freq is the clock's units a second.
	freq       uint64
	registered bool
	// seen is what the notifier had counted as the device last looked.
	seen counts
	// The calls the loop makes, found once, and what they write, on
	// the heap, so a call allocates nothing.
	getPadding, getBuffer, releaseBuffer, getPosition uintptr
	nextPacket, capGet, capRelease                    uintptr
	p                                                 *params
	closed                                            bool
}

// params holds what the loop's calls write.
type params struct {
	padding, n, frames, flags uint32
	data                      uintptr
	pos, qpc, devPos          uint64
}

// Open opens the default output, and the default input where c.Record
// says, stopped. The calling thread must be locked to its goroutine,
// as the device belongs to it: every call into the device comes from
// it, and Close too. An input that fails to open leaves the output
// open, and Info.InputErr says why.
func Open(c Config) (*Device, error) {
	leave, err := InitCOM()
	if err != nil {
		return nil, err
	}
	d := &Device{cfg: c, leaveCOM: leave, p: new(params)}
	if err := d.open(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Device) open() error {
	e, err := Enumerator()
	if err != nil {
		return err
	}
	d.enum = e
	n := theNotifier()
	d.seen = n.read()
	d.registered = Call(e, mRegisterNotification, n.addr()) == 0
	if hr := Call(e, MGetDefaultAudioEndpoint, ERender, EConsole, uintptr(unsafe.Pointer(&d.dev))); hr != 0 {
		return failed("finding the default output", hr)
	}
	d.info.ID, d.info.Name = DeviceID(d.dev), FriendlyName(d.dev)
	if err := d.openOutput(); err != nil {
		return err
	}
	if d.cfg.Record {
		if err := d.openInput(); err != nil {
			d.dropInput()
			d.info.InputErr = err
		}
	}
	return nil
}

// event makes the next of the device's events.
func (d *Device) event() (windows.Handle, error) {
	h, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return 0, err
	}
	d.events[d.nev] = h
	d.nev++
	return h, nil
}

// openOutput readies the output to play float32 at its mix format's
// rate: through IAudioClient3 at the engine's smallest period where it
// offers one smaller than its default, else through IAudioClient.
func (d *Device) openOutput() error {
	name := d.info.Name
	three := true
	if Call(d.dev, MActivate, uintptr(unsafe.Pointer(&iidIAudioClient3)), ClsctxAll, 0, uintptr(unsafe.Pointer(&d.client))) != 0 {
		three = false
		if hr := Call(d.dev, MActivate, uintptr(unsafe.Pointer(&IIDAudioClient)), ClsctxAll, 0, uintptr(unsafe.Pointer(&d.client))); hr != 0 {
			return failed("opening "+name, hr)
		}
	}
	mf, err := engineFormat(d.client)
	if err != nil {
		return err
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(mf))
	ch, rate := int(mf.Channels), int(mf.SamplesPerSec)
	if ch < 1 || rate < 1 {
		return fmt.Errorf("wasapi: %s mixes %d channels at %d Hz", name, ch, rate)
	}
	d.info.Rate, d.info.Channels = rate, ch
	// Float32 as the engine mixes it, mostly; where it mixes otherwise,
	// Windows converts float32 to it, at the same rate.
	format := uintptr(unsafe.Pointer(mf))
	flags := uintptr(StreamFlagsEventCallback)
	var wf WaveFormat
	if !mf.isFloat32() {
		mask := uint32(0)
		if mf.FormatTag == WaveFormatExtended {
			mask = mf.ChannelMask
		}
		wf = Float32(ch, rate, mask)
		format = uintptr(unsafe.Pointer(&wf))
		flags |= StreamFlagsAutoConvertPCM | StreamFlagsSRCDefaultQuality
		three = false
	}
	ready := false
	if three {
		var def, fund, least, most uint32
		if Call(d.client, mGetSharedModeEnginePeriod, format, uintptr(unsafe.Pointer(&def)), uintptr(unsafe.Pointer(&fund)),
			uintptr(unsafe.Pointer(&least)), uintptr(unsafe.Pointer(&most))) == 0 && least > 0 && least < def {
			if Call(d.client, mInitializeSharedAudioStream, flags, uintptr(least), format, 0) == 0 {
				ready, d.info.LowLatency = true, true
			} else {
				// A client refused is spent: open another.
				Release(d.client)
				d.client = 0
				if hr := Call(d.dev, MActivate, uintptr(unsafe.Pointer(&IIDAudioClient)), ClsctxAll, 0, uintptr(unsafe.Pointer(&d.client))); hr != 0 {
					return failed("opening "+name, hr)
				}
			}
		}
	}
	if !ready {
		if hr := Call(d.client, MInitialize, 0, flags, 0, 0, format, 0); hr != 0 {
			return failed("readying "+name+" to play", hr)
		}
	}
	var size uint32
	if hr := Call(d.client, MGetBufferSize, uintptr(unsafe.Pointer(&size))); hr != 0 {
		return failed("reading the buffer's size", hr)
	}
	d.info.Buffer = int(size)
	var latency int64
	_ = Call(d.client, MGetStreamLatency, uintptr(unsafe.Pointer(&latency)))
	d.info.Latency = int(audio.FramesAt(time.Duration(latency)*100, rate))
	d.info.Period = d.period(rate)
	h, err := d.event()
	if err != nil {
		return err
	}
	if hr := Call(d.client, MSetEventHandle, uintptr(h)); hr != 0 {
		return failed("readying "+name+" to play", hr)
	}
	if hr := Call(d.client, MGetService, uintptr(unsafe.Pointer(&iidIAudioRenderClient)), uintptr(unsafe.Pointer(&d.render))); hr != 0 {
		return failed("readying "+name+" to play", hr)
	}
	if hr := Call(d.client, MGetService, uintptr(unsafe.Pointer(&iidIAudioClock)), uintptr(unsafe.Pointer(&d.clock))); hr != 0 {
		return failed("reading "+name+"'s clock", hr)
	}
	if hr := Call(d.clock, mGetFrequency, uintptr(unsafe.Pointer(&d.freq))); hr != 0 || d.freq == 0 {
		return failed("reading "+name+"'s clock", hr)
	}
	d.getPadding = Method(d.client, mGetCurrentPadding)
	d.getBuffer, d.releaseBuffer = Method(d.render, mRenderGetBuffer), Method(d.render, mRenderReleaseBuffer)
	d.getPosition = Method(d.clock, mGetPosition)
	return nil
}

// period returns the frames the engine takes at a time, as the output
// was readied.
func (d *Device) period(rate int) int {
	if d.info.LowLatency {
		var f uintptr
		var frames uint32
		if Call(d.client, mGetCurrentSharedModeEnginePeriod, uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&frames))) == 0 {
			if f != 0 {
				windows.CoTaskMemFree(*(*unsafe.Pointer)(unsafe.Pointer(&f)))
			}
			if frames > 0 {
				return int(frames)
			}
		}
	}
	var def, least int64
	if Call(d.client, MGetDevicePeriod, uintptr(unsafe.Pointer(&def)), uintptr(unsafe.Pointer(&least))) == 0 && def > 0 {
		return max(1, int(audio.FramesAt(time.Duration(def)*100, rate)))
	}
	return max(1, d.info.Buffer/2)
}

// openInput readies the default input to record float32 at the
// output's rate, Windows converting it from the input's own.
func (d *Device) openInput() error {
	if hr := Call(d.enum, MGetDefaultAudioEndpoint, ECapture, EConsole, uintptr(unsafe.Pointer(&d.inDev))); hr != 0 {
		return failed("finding the default input", hr)
	}
	d.info.InputID, d.info.InputName = DeviceID(d.inDev), FriendlyName(d.inDev)
	name := d.info.InputName
	if hr := Call(d.inDev, MActivate, uintptr(unsafe.Pointer(&IIDAudioClient)), ClsctxAll, 0, uintptr(unsafe.Pointer(&d.inClient))); hr != 0 {
		return failed("opening "+name, hr)
	}
	ch, _, mask, err := MixFormat(d.inClient)
	if err != nil {
		return err
	}
	rate := d.info.Rate
	wf := Float32(ch, rate, mask)
	// The buffer holds plenty: the input hands each packet on as it
	// comes, a period of the engine's, whatever the buffer's size.
	hns := int64(100 * time.Millisecond / 100)
	if hr := Call(d.inClient, MInitialize, 0, StreamFlagsEventCallback|StreamFlagsAutoConvertPCM|StreamFlagsSRCDefaultQuality,
		uintptr(hns), 0, uintptr(unsafe.Pointer(&wf)), 0); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	var size uint32
	if hr := Call(d.inClient, MGetBufferSize, uintptr(unsafe.Pointer(&size))); hr != 0 {
		return failed("reading the input's buffer size", hr)
	}
	var latency, period, least int64
	_ = Call(d.inClient, MGetStreamLatency, uintptr(unsafe.Pointer(&latency)))
	_ = Call(d.inClient, MGetDevicePeriod, uintptr(unsafe.Pointer(&period)), uintptr(unsafe.Pointer(&least)))
	h, err := d.event()
	if err != nil {
		return err
	}
	if hr := Call(d.inClient, MSetEventHandle, uintptr(h)); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	if hr := Call(d.inClient, MGetService, uintptr(unsafe.Pointer(&IIDAudioCaptureClient)), uintptr(unsafe.Pointer(&d.capture))); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	d.info.Inputs, d.info.InputBuffer = ch, int(size)
	d.info.InputLatency = int(audio.FramesAt(time.Duration(latency+period)*100, rate))
	d.nextPacket = Method(d.capture, MGetNextPacketSize)
	d.capGet, d.capRelease = Method(d.capture, MGetBuffer), Method(d.capture, MReleaseBuffer)
	return nil
}

// dropInput lets an input go that failed to open.
func (d *Device) dropInput() {
	Release(d.capture)
	Release(d.inClient)
	Release(d.inDev)
	d.capture, d.inClient, d.inDev = 0, 0, 0
	if d.nev == 2 {
		_ = windows.CloseHandle(d.events[1])
		d.nev = 1
	}
	d.info.Inputs, d.info.InputBuffer, d.info.InputLatency = 0, 0, 0
}

// Info says how the device plays.
func (d *Device) Info() Info { return d.info }

// Start starts the output playing, and the input recording.
func (d *Device) Start() error {
	if hr := Call(d.client, MStart); !succeeded(hr) {
		return failed("starting to play", hr)
	}
	if d.capture != 0 {
		if hr := Call(d.inClient, MStart); !succeeded(hr) {
			return failed("starting to record", hr)
		}
	}
	return nil
}

// Stop stops both, keeping what the output's buffer holds.
func (d *Device) Stop() error {
	var err error
	if d.capture != 0 {
		if hr := Call(d.inClient, MStop); !succeeded(hr) {
			err = failed("stopping to record", hr)
		}
	}
	if hr := Call(d.client, MStop); !succeeded(hr) {
		err = failed("stopping to play", hr)
	}
	return err
}

// Wait waits for the output to want frames, or the input to have a
// packet, for d at most.
func (d *Device) Wait(t time.Duration) {
	_, _ = windows.WaitForMultipleObjects(d.events[:d.nev], false, uint32(t/time.Millisecond))
}

// Room returns how many frames the output's buffer has room for.
func (d *Device) Room() (int, error) {
	if hr, _, _ := syscall.SyscallN(d.getPadding, d.client, uintptr(unsafe.Pointer(&d.p.padding))); uint32(hr) != 0 {
		return 0, failed("playing", uint32(hr))
	}
	return d.info.Buffer - int(d.p.padding), nil
}

// Write adds frames, stereo pairs of interleaved samples, to the
// output's buffer, which must have room for them: on its first two
// channels, the rest silent, or averaged onto its one.
func (d *Device) Write(frames []float32) error {
	n := len(frames) / 2
	if n == 0 {
		return nil
	}
	if hr, _, _ := syscall.SyscallN(d.getBuffer, d.render, uintptr(n), uintptr(unsafe.Pointer(&d.p.data))); uint32(hr) != 0 {
		return failed("playing", uint32(hr))
	}
	ch := d.info.Channels
	dst := unsafe.Slice(Ptr[float32](d.p.data), n*ch)
	switch ch {
	case 2:
		copy(dst, frames[:2*n])
	case 1:
		for i := range n {
			dst[i] = (frames[2*i] + frames[2*i+1]) / 2
		}
	default:
		clear(dst)
		for i := range n {
			dst[i*ch], dst[i*ch+1] = frames[2*i], frames[2*i+1]
		}
	}
	if hr, _, _ := syscall.SyscallN(d.releaseBuffer, d.render, uintptr(n), 0); uint32(hr) != 0 {
		return failed("playing", uint32(hr))
	}
	return nil
}

// Silence adds n frames of silence to the output's buffer.
func (d *Device) Silence(n int) error {
	if n <= 0 {
		return nil
	}
	if hr, _, _ := syscall.SyscallN(d.getBuffer, d.render, uintptr(n), uintptr(unsafe.Pointer(&d.p.data))); uint32(hr) != 0 {
		return failed("playing", uint32(hr))
	}
	if hr, _, _ := syscall.SyscallN(d.releaseBuffer, d.render, uintptr(n), BufferFlagsSilent); uint32(hr) != 0 {
		return failed("playing", uint32(hr))
	}
	return nil
}

// Clock returns the output's frame at the speakers, counted from the
// first written, as IAudioClock tells it, and the moment it was there,
// in nanoseconds of the performance counter.
func (d *Device) Clock() (pos, at int64, err error) {
	if hr, _, _ := syscall.SyscallN(d.getPosition, d.clock, uintptr(unsafe.Pointer(&d.p.pos)), uintptr(unsafe.Pointer(&d.p.qpc))); uint32(hr) != 0 {
		return 0, 0, failed("reading the clock", uint32(hr))
	}
	return int64(d.p.pos * uint64(d.info.Rate) / d.freq), int64(d.p.qpc) * 100, nil
}

// Read takes the next packet the input recorded into dst, as many
// frames as it holds, of Info.Inputs channels, and returns how many,
// and the moment its first frame came in, in nanoseconds of the
// performance counter: 0 where WASAPI could not tell. ok is false
// where no packet waits. A packet the device marks silent reads as
// silence.
func (d *Device) Read(dst []float32) (n int, at int64, ok bool, err error) {
	if d.capture == 0 {
		return 0, 0, false, nil
	}
	if hr, _, _ := syscall.SyscallN(d.nextPacket, d.capture, uintptr(unsafe.Pointer(&d.p.n))); uint32(hr) != 0 {
		return 0, 0, false, failed("recording", uint32(hr))
	}
	if d.p.n == 0 {
		return 0, 0, false, nil
	}
	if hr, _, _ := syscall.SyscallN(d.capGet, d.capture, uintptr(unsafe.Pointer(&d.p.data)), uintptr(unsafe.Pointer(&d.p.frames)),
		uintptr(unsafe.Pointer(&d.p.flags)), uintptr(unsafe.Pointer(&d.p.devPos)), uintptr(unsafe.Pointer(&d.p.qpc))); uint32(hr) != 0 {
		return 0, 0, false, failed("recording", uint32(hr))
	}
	ch := d.info.Inputs
	frames := int(d.p.frames)
	n = min(frames, len(dst)/ch)
	if d.p.flags&BufferFlagsSilent != 0 || d.p.data == 0 {
		clear(dst[:n*ch])
	} else {
		copy(dst[:n*ch], unsafe.Slice(Ptr[float32](d.p.data), n*ch))
	}
	if d.p.flags&BufferFlagsTimestampError == 0 {
		at = int64(d.p.qpc) * 100
	}
	if hr, _, _ := syscall.SyscallN(d.capRelease, d.capture, uintptr(frames)); uint32(hr) != 0 {
		return 0, 0, false, failed("recording", uint32(hr))
	}
	return n, at, true, nil
}

// Changed says the device should open again: the default output
// changed, or the default input where it records or was asked to, or
// its endpoint went or was set to another format. It reads what the
// notifier counted, and asks the devices only where that moved.
func (d *Device) Changed() bool {
	now := theNotifier().read()
	if now == d.seen {
		return false
	}
	was := d.seen
	d.seen = now
	if now.renderDefault != was.renderDefault || (d.cfg.Record && now.captureDefault != was.captureDefault) {
		return true
	}
	if now.states != was.states && (!active(d.dev) || (d.inDev != 0 && !active(d.inDev))) {
		return true
	}
	if now.formats != was.formats {
		ch, rate, _, err := MixFormat(d.client)
		if err != nil || ch != d.info.Channels || rate != d.info.Rate {
			return true
		}
	}
	return false
}

// Close stops the device and lets it go, on the thread that opened it.
// Later calls do nothing.
func (d *Device) Close() {
	if d.closed {
		return
	}
	d.closed = true
	if d.client != 0 {
		_ = d.Stop()
	}
	if d.registered {
		Call(d.enum, mUnregisterNotification, theNotifier().addr())
	}
	for _, o := range []uintptr{d.capture, d.inClient, d.inDev, d.clock, d.render, d.client, d.dev, d.enum} {
		Release(o)
	}
	for _, h := range d.events[:d.nev] {
		_ = windows.CloseHandle(h)
	}
	d.leaveCOM()
}
