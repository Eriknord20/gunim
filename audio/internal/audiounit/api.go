//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64)

package audiounit

import (
	"strconv"
	"sync"
	"unsafe"
)

// Core Audio's own numbers.
const (
	noErr = 0
	// badObject is kAudioHardwareBadObjectError, '!obj': the object
	// named is gone, as a device unplugged is.
	badObject = 0x216f626a

	systemObject = 1

	scopeGlobal   = 0x676c6f62 // 'glob'
	scopeInput    = 0x696e7074 // 'inpt'
	scopeOutput   = 0x6f757470 // 'outp'
	scopeWildcard = 0x2a2a2a2a // '****'
	elementMain   = 0

	propDefaultOutputDevice = 0x644f7574 // 'dOut'
	propDefaultInputDevice  = 0x64496e20 // 'dIn '
	propName                = 0x6c6e616d // 'lnam'
	propElementName         = 0x6c63686e // 'lchn'
	propDeviceIsAlive       = 0x6c69766e // 'livn'
	propBufferFrameSize     = 0x6673697a // 'fsiz'
	propBufferFrameRange    = 0x66737a23 // 'fsz#'
	propNominalSampleRate   = 0x6e737274 // 'nsrt'
	propLatency             = 0x6c746e63 // 'ltnc', of a device and of a stream
	propSafetyOffset        = 0x73616674 // 'saft'
	propAvailableRates      = 0x6e737223 // 'nsr#'
	propStreams             = 0x73746d23 // 'stm#'
	propStreamConfiguration = 0x736c6179 // 'slay'

	// The AudioUnit's scopes and elements: element 0 is the output,
	// element 1 the input.
	unitGlobal = 0
	unitInput  = 1
	unitOutput = 2
	busOut     = 0
	busIn      = 1

	unitStreamFormat     = 8
	unitMaxFrames        = 14
	unitRenderCallback   = 23
	unitCurrentDevice    = 2000
	unitChannelMap       = 2002
	unitEnableIO         = 2003
	unitInputCallback    = 2005
	formatLinearPCM      = 0x6c70636d // 'lpcm'
	formatFloat          = 1 << 0
	formatPacked         = 1 << 3
	formatNonInterleaved = 1 << 5
)

// address is an AudioObjectPropertyAddress.
type address struct{ selector, scope, element uint32 }

// streamDescription is an AudioStreamBasicDescription.
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

// planar returns the format of float32 samples, one channel a buffer,
// that the client plays and records.
func planar(rate, chans int) streamDescription {
	return streamDescription{sampleRate: float64(rate), formatID: formatLinearPCM,
		formatFlags: formatFloat | formatPacked | formatNonInterleaved, bytesPerPacket: 4, framesPerPacket: 1,
		bytesPerFrame: 4, channelsPerFrame: uint32(chans), bitsPerChannel: 32}
}

// callbackStruct is an AURenderCallbackStruct.
type callbackStruct struct{ proc, refCon uintptr }

// valueRange is an AudioValueRange.
type valueRange struct{ min, max float64 }

// audioBuffer is an AudioBuffer: an AudioBufferList holds a count of
// them, then the buffers, from the next 8-byte boundary.
type audioBuffer struct {
	channels uint32
	bytes    uint32
	data     unsafe.Pointer
}

const bufferListHeader = 8

