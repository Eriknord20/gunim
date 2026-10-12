package filemanager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/widget"
)

// tree makes the files and folders in spec under root: a name ending in
// a slash is a folder, and any other gets its own name as its contents.
func tree(t *testing.T, root string, spec ...string) {
	t.Helper()
	for _, s := range spec {
		p := filepath.Join(root, filepath.FromSlash(s))
		if strings.HasSuffix(s, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// at returns the path rel, written with slashes, names under root.
func at(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// contents returns what the file at path holds, or "missing".
func contents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// names lists what dir holds, sorted.
func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// testEnv returns an env with a trash in root, which answers every clash
// with ans and counts the questions in asked.
func testEnv(root string, ans answer, asked *int) env {
	return env{
		fs:    LocalFS(),
		trash: xdgTrash{dir: filepath.Join(root, "Trash")},
		ask: func(context.Context, clash) (answer, error) {
			if asked != nil {
				*asked++
			}
			return ans, nil
		},
	}
}

func TestCopyCopiesFilesAndFoldersWithTheirTimes(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "src/a.txt", "src/dir/b.txt", "src/dir/deep/c.txt", "dst/")
	when := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(at(root, "src/a.txt"), when, when); err != nil {
		t.Fatal(err)
	}
	var last progress
	e := testEnv(root, answer{}, nil)
	e.report = func(p progress) { last = p }
	rec, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
		srcs: []string{at(root, "src/a.txt"), at(root, "src/dir")}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := contents(t, at(root, "dst/dir/deep/c.txt")); got != "src/dir/deep/c.txt" {
		t.Fatalf("the deep file holds %q", got)
	}
	info, err := os.Stat(at(root, "dst/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(when) {
		t.Fatalf("the copy was modified at %v, want %v", info.ModTime(), when)
	}
	if len(rec.steps) != 2 || rec.steps[1].to != at(root, "dst/dir") {
		t.Fatalf("the record holds %v, want the two items copied", rec.steps)
	}
	if last.items != last.itemsTotal || last.bytes != last.bytesTotal || last.items != 5 {
		t.Fatalf("the last progress was %+v, want all 5 items and every byte", last)
	}
	if got := contents(t, at(root, "src/a.txt")); got != "src/a.txt" {
		t.Fatal("the copy changed its source")
	}
}

func TestACopyIntoItsOwnFolderTakesAFreeName(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "a (2).txt")
	_, err := runJob(context.Background(), job{kind: OpCopy, dest: root, srcs: []string{filepath.Join(root, "a.txt")}},
		testEnv(root, answer{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := contents(t, filepath.Join(root, "a (3).txt")); got != "a.txt" {
		t.Fatalf("a (3).txt holds %q", got)
	}
}

func TestACopyAsksAboutEachClash(t *testing.T) {
	for _, c := range []struct {
		name  string
		ans   answer
		file  string
		holds string
		asked int
	}{
		{"replace", answer{choice: choiceReplace}, "dst/a.txt", "src/a.txt", 2},
		{"keep both", answer{choice: choiceKeepBoth}, "dst/a (2).txt", "src/a.txt", 2},
		{"skip", answer{choice: choiceSkip}, "dst/a.txt", "dst/a.txt", 2},
		{"replace all", answer{choice: choiceReplace, all: true}, "dst/b.txt", "src/b.txt", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			tree(t, root, "src/a.txt", "src/b.txt", "dst/a.txt", "dst/b.txt")
			asked := 0
			rec, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
				srcs: []string{at(root, "src/a.txt"), at(root, "src/b.txt")}},
				testEnv(root, c.ans, &asked))
			if err != nil {
				t.Fatal(err)
			}
			if got := contents(t, filepath.Join(root, c.file)); got != c.holds {
				t.Fatalf("%s holds %q, want %q", c.file, got, c.holds)
			}
			if asked != c.asked {
				t.Fatalf("asked %d times, want %d", asked, c.asked)
			}
			if c.ans.choice == choiceReplace && rec.replaced != 2 {
				t.Fatalf("the record counts %d files replaced, want 2", rec.replaced)
			}
			if c.ans.choice == choiceSkip && len(names(t, filepath.Join(root, "dst"))) != 2 {
				t.Fatal("a skipped clash left something new behind")
			}
		})
	}
}

func TestReplacingAFolderMergesItAndAsksInside(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "src/d/x.txt", "src/d/new.txt", "dst/d/x.txt", "dst/d/old.txt")
	var clashes []string
	e := testEnv(root, answer{}, nil)
	e.ask = func(_ context.Context, c clash) (answer, error) {
		clashes = append(clashes, filepath.Base(c.dst))
		return answer{choice: choiceReplace}, nil
	}
	rec, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
		srcs: []string{at(root, "src/d")}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(clashes, []string{"d", "x.txt"}) {
		t.Fatalf("asked about %v, want d and then x.txt", clashes)
	}
	if got := names(t, at(root, "dst/d")); !slices.Equal(got, []string{"new.txt", "old.txt", "x.txt"}) {
		t.Fatalf("the merged folder holds %v", got)
	}
	if contents(t, at(root, "dst/d/x.txt")) != "src/d/x.txt" {
		t.Fatal("the clashing file was not replaced")
	}
	if len(rec.steps) != 1 || filepath.Base(rec.steps[0].to) != "new.txt" {
		t.Fatalf("the record holds %v, want only the new file", rec.steps)
	}
}

func TestAFolderCannotGoInsideItself(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/in/", "d/..x/")
	for _, kind := range []OpKind{OpCopy, OpMove} {
		_, err := runJob(context.Background(), job{kind: kind, dest: at(root, "d/in"),
			srcs: []string{filepath.Join(root, "d")}}, testEnv(root, answer{}, nil))
		if err == nil || !strings.Contains(err.Error(), "inside itself") {
			t.Fatalf("kind %d: err = %v, want one about going inside itself", kind, err)
		}
		_, err = runJob(context.Background(), job{kind: kind, dest: at(root, "d/..x"),
			srcs: []string{filepath.Join(root, "d")}}, testEnv(root, answer{}, nil))
		if err == nil || !strings.Contains(err.Error(), "inside itself") {
			t.Fatalf("kind %d: into a folder called ..x, err = %v", kind, err)
		}
	}
}

// A link to a folder inside the folder is inside it too.
func TestAFolderCannotGoInsideItselfThroughALink(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/in/f.txt")
	if err := os.Symlink(at(root, "d/in"), at(root, "ln")); err != nil {
		t.Skip("this system makes no links:", err)
	}
	for _, kind := range []OpKind{OpCopy, OpMove} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := runJob(ctx, job{kind: kind, dest: at(root, "ln"), srcs: []string{at(root, "d")}},
			testEnv(root, answer{}, nil))
		cancel()
		if err == nil || !strings.Contains(err.Error(), "inside itself") {
			t.Fatalf("kind %d: err = %v, want one about going inside itself", kind, err)
		}
		if got := names(t, at(root, "d/in")); !slices.Equal(got, []string{"f.txt"}) {
			t.Fatalf("kind %d: the folder holds %v after the refusal", kind, got)
		}
	}
	// A link itself can go into the folder it leads to.
	if _, err := runJob(context.Background(), job{kind: OpCopy, dest: at(root, "d/in"), srcs: []string{at(root, "ln")}},
		testEnv(root, answer{}, nil)); err != nil {
		t.Fatal(err)
	}
}

// A copy stays out of the folder it makes, where the folder it copies
// holds the one it makes.
func TestACopyStaysOutOfTheFolderItMakes(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/in/f.txt")
	r := &runner{ctx: context.Background(), env: testEnv(root, answer{}, nil)}
	// Past the check of into, as a mount of one folder in another would
	// get.
	err := r.copyItem(at(root, "d"), at(root, "d/in/d"), false, false, true)
	if err == nil || !strings.Contains(err.Error(), "inside itself") {
		t.Fatalf("err = %v, want one about going inside itself", err)
	}
}

func TestACopyStopsAtTheFirstErrorAndSaysWhere(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "b.txt", "dst/")
	missing := filepath.Join(root, "a.txt")
	_, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
		srcs: []string{missing, filepath.Join(root, "b.txt")}}, testEnv(root, answer{}, nil))
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err = %v, want one naming %s", err, missing)
	}
	if got := names(t, filepath.Join(root, "dst")); len(got) != 0 {
		t.Fatalf("the copy went on past the error and made %v", got)
	}
}

