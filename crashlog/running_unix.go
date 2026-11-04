//go:build !windows

package crashlog

import (
	"errors"
	"os"
	"syscall"
)

// running reports whether the process pid runs. A process of another
// user's that the signal may not reach runs too.
func running(pid int) bool {
	if pid == os.Getpid() {
		return true
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// heldOpen reports whether err, removing a crash file, says a run still
// going holds it open, which happens on Windows alone.
func heldOpen(error) bool { return false }
