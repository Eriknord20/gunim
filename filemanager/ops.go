package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// OpKind is what a file operation does.
type OpKind uint8

// The file operations.
const (
	OpCopy OpKind = iota
	OpMove
	OpTrash
	OpDelete
	OpRename
	OpNewFolder
	OpUndo
	// OpZip writes items into a new zip file.
	OpZip
	// OpExtract extracts an archive into a new folder.
	OpExtract
)

// job is a file operation to run.
type job struct {
	kind OpKind
	// srcs are the items it works on.
	srcs []string
	// dest is the folder a copy or a move goes into, or the folder a new
	// folder goes in.
	dest string
	// name is the new name of a rename, the name of a new folder, the
	// name of a zip, or of the folder an archive is extracted into, made
	// in dest.
	name string
	// undo is the record an undo reverses.
	undo *record
}

// step is one item an operation moved or made: what was at from is now at
// to. A new folder has no from, and a trashed item has no to where the
// app cannot take it back out. at is when a trashed item went.
type step struct {
	from, to string
	at       time.Time
}

// record is what an operation did, for undo to reverse.
type record struct {
	kind  OpKind
	steps []step
	// replaced counts the files a copy or a move put in place of others,
	// which undo cannot bring back.
	replaced int
	// left holds, for an undo that stopped, the steps of the record it
	// undid that it did not undo.
	left []step
	// landed holds the names a copy or a move left in its folder, made,
	// merged into or put in place of others, for the window to select.
	landed []string
}

// progress is how far an operation has got.
type progress struct {
	items, itemsTotal int
	bytes, bytesTotal int64
	current           string
}

// choice is an answer to a name clash.
type choice uint8

// The answers to a name clash.
const (
	choiceReplace choice = iota
	choiceKeepBoth
	choiceSkip
)

// answer is the user's answer to a name clash; all applies it to every
// clash left in the operation.
type answer struct {
	choice choice
	all    bool
}

// clash is an item going where one of the same name already is.
type clash struct {
	src, dst string
	// sameKind is set when both are folders or both are not, which a
	// replace needs.
	sameKind bool
	// from says what src is where it is no path to read, as for an item
	// in the Recycle Bin.
	from string
}

// errStopped is returned when the user stops an operation at a clash.
var errStopped = errors.New("stopped")

// env is what an operation reaches outside itself.
type env struct {
	// fs is the file system the operation works on, and trash its trash,
	// or nil where it has none. to is the file system a zip is written
	// to, where it is not fs.
	fs    FS
	trash Trasher
	to    FS
	// ask asks the user about a clash, and waits for the answer.
	ask func(ctx context.Context, c clash) (answer, error)
	// report hears how far the operation has got, every reportEvery, or
	// every 50 ms when that is zero.
	report      func(progress)
	reportEvery time.Duration
	// limit, when above zero, holds a copy to that many bytes a second.
	limit float64
}

// runner runs one job.
type runner struct {
	ctx  context.Context
	env  env
	rec  record
	p    progress
	all  *answer
	last time.Time
	// paced counts the bytes copied since paceStart, for env.limit.
	paced     int64
	paceStart time.Time
	// made holds the folders a copy made at the top of what it copies,
	// for the copy to stay out of.
	made []fs.FileInfo
}

// runJob runs j, stopping at the first error, and returns what it did.
func runJob(ctx context.Context, j job, e env) (record, error) {
	r := &runner{ctx: ctx, env: e, rec: record{kind: j.kind}}
	var err error
	switch j.kind {
	case OpCopy:
		err = r.copyAll(j.srcs, j.dest)
	case OpMove:
		err = r.moveAll(j.srcs, j.dest)
	case OpTrash:
		err = r.trashAll(j.srcs)
	case OpDelete:
		err = r.deleteAll(j.srcs)
	case OpRename:
		err = r.rename(j.srcs[0], j.name)
	case OpNewFolder:
		err = r.newFolder(j.dest, j.name)
	case OpUndo:
		err = r.undo(j.undo)
	case OpZip:
		err = r.zipAll(j.srcs, j.dest, j.name)
	case OpExtract:
		err = r.extractAll(j.srcs[0], j.dest, j.name)
	}
	r.tell(true)
	return r.rec, err
}

