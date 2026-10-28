package filemanager

import (
	"context"
	"errors"
	"image"
	"io"
	"io/fs"
	"time"
)

// FS is a file system a window shows: the computer's own, as LocalFS
// is, or one elsewhere, as a server reached over SFTP. It holds what
// every file system can do; what only some can is in the smaller
// interfaces below, which a window looks for with a type assertion and
// does without where they are missing.
//
// Paths are whole paths, written as Paths says. The methods are called
// from several goroutines at once.
type FS interface {
	// ID names the file system. It is empty for the computer's own,
	// whose paths the system's other programs understand, and otherwise
	// the same for every FS that reaches the same files. Windows on file
	// systems of different IDs share no clipboard and take no drops
	// from each other.
	ID() string
	// Paths is how the file system writes its paths.
	Paths() PathStyle
	// Home is the folder a window opens on when it is given none.
	Home() (string, error)
	// ReadDir reads the items of the folder dir, in no set order, and
	// stops early with ctx's error when ctx ends. What an item's Info
	// returns is what Lstat would say of it.
	ReadDir(ctx context.Context, dir string) ([]fs.DirEntry, error)
	// Stat says what is at path, following a link at the end of it.
	Stat(path string) (fs.FileInfo, error)
	// Lstat says what is at path, and of a link the link itself.
	Lstat(path string) (fs.FileInfo, error)
	// Open opens the file at path for reading.
	Open(path string) (io.ReadSeekCloser, error)
	// Create makes a file at path to write, which only the user can
	// read until Chmod says otherwise, and fails with an error that
	// matches fs.ErrExist where something is there already.
	Create(path string) (io.WriteCloser, error)
	// Mkdir makes a folder at path, with perm, and fails with an error
	// that matches fs.ErrExist where something is there already.
	Mkdir(path string, perm fs.FileMode) error
	// Rename moves the item at from to to. It must replace a file
	// already at to, as os.Rename does, since a copy writes to a part
	// file and renames it over the file it replaces. Where the two are
	// on different volumes and it cannot, it fails with an error that
	// matches ErrCrossDevice, and a move copies and deletes instead.
	Rename(from, to string) error
	// Remove removes the file, the link or the empty folder at path.
	Remove(path string) error
}

// ErrCrossDevice is what a rename across volumes fails with.
var ErrCrossDevice = errors.New("the rename crosses volumes")

// Trasher is a file system with a trash. Without one, deleting asks first
// and deletes for good, and a copy cannot be undone. A file system that
// finds only when it tries that it has no trash, as a server may, fails
// Trash with an error that matches errors.ErrUnsupported, and the window
// then does without.
type Trasher interface {
	// Trash moves path to the trash and returns where it went there, or
	// "" where the system does not say.
	Trash(path string) (string, error)
	// Restore puts the item trashed from original at time at, which went
	// to trashed, back at to. Nothing may be at to.
	Restore(original, trashed string, at time.Time, to string) error
	// Describe says what the item that went to trashed is, for a question
	// about restoring it.
	Describe(trashed string) string
}

// TrashCan is a trash the window can open: Go › Trash shows what it holds
// on a page of its own, where the user puts items back or deletes them for
// good. Without one, the trash is the system's to open. A trash that finds
// only when it reads that the system has none fails TrashItems with an
// error that matches errors.ErrUnsupported.
type TrashCan interface {
	Trasher
	// TrashItems lists what the trash holds, in no set order, and stops
	// early with ctx's error when ctx ends.
	TrashItems(ctx context.Context) ([]TrashItem, error)
	// RestoreItem puts it back where it came from, where nothing may be.
	RestoreItem(it TrashItem) error
	// PurgeItem deletes it for good.
	PurgeItem(ctx context.Context, it TrashItem) error
	// EmptyTrash deletes all the trash holds for good.
	EmptyTrash(ctx context.Context) error
}

// TrashItem is an item in a trash.
type TrashItem struct {
	// ID is where the trash keeps the item, which names it there.
	ID string
	// Name is the item's own name, and Original the whole path it came
	// from.
	Name, Original string
	// Deleted is when it went to the trash, zero where the trash does not
	// say.
	Deleted time.Time
	// Size is a file's bytes, and -1 for a folder.
	Size int64
	Dir  bool
}

