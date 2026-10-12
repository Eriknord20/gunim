//go:build (!linux && !windows) || android

package install

import (
	"os"
	"os/exec"
)

// Installing is not done on this system yet: Run lets the program run
// as it is.
const (
	supported   = false
	caseless    = false
	movesAside  = false
	exeSuffix   = ""
	foldersHere = false
)

func defaultDir(*App) (string, error)            { return "", ErrUnsupported }
func register(*App, Installation) error          { return ErrUnsupported }
func detect(*App, string) map[string]bool        { return map[string]bool{} }
func unregister(*App, Installation) error        { return ErrUnsupported }
func removeFiles([]string, string) error         { return ErrUnsupported }
func removeWhenEnded(string)                     {}
func freeSpace(string) (int64, error)            { return 0, ErrUnsupported }
func running(string) ([]int, error)              { return nil, ErrUnsupported }
func runningParents(string) (map[int]int, error) { return nil, ErrUnsupported }
func watchProcess(int) (func() bool, func())     { return func() bool { return false }, func() {} }

func launch(exe string, args []string, env ...string) error {
	cmd := exec.Command(exe, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Start()
}
