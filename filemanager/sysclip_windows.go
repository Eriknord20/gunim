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
	procSetLastError               = kernel32.NewProc("SetLastError")
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

	// A stream is asked for minPiece at first, for twice the last after a
	// read quicker than aimRead, and after a slower one for what it moved
	// in aimRead, from minPiece to maxPiece. Stop is heard between reads,
	// and Remote Desktop fetches what is asked as it is asked, so a large
	// piece on a slow link keeps a paste from stopping, and a small one on
	// a link with a long way to go spends it on asking. Pieces so grow on
	// any round trip under aimRead, and a read takes about twice aimRead
	// at most, or what minPiece takes on a link slower than that. A link
	// that slows all at once is learnt of only as a read ends: that one
	// read may hold Stop off, and the next is sized to the new pace.
	minPiece = 64 << 10
	maxPiece = 1 << 20
	aimRead  = time.Second
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

// globalBytes copies what the global memory h holds. Memory that cannot
// be locked, or whose size cannot be read, is an error: no bytes would
// read as an empty file.
func globalBytes(h uintptr) ([]byte, error) {
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("reading the memory the program handed over: %w", err)
	}
	defer func() { _, _, _ = procGlobalUnlock.Call(h) }()
	// GlobalSize says 0 both for memory that holds nothing and when it
	// fails, which only the last error tells apart.
	_, _, _ = procSetLastError.Call(0)
	n, _, err := procGlobalSize.Call(h)
	if n == 0 && !errors.Is(err, windows.ERROR_SUCCESS) {
		return nil, fmt.Errorf("reading the size of the memory the program handed over: %w", err)
	}
	return bytes.Clone(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n)), nil
}

func (c osClip) files() (sf systemFiles, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return sf, err
	}
	defer closeClipboard()
	// The seq once all read is rendered, before the clipboard closes: a
	// program that renders what it holds only when asked changes the seq
	// as it does, so a seq read before would not be the one the cut keeps.
	defer func() { sf.seq = c.seq() }()
	if h, _, _ := procGetClipboardData.Call(cfHDrop); h != 0 {
		count, _, _ := procDragQueryFileW.Call(h, 0xffffffff, 0, 0)
		for i := range count {
			n, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
			buf := make([]uint16, n+1)
			got, _, _ := procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			if n == 0 || got == 0 {
				return sf, fmt.Errorf("reading the path of item %d of the %d on the clipboard", i+1, count)
			}
			sf.paths = append(sf.paths, windows.UTF16ToString(buf))
		}
		if h, _, _ := procGetClipboardData.Call(clipFormats().effect); h != 0 {
			// Where it cannot be read, the files are copied rather than
			// moved. That is safe: a copy deletes nothing where they were,
			// and leaves the clipboard as it is, so the worst is a second
			// copy the user can delete, where a copy taken for a move
			// would take the files away.
			if b, err := globalBytes(h); err == nil && len(b) >= 4 {
				effect := binary.LittleEndian.Uint32(b)
				sf.cut = effect&dropEffectMove != 0 && effect&dropEffectCopy == 0
			}
		}
		return sf, nil
	}
	h, _, err := procGetClipboardData.Call(clipFormats().descriptor)
	if h == 0 {
		if c.hasFiles() {
			// The clipboard says it holds files, and its owner did not
			// hand them over: one that hands them over only when asked
			// failed to, or took too long. That is no paste of nothing.
			why := errors.New("the program that put files on the clipboard did not hand them over")
			if !errors.Is(err, windows.ERROR_SUCCESS) {
				why = fmt.Errorf("%w: %w", why, err)
			}
			return sf, why
		}
		return sf, nil
	}
	b, err := globalBytes(h)
	if err != nil {
		return sf, fmt.Errorf("reading the list of files on the clipboard: %w", err)
	}
	items, err := fileDescriptors(b)
	if err != nil {
		return sf, err
	}
	sf.names = topNames(items)
	return sf, nil
}

func (c osClip) empty(seq uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if openClipboard() != nil {
		return
	}
	defer closeClipboard()
	if c.seq() == seq {
		_, _, _ = procEmptyClipboard.Call()
	}
}

// outside reads the clipboard through OLE, which alone hands over each
// file held as contents, by its index. The data object it gives works
// only on the thread that took it, so fn runs on that thread.
func (c osClip) outside(seq uint32, fn func(outsideFiles) error) error {
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
	of, err := heldFiles(obj, seq, c.seq)
	if err != nil {
		return err
	}
	return fn(of)
}

// heldFiles reads the list of the files the clipboard's data object obj
// holds as contents, and fails where the clipboard's seq, as now gives
// it, is no longer seq once the list is read. The look comes after the
// read, not before: a copy that came between would have its own folders
// made, which no look before a file is opened reaches.
func heldFiles(obj unsafe.Pointer, seq uint32, now func() uint32) (*oleFiles, error) {
	m, err := getData(obj, clipFormats().descriptor, -1, tymedHGlobal)
	if err != nil {
		return nil, fmt.Errorf("reading the list of files on the clipboard: %w", err)
	}
	b, err := globalBytes(m.handle)
	releaseMedium(&m)
	if err != nil {
		return nil, fmt.Errorf("reading the list of files on the clipboard: %w", err)
	}
	items, err := fileDescriptors(b)
	if err != nil {
		return nil, err
	}
	if err := stillHeld(seq, now()); err != nil {
		return nil, err
	}
	return &oleFiles{now: now, seq: seq, obj: obj, list: items}, nil
}

