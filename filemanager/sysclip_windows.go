//go:build windows && (amd64 || arm64)

package filemanager

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procOleInitialize              = ole32.NewProc("OleInitialize")
	procOleUninitialize            = ole32.NewProc("OleUninitialize")
	procOleGetClipboard            = ole32.NewProc("OleGetClipboard")
	procReleaseStgMedium           = ole32.NewProc("ReleaseStgMedium")
)

// The clipboard's formats and media, and the methods of IDataObject and
// IStream called, by their place in each table.
const (
	cfHDrop         = 15
	dvAspectContent = 1
	tymedHGlobal    = 1
	tymedIStream    = 4
	dropEffectCopy  = 1
	dropEffectMove  = 2

	vtGetData    = 3
	vtStreamRead = 3
)

// The formats Windows names rather than numbers: the list of files held
// as contents, each file's contents, and whether what is held was cut.
var clipFormats = sync.OnceValue(func() (f struct{ descriptor, contents, effect uintptr }) {
	register := func(name string) uintptr {
		r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(name))))
		return r
	}
	f.descriptor = register("FileGroupDescriptorW")
	f.contents = register("FileContents")
	f.effect = register("Preferred DropEffect")
	return f
})

// formatEtc is FORMATETC, and stgMedium STGMEDIUM.
type formatEtc struct {
	cf     uint16
	ptd    uintptr
	aspect uint32
	index  int32
	tymed  uint32
}

type stgMedium struct {
	tymed  uint32
	handle uintptr
	unk    uintptr
}

// osClip is the clipboard of Windows.
type osClip struct{}

func (osClip) seq() uint32 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(r)
}

func (osClip) hasFiles() bool {
	for _, cf := range []uintptr{cfHDrop, clipFormats().descriptor} {
		if r, _, _ := procIsClipboardFormatAvailable.Call(cf); r != 0 {
			return true
		}
	}
	return false
}

// openClipboard opens the clipboard on the calling thread, which must stay
// locked to it until closeClipboard. Another program may hold it for a
// moment, so it tries for a while.
func openClipboard() error {
	var err error
	for range 20 {
		r, _, e := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		err = e
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("another program holds the clipboard: %w", err)
}

func closeClipboard() { _, _, _ = procCloseClipboard.Call() }

// globalBytes copies what the global memory h holds.
func globalBytes(h uintptr) []byte {
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil
	}
	defer func() { _, _, _ = procGlobalUnlock.Call(h) }()
	n, _, _ := procGlobalSize.Call(h)
	return bytes.Clone(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n))
}

func (c osClip) files() (systemFiles, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	sf := systemFiles{seq: c.seq()}
	if err := openClipboard(); err != nil {
		return sf, err
	}
	defer closeClipboard()
	if h, _, _ := procGetClipboardData.Call(cfHDrop); h != 0 {
		count, _, _ := procDragQueryFileW.Call(h, 0xffffffff, 0, 0)
		for i := range count {
			n, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
			buf := make([]uint16, n+1)
			_, _, _ = procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			sf.paths = append(sf.paths, windows.UTF16ToString(buf))
		}
		if h, _, _ := procGetClipboardData.Call(clipFormats().effect); h != 0 {
			if b := globalBytes(h); len(b) >= 4 {
				effect := binary.LittleEndian.Uint32(b)
				sf.cut = effect&dropEffectMove != 0 && effect&dropEffectCopy == 0
			}
		}
		return sf, nil
	}
	h, _, _ := procGetClipboardData.Call(clipFormats().descriptor)
	if h == 0 {
		return sf, nil
	}
	items, err := fileDescriptors(globalBytes(h))
	if err != nil {
		return sf, err
	}
	sf.names = topNames(items)
	return sf, nil
}

func (c osClip) empty(seq uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if c.seq() != seq || openClipboard() != nil {
		return
	}
	defer closeClipboard()
	_, _, _ = procEmptyClipboard.Call()
}

// outside reads the clipboard through OLE, which alone hands over each
// file held as contents, by its index. The data object it gives works
// only on the thread that took it, so fn runs on that thread.
func (osClip) outside(fn func(outsideFiles) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procOleInitialize.Call(0); failed(uint32(hr)) {
		return fmt.Errorf("starting OLE: HRESULT %#x", uint32(hr))
	}
	defer func() { _, _, _ = procOleUninitialize.Call() }()
	var obj unsafe.Pointer
	if hr, _, _ := procOleGetClipboard.Call(uintptr(unsafe.Pointer(&obj))); failed(uint32(hr)) || obj == nil {
		return fmt.Errorf("reading the clipboard: HRESULT %#x", uint32(hr))
	}
	defer release(obj)
	m, err := getData(obj, clipFormats().descriptor, -1, tymedHGlobal)
	if err != nil {
		return fmt.Errorf("reading the list of files on the clipboard: %w", err)
	}
	b := globalBytes(m.handle)
	releaseMedium(&m)
	items, err := fileDescriptors(b)
	if err != nil {
		return err
	}
	return fn(&oleFiles{obj: obj, list: items})
}

// getData asks the data object obj for format cf, the item of index i in
// it or -1 for the whole, in one of the media tymed allows.
func getData(obj unsafe.Pointer, cf uintptr, i int32, tymed uint32) (stgMedium, error) {
	f := formatEtc{cf: uint16(cf), aspect: dvAspectContent, index: i, tymed: tymed}
	var m stgMedium
	if hr := comCall(obj, vtGetData, uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&m))); failed(hr) {
		return stgMedium{}, fmt.Errorf("HRESULT %#x", hr)
	}
	if m.tymed&tymed == 0 || m.handle == 0 {
		releaseMedium(&m)
		return stgMedium{}, errors.New("the program that put it there gave it in a form that cannot be read")
	}
	return m, nil
}

func releaseMedium(m *stgMedium) { _, _, _ = procReleaseStgMedium.Call(uintptr(unsafe.Pointer(m))) }

// oleFiles is the files the clipboard's data object obj holds as
// contents, described by list.
type oleFiles struct {
	obj  unsafe.Pointer
	list []outsideItem
}

func (o *oleFiles) items() []outsideItem { return o.list }

func (o *oleFiles) open(i int) (io.ReadCloser, error) {
	m, err := getData(o.obj, clipFormats().contents, int32(i), tymedIStream|tymedHGlobal)
	if err != nil {
		return nil, err
	}
	if m.tymed == tymedIStream {
		// The medium's handle is the stream itself.
		return &oleStream{m: m, stream: *(*unsafe.Pointer)(unsafe.Pointer(&m.handle))}, nil
	}
	b := globalBytes(m.handle)
	releaseMedium(&m)
	if size := o.list[i].size; size >= 0 && size < int64(len(b)) {
		// Global memory may run past what it holds.
		b = b[:size]
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// oleStream reads a file's contents from the stream a program handed
// over, which it may fetch as it is read, as Remote Desktop does.
type oleStream struct {
	m      stgMedium
	stream unsafe.Pointer
}

// Read implements [io.Reader].
func (s *oleStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var n uint32
	hr := comCall(s.stream, vtStreamRead, uintptr(unsafe.Pointer(&p[0])),
		uintptr(min(len(p), 1<<30)), uintptr(unsafe.Pointer(&n)))
	if failed(hr) {
		return int(n), fmt.Errorf("HRESULT %#x", hr)
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

// Close implements [io.Closer]: it lets the stream go.
func (s *oleStream) Close() error {
	releaseMedium(&s.m)
	return nil
}