// api is what the client asks of Core Audio, as Go functions: the
// system's, or a test's stand-in. An AudioUnit is its handle.
type api struct {
	// get, size and set read, size and write a property of an
	// AudioObject; str reads one that is a CFString.
	get  func(obj uint32, a address, size *uint32, out unsafe.Pointer) int32
	size func(obj uint32, a address, size *uint32) int32
	set  func(obj uint32, a address, size uint32, in unsafe.Pointer) int32
	str  func(obj uint32, a address) (string, bool)
	// listen and unlisten add and remove a listener of a property.
	listen, unlisten func(obj uint32, a address, proc, client uintptr) int32
	// newUnit makes a HAL output unit.
	newUnit func() (uintptr, int32)
	// setUnit and getUnit write and read a property of a unit.
	setUnit func(u uintptr, prop, scope, elem uint32, in unsafe.Pointer, size uint32) int32
	getUnit func(u uintptr, prop, scope, elem uint32, out unsafe.Pointer, size *uint32) int32
	// The unit's life: made ready, started, stopped, unready, let go.
	initialize, start, stop, uninitialize, dispose func(u uintptr) int32
	// render is AudioUnitRender's address, which the input callback
	// calls.
	render uintptr
	// numer and denom turn host time into nanoseconds: ticks times
	// numer over denom.
	numer, denom uint64
	// now returns the host time now, in nanoseconds.
	now func() int64
	// masked runs f on a thread of its own with every signal blocked,
	// so the threads Core Audio starts as f calls it inherit none.
	masked func(f func())
	// mayRecord returns why the application may not record, or nil.
	mayRecord func() error
}

var (
	libMu sync.Mutex
	// testAPI, where set, stands in for Core Audio.
	testAPI *api
)

// getAPI returns Core Audio's functions, or a test's.
func getAPI() (*api, error) {
	libMu.Lock()
	defer libMu.Unlock()
	if testAPI != nil {
		return testAPI, nil
	}
	return systemAPI()
}

// hostNs returns host time t in nanoseconds.
func (a *api) hostNs(t uint64) int64 { return int64(t * a.numer / a.denom) }

// get32 reads a property of obj that is a UInt32, and false where it
// has none.
func get32(a *api, obj, selector, scope uint32) (uint32, bool) {
	var v uint32
	size := uint32(4)
	ok := a.get(obj, address{selector, scope, elementMain}, &size, unsafe.Pointer(&v)) == noErr
	return v, ok
}

// rateOf reads device dev's nominal rate.
func rateOf(a *api, dev uint32) (float64, bool) {
	var v float64
	size := uint32(8)
	ok := a.get(dev, address{propNominalSampleRate, scopeGlobal, elementMain}, &size, unsafe.Pointer(&v)) == noErr
	return v, ok
}

// getBytes reads a property of variable size, nil where it is empty or
// missing.
func getBytes(a *api, obj uint32, addr address) []byte {
	var size uint32
	if a.size(obj, addr, &size) != noErr || size == 0 {
		return nil
	}
	// Of uint64s, for the pointers the property may hold.
	buf := make([]uint64, (size+7)/8)
	if a.get(obj, addr, &size, unsafe.Pointer(&buf[0])) != noErr {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), size)
}

// channels returns how many channels the device's buffers of scope hold
// in all, as its stream configuration says.
func channels(a *api, dev, scope uint32) int {
	b := getBytes(a, dev, address{propStreamConfiguration, scope, elementMain})
	if len(b) < bufferListHeader {
		return 0
	}
	n := int(*(*uint32)(unsafe.Pointer(&b[0])))
	size := int(unsafe.Sizeof(audioBuffer{}))
	total := 0
	for i := range n {
		at := bufferListHeader + i*size
		if at+size > len(b) {
			break
		}
		total += int((*audioBuffer)(unsafe.Pointer(&b[at])).channels)
	}
	return total
}

// streamLatency returns the latency of the device's first stream of
// scope, 0 where it reports none.
func streamLatency(a *api, dev, scope uint32) int {
	b := getBytes(a, dev, address{propStreams, scope, elementMain})
	if len(b) < 4 {
		return 0
	}
	lat, _ := get32(a, *(*uint32)(unsafe.Pointer(&b[0])), propLatency, scopeGlobal)
	return int(lat)
}

// channelNames returns the names of the device's n channels of scope,
// as it names them, or its name and the channel's number.
func channelNames(a *api, dev, scope uint32, device string, n int) []string {
	names := make([]string, n)
	for i := range n {
		if s, ok := a.str(dev, address{propElementName, scope, uint32(i + 1)}); ok && s != "" {
			names[i] = s
			continue
		}
		names[i] = device + " " + strconv.Itoa(i+1)
	}
	return names
}
