package filecopy

import (
	"os"
	"syscall"
)

// dupFD returns a handle of its own for f, for Take to close.
func dupFD(f *os.File) (int, error) {
	p, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	err = syscall.DuplicateHandle(p, syscall.Handle(f.Fd()), p, &h, 0, false, syscall.DUPLICATE_SAME_ACCESS)
	return int(h), err
}
