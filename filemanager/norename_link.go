//go:build !windows

package filemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// unlink removes a name; tests stand in for it to fail as disks do.
var unlink = os.Remove

// linkRename moves a file from one name to another by a link, which fails
// where the new name is taken, and then removes the old name. What it
// cannot move so it refuses with an error that matches
// errors.ErrUnsupported, having moved nothing: a folder, which cannot be
// linked; a link, which some systems follow as they link; and a file the
// system will not link here, as one another user owns where links are
// protected, or one on a file system without links. Any other failure, as
// of the disk, it returns as it is.
func linkRename(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s cannot be moved by a link: %w", from, errors.ErrUnsupported)
	}
	if err := os.Link(from, to); err != nil {
		if cannotLink(err) {
			return fmt.Errorf("%s cannot be moved by a link: %w: %w", from, errors.ErrUnsupported, err)
		}
		return err
	}
	if err := unlink(from); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// The old name went anyway, as an unlink retried over NFS does
			// or another program's purge; to is the file's only name. Not
			// where its folder went: the old name may be elsewhere now.
			if _, derr := os.Lstat(filepath.Dir(from)); derr == nil {
				return nil
			}
		}
		// The new name goes again, so the file is not left at both.
		if rerr := unlink(to); rerr != nil {
			return fmt.Errorf("%s is at %s now, but it is still at %s too: %w",
				from, to, from, errors.Join(err, fmt.Errorf("taking the new name off again: %w", rerr)))
		}
		return err
	}
	return nil
}

// cannotLink reports whether err, from a link that failed, says the
// system makes no link here, rather than that something went wrong:
// links protected (EPERM), none on the file system (EOPNOTSUPP, ENOTSUP,
// ENOSYS), or too many to the file already (EMLINK).
func cannotLink(err error) bool {
	if errors.Is(err, fs.ErrExist) {
		return false
	}
	for _, e := range []syscall.Errno{syscall.EPERM, syscall.EOPNOTSUPP, syscall.ENOTSUP, syscall.ENOSYS, syscall.EMLINK} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
