package wasapi

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The interfaces' IDs, and the classes'.
var (
	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	// IIDAudioClient is IAudioClient's ID, and iidIAudioClient3 that of
	// IAudioClient3, which Windows 10 added.
	IIDAudioClient         = windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	iidIAudioClient3       = windows.GUID{Data1: 0x7ed4ee07, Data2: 0x8e67, Data3: 0x4cd4, Data4: [8]byte{0x8c, 0x1a, 0x2b, 0x7a, 0x59, 0x87, 0xad, 0x42}}
	IIDAudioCaptureClient  = windows.GUID{Data1: 0xc8adbd64, Data2: 0xe71e, Data3: 0x48a0, Data4: [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
	iidIAudioRenderClient  = windows.GUID{Data1: 0xf294acfc, Data2: 0x3146, Data3: 0x4483, Data4: [8]byte{0xa7, 0xbf, 0xad, 0xdc, 0xa7, 0xc2, 0x60, 0xe2}}
	iidIAudioClock         = windows.GUID{Data1: 0xcd63314f, Data2: 0x3fba, Data3: 0x4a1b, Data4: [8]byte{0x81, 0x2c, 0xef, 0x96, 0x35, 0x87, 0x28, 0xe7}}
	iidIUnknown            = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIMMNotificationClnt = windows.GUID{Data1: 0x7991eec9, Data2: 0x7e89, Data3: 0x4d85, Data4: [8]byte{0x83, 0x90, 0x6c, 0x70, 0x3c, 0xec, 0x60, 0xc0}}
	// SubtypeIEEEFloat is KSDATAFORMAT_SUBTYPE_IEEE_FLOAT.
	SubtypeIEEEFloat = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	// pkeyFriendlyName is PKEY_Device_FriendlyName.
	pkeyFriendlyName = propertyKey{fmtid: windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd, Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, pid: 14}
	// pkeyDeviceFormat is PKEY_AudioEngine_DeviceFormat: the format the
	// device is set to in the Sound control panel.
	pkeyDeviceFormat = propertyKey{fmtid: windows.GUID{Data1: 0xf19f064d, Data2: 0x082c, Data3: 0x4e27, Data4: [8]byte{0xbc, 0x73, 0x68, 0x82, 0xa1, 0xbb, 0x8e, 0x4c}}, pid: 0}
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procPropVariantClear = ole32.NewProc("PropVariantClear")
)

// The methods of the interfaces, by their place in each one's table.
const (
	mQueryInterface = 0
	mAddRef         = 1
	MRelease        = 2

	// IMMDeviceEnumerator's.
	MEnumAudioEndpoints      = 3
	MGetDefaultAudioEndpoint = 4
	MGetDevice               = 5
	mRegisterNotification    = 6
	mUnregisterNotification  = 7

	// IMMDeviceCollection's.
	MGetCount = 3
	MItem     = 4

	// IMMDevice's.
	MActivate          = 3
	mOpenPropertyStore = 4
	mGetID             = 5
	mGetState          = 6

	// IPropertyStore's.
	mGetValue = 5

	// IAudioClient's.
	MInitialize        = 3
	MGetBufferSize     = 4
	MGetStreamLatency  = 5
	mGetCurrentPadding = 6
	MGetMixFormat      = 8
	MGetDevicePeriod   = 9
	MStart             = 10
	MStop              = 11
	MSetEventHandle    = 13
	MGetService        = 14

	// IAudioClient3's, past IAudioClient2's three.
	mGetSharedModeEnginePeriod        = 18
	mGetCurrentSharedModeEnginePeriod = 19
	mInitializeSharedAudioStream      = 20

	// IAudioRenderClient's.
	mRenderGetBuffer     = 3
	mRenderReleaseBuffer = 4

	// IAudioCaptureClient's.
	MGetBuffer         = 3
	MReleaseBuffer     = 4
	MGetNextPacketSize = 5

	// IAudioClock's.
	mGetFrequency = 3
	mGetPosition  = 4
)

// Values the calls take and return: flows and roles of endpoints, their
// states, formats, stream and buffer flags, and HRESULTs.
const (
	ERender            = 0
	ECapture           = 1
	EConsole           = 0
	DeviceStateActive  = 1
	ClsctxAll          = 0x17
	stgmRead           = 0
	vtLPWSTR           = 31
	WaveFormatExtended = 0xfffe
	waveFormatIEEE     = 3

	StreamFlagsEventCallback     = 0x00040000
	StreamFlagsAutoConvertPCM    = 0x80000000
	StreamFlagsSRCDefaultQuality = 0x08000000

	BufferFlagsSilent         = 0x2
	BufferFlagsTimestampError = 0x4

	errDeviceInvalidated = 0x88890004
	errServiceNotRunning = 0x88890010
	errNotFound          = 0x80070490
	eNoInterface         = 0x80004002
	sOK                  = 0
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

// WaveFormat is WAVEFORMATEXTENSIBLE.
type WaveFormat struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
	ValidBits      uint16
	ChannelMask    uint32
	SubFormat      windows.GUID
}

// Float32 returns a format of float32 samples, of ch channels at rate,
// in the channel layout mask.
func Float32(ch, rate int, mask uint32) WaveFormat {
	return WaveFormat{FormatTag: WaveFormatExtended, Channels: uint16(ch), SamplesPerSec: uint32(rate),
		AvgBytesPerSec: uint32(rate * ch * 4), BlockAlign: uint16(ch * 4), BitsPerSample: 32, Size: 22,
		ValidBits: 32, ChannelMask: mask, SubFormat: SubtypeIEEEFloat}
}

// isFloat32 says f is of float32 samples.
func (f *WaveFormat) isFloat32() bool {
	if f.BitsPerSample != 32 {
		return false
	}
	return f.FormatTag == waveFormatIEEE || (f.FormatTag == WaveFormatExtended && f.SubFormat == SubtypeIEEEFloat)
}

// Call calls method m of the COM object obj, and returns its HRESULT.
// It allocates: a loop that must not calls through [Method] and
// syscall.SyscallN.
//
//go:uintptrescapes
func Call(obj uintptr, m int, args ...uintptr) uint32 {
	var a [8]uintptr
	a[0] = obj
	n := copy(a[1:], args)
	r, _, _ := syscall.SyscallN(Method(obj, m), a[:1+n]...)
	return uint32(r)
}

// Method returns the function at place m of obj's table.
func Method(obj uintptr, m int) uintptr {
	return *(*uintptr)(unsafe.Add(*Ptr[unsafe.Pointer](obj), uintptr(m)*unsafe.Sizeof(uintptr(0))))
}

// Ptr returns p, an address in the system's memory, as a pointer.
func Ptr[T any](p uintptr) *T {
	return *(**T)(unsafe.Pointer(&p))
}

// Release releases obj, where it is set.
func Release(obj uintptr) {
	if obj != 0 {
		Call(obj, MRelease)
	}
}

// succeeded says hr is a success: S_OK, or S_FALSE, as a stream
// stopped already says.
func succeeded(hr uint32) bool { return int32(hr) >= 0 }

// Lost says hr tells of a device gone: unplugged, disabled, or set to
// another format, or of the audio service stopped.
func Lost(hr uint32) bool {
	return hr == errDeviceInvalidated || hr == errNotFound || hr == errServiceNotRunning
}

// HRESULTError is a failed HRESULT, as Op met it.
type HRESULTError struct {
	Op string
	HR uint32
}

func (e *HRESULTError) Error() string {
	return fmt.Sprintf("wasapi: %s: %v", e.Op, windows.Errno(e.HR))
}

// failed returns ErrLost for a device gone, or what op met.
func failed(op string, hr uint32) error {
	if Lost(hr) {
		return ErrLost
	}
	return &HRESULTError{Op: op, HR: hr}
}

// InitCOM joins the calling thread, which must be locked, to COM's
// multithreaded apartment, and returns what leaves it.
func InitCOM() (leave func(), err error) {
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return nil, fmt.Errorf("wasapi: starting COM: %w", err)
	}
	return windows.CoUninitialize, nil
}

// OnCOMThread runs f on a thread of its own in COM's multithreaded
// apartment, and returns once f does, with its error.
func OnCOMThread(f func() error) error {
	errc := make(chan error, 1)
	go func() {
		// The thread ends with the goroutine, its COM state with it.
		runtime.LockOSThread()
		leave, err := InitCOM()
		if err != nil {
			errc <- err
			return
		}
		defer leave()
		errc <- f()
	}()
	return <-errc
}

// Enumerator returns a new IMMDeviceEnumerator.
func Enumerator() (uintptr, error) {
	var e uintptr
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, ClsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&e))); uint32(hr) != 0 {
		return 0, failed("listing the devices", uint32(hr))
	}
	return e, nil
}

