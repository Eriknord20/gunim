package filemanager

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/marrasen/gunim/zipcrypt"
)

// The archives Extract opens, by what their names end with, longest
// first.
var archiveExts = []string{".tar.gz", ".tar.bz2", ".tgz", ".tbz2", ".tbz", ".zip", ".tar"}

// archiveExt returns the end of name that says it is an archive Extract
// opens, in the case it is written, or "" for none.
func archiveExt(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range archiveExts {
		if strings.HasSuffix(lower, ext) && len(name) > len(ext) {
			return name[len(name)-len(ext):]
		}
	}
	return ""
}

// isArchive reports whether the file called name is an archive Extract
// opens: a zip, or a tar, plain or compressed with gzip or bzip2.
func isArchive(name string) bool { return archiveExt(name) != "" }

// askExtract asks for the name of the folder to extract the archive at
// src into, beside it, and extracts it there.
func (a *app) askExtract(src string) {
	dest, base := a.ps.Split(src)
	free, err := freeName(a.fs, a.ps.Join(dest, strings.TrimSuffix(base, archiveExt(base))))
	if err != nil {
		a.fail("Looking for a free name: " + err.Error())
		return
	}
	name := a.ps.Base(free)
	a.prompt(Prompt{Title: "Extract", Text: name, OK: "Extract", Stem: utf8.RuneCountInString(name)}, func(name string) {
		a.nav.pick = []string{name}
		a.startOp(job{kind: OpExtract, srcs: []string{src}, dest: dest, name: name}, "Extracting "+base+" to "+name)
	})
}

// extractAll extracts the archive at src into a new folder called name
// in dest. Folders and files come first, and links last, so nothing is
// written through a link the archive makes. An item whose path would
// lead out of the folder stops it.
func (r *runner) extractAll(src, dest, name string) error {
	fsys := r.env.fs
	ps := fsys.Paths()
	if err := checkName(ps, name); err != nil {
		return err
	}
	root := ps.Join(dest, name)
	switch _, err := fsys.Lstat(root); {
	case err == nil:
		return fmt.Errorf("%s already exists", ps.Show(root))
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("writing %s: %w", ps.Show(root), err)
	}
	info, err := fsys.Stat(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ps.Show(src), err)
	}
	in, err := fsys.Open(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ps.Show(src), err)
	}
	defer func() { _ = in.Close() }()
	x := &extractor{r: r, root: root, archive: ps.Show(src), pw: &passwords{ask: r.env.password}}
	ext := strings.ToLower(archiveExt(ps.Base(src)))
	var zr *zip.Reader
	if ext == ".zip" {
		if zr, err = zip.NewReader(&seekReaderAt{rs: in}, info.Size()); err != nil {
			return fmt.Errorf("reading %s: %w", x.archive, err)
		}
		// The password is asked for, and tried, before anything is made,
		// so a zip that does not open leaves nothing behind.
		if uerr := x.unlock(zr); uerr != nil {
			return uerr
		}
	}
	if merr := fsys.Mkdir(root, 0o777); merr != nil {
		return fmt.Errorf("writing %s: %w", ps.Show(root), merr)
	}
	// Undone, the folder goes, with all that came into it.
	r.did("", root)
	r.rec.landed = []string{name}
	if zr != nil {
		err = x.zip(zr)
	} else {
		err = x.tar(in, info.Size(), ext)
	}
	if err != nil {
		return err
	}
	if err := x.makeLinks(); err != nil {
		return err
	}
	x.pw.worked()
	return nil
}

// extractor is an archive being extracted into the folder root.
type extractor struct {
	r       *runner
	root    string
	archive string
	// links are the links the archive makes, made once all else is.
	links []archiveLink
	// pw gives the password of a zip a password protects.
	pw *passwords
}

// archiveLink is a link in an archive: a symbolic one at path pointing
// to target, or with hard set a second name for the file at target, a
// path in the archive.
type archiveLink struct {
	path, target string
	hard         bool
}

// unlock asks for the password of zr, where one protects any of it,
// until the first item it protects opens with it.
func (x *extractor) unlock(zr *zip.Reader) error {
	for _, f := range zr.File {
		if !zipcrypt.Encrypted(f) {
			continue
		}
		rc, err := x.open(f)
		if err != nil {
			return err
		}
		return rc.Close()
	}
	return nil
}