// tell reports progress, at most every reportEvery unless now is set.
func (r *runner) tell(now bool) {
	if r.env.report == nil {
		return
	}
	every := r.env.reportEvery
	if every == 0 {
		every = 50 * time.Millisecond
	}
	if t := time.Now(); now || t.Sub(r.last) >= every {
		r.last = t
		r.env.report(r.p)
	}
}

func (r *runner) did(from, to string) { r.rec.steps = append(r.rec.steps, step{from: from, to: to}) }

// measure counts the items and bytes under path, stopping at the first
// error.
func (r *runner) measure(path string) (items int, bytes int64, err error) {
	err = walkTree(r.ctx, r.env.fs, path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		if err := r.ctx.Err(); err != nil {
			return err
		}
		items++
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("reading %s: %w", p, err)
			}
			bytes += info.Size()
		}
		return nil
	})
	return items, bytes, err
}

// planBytes adds what srcs hold to the totals.
func (r *runner) planBytes(srcs []string) error {
	for _, s := range srcs {
		n, b, err := r.measure(s)
		if err != nil {
			return err
		}
		r.p.itemsTotal += n
		r.p.bytesTotal += b
	}
	r.tell(true)
	return nil
}

// into checks that src on fsys is not a folder that dir lies inside. It
// checks the paths as written and with the links along them followed.
// src itself stays as it is, as a link can go into the folder it leads
// to.
func into(fsys FS, src, dir string) error {
	ps := fsys.Paths()
	followed := ps.Join(realPath(fsys, ps.Dir(src)), ps.Base(src))
	if ps.inside(dir, src) || ps.inside(realPath(fsys, dir), followed) {
		return fmt.Errorf("%s cannot go inside itself", ps.Base(src))
	}
	return nil
}

// freeName returns path on fsys, or path with the first " (n)" that is
// free.
func freeName(fsys FS, path string) (string, error) {
	ps := fsys.Paths()
	dir, base := ps.Split(path)
	for n := 1; ; n++ {
		p := path
		if n > 1 {
			p = ps.Join(dir, numbered(base, n))
		}
		_, err := fsys.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return p, nil
		}
		if err != nil {
			return "", err
		}
	}
}

// resolve decides where src goes when it goes to dst: dst, or a free name
// beside it, or nowhere. merge says dst is a folder src's contents go
// into, and replace that dst is a file src takes the place of.
func (r *runner) resolve(src, dst string) (to string, skip, merge, replace bool, err error) {
	dInfo, err := r.env.fs.Lstat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return dst, false, false, false, nil
	}
	if err != nil {
		return "", false, false, false, err
	}
	sInfo, err := r.env.fs.Lstat(src)
	if err != nil {
		return "", false, false, false, err
	}
	sDir, dDir := realDir(sInfo), realDir(dInfo)
	ans := answer{}
	if r.all != nil {
		ans = *r.all
	} else {
		if r.env.ask == nil {
			return "", false, false, false, fmt.Errorf("%s already exists", r.env.fs.Paths().Show(dst))
		}
		ans, err = r.env.ask(r.ctx, clash{src: src, dst: dst, sameKind: sDir == dDir})
		if err != nil {
			return "", false, false, false, err
		}
		if ans.all {
			r.all = &ans
		}
	}
	switch ans.choice {
	case choiceSkip:
		return "", true, false, false, nil
	case choiceKeepBoth:
		to, err := freeName(r.env.fs, dst)
		return to, false, false, false, err
	case choiceReplace:
	}
	if sDir != dDir {
		return "", false, false, false, fmt.Errorf("%s cannot replace %s: one is a folder and the other is not", r.env.fs.Paths().Show(src),
			r.env.fs.Paths().Show(dst))
	}
	return dst, false, sDir, !sDir, nil
}

// realDir reports whether info is a folder rather than a link to one.
func realDir(info fs.FileInfo) bool { return info.IsDir() && info.Mode()&fs.ModeSymlink == 0 }

