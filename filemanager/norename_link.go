//go:build !windows

package filemanager

import (
	"errors"
	"fmt"
	"os"
)

// linkRename moves a file from one name to another by a link, which fails
// where the new name is taken, and then removes the old name. A folder
// cannot be linked, and is refused with an error that matches
// errors.ErrUnsupported.
func linkRename(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a folder, which this file system cannot move without the risk of replacing one at %s: %w",
			from, to, errors.ErrUnsupported)
	}
	if err := os.Link(from, to); err != nil {
		return err
	}
	if err := os.Remove(from); err != nil {
		return fmt.Errorf("%s is at %s now, but it is still at %s too: %w", from, to, from, err)
	}
	return nil
}
