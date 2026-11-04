// Package crashlog keeps what the Go runtime writes as a program dies:
// a panic no code recovers, or a fatal error, as a map written by two
// goroutines at once. Each run writes to a file of its own, and the
// next start finds the files a crash wrote to, to show them.
package crashlog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
)

// cut ends the head of a run's file. Anything after it is what the
// runtime wrote as the run crashed.
const cut = "----\n"

// keepSeen is how many reports seen the folder seen keeps.
const keepSeen = 20

// Dir is the folder crash reports of the program in folder app go in, in
// the user's cache folder, or "" where the system has none.
func Dir(app string) string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, app, "crashes")
}

// Run is a run's crash file.
type Run struct {
	f    *os.File
	head int64
}

// Start makes a file for this run in dir, says at its head that app runs,
// at version,
// and has the runtime write there as the program crashes, every
// goroutine's stack with it.
func Start(dir, app, version string, now time.Time) (*Run, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("making the crash folder: %w", err)
	}
	name := fmt.Sprintf("crash-%s-%d.txt", now.Format("20060102-150405"), os.Getpid())
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("making the crash file: %w", err)
	}
	head := fmt.Sprintf("%s %s · %s · %s/%s\nStarted %s, process %d\n%s",
		app, version, runtime.Version(), runtime.GOOS, runtime.GOARCH, now.Format("2006-01-02 15:04:05 -0700"), os.Getpid(), cut)
	if _, err := f.WriteString(head); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("writing the crash file: %w", err)
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("writing crashes to %s: %w", f.Name(), err)
	}
	// Every goroutine's stack, not the crashing one's alone: the bug is
	// often in what another was doing.
	debug.SetTraceback("all")
	return &Run{f: f, head: int64(len(head))}, nil
}

// Path is where the run's file is.
func (r *Run) Path() string { return r.f.Name() }

// Close ends the run as it ends with no crash: the runtime writes
// nowhere, and the file goes, unless something was written to it.
func (r *Run) Close() error {
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	path := r.f.Name()
	fi, statErr := r.f.Stat()
	if err := r.f.Close(); err != nil {
		return err
	}
	if statErr == nil && fi.Size() <= r.head {
		return os.Remove(path)
	}
	return nil
}

// Report is a crash a run wrote down.
type Report struct {
	// Path is the report's file, and At when its run crashed: when the
	// file was written last.
	Path string
	At   time.Time
	// Text is the whole file, and Summary its first line of what went
	// wrong, as "panic: runtime error: index out of range".
	Text    string
	Summary string
}

// Left are the crashes runs before wrote to dir and nobody has seen
// yet, the newest first, all but the file at self, this run's. The
// empty files of runs killed with no crash go, but a copy running
// keeps its own. The error tells of the reports that could not be read,
// and of empty files that failed to go, beside the reports read.
func Left(dir, self string) ([]Report, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// Windows reads a file where the folder goes as no folder at all.
		if !isFile(dir) {
			return nil, nil
		}
		err = errors.New("it is a file, not a folder")
	}
	if err != nil {
		return nil, fmt.Errorf("looking for crash reports in %s: %w. Make it a folder to stop this note", dir, err)
	}
	var out []Report
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "crash-") || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if self != "" && sameFile(path, self) {
			continue
		}
		b, err := readFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			// Another copy started at once moved it aside.
			continue
		}
		if err != nil {
			errs = append(errs, unreadReport(path, err))
			continue
		}
		text := string(b)
		_, after, _ := strings.Cut(text, cut)
		if strings.TrimSpace(after) == "" {
			if pid, ok := pidOf(e.Name()); ok && !running(pid) {
				// On Windows, the file of a copy running stays as it
				// is open: running says no to every process there.
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) && !heldOpen(err) {
					errs = append(errs, fmt.Errorf("removing the empty crash file %s: %w. Delete it or move it aside to stop this note", path, err))
				}
			}
			continue
		}
		fi, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, unreadReport(path, err))
			continue
		}
		out = append(out, Report{Path: path, At: fi.ModTime(), Text: text, Summary: summary(after)})
	}
	slices.SortFunc(out, func(a, b Report) int { return b.At.Compare(a.At) })
	return out, errors.Join(errs...)
}

// isFile reports whether a file that is no folder is at path.
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// readFile is os.ReadFile, and removeFile os.Remove as old reports seen
// go; a test makes them fail.
var (
	readFile   = os.ReadFile
	removeFile = os.Remove
)

// unreadReport is the error of the crash report at path, which failed to
// read with err.
func unreadReport(path string, err error) error {
	return fmt.Errorf("reading the crash report %s: %w. Delete it or move it aside to stop this note", path, err)
}

// Seen moves the reports to the folder seen, beside them, so the next
// start does not show them again, and lets the oldest there go. err
// tells of the reports that failed to move, which the next start tells
// of again; tidy of the old reports seen that failed to go, once the
// reports moved.
func Seen(reports []Report) (err, tidy error) {
	var errs, tidies []error
	var dirs []string
	for _, r := range reports {
		seen := filepath.Join(filepath.Dir(r.Path), "seen")
		if err := os.MkdirAll(seen, 0o755); err != nil {
			errs = append(errs, fmt.Errorf("%w. Make %s a folder to stop this note", err, seen))
			continue
		}
		// Another copy started at once may have moved it already.
		if err := os.Rename(r.Path, filepath.Join(seen, filepath.Base(r.Path))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		if !slices.Contains(dirs, seen) {
			dirs = append(dirs, seen)
		}
	}
	for _, seen := range dirs {
		if err := trim(seen); err != nil {
			tidies = append(tidies, err)
		}
	}
	return errors.Join(errs...), errors.Join(tidies...)
}

// trim lets all but the newest keepSeen reports in dir go.
func trim(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("an old one in %s couldn't be deleted: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "crash-") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keepSeen {
		return nil
	}
	// The names hold the time their run started, so sort oldest first.
	slices.Sort(names)
	var errs []error
	for _, n := range names[:len(names)-keepSeen] {
		if err := removeFile(filepath.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("an old one in %s couldn't be deleted: %w", dir, err))
		}
	}
	return errors.Join(errs...)
}

// pidOf is the process of the run whose file is named name, as
// crash-20261029-140203-1234.txt.
func pidOf(name string) (int, bool) {
	s := strings.TrimSuffix(name, ".txt")
	pid, err := strconv.Atoi(s[strings.LastIndexByte(s, '-')+1:])
	return pid, err == nil && pid > 0
}

// summary is the first line of what the runtime wrote that says what
// went wrong.
func summary(s string) string {
	for line := range strings.Lines(s) {
		line = strings.TrimSpace(line)
		for _, p := range []string{"panic:", "fatal error:", "Exception ", "unexpected fault", "SIG"} {
			if strings.HasPrefix(line, p) {
				return line
			}
		}
	}
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// sameFile reports whether the paths a and b name one file.
func sameFile(a, b string) bool {
	x, errA := os.Stat(a)
	y, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(x, y)
}
