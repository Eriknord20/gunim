package capture

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/audio"
)

// The system's inputs are WASAPI's capture endpoints, in shared mode:
// Windows converts the device's format to float32 at the rate asked
// for. The package speaks to them through its own definitions of their
// COM interfaces, in Go.

var (
	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidIAudioClient         = windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	iidIAudioCaptureClient  = windows.GUID{Data1: 0xc8adbd64, Data2: 0xe71e, Data3: 0x48a0, Data4: [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
	subtypeIEEEFloat        = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	// pkeyFriendlyName is PKEY_Device_FriendlyName.
	pkeyFriendlyName = propertyKey{fmtid: windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd, Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, pid: 14}
)

var (
	ole32                         = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance          = ole32.NewProc("CoCreateInstance")
	procPropVariantClear          = ole32.NewProc("PropVariantClear")
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
)

// The methods of the interfaces, by their place in each one's table.
const (
	mRelease = 2

	// IMMDeviceEnumerator's.
	mEnumAudioEndpoints      = 3
	mGetDefaultAudioEndpoint = 4
	mGetDevice               = 5

	// IMMDeviceCollection's.
	mGetCount = 3
	mItem     = 4

	// IMMDevice's.
	mActivate          = 3
	mOpenPropertyStore = 4
	mGetID             = 5

	// IPropertyStore's.
	mGetValue = 5

	// IAudioClient's.
	mInitialize       = 3
	mGetBufferSize    = 4
	mGetStreamLatency = 5
	mGetMixFormat     = 8
	mGetDevicePeriod  = 9
	mStart            = 10
	mStop             = 11
	mSetEventHandle   = 13
	mGetService       = 14

	// IAudioCaptureClient's.
	mGetBuffer         = 3
	mReleaseBuffer     = 4
	mGetNextPacketSize = 5
)

const (
	eCapture           = 1
	eConsole           = 0
	deviceStateActive  = 1
	clsctxAll          = 0x17
	stgmRead           = 0
	vtLPWSTR           = 31
	waveFormatExtended = 0xfffe

	streamFlagsEventCallback     = 0x00040000
	streamFlagsAutoConvertPCM    = 0x80000000
	streamFlagsSRCDefaultQuality = 0x08000000

	bufferFlagsSilent         = 0x2
	bufferFlagsTimestampError = 0x4

	errDeviceInvalidated = 0x88890004
	errNotFound          = 0x80070490
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// propVariant is PROPVARIANT, as far as a string in it.
type propVariant struct {
	vt       uint16
	_        [3]uint16
	val      uintptr
	_reserve uintptr
}

// waveFormat is WAVEFORMATEXTENSIBLE.
type waveFormat struct {
	formatTag      uint16
	channels       uint16
	samplesPerSec  uint32
	avgBytesPerSec uint32
	blockAlign     uint16
	bitsPerSample  uint16
	size           uint16
	validBits      uint16
	channelMask    uint32
	subFormat      windows.GUID
}

// call calls method m of the COM object obj, and returns its HRESULT.
//
//go:uintptrescapes
func call(obj uintptr, m int, args ...uintptr) uint32 {
	vtbl := *ptr[unsafe.Pointer](obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(m)*unsafe.Sizeof(uintptr(0))))
	var a [8]uintptr
	a[0] = obj
	n := copy(a[1:], args)
	r, _, _ := syscall.SyscallN(fn, a[:1+n]...)
	return uint32(r)
}

// ptr returns p, an address in the system's memory, as a pointer.
func ptr[T any](p uintptr) *T {
	return *(**T)(unsafe.Pointer(&p))
}

func release(obj uintptr) {
	if obj != 0 {
		call(obj, mRelease)
	}
}

// hresultError is a failed HRESULT, as op met it.
type hresultError struct {
	op string
	hr uint32
}

func (e *hresultError) Error() string {
	return fmt.Sprintf("capture: %s: %v", e.op, windows.Errno(e.hr))
}

func failed(op string, hr uint32) error {
	if hr == errDeviceInvalidated || hr == errNotFound {
		return fmt.Errorf("%w (%s)", ErrLost, op)
	}
	return &hresultError{op: op, hr: hr}
}

// onCOMThread runs f on a thread of its own in COM's multithreaded
// apartment, and returns once f does, with its error.
func onCOMThread(f func() error) error {
	errc := make(chan error, 1)
	go func() {
		// The thread ends with the goroutine, its COM state with it.
		runtime.LockOSThread()
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
			errc <- fmt.Errorf("capture: starting COM: %w", err)
			return
		}
		defer windows.CoUninitialize()
		errc <- f()
	}()
	return <-errc
}

// enumerator returns a new IMMDeviceEnumerator.
func enumerator() (uintptr, error) {
	var e uintptr
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&e))); hr != 0 {
		return 0, failed("listing the inputs", uint32(hr))
	}
	return e, nil
}

