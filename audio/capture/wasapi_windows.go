package capture

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/internal/wasapi"
)

// The system's inputs are WASAPI's capture endpoints, in shared mode:
// Windows converts the device's format to float32 at the rate asked
// for. The package speaks to them through package audio/internal/wasapi's
// definitions of their COM interfaces, in Go.

var (
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
)

// hresultError is a failed HRESULT, as op met it.
type hresultError struct {
	op string
	hr uint32
}

func (e *hresultError) Error() string {
	return fmt.Sprintf("capture: %s: %v", e.op, windows.Errno(e.hr))
}

func failed(op string, hr uint32) error {
	if wasapi.Lost(hr) {
		return ErrLost
	}
	return &hresultError{op: op, hr: hr}
}

// enumerator returns a new IMMDeviceEnumerator.
func enumerator() (uintptr, error) {
	e, err := wasapi.Enumerator()
	if errors.Is(err, wasapi.ErrLost) {
		return 0, ErrLost
	}
	var h *wasapi.HRESULTError
	if errors.As(err, &h) {
		return 0, failed("listing the inputs", h.HR)
	}
	return e, err
}

func systemDevices() ([]Device, error) {
	var out []Device
	err := wasapi.OnCOMThread(func() error {
		e, err := enumerator()
		if err != nil {
			return err
		}
		defer wasapi.Release(e)
		def := ""
		var d uintptr
		if wasapi.Call(e, wasapi.MGetDefaultAudioEndpoint, wasapi.ECapture, wasapi.EConsole, uintptr(unsafe.Pointer(&d))) == 0 {
			def = wasapi.DeviceID(d)
			wasapi.Release(d)
		}
		var coll uintptr
		if hr := wasapi.Call(e, wasapi.MEnumAudioEndpoints, wasapi.ECapture, wasapi.DeviceStateActive, uintptr(unsafe.Pointer(&coll))); hr != 0 {
			return failed("listing the inputs", hr)
		}
		defer wasapi.Release(coll)
		var n uint32
		if hr := wasapi.Call(coll, wasapi.MGetCount, uintptr(unsafe.Pointer(&n))); hr != 0 {
			return failed("counting the inputs", hr)
		}
		for i := range n {
			var dev uintptr
			if wasapi.Call(coll, wasapi.MItem, uintptr(i), uintptr(unsafe.Pointer(&dev))) != 0 {
				continue
			}
			id := wasapi.DeviceID(dev)
			d := Device{ID: id, Name: wasapi.FriendlyName(dev), Default: id == def}
			var client uintptr
			if wasapi.Call(dev, wasapi.MActivate, uintptr(unsafe.Pointer(&wasapi.IIDAudioClient)), wasapi.ClsctxAll, 0, uintptr(unsafe.Pointer(&client))) == 0 {
				d.Channels, d.Rate, _, _ = wasapi.MixFormat(client)
				wasapi.Release(client)
			}
			wasapi.Release(dev)
			out = append(out, d)
		}
		return nil
	})
	return out, err
}

// wasapiIn records a WASAPI capture endpoint, on a thread of its own.
type wasapiIn struct {
	s      *Stream
	id     string
	rate   int
	buffer time.Duration
	f      format
	quit   atomic.Bool
	event  windows.Handle
	// begin lets the thread start recording, once the stream is
	// ready; silence stands in for a packet the device marks silent.
	begin   chan struct{}
	started bool
	silence []float32
	done    chan struct{}
}

func openSystem(s *Stream, id string, rate int, buffer time.Duration) (format, error) {
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return format{}, err
	}
	b := &wasapiIn{s: s, id: id, rate: rate, buffer: buffer, event: event, begin: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go b.run(ready)
	if err := <-ready; err != nil {
		<-b.done
		_ = windows.CloseHandle(event)
		return format{}, err
	}
	s.b = b
	return b.f, nil
}

func (b *wasapiIn) start() error {
	b.started = true
	close(b.begin)
	return nil
}

// close runs once, from Open or Close.
func (b *wasapiIn) close() error {
	b.quit.Store(true)
	if !b.started {
		close(b.begin)
	}
	_ = windows.SetEvent(b.event)
	<-b.done
	return windows.CloseHandle(b.event)
}

// run opens the device, tells ready, and records until close, on a
// thread of its own.
func (b *wasapiIn) run(ready chan<- error) {
	defer close(b.done)
	told := false
	tell := func(err error) {
		if !told {
			told = true
			ready <- err
		}
	}
	err := wasapi.OnCOMThread(func() error {
		var client, capture uintptr
		defer func() {
			wasapi.Release(capture)
			wasapi.Release(client)
		}()
		if err := b.open(&client, &capture); err != nil {
			return err
		}
		tell(nil)
		<-b.begin
		if b.quit.Load() {
			return nil
		}
		if hr := wasapi.Call(client, wasapi.MStart); hr != 0 {
			return failed("starting to record", hr)
		}
		defer wasapi.Call(client, wasapi.MStop)
		return b.loop(capture)
	})
	if !told {
		tell(err)
		return
	}
	if err != nil && !b.quit.Load() {
		b.s.lose(err)
	}
}

