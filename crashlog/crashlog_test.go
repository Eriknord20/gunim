package crashlog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain crashes, as the child the test below starts asks.
func TestMain(m *testing.M) {
	if dir := os.Getenv("CRASHLOG_CRASH_IN"); dir != "" {
		if _, err := Start(dir, "My App", "test", time.Now()); err != nil {
			panic(err)
		}
		go func() { panic("the test's crash") }()
		select {}
	}
	os.Exit(m.Run())
}

func TestACrashIsLeftForTheNextStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "CRASHLOG_CRASH_IN="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("the child did not crash")
	}
	left, err := Left(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("left %d reports, want 1", len(left))
	}
	r := left[0]
	if r.Summary != "panic: the test's crash" {
		t.Errorf("summary %q, want the panic", r.Summary)
	}
	if !strings.Contains(r.Text, "My App test") || !strings.Contains(r.Text, "goroutine ") {
		t.Errorf("the report lacks its head or the stacks:\n%s", r.Text)
	}
	if time.Since(r.At) > time.Minute {
		t.Errorf("the run crashed at %v", r.At)
	}
	if err, tidy := Seen(left); err != nil || tidy != nil {
		t.Fatal(err, tidy)
	}
	if again, _ := Left(dir, ""); len(again) != 0 {
		t.Errorf("a report seen is left still: %v", again)
	}
	if _, err := os.Stat(filepath.Join(dir, "seen", filepath.Base(r.Path))); err != nil {
		t.Errorf("the report seen is not kept: %v", err)
	}
}

func TestARunEndingWellLeavesNothing(t *testing.T) {
	// Not parallel: the crash output is the process's.
	dir := t.TempDir()
	r, err := Start(dir, "My App", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := Left(dir, r.Path()); len(left) != 0 {
		t.Errorf("the run's own file is left: %v", left)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the folder holds %d files, want none", len(entries))
	}
}

func TestAnEmptyFileOfARunGoneGoesAndARunningOnesStays(t *testing.T) {
	t.Parallel()
	// A process that has ended, for a run killed with no crash.
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gone := filepath.Join(dir, fmt.Sprintf("crash-20261001-100000-%d.txt", child.Process.Pid))
	alive := filepath.Join(dir, fmt.Sprintf("crash-20261029-100000-%d.txt", os.Getpid()))
	for _, p := range []string{gone, alive} {
		if err := os.WriteFile(p, []byte("My App test\n"+cut), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := Left(dir, ""); len(left) != 0 {
		t.Errorf("empty files are taken for crashes: %v", left)
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("the empty file of a run gone stays")
	}
	// On Windows the file of a run going is kept by being open.
	if _, err := os.Stat(alive); err != nil && runtime.GOOS != "windows" {
		t.Error("the empty file of a run going went")
	}
}

// writeReport writes a crash report named name in dir.
func writeReport(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("My App test\n"+cut+"panic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A report that can't be read is told of, beside those that can, with
// what stops the note; one moved away meanwhile is passed over.
func TestAReportThatCantBeReadIsToldOf(t *testing.T) {
	// Not parallel: it makes the package's reads fail.
	dir := t.TempDir()
	good := writeReport(t, dir, "crash-20261029-100000-1.txt")
	bad := writeReport(t, dir, "crash-20261029-110000-2.txt")
	gone := writeReport(t, dir, "crash-20261029-120000-3.txt")
	readFile = func(path string) ([]byte, error) {
		switch path {
		case bad:
			return nil, errors.New("the disk failed")
		case gone:
			return nil, fs.ErrNotExist
		}
		return os.ReadFile(path)
	}
	defer func() { readFile = os.ReadFile }()
	left, err := Left(dir, "")
	if err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "Delete it or move it aside") {
		t.Fatalf("a report that can't be read: %v", err)
	}
	if strings.Contains(err.Error(), gone) {
		t.Errorf("a report moved away meanwhile is told of: %v", err)
	}
	if len(left) != 1 || left[0].Path != good {
		t.Errorf("the reports read are %v", left)
	}
}

// Reports seen whose folder can't be made say so, and how to stop it:
// they would be told of again at every start.
func TestReportsSeenThatCantBeMovedSaySo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	report := writeReport(t, dir, "crash-20261029-100000-1.txt")
	// A file where the folder seen goes.
	seen := filepath.Join(dir, "seen")
	if err := os.WriteFile(seen, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err, tidy := Seen([]Report{{Path: report}})
	if err == nil || !strings.Contains(err.Error(), "Make "+seen+" a folder") || tidy != nil {
		t.Fatalf("reports that can't be moved aside: %v, tidying %v", err, tidy)
	}
}

// Reports put aside whose oldest seen can't go say so apart: the
// reports themselves moved. The folder is tidied once for them all.
func TestOldReportsSeenThatCantGoSaySoApart(t *testing.T) {
	// Not parallel: it makes the package's removes fail.
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	if err := os.MkdirAll(seen, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range keepSeen {
		writeReport(t, seen, fmt.Sprintf("crash-20261001-%06d-1.txt", i))
	}
	reports := []Report{{Path: writeReport(t, dir, "crash-20261029-100000-1.txt")}, {Path: writeReport(t, dir, "crash-20261029-110000-2.txt")}}
	removes := 0
	removeFile = func(string) error { removes++; return errors.New("the disk failed") }
	defer func() { removeFile = os.Remove }()
	err, tidy := Seen(reports)
	if err != nil || tidy == nil || !strings.Contains(tidy.Error(), "an old one in "+seen+" couldn't be deleted") {
		t.Fatalf("old reports that can't go: %v, tidying %v", err, tidy)
	}
	if removes != 2 {
		t.Errorf("the folder seen was tidied with %d removes, want 2, once", removes)
	}
	for _, r := range reports {
		if _, err := os.Stat(filepath.Join(seen, filepath.Base(r.Path))); err != nil {
			t.Errorf("a report wasn't put aside: %v", err)
		}
	}
}
