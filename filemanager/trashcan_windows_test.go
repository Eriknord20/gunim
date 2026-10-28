//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// binItemFrom finds the item that came from path in the real Recycle Bin.
func binItemFrom(t *testing.T, path string) (TrashItem, bool) {
	t.Helper()
	its, err := recycleBin{}.TrashItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range its {
		if SystemPaths.Same(it.Original, path) {
			return it, true
		}
	}
	return TrashItem{}, false
}

// TestTheRecycleBinListsRestoresAndPurges trashes files made for the test
// to the real Recycle Bin, finds them in its list, restores one and
// deletes the other for good. It runs only with
// GUNIM_FILES_RECYCLE_TEST=1, and never empties the bin.
func TestTheRecycleBinListsRestoresAndPurges(t *testing.T) {
	if os.Getenv("GUNIM_FILES_RECYCLE_TEST") != "1" {
		t.Skip("set GUNIM_FILES_RECYCLE_TEST=1 to use the real Recycle Bin")
	}
	root := t.TempDir()
	back, gone := filepath.Join(root, "gunim-bin-back.txt"), filepath.Join(root, "gunim-bin-gone.txt")
	for _, p := range []string{back, gone} {
		if err := os.WriteFile(p, []byte(filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := (recycleBin{}).Trash(p); err != nil {
			t.Fatal(err)
		}
	}
	it, ok := binItemFrom(t, back)
	if !ok {
		t.Fatalf("the Recycle Bin does not list %s", back)
	}
	if it.Name != "gunim-bin-back.txt" || it.Dir || it.Size != int64(len("gunim-bin-back.txt")) || it.Deleted.IsZero() {
		t.Fatalf("the Recycle Bin lists %+v", it)
	}
	if err := (recycleBin{}).RestoreItem(it); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, back); got != "gunim-bin-back.txt" {
		t.Fatalf("the restored file holds %q", got)
	}
	noRecord(t, it)
	it, ok = binItemFrom(t, gone)
	if !ok {
		t.Fatalf("the Recycle Bin does not list %s", gone)
	}
	if err := (recycleBin{}).PurgeItem(context.Background(), it); err != nil {
		t.Fatal(err)
	}
	if _, ok := binItemFrom(t, gone); ok {
		t.Fatal("the purged file is still in the Recycle Bin")
	}
	if _, err := os.Lstat(it.ID); !os.IsNotExist(err) {
		t.Fatalf("the bin still keeps %s: %v", it.ID, err)
	}
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Fatalf("the purged file came back: %v", err)
	}
	noRecord(t, it)
}

// noRecord fails t where the Recycle Bin's folder still keeps the record
// of it, the $I file beside its $R one.
func noRecord(t *testing.T, it TrashItem) {
	t.Helper()
	record := filepath.Join(filepath.Dir(it.ID), "$I"+filepath.Base(it.ID)[2:])
	if _, err := os.Lstat(record); !os.IsNotExist(err) {
		t.Fatalf("the bin still keeps the record %s: %v", record, err)
	}
}
