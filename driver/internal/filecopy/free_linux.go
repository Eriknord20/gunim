//go:build linux || android

package filecopy

import "syscall"

// freeSpace returns the bytes free on the disk holding dir for this user.
func freeSpace(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true //nolint:unconvert // Bsize is int32 on 32-bit Linux.
}
