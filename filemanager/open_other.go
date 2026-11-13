//go:build !windows

package filemanager

import "os"

// openFile opens the file at path for reading.
func openFile(path string) (*os.File, error) { return os.Open(path) }