// open opens the item f of a zip, with the zip's password where one
// protects it, asked for again while the one given is wrong.
func (x *extractor) open(f *zip.File) (io.ReadCloser, error) {
	if !zipcrypt.Encrypted(f) {
		return f.Open()
	}
	for {
		if x.pw.asks() {
			x.r.p.waiting = "Waiting for the password"
			x.r.tell(true)
		}
		pw, err := x.pw.get(x.r.ctx)
		x.r.p.waiting = ""
		if err != nil {
			if cerr := x.r.ctx.Err(); cerr != nil {
				return nil, cerr
			}
			// Cancelled, or turned down: the extraction stops, and says
			// so, as a stop at a clash does.
			return nil, errStopped
		}
		rc, err := zipcrypt.Open(f, pw)
		if errors.Is(err, zipcrypt.ErrPassword) {
			x.pw.failed()
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s from %s: %w", f.Name, x.archive, err)
		}
		return rc, nil
	}
}

// zip extracts the zip archive zr.
func (x *extractor) zip(zr *zip.Reader) error {
	var err error
	r := x.r
	r.p.itemsTotal = len(zr.File)
	for _, f := range zr.File {
		r.p.bytesTotal += int64(f.UncompressedSize64)
	}
	r.tell(true)
	for _, f := range zr.File {
		if cerr := r.ctx.Err(); cerr != nil {
			return cerr
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = x.folder(f.Name)
		case mode&fs.ModeSymlink != 0:
			err = x.zipLink(f)
		case mode.IsRegular():
			err = x.zipFile(f, mode)
		}
		if err != nil {
			return err
		}
		r.p.items++
	}
	return nil
}

// zipFile writes the file f of a zip, its mode mode.
func (x *extractor) zipFile(f *zip.File, mode fs.FileMode) error {
	for {
		rc, err := x.open(f)
		if err != nil {
			return err
		}
		bytes := x.r.p.bytes
		err = errors.Join(x.file(f.Name, rc, mode, f.Modified, true), rc.Close())
		if x.retry(f, err) {
			// What came out goes, and does not count.
			x.r.p.bytes = bytes
			if p, ierr := x.inside(f.Name); ierr == nil {
				_ = x.r.env.fs.Remove(p)
			}
			continue
		}
		return err
	}
}

// retry reports whether f, read through with err, is to be read again
// with another password: one that no item of the zip has proved yet,
// read through with which f does not check. That password is taken for
// wrong, as its first bytes may pass with a wrong one: once in 256 times
// for the old protection, and once in 65536 for AES. An item read
// through with no error proves the password.
func (x *extractor) retry(f *zip.File, err error) bool {
	if !zipcrypt.Encrypted(f) {
		return false
	}
	if err == nil {
		x.pw.proven = true
		return false
	}
	if errors.Is(err, zipcrypt.ErrDamaged) && !x.pw.proven {
		x.pw.failed()
		return true
	}
	return false
}

// zipLink notes the symbolic link f of a zip, whose contents are where
// it points.
func (x *extractor) zipLink(f *zip.File) error {
	var target []byte
	var err error
	for {
		var rc io.ReadCloser
		if rc, err = x.open(f); err != nil {
			return err
		}
		target, err = io.ReadAll(io.LimitReader(rc, 4096))
		err = errors.Join(err, rc.Close())
		if !x.retry(f, err) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("reading %s from %s: %w", f.Name, x.archive, err)
	}
	x.links = append(x.links, archiveLink{path: f.Name, target: string(target)})
	return nil
}

// tar extracts a tar archive that in reads, size bytes long, compressed
// as its name's end ext says. Its size is not known before it is read
// whole, so how far along it is counts the archive's own bytes.
func (x *extractor) tar(in io.Reader, size int64, ext string) error {
	r := x.r
	r.p.bytesTotal = size
	r.tell(true)
	var stream io.Reader = &countingReader{r: in, n: func(n int) {
		r.p.bytes += int64(n)
		r.tell(false)
	}}
	switch ext {
	case ".tar.gz", ".tgz":
		gz, err := gzip.NewReader(stream)
		if err != nil {
			return fmt.Errorf("reading %s: %w", x.archive, err)
		}
		defer func() { _ = gz.Close() }()
		stream = gz
	case ".tar.bz2", ".tbz2", ".tbz":
		stream = bzip2.NewReader(stream)
	}
	tr := tar.NewReader(stream)
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", x.archive, err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = x.folder(h.Name)
		case tar.TypeReg:
			err = x.file(h.Name, tr, fs.FileMode(h.Mode).Perm(), h.ModTime, false)
		case tar.TypeSymlink:
			x.links = append(x.links, archiveLink{path: h.Name, target: h.Linkname})
		case tar.TypeLink:
			x.links = append(x.links, archiveLink{path: h.Name, target: h.Linkname, hard: true})
		}
		// Devices, pipes and the like have no place in a folder.
		if err != nil {
			return err
		}
		r.p.items++
	}
}