// SpaceReporter is a file system that knows how much room its volumes
// have. Without one, or when Space fails with an error that matches
// errors.ErrUnsupported, the window says nothing of free space.
type SpaceReporter interface {
	// Space returns the bytes free and in all on the volume that holds
	// path.
	Space(path string) (free, total uint64, err error)
}

// VolumeNamer is a file system of several volumes, such as the drives of
// a computer. A drag moves items on one volume and copies them across
// volumes, so the window needs to tell them apart. Without one, the whole
// file system is one volume.
type VolumeNamer interface {
	// Volume names the volume the item at path is on.
	Volume(path string) (string, error)
}

// Linker is a file system with symbolic links. Without one, a link can
// neither be copied nor show where it points.
type Linker interface {
	// Readlink returns where the link at path points.
	Readlink(path string) (string, error)
	// Symlink makes a link at path that points to target.
	Symlink(target, path string) error
	// EvalSymlinks returns path with the links along it followed.
	EvalSymlinks(path string) (string, error)
}

// Stamper is a file system that sets the mode and the time of its items.
// Without one, a copy keeps neither.
type Stamper interface {
	Chmod(path string, mode fs.FileMode) error
	// Chtimes sets when the item at path was last changed.
	Chtimes(path string, mod time.Time) error
}

// SameFiler is a file system that tells when two descriptions are of the
// same item, as a name that differs in case only can be on a system
// that ignores case. Without one, two items are the same only by path.
type SameFiler interface {
	SameFile(a, b fs.FileInfo) bool
}

// HiddenReporter is a file system that hides items in a way of its own,
// beyond a name starting with a dot, as Windows does with an attribute.
type HiddenReporter interface {
	// Hidden reports whether the item info describes is hidden.
	Hidden(info fs.FileInfo) bool
}

// OnlineReporter is a file system some of whose files keep their
// contents online only, with a cloud provider, so reading them downloads
// them. The window reads such a file only when the user asks.
type OnlineReporter interface {
	// OnlineOnly reports whether the file info describes keeps its
	// contents online only.
	OnlineOnly(info fs.FileInfo) bool
	// SystemThumb returns the thumbnail the system already keeps of the
	// file at path, at about size pixels across, or nil where it keeps
	// none.
	SystemThumb(path string, size int) (image.Image, error)
}

// TimesReporter is a file system that knows when its items were made and
// last read, for the Properties dialog.
type TimesReporter interface {
	// Times returns when the item info describes was made and last read.
	// created is zero where the system does not keep it, and ok is false
	// where it says neither.
	Times(info fs.FileInfo) (created, accessed time.Time, ok bool)
}

// Attributer is a file system whose items are read-only and hidden by
// attributes, as on Windows, which the Properties dialog shows and
// changes.
type Attributer interface {
	// Attrs reads whether the item at path is read-only and hidden. ok
	// is false where the item has no such attributes.
	Attrs(path string) (readOnly, hidden, ok bool, err error)
	SetAttrs(path string, readOnly, hidden bool) error
}

// SystemOpener is a file system other than the computer's own that
// opens its files with the computer's programs itself. The computer's
// own files open through the window. The files of a file system that is
// neither are fetched to a temporary folder on the computer and open
// from there; its folders do not open with the computer's programs, and
// nothing of it shows in the system's file manager.
type SystemOpener interface {
	// OpenInSystem opens the file or folder at path with the program the
	// system has for it.
	OpenInSystem(path string) error
	// RevealInSystem shows the item at path in the system's file
	// manager.
	RevealInSystem(path string) error
}

// TrueCaser is a file system whose names ignore case, which can say how
// a path is really spelled: TrueCase returns path with each name as the
// file system has it, for a path typed in another case, such as
// /g:/workspace for /G:/Workspace. The window asks only of paths the
// user typed.
type TrueCaser interface {
	TrueCase(path string) (string, error)
}