// getData asks the data object obj for format cf, the item of index i in
// it or -1 for the whole, in one of the media tymed allows.
func getData(obj unsafe.Pointer, cf uintptr, i int32, tymed uint32) (stgMedium, error) {
	f := formatEtc{cf: uint16(cf), aspect: dvAspectContent, index: i, tymed: tymed}
	var m stgMedium
	if hr := comCall(obj, vtGetData, uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&m))); failed(hr) {
		return stgMedium{}, fmt.Errorf("HRESULT %#x", hr)
	}
	if m.tymed&tymed == 0 {
		releaseMedium(&m)
		return stgMedium{}, errors.New("the program that put it there gave it in a form that cannot be read")
	}
	if m.handle == 0 {
		releaseMedium(&m)
		return stgMedium{}, errNoMedium
	}
	return m, nil
}

// errNoMedium says a program handed over nothing for what was asked.
var errNoMedium = errors.New("the program that put it there handed over nothing")

func releaseMedium(m *stgMedium) { _, _, _ = procReleaseStgMedium.Call(uintptr(unsafe.Pointer(m))) }

// oleFiles is the files the clipboard's data object obj holds as
// contents, described by list, while the clipboard's seq, as now gives
// it, is still seq: the object reads what the clipboard holds now, not
// what it held when it was taken.
type oleFiles struct {
	now  func() uint32
	seq  uint32
	obj  unsafe.Pointer
	list []outsideItem
}

func (o *oleFiles) items() []outsideItem { return o.list }

func (o *oleFiles) open(i int) (io.ReadCloser, error) {
	if err := stillHeld(o.seq, o.now()); err != nil {
		return nil, err
	}
	m, err := getData(o.obj, clipFormats().contents, int32(i), tymedIStream|tymedHGlobal)
	if errors.Is(err, errNoMedium) && o.list[i].size == 0 {
		// A file the list says is empty, handed over as no memory at
		// all. The list carries its size, so nothing of what it holds is
		// lost by writing it empty, and sizedReader checks the same.
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if err != nil {
		return nil, err
	}
	// Again, as another copy may have come between the look and the
	// fetch, and a seq only grows. A stream is fetched as it is read, so
	// it is looked at after each read too.
	if err := stillHeld(o.seq, o.now()); err != nil {
		releaseMedium(&m)
		return nil, err
	}
	if m.tymed == tymedIStream {
		// The medium's handle is the stream itself.
		s := &oleStream{m: m, stream: *(*unsafe.Pointer)(unsafe.Pointer(&m.handle))}
		return clipReader{ReadCloser: s, now: o.now, seq: o.seq}, nil
	}
	b, err := globalBytes(m.handle)
	releaseMedium(&m)
	if err != nil && o.list[i].size == 0 {
		// Movable memory of no size cannot be locked, which Windows says
		// of all memory GlobalAlloc gives with GMEM_MOVEABLE and no size.
		// The list says the file is empty, so writing it empty loses
		// nothing; a file the list gives a size cannot take this way.
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if err != nil {
		return nil, err
	}
	if size := o.list[i].size; size >= 0 && size < int64(len(b)) {
		// Global memory may run past what it holds.
		b = b[:size]
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// clipReader reads what the clipboard held at seq, and fails once the
// clipboard's seq, as now gives it, has moved on: what was read may hold
// some of another copy, and is not to be written. It looks after every
// read, so a change is learnt of at once, and where the stream fails or
// runs on rather than ending.
type clipReader struct {
	io.ReadCloser
	now func() uint32
	seq uint32
}

// Read implements [io.Reader].
func (h clipReader) Read(p []byte) (int, error) {
	n, err := h.ReadCloser.Read(p)
	if err := stillHeld(h.seq, h.now()); err != nil {
		return n, err
	}
	return n, err
}

// oleStream reads a file's contents from the stream a program handed
// over, which it may fetch as it is read, as Remote Desktop does.
type oleStream struct {
	m      stgMedium
	stream unsafe.Pointer
	// piece is how much the next read asks for, once it has been set.
	piece int
	// since says how long ago a time was; a test gives one of its own.
	since func(time.Time) time.Duration
}

// Read implements [io.Reader].
func (s *oleStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.piece == 0 {
		s.piece = minPiece
	}
	if s.since == nil {
		s.since = time.Since
	}
	var n uint32
	start := time.Now()
	hr := comCall(s.stream, vtStreamRead, uintptr(unsafe.Pointer(&p[0])),
		uintptr(min(len(p), s.piece)), uintptr(unsafe.Pointer(&n)))
	if took := s.since(start); took < aimRead {
		s.piece = min(s.piece*2, maxPiece)
	} else if n > 0 {
		s.piece = min(max(int(float64(n)*aimRead.Seconds()/took.Seconds()), minPiece), maxPiece)
	}
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
