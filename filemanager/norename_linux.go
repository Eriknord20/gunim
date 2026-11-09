package filemanager

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace implements [NoReplaceRenamer] with renameat2 and
// RENAME_NOREPLACE. A file system that has no such rename, as an older
// NFS, moves a file by a link instead.
func (localFS) RenameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return linkRename(from, to)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
