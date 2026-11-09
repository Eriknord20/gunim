//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// TestTheSystemClipboardHandsOverItsFiles reads the files on the system
// clipboard as Paste does, and says what they are. It runs with
// GUNIM_CLIPBOARD=1, after copying files: in Explorer, or in a Remote
// Desktop session, for files held as contents.
func TestTheSystemClipboardHandsOverItsFiles(t *testing.T) {
	if os.Getenv("GUNIM_CLIPBOARD") != "1" {
		t.Skip("set GUNIM_CLIPBOARD=1, after copying files, to read them off the system clipboard")
	}
	var c osClip
	if !c.hasFiles() {
		t.Fatal("the clipboard holds no files")
	}
	sf, err := c.files()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("seq %d, cut %v, paths %q, names %q", sf.seq, sf.cut, sf.paths, sf.names)
	if len(sf.paths) > 0 {
		return
	}
	err = c.outside(sf.seq, func(of outsideFiles) error {
		for i, it := range of.items() {
			if it.dir {
				t.Logf("%d: folder %s, changed %v", i, it.name, it.mod)
				continue
			}
			in, err := of.open(i)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, in)
			if err := in.Close(); err != nil {
				return err
			}
			if err != nil {
				return err
			}
			t.Logf("%d: %s, %d bytes read of %d said, sha256 %x, changed %v", i, it.name, n, it.size, h.Sum(nil), it.mod)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	rec, err := runJob(context.Background(), job{kind: OpCopy, srcs: sf.names, dest: dest, outside: true, seq: sf.seq},
		env{fs: LocalFS(), outside: c.outside})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pasted %q into %s", rec.landed, dest)
	err = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		t.Logf("%s: %d bytes, sha256 %x", p, len(b), sha256.Sum256(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fakeCOM is a COM object whose methods are Go functions.
type fakeCOM struct{ table *[5]uintptr }

// newFakeCOM makes an object whose method 3 is method3, and whose other
// methods do nothing.
func newFakeCOM(method3 any) unsafe.Pointer {
	nop := syscall.NewCallback(func(uintptr) uintptr { return 0 })
	return unsafe.Pointer(&fakeCOM{table: &[5]uintptr{nop, nop, nop, syscall.NewCallback(method3), nop}})
}

// TestAStreamCountsWhatItReadsWhereTheStackMoves fails where comCall lets
// a COM method write through a pointer into a stack the goroutine has
// since been moved off, and Read sees no bytes read. Go moves a stack
// only now and then, so the test shows it run as
//
//	go test -gcflags=github.com/marrasen/gunim/filemanager=-d=maymorestack=runtime.mayMoreStackMove -run StackMoves ./filemanager/
//
// which moves the stack at every call, as CI runs it.
func TestAStreamCountsWhatItReadsWhereTheStackMoves(t *testing.T) {
	s := &oleStream{stream: newFakeCOM(func(_, _, size, read uintptr) uintptr {
		*(*uint32)(*(*unsafe.Pointer)(unsafe.Pointer(&read))) = uint32(size)
		return 0
	})}
	buf := make([]byte, 4096)
	if n, err := s.Read(buf); n != len(buf) || err != nil {
		t.Fatalf("the stream filled %d bytes, and Read says %d, %v", len(buf), n, err)
	}
}

func TestMemoryThatCannotBeReadIsAnError(t *testing.T) {
	if b, err := globalBytes(0); err == nil {
		t.Fatalf("memory that cannot be locked reads as %q", b)
	}
}

func TestAStreamIsAskedForMoreWhileItAnswersQuicklyAndLessWhenNot(t *testing.T) {
	var asked []uintptr
	slow := false
	s := &oleStream{stream: newFakeCOM(func(_, _, size, read uintptr) uintptr {
		asked = append(asked, size)
		if slow {
			time.Sleep(slowRead + 100*time.Millisecond)
		}
		*(*uint32)(*(*unsafe.Pointer)(unsafe.Pointer(&read))) = uint32(size)
		return 0
	})}
	buf := make([]byte, 4<<20)
	for range 6 {
		if _, err := s.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	if asked[0] != minPiece || slices.Max(asked) != maxPiece {
		t.Fatalf("a quick stream was asked for %d", asked)
	}
	slow = true
	for range 2 {
		if _, err := s.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	if last := asked[len(asked)-1]; last != maxPiece/2 {
		t.Fatalf("a stream slow to answer was asked for %d", asked)
	}
}

func TestAnEmptyFileIsEmptyThoughItsMemoryCannotBeLocked(t *testing.T) {
	// Movable memory of no size, as a program may hand over for an empty
	// file, cannot be locked.
	const gmemMoveable = 0x2
	h, _, err := kernel32.NewProc("GlobalAlloc").Call(gmemMoveable, 0)
	if h == 0 {
		t.Fatal(err)
	}
	nop := syscall.NewCallback(func(uintptr) uintptr { return 0 })
	obj := unsafe.Pointer(&fakeCOM{table: &[5]uintptr{nop, nop, nop, syscall.NewCallback(func(_, _, medium uintptr) uintptr {
		m := (*stgMedium)(*(*unsafe.Pointer)(unsafe.Pointer(&medium)))
		m.tymed, m.handle = tymedHGlobal, h
		return 0
	}), nop}})
	for _, tc := range []struct {
		size  int64
		empty bool
	}{{0, true}, {-1, false}} {
		o := &oleFiles{now: func() uint32 { return 0 }, obj: obj, list: []outsideItem{{name: "a", size: tc.size}}}
		in, err := o.open(0)
		if tc.empty != (err == nil) {
			t.Fatalf("a file said to be %d bytes opens with %v", tc.size, err)
		}
		if in != nil {
			if b, _ := io.ReadAll(in); len(b) > 0 {
				t.Fatalf("an empty file holds %q", b)
			}
		}
		if tc.size == 0 {
			// The release freed it; the next case hands over another.
			h, _, _ = kernel32.NewProc("GlobalAlloc").Call(gmemMoveable, 0)
		}
	}
}

// fakeDataObject is a data object whose GetData hands over a stream, and
// counts its calls and the stream's releases.
func fakeDataObject(gets, releases *int) unsafe.Pointer {
	nop := syscall.NewCallback(func(uintptr) uintptr { return 0 })
	release := syscall.NewCallback(func(uintptr) uintptr { *releases++; return 0 })
	stream := unsafe.Pointer(&fakeCOM{table: &[5]uintptr{nop, nop, release, nop, nop}})
	return newFakeCOM(func(_, _, medium uintptr) uintptr {
		*gets++
		m := (*stgMedium)(*(*unsafe.Pointer)(unsafe.Pointer(&medium)))
		m.tymed, m.handle = tymedIStream, uintptr(stream)
		return 0
	})
}

func TestAFileIsNotFetchedFromAClipboardThatChanged(t *testing.T) {
	var gets, releases int
	o := &oleFiles{now: func() uint32 { return 8 }, seq: 7, obj: fakeDataObject(&gets, &releases),
		list: []outsideItem{{name: "a.txt", size: 1}}}
	if _, err := o.open(0); !errors.Is(err, errClipboardChanged) || gets != 0 {
		t.Fatalf("open says %v, and fetched %d times", err, gets)
	}
}

func TestAFileFetchedAsTheClipboardChangesIsLetGo(t *testing.T) {
	var gets, releases int
	seqs := []uint32{7, 8}
	o := &oleFiles{seq: 7, obj: fakeDataObject(&gets, &releases), list: []outsideItem{{name: "a.txt", size: 1}}}
	o.now = func() uint32 { s := seqs[0]; seqs = seqs[1:]; return s }
	if _, err := o.open(0); !errors.Is(err, errClipboardChanged) || gets != 1 || releases != 1 {
		t.Fatalf("open says %v, fetched %d times and let go %d", err, gets, releases)
	}
}
