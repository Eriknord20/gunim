//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestTheRecycleBinGivesBackATempFile trashes a file made for the test to
// the real Recycle Bin, and takes it back out. It runs only with
// GUNIM_FILES_RECYCLE_TEST=1.
func TestTheRecycleBinGivesBackATempFile(t *testing.T) {
	if os.Getenv("GUNIM_FILES_RECYCLE_TEST") != "1" {
		t.Skip("set GUNIM_FILES_RECYCLE_TEST=1 to use the real Recycle Bin")
	}
	root := t.TempDir()
	path := filepath.Join(root, "gunim-recycle-test.txt")
	if err := os.WriteFile(path, []byte("come back"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := recycleBin{}
	rec, err := runJob(context.Background(), job{kind: OpTrash, srcs: []string{path}}, env{fs: LocalFS(), trash: tr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("the file is still there after the trash: %v", err)
	}
	if !restorable(rec) {
		t.Fatal("the record of the trash says it cannot be restored")
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, env{fs: LocalFS(), trash: tr}); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, path); got != "come back" {
		t.Fatalf("after the restore the file holds %q", got)
	}
}

// TestTheRecycleBinGivesBackTwoFilesOfOneStem trashes a.txt and a.log
// together, which Explorer shows alike with extensions hidden, and
// takes them back out, each with its own contents. It runs only with
// GUNIM_FILES_RECYCLE_TEST=1.
func TestTheRecycleBinGivesBackTwoFilesOfOneStem(t *testing.T) {
	if os.Getenv("GUNIM_FILES_RECYCLE_TEST") != "1" {
		t.Skip("set GUNIM_FILES_RECYCLE_TEST=1 to use the real Recycle Bin")
	}
	root := t.TempDir()
	txt, log := filepath.Join(root, "gunim-recycle-a.txt"), filepath.Join(root, "gunim-recycle-a.log")
	for p, s := range map[string]string{txt: "the text", log: "the log"} {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr := recycleBin{}
	rec, err := runJob(context.Background(), job{kind: OpTrash, srcs: []string{txt, log}}, env{fs: LocalFS(), trash: tr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, env{fs: LocalFS(), trash: tr}); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, txt); got != "the text" {
		t.Fatalf("a.txt came back holding %q", got)
	}
	if got := contents(t, log); got != "the log" {
		t.Fatalf("a.log came back holding %q", got)
	}
}

// TestTheRecycleBinTakesAFileBeingRead trashes a file to the real Recycle
// Bin while the app has it open to read, and takes it back out. It runs
// only with GUNIM_FILES_RECYCLE_TEST=1.
func TestTheRecycleBinTakesAFileBeingRead(t *testing.T) {
	if os.Getenv("GUNIM_FILES_RECYCLE_TEST") != "1" {
		t.Skip("set GUNIM_FILES_RECYCLE_TEST=1 to use the real Recycle Bin")
	}
	path := filepath.Join(t.TempDir(), "gunim-recycle-open.txt")
	if err := os.WriteFile(path, []byte("come back"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LocalFS().Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tr := recycleBin{}
	rec, err := runJob(context.Background(), job{kind: OpTrash, srcs: []string{path}}, env{fs: LocalFS(), trash: tr})
	if cerr := f.Close(); cerr != nil {
		t.Error(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("the file is still there after the trash: %v", err)
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, env{fs: LocalFS(), trash: tr}); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, path); got != "come back" {
		t.Fatalf("after the restore the file holds %q", got)
	}
}