// takeString returns the string at p, which COM made, and frees it.
func takeString(p uintptr) string {
	s := windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&p)))
	windows.CoTaskMemFree(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
	return s
}

// deviceID returns the ID of the IMMDevice dev.
func deviceID(dev uintptr) string {
	var p uintptr
	if call(dev, mGetID, uintptr(unsafe.Pointer(&p))) != 0 || p == 0 {
		return ""
	}
	return takeString(p)
}

// friendlyName returns the name people know dev by.
func friendlyName(dev uintptr) string {
	var store uintptr
	if call(dev, mOpenPropertyStore, stgmRead, uintptr(unsafe.Pointer(&store))) != 0 {
		return ""
	}
	defer release(store)
	var pv propVariant
	if call(store, mGetValue, uintptr(unsafe.Pointer(&pkeyFriendlyName)), uintptr(unsafe.Pointer(&pv))) != 0 {
		return ""
	}
	name := ""
	if pv.vt == vtLPWSTR && pv.val != 0 {
		name = windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&pv.val)))
	}
	_, _, _ = procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	return name
}

// mixFormat returns the channels and rate dev's IAudioClient client
// mixes at.
func mixFormat(client uintptr) (channels, rate int, mask uint32, err error) {
	var p uintptr
	if hr := call(client, mGetMixFormat, uintptr(unsafe.Pointer(&p))); hr != 0 {
		return 0, 0, 0, failed("reading the input's format", hr)
	}
	f := *(**waveFormat)(unsafe.Pointer(&p))
	channels, rate = int(f.channels), int(f.samplesPerSec)
	if f.formatTag == waveFormatExtended {
		mask = f.channelMask
	}
	windows.CoTaskMemFree(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
	return channels, rate, mask, nil
}

func systemDevices() ([]Device, error) {
	var out []Device
	err := onCOMThread(func() error {
		e, err := enumerator()
		if err != nil {
			return err
		}
		defer release(e)
		def := ""
		var d uintptr
		if call(e, mGetDefaultAudioEndpoint, eCapture, eConsole, uintptr(unsafe.Pointer(&d))) == 0 {
			def = deviceID(d)
			release(d)
		}
		var coll uintptr
		if hr := call(e, mEnumAudioEndpoints, eCapture, deviceStateActive, uintptr(unsafe.Pointer(&coll))); hr != 0 {
			return failed("listing the inputs", hr)
		}
		defer release(coll)
		var n uint32
		if hr := call(coll, mGetCount, uintptr(unsafe.Pointer(&n))); hr != 0 {
			return failed("counting the inputs", hr)
		}
		for i := range n {
			var dev uintptr
			if call(coll, mItem, uintptr(i), uintptr(unsafe.Pointer(&dev))) != 0 {
				continue
			}
			id := deviceID(dev)
			d := Device{ID: id, Name: friendlyName(dev), Default: id == def}
			var client uintptr
			if call(dev, mActivate, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client))) == 0 {
				d.Channels, d.Rate, _, _ = mixFormat(client)
				release(client)
			}
			release(dev)
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
	err := onCOMThread(func() error {
		var client, capture uintptr
		defer func() {
			release(capture)
			release(client)
		}()
		if err := b.open(&client, &capture); err != nil {
			return err
		}
		tell(nil)
		<-b.begin
		if b.quit.Load() {
			return nil
		}
		if hr := call(client, mStart); hr != 0 {
			return failed("starting to record", hr)
		}
		defer call(client, mStop)
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
	defer release(e)
	var dev uintptr
	if b.id == "" {
		if hr := call(e, mGetDefaultAudioEndpoint, eCapture, eConsole, uintptr(unsafe.Pointer(&dev))); hr != 0 {
			return failed("finding the default input", hr)
		}
	} else {
		id, err := windows.UTF16PtrFromString(b.id)
		if err != nil {
			return err
		}
		if hr := call(e, mGetDevice, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&dev))); hr != 0 {
			return failed("finding the input "+b.id, hr)
		}
	}
	defer release(dev)
	name := friendlyName(dev)
	if hr := call(dev, mActivate, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(client))); hr != 0 {
		return failed("opening "+name, hr)
	}
	ch, rate, mask, err := mixFormat(*client)
	if err != nil {
		return err
	}
	if b.rate > 0 {
		rate = b.rate
	}
	wf := waveFormat{formatTag: waveFormatExtended, channels: uint16(ch), samplesPerSec: uint32(rate),
		avgBytesPerSec: uint32(rate * ch * 4), blockAlign: uint16(ch * 4), bitsPerSample: 32, size: 22,
		validBits: 32, channelMask: mask, subFormat: subtypeIEEEFloat}
	// The buffer holds plenty: the device hands each packet on as it
	// comes, a period of the engine's, whatever the buffer's size.
	hns := int64(max(10*b.buffer, 100*time.Millisecond) / 100)
	if hr := call(*client, mInitialize, 0, streamFlagsEventCallback|streamFlagsAutoConvertPCM|streamFlagsSRCDefaultQuality,
		uintptr(hns), 0, uintptr(unsafe.Pointer(&wf)), 0); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	var size uint32
	if hr := call(*client, mGetBufferSize, uintptr(unsafe.Pointer(&size))); hr != 0 {
		return failed("reading the buffer's size", hr)
	}
	var latency, period int64
	_ = call(*client, mGetStreamLatency, uintptr(unsafe.Pointer(&latency)))
	_ = call(*client, mGetDevicePeriod, uintptr(unsafe.Pointer(&period)), 0)
	if hr := call(*client, mSetEventHandle, uintptr(b.event)); hr != 0 {
		return failed("readying "+name+" to record", hr)
	}
	if hr := call(*client, mGetService, uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(capture))); hr != 0 {
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

// method returns the function at place m of obj's table.
func method(obj uintptr, m int) uintptr {
	return *(*uintptr)(unsafe.Add(*ptr[unsafe.Pointer](obj), uintptr(m)*unsafe.Sizeof(uintptr(0))))
}

// loop hands each packet the device records to the stream, until
// close. It runs on the device's thread, and calls the device directly,
// as call would allocate.
func (b *wasapiIn) loop(capture uintptr) error {
	var freq int64
	_, _, _ = procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&freq)))
	counter := procQueryPerformanceCounter.Addr()
	getNext, getBuffer, releaseBuffer := method(capture, mGetNextPacketSize), method(capture, mGetBuffer), method(capture, mReleaseBuffer)
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
			if hr, _, _ := syscall.SyscallN(getNext, capture, uintptr(unsafe.Pointer(&p.n))); hr != 0 {
				return failed("recording", uint32(hr))
			}
			if p.n == 0 {
				break
			}
			if hr, _, _ := syscall.SyscallN(getBuffer, capture, uintptr(unsafe.Pointer(&p.data)), uintptr(unsafe.Pointer(&p.frames)),
				uintptr(unsafe.Pointer(&p.flags)), uintptr(unsafe.Pointer(&p.devPos)), uintptr(unsafe.Pointer(&p.at))); hr != 0 {
				return failed("recording", uint32(hr))
			}
			idle = time.Now()
			if frames := int(p.frames); frames > 0 {
				in := b.silence[:min(len(b.silence), frames*ch)]
				if p.flags&bufferFlagsSilent == 0 && p.data != 0 {
					in = unsafe.Slice(ptr[float32](p.data), frames*ch)
				}
				// The packet's first frame reached the input at the
				// time WASAPI stamped it with, in 100 nanoseconds of
				// the performance counter.
				ago := int64(b.f.latency) + int64(frames)
				if p.flags&bufferFlagsTimestampError == 0 && p.at != 0 && freq > 0 {
					_, _, _ = syscall.SyscallN(counter, uintptr(unsafe.Pointer(&p.now)))
					now := p.now/freq*1e7 + p.now%freq*1e7/freq
					ago = (now - int64(p.at)) * int64(b.f.rate) / 1e7
				}
				b.s.putAgo(in, ago)
			}
			if hr, _, _ := syscall.SyscallN(releaseBuffer, capture, uintptr(p.frames)); hr != 0 {
				return failed("recording", uint32(hr))
			}
		}
	}
	return nil
}
