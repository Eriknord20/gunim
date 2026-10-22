package speaker

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows, Go's clock moves in ticks of 0.5 to 15.6 ms, too coarse
// to time a buffer of a millisecond; the performance counter moves in
// well under a microsecond.
var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	// qpcAddr is the counter's function, and qpcFreq its ticks a
	// second, found once; 0 where it is missing.
	qpcAddr, qpcFreq = findCounter()
)

func findCounter() (uintptr, int64) {
	qpc, qpf := kernel32.NewProc("QueryPerformanceCounter"), kernel32.NewProc("QueryPerformanceFrequency")
	if qpc.Find() != nil || qpf.Find() != nil {
		return 0, 0
	}
	var f int64
	if r, _, _ := syscall.SyscallN(qpf.Addr(), uintptr(unsafe.Pointer(&f))); r == 0 || f <= 0 {
		return 0, 0
	}
	return qpc.Addr(), f
}

// nanotime is a clock in nanoseconds, from a moment of its own. It
// allocates nothing.
func nanotime() int64 {
	if qpcAddr == 0 {
		return goNanotime()
	}
	var c int64
	_, _, _ = syscall.SyscallN(qpcAddr, uintptr(unsafe.Pointer(&c)))
	return c/qpcFreq*1e9 + c%qpcFreq*1e9/qpcFreq
}
