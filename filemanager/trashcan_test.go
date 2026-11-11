package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// trashItems lists tr, sorted by name.
func trashItems(t *testing.T, tr TrashCan) []TrashItem {
	t.Helper()
	its, err := tr.TrashItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(its, func(a, b TrashItem) int { return strings.Compare(a.Name, b.Name) })
	return its
}

func TestTheTrashListsWhatItHolds(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "work/a.txt", "work/d/e.txt", "other/a.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	before := time.Now().Add(-time.Second)
	for _, p := range []string{"work/a.txt", "work/d", "other/a.txt"} {
		if _, err := tr.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	its := trashItems(t, tr)
	if len(its) != 3 {
		t.Fatalf("the trash lists %+v", its)
	}
	origins := []string{its[0].Original, its[1].Original, its[2].Original}
	slices.Sort(origins)
	want := []string{at(root, "other/a.txt"), at(root, "work/a.txt"), at(root, "work/d")}
	slices.Sort(want)
	if !slices.Equal(origins, want) {
		t.Fatalf("the items came from %v, want %v", origins, want)
	}
	for _, it := range its {
		if it.Deleted.Before(before) || it.Deleted.After(time.Now().Add(time.Second)) {
			t.Errorf("%s was deleted at %v", it.Original, it.Deleted)
		}
		switch it.Name {
		case "a.txt":
			// Each file holds its own path, from root.
			rel, _ := filepath.Rel(root, it.Original)
			if it.Dir || it.Size != int64(len(rel)) {
				t.Errorf("%s reads as dir %v, size %d", it.Original, it.Dir, it.Size)
			}
		case "d":
			if !it.Dir || it.Size != -1 {
				t.Errorf("the folder reads as dir %v, size %d", it.Dir, it.Size)
			}
		}
	}
}

func TestATrashItemRestores(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "work/d/e.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	if _, err := tr.Trash(at(root, "work/d")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(at(root, "work")); err != nil {
		t.Fatal(err)
	}
	its := trashItems(t, tr)
	if len(its) != 1 {
		t.Fatalf("the trash lists %+v", its)
	}
	if err := tr.RestoreItem(its[0]); err != nil {
		t.Fatal(err)
	}
	if contents(t, at(root, "work/d/e.txt")) != "work/d/e.txt" {
		t.Fatal("the folder did not come back, with its folder made again")
	}
	if its := trashItems(t, tr); len(its) != 0 {
		t.Fatalf("the trash still lists %+v", its)
	}
}

