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
