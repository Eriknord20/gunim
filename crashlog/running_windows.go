package crashlog

import (
	"errors"

	"golang.org/x/sys/windows"
)

// running says no to every process: on Windows, the file of a run
// still going is open, and so cannot be removed.
func running(int) bool { return false }

// heldOpen reports whether err, removing a crash file, says a run still
// going holds it open, as it does on Windows.
func heldOpen(err error) bool { return errors.Is(err, windows.ERROR_SHARING_VIOLATION) }