// copyAll copies srcs into dest.
func (r *runner) copyAll(srcs []string, dest string) error {
	if err := r.planBytes(srcs); err != nil {
		return err
	}
	ps := r.env.fs.Paths()
	for _, src := range srcs {
		if err := into(r.env.fs, src, dest); err != nil {
			return err
		}
		dst := ps.Join(dest, ps.Base(src))
		var skip, merge, replace bool
		var err error
		if ps.Same(ps.Dir(src), dest) {
			// A copy into the folder it is in takes a name of its own.
			dst, err = freeName(r.env.fs, dst)
		} else {
			dst, skip, merge, replace, err = r.resolve(src, dst)
		}
		if err != nil {
			return err
		}
		if skip {
			if err := r.skipped(src); err != nil {
				return err
			}
			continue
		}
		if err := r.copyItem(src, dst, merge, replace, true); err != nil {
			return err
		}
		r.rec.landed = append(r.rec.landed, ps.Base(dst))
	}
	return nil
}

// skipped counts what src holds as done.
func (r *runner) skipped(src string) error {
	n, b, err := r.measure(src)
	if err != nil {
		return err
	}
	r.p.items += n
	r.p.bytes += b
	r.tell(false)
	return nil
}

// copyItem copies src to dst, into the folder there with merge, or in
// place of the file there with replace. top records dst as made by the
// operation.
func (r *runner) copyItem(src, dst string, merge, replace, top bool) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	info, err := r.env.fs.Lstat(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	r.p.current = src
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		err = copyLink(r.env.fs, src, dst, replace)
	case info.IsDir():
		if slices.ContainsFunc(r.made, func(m fs.FileInfo) bool { return sameFile(r.env.fs, m, info) }) {
			return fmt.Errorf("%s cannot go inside itself", r.env.fs.Paths().Base(src))
		}
		err = r.copyDir(src, dst, info, merge, top)
	case info.Mode().IsRegular():
		err = r.copyFile(src, dst, info)
	default:
		err = fmt.Errorf("%s is not a file, a folder or a link, and cannot be copied", r.env.fs.Paths().Show(src))
	}
	if err != nil {
		return err
	}
	r.p.items++
	r.tell(false)
	switch {
	case replace:
		r.rec.replaced++
	case top && !merge:
		r.did(src, dst)
	}
	return nil
}

// copyDir copies the folder src to dst, into the folder there with merge.
// top keeps dst in made, where the copy makes it.
func (r *runner) copyDir(src, dst string, info fs.FileInfo, merge, top bool) error {
	fsys := r.env.fs
	if !merge {
		if err := fsys.Mkdir(dst, info.Mode().Perm()|0o700); err != nil {
			return fmt.Errorf("making %s: %w", dst, err)
		}
		if made, err := fsys.Lstat(dst); err == nil && top {
			r.made = append(r.made, made)
		}
	}
	kids, err := readDirSorted(r.ctx, fsys, src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	ps := fsys.Paths()
	for _, k := range kids {
		s, d := ps.Join(src, k.Name()), ps.Join(dst, k.Name())
		if !merge {
			if err := r.copyItem(s, d, false, false, false); err != nil {
				return err
			}
			continue
		}
		to, skip, m, rep, err := r.resolve(s, d)
		if err != nil {
			return err
		}
		if skip {
			if err := r.skipped(s); err != nil {
				return err
			}
			continue
		}
		if err := r.copyItem(s, to, m, rep, true); err != nil {
			return err
		}
	}
	if st, ok := fsys.(Stamper); ok && !merge {
		if err := st.Chtimes(dst, info.ModTime()); err != nil {
			return fmt.Errorf("setting the time of %s: %w", dst, err)
		}
	}
	return nil
}

// errNoLinks says a file system has no links to copy.
var errNoLinks = errors.New("this file system has no links")

// copyLink makes a link at dst on fsys to what the link src points to.
func copyLink(fsys FS, src, dst string, replace bool) error {
	l, ok := fsys.(Linker)
	if !ok {
		return fmt.Errorf("copying the link %s: %w", src, errNoLinks)
	}
	target, err := l.Readlink(src)
	if err != nil {
		return fmt.Errorf("reading the link %s: %w", src, err)
	}
	if replace {
		if err := fsys.Remove(dst); err != nil {
			return fmt.Errorf("replacing %s: %w", dst, err)
		}
	}
	if err := l.Symlink(target, dst); err != nil {
		return fmt.Errorf("making the link %s: %w", dst, err)
	}
	return nil
}

// copyBuffer is how much a copy reads at a time.
const copyBuffer = 1 << 20

// copyFile copies the file src to dst through a part file beside dst, so
// dst is never left half written.
func (r *runner) copyFile(src, dst string, info fs.FileInfo) (err error) {
	fsys := r.env.fs
	in, err := fsys.Open(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer func() {
		if cerr := in.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("reading %s: %w", src, cerr)
		}
	}()
	return r.writeFile(in, src, dst, info)
}

// writeFile writes what in reads of src to dst, on the runner's file
// system, through a part file beside dst that it then renames over dst,
// so dst is never left half written. The file takes the mode and the
// time info gives, where the file system can set them.
func (r *runner) writeFile(in io.Reader, src, dst string, info fs.FileInfo) error {
	fsys := r.env.fs
	// The part file's name is short, so a dst whose name is near the
	// longest a name can be fits too.
	out, part, err := createTemp(fsys, fsys.Paths().Dir(dst), ".files-*.part")
	if err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	fail := func(e error) error {
		return errors.Join(e, out.Close(), fsys.Remove(part))
	}
	buf := make([]byte, copyBuffer)
	for {
		if err := r.ctx.Err(); err != nil {
			return errors.Join(err, out.Close(), fsys.Remove(part))
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return fail(fmt.Errorf("writing %s: %w", dst, err))
			}
			r.p.bytes += int64(n)
			r.tell(false)
			if err := r.pace(n); err != nil {
				return fail(err)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return fail(fmt.Errorf("reading %s: %w", src, rerr))
		}
	}
	if err := out.Close(); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", dst, err), fsys.Remove(part))
	}
	if st, ok := fsys.(Stamper); ok {
		if err := st.Chmod(part, info.Mode().Perm()); err != nil {
			return errors.Join(fmt.Errorf("setting the mode of %s: %w", dst, err), fsys.Remove(part))
		}
		if err := st.Chtimes(part, info.ModTime()); err != nil {
			return errors.Join(fmt.Errorf("setting the time of %s: %w", dst, err), fsys.Remove(part))
		}
	}
	if err := fsys.Rename(part, dst); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", dst, err), fsys.Remove(part))
	}
	return nil
}