// inside returns where the item called name in the archive goes, in
// the folder extracted into, or an error where that would lead out of
// it, or the name cannot be written on the file system.
func (x *extractor) inside(name string) (string, error) {
	ps := x.r.env.fs.Paths()
	slashed := strings.ReplaceAll(name, `\`, "/")
	if slices.Contains(strings.Split(slashed, "/"), "..") {
		return "", fmt.Errorf("%s holds %s, which leads out of the folder it is extracted into", x.archive, name)
	}
	// A path from the root lands in the folder, as tar takes it.
	clean := path.Clean("/" + slashed)
	if clean == "/" {
		return x.root, nil
	}
	out := x.root
	for _, part := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if err := checkName(ps, part); err != nil {
			return "", fmt.Errorf("%s holds %s, which cannot be written here: %w", x.archive, name, err)
		}
		out = ps.Join(out, part)
	}
	return out, nil
}

// folder makes the folder called name in the archive.
func (x *extractor) folder(name string) error {
	p, err := x.inside(name)
	if err != nil {
		return err
	}
	if err := mkdirAll(x.r.env.fs, p, 0o777); err != nil {
		return fmt.Errorf("writing %s: %w", x.r.env.fs.Paths().Show(p), err)
	}
	return nil
}

// file writes the file called name in the archive from in, with its
// mode and time, counting its bytes as it goes when count is set.
func (x *extractor) file(name string, in io.Reader, mode fs.FileMode, mod time.Time, count bool) error {
	fsys := x.r.env.fs
	ps := fsys.Paths()
	p, err := x.inside(name)
	if err != nil {
		return err
	}
	if merr := mkdirAll(fsys, ps.Dir(p), 0o777); merr != nil {
		return fmt.Errorf("writing %s: %w", ps.Show(ps.Dir(p)), merr)
	}
	x.r.p.current = ps.Base(p)
	w, err := fsys.Create(p)
	if err != nil {
		return fmt.Errorf("writing %s: %w", ps.Show(p), err)
	}
	poured := x.r.pourAs(w, in, x.archive, ps.Show(p), count)
	if werr := errors.Join(poured, w.Close()); werr != nil {
		return werr
	}
	if st, ok := fsys.(Stamper); ok {
		if mode == 0 {
			mode = 0o644
		}
		if err := st.Chmod(p, mode); err != nil {
			return fmt.Errorf("setting the mode of %s: %w", ps.Show(p), err)
		}
		if !mod.IsZero() {
			if err := st.Chtimes(p, mod); err != nil {
				return fmt.Errorf("setting the time of %s: %w", ps.Show(p), err)
			}
		}
	}
	return nil
}

// makeLinks makes the links the archive holds, now that all else is
// there. A symbolic link is left out on a file system without them; a
// hard one becomes a copy of the file it names.
func (x *extractor) makeLinks() error {
	fsys := x.r.env.fs
	ps := fsys.Paths()
	for _, l := range x.links {
		p, err := x.inside(l.path)
		if err != nil {
			return err
		}
		if merr := mkdirAll(fsys, ps.Dir(p), 0o777); merr != nil {
			return fmt.Errorf("writing %s: %w", ps.Show(ps.Dir(p)), merr)
		}
		if !l.hard {
			if ln, ok := fsys.(Linker); ok {
				if lerr := ln.Symlink(l.target, p); lerr != nil {
					return fmt.Errorf("writing %s: %w", ps.Show(p), lerr)
				}
			}
			continue
		}
		from, err := x.inside(l.target)
		if err != nil {
			return err
		}
		if err := x.copyFile(from, p); err != nil {
			return err
		}
	}
	return nil
}

// copyFile writes a copy of the file at from, extracted already, at to.
func (x *extractor) copyFile(from, to string) error {
	fsys := x.r.env.fs
	ps := fsys.Paths()
	in, err := fsys.Open(from)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ps.Show(from), err)
	}
	w, err := fsys.Create(to)
	if err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", ps.Show(to), err), in.Close())
	}
	err = x.r.pourAs(w, in, ps.Show(from), ps.Show(to), false)
	return errors.Join(err, w.Close(), in.Close())
}

// seekReaderAt reads at an offset from a file that can only seek, as a
// zip's reader asks, one read at a time.
type seekReaderAt struct {
	mu sync.Mutex
	rs io.ReadSeeker
}

// ReadAt implements [io.ReaderAt].
func (s *seekReaderAt) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.rs.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(s.rs, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}

// countingReader tells n of the bytes it reads from r.
type countingReader struct {
	r io.Reader
	n func(int)
}

// Read implements [io.Reader].
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n(n)
	}
	return n, err
}
