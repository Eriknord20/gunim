// Package openfiles lists the files a process holds open, for tests:
// Windows refuses to take away a file held open, or the folder holding
// it, so a test that leaves one open fails there. Under lists them on
// Linux, so the same test fails on Linux too.
package openfiles

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Under lists the files under dir the process holds open. It lists
// them on Linux, through /proc, and none elsewhere.
func Under(dir string) []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	var out []string
	for _, fd := range fds {
		target, err := os.Readlink("/proc/self/fd/" + fd.Name())
		if err != nil {
			continue
		}
		target = strings.TrimSuffix(target, " (deleted)")
		if target == dir || strings.HasPrefix(target, dir+string(filepath.Separator)) {
			out = append(out, target)
		}
	}
	return out
}

// checked are the folders each test checks, so a test that checks one
// twice checks it once, after the cleanups of both.
var checked sync.Map

type checkKey struct {
	t   testing.TB
	dir string
}

// Check fails t where it ends holding a file under dir open, once the
// cleanups registered after the first Check of dir ran: call it before
// them.
func Check(t testing.TB, dir string) {
	t.Helper()
	k := checkKey{t, dir}
	if _, again := checked.LoadOrStore(k, true); again {
		return
	}
	t.Cleanup(func() {
		checked.Delete(k)
		if open := Under(dir); len(open) > 0 {
			t.Errorf("the test ends with files open, which Windows would refuse to take away: %v", open)
		}
	})
}
