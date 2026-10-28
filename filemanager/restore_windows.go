//go:build windows && (amd64 || arm64)

package filemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                           = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	procSHGetKnownFolderItem        = shell32.NewProc("SHGetKnownFolderItem")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
)

// The shell's IDs the restore uses.
var (
	folderRecycleBin = windows.GUID{Data1: 0xB7534046, Data2: 0x3ECB, Data3: 0x4C18,
		Data4: [8]byte{0xBE, 0x4E, 0x64, 0xCD, 0x4C, 0xB7, 0xD6, 0xAC}}
	iidShellItem = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE,
		Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
	iidShellItem2 = windows.GUID{Data1: 0x7E9FB0D3, Data2: 0x919F, Data3: 0x4307,
		Data4: [8]byte{0xAB, 0x2E, 0x9B, 0x18, 0x60, 0x31, 0x0C, 0x93}}
	iidEnumShellItems = windows.GUID{Data1: 0x70629033, Data2: 0xE363, Data3: 0x4A28,
		Data4: [8]byte{0xA5, 0x67, 0x0D, 0xB7, 0x80, 0x06, 0xE6, 0xD7}}
	bhidEnumItems = windows.GUID{Data1: 0x94F60519, Data2: 0x2850, Data3: 0x4924,
		Data4: [8]byte{0xAA, 0x5A, 0xD1, 0x5E, 0x84, 0x86, 0x80, 0x39}}
	clsidFileOperation = windows.GUID{Data1: 0x3AD05575, Data2: 0x8857, Data3: 0x4850,
		Data4: [8]byte{0x92, 0x77, 0x11, 0xB8, 0x5B, 0xDB, 0x8E, 0x09}}
	iidFileOperation = windows.GUID{Data1: 0x947AAB5F, Data2: 0x0A5C, Data3: 0x4C13,
		Data4: [8]byte{0xB4, 0xD6, 0x4B, 0xF7, 0x83, 0x6F, 0xC9, 0xF8}}
	// displaced holds the properties of an item in the Recycle Bin: where
	// it came from, and when it went.
	displaced = windows.GUID{Data1: 0x9B174B33, Data2: 0x40FF, Data3: 0x11D2,
		Data4: [8]byte{0xA2, 0x7E, 0x00, 0xC0, 0x4F, 0xC3, 0x08, 0x71}}
	keyOriginalLocation = propertyKey{fmtid: displaced, pid: 2}
	keyDateDeleted      = propertyKey{fmtid: displaced, pid: 3}
)

// propertyKey is PROPERTYKEY.
type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// COM constants, and the methods called by their place in each table.
const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	rpcEChangedMode         = 0x80010106
	clsctxInprocServer      = 0x1
	sigdnNormalDisplay      = 0
	sigdnFileSysPath        = 0x80058000
	fofNoConfirmMkdir       = 0x200

	vtQueryInterface      = 0
	vtRelease             = 2
	vtBindToHandler       = 3
	vtGetDisplayName      = 5
	vtGetFileTime         = 15
	vtGetString           = 17
	vtEnumNext            = 3
	vtSetOperationFlags   = 5
	vtMoveItem            = 14
	vtPerformOperations   = 21
	vtAnyOperationAborted = 22
)

// comCall calls method index of the COM object obj.
func comCall(obj unsafe.Pointer, index int, args ...uintptr) uint32 {
	table := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(table, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return uint32(r)
}

func release(obj unsafe.Pointer) { comCall(obj, vtRelease) }

func failed(hr uint32) bool { return int32(hr) < 0 }

// onCOM runs fn on a thread with COM started.
func onCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	switch {
	case uint32(hr) == rpcEChangedMode:
	case failed(uint32(hr)):
		return fmt.Errorf("starting COM: HRESULT %#x", uint32(hr))
	default:
		defer func() { _, _, _ = procCoUninitialize.Call() }()
	}
	return fn()
}

// Restore implements [Trasher]: it finds the item that came from original
// closest to time at in the Recycle Bin, and moves it to the path to with
// the shell, which clears its record.
func (recycleBin) Restore(original, _ string, at time.Time, to string) error {
	return restoreTo(to, func() (unsafe.Pointer, error) { return findRecycled(original, at) })
}

