//go:build darwin && !ios && (amd64 || arm64)

package audiounit

import (
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Every function the client calls loads from the system's frameworks,
// on any Mac, with a sound device or none; the host clock reads as
// mach_absolute_time does, in nanoseconds; and the threads masked run
// their work.
func TestSystemLoads(t *testing.T) {
	a, err := systemAPI()
	if err != nil {
		t.Fatal(err)
	}
	if a.render == 0 || a.numer == 0 || a.denom == 0 {
		t.Fatalf("render %#x, timebase %d/%d", a.render, a.numer, a.denom)
	}
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := purego.Dlsym(libc, "mach_absolute_time")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		ticks, _, _ := purego.SyscallN(abs)
		if d := a.now() - a.hostNs(uint64(ticks)); d < -time.Millisecond.Nanoseconds() || d > time.Millisecond.Nanoseconds() {
			t.Fatalf("the host clock is %v off mach_absolute_time", time.Duration(d))
		}
		time.Sleep(20 * time.Millisecond)
	}
	ran := false
	a.masked(func() { ran = true })
	if !ran {
		t.Fatal("masked ran nothing")
	}
	inputs, outputs, err := Ports()
	t.Logf("inputs %v, outputs %v, %v", inputs, outputs, err)
	var dev uint32
	size := uint32(unsafe.Sizeof(dev))
	if a.get(systemObject, address{propDefaultOutputDevice, scopeGlobal, 0}, &size, unsafe.Pointer(&dev)) == noErr && dev != 0 {
		u, st := a.newUnit()
		if st != noErr || u == 0 {
			t.Fatalf("the HAL output unit: %d", st)
		}
		if st := a.dispose(u); st != noErr {
			t.Fatalf("AudioComponentInstanceDispose: %d", st)
		}
	}
}