func TestACancelledCopyLeavesNoPartFile(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "dst/")
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(big, make([]byte, 5*copyBuffer), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := testEnv(root, answer{}, nil)
	e.reportEvery = time.Nanosecond
	e.report = func(p progress) {
		if p.bytes > 0 {
			cancel()
		}
	}
	_, err := runJob(ctx, job{kind: OpCopy, dest: filepath.Join(root, "dst"), srcs: []string{big}}, e)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := names(t, filepath.Join(root, "dst")); len(got) != 0 {
		t.Fatalf("the cancelled copy left %v", got)
	}
}

// A file with a name near the longest a name can be copies too: its
// part file takes a short name of its own.
func TestAFileWithALongNameCopies(t *testing.T) {
	root := t.TempDir()
	name := strings.Repeat("n", 250) + ".txt"
	tree(t, root, "dst/", name)
	if _, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
		srcs: []string{filepath.Join(root, name)}}, testEnv(root, answer{}, nil)); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(root, "dst")); !slices.Equal(got, []string{name}) {
		t.Fatalf("the copy left %v", got)
	}
}

func TestMoveRenamesAndUndoMovesBack(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "d/b.txt", "dst/")
	srcs := []string{filepath.Join(root, "a.txt"), filepath.Join(root, "d")}
	e := testEnv(root, answer{}, nil)
	rec, err := runJob(context.Background(), job{kind: OpMove, dest: filepath.Join(root, "dst"), srcs: srcs}, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, root); !slices.Equal(got, []string{"dst"}) {
		t.Fatalf("after the move the folder holds %v", got)
	}
	if contents(t, at(root, "dst/d/b.txt")) != "d/b.txt" {
		t.Fatal("the moved folder lost its file")
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	if got := names(t, root); !slices.Equal(got, []string{"a.txt", "d", "dst"}) {
		t.Fatalf("after undo the folder holds %v", got)
	}
}