// moveAll moves srcs into dest.
func (r *runner) moveAll(srcs []string, dest string) error {
	r.p.itemsTotal = len(srcs)
	r.tell(true)
	ps := r.env.fs.Paths()
	for _, src := range srcs {
		dst := ps.Join(dest, ps.Base(src))
		if ps.Same(src, dst) {
			r.p.items++
			continue
		}
		if err := into(r.env.fs, src, dest); err != nil {
			return err
		}
		to, skip, merge, _, err := r.resolve(src, dst)
		if err != nil {
			return err
		}
		if !skip {
			if err := r.moveItem(src, to, merge, true); err != nil {
				return err
			}
			r.rec.landed = append(r.rec.landed, ps.Base(to))
		}
		r.p.items++
		r.tell(false)
	}
	return nil
}

// moveItem moves src to dst, or into the folder there with merge, by a
// rename on one volume and by a copy and a delete across volumes. top
// records the move.
func (r *runner) moveItem(src, dst string, merge, top bool) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	r.p.current = src
	if merge {
		_, err := r.mergeInto(src, dst)
		return err
	}
	_, statErr := r.env.fs.Lstat(dst)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("moving %s: %w", src, statErr)
	}
	replacing := statErr == nil
	err := r.env.fs.Rename(src, dst)
	switch {
	case err == nil && replacing:
		r.rec.replaced++
	case err == nil:
	case !errors.Is(err, ErrCrossDevice):
		return fmt.Errorf("moving %s: %w", src, err)
	default:
		// A copy that replaces counts the file it replaced.
		if err := r.moveAcross(src, dst, replacing); err != nil {
			return err
		}
	}
	if top && !replacing {
		r.did(src, dst)
	}
	return nil
}

