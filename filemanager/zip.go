package filemanager

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/marrasen/gunim/zipcrypt"
)

// Making a zip of items: the items of one file system, with all that
// folders among them hold, written as one zip file on the same file
// system or another. The zip is written through a part file beside it,
// so a zip that stopped short is never left looking whole.

// zipExt is the extension a zip's name ends with.
const zipExt = ".zip"

// zipName is the name suggested for a zip of the items at paths, of
// path style ps: the one item's name without its extension, or the
// name of the folder they are in, or Archive where that has none, as a
// drive.
func zipName(ps PathStyle, paths []string) string {
	stem := ""
	switch {
	case len(paths) == 1:
		stem = ps.Base(paths[0])
		if i := strings.LastIndexByte(stem, '.'); i > 0 {
			stem = stem[:i]
		}
	case len(paths) > 1:
		dir := ps.Dir(paths[0])
		if base := ps.Base(dir); base != dir && strings.Trim(base, `/\:`) != "" {
			stem = base
		}
	}
	if stem == "" {
		stem = "Archive"
	}
	return stem + zipExt
}

// withZipExt is name, ending in .zip: a name the user typed without it
// gets it.
func withZipExt(name string) string {
	if strings.HasSuffix(strings.ToLower(name), zipExt) {
		return name
	}
	return name + zipExt
}

// ZipFiles writes the items at paths on from, with all that folders
// among them hold, into a new zip file called name in the folder into
// on to, and tells p how far it has got. It is what a program does for
// a Transfer whose Zip is set: a window makes a zip on its own file
// system itself. Nothing may be at the zip's path already. A link goes
// in as a link where from can read one, and anything else that is not
// a file or a folder is left out.
func ZipFiles(ctx context.Context, from FS, paths []string, to FS, into, name, password string, p *TransferProgress) error {
	e := env{fs: from, to: to}
	if p != nil {
		e.report = func(pr progress) { p.Report(pr.bytes, pr.bytesTotal, pr.items, pr.itemsTotal, pr.current) }
	}
	r := &runner{ctx: ctx, env: e, rec: record{kind: OpZip}}
	err := r.zipAll(paths, into, name, password)
	r.tell(true)
	return err
}

// zipAll writes srcs into a new zip file called name in dest, on the
// file system env.to, or env.fs where that is nil, protected with
// password, where it is not empty.
func (r *runner) zipAll(srcs []string, dest, name, password string) error {
	from, to := r.env.fs, r.env.to
	if to == nil {
		to = from
	}
	tps := to.Paths()
	if err := checkName(tps, name); err != nil {
		return err
	}
	path := tps.Join(dest, name)
	switch _, err := to.Lstat(path); {
	case err == nil:
		return fmt.Errorf("%s already exists", tps.Show(path))
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("writing %s: %w", tps.Show(path), err)
	}
	if err := r.planBytes(srcs); err != nil {
		return err
	}
	out, part, err := createTemp(to, dest, ".files-*.part")
	if err != nil {
		return fmt.Errorf("writing %s: %w", tps.Show(path), err)
	}
	// A zip written into a folder it zips leaves its own part file out.
	skip := ""
	if to.ID() == from.ID() {
		skip = part
	}
	// Each item is protected with the password, where it is not empty.
	zw := zipcrypt.NewWriter(zip.NewWriter(out), password)
	for _, src := range srcs {
		if err := r.zipTree(zw, src, skip); err != nil {
			return errors.Join(err, zw.Close(), out.Close(), to.Remove(part))
		}
	}
	if err := zw.Close(); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", tps.Show(path), err), out.Close(), to.Remove(part))
	}
	if err := out.Close(); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", tps.Show(path), err), to.Remove(part))
	}
	// Made meanwhile, it is not written over.
	if _, err := to.Lstat(path); err == nil {
		return errors.Join(fmt.Errorf("%s already exists", tps.Show(path)), to.Remove(part))
	}
	if err := to.Rename(part, path); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", tps.Show(path), err), to.Remove(part))
	}
	r.did("", path)
	r.rec.landed = []string{name}
	return nil
}

// zipTree writes the item at src, and all it holds, into zw, under its
// own name, leaving out the file at skip.
func (r *runner) zipTree(zw *zipcrypt.Writer, src, skip string) error {
	fsys := r.env.fs
	ps := fsys.Paths()
	top := ps.Dir(src)
	return walkTree(r.ctx, fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("reading %s: %w", ps.Show(p), err)
		}
		if cerr := r.ctx.Err(); cerr != nil {
			return cerr
		}
		if skip != "" && ps.Same(p, skip) {
			return nil
		}
		rel, err := ps.Rel(top, p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", ps.Show(p), err)
		}
		if ps.Sep() != "/" {
			rel = strings.ReplaceAll(rel, ps.Sep(), "/")
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("reading %s: %w", ps.Show(p), err)
		}
		r.p.current = p
		if err := r.zipItem(zw, p, rel, info); err != nil {
			return err
		}
		r.p.items++
		r.tell(false)
		return nil
	})
}

// zipItem writes the item at p, found as info, into zw as rel.
func (r *runner) zipItem(zw *zipcrypt.Writer, p, rel string, info fs.FileInfo) error {
	fsys := r.env.fs
	ps := fsys.Paths()
	h, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ps.Show(p), err)
	}
	h.Name = rel
	mode := info.Mode()
	switch {
	case mode.IsDir():
		h.Name += "/"
		h.Method = zip.Store
		_, err = zw.Create(h)
		return err
	case mode&fs.ModeSymlink != 0:
		l, ok := fsys.(Linker)
		if !ok {
			return nil
		}
		var target string
		if target, err = l.Readlink(p); err != nil {
			return fmt.Errorf("reading the link %s: %w", ps.Show(p), err)
		}
		h.Method = zip.Store
		var w io.Writer
		if w, err = zw.Create(h); err == nil {
			_, err = io.WriteString(w, target)
		}
		return err
	case !mode.IsRegular():
		// A device, a pipe or a socket holds nothing to keep.
		return nil
	}
	h.Method = zip.Deflate
	w, err := zw.Create(h)
	if err != nil {
		return err
	}
	in, err := fsys.Open(p)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ps.Show(p), err)
	}
	err = r.pour(w, in, p)
	return errors.Join(err, in.Close())
}

// pour writes what in reads of the file at p to w, counting the bytes.
func (r *runner) pour(w io.Writer, in io.Reader, p string) error {
	return r.pourAs(w, in, r.env.fs.Paths().Show(p), "the zip", true)
}

// pourAs writes what in reads of from to w, which writes to, both said
// as they are shown, counting the bytes when count is set.
func (r *runner) pourAs(w io.Writer, in io.Reader, from, to string, count bool) error {
	buf := make([]byte, copyBuffer)
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return fmt.Errorf("writing %s: %w", to, err)
			}
			if count {
				r.p.bytes += int64(n)
				r.tell(false)
			}
			if err := r.pace(n); err != nil {
				return err
			}
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("reading %s: %w", from, rerr)
		}
	}
}
