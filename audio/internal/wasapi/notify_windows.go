package wasapi

import (
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// The system tells of devices changing through an IMMNotificationClient
// the process registers: an object of its own whose methods Windows
// calls on a thread of its own. The package has one, for every device
// it opens. Each method counts what it heard and returns at once; a
// device reads the counts on its own thread, and opens nothing from
// inside a call: Windows forbids it there.
type notifier struct {
	// vtbl is the object's table, as COM reads it: it must come first.
	vtbl *notifierVtbl
	// renderDefault and captureDefault count the changes of the
	// default output and input; states counts the devices that changed
	// state or went, and formats the formats set.
	renderDefault, captureDefault, states, formats atomic.Int64
}

type notifierVtbl struct {
	queryInterface, addRef, release                      uintptr
	onStateChanged, onAdded, onRemoved, onDefaultChanged uintptr
	onPropertyChanged                                    uintptr
}

var (
	notes     notifier
	notesVtbl notifierVtbl
	notesOnce sync.Once
)

// theNotifier returns the package's notification client, made once: a
// callback Go makes is never freed, and a process may make few.
func theNotifier() *notifier {
	notesOnce.Do(func() {
		notesVtbl = notifierVtbl{
			queryInterface:    syscall.NewCallback(noteQueryInterface),
			addRef:            syscall.NewCallback(noteAddRef),
			release:           syscall.NewCallback(noteAddRef),
			onStateChanged:    syscall.NewCallback(noteStateChanged),
			onAdded:           syscall.NewCallback(noteAdded),
			onRemoved:         syscall.NewCallback(noteRemoved),
			onDefaultChanged:  syscall.NewCallback(noteDefaultChanged),
			onPropertyChanged: syscall.NewCallback(notePropertyChanged),
		}
		notes.vtbl = &notesVtbl
	})
	return &notes
}

// addr is the object's address, as COM takes it.
func (n *notifier) addr() uintptr { return uintptr(unsafe.Pointer(n)) }

// counts is what a notifier has counted, as a device last read it.
type counts struct{ renderDefault, captureDefault, states, formats int64 }

func (n *notifier) read() counts {
	return counts{n.renderDefault.Load(), n.captureDefault.Load(), n.states.Load(), n.formats.Load()}
}

func noteQueryInterface(this, riid, ppv uintptr) uintptr {
	id := Ptr[[16]byte](riid)
	out := Ptr[uintptr](ppv)
	if *id == *(*[16]byte)(unsafe.Pointer(&iidIUnknown)) || *id == *(*[16]byte)(unsafe.Pointer(&iidIMMNotificationClnt)) {
		*out = this
		return sOK
	}
	*out = 0
	return eNoInterface
}

// noteAddRef counts nothing: the object lives as long as the process.
func noteAddRef(uintptr) uintptr { return 1 }

func noteStateChanged(_, _, _ uintptr) uintptr {
	notes.states.Add(1)
	return sOK
}

func noteAdded(_, _ uintptr) uintptr { return sOK }

func noteRemoved(_, _ uintptr) uintptr {
	notes.states.Add(1)
	return sOK
}

func noteDefaultChanged(_, flow, role, _ uintptr) uintptr {
	if uint32(role) != EConsole {
		return sOK
	}
	switch uint32(flow) {
	case ERender:
		notes.renderDefault.Add(1)
	case ECapture:
		notes.captureDefault.Add(1)
	}
	return sOK
}