func TestATrashItemDoesNotRestoreOverANewOne(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	if _, err := tr.Trash(at(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at(root, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	its := trashItems(t, tr)
	if err := tr.RestoreItem(its[0]); err == nil || !strings.Contains(err.Error(), "exists again") {
		t.Fatalf("restoring over a new file returned %v", err)
	}
	if contents(t, at(root, "a.txt")) != "new" || len(trashItems(t, tr)) != 1 {
		t.Fatal("the refused restore changed a file")
	}
}

func TestATrashItemPurgesWithItsRecord(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/e.txt", "f.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	for _, p := range []string{"d", "f.txt"} {
		if _, err := tr.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	its := trashItems(t, tr)
	if err := tr.PurgeItem(context.Background(), its[0]); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(root, "Trash", "files")); !slices.Equal(got, []string{"f.txt"}) {
		t.Fatalf("the trash keeps %v", got)
	}
	if got := names(t, filepath.Join(root, "Trash", "info")); !slices.Equal(got, []string{"f.txt.trashinfo"}) {
		t.Fatalf("the trash keeps the records %v", got)
	}
}

func TestPurgeTouchesNothingOutsideTheTrash(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "keep.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	err := tr.PurgeItem(context.Background(), TrashItem{ID: at(root, "keep.txt"), Name: "keep.txt"})
	if err == nil {
		t.Fatal("purging an item outside the trash worked")
	}
	if contents(t, at(root, "keep.txt")) != "keep.txt" {
		t.Fatal("the purge deleted a file outside the trash")
	}
}

func TestTheTrashEmpties(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/e.txt", "f.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	for _, p := range []string{"d", "f.txt"} {
		if _, err := tr.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.EmptyTrash(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"files", "info"} {
		if got := names(t, filepath.Join(root, "Trash", sub)); len(got) != 0 {
			t.Fatalf("the trash's %s keeps %v", sub, got)
		}
	}
	if err := (xdgTrash{dir: filepath.Join(root, "None")}).EmptyTrash(context.Background()); err != nil {
		t.Fatalf("emptying a trash never made returned %v", err)
	}
	if its := trashItems(t, xdgTrash{dir: filepath.Join(root, "None")}); len(its) != 0 {
		t.Fatalf("a trash never made lists %+v", its)
	}
}

func TestATrashOnAFileSystemOfSlashPathsWorksAsTheComputersDoes(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "home/me/work/a b.txt", "home/me/d/e.txt")
	fsys := slashFS{root: root}
	tr := XDGTrash(fsys, "/home/me/.local/share/Trash")
	to, err := tr.Trash("/home/me/work/a b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if to != "/home/me/.local/share/Trash/files/a b.txt" {
		t.Fatalf("the item went to %s", to)
	}
	info := contents(t, at(root, "home/me/.local/share/Trash/info/a b.txt.trashinfo"))
	if !strings.Contains(info, "Path=/home/me/work/a%20b.txt\n") {
		t.Fatalf("the record reads %q", info)
	}
	if _, err := tr.Trash("/home/me/d"); err != nil {
		t.Fatal(err)
	}
	its := trashItems(t, tr)
	if len(its) != 2 || its[0].Original != "/home/me/work/a b.txt" || its[1].Original != "/home/me/d" || !its[1].Dir {
		t.Fatalf("the trash lists %+v", its)
	}
	if err := tr.RestoreItem(its[0]); err != nil {
		t.Fatal(err)
	}
	if contents(t, at(root, "home/me/work/a b.txt")) != "home/me/work/a b.txt" {
		t.Fatal("the file did not come back")
	}
	if err := tr.PurgeItem(context.Background(), its[1]); err != nil {
		t.Fatal(err)
	}
	if its := trashItems(t, tr); len(its) != 0 {
		t.Fatalf("the trash still lists %+v", its)
	}
}

func TestATrashThatCannotBeReadSaysSo(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "b.txt")
	local := xdgTrash{dir: filepath.Join(root, "Trash")}
	for _, p := range []string{"a.txt", "b.txt"} {
		if _, err := local.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	tr := xdgTrash{fs: lostFS{FS: LocalFS(), lost: "b.txt.trashinfo"}, dir: filepath.Join(root, "Trash")}
	if its, err := tr.TrashItems(context.Background()); err == nil {
		t.Fatalf("a trash read part of the way listed %+v", its)
	}
	// A record whose item went, and one that is no record, pass by.
	if err := os.Remove(filepath.Join(root, "Trash", "files", "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Trash", "info", "c.txt.trashinfo"), []byte("[Trash Info]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree(t, filepath.Join(root, "Trash", "files"), "c.txt")
	if got := trashItems(t, local); len(got) != 1 || got[0].Name != "b.txt" {
		t.Fatalf("the trash lists %+v", got)
	}
}

// lostFS is a file system whose connection drops as the file named lost
// is opened.
type lostFS struct {
	FS
	lost string
}

func (l lostFS) Open(p string) (io.ReadSeekCloser, error) {
	if filepath.Base(p) == l.lost {
		return nil, errors.New("the connection ended")
	}
	return l.FS.Open(p)
}

// failingFS is a file system on another machine whose Mkdir fails with
// mkdir, and whose Rename of a file named far fails as one across volumes,
// as a server may say of any failure.
type failingFS struct {
	slashFS
	mkdir error
	far   string
}

func (f failingFS) ID() string { return "server" }

func (f failingFS) Mkdir(p string, perm fs.FileMode) error {
	if f.mkdir != nil {
		return f.mkdir
	}
	return f.slashFS.Mkdir(p, perm)
}

func (f failingFS) Rename(from, to string) error {
	if path.Base(from) == f.far {
		return fmt.Errorf("%w: failure", ErrCrossDevice)
	}
	return f.slashFS.Rename(from, to)
}

func TestOnlyACertainFailureOffersToDeleteForGood(t *testing.T) {
	for _, c := range []struct {
		name    string
		fsys    failingFS
		offered bool
	}{
		{"a home the trash cannot be made in", failingFS{mkdir: fs.ErrPermission}, true},
		{"a failure that may pass", failingFS{mkdir: errors.New("the connection ended")}, false},
		{"a server's rename that says it crossed volumes", failingFS{far: "a.txt"}, false},
	} {
		root := t.TempDir()
		tree(t, root, "home/a.txt")
		c.fsys.slashFS = slashFS{root: root}
		_, err := XDGTrash(c.fsys, "/home/.local/share/Trash").Trash("/home/a.txt")
		if err == nil {
			t.Fatalf("%s: the trash took the file", c.name)
		}
		if got := errors.Is(err, errNotTrashable); got != c.offered {
			t.Errorf("%s: %v, offered to delete for good %v, want %v", c.name, err, got, c.offered)
		}
		if contents(t, at(root, "home/a.txt")) != "home/a.txt" {
			t.Fatalf("%s: the file is gone", c.name)
		}
	}
}

// statCount is a file system that counts the Lstats made of it.
type statCount struct {
	FS
	n *atomic.Int32
}

func (s statCount) Lstat(p string) (fs.FileInfo, error) {
	s.n.Add(1)
	return s.FS.Lstat(p)
}

func TestTheTrashListsItsItemsInOneRead(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "b.txt", "c.txt")
	local := xdgTrash{dir: filepath.Join(root, "Trash")}
	for _, p := range []string{"a.txt", "b.txt", "c.txt"} {
		if _, err := local.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	var n atomic.Int32
	if its := trashItems(t, xdgTrash{fs: statCount{FS: LocalFS(), n: &n}, dir: local.dir}); len(its) != 3 {
		t.Fatalf("the trash lists %+v", its)
	}
	if n.Load() != 0 {
		t.Fatalf("listing the trash looked at its items one by one, %d times", n.Load())
	}
}

func TestTheTrashActsOnlyOnTheItemListed(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a/notes.txt", "b/notes.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	if _, err := tr.Trash(at(root, "a/notes.txt")); err != nil {
		t.Fatal(err)
	}
	listed := trashItems(t, tr)[0]
	// Another window restores it, and then deletes b's notes.txt, which
	// takes the name in the trash that a's had.
	if err := tr.RestoreItem(listed); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Trash(at(root, "b/notes.txt")); err != nil {
		t.Fatal(err)
	}
	if listed.ID != trashItems(t, tr)[0].ID {
		t.Fatal("the second notes.txt took another name in the trash; the test needs the same")
	}
	if err := tr.PurgeItem(context.Background(), listed); err == nil || !strings.Contains(err.Error(), "no longer holds") {
		t.Fatalf("deleting the row listed before deleted what took its name: %v", err)
	}
	if err := tr.RestoreItem(listed); err == nil {
		t.Fatal("restoring the row listed before restored what took its name")
	}
	if contents(t, listed.ID) != "b/notes.txt" {
		t.Fatal("b's notes.txt is gone from the trash")
	}
}

func TestARecordThatClimbsOutOfItsVolumeIsNoRecord(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "vol/.Trash-1000/files/x.txt")
	tr := xdgTrash{dir: filepath.Join(root, "vol", ".Trash-1000")}
	record := filepath.Join(tr.dir, "info", "x.txt.trashinfo")
	for _, path := range []string{"../../elsewhere/x.txt", ".."} {
		if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record, []byte("[Trash Info]\nPath="+path+"\nDeletionDate=2026-11-09T10:00:00\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if its := trashItems(t, tr); len(its) != 0 {
			t.Fatalf("a record of %q lists %+v", path, its)
		}
	}
	if err := os.WriteFile(record, []byte("[Trash Info]\nPath=docs/x.txt\nDeletionDate=2026-11-09T10:00:00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if its := trashItems(t, tr); len(its) != 1 || its[0].Original != filepath.Join(root, "vol", "docs", "x.txt") {
		t.Fatalf("a record of a path in its volume lists %+v", its)
	}
}

func TestARenameThatMustNotReplaceRefusesANameTaken(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "b.txt", "d/x.txt", "e/y.txt")
	r := LocalFS().(NoReplaceRenamer)
	if err := r.RenameNoReplace(at(root, "a.txt"), at(root, "b.txt")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a rename over a file made %v", err)
	}
	if err := r.RenameNoReplace(at(root, "d"), at(root, "e")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a rename of a folder over one made %v", err)
	}
	if contents(t, at(root, "a.txt")) != "a.txt" || contents(t, at(root, "b.txt")) != "b.txt" ||
		contents(t, at(root, "e/y.txt")) != "e/y.txt" || contents(t, at(root, "d/x.txt")) != "d/x.txt" {
		t.Fatal("a refused rename changed a file")
	}
	if err := r.RenameNoReplace(at(root, "a.txt"), at(root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	if err := r.RenameNoReplace(at(root, "d"), at(root, "f")); err != nil {
		t.Fatal(err)
	}
	if contents(t, at(root, "c.txt")) != "a.txt" || contents(t, at(root, "f/x.txt")) != "d/x.txt" {
		t.Fatal("a rename to a free name did not move the item")
	}
}

func TestTheTrashHasAPlaceOnlyWhereItOpens(t *testing.T) {
	if _, ok := TrashPlace(bareFS{LocalFS()}); ok {
		t.Error("a file system with no trash has a place for one")
	}
	if _, ok := TrashPlace(slashFS{root: t.TempDir()}); ok {
		t.Error("a file system with no trash has a place for one")
	}
	p, ok := TrashPlace(LocalFS())
	if _, err := systemTrashCan(); ok != (err == nil) {
		t.Fatalf("the computer's own trash has a place %v, though the system's trash says %v", ok, err)
	}
	if ok && (p.Path != TrashPath || p.Kind != "trash" || p.FS != "" || p.Name == "") {
		t.Fatalf("the computer's own trash has the place %+v", p)
	}
}

// deferringFS is a file system whose renames that must not replace move
// nothing and say it cannot, as one without such a rename does.
type deferringFS struct{ FS }

func (deferringFS) RenameNoReplace(from, to string) error {
	return fmt.Errorf("not here: %w", errors.ErrUnsupported)
}

func TestARestoreGoesOnWhereTheFileSystemCannotRefuseANameTaken(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "work/d/x.txt", "work/b.txt")
	tr := xdgTrash{fs: deferringFS{LocalFS()}, dir: filepath.Join(root, "Trash")}
	for _, p := range []string{"work/d", "work/b.txt"} {
		if _, err := tr.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	tree(t, root, "work/b.txt")
	for _, it := range trashItems(t, tr) {
		err := tr.RestoreItem(it)
		switch it.Name {
		case "d":
			if err != nil || contents(t, at(root, "work/d/x.txt")) != "work/d/x.txt" {
				t.Fatalf("the folder did not come back: %v", err)
			}
		case "b.txt":
			if err == nil || !strings.Contains(err.Error(), "exists again") {
				t.Fatalf("restoring over a new b.txt made %v", err)
			}
		}
	}
}

// meanwhileFS is a file system on which do happens once, as the first
// name is removed: what another window does while this one works.
type meanwhileFS struct {
	FS
	once *sync.Once
	do   func()
}

func (m meanwhileFS) Remove(p string) error {
	m.once.Do(m.do)
	return m.FS.Remove(p)
}

func TestEmptyingLeavesAnItemThatComesToTheTrashMeanwhile(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "b.txt", "new.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	for _, p := range []string{"a.txt", "b.txt"} {
		if _, err := tr.Trash(at(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	// An item with no record, as a trash another program left may hold.
	tree(t, filepath.Join(root, "Trash", "files"), "stray.txt")
	emptying := xdgTrash{dir: tr.dir, fs: meanwhileFS{FS: LocalFS(), once: new(sync.Once), do: func() {
		if _, err := tr.Trash(at(root, "new.txt")); err != nil {
			t.Error(err)
		}
	}}}
	if err := emptying.EmptyTrash(context.Background()); err != nil {
		t.Fatal(err)
	}
	its := trashItems(t, tr)
	if len(its) != 1 || its[0].Name != "new.txt" {
		t.Fatalf("after an empty that new.txt came to the trash during, the trash lists %+v", its)
	}
	if got := names(t, filepath.Join(root, "Trash", "files")); len(got) != 1 {
		t.Fatalf("the trash's files keep %v, want new.txt alone", got)
	}
	if got := names(t, filepath.Join(root, "Trash", "info")); len(got) != 1 {
		t.Fatalf("the trash's info keeps %v, want new.txt's record alone", got)
	}
}

// betweenFS is a file system on which do happens once, as the folder
// named at is listed and before the listing is read.
type betweenFS struct {
	FS
	at   string
	once *sync.Once
	do   func()
}

func (b betweenFS) ReadDir(ctx context.Context, p string) ([]fs.DirEntry, error) {
	if filepath.Base(p) == b.at {
		b.once.Do(b.do)
	}
	return b.FS.ReadDir(ctx, p)
}

func TestEmptyingLeavesAnItemThatComesBetweenItsTwoListings(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "new.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	if _, err := tr.Trash(at(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	emptying := xdgTrash{dir: tr.dir, fs: betweenFS{FS: LocalFS(), at: "files", once: new(sync.Once), do: func() {
		if _, err := tr.Trash(at(root, "new.txt")); err != nil {
			t.Error(err)
		}
	}}}
	if err := emptying.EmptyTrash(context.Background()); err != nil {
		t.Fatal(err)
	}
	if its := trashItems(t, tr); len(its) != 1 || its[0].Name != "new.txt" {
		t.Fatalf("after an empty that new.txt came between the listings of, the trash lists %+v", its)
	}
}

func TestEmptyingKeepsARecordWithNoItemAndTakesWhatIsNoRecord(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	if _, err := tr.Trash(at(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(root, "Trash", "info")
	// A record of an item on its way in, a folder named as a.txt's record
	// would be beside b.txt, and two things that are no records.
	tree(t, info, "coming.txt.trashinfo", "b.txt.trashinfo/x", "notes", ".trashinfo")
	tree(t, filepath.Join(root, "Trash", "files"), "b.txt")
	if err := tr.EmptyTrash(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := names(t, info); len(got) != 1 || got[0] != "coming.txt.trashinfo" {
		t.Fatalf("the trash's info keeps %v, want the record with no item alone", got)
	}
	if got := names(t, filepath.Join(root, "Trash", "files")); len(got) != 0 {
		t.Fatalf("the trash's files keep %v", got)
	}
}