// A move that merges a folder into one of its name removes the folder it
// emptied; undo makes it again to put its items back in.
func TestUndoingAMergeMakesTheEmptiedFolderAgain(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/a.txt", "d/e/b.txt", "dst/d/c.txt", "dst/d/e/")
	e := testEnv(root, answer{choice: choiceReplace, all: true}, nil)
	rec, err := runJob(context.Background(), job{kind: OpMove, dest: filepath.Join(root, "dst"),
		srcs: []string{filepath.Join(root, "d")}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, root); !slices.Equal(got, []string{"dst"}) {
		t.Fatalf("after the merge the folder holds %v", got)
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	if contents(t, at(root, "d/a.txt")) != "d/a.txt" || contents(t, at(root, "d/e/b.txt")) != "d/e/b.txt" {
		t.Fatalf("after undo d holds %v", names(t, at(root, "d")))
	}
	if got := names(t, at(root, "dst/d")); !slices.Equal(got, []string{"c.txt", "e"}) {
		t.Fatalf("after undo dst/d holds %v", got)
	}
}

// An item skipped deep in a merge keeps each folder above it.
func TestAMergeKeepsTheFoldersOfASkippedItem(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/x/a.txt", "d/x/b.txt", "dst/d/x/a.txt")
	e := testEnv(root, answer{}, nil)
	e.ask = func(_ context.Context, c clash) (answer, error) {
		if filepath.Base(c.dst) == "a.txt" {
			return answer{choice: choiceSkip}, nil
		}
		return answer{choice: choiceReplace}, nil
	}
	_, err := runJob(context.Background(), job{kind: OpMove, dest: filepath.Join(root, "dst"),
		srcs: []string{filepath.Join(root, "d")}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if contents(t, at(root, "d/x/a.txt")) != "d/x/a.txt" || contents(t, at(root, "dst/d/x/b.txt")) != "d/x/b.txt" {
		t.Fatalf("after the merge d holds %v and dst/d/x holds %v", names(t, at(root, "d")), names(t, at(root, "dst/d/x")))
	}
}

func TestAMoveThatReplacesCountsIt(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "dst/a.txt")
	rec, err := runJob(context.Background(), job{kind: OpMove, dest: filepath.Join(root, "dst"),
		srcs: []string{filepath.Join(root, "a.txt")}}, testEnv(root, answer{choice: choiceReplace}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if contents(t, at(root, "dst/a.txt")) != "a.txt" || rec.replaced != 1 || len(rec.steps) != 0 {
		t.Fatalf("the move left %q with record %+v", contents(t, at(root, "dst/a.txt")), rec)
	}
}

func TestAMoveAcrossVolumesCopiesThenDeletes(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/a.txt", "d/e/b.txt", "dst/")
	r := &runner{ctx: context.Background(), env: testEnv(root, answer{}, nil)}
	if err := r.moveAcross(filepath.Join(root, "d"), at(root, "dst/d"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "d")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the source is still there: %v", err)
	}
	if contents(t, at(root, "dst/d/e/b.txt")) != "d/e/b.txt" {
		t.Fatal("the copy lost a file")
	}
}

func TestTrashFollowsTheFreedesktopLayoutAndUndoRestores(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "one/a.txt", "two/a.txt")
	trashDir := filepath.Join(root, "Trash")
	e := testEnv(root, answer{}, nil)
	first := at(root, "one/a.txt")
	rec, err := runJob(context.Background(), job{kind: OpTrash, srcs: []string{first, at(root, "two/a.txt")}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(trashDir, "files")); !slices.Equal(got, []string{"a (2).txt", "a.txt"}) {
		t.Fatalf("the trash holds %v, want a.txt and a (2).txt", got)
	}
	info := contents(t, filepath.Join(trashDir, "info", "a.txt.trashinfo"))
	lines := strings.Split(info, "\n")
	if lines[0] != "[Trash Info]" || !strings.HasPrefix(lines[1], "Path=") || !strings.HasPrefix(lines[2], "DeletionDate=") {
		t.Fatalf("the trash record reads %q", info)
	}
	if !strings.HasSuffix(lines[1], "/one/a.txt") {
		t.Fatalf("the record's path is %q, want the item's own", lines[1])
	}
	if _, err := time.Parse("2006-01-02T15:04:05", strings.TrimPrefix(lines[2], "DeletionDate=")); err != nil {
		t.Fatalf("the deletion date does not follow the spec: %v", err)
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	if contents(t, first) != "one/a.txt" || contents(t, at(root, "two/a.txt")) != "two/a.txt" {
		t.Fatal("undo did not put both files back")
	}
	if got := names(t, filepath.Join(trashDir, "info")); len(got) != 0 {
		t.Fatalf("undo left the records %v", got)
	}
}

func TestTheTrashPathIsEscaped(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a b%.txt")
	if _, err := (xdgTrash{dir: filepath.Join(root, "Trash")}).Trash(filepath.Join(root, "a b%.txt")); err != nil {
		t.Fatal(err)
	}
	info := contents(t, filepath.Join(root, "Trash", "info", "a b%.txt.trashinfo"))
	if !strings.Contains(info, "a%20b%25.txt") {
		t.Fatalf("the record reads %q, want the name escaped", info)
	}
}

func TestRestoreRefusesToOverwrite(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt")
	tr := xdgTrash{dir: filepath.Join(root, "Trash")}
	to, err := tr.Trash(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	tree(t, root, "a.txt")
	if err := tr.Restore(filepath.Join(root, "a.txt"), to, time.Now(), filepath.Join(root, "a.txt")); err == nil || !strings.Contains(err.Error(), "exists again") {
		t.Fatalf("restoring over a new file returned %v", err)
	}
	if contents(t, to) != "a.txt" {
		t.Fatal("the refused restore lost the trashed file")
	}
}

func TestDeleteRemovesATree(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "d/a.txt", "d/e/f/g.txt", "keep.txt")
	var last progress
	e := testEnv(root, answer{}, nil)
	e.report = func(p progress) { last = p }
	if _, err := runJob(context.Background(), job{kind: OpDelete, srcs: []string{filepath.Join(root, "d")}}, e); err != nil {
		t.Fatal(err)
	}
	if got := names(t, root); !slices.Equal(got, []string{"keep.txt"}) {
		t.Fatalf("after the delete the folder holds %v", got)
	}
	if last.items != 5 || last.itemsTotal != 5 {
		t.Fatalf("the last progress was %+v, want 5 of 5 items", last)
	}
}

func TestRenameChecksTheNameAndUndoes(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "b.txt")
	e := testEnv(root, answer{}, nil)
	src := filepath.Join(root, "a.txt")
	for _, bad := range []string{"", "..", "x" + string(filepath.Separator) + "y", "b.txt"} {
		if _, err := runJob(context.Background(), job{kind: OpRename, srcs: []string{src}, name: bad}, e); err == nil {
			t.Fatalf("renaming to %q worked", bad)
		}
	}
	rec, err := runJob(context.Background(), job{kind: OpRename, srcs: []string{src}, name: "c.txt"}, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, root); !slices.Equal(got, []string{"b.txt", "c.txt"}) {
		t.Fatalf("after the rename the folder holds %v", got)
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	if got := names(t, root); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Fatalf("after undo the folder holds %v", got)
	}
}

// On a file system that ignores case, a rename that changed only the
// case finds the item at its old name too. A hard link stands in for
// that here, as the test's file system tells case apart.
func TestUndoingARenameOfTheCaseOnly(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "Report.txt", "Other.txt")
	if err := os.Link(at(root, "Report.txt"), at(root, "report.txt")); err != nil {
		t.Skip("this system makes no hard links:", err)
	}
	e := testEnv(root, answer{}, nil)
	rec := record{kind: OpRename, steps: []step{{from: at(root, "report.txt"), to: at(root, "Report.txt")}}}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	// Another file at the old name still stops the undo.
	if err := os.Rename(at(root, "Other.txt"), at(root, "OTHER.txt")); err != nil {
		t.Fatal(err)
	}
	tree(t, root, "Other.txt")
	rec = record{kind: OpRename, steps: []step{{from: at(root, "Other.txt"), to: at(root, "OTHER.txt")}}}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err == nil {
		t.Fatal("undo moved a file onto another at its old name")
	}
}

func TestANewFolderAndItsUndo(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "dir/")
	e := testEnv(root, answer{}, nil)
	dir := filepath.Join(root, "dir")
	rec, err := runJob(context.Background(), job{kind: OpNewFolder, dest: dir, name: "New folder"}, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runJob(context.Background(), job{kind: OpNewFolder, dest: dir, name: "New folder"}, e); err == nil {
		t.Fatal("a second folder of the same name was made")
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("after undo the folder holds %v", got)
	}
	if got := names(t, filepath.Join(root, "Trash", "files")); !slices.Equal(got, []string{"New folder"}) {
		t.Fatalf("undo put %v in the trash, want the new folder", got)
	}
}

func TestUndoingACopyTrashesTheCopies(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "dst/")
	e := testEnv(root, answer{}, nil)
	rec, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
		srcs: []string{filepath.Join(root, "a.txt")}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, e); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(root, "dst")); len(got) != 0 {
		t.Fatalf("after undo the copy is still there: %v", got)
	}
	if contents(t, filepath.Join(root, "a.txt")) != "a.txt" {
		t.Fatal("undoing the copy touched the original")
	}
}

func TestUndoingATrashWithoutARecordSaysSo(t *testing.T) {
	root := t.TempDir()
	rec := record{kind: OpTrash, steps: []step{{from: filepath.Join(root, "a.txt")}}}
	_, err := runJob(context.Background(), job{kind: OpUndo, undo: &rec}, testEnv(root, answer{}, nil))
	if !errors.Is(err, errNoRestore) {
		t.Fatalf("err = %v, want errNoRestore", err)
	}
}

func TestAStoppedClashStopsTheOperation(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "b.txt", "dst/a.txt")
	e := testEnv(root, answer{}, nil)
	e.ask = func(context.Context, clash) (answer, error) { return answer{}, errStopped }
	_, err := runJob(context.Background(), job{kind: OpCopy, dest: filepath.Join(root, "dst"),
		srcs: []string{filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")}}, e)
	if !errors.Is(err, errStopped) {
		t.Fatalf("err = %v, want errStopped", err)
	}
	if got := names(t, filepath.Join(root, "dst")); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("the stopped copy went on and made %v", got)
	}
}

func TestNumberedPutsTheNumberBeforeTheExtension(t *testing.T) {
	for in, want := range map[string]string{"a.txt": "a (2).txt", "archive": "archive (2)", ".bashrc": ".bashrc (2)"} {
		if got := numbered(in, 2); got != want {
			t.Fatalf("numbered(%q) = %q, want %q", in, got, want)
		}
	}
}

// An item is said as the replace dialog compares two: its size, or that
// it is a folder, and when it was changed.
func TestItemFactsSayASizeOrAFolderAndWhen(t *testing.T) {
	at := time.Date(2026, 10, 10, 15, 15, 0, 0, time.UTC)
	for _, c := range []struct {
		size int64
		dir  bool
		mod  time.Time
		want string
	}{
		{219, false, at, "219 bytes · 10 Oct 2026 15:15"},
		{1, false, time.Time{}, "1 byte"},
		{0, true, at, "Folder · 10 Oct 2026 15:15"},
	} {
		if got := ItemFacts(c.size, c.dir, c.mod); got != c.want {
			t.Errorf("ItemFacts(%d, %v) = %q, want %q", c.size, c.dir, got, c.want)
		}
	}
}

// The replace dialog asks as file managers do: Replace "name"?, where it
// is and what it is, the existing one and the new one, Apply to all,
// and Skip, Keep Both, Stop and Replace, in that order.
func TestTheReplaceDialogAsksAsFileManagersDo(t *testing.T) {
	d := newClashDialog(ClashAsk{Name: "run.cmd", Where: "windows", Old: "219 bytes · 11 Oct 2026 17:29", New: "219 bytes · 10 Oct 2026 15:15",
		SameKind: true, CanForAll: true})
	if d.Title != "Replace “run.cmd”?" {
		t.Fatalf("the dialog is titled %q", d.Title)
	}
	body, ok := d.Body.(*clashBody)
	if !ok {
		t.Fatalf("the dialog's body is a %T", d.Body)
	}
	var said []string
	for _, n := range body.Children() {
		switch v := n.(type) {
		case *widget.Label:
			said = append(said, v.Text)
		case *widget.Checkbox:
			said = append(said, v.Label)
		}
	}
	want := []string{"windows already has a file named “run.cmd”.", "Existing: 219 bytes · 11 Oct 2026 17:29",
		"New: 219 bytes · 10 Oct 2026 15:15", "Apply to all"}
	if !slices.Equal(said, want) {
		t.Fatalf("the dialog says %q, want %q", said, want)
	}
	var buttons []string
	for _, b := range d.Buttons() {
		if btn, ok := b.(*widget.Button); ok {
			buttons = append(buttons, btn.Label)
		}
	}
	if want := []string{"Skip", "Keep Both", "Stop", "Replace"}; !slices.Equal(buttons, want) {
		t.Fatalf("the buttons are %q, want %q", buttons, want)
	}
}
