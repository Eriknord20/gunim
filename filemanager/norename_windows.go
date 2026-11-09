package filemanager

import (
	"os"

	"golang.org/x/sys/windows"
)

// RenameNoReplace implements [NoReplaceRenamer] with MoveFileEx, given no
// MOVEFILE_REPLACE_EXISTING.
func (localFS) RenameNoReplace(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, 0); err != nil {
		if isCrossDevice(err) {
			return crossDevice{&os.LinkError{Op: "rename", Old: from, New: to, Err: err}}
		}
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