// mergeInto moves the contents of the folder src into the folder dst, and
// removes src once nothing in it was skipped, at any depth. kept reports
// that something was skipped, and src kept.
func (r *runner) mergeInto(src, dst string) (kept bool, err error) {
	kids, err := readDirSorted(r.ctx, r.env.fs, src)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", src, err)
	}
	ps := r.env.fs.Paths()
	for _, k := range kids {
		s, d := ps.Join(src, k.Name()), ps.Join(dst, k.Name())
		to, skip, m, _, err := r.resolve(s, d)
		if err != nil {
			return false, err
		}
		switch {
		case skip:
			kept = true
		case m:
			if err := r.ctx.Err(); err != nil {
				return false, err
			}
			r.p.current = s
			deep, err := r.mergeInto(s, to)
			if err != nil {
				return false, err
			}
			kept = kept || deep
		default:
			if err := r.moveItem(s, to, false, true); err != nil {
				return false, err
			}
		}
	}
	if kept {
		return true, nil
	}
	if err := r.env.fs.Remove(src); err != nil {
		return false, fmt.Errorf("removing %s after moving what it held: %w", src, err)
	}
	return false, nil
}

// moveAcross moves src to another volume by copying it and deleting it.
// A copy that fails is taken away again.
func (r *runner) moveAcross(src, dst string, replacing bool) error {
	_, b, err := r.measure(src)
	if err != nil {
		return err
	}
	r.p.bytesTotal += b
	info, err := r.env.fs.Lstat(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	items := r.p.items
	defer func() { r.p.items = items }()
	if err := r.copyItem(src, dst, false, replacing, false); err != nil {
		if replacing || !realDir(info) {
			return err
		}
		// The cleanup runs even when the operation was cancelled.
		return errors.Join(err, removeAll(context.WithoutCancel(r.ctx), r.env.fs, dst))
	}
	return r.removeTree(src)
}

// errTrashless says a file system has no trash.
var errTrashless = errors.New("this file system has no trash; delete permanently instead")

// trashAll moves srcs to the trash.
func (r *runner) trashAll(srcs []string) error {
	if r.env.trash == nil {
		return errTrashless
	}
	r.p.itemsTotal = len(srcs)
	r.tell(true)
	for _, src := range srcs {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.p.current = src
		r.tell(false)
		at := time.Now()
		to, err := r.env.trash.Trash(src)
		if err != nil {
			return err
		}
		r.rec.steps = append(r.rec.steps, step{from: src, to: to, at: at})
		r.p.items++
	}
	return nil
}

// deleteAll deletes srcs for good.
func (r *runner) deleteAll(srcs []string) error {
	for _, s := range srcs {
		n, _, err := r.measure(s)
		if err != nil {
			return err
		}
		r.p.itemsTotal += n
	}
	r.tell(true)
	for _, s := range srcs {
		if err := r.removeTree(s); err != nil {
			return err
		}
		r.did(s, "")
	}
	return nil
}

// removeTree deletes path and all it holds, the contents first.
func (r *runner) removeTree(path string) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	fsys := r.env.fs
	info, err := fsys.Lstat(path)
	if err != nil {
		return fmt.Errorf("deleting %s: %w", fsys.Paths().Show(path), err)
	}
	if realDir(info) {
		kids, err := readDirSorted(r.ctx, fsys, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", fsys.Paths().Show(path), err)
		}
		for _, k := range kids {
			if err := r.removeTree(fsys.Paths().Join(path, k.Name())); err != nil {
				return err
			}
		}
	}
	r.p.current = path
	if err := fsys.Remove(path); err != nil {
		return fmt.Errorf("deleting %s: %w", fsys.Paths().Show(path), err)
	}
	r.p.items++
	r.tell(false)
	return nil
}

// checkName says what is wrong with name as the name of an item in paths
// of style ps, or nothing.
func checkName(ps PathStyle, name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("a name cannot be empty")
	case name == "." || name == "..":
		return fmt.Errorf("%q is not a name an item can have", name)
	case strings.ContainsAny(name, `/`+ps.Sep()):
		return fmt.Errorf("a name cannot hold %q", ps.Sep())
	case ps.windowsNames() && strings.ContainsAny(name, `<>:"|?*`):
		return errors.New(`a name cannot hold any of < > : " | ? *`)
	}
	return nil
}

