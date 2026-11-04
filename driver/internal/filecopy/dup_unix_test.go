//go:build !windows

package filecopy

import (
	"os"
	"syscall"
)

// dupFD returns a descriptor of its own for f, for Take to close.
func dupFD(f *os.File) (int, error) { return syscall.Dup(int(f.Fd())) }