// open opens the device and readies it to record. It runs on the
// device's thread.
func (b *wasapiIn) open(client, capture *uintptr) error {
	e, err := enumerator()
	if err != nil {
		return err
	}
	defer wasapi.Release(e)
	var dev uintptr
	if b.id == "" {
		if hr := wasapi.Call(e, wasapi.MGetDefaultAudioEndpoint, wasapi.ECapture, wasapi.EConsole, uintptr(unsafe.Pointer(&dev))); hr != 0 {
			return failed("finding the default input", hr)
		}
	} else {
		id, err := windows.UTF16PtrFromString(b.id)
		if err != nil {
			return err
		}
		if hr := wasapi.Call(e, wasapi.MGetDevice, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&dev))); hr != 0 {
			return failed("finding the input "+b.id, hr)
		}
	}
	defer wasapi.Release(dev)
	name := wasapi.FriendlyName(dev)
	if hr := wasapi.Call(dev, wasapi.MActivate, uintptr(unsafe.Pointer(&wasapi.IIDAudioClient)), wasapi.ClsctxAll, 0, uintptr(unsafe.Pointer(client))); hr != 0 {
		return failed("opening "+name, hr)
	}
	ch, rate, mask, err := wasapi.MixFormat(*client)
	if err != nil {
		return err
	}
	if b.rate > 0 {
		rate = b.rate
	}
	wf := wasapi.Float32(ch, rate, mask)
	// The buffer holds plenty: the device hands each packet on as it
	// comes, a period of the engine's, whatever the buffer's size.
	hns := int64(max(10*b.buffer, 100*time.Millisecond) / 100)
	if hr := wasapi.Call(*client, wasapi.MInitialize, 0, wasapi.StreamFlagsEventCallback|wasapi.StreamFlagsAutoConvertPCM|wasapi.StreamFlagsSRCDefaultQuality,
		uintptr(hns), 0, uintptr(unsafe.Pointer(&wf)), 0); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	var size uint32
	if hr := wasapi.Call(*client, wasapi.MGetBufferSize, uintptr(unsafe.Pointer(&size))); hr != 0 {
		return failed("reading the buffer's size", hr)
	}
	var latency, period int64
	_ = wasapi.Call(*client, wasapi.MGetStreamLatency, uintptr(unsafe.Pointer(&latency)))
	_ = wasapi.Call(*client, wasapi.MGetDevicePeriod, uintptr(unsafe.Pointer(&period)), 0)
	if hr := wasapi.Call(*client, wasapi.MSetEventHandle, uintptr(b.event)); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	if hr := wasapi.Call(*client, wasapi.MGetService, uintptr(unsafe.Pointer(&wasapi.IIDAudioCaptureClient)), uintptr(unsafe.Pointer(capture))); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	periodFrames := int(audio.FramesAt(time.Duration(period)*100, rate))
	b.f = format{name: name, channels: ch, rate: rate, buffer: max(1, periodFrames),
		latency: int(audio.FramesAt(time.Duration(latency)*100, rate)) + periodFrames}
	b.silence = make([]float32, int(size)*ch)
	return nil
}

// packet holds what the loop's calls write, made once, so the loop
// allocates nothing.
type packet struct {
	n, frames, flags uint32
	data             uintptr
	devPos, at       uint64
	now              int64
}

// loop hands each packet the device records to the stream, until
// close. It runs on the device's thread, and calls the device directly,
// as call would allocate.
func (b *wasapiIn) loop(capture uintptr) error {
	var freq int64
	_, _, _ = procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&freq)))
	counter := procQueryPerformanceCounter.Addr()
	getNext, getBuffer, releaseBuffer := wasapi.Method(capture, wasapi.MGetNextPacketSize), wasapi.Method(capture, wasapi.MGetBuffer), wasapi.Method(capture, wasapi.MReleaseBuffer)
	p := new(packet)
	ch := b.f.channels
	idle := time.Now()
	for !b.quit.Load() {
		r, _ := windows.WaitForSingleObject(b.event, 200)
		if r == uint32(windows.WAIT_TIMEOUT) {
			if time.Since(idle) > lost {
				return ErrLost
			}
			continue
		}
		for !b.quit.Load() {
			if hr, _, _ := syscall.SyscallN(getNext, capture, uintptr(unsafe.Pointer(&p.n))); uint32(hr) != 0 {
				return failed("recording", uint32(hr))
			}
			if p.n == 0 {
				break
			}
			if hr, _, _ := syscall.SyscallN(getBuffer, capture, uintptr(unsafe.Pointer(&p.data)), uintptr(unsafe.Pointer(&p.frames)),
				uintptr(unsafe.Pointer(&p.flags)), uintptr(unsafe.Pointer(&p.devPos)), uintptr(unsafe.Pointer(&p.at))); uint32(hr) != 0 {
				return failed("recording", uint32(hr))
			}
			idle = time.Now()
			if frames := int(p.frames); frames > 0 {
				in := b.silence[:min(len(b.silence), frames*ch)]
				if p.flags&wasapi.BufferFlagsSilent == 0 && p.data != 0 {
					in = unsafe.Slice(wasapi.Ptr[float32](p.data), frames*ch)
				}
				// The packet's first frame reached the input at the
				// time WASAPI stamped it with, in 100 nanoseconds of
				// the performance counter.
				ago := int64(b.f.latency) + int64(frames)
				if p.flags&wasapi.BufferFlagsTimestampError == 0 && p.at != 0 && freq > 0 {
					_, _, _ = syscall.SyscallN(counter, uintptr(unsafe.Pointer(&p.now)))
					now := p.now/freq*1e7 + p.now%freq*1e7/freq
					ago = (now - int64(p.at)) * int64(b.f.rate) / 1e7
				}
				b.s.putAgo(in, ago)
			}
			if hr, _, _ := syscall.SyscallN(releaseBuffer, capture, uintptr(p.frames)); uint32(hr) != 0 {
				return failed("recording", uint32(hr))
			}
		}
	}
	return nil
}
