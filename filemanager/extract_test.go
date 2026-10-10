package filemanager

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
)

// extract runs an extraction of the archive at src into a folder called
// name beside it, on the computer's own file system.
func extract(t *testing.T, src, name string) (record, error) {
	t.Helper()
	return runJob(context.Background(), job{kind: OpExtract, srcs: []string{src}, dest: filepath.Dir(src), name: name}, env{fs: LocalFS()})
}

// Extract knows the archives it opens by their names, in any case.
func TestArchivesAreKnownByTheirNames(t *testing.T) {
	for name, want := range map[string]string{
		"a.zip": ".zip", "A.ZIP": ".ZIP", "src.tar.gz": ".tar.gz", "src.tgz": ".tgz", "x.tar": ".tar",
		"x.tar.bz2": ".tar.bz2", "x.tbz2": ".tbz2", "notes.txt": "", ".zip": "", "zip": "", "a.gz": "",
	} {
		if got := archiveExt(name); got != want {
			t.Errorf("archiveExt(%q) = %q, want %q", name, got, want)
		}
	}
}

// A zip extracted gives back what it holds, in a folder of its own.
func TestAZipExtractsIntoAFolder(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "src/a.txt", "src/sub/b.txt", "src/sub/empty/")
	fsys := LocalFS()
	if err := ZipFiles(context.Background(), fsys, []string{at(root, "src/a.txt"), at(root, "src/sub")}, fsys, root, "x.zip", nil); err != nil {
		t.Fatal(err)
	}
	rec, err := extract(t, at(root, "x.zip"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := contents(t, at(root, "x/a.txt")); got != "src/a.txt" {
		t.Fatalf("a.txt holds %q", got)
	}
	if got := contents(t, at(root, "x/sub/b.txt")); got != "src/sub/b.txt" {
		t.Fatalf("sub/b.txt holds %q", got)
	}
	if info, err := os.Stat(at(root, "x/sub/empty")); err != nil || !info.IsDir() {
		t.Fatalf("the empty folder is %v, %v", info, err)
	}
	if len(rec.steps) != 1 || rec.steps[0].to != at(root, "x") || len(rec.landed) != 1 || rec.landed[0] != "x" {
		t.Fatalf("the record is %+v, want the folder made, to undo", rec)
	}
	if _, err := extract(t, at(root, "x.zip"), "x"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("extracted again into the same folder: %v", err)
	}
}

// A gzipped tar extracts its folders, files with their times, and its
// links: a hard one as a copy.
func TestATarExtractsWithItsLinks(t *testing.T) {
	root := t.TempDir()
	src := at(root, "src.tar.gz")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	mod := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	add := func(h tar.Header, body string) {
		h.Size = int64(len(body))
		h.ModTime = mod
		if werr := tw.WriteHeader(&h); werr != nil {
			t.Fatal(werr)
		}
		if _, werr := tw.Write([]byte(body)); werr != nil {
			t.Fatal(werr)
		}
	}
	add(tar.Header{Name: "src/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(tar.Header{Name: "src/main.go", Typeflag: tar.TypeReg, Mode: 0o644}, "package main")
	add(tar.Header{Name: "src/run.sh", Typeflag: tar.TypeReg, Mode: 0o755}, "echo hi")
	add(tar.Header{Name: "src/again.go", Typeflag: tar.TypeLink, Linkname: "src/main.go"}, "")
	add(tar.Header{Name: "src/latest", Typeflag: tar.TypeSymlink, Linkname: "main.go"}, "")
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = extract(t, src, "src"); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, at(root, "src/src/main.go")); got != "package main" {
		t.Fatalf("main.go holds %q", got)
	}
	if got := contents(t, at(root, "src/src/again.go")); got != "package main" {
		t.Fatalf("the hard link holds %q", got)
	}
	info, err := os.Stat(at(root, "src/src/run.sh"))
	if err != nil || !info.ModTime().Equal(mod) {
		t.Fatalf("run.sh is %v, %v, want its time from the archive", info, err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("run.sh has mode %v, want 0755", info.Mode().Perm())
		}
		if target, err := os.Readlink(at(root, "src/src/latest")); err != nil || target != "main.go" {
			t.Fatalf("the link points to %q, %v", target, err)
		}
	}
}

// An archive that would write outside the folder it is extracted into
// stops, and writes nothing there.
func TestAnArchiveLeadingOutOfItsFolderStops(t *testing.T) {
	root := t.TempDir()
	src := at(root, "evil.zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"ok.txt", "../evil.txt"} {
		w, cerr := zw.Create(name)
		if cerr != nil {
			t.Fatal(cerr)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_, err = extract(t, src, "evil")
	if err == nil || !strings.Contains(err.Error(), "leads out of") {
		t.Fatalf("extracting it said %v", err)
	}
	if got := contents(t, at(root, "evil.txt")); got != "missing" {
		t.Fatal("the archive wrote outside its folder")
	}
}

// Extract asks for the folder's name, the archive's without its end,
// and extracts it beside the archive, selected.
func TestExtractAsksAndExtracts(t *testing.T) {
	h := newHarness(t, "pics/a.png")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	fsys := LocalFS()
	if err := ZipFiles(context.Background(), fsys, []string{filepath.Join(h.dir, "pics")}, fsys, h.dir, "Pics.Backup.zip", nil); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdRefresh})
	h.until("the zip shows", func() bool { return len(h.shown()) == 2 })
	h.pick("Pics.Backup.zip")
	h.do(Command{Name: CmdExtract})
	h.until("the prompt shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, ok := h.a.ops.dialogs[0].state.(Prompt)
	if !ok || p.Text != "Pics.Backup" || p.OK != "Extract" {
		t.Fatalf("the prompt is %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(Prompted{Token: p.Token, Text: "restored", OK: true})
	h.until("the folder is made and selected", func() bool { return h.exists("restored") && h.a.nav.sel["restored"] })
	if got := contents(t, filepath.Join(h.dir, "restored", "pics", "a.png")); got != "pics/a.png" {
		t.Fatalf("the file came out holding %q", got)
	}
}

// The menu of an item offers Extract for an archive alone.
func TestTheMenuOffersExtractForAnArchive(t *testing.T) {
	h := newDndHarness(t, "a.txt", "b.tar.gz")
	labels := func(name string) []string {
		h.choose(name)
		h.script("menu:" + name)
		var items []string
		h.ui(func(b *browser, _ *gunim.UI) { items = labelsOf(b.listing.cur.menu.Items()) })
		return items
	}
	if items := labels("a.txt"); slices.Contains(items, "Extract…") {
		t.Fatalf("a text file's menu offers Extract: %v", items)
	}
	if items := labels("b.tar.gz"); !slices.Contains(items, "Extract…") {
		t.Fatalf("an archive's menu lacks Extract: %v", items)
	}
}
