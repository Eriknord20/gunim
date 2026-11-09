//go:build darwin && !ios && (amd64 || arm64)

package audiounit

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/marrasen/gunim/audio/internal/micaccess"
)

// The HAL output unit, as AudioComponentFindNext finds it.
const (
	typeOutput      = 0x61756f75 // 'auou'
	subtypeHAL      = 0x6168616c // 'ahal'
	manufacturerApp = 0x6170706c // 'appl'
)

// componentDescription is an AudioComponentDescription.
type componentDescription struct {
	componentType, subtype, manufacturer uint32
	// The flags and their mask, which the search leaves at 0.
	_ [2]uint32
}

// sigSetmask is pthread_sigmask's SIG_SETMASK on macOS.
const sigSetmask = 3

var (
	sysOnce sync.Once
	sys     *api
	sysErr  error
)

// systemAPI loads Core Audio, AudioToolbox and the rest, once.
func systemAPI() (*api, error) {
	sysOnce.Do(func() { sys, sysErr = loadSystem() })
	return sys, sysErr
}

func loadSystem() (a *api, err error) {
	defer func() {
		// RegisterLibFunc panics on a symbol a library lacks.
		if r := recover(); r != nil {
			a, err = nil, fmt.Errorf("audiounit: %v", r)
		}
	}()
	open := func(path string) (uintptr, error) {
		h, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return 0, fmt.Errorf("audiounit: loading %s: %w", path, err)
		}
		return h, nil
	}
	ca, err := open("/System/Library/Frameworks/CoreAudio.framework/CoreAudio")
	if err != nil {
		return nil, err
	}
	at, err := open("/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox")
	if err != nil {
		return nil, err
	}
	cf, err := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	if err != nil {
		return nil, err
	}
	libc, err := open("/usr/lib/libSystem.B.dylib")
	if err != nil {
		return nil, err
	}

	var (
		getData     func(obj uint32, a *address, qualSize uint32, qual unsafe.Pointer, size *uint32, out unsafe.Pointer) int32
		getSize     func(obj uint32, a *address, qualSize uint32, qual unsafe.Pointer, size *uint32) int32
		setData     func(obj uint32, a *address, qualSize uint32, qual unsafe.Pointer, size uint32, in unsafe.Pointer) int32
		addListener func(obj uint32, a *address, proc, client uintptr) int32
		rmListener  func(obj uint32, a *address, proc, client uintptr) int32
		findNext    func(after uintptr, desc *componentDescription) uintptr
		instanceNew func(c uintptr, out *uintptr) int32
		cfGetCStr   func(s uintptr, buf *byte, size int, encoding uint32) bool
		cfRelease   func(ref uintptr)
		absTime     func() uint64
		timebase    func(tb *[2]uint32) int32
		sigmask     func(how int32, set, old *uint32) int32
	)
	purego.RegisterLibFunc(&getData, ca, "AudioObjectGetPropertyData")
	purego.RegisterLibFunc(&getSize, ca, "AudioObjectGetPropertyDataSize")
	purego.RegisterLibFunc(&setData, ca, "AudioObjectSetPropertyData")
	purego.RegisterLibFunc(&addListener, ca, "AudioObjectAddPropertyListener")
	purego.RegisterLibFunc(&rmListener, ca, "AudioObjectRemovePropertyListener")
	purego.RegisterLibFunc(&findNext, at, "AudioComponentFindNext")
	purego.RegisterLibFunc(&instanceNew, at, "AudioComponentInstanceNew")
	purego.RegisterLibFunc(&cfGetCStr, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&absTime, libc, "mach_absolute_time")
	purego.RegisterLibFunc(&timebase, libc, "mach_timebase_info")
	purego.RegisterLibFunc(&sigmask, libc, "pthread_sigmask")

	a = &api{}
	a.get = func(obj uint32, addr address, size *uint32, out unsafe.Pointer) int32 {
		return getData(obj, &addr, 0, nil, size, out)
	}
	a.size = func(obj uint32, addr address, size *uint32) int32 { return getSize(obj, &addr, 0, nil, size) }
	a.set = func(obj uint32, addr address, size uint32, in unsafe.Pointer) int32 {
		return setData(obj, &addr, 0, nil, size, in)
	}
	a.listen = func(obj uint32, addr address, proc, client uintptr) int32 {
		return addListener(obj, &addr, proc, client)
	}
	a.unlisten = func(obj uint32, addr address, proc, client uintptr) int32 {
		return rmListener(obj, &addr, proc, client)
	}
	a.str = func(obj uint32, addr address) (string, bool) {
		var ref uintptr
		size := uint32(unsafe.Sizeof(ref))
		if getData(obj, &addr, 0, nil, &size, unsafe.Pointer(&ref)) != noErr || ref == 0 {
			return "", false
		}
		defer cfRelease(ref)
		buf := make([]byte, 256)
		if !cfGetCStr(ref, &buf[0], len(buf), 0x08000100) { // kCFStringEncodingUTF8
			return "", false
		}
		for i, b := range buf {
			if b == 0 {
				return string(buf[:i]), true
			}
		}
		return string(buf), true
	}
	desc := componentDescription{componentType: typeOutput, subtype: subtypeHAL, manufacturer: manufacturerApp}
	a.newUnit = func() (uintptr, int32) {
		c := findNext(0, &desc)
		if c == 0 {
			return 0, -1
		}
		var u uintptr
		st := instanceNew(c, &u)
		return u, st
	}
	purego.RegisterLibFunc(&a.setUnit, at, "AudioUnitSetProperty")
	purego.RegisterLibFunc(&a.getUnit, at, "AudioUnitGetProperty")
	purego.RegisterLibFunc(&a.initialize, at, "AudioUnitInitialize")
	purego.RegisterLibFunc(&a.uninitialize, at, "AudioUnitUninitialize")
	purego.RegisterLibFunc(&a.start, at, "AudioOutputUnitStart")
	purego.RegisterLibFunc(&a.stop, at, "AudioOutputUnitStop")
	purego.RegisterLibFunc(&a.dispose, at, "AudioComponentInstanceDispose")
	a.render, err = purego.Dlsym(at, "AudioUnitRender")
	if err != nil {
		return nil, fmt.Errorf("audiounit: AudioToolbox has no AudioUnitRender: %w", err)
	}

	var tb [2]uint32
	if timebase(&tb) != 0 || tb[0] == 0 || tb[1] == 0 {
		return nil, errors.New("audiounit: mach_timebase_info failed")
	}
	a.numer, a.denom = uint64(tb[0]), uint64(tb[1])
	// Go's monotonic clock on macOS is mach_absolute_time, in
	// nanoseconds: the host time is that clock and a fixed offset, read
	// once, so reading it costs no call into the system.
	base := a.hostNs(absTime())
	since := time.Now()
	a.now = func() int64 { return base + int64(time.Since(since)) }

	a.masked = func(f func()) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			all, old := ^uint32(0), uint32(0)
			sigmask(sigSetmask, &all, &old)
			defer sigmask(sigSetmask, &old, nil)
			f()
		}()
		<-done
	}
	a.mayRecord = func() error {
		denied, err := micaccess.Check()
		if err != nil {
			return fmt.Errorf("audiounit: %w", err)
		}
		if denied {
			return ErrDenied
		}
		return nil
	}
	return a, nil
}

// Available reports whether Core Audio loads, as it does on every Mac.
func Available() bool {
	_, err := systemAPI()
	return err == nil
}

// ControlPanel opens Audio MIDI Setup, where the devices' rates and
// formats are set.
func ControlPanel() error {
	return exec.CommandContext(context.Background(), "open", "-b", "com.apple.audio.AudioMIDISetup").Run()
}
