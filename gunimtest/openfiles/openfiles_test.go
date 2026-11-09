package openfiles

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnderListsAFileHeldOpenUntilItCloses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lists files on Linux alone")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "held")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if open := Under(dir); len(open) != 1 {
		t.Fatalf("with one file open, Under lists %v", open)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if open := Under(dir); len(open) != 0 {
		t.Fatalf("with the file closed, Under lists %v", open)
	}
}

func TestCheckPassesATestThatClosesItsFiles(t *testing.T) {
	dir := t.TempDir()
	Check(t, dir)
	Check(t, dir)
	f, err := os.Create(filepath.Join(dir, "held"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// fakeTB takes a test's cleanups and failures, for a test of what fails a test.
type fakeTB struct {
	testing.TB
	cleanups []func()
	failed   int
}

func (f *fakeTB) Helper()               { _ = f }
func (f *fakeTB) Cleanup(fn func())     { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Errorf(string, ...any) { f.failed++ }

// A file left open fails the test once, however often the folder was checked; closed in time, it fails nothing.
func TestCheckFailsATestThatEndsHoldingAFileOpen(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lists files on Linux alone")
	}
	dir := t.TempDir()
	fake := &fakeTB{}
	Check(fake, dir)
	Check(fake, dir)
	if len(fake.cleanups) != 1 {
		t.Fatalf("two Checks of a folder registered %d cleanups, want 1", len(fake.cleanups))
	}
	f, err := os.Create(filepath.Join(dir, "held"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fake.cleanups[0]()
	if fake.failed != 1 {
		t.Fatalf("a file left open failed the test %d times, want 1", fake.failed)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	closed := &fakeTB{}
	Check(closed, dir)
	closed.cleanups[0]()
	if closed.failed != 0 {
		t.Fatalf("with the file closed, the test failed %d times", closed.failed)
	}
}
