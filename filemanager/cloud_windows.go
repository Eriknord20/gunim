package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	cldapi                      = windows.NewLazySystemDLL("cldapi.dll")
	procCfGetSyncRootInfoByPath = cldapi.NewProc("CfGetSyncRootInfoByPath")
)

// onlineOnly reports whether the item info describes keeps its contents online only, so reading them downloads them.
func onlineOnly(info fs.FileInfo) bool {
	return !info.IsDir() && cloudOf("", info) == CloudOnline
}

// cloudOf is the cloud state of the item info describes in the folder dir; "" for a folder not known.
func cloudOf(dir string, info fs.FileInfo) CloudState {
	d, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return CloudNone
	}
	return WindowsCloud(d.FileAttributes, info.IsDir(), dir != "" && inSyncRoot(dir))
}

// InCloudFolder reports whether the folder dir lies in a cloud provider's folder, a sync root, as OneDrive's does:
// for a program that sends Windows' attributes on, to say so beside them.
func InCloudFolder(dir string) bool { return inSyncRoot(dir) }

// syncRoots remembers which folders lie in a cloud provider's folder, as a folder's items are each asked about.
var syncRoots struct {
	sync.Mutex
	in map[string]bool
}

// inSyncRoot reports whether the folder dir lies in a cloud provider's folder, a sync root, as OneDrive's does.
func inSyncRoot(dir string) bool {
	syncRoots.Lock()
	in, ok := syncRoots.in[dir]
	syncRoots.Unlock()
	if ok {
		return in
	}
	in = askSyncRoot(dir)
	syncRoots.Lock()
	if syncRoots.in == nil || len(syncRoots.in) > 256 {
		syncRoots.in = map[string]bool{}
	}
	syncRoots.in[dir] = in
	syncRoots.Unlock()
	return in
}

// askSyncRoot asks Windows' cloud files API whether path lies in a sync root. Before that API, none does.
func askSyncRoot(path string) bool {
	if procCfGetSyncRootInfoByPath.Find() != nil {
		return false
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	// CF_SYNC_ROOT_INFO_BASIC, whose answer is the root's file ID.
	var basic [64]byte
	var got uint32
	hr, _, _ := procCfGetSyncRootInfoByPath.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&basic[0])), uintptr(len(basic)), uintptr(unsafe.Pointer(&got)))
	return hr == 0
}

// errNotInCloud is why an item outside any cloud provider's folder
// cannot be kept on this device or freed.
var errNotInCloud = errors.New("it is not in a cloud provider's folder, such as OneDrive's")

// The attributes that tell a cloud provider to keep an item on this
// device, or to free its space, as attrib's +P and +U set them, and those
// SetFileAttributes takes besides.
const (
	fileAttributeUnpinned = 0x00100000
	settableAttributes    = windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_HIDDEN |
		windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_TEMPORARY |
		windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED | windows.FILE_ATTRIBUTE_OFFLINE | fileAttributePinned |
		fileAttributeUnpinned
)

// CanKeepOnDevice implements [CloudKeeper]: Windows' own files always
// can.
func (localFS) CanKeepOnDevice() bool { return true }

// KeepOnDevice implements [CloudKeeper], as Explorer's Always keep on this
// device and Free up space do: it marks the item pinned or unpinned, and
// all a folder holds, and the cloud provider downloads or frees them.
func (localFS) KeepOnDevice(ctx context.Context, path string, keep bool) error {
	if !inSyncRoot(filepath.Dir(path)) && !inSyncRoot(path) {
		return errNotInCloud
	}
	var failed int
	var first error
	keepOnDevice(ctx, path, keep, func(err error) {
		failed++
		if first == nil {
			first = err
		}
	})
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case failed == 1:
		return first
	case failed > 1:
		return fmt.Errorf("%d items could not be marked, the first: %w", failed, first)
	}
	return nil
}

// keepOnDevice marks the item at path, and all under it, pinned with keep
// and unpinned without, telling fail of each item it could not mark.
func keepOnDevice(ctx context.Context, path string, keep bool, fail func(error)) {
	if ctx.Err() != nil {
		return
	}
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		fail(err)
		return
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		fail(&fs.PathError{Op: "read the attributes of", Path: path, Err: err})
		return
	}
	next := attrs &^ (fileAttributePinned | fileAttributeUnpinned)
	if keep {
		next |= fileAttributePinned
	} else {
		next |= fileAttributeUnpinned
	}
	if err := windows.SetFileAttributes(p, next&settableAttributes); err != nil {
		fail(&fs.PathError{Op: "mark", Path: path, Err: err})
		return
	}
	// Into a folder, never by a link or a junction to one elsewhere: a
	// cloud provider's own items show as plain files and folders.
	if info, err := os.Lstat(path); err != nil || !info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return
	}
	es, err := os.ReadDir(path)
	if err != nil {
		fail(err)
		return
	}
	for _, e := range es {
		keepOnDevice(ctx, filepath.Join(path, e.Name()), keep, fail)
	}
}

// longPath is path as Windows takes it past 260 characters: \\?\C:\…, or
// \\?\UNC\server\share\… for a share.
func longPath(path string) string {
	switch {
	case len(path) < 248 || strings.HasPrefix(path, `\\?\`):
		return path
	case strings.HasPrefix(path, `\\`):
		return `\\?\UNC\` + path[2:]
	case filepath.IsAbs(path):
		return `\\?\` + path
	}
	return path
}
