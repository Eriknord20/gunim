//go:build !linux && !windows

package filemanager

// RenameNoReplace implements [NoReplaceRenamer] by a link, as the system
// has no rename that refuses a name taken.
func (localFS) RenameNoReplace(from, to string) error { return linkRename(from, to) }
