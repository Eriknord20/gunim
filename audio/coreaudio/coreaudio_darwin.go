//go:build darwin && !ios

package coreaudio

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	noErr = 0

	systemObject = 1

	scopeGlobal = 0x676c6f62 // 'glob'
	scopeInput  = 0x696e7074 // 'inpt'
	scopeOutput = 0x6f757470 // 'outp'
	elementMain = 0

	propDefaultOutputDevice = 0x644f7574 // 'dOut'
	propDefaultInputDevice  = 0x64496e20 // 'dIn '
	propDevices             = 0x64657623 // 'dev#'
	propDeviceUID           = 0x75696420 // 'uid '
	propElementName         = 0x6c63686e // 'lchn'
	propName                = 0x6c6e616d // 'lnam'
	propDeviceIsAlive       = 0x6c69766e // 'livn'
	propBufferFrameSize     = 0x6673697a // 'fsiz'
	propBufferFrameRange    = 0x66737a23 // 'fsz#'
	propNominalSampleRate   = 0x6e737274 // 'nsrt'
	propLatency             = 0x6c746e63 // 'ltnc', of a device and of a stream
	propSafetyOffset        = 0x73616674 // 'saft'
	propStreams             = 0x73746d23 // 'stm#'
	propStreamConfiguration = 0x736c6179 // 'slay'
	propStreamFormat        = 0x73666d74 // 'sfmt'

	formatLinearPCM = 0x6c70636d // 'lpcm'
	formatIsFloat   = 1 << 0

	cfStringEncodingUTF8 = 0x08000100
)

type propertyAddress struct {
	selector, scope, element uint32
}

type valueRange struct {
	min, max float64
}

type streamDescription struct {
	sampleRate       float64
	formatID         uint32
	formatFlags      uint32
	bytesPerPacket   uint32
	framesPerPacket  uint32
	bytesPerFrame    uint32
	channelsPerFrame uint32
	bitsPerChannel   uint32
	reserved         uint32
}

// audioBuffer is an AudioBuffer: an AudioBufferList holds a count of
// them, then the buffers, from the next 8-byte boundary.
type audioBuffer struct {
	channels uint32
	bytes    uint32
	data     unsafe.Pointer
}

const bufferListHeader = 8

var (
	getPropertyData     func(id uint32, addr *propertyAddress, qualSize uint32, qual unsafe.Pointer, size *uint32, out unsafe.Pointer) int32
	getPropertyDataSize func(id uint32, addr *propertyAddress, qualSize uint32, qual unsafe.Pointer, size *uint32) int32
	setPropertyData     func(id uint32, addr *propertyAddress, qualSize uint32, qual unsafe.Pointer, size uint32, data unsafe.Pointer) int32
	destroyIOProcID     func(dev uint32, id uintptr) int32
	deviceStart         func(dev uint32, id uintptr) int32
	deviceStop          func(dev uint32, id uintptr) int32
	cfStringGetCString  func(s uintptr, buf *byte, size int, encoding uint32) bool
	cfRelease           func(ref uintptr)
)

var (
	loadOnce sync.Once
	loadErr  error
)

func load() error {
	loadOnce.Do(func() {
		ca, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterLibFunc(&getPropertyData, ca, "AudioObjectGetPropertyData")
		purego.RegisterLibFunc(&getPropertyDataSize, ca, "AudioObjectGetPropertyDataSize")
		purego.RegisterLibFunc(&setPropertyData, ca, "AudioObjectSetPropertyData")
		purego.RegisterLibFunc(&destroyIOProcID, ca, "AudioDeviceDestroyIOProcID")
		purego.RegisterLibFunc(&deviceStart, ca, "AudioDeviceStart")
		purego.RegisterLibFunc(&deviceStop, ca, "AudioDeviceStop")
		purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&createInputProcID, ca, "AudioDeviceCreateIOProcID")
		purego.RegisterLibFunc(&addInputListener, ca, "AudioObjectAddPropertyListener")
		purego.RegisterLibFunc(&removeInputListener, ca, "AudioObjectRemovePropertyListener")
		recordProc = purego.NewCallback(record)
		inputListener = purego.NewCallback(inputChanged)
	})
	return loadErr
}

func get[T any](id, selector, scope uint32, out *T) bool {
	addr := propertyAddress{selector, scope, elementMain}
	size := uint32(unsafe.Sizeof(*out))
	return getPropertyData(id, &addr, 0, nil, &size, unsafe.Pointer(out)) == noErr
}

