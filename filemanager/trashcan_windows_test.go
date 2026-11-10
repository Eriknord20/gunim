//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

func TestTheRecycleBinTouchesNothingOutsideIt(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"plain.txt", "$Rlookalike.txt"} {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("stays"), 0o644); err != nil {
			t.Fatal(err)
		}
		it := TrashItem{ID: p, Name: name, Original: filepath.Join(root, "back", name)}
		if err := (recycleBin{}).PurgeItem(context.Background(), it); err == nil {
			t.Errorf("deleting %s, outside the Recycle Bin, worked", name)
		}
		if err := (recycleBin{}).RestoreItem(it); err == nil {
			t.Errorf("restoring %s, outside the Recycle Bin, worked", name)
		}
		if got := contents(t, p); got != "stays" {
			t.Fatalf("%s holds %q", name, got)
		}
	}
}

func TestTheRecycleBinsRecordsRead(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "$RABC123.txt")
	at := time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)
	ft := windows.NsecToFiletime(at.UnixNano())
	path := `C:\Users\me\notes.txt`
	units, err := windows.UTF16FromString(path)
	if err != nil {
		t.Fatal(err)
	}
	head := func(version uint64) []byte {
		b := binary.LittleEndian.AppendUint64(nil, version)
		b = binary.LittleEndian.AppendUint64(b, 42)
		b = binary.LittleEndian.AppendUint32(b, ft.LowDateTime)
		return binary.LittleEndian.AppendUint32(b, ft.HighDateTime)
	}
	v1 := head(1)
	for i := range 260 {
		var u uint16
		if i < len(units) {
			u = units[i]
		}
		v1 = binary.LittleEndian.AppendUint16(v1, u)
	}
	v2 := binary.LittleEndian.AppendUint32(head(2), uint32(len(units)))
	for _, u := range units {
		v2 = binary.LittleEndian.AppendUint16(v2, u)
	}
	for name, b := range map[string][]byte{"version 1": v1, "version 2": v2} {
		if err := os.WriteFile(recordOf(kept), b, 0o644); err != nil {
			t.Fatal(err)
		}
		from, deleted, err := readBinRecord(kept)
		if err != nil || from != path || !deleted.Equal(at) {
			t.Errorf("a %s record reads as %q, %v, %v", name, from, deleted, err)
		}
	}
	for name, b := range map[string][]byte{"short": v2[:20], "cut": v2[:30], "unknown": head(3)} {
		if err := os.WriteFile(recordOf(kept), b, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readBinRecord(kept); err == nil {
			t.Errorf("a %s record read", name)
		}
	}
}

// TestTheRecycleBinActsOnlyOnTheItemListed lists an item of the real
// Recycle Bin, changes its record as another deletion that took its name
// would, and checks the row listed before is refused. It runs only with
// GUNIM_FILES_RECYCLE_TEST=1.
func TestTheRecycleBinActsOnlyOnTheItemListed(t *testing.T) {
	if os.Getenv("GUNIM_FILES_RECYCLE_TEST") != "1" {
		t.Skip("set GUNIM_FILES_RECYCLE_TEST=1 to use the real Recycle Bin")
	}
	if n, err := binItems(); err != nil {
		t.Fatal(err)
	} else if n < 0 {
		t.Fatalf("the bin holds %d items", n)
	}
	root := t.TempDir()
	p := filepath.Join(root, "gunim-bin-listed.txt")
	if err := os.WriteFile(p, []byte("listed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (recycleBin{}).Trash(p); err != nil {
		t.Fatal(err)
	}
	listed, ok := binItemFrom(t, p)
	if !ok {
		t.Fatalf("the Recycle Bin does not list %s", p)
	}
	// What the shell counts, read as SHQUERYRBINFO is laid out, holds the
	// item just deleted.
	if n, err := binItems(); err != nil || n < 1 {
		t.Fatalf("the Recycle Bin counts %d items, %v, holding at least the one just deleted", n, err)
	}
	stale := listed
	stale.Deleted = stale.Deleted.Add(-time.Minute)
	if err := (recycleBin{}).PurgeItem(context.Background(), stale); err == nil {
		t.Fatal("deleting a row whose record says another time worked")
	}
	stale = listed
	stale.Original = filepath.Join(root, "elsewhere.txt")
	if err := (recycleBin{}).RestoreItem(stale); err == nil {
		t.Fatal("restoring a row whose record says another place worked")
	}
	if err := (recycleBin{}).PurgeItem(context.Background(), listed); err != nil {
		t.Fatal(err)
	}
	noRecord(t, listed)
}

func TestARestoreThatFailsTakesAwayTheFoldersItMade(t *testing.T) {
	root := t.TempDir()
	to := filepath.Join(root, "new", "deeper", "a.txt")
	if err := restoreKept(filepath.Join(root, "$RGONE.txt"), to); err == nil {
		t.Fatal("restoring an item that is not there worked")
	}
	if _, err := os.Lstat(filepath.Join(root, "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the folder made for the restore stayed: %v", err)
	}
}

func TestARestoreAcrossDrivesSaysWhereToRestoreIt(t *testing.T) {
	here, err := os.MkdirTemp(".", "across")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(here) })
	there := t.TempDir()
	abs, err := filepath.Abs(here)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.VolumeName(abs), filepath.VolumeName(there)) {
		t.Skip("the package and the temporary folder are on one drive")
	}
	kept := filepath.Join(abs, "$RX.txt")
	if err := os.WriteFile(kept, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(there, "made", "a.txt")
	err = restoreKept(kept, to)
	if err == nil || !strings.Contains(err.Error(), "File Explorer") {
		t.Fatalf("restoring across drives said %v", err)
	}
	if _, err := os.Lstat(kept); err != nil {
		t.Fatalf("the item left the bin: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(there, "made")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the folder made for the restore stayed: %v", err)
	}
}