// takeString returns the string at p, which COM made, and frees it.
func takeString(p uintptr) string {
	s := windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&p)))
	windows.CoTaskMemFree(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
	return s
}

// DeviceID returns the ID of the IMMDevice dev.
func DeviceID(dev uintptr) string {
	var p uintptr
	if Call(dev, mGetID, uintptr(unsafe.Pointer(&p))) != 0 || p == 0 {
		return ""
	}
	return takeString(p)
}

// active says the IMMDevice dev is active: present, and enabled.
func active(dev uintptr) bool {
	var st uint32
	return Call(dev, mGetState, uintptr(unsafe.Pointer(&st))) == 0 && st == DeviceStateActive
}

// FriendlyName returns the name people know dev by.
func FriendlyName(dev uintptr) string {
	var store uintptr
	if Call(dev, mOpenPropertyStore, stgmRead, uintptr(unsafe.Pointer(&store))) != 0 {
		return ""
	}
	defer Release(store)
	var pv propVariant
	if Call(store, mGetValue, uintptr(unsafe.Pointer(&pkeyFriendlyName)), uintptr(unsafe.Pointer(&pv))) != 0 {
		return ""
	}
	name := ""
	if pv.vt == vtLPWSTR && pv.val != 0 {
		name = windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&pv.val)))
	}
	_, _, _ = procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	return name
}

// engineFormat returns the format the IAudioClient client mixes at, the
// engine's, in COM's memory: free it with windows.CoTaskMemFree.
func engineFormat(client uintptr) (*WaveFormat, error) {
	var p uintptr
	if hr := Call(client, MGetMixFormat, uintptr(unsafe.Pointer(&p))); hr != 0 {
		return nil, failed("reading the device's format", hr)
	}
	return Ptr[WaveFormat](p), nil
}

// MixFormat returns the channels and rate the IAudioClient client
// mixes at, and its channel layout.
func MixFormat(client uintptr) (channels, rate int, mask uint32, err error) {
	f, err := engineFormat(client)
	if err != nil {
		return 0, 0, 0, err
	}
	channels, rate = int(f.Channels), int(f.SamplesPerSec)
	if f.FormatTag == WaveFormatExtended {
		mask = f.ChannelMask
	}
	windows.CoTaskMemFree(unsafe.Pointer(f))
	return channels, rate, mask, nil
}