func set[T any](id, selector, scope uint32, v T) int32 {
	addr := propertyAddress{selector, scope, elementMain}
	return setPropertyData(id, &addr, 0, nil, uint32(unsafe.Sizeof(v)), unsafe.Pointer(&v))
}

// getBytes reads a property of variable size, nil where it is empty.
func getBytes(id, selector, scope uint32) ([]byte, error) {
	addr := propertyAddress{selector, scope, elementMain}
	var size uint32
	if st := getPropertyDataSize(id, &addr, 0, nil, &size); st != noErr {
		return nil, fmt.Errorf("coreaudio: AudioObjectGetPropertyDataSize: %d", st)
	}
	if size == 0 {
		return nil, nil
	}
	// Of uint64s, for the pointers and float64s the property holds.
	buf := make([]uint64, (size+7)/8)
	if st := getPropertyData(id, &addr, 0, nil, &size, unsafe.Pointer(&buf[0])); st != noErr {
		return nil, fmt.Errorf("coreaudio: AudioObjectGetPropertyData: %d", st)
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), size), nil
}

// name returns the device's name.
func name(dev uint32) string { return stringOf(dev, propName, scopeGlobal, elementMain) }

// stringOf returns a property of the object id that is a CFString, or
// "" where it has none.
func stringOf(id, selector, scope, element uint32) string {
	s, err := readString(id, selector, scope, element)
	if err != nil {
		return ""
	}
	return s
}

// readString returns a property of the object id that is a CFString, or
// why it reads none.
func readString(id, selector, scope, element uint32) (string, error) {
	var ref uintptr
	addr := propertyAddress{selector, scope, element}
	size := uint32(unsafe.Sizeof(ref))
	if st := getPropertyData(id, &addr, 0, nil, &size, unsafe.Pointer(&ref)); st != noErr {
		return "", fmt.Errorf("coreaudio: AudioObjectGetPropertyData: %d", st)
	}
	if ref == 0 {
		return "", errors.New("coreaudio: the property holds no string")
	}
	defer cfRelease(ref)
	buf := make([]byte, 256)
	if !cfStringGetCString(ref, &buf[0], len(buf), cfStringEncodingUTF8) {
		return "", errors.New("coreaudio: CFStringGetCString failed")
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), nil
		}
	}
	return string(buf), nil
}

// bufferChannels returns how many channels each of the device's buffers
// of scope holds, in order.
func bufferChannels(dev, scope uint32) ([]int, error) {
	b, err := getBytes(dev, propStreamConfiguration, scope)
	if err != nil || len(b) < bufferListHeader {
		return nil, err
	}
	n := int(*(*uint32)(unsafe.Pointer(&b[0])))
	size := int(unsafe.Sizeof(audioBuffer{}))
	var chans []int
	for i := range n {
		at := bufferListHeader + i*size
		if at+size > len(b) {
			break
		}
		chans = append(chans, int((*audioBuffer)(unsafe.Pointer(&b[at])).channels))
	}
	return chans, nil
}

// firstStreamLatency returns the latency of the device's first stream
// of scope, and false where the device reports none.
func firstStreamLatency(dev, scope uint32) (int, bool) {
	b, err := getBytes(dev, propStreams, scope)
	if err != nil || len(b) < 4 {
		return 0, false
	}
	var lat uint32
	if !get(*(*uint32)(unsafe.Pointer(&b[0])), propLatency, scopeGlobal, &lat) {
		return 0, false
	}
	return int(lat), true
}

// openMu guards the inputs opening and closing.
var openMu sync.Mutex

// rateSet are the rates the process last set the devices to. openMu
// guards it.
var rateSet = map[uint32]int{}

// setRate sets device dev, called name, to rate, where it is not at it
// already, and waits a moment for it to change, as the device changes in
// its own time. It returns why the device stays at another rate.
func setRate(dev uint32, rate int, name string) error {
	var now float64
	if rate <= 0 {
		return nil
	}
	if !get(dev, propNominalSampleRate, scopeGlobal, &now) {
		return fmt.Errorf("coreaudio: %s reports no rate", name)
	}
	if int(math.Round(now)) == rate {
		return nil
	}
	if st := set(dev, propNominalSampleRate, scopeGlobal, float64(rate)); st != noErr {
		return fmt.Errorf("coreaudio: %s refuses %d Hz: %d", name, rate, st)
	}
	rateSet[dev] = rate
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if get(dev, propNominalSampleRate, scopeGlobal, &now) && int(math.Round(now)) == rate {
			return nil
		}
	}
	return fmt.Errorf("coreaudio: %s stays at %v Hz in place of %d Hz", name, now, rate)
}
