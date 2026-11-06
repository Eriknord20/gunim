package vst3

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A plugin may catch its own faults: Reason Rack Plugin keeps a Rack Extension
// that reads a bad address from taking its host down, by a vectored exception
// handler that moves the thread to a recovery point and answers
// EXCEPTION_CONTINUE_EXECUTION. Windows then runs the vectored continue
// handlers before the thread goes on, and the Go runtime's last one takes any
// exception that reaches it for one no one can handle, and ends the process.
//
// continueGuard goes before it in that list. Its answer of
// EXCEPTION_CONTINUE_EXECUTION ends the walk, so it gives that answer only where
// all of this holds:
//
//   - the context's Rip differs from the exception's address, so someone moved
//     the thread: where nothing did, as in a plugin's fault no one caught,
//     going on would only fault again, and Go is to report the crash;
//   - the fault was outside this program's image: a fault in Go is Go's;
//   - the thread resumes outside it: Go moves a thread into its own sigpanic in
//     just this way, to turn a nil dereference into a panic.
//
// All else goes on to Go, as it would with no guard.

// continueGuard is the handler, in guard_windows_amd64.s; continueGuardPC is its
// address, as Go's own func value for it is a wrapper Windows can't call.
//
//nolint:unused // only the assembly names it
func continueGuard()
func continueGuardPC() uintptr

var (
	// imageLo and imageHi bound this program's image. continueGuard reads them.
	imageLo, imageHi uintptr //nolint:unused // only the assembly reads them
	// recoveredAt is where the last fault a plugin recovered from was, and
	// recoveredN how many there have been. continueGuard writes them.
	recoveredAt uintptr
	recoveredN  uint64

	guardOnce sync.Once
	guardErr  error
)

// installGuard puts continueGuard first among the continue handlers, once. It
// runs before any plugin loads.
func installGuard() error {
	guardOnce.Do(func() {
		var self windows.Handle
		if err := windows.GetModuleHandleEx(0, nil, &self); err != nil {
			guardErr = err
			return
		}
		var info windows.ModuleInfo
		if err := windows.GetModuleInformation(windows.CurrentProcess(), self, &info, uint32(unsafe.Sizeof(info))); err != nil {
			guardErr = err
			return
		}
		imageLo, imageHi = info.BaseOfDll, info.BaseOfDll+uintptr(info.SizeOfImage)
		add := windows.NewLazySystemDLL("kernel32.dll").NewProc("AddVectoredContinueHandler")
		if r, _, err := add.Call(1, continueGuardPC()); r == 0 {
			guardErr = err
			return
		}
		guardErr = makeWrapper()
	})
	return guardErr
}

// Recovered reports how many faults plugins have recovered from since the
// program started, and where the last one was: the file of the module it was
// in, where it is still loaded, and its offset there. A plugin that recovers
// has usually stopped part of its sound, so a host may want to say so. It is
// safe to call from any goroutine.
func Recovered() (n uint64, module string, offset uintptr) {
	n = atomic.LoadUint64(&recoveredN)
	if n == 0 {
		return 0, "", 0
	}
	at := atomic.LoadUintptr(&recoveredAt)
	var h windows.Handle
	const flags = windows.GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT
	get := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleExW")
	if r, _, _ := get.Call(flags, at, uintptr(unsafe.Pointer(&h))); r == 0 {
		return n, "", at
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	l, err := windows.GetModuleFileName(h, &buf[0], uint32(len(buf)))
	if err != nil {
		return n, "", at
	}
	return n, windows.UTF16ToString(buf[:l]), at - uintptr(h)
}
