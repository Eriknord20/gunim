//go:build !linux && !android

package filecopy

// freeSpace tells nothing here: only Android copies files handed over.
func freeSpace(string) (int64, bool) { return 0, false }
