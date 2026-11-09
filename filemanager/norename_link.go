//go:build !windows

package filemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// linkRename moves a file from one name to another by a link, which fails
// where the new name is taken, and then removes the old name. What it
// cannot move so it refuses with an error that matches
// errors.ErrUnsupported, having moved nothing: a folder, which cannot be
// linked; a link, which some systems follow as they link; and a file the
// system will not link, as one another user owns where links are
// protected, or one on a file system without links.
func linkRename(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s cannot be moved by a link: %w", from, errors.ErrUnsupported)
	}
	if err := os.Link(from, to); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return err
		}
		return fmt.Errorf("%s cannot be moved by a link: %w: %w", from, errors.ErrUnsupported, err)
	}
	if err := os.Remove(from); err != nil {
		// The new name goes again, so the file is not left at both.
		if rerr := os.Remove(to); rerr != nil {
			return fmt.Errorf("%s is at %s now, but it is still at %s too: %w", from, to, from, err)
		}
		return err
	}
	return nil
}
