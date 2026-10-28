package filemanager

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeZip writes a zip at path holding files, by their names in it,
// each deflated, or stored where its name ends in .bin.
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)}
		if strings.HasSuffix(name, ".bin") {
			h.Method = zip.Store
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirNames returns the names ReadDir gives for dir on fsys, sorted.
func dirNames(t *testing.T, fsys FS, dir string) []string {
	t.Helper()
	ds, err := fsys.ReadDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		name := d.Name()
		if d.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// readAll reads the file at p on fsys.
func readAll(t *testing.T, fsys FS, p string) string {
	t.Helper()
	f, err := fsys.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An archive lists as a folder of what it holds, the folders its names
// imply among them, while the archive itself stays a file.
func TestAnArchiveListsAsAFolder(t *testing.T) {
	root := t.TempDir()
	zp := filepath.Join(root, "a.zip")
	writeZip(t, zp, map[string]string{"top.txt": "top", "sub/deep/x.txt": "x", "sub/y.bin": "stored", "../out.txt": "kept in"})
	fsys := withArchives(LocalFS(), &zipKeys{})
	if got, want := dirNames(t, fsys, zp), []string{"out.txt", "sub/", "top.txt"}; !slices.Equal(got, want) {
		t.Fatalf("the zip lists %v, want %v", got, want)
	}
	if got, want := dirNames(t, fsys, filepath.Join(zp, "sub")), []string{"deep/", "y.bin"}; !slices.Equal(got, want) {
		t.Fatalf("sub lists %v, want %v", got, want)
	}
	info, err := fsys.Stat(zp)
	if err != nil || info.IsDir() {
		t.Fatalf("the zip itself is %v, %v; want a file", info, err)
	}
	info, err = fsys.Stat(filepath.Join(zp, "sub", "deep", "x.txt"))
	if err != nil || info.IsDir() || info.Size() != 1 || !info.ModTime().Equal(time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)) {
		t.Fatalf("x.txt is %v, %v", info, err)
	}
	if _, err := fsys.Stat(filepath.Join(zp, "nothing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a name the zip does not hold is %v", err)
	}
	if got := readAll(t, fsys, filepath.Join(zp, "sub", "deep", "x.txt")); got != "x" {
		t.Fatalf("x.txt holds %q", got)
	}
	if got := readAll(t, fsys, filepath.Join(zp, "sub", "y.bin")); got != "stored" {
		t.Fatalf("y.bin holds %q", got)
	}
}

// A folder named as an archive is, is a folder.
func TestAFolderNamedLikeAZipIsAFolder(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "odd.zip/in.txt")
	fsys := withArchives(LocalFS(), &zipKeys{})
	if got := dirNames(t, fsys, filepath.Join(root, "odd.zip")); !slices.Equal(got, []string{"in.txt"}) {
		t.Fatalf("the folder lists %v", got)
	}
	if got := readAll(t, fsys, filepath.Join(root, "odd.zip", "in.txt")); got != "odd.zip/in.txt" {
		t.Fatalf("in.txt holds %q", got)
	}
}

// Nothing changes in an archive: each change fails with ErrInArchive,
// and the archive itself still can.
func TestAnArchiveIsReadOnly(t *testing.T) {
	root := t.TempDir()
	zp := filepath.Join(root, "a.zip")
	writeZip(t, zp, map[string]string{"x.txt": "x"})
	fsys := withArchives(LocalFS(), &zipKeys{})
	in := filepath.Join(zp, "x.txt")
	if _, err := fsys.Create(filepath.Join(zp, "new.txt")); !errors.Is(err, ErrInArchive) {
		t.Errorf("Create: %v", err)
	}
	for what, err := range map[string]error{
		"Mkdir":          fsys.Mkdir(filepath.Join(zp, "d"), 0o755),
		"Remove":         fsys.Remove(in),
		"Rename out":     fsys.Rename(in, filepath.Join(root, "x.txt")),
		"Rename in":      fsys.Rename(filepath.Join(root, "a.zip"), filepath.Join(zp, "b.zip")),
		"Chmod":          fsys.(Stamper).Chmod(in, 0o600),
		"Trash":          func() error { _, err := fsys.(Trasher).Trash(in); return err }(),
		"Symlink inside": fsys.(Linker).Symlink("x", filepath.Join(zp, "l")),
	} {
		if !errors.Is(err, ErrInArchive) {
			t.Errorf("%s: %v, want ErrInArchive", what, err)
		}
	}
	if err := fsys.Rename(zp, filepath.Join(root, "b.zip")); err != nil {
		t.Fatalf("renaming the zip itself: %v", err)
	}
	if got := dirNames(t, fsys, filepath.Join(root, "b.zip")); !slices.Equal(got, []string{"x.txt"}) {
		t.Fatalf("the zip renamed lists %v", got)
	}
}

// Optional only hands out what the file system under the archives has.
func TestOptionalAsksTheFileSystemUnder(t *testing.T) {
	fsys := withArchives(LocalFS(), &zipKeys{})
	if _, ok := optional[Trasher](fsys); ok != hasTrash(LocalFS()) {
		t.Fatalf("the trash is %v under the archives", ok)
	}
	if _, ok := optional[SystemOpener](fsys); ok {
		t.Fatal("the archives open in the system, where the computer's own files do not say they do")
	}
	if _, ok := optional[Linker](fsys); !ok {
		t.Fatal("the computer's own links went with the archives")
	}
}

func hasTrash(fsys FS) bool {
	_, ok := fsys.(Trasher)
	return ok
}

// A compressed file seeks, by reading on or from its start again.
func TestAFileInAnArchiveSeeks(t *testing.T) {
	root := t.TempDir()
	zp := filepath.Join(root, "a.zip")
	text := strings.Repeat("0123456789", 1000)
	writeZip(t, zp, map[string]string{"z.txt": text, "s.bin": text})
	fsys := withArchives(LocalFS(), &zipKeys{})
	for _, name := range []string{"z.txt", "s.bin"} {
		f, err := fsys.Open(filepath.Join(zp, name))
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 5)
		for _, at := range []int64{5003, 12, 9990} {
			if _, err := f.Seek(at, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(f, buf); err != nil || string(buf) != text[at:at+5] {
				t.Fatalf("%s at %d: %q, %v", name, at, buf, err)
			}
		}
		if end, err := f.Seek(0, io.SeekEnd); err != nil || end != int64(len(text)) {
			t.Fatalf("%s's end is %d, %v", name, end, err)
		}
		_ = f.Close()
	}
}

// countingFS counts the bytes read from its files.
type countingFS struct {
	FS
	n *atomic.Int64
}

func (c countingFS) Open(p string) (io.ReadSeekCloser, error) {
	f, err := c.FS.Open(p)
	if err != nil {
		return nil, err
	}
	return countingFile{f, c.n}, nil
}

type countingFile struct {
	io.ReadSeekCloser
	n *atomic.Int64
}

func (c countingFile) Read(p []byte) (int, error) {
	n, err := c.ReadSeekCloser.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// A large archive is read in place: listing it and reading a small file
// of it reads its index and that file, and not the rest.
func TestALargeArchiveIsNotReadWhole(t *testing.T) {
	root := t.TempDir()
	zp := filepath.Join(root, "big.zip")
	noise := make([]byte, 32<<20)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range noise {
		noise[i] = byte(r.Uint32())
	}
	writeZip(t, zp, map[string]string{"noise.bin": string(noise), "small.txt": "small"})
	var n atomic.Int64
	fsys := withArchives(countingFS{LocalFS(), &n}, &zipKeys{})
	if got := dirNames(t, fsys, zp); !slices.Equal(got, []string{"noise.bin", "small.txt"}) {
		t.Fatalf("the zip lists %v", got)
	}
	if got := readAll(t, fsys, filepath.Join(zp, "small.txt")); got != "small" {
		t.Fatalf("small.txt holds %q", got)
	}
	if got := n.Load(); got > 1<<20 {
		t.Fatalf("read %d bytes of a zip of %d to list it and read 5", got, len(noise))
	}
}

// An archive changed on disk is read again.
func TestAChangedArchiveIsReadAgain(t *testing.T) {
	root := t.TempDir()
	zp := filepath.Join(root, "a.zip")
	writeZip(t, zp, map[string]string{"one.txt": "1"})
	fsys := withArchives(LocalFS(), &zipKeys{})
	if got := dirNames(t, fsys, zp); !slices.Equal(got, []string{"one.txt"}) {
		t.Fatalf("the zip lists %v", got)
	}
	writeZip(t, zp, map[string]string{"one.txt": "1", "two.txt": "22"})
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(zp, later, later); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * heldFresh)
	for !slices.Equal(dirNames(t, fsys, zp), []string{"one.txt", "two.txt"}) {
		if time.Now().After(deadline) {
			t.Fatalf("the zip still lists %v", dirNames(t, fsys, zp))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A zip opens as a folder in the window; what it holds copies out, a
// drag that would move it copies, and nothing changes in it.
func TestAZipOpensInTheWindowAndCopiesOut(t *testing.T) {
	h := newHarness(t, "out/keep.txt")
	writeZip(t, filepath.Join(h.dir, "a.zip"), map[string]string{"in.txt": "inside", "sub/deep.txt": "deep"})
	got := notices(h)
	h.do(Command{Name: CmdRefresh})
	h.until("the zip shows", func() bool { return slices.Equal(h.shown(), []string{"out", "a.zip"}) })
	h.pick("a.zip")
	h.do(Activated{Gen: h.a.nav.gen, Row: slices.Index(h.shown(), "a.zip")})
	h.until("the zip opens", func() bool { return slices.Equal(h.shown(), []string{"sub", "in.txt"}) })
	h.until("the window knows it is in the zip", func() bool { return h.b.listing.readOnly == "a.zip" && h.b.shell.Fetches })

	h.pick("in.txt")
	h.do(Command{Name: CmdDelete})
	if len(h.a.ops.dialogs) != 0 {
		t.Fatal("Delete asked to delete what is in a zip")
	}
	h.until("the window says why", func() bool {
		return slices.ContainsFunc(got(), func(n Notice) bool { return n.Title == "a.zip can only be read here" })
	})

	h.do(Command{Name: CmdCopy})
	h.do(Navigate{Path: filepath.Join(h.dir, "out")})
	h.until("out opens", func() bool { return slices.Equal(h.shown(), []string{"keep.txt"}) })
	if h.b.listing.readOnly != "" {
		t.Fatal("out of the zip, the folder is still read only")
	}
	h.do(Command{Name: CmdPaste})
	h.until("the copy lands", func() bool { return h.exists("out/in.txt") })
	if got := contents(t, filepath.Join(h.dir, "out", "in.txt")); got != "inside" {
		t.Fatalf("the copy holds %q", got)
	}

	h.do(DropFiles{Paths: []string{filepath.Join(h.dir, "a.zip", "sub")}, Into: filepath.Join(h.dir, "out"), FS: "", To: ""})
	h.until("the folder lands", func() bool { return h.exists("out/sub/deep.txt") })
	if got := dirNames(t, h.a.fs, filepath.Join(h.dir, "a.zip")); !slices.Equal(got, []string{"in.txt", "sub/"}) {
		t.Fatalf("a drag out moved from the zip, which holds %v", got)
	}

	h.do(DropFiles{Paths: []string{filepath.Join(h.dir, "out", "keep.txt")}, Into: filepath.Join(h.dir, "a.zip"), Copy: true})
	h.frames(10)
	if _, err := h.a.fs.Stat(filepath.Join(h.dir, "a.zip", "keep.txt")); err == nil {
		t.Fatal("a drop went into the zip")
	}
}

// Going into a zip a password protects asks for it, again while it is
// wrong, and once right its files read.
func TestAProtectedZipAsksForItsPassword(t *testing.T) {
	for _, zipped := range []string{"legacy.zip", "aes256.zip"} {
		t.Run(zipped, func(t *testing.T) {
			h := newHarness(t, "out/")
			copyTestZip(t, zipped, h.dir)
			h.do(Command{Name: CmdRefresh})
			h.until("the zip shows", func() bool { return slices.Contains(h.shown(), "secret.zip") })
			h.do(Activated{Gen: h.a.nav.gen, Row: slices.Index(h.shown(), "secret.zip")})
			p := h.passwordShown()
			h.until("its names show before the password", func() bool { return slices.Equal(h.shown(), []string{"fox.txt", "tiny.txt"}) })
			if _, err := h.a.fs.Open(filepath.Join(h.dir, "secret.zip", "tiny.txt")); err == nil {
				t.Fatal("a protected file opened before the password was given")
			}
			h.answer(PasswordGiven{Token: p.Token, Text: "wrong", OK: true})
			again := h.passwordShown()
			if again.Token == p.Token || again.Problem == "" {
				t.Fatalf("a wrong password was asked about as %+v", again)
			}
			h.answer(PasswordGiven{Token: again.Token, Text: "hunter2", OK: true})
			h.until("the password is kept", func() bool { return len(h.a.ops.dialogs) == 0 })
			h.until("its files read", func() bool {
				f, err := h.a.fs.Open(filepath.Join(h.dir, "secret.zip", "tiny.txt"))
				if err != nil {
					h.pump()
					return false
				}
				defer func() { _ = f.Close() }()
				b, err := io.ReadAll(f)
				return err == nil && string(b) == "tiny"
			})
			h.pick("fox.txt")
			h.do(Command{Name: CmdCopy})
			h.do(Navigate{Path: filepath.Join(h.dir, "out")})
			h.until("out opens", func() bool { return h.a.nav.archive == "" && !h.a.nav.loading })
			h.do(Command{Name: CmdPaste})
			h.until("the copy lands", func() bool { return h.exists("out/fox.txt") })
			if got := contents(t, filepath.Join(h.dir, "out", "fox.txt")); !strings.HasPrefix(got, "The quick brown fox") {
				t.Fatalf("the copy holds %q", got)
			}
		})
	}
}

// What an archive holds goes to another file system through the program
// with the archive to read it from, and is copied, not moved.
func TestATransferOutOfAZipCarriesTheZip(t *testing.T) {
	ho := newHolder()
	h := newHarnessWith(t, func(o *Options) { o.Transfer = ho.transfer })
	zp := filepath.Join(h.dir, "a.zip")
	writeZip(t, zp, map[string]string{"in.txt": "inside"})
	in := filepath.Join(zp, "in.txt")
	h.a.transfer("", SystemPaths, []string{in}, "server", "/srv", true, nil)
	c := ho.next(h)
	if c.t.From == nil || c.t.Move {
		t.Fatalf("the transfer is %+v, want one from the zip that copies", c.t)
	}
	if got := readAll(t, c.t.From, in); got != "inside" {
		t.Fatalf("read %q through the transfer's file system", got)
	}
	c.reply <- nil

	h.a.transfer("", SystemPaths, []string{filepath.Join(h.dir, "a.zip")}, "server", "/srv", true, nil)
	if c := ho.next(h); c.t.From != nil || !c.t.Move {
		t.Fatalf("the zip itself went as %+v", c.t)
	} else {
		c.reply <- nil
	}
}

// openCounter counts the files of its file system open.
type openCounter struct {
	FS
	open *atomic.Int64
}

func (c openCounter) Open(p string) (io.ReadSeekCloser, error) {
	f, err := c.FS.Open(p)
	if err != nil {
		return nil, err
	}
	c.open.Add(1)
	return &countedFile{ReadSeekCloser: f, open: c.open}, nil
}

type countedFile struct {
	io.ReadSeekCloser
	open *atomic.Int64
}

func (c *countedFile) Close() error {
	c.open.Add(-1)
	return c.ReadSeekCloser.Close()
}

// An archive's file is open only while a file of it is read, so on
// Windows the archive can still be deleted or replaced while a window
// shows what it holds.
func TestAnArchivesFileIsClosedWhenNothingReadsIt(t *testing.T) {
	root := t.TempDir()
	zp := filepath.Join(root, "a.zip")
	writeZip(t, zp, map[string]string{"x.txt": "x", "s.bin": "stored"})
	var open atomic.Int64
	fsys := withArchives(openCounter{LocalFS(), &open}, &zipKeys{})
	dirNames(t, fsys, zp)
	if _, err := fsys.Stat(filepath.Join(zp, "x.txt")); err != nil {
		t.Fatal(err)
	}
	if n := open.Load(); n != 0 {
		t.Fatalf("listed, %d of its files stay open", n)
	}
	for _, name := range []string{"x.txt", "s.bin"} {
		f, err := fsys.Open(filepath.Join(zp, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(f); err != nil {
			t.Fatal(err)
		}
		if n := open.Load(); n != 1 {
			t.Fatalf("reading %s, %d files are open", name, n)
		}
		_ = f.Close()
		if n := open.Load(); n != 0 {
			t.Fatalf("%s closed, %d files stay open", name, n)
		}
	}
}