// restoreTo moves the item find returns out of the Recycle Bin to the
// path to, where nothing may be.
func restoreTo(to string, find func() (unsafe.Pointer, error)) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s exists again; move it away to restore the one in the Recycle Bin", to)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	err := onCOM(func() error {
		item, err := find()
		if err != nil {
			return err
		}
		defer release(item)
		return shellMove(item, to)
	})
	if err != nil {
		return err
	}
	if _, err := os.Lstat(to); err != nil {
		return fmt.Errorf("the Recycle Bin did not put %s back: %w", to, err)
	}
	return nil
}

// Describe implements [Trasher].
func (recycleBin) Describe(string) string { return "in the Recycle Bin" }

// recycled is an item in the Recycle Bin, and what the bin says of it.
type recycled struct {
	item unsafe.Pointer
	binItem
	// kept is the file the bin keeps the item as, where the shell says.
	kept string
}

// findRecycled returns the IShellItem2 of the item in the Recycle Bin that
// came from original, deleted closest to at and no earlier than a moment
// before it. An item known to have original's extension comes before one
// that matches only by the name without it.
func findRecycled(original string, at time.Time) (unsafe.Pointer, error) {
	// found holds the items that match, to pick the best of.
	var found []recycled
	defer func() {
		for _, r := range found {
			if r.item != nil {
				release(r.item)
			}
		}
	}()
	err := eachRecycled(func(r recycled) bool {
		if rankRecycled(r.binItem, original, at) == 0 {
			release(r.item)
			return true
		}
		found = append(found, r)
		return true
	})
	if err != nil {
		return nil, err
	}
	items := make([]binItem, len(found))
	for i, r := range found {
		items[i] = r.binItem
	}
	best := pickRecycled(items, original, at)
	if best < 0 {
		return nil, fmt.Errorf("the Recycle Bin holds nothing from %s deleted at %s; it may have been restored or emptied",
			original, at.Format("15:04:05"))
	}
	// The caller releases the item it gets.
	item := found[best].item
	found[best].item = nil
	return item, nil
}

// eachRecycled hands fn each item in the Recycle Bin, as readRecycled
// reads it, until fn returns false. fn releases the item it is handed.
func eachRecycled(fn func(r recycled) bool) error {
	var bin unsafe.Pointer
	hr, _, _ := procSHGetKnownFolderItem.Call(uintptr(unsafe.Pointer(&folderRecycleBin)), 0, 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&bin)))
	if failed(uint32(hr)) {
		return fmt.Errorf("opening the Recycle Bin: HRESULT %#x", uint32(hr))
	}
	defer release(bin)
	var enum unsafe.Pointer
	if hr := comCall(bin, vtBindToHandler, 0, uintptr(unsafe.Pointer(&bhidEnumItems)),
		uintptr(unsafe.Pointer(&iidEnumShellItems)), uintptr(unsafe.Pointer(&enum))); failed(hr) {
		return fmt.Errorf("listing the Recycle Bin: HRESULT %#x", hr)
	}
	defer release(enum)
	for {
		var child unsafe.Pointer
		var got uint32
		hr := comCall(enum, vtEnumNext, 1, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&got)))
		if failed(hr) {
			return fmt.Errorf("listing the Recycle Bin: HRESULT %#x", hr)
		}
		if got == 0 {
			return nil
		}
		r, err := readRecycled(child)
		release(child)
		if err != nil {
			return err
		}
		if !fn(r) {
			return nil
		}
	}
}

