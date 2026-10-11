package filemanager

import (
	"archive/zip"
	"context"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"testing"
)

// zipped returns what the zip at path holds: each name, and a file's
// contents, a folder's as "/".
func zipped(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	got := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			got[f.Name] = "/"
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = string(b)
	}
	return got
}

// A zip holds the items under their own names, with all a folder holds,
// and leaves no part file behind.
func TestZipFilesWritesTheItemsAndAllTheyHold(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "src/a.txt", "src/sub/b.txt", "src/sub/empty/", "out/")
	src, out := at(root, "src"), at(root, "out")
	fsys := LocalFS()
	err := ZipFiles(context.Background(), fsys, []string{at(root, "src/a.txt"), at(root, "src/sub")}, fsys, out, "x.zip", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := zipped(t, filepath.Join(out, "x.zip"))
	want := map[string]string{"a.txt": "src/a.txt", "sub/": "/", "sub/b.txt": "src/sub/b.txt", "sub/empty/": "/"}
	if !maps.Equal(got, want) {
		t.Fatalf("the zip holds %v, want %v", got, want)
	}
	if es, _ := filepath.Glob(filepath.Join(out, "*.part")); len(es) != 0 {
		t.Fatalf("a part file is left: %v", es)
	}
	// Not over a file that is there.
	err = ZipFiles(context.Background(), fsys, []string{src}, fsys, out, "x.zip", "", nil)
	if err == nil {
		t.Fatal("a zip was written over one that was there")
	}
	// A folder zipped into itself leaves its own zip out.
	if err := ZipFiles(context.Background(), fsys, []string{src}, fsys, src, "self.zip", "", nil); err != nil {
		t.Fatal(err)
	}
	names := slices.Sorted(maps.Keys(zipped(t, filepath.Join(src, "self.zip"))))
	if want := []string{"src/", "src/a.txt", "src/sub/", "src/sub/b.txt", "src/sub/empty/"}; !slices.Equal(names, want) {
		t.Fatalf("the zip of its own folder holds %v, want %v", names, want)
	}
}

// The name suggested is the one item's, or the folder's of several.
func TestAZipIsNamedForWhatItHolds(t *testing.T) {
	for _, c := range []struct {
		paths []string
		want  string
	}{
		{[]string{"/home/me/report.pdf"}, "report.zip"},
		{[]string{"/home/me/photos"}, "photos.zip"},
		{[]string{"/home/me/a.txt", "/home/me/b.txt"}, "me.zip"},
		{[]string{"/a.txt", "/b.txt"}, "Archive.zip"},
		{[]string{"/home/me/.bashrc"}, ".bashrc.zip"},
	} {
		if got := zipName(SlashPaths, c.paths); got != c.want {
			t.Errorf("zipName(%v) = %q, want %q", c.paths, got, c.want)
		}
	}
	if got := zipName(DrivePaths, []string{`D:\a.txt`, `D:\b.txt`}); got != "Archive.zip" {
		t.Errorf("a zip of a drive's items is called %q", got)
	}
	if withZipExt("x") != "x.zip" || withZipExt("x.ZIP") != "x.ZIP" {
		t.Error("a name typed without .zip does not get it")
	}
}

// Create zip asks for a name, the item's, and makes the zip beside it,
// selected; undo takes it to the trash.
func TestCreateZipAsksAndMakesIt(t *testing.T) {
	h := newHarness(t, "notes.txt", "pics/a.png")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	h.pick("notes.txt", "pics")
	h.do(Command{Name: CmdZip})
	h.until("the prompt shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, ok := h.a.ops.dialogs[0].state.(Prompt)
	if !ok || p.Text != "dir.zip" || p.Stem != 3 {
		t.Fatalf("the prompt is %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(Prompted{Token: p.Token, Text: "both", OK: true})
	h.until("the zip is made and selected", func() bool { return h.exists("both.zip") && h.a.nav.sel["both.zip"] })
	got := slices.Sorted(maps.Keys(zipped(t, filepath.Join(h.dir, "both.zip"))))
	if want := []string{"notes.txt", "pics/", "pics/a.png"}; !slices.Equal(got, want) {
		t.Fatalf("the zip holds %v, want %v", got, want)
	}
}

// Paste as zip of items on another file system hands the program one
// transfer, with the zip's name, and leaves the clipboard as it was.
func TestPasteAsZipFromAnotherFileSystemGoesToTheProgram(t *testing.T) {
	var ts transfers
	local, other := twoFileSystems(t, &ts)
	local.choose("a.txt")
	local.do(Command{Name: CmdCopy})
	other.until("the window elsewhere offers Paste", func() bool { return other.b.dnd.clip.Count == 1 })
	other.do(Command{Name: CmdPasteZip})
	other.until("the prompt shows", func() bool { return len(other.a.ops.dialogs) == 1 })
	p, ok := other.a.ops.dialogs[0].state.(Prompt)
	if !ok || p.Text != "a.zip" {
		t.Fatalf("the prompt is %+v", other.a.ops.dialogs[0].state)
	}
	other.answer(Prompted{Token: p.Token, Text: p.Text, OK: true})
	got := ts.count(other, 1)
	if want := (Transfer{FromFS: "", Paths: []string{filepath.Join(local.dir, "a.txt")}, ToFS: "elsewhere", Into: other.dir, Zip: "a.zip"}); !sameTransfer(got[0], want) || got[0].Zip != want.Zip {
		t.Fatalf("the paste hands over %+v, want %+v", got[0], want)
	}
	if other.b.dnd.clip.Count != 1 {
		t.Fatal("pasting as a zip emptied the clipboard")
	}
}