// rename gives src a new name in its folder.
func (r *runner) rename(src, name string) error {
	fsys := r.env.fs
	ps := fsys.Paths()
	if err := checkName(ps, name); err != nil {
		return err
	}
	dst := ps.Join(ps.Dir(src), name)
	if dst == src {
		return nil
	}
	sInfo, err := fsys.Lstat(src)
	if err != nil {
		return fmt.Errorf("renaming %s: %w", ps.Show(src), err)
	}
	dInfo, err := fsys.Lstat(dst)
	switch {
	case err == nil && !sameFile(fsys, sInfo, dInfo):
		return fmt.Errorf("%s already exists", ps.Show(dst))
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("renaming %s: %w", ps.Show(src), err)
	}
	if err := fsys.Rename(src, dst); err != nil {
		return fmt.Errorf("renaming %s: %w", ps.Show(src), err)
	}
	r.did(src, dst)
	return nil
}

// newFolder makes a folder called name in dir.
func (r *runner) newFolder(dir, name string) error {
	ps := r.env.fs.Paths()
	if err := checkName(ps, name); err != nil {
		return err
	}
	path := ps.Join(dir, name)
	if err := r.env.fs.Mkdir(path, 0o777); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", ps.Show(path))
		}
		return fmt.Errorf("making %s: %w", ps.Show(path), err)
	}
	r.did("", path)
	return nil
}

// undo reverses what rec did, the last step first. Where it stops, it
// keeps the steps it did not undo in left.
func (r *runner) undo(rec *record) error {
	steps := slices.Clone(rec.steps)
	slices.Reverse(steps)
	r.p.itemsTotal = len(steps)
	r.tell(true)
	for i, s := range steps {
		err := r.ctx.Err()
		if err == nil {
			r.p.current = s.to
			if rec.kind == OpTrash {
				r.p.current = s.from
			}
			r.tell(false)
			err = r.undoStep(rec.kind, s)
		}
		if err != nil {
			r.rec.left = slices.Clone(rec.steps[:len(steps)-i])
			return err
		}
		r.p.items++
	}
	return nil
}

// undoStep reverses one step of an operation of kind k.
func (r *runner) undoStep(k OpKind, s step) error {
	fsys := r.env.fs
	ps := fsys.Paths()
	switch k {
	case OpCopy, OpNewFolder, OpZip, OpExtract:
		if r.env.trash == nil {
			return errTrashless
		}
		to, err := r.env.trash.Trash(s.to)
		if err != nil {
			return err
		}
		r.did(s.to, to)
	case OpMove, OpRename:
		info, err := fsys.Lstat(s.from)
		switch {
		case err == nil && caseOnly(fsys, s, info):
			if err = fsys.Rename(s.to, s.from); err != nil {
				return fmt.Errorf("renaming %s back: %w", s.to, err)
			}
			r.did(s.to, s.from)
			return nil
		case err == nil:
			return fmt.Errorf("%s exists again, so %s cannot go back there", s.from, ps.Base(s.to))
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("moving %s back: %w", s.to, err)
		}
		// A move that merged a folder removed the folder it emptied.
		if err := mkdirAll(fsys, ps.Dir(s.from), 0o777); err != nil {
			return fmt.Errorf("moving %s back: %w", s.to, err)
		}
		if err := r.moveItem(s.to, s.from, false, true); err != nil {
			return err
		}
	case OpTrash:
		return r.restore(s)
	case OpDelete, OpUndo:
		return errors.New("this cannot be undone")
	}
	return nil
}

// caseOnly reports whether step s changed only the case of a name, on a
// file system that ignores case: from, found as info, is the item at to.
func caseOnly(fsys FS, s step, info fs.FileInfo) bool {
	ps := fsys.Paths()
	if !ps.Same(ps.Dir(s.from), ps.Dir(s.to)) || !strings.EqualFold(ps.Base(s.from), ps.Base(s.to)) {
		return false
	}
	now, err := fsys.Lstat(s.to)
	return err == nil && sameFile(fsys, info, now)
}

// undoable reports whether rec can be undone, on a file system with a
// trash when trash is set. Undoing a copy, a new folder, a zip or an
// extraction moves what it made to the trash.
func undoable(rec record, trash bool) bool {
	if !trash && (rec.kind == OpCopy || rec.kind == OpNewFolder || rec.kind == OpZip || rec.kind == OpExtract) {
		return false
	}
	return len(rec.steps) > 0 && rec.kind != OpDelete && rec.kind != OpUndo
}
