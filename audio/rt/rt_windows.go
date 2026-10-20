package rt

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Multimedia Class Scheduler Service gives a thread that plays
// audio its share of the processor before the rest: "Pro Audio" is the
// task a DAW's threads take. avrt.dll is loaded the first time a
// thread enters; where it is missing, threads run as they are.
var (
	avrt          = windows.NewLazySystemDLL("avrt.dll")
	avSetTask     = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	avSetPriority = avrt.NewProc("AvSetMmThreadPriority")
	avRevert      = avrt.NewProc("AvRevertMmThreadCharacteristics")
	proAudio, _   = windows.UTF16PtrFromString("Pro Audio")
)

// avrtPriorityHigh is AVRT_PRIORITY_HIGH.
const avrtPriorityHigh = 1

// raise has the calling thread join the Pro Audio task, at high
// priority, and returns what has it leave.
func raise() func() {
	if avSetTask.Find() != nil {
		return func() {}
	}
	var index uint32
	h, _, _ := avSetTask.Call(uintptr(unsafe.Pointer(proAudio)), uintptr(unsafe.Pointer(&index)))
	if h == 0 {
		return func() {}
	}
	if avSetPriority.Find() == nil {
		_, _, _ = avSetPriority.Call(h, avrtPriorityHigh)
	}
	return func() {
		if avRevert.Find() == nil {
			_, _, _ = avRevert.Call(h)
		}
	}
}