// readRecycled reads where the Recycle Bin item child came from and when
// it went, and returns it as IShellItem2.
func readRecycled(child unsafe.Pointer) (recycled, error) {
	var item unsafe.Pointer
	if hr := comCall(child, vtQueryInterface, uintptr(unsafe.Pointer(&iidShellItem2)),
		uintptr(unsafe.Pointer(&item))); failed(hr) {
		return recycled{}, fmt.Errorf("reading an item in the Recycle Bin: HRESULT %#x", hr)
	}
	dir, err := shellString(item, &keyOriginalLocation)
	if err != nil {
		release(item)
		return recycled{}, err
	}
	name, err := shellName(item, sigdnNormalDisplay)
	if err != nil {
		release(item)
		return recycled{}, err
	}
	var ft windows.Filetime
	if hr := comCall(item, vtGetFileTime, uintptr(unsafe.Pointer(&keyDateDeleted)),
		uintptr(unsafe.Pointer(&ft))); failed(hr) {
		release(item)
		return recycled{}, fmt.Errorf("reading when %s went to the Recycle Bin: HRESULT %#x", name, hr)
	}
	// The Recycle Bin shows an item by its whole original path.
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, name)
	}
	r := recycled{item: item, binItem: binItem{path: path, when: time.Unix(0, ft.Nanoseconds())}}
	// The bin keeps the item as a file named $R and a few letters, with
	// the item's own extension. That tells a.txt from a.log where the name
	// shows without its extension.
	if kept, err := shellName(item, sigdnFileSysPath); err == nil && strings.HasPrefix(filepath.Base(kept), "$R") {
		r.kept = kept
		r.ext, r.extKnown = filepath.Ext(kept), true
	}
	return r, nil
}

// shellString reads the text of property key of item.
func shellString(item unsafe.Pointer, key *propertyKey) (string, error) {
	var p *uint16
	if hr := comCall(item, vtGetString, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&p))); failed(hr) {
		return "", fmt.Errorf("reading where an item in the Recycle Bin came from: HRESULT %#x", hr)
	}
	defer func() { _, _, _ = procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p))) }()
	return windows.UTF16PtrToString(p), nil
}

// shellName reads the name of item in the form sigdn.
func shellName(item unsafe.Pointer, sigdn uint32) (string, error) {
	var p *uint16
	if hr := comCall(item, vtGetDisplayName, uintptr(sigdn), uintptr(unsafe.Pointer(&p))); failed(hr) {
		return "", fmt.Errorf("reading the name of an item in the Recycle Bin: HRESULT %#x", hr)
	}
	defer func() { _, _, _ = procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p))) }()
	return windows.UTF16PtrToString(p), nil
}

// shellMove moves item to the path to with IFileOperation, without any
// window of its own.
func shellMove(item unsafe.Pointer, to string) error {
	dir, err := windows.UTF16PtrFromString(filepath.Dir(to))
	if err != nil {
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	name, err := windows.UTF16PtrFromString(filepath.Base(to))
	if err != nil {
		return fmt.Errorf("restoring %s: %w", to, err)
	}
	var dest unsafe.Pointer
	hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(dir)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&dest)))
	if failed(uint32(hr)) {
		return fmt.Errorf("finding the folder %s: HRESULT %#x", filepath.Dir(to), uint32(hr))
	}
	defer release(dest)
	return fileOp("restoring "+to, func(op unsafe.Pointer) uint32 {
		return comCall(op, vtMoveItem, uintptr(item), uintptr(dest), uintptr(unsafe.Pointer(name)), 0)
	})
}

// fileOp runs the operation add puts on an IFileOperation, without any
// window of its own; what names it in an error.
func fileOp(what string, add func(op unsafe.Pointer) uint32) error {
	var op unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&op)))
	if failed(uint32(hr)) {
		return fmt.Errorf("starting a file operation: HRESULT %#x", uint32(hr))
	}
	defer release(op)
	flags := uintptr(fofSilent | fofNoConfirmation | fofNoErrorUI | fofNoConfirmMkdir)
	if hr := comCall(op, vtSetOperationFlags, flags); failed(hr) {
		return fmt.Errorf("starting a file operation: HRESULT %#x", hr)
	}
	if hr := add(op); failed(hr) {
		return fmt.Errorf("%s: HRESULT %#x", what, hr)
	}
	if hr := comCall(op, vtPerformOperations); failed(hr) {
		return fmt.Errorf("%s failed with HRESULT %#x", what, hr)
	}
	var aborted int32
	if hr := comCall(op, vtAnyOperationAborted, uintptr(unsafe.Pointer(&aborted))); failed(hr) {
		return fmt.Errorf("%s: HRESULT %#x", what, hr)
	}
	if aborted != 0 {
		return fmt.Errorf("%s was stopped", what)
	}
	return nil
}
