package filemanager

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// LocalFS returns the computer's own file system, with its trash, its
// drives and their space, its links, and on Windows its attributes and
// the files a cloud provider keeps online. It is what a window shows
// when its options name no other.
func LocalFS() FS { return localFS{} }

// localFS is the computer's own file system, through package os.
type localFS struct{}

// ID implements [FS]: the computer's own file system has the empty ID.
func (localFS) ID() string { return "" }

// Paths implements [FS].
func (localFS) Paths() PathStyle { return SystemPaths }

// Home implements [FS]: the user's home folder.
func (localFS) Home() (string, error) { return os.UserHomeDir() }

// ReadDir implements [FS]. It reads a large folder in batches, so ctx can
// stop it part of the way through.
func (localFS) ReadDir(ctx context.Context, dir string) (es []fs.DirEntry, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			es, err = nil, fmt.Errorf("reading %s: %w", dir, cerr)
		}
	}()
	var out []fs.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, rerr := f.ReadDir(1024)
		out = append(out, batch...)
		if errors.Is(rerr, io.EOF) {
			return out, nil
		}
		if rerr != nil {
			return nil, fmt.Errorf("reading %s: %w", dir, rerr)
		}
		if len(batch) == 0 {
			return out, nil
		}
	}
}

// Stat implements [FS].
func (localFS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// Lstat implements [FS].
func (localFS) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

// Open implements [FS].
func (localFS) Open(path string) (io.ReadSeekCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		// A nil *os.File in the interface would not be nil.
		return nil, err
	}
	return f, nil
}

// Create implements [FS], as os.CreateTemp makes its files.
func (localFS) Create(path string) (io.WriteCloser, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Mkdir implements [FS].
func (localFS) Mkdir(path string, perm fs.FileMode) error { return os.Mkdir(path, perm) }

// Rename implements [FS].
func (localFS) Rename(from, to string) error {
	err := os.Rename(from, to)
	if err != nil && isCrossDevice(err) {
		return crossDevice{err}
	}
	return err
}

// crossDevice is a rename that failed as it crossed volumes. It says
// what the system said, and matches ErrCrossDevice.
type crossDevice struct{ err error }

func (c crossDevice) Error() string { return c.err.Error() }

func (c crossDevice) Unwrap() error { return c.err }

// Is makes the error match ErrCrossDevice.
func (c crossDevice) Is(target error) bool { return target == ErrCrossDevice }

// Remove implements [FS].
func (localFS) Remove(path string) error { return os.Remove(path) }

// Trash implements [Trasher] with the system's trash.
func (localFS) Trash(path string) (string, error) {
	t, err := systemTrash()
	if err != nil {
		return "", err
	}
	return t.Trash(path)
}

// Restore implements [Trasher].
func (localFS) Restore(original, trashed string, at time.Time, to string) error {
	t, err := systemTrash()
	if err != nil {
		return err
	}
	return t.Restore(original, trashed, at, to)
}

// Describe implements [Trasher].
func (localFS) Describe(trashed string) string {
	t, err := systemTrash()
	if err != nil {
		return ""
	}
	return t.Describe(trashed)
}

// systemTrashCan is the system's trash, where the window can open it.
func systemTrashCan() (TrashCan, error) {
	t, err := systemTrash()
	if err != nil {
		return nil, err
	}
	c, ok := t.(TrashCan)
	if !ok {
		return nil, errNoTrashCan
	}
	return c, nil
}

// errNoTrashCan says the window cannot open the system's trash.
var errNoTrashCan = noTrashCan{}

// noTrashCan is errNoTrashCan, which matches errors.ErrUnsupported.
type noTrashCan struct{}

func (noTrashCan) Error() string { return "opening the trash is not supported on this system" }

// Is makes the error match errors.ErrUnsupported.
func (noTrashCan) Is(target error) bool { return target == errors.ErrUnsupported }

// TrashItems implements [TrashCan] with the system's trash.
func (localFS) TrashItems(ctx context.Context) ([]TrashItem, error) {
	t, err := systemTrashCan()
	if err != nil {
		return nil, err
	}
	return t.TrashItems(ctx)
}

// RestoreItem implements [TrashCan].
func (localFS) RestoreItem(it TrashItem) error {
	t, err := systemTrashCan()
	if err != nil {
		return err
	}
	return t.RestoreItem(it)
}

// PurgeItem implements [TrashCan].
func (localFS) PurgeItem(ctx context.Context, it TrashItem) error {
	t, err := systemTrashCan()
	if err != nil {
		return err
	}
	return t.PurgeItem(ctx, it)
}

// EmptyTrash implements [TrashCan].
func (localFS) EmptyTrash(ctx context.Context) error {
	t, err := systemTrashCan()
	if err != nil {
		return err
	}
	return t.EmptyTrash(ctx)
}

// Space implements [SpaceReporter].
func (localFS) Space(path string) (free, total uint64, err error) {
	s, err := volumeSpace(path)
	return s.free, s.total, err
}

// Volume implements [VolumeNamer].
func (localFS) Volume(path string) (string, error) { return volumeOf(path) }

// Readlink implements [Linker].
func (localFS) Readlink(path string) (string, error) { return os.Readlink(path) }

// Symlink implements [Linker].
func (localFS) Symlink(target, path string) error { return os.Symlink(target, path) }

// EvalSymlinks implements [Linker].
func (localFS) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

// Chmod implements [Stamper].
func (localFS) Chmod(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }

// Chtimes implements [Stamper]. The time the item was last read stays.
func (localFS) Chtimes(path string, mod time.Time) error { return os.Chtimes(path, time.Time{}, mod) }

// SameFile implements [SameFiler].
func (localFS) SameFile(a, b fs.FileInfo) bool { return os.SameFile(a, b) }

// Hidden implements [HiddenReporter]: the hidden attribute of Windows.
func (localFS) Hidden(info fs.FileInfo) bool { return hiddenAttr(info) }

// OnlineOnly implements [OnlineReporter]: a file a cloud provider such
// as OneDrive keeps online, on Windows.
func (localFS) OnlineOnly(info fs.FileInfo) bool { return onlineOnly(info) }

// Cloud implements [CloudReporter]: how a cloud provider such as
// OneDrive keeps the item, on Windows.
func (localFS) Cloud(dir string, info fs.FileInfo) CloudState { return cloudOf(dir, info) }

// SystemThumb implements [OnlineReporter]: the thumbnail Windows keeps.
func (localFS) SystemThumb(path string, size int) (image.Image, error) {
	return cachedShellThumb(path, size)
}

// Times implements [TimesReporter].
func (localFS) Times(info fs.FileInfo) (created, accessed time.Time, ok bool) { return fileTimes(info) }

// Attrs implements [Attributer]: Windows's attributes, and none
// elsewhere.
func (localFS) Attrs(path string) (readOnly, hidden, ok bool, err error) { return readAttrs(path) }

// SetAttrs implements [Attributer].
func (localFS) SetAttrs(path string, readOnly, hidden bool) error {
	return setAttrs(path, readOnly, hidden)
}
