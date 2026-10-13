package filemanager

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim/zipcrypt"
)

// browseExts are the names of the archives a window walks into as it
// does into a folder: zips, and the formats that are zips by another
// name. A tar has no index, so listing one means reading all of it.
var browseExts = []string{".zip", ".jar", ".whl", ".xpi", ".crx", ".vsix"}

// browsable reports whether a file called name opens as a folder.
func browsable(name string) bool {
	lower := strings.ToLower(name)
	return slices.ContainsFunc(browseExts, func(ext string) bool {
		return strings.HasSuffix(lower, ext) && len(lower) > len(ext)
	})
}

// ErrInArchive is why a change to what an archive holds fails: a window
// reads an archive as a folder, and does not write one.
var ErrInArchive = errors.New("what is in an archive can only be read here")

// errLocked is why a file a password protects does not open before the
// password is given.
var errLocked = errors.New("it is protected with a password, which was not given")

const (
	// heldMost is how many archives an archives keeps open at once: the
	// one a window shows, and those a copy or a preview reads besides.
	heldMost = 4
	// heldFresh is how long an archive is taken to be as it was read
	// before its file is asked again whether it changed.
	heldFresh = time.Second
)

// archives is a file system with its archives opened as folders. A path
// inside an archive is answered as any other is, so the window lists,
// previews, copies out of and filters an archive with what it has for a
// folder. What an archive holds is read in place: only its index, and
// what is opened of it, so a large one on a server opens as fast as a
// small one. A change inside one fails with [ErrInArchive].
//
// It has every optional interface of a file system, each answering for
// an archive's paths and passing the others on, so a window asks for
// them with [optional], which also asks the file system under it.
type archives struct {
	FS
	keys *zipKeys

	mu   sync.Mutex
	held []*heldZip
	// files are the paths found to be an archive, or found not to be,
	// by when.
	files map[string]fileSeen
}

// fileSeen is whether a path is an archive's file, as found at when.
type fileSeen struct {
	archive bool
	when    time.Time
}

// withArchives returns fsys with its archives opened as folders, their
// passwords kept in keys.
func withArchives(fsys FS, keys *zipKeys) FS {
	if a, ok := fsys.(*archives); ok {
		fsys = a.FS
	}
	return &archives{FS: fsys, keys: keys, files: map[string]fileSeen{}}
}

// unwrapFS returns the file system under fsys, where it opens archives.
func unwrapFS(fsys FS) FS {
	if a, ok := fsys.(*archives); ok {
		return a.FS
	}
	return fsys
}

// optional returns fsys as T, an optional interface of a file system,
// where fsys has it, or where its archives are opened, where the file
// system under it does.
func optional[T any](fsys FS) (T, bool) {
	if a, ok := fsys.(*archives); ok {
		if _, ok := a.FS.(T); !ok {
			var none T
			return none, false
		}
	}
	t, ok := fsys.(T)
	return t, ok
}

// under returns the file system under a as T, one of the optional
// interfaces it has, as [optional] made sure.
func under[T any](a *archives) T {
	t, _ := a.FS.(T)
	return t
}

// zipKeys are the passwords that opened archives, by the file system
// and the path of each, for the windows of a hub.
type zipKeys struct {
	mu sync.Mutex
	m  map[string]string
}

// get returns the password that opened the archive at archive on the
// file system of ID id.
func (k *zipKeys) get(id, archive string) (string, bool) {
	if k == nil {
		return "", false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	pw, ok := k.m[id+"\x00"+archive]
	return pw, ok
}

// set keeps pw as the password of the archive at archive on the file
// system of ID id.
func (k *zipKeys) set(id, archive, pw string) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[id+"\x00"+archive] = pw
}

// heldZip is an archive held open: its file, the reader of its index,
// and the folders that index makes.
type heldZip struct {
	path    string
	size    int64
	mod     time.Time
	checked time.Time
	file    io.ReadSeekCloser
	ra      *seekReaderAt
	zr      *zip.Reader
	// nodes are what it holds by their path in it, '/' between folders,
	// and fold the paths by their lower case, for a file system whose
	// names ignore case.
	nodes map[string]*zipNode
	fold  map[string]string
	// locked says a password protects some of what it holds.
	locked bool
	// users counts the files open out of it, and gone says it is no
	// longer held, so its file closes once the last of them does.
	users int
	gone  bool
}

// zipNode is a file or a folder of an archive.
type zipNode struct {
	name string
	f    *zip.File
	dir  bool
	mod  time.Time
	kids []*zipNode
}

// zipInfo describes a zipNode, as a file system's FileInfo does.
type zipInfo struct{ n *zipNode }

// Name implements [fs.FileInfo].
func (i zipInfo) Name() string { return i.n.name }

// Size implements [fs.FileInfo].
func (i zipInfo) Size() int64 {
	if i.n.dir {
		return 0
	}
	return int64(i.n.f.UncompressedSize64)
}

// Mode is that of a folder or a plain file: a link in an archive shows
// as a file holding where it points, as no link of it can be followed.
func (i zipInfo) Mode() fs.FileMode {
	if i.n.dir {
		return fs.ModeDir | 0o755
	}
	if perm := i.n.f.Mode().Perm(); perm != 0 {
		return perm
	}
	return 0o644
}

// ModTime implements [fs.FileInfo].
func (i zipInfo) ModTime() time.Time { return i.n.mod }

// IsDir implements [fs.FileInfo].
func (i zipInfo) IsDir() bool { return i.n.dir }

// Sys implements [fs.FileInfo]: nil, as nothing of the system describes it.
func (i zipInfo) Sys() any { return nil }

// zipDirEntry is a zipNode in a listing.
type zipDirEntry struct{ zipInfo }

// Type implements [fs.DirEntry].
func (d zipDirEntry) Type() fs.FileMode { return d.Mode().Type() }

// Info implements [fs.DirEntry].
func (d zipDirEntry) Info() (fs.FileInfo, error) { return d.zipInfo, nil }

// split finds the archive path is in: the outermost folder along it
// that is an archive's file, and path's place in it, '/' between its
// folders, and "" for the archive's top. With self, path may be the
// archive itself, as a folder to list is. ok is false for a path in no
// archive.
func (a *archives) split(p string, self bool) (archive, inner string, ok bool) {
	ps := a.Paths()
	var named []string
	for at := p; ; {
		if (self || at != p) && browsable(ps.Base(at)) {
			named = append(named, at)
		}
		up := ps.Dir(at)
		if up == at || up == "." || up == "" {
			break
		}
		at = up
	}
	for i := len(named) - 1; i >= 0; i-- {
		at := named[i]
		if !a.isArchive(at) {
			continue
		}
		rel, err := ps.Rel(at, p)
		if err != nil {
			return "", "", false
		}
		if !ps.slash() {
			rel = strings.ReplaceAll(rel, `\`, "/")
		}
		if rel == "." {
			rel = ""
		}
		return at, rel, true
	}
	return "", "", false
}

// isArchive reports whether the file at p, named as an archive is, is
// one: a file, and not a folder of that name.
func (a *archives) isArchive(p string) bool {
	a.mu.Lock()
	seen, ok := a.files[p]
	a.mu.Unlock()
	if ok && time.Since(seen.when) < heldFresh {
		return seen.archive
	}
	info, err := a.FS.Stat(p)
	is := err == nil && info.Mode().IsRegular()
	a.mu.Lock()
	if len(a.files) > 256 {
		clear(a.files)
	}
	a.files[p] = fileSeen{archive: is, when: time.Now()}
	a.mu.Unlock()
	return is
}

// inArchive reports whether path is in an archive, or with self is one,
// and names the archive.
func (a *archives) inArchive(p string, self bool) (string, bool) {
	at, _, ok := a.split(p, self)
	return at, ok
}

// hold returns the archive at p, read anew where its file changed, with
// a use counted that release gives back.
func (a *archives) hold(p string) (*heldZip, error) {
	a.mu.Lock()
	for i, h := range a.held {
		if h.path != p {
			continue
		}
		if time.Since(h.checked) >= heldFresh {
			a.mu.Unlock()
			info, err := a.FS.Stat(p)
			a.mu.Lock()
			if err != nil || info.Size() != h.size || !info.ModTime().Equal(h.mod) {
				a.drop(h)
				break
			}
			h.checked = time.Now()
		}
		// The one used last goes first.
		copy(a.held[1:i+1], a.held[:i])
		a.held[0] = h
		h.users++
		a.mu.Unlock()
		return h, nil
	}
	a.mu.Unlock()
	h, err := a.read(p)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.held = append([]*heldZip{h}, a.held...)
	for len(a.held) > heldMost {
		a.drop(a.held[len(a.held)-1])
	}
	h.users++
	return h, nil
}

// release gives back a use hold counted.
func (a *archives) release(h *heldZip) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h.users--
	if h.gone && h.users == 0 {
		_ = h.file.Close()
	}
}

// drop lets h go, closing its file once nothing reads it. a.mu is held.
func (a *archives) drop(h *heldZip) {
	a.held = slices.DeleteFunc(a.held, func(o *heldZip) bool { return o == h })
	h.gone = true
	if h.users == 0 {
		_ = h.file.Close()
	}
}

// forget lets go of the archive at p, and of what is known of whether p
// is one, as it was changed through the window.
func (a *archives) forget(p string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.files, p)
	for _, h := range a.held {
		if h.path == p {
			a.drop(h)
			return
		}
	}
}

// read opens the archive at p and reads its index.
func (a *archives) read(p string) (*heldZip, error) {
	info, err := a.FS.Stat(p)
	if err != nil {
		return nil, err
	}
	f, err := a.FS.Open(p)
	if err != nil {
		return nil, err
	}
	ra := &seekReaderAt{rs: f}
	zr, err := zip.NewReader(ra, info.Size())
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s cannot be read as a zip: %w", a.Paths().Show(p), err)
	}
	h := &heldZip{path: p, size: info.Size(), mod: info.ModTime(), checked: time.Now(), file: f, ra: ra, zr: zr,
		nodes: map[string]*zipNode{"": {dir: true, mod: info.ModTime()}}}
	if a.foldsCase() {
		h.fold = map[string]string{"": ""}
	}
	for _, zf := range zr.File {
		name := strings.ReplaceAll(zf.Name, `\`, "/")
		dir := strings.HasSuffix(name, "/") || zf.Mode().IsDir()
		// A name cannot lead out of the archive, nor start at the top.
		name = strings.TrimPrefix(path.Clean("/"+name), "/")
		if name == "" {
			continue
		}
		if zipcrypt.Encrypted(zf) && !dir {
			h.locked = true
		}
		n := h.node(name, true, info.ModTime())
		// A file of the name of a folder the archive also has is a folder.
		n.dir = dir || len(n.kids) > 0
		n.mod = zf.Modified
		if !n.dir {
			n.f = zf
		}
	}
	return h, nil
}

// foldsCase reports whether the file system's names ignore case.
func (a *archives) foldsCase() bool {
	ps := a.Paths()
	return ps.Same("A", "a")
}

// node returns what is at name in h, made with the folders along it
// where create is set and it is not there.
func (h *heldZip) node(name string, create bool, mod time.Time) *zipNode {
	if n, ok := h.nodes[name]; ok {
		return n
	}
	if h.fold != nil {
		if spelled, ok := h.fold[strings.ToLower(name)]; ok {
			return h.nodes[spelled]
		}
	}
	if !create {
		return nil
	}
	up, base := "", name
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		up, base = name[:i], name[i+1:]
	}
	parent := h.node(up, true, mod)
	// What holds something is a folder.
	parent.dir, parent.f = true, nil
	n := &zipNode{name: base, dir: true, mod: mod}
	parent.kids = append(parent.kids, n)
	h.nodes[name] = n
	if h.fold != nil {
		h.fold[strings.ToLower(name)] = name
	}
	return n
}

// lookup returns what is at path, in an archive or not. n is nil for a
// path in no archive; where it is in one, h is held, for the caller to
// release.
func (a *archives) lookup(p string, self bool) (h *heldZip, n *zipNode, err error) {
	at, inner, ok := a.split(p, self)
	if !ok {
		return nil, nil, nil
	}
	h, err = a.hold(at)
	if err != nil {
		return nil, nil, err
	}
	if n = h.node(inner, false, time.Time{}); n == nil {
		a.release(h)
		return nil, nil, fs.ErrNotExist
	}
	return h, n, nil
}

// ReadDir implements [FS]: the folder at dir, or what the archive at dir
// or a folder in one holds.
func (a *archives) ReadDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	h, n, err := a.lookup(dir, true)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: err}
	}
	if n == nil {
		return a.FS.ReadDir(ctx, dir)
	}
	defer a.release(h)
	if !n.dir {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: errors.New("not a folder")}
	}
	out := make([]fs.DirEntry, len(n.kids))
	for i, k := range n.kids {
		out[i] = zipDirEntry{zipInfo{k}}
	}
	return out, nil
}

// Stat implements [FS]. An archive itself is a file.
func (a *archives) Stat(p string) (fs.FileInfo, error) {
	h, n, err := a.lookup(p, false)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: err}
	}
	if n == nil {
		return a.FS.Stat(p)
	}
	a.release(h)
	return zipInfo{n}, nil
}

// Lstat implements [FS].
func (a *archives) Lstat(p string) (fs.FileInfo, error) {
	h, n, err := a.lookup(p, false)
	if err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: p, Err: err}
	}
	if n == nil {
		return a.FS.Lstat(p)
	}
	a.release(h)
	return zipInfo{n}, nil
}

// Open implements [FS]: a file in an archive opens with the archive's
// password where one protects it, and seeks, reading again from the
// start to go back where it is compressed.
func (a *archives) Open(p string) (io.ReadSeekCloser, error) {
	h, n, err := a.lookup(p, false)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: p, Err: err}
	}
	if n == nil {
		return a.FS.Open(p)
	}
	if n.dir {
		a.release(h)
		return nil, &fs.PathError{Op: "open", Path: p, Err: errors.New("it is a folder")}
	}
	f := n.f
	if f.Method == zip.Store && !zipcrypt.Encrypted(f) {
		if off, err := f.DataOffset(); err == nil {
			return &heldSection{SectionReader: io.NewSectionReader(h.ra, off, int64(f.CompressedSize64)), a: a, h: h}, nil
		}
	}
	pw := ""
	if zipcrypt.Encrypted(f) {
		got, ok := a.keys.get(a.ID(), h.path)
		if !ok {
			a.release(h)
			return nil, &fs.PathError{Op: "open", Path: p, Err: fmt.Errorf("%s %w", a.Paths().Base(h.path), errLocked)}
		}
		pw = got
	}
	r := &zipEntryReader{open: func() (io.ReadCloser, error) { return zipcrypt.Open(f, pw) }, size: int64(f.UncompressedSize64), a: a, h: h}
	if err := r.reopen(); err != nil {
		r.release()
		return nil, &fs.PathError{Op: "open", Path: p, Err: err}
	}
	return r, nil
}

// heldSection is a file stored in an archive as it is, read straight
// from it.
type heldSection struct {
	*io.SectionReader
	a    *archives
	h    *heldZip
	once sync.Once
}

// Close implements [io.Closer].
func (s *heldSection) Close() error {
	s.once.Do(func() { s.a.release(s.h) })
	return nil
}

// zipEntryReader is a compressed file of an archive, which seeks by
// reading on, or from its start again.
type zipEntryReader struct {
	open func() (io.ReadCloser, error)
	rc   io.ReadCloser
	pos  int64
	size int64
	a    *archives
	h    *heldZip
	once sync.Once
}

// reopen reads the file from its start again.
func (r *zipEntryReader) reopen() error {
	if r.rc != nil {
		_ = r.rc.Close()
	}
	rc, err := r.open()
	if err != nil {
		r.rc = nil
		return err
	}
	r.rc, r.pos = rc, 0
	return nil
}

// Read implements [io.Reader].
func (r *zipEntryReader) Read(p []byte) (int, error) {
	if r.rc == nil {
		if err := r.reopen(); err != nil {
			return 0, err
		}
	}
	n, err := r.rc.Read(p)
	r.pos += int64(n)
	return n, err
}

// Seek implements [io.Seeker].
func (r *zipEntryReader) Seek(offset int64, whence int) (int64, error) {
	to := offset
	switch whence {
	case io.SeekCurrent:
		to += r.pos
	case io.SeekEnd:
		to += r.size
	}
	if to < 0 {
		return 0, errors.New("seek to before the start")
	}
	if to < r.pos || r.rc == nil {
		if err := r.reopen(); err != nil {
			return 0, err
		}
	}
	if to > r.pos {
		n, err := io.CopyN(io.Discard, r.rc, to-r.pos)
		r.pos += n
		if err != nil && !errors.Is(err, io.EOF) {
			return r.pos, err
		}
	}
	return r.pos, nil
}

// Close implements [io.Closer].
func (r *zipEntryReader) Close() error {
	var err error
	if r.rc != nil {
		err = r.rc.Close()
		r.rc = nil
	}
	r.release()
	return err
}

func (r *zipEntryReader) release() { r.once.Do(func() { r.a.release(r.h) }) }

// readOnly returns the error of a change at p, where p is in an archive,
// and nil where it is not.
func (a *archives) readOnly(op, p string) error {
	if at, ok := a.inArchive(p, false); ok {
		return &fs.PathError{Op: op, Path: p, Err: fmt.Errorf("%w: %s", ErrInArchive, a.Paths().Base(at))}
	}
	return nil
}

// Create implements [FS].
func (a *archives) Create(p string) (io.WriteCloser, error) {
	if err := a.readOnly("create", p); err != nil {
		return nil, err
	}
	a.forget(p)
	return a.FS.Create(p)
}

// Mkdir implements [FS].
func (a *archives) Mkdir(p string, perm fs.FileMode) error {
	if err := a.readOnly("mkdir", p); err != nil {
		return err
	}
	return a.FS.Mkdir(p, perm)
}

// Rename implements [FS].
func (a *archives) Rename(from, to string) error {
	if err := errors.Join(a.readOnly("rename", from), a.readOnly("rename", to)); err != nil {
		return err
	}
	a.forget(from)
	a.forget(to)
	return a.FS.Rename(from, to)
}

// Remove implements [FS].
func (a *archives) Remove(p string) error {
	if err := a.readOnly("remove", p); err != nil {
		return err
	}
	a.forget(p)
	return a.FS.Remove(p)
}

// locked reports whether a password protects some of the archive at p,
// which no password given yet opens.
func (a *archives) locked(p string) bool {
	h, err := a.hold(p)
	if err != nil {
		return false
	}
	defer a.release(h)
	if !h.locked {
		return false
	}
	_, ok := a.keys.get(a.ID(), p)
	return !ok
}

// unlock tries pw on the archive at p, reading through the smallest of
// the files it protects, and keeps it where that checks. It returns
// zipcrypt.ErrPassword for a password that is wrong.
func (a *archives) unlock(p, pw string) error {
	h, err := a.hold(p)
	if err != nil {
		return err
	}
	defer a.release(h)
	var smallest *zip.File
	for _, f := range h.zr.File {
		if zipcrypt.Encrypted(f) && !strings.HasSuffix(f.Name, "/") &&
			(smallest == nil || f.CompressedSize64 < smallest.CompressedSize64) {
			smallest = f
		}
	}
	if smallest != nil {
		rc, err := zipcrypt.Open(smallest, pw)
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, rc)
		err = errors.Join(err, rc.Close())
		if errors.Is(err, zipcrypt.ErrDamaged) {
			// Its first bytes passed by chance; the whole of it did not.
			return zipcrypt.ErrPassword
		}
		if err != nil {
			return err
		}
	}
	a.keys.set(a.ID(), p, pw)
	return nil
}

// The optional interfaces, for the archive's paths and passed on for the
// others. [optional] hands out only those the file system under has.

// Trash implements [Trasher].
func (a *archives) Trash(p string) (string, error) {
	if err := a.readOnly("trash", p); err != nil {
		return "", err
	}
	a.forget(p)
	return under[Trasher](a).Trash(p)
}

// Restore implements [Trasher].
func (a *archives) Restore(original, trashed string, at time.Time, to string) error {
	if err := a.readOnly("restore", to); err != nil {
		return err
	}
	return under[Trasher](a).Restore(original, trashed, at, to)
}

// Describe implements [Trasher].
func (a *archives) Describe(trashed string) string { return under[Trasher](a).Describe(trashed) }

// Space implements [SpaceReporter]: that of the volume an archive is on.
func (a *archives) Space(p string) (free, total uint64, err error) {
	if at, ok := a.inArchive(p, true); ok {
		p = a.Paths().Dir(at)
	}
	return under[SpaceReporter](a).Space(p)
}

// Volume implements [VolumeNamer].
func (a *archives) Volume(p string) (string, error) {
	if at, ok := a.inArchive(p, false); ok {
		p = at
	}
	return under[VolumeNamer](a).Volume(p)
}

// Readlink implements [Linker]. An archive holds no link to follow.
func (a *archives) Readlink(p string) (string, error) {
	if _, ok := a.inArchive(p, false); ok {
		return "", &fs.PathError{Op: "readlink", Path: p, Err: errors.New("not a link")}
	}
	return under[Linker](a).Readlink(p)
}

// Symlink implements [Linker].
func (a *archives) Symlink(target, p string) error {
	if err := a.readOnly("symlink", p); err != nil {
		return err
	}
	return under[Linker](a).Symlink(target, p)
}

// EvalSymlinks implements [Linker]: those along the way to an archive.
func (a *archives) EvalSymlinks(p string) (string, error) {
	at, inner, ok := a.split(p, true)
	if !ok {
		return under[Linker](a).EvalSymlinks(p)
	}
	followed, err := under[Linker](a).EvalSymlinks(at)
	if err != nil {
		return "", err
	}
	return a.under(followed, inner), nil
}

// Chmod implements [Stamper].
func (a *archives) Chmod(p string, mode fs.FileMode) error {
	if err := a.readOnly("chmod", p); err != nil {
		return err
	}
	return under[Stamper](a).Chmod(p, mode)
}

// Chtimes implements [Stamper].
func (a *archives) Chtimes(p string, mod time.Time) error {
	if err := a.readOnly("chtimes", p); err != nil {
		return err
	}
	return under[Stamper](a).Chtimes(p, mod)
}

// SameFile implements [SameFiler].
func (a *archives) SameFile(x, y fs.FileInfo) bool {
	xi, xok := x.(zipInfo)
	yi, yok := y.(zipInfo)
	switch {
	case xok && yok:
		return xi.n == yi.n
	case xok || yok:
		return false
	}
	return under[SameFiler](a).SameFile(x, y)
}

// Hidden implements [HiddenReporter].
func (a *archives) Hidden(info fs.FileInfo) bool {
	if _, ok := info.(zipInfo); ok {
		return false
	}
	return under[HiddenReporter](a).Hidden(info)
}

// OnlineOnly implements [OnlineReporter].
func (a *archives) OnlineOnly(info fs.FileInfo) bool {
	if _, ok := info.(zipInfo); ok {
		return false
	}
	return under[OnlineReporter](a).OnlineOnly(info)
}

// SystemThumb implements [OnlineReporter].
func (a *archives) SystemThumb(p string, size int) (image.Image, error) {
	if _, ok := a.inArchive(p, false); ok {
		return nil, nil
	}
	return under[OnlineReporter](a).SystemThumb(p, size)
}

// Cloud implements [CloudReporter].
func (a *archives) Cloud(dir string, info fs.FileInfo) CloudState {
	if _, ok := info.(zipInfo); ok {
		return CloudNone
	}
	return under[CloudReporter](a).Cloud(dir, info)
}

// Times implements [TimesReporter].
func (a *archives) Times(info fs.FileInfo) (created, accessed time.Time, ok bool) {
	if _, in := info.(zipInfo); in {
		return time.Time{}, time.Time{}, false
	}
	return under[TimesReporter](a).Times(info)
}

// Attrs implements [Attributer].
func (a *archives) Attrs(p string) (readOnly, hidden, ok bool, err error) {
	if _, in := a.inArchive(p, false); in {
		return false, false, false, nil
	}
	return under[Attributer](a).Attrs(p)
}

// SetAttrs implements [Attributer].
func (a *archives) SetAttrs(p string, readOnly, hidden bool) error {
	if err := a.readOnly("setattrs", p); err != nil {
		return err
	}
	return under[Attributer](a).SetAttrs(p, readOnly, hidden)
}

// OpenInSystem implements [SystemOpener]. What an archive holds does not
// open there: the window fetches it first.
func (a *archives) OpenInSystem(p string) error {
	if _, ok := a.inArchive(p, false); ok {
		return errors.ErrUnsupported
	}
	return under[SystemOpener](a).OpenInSystem(p)
}

// RevealInSystem implements [SystemOpener]: an item of an archive shows
// as the archive.
func (a *archives) RevealInSystem(p string) error {
	if at, ok := a.inArchive(p, false); ok {
		p = at
	}
	return under[SystemOpener](a).RevealInSystem(p)
}

// TrueCase implements [TrueCaser]: the way to an archive as the file
// system spells it, and what is in it as the archive does.
func (a *archives) TrueCase(p string) (string, error) {
	at, inner, ok := a.split(p, true)
	if !ok {
		return under[TrueCaser](a).TrueCase(p)
	}
	spelled, err := under[TrueCaser](a).TrueCase(at)
	if err != nil {
		return "", err
	}
	if h, err := a.hold(at); err == nil {
		if in, ok := h.fold[strings.ToLower(inner)]; ok {
			inner = in
		}
		a.release(h)
	}
	return a.under(spelled, inner), nil
}

// under returns the path of inner, a path in the archive at archive.
func (a *archives) under(archive, inner string) string {
	if inner == "" {
		return archive
	}
	return a.Paths().Join(append([]string{archive}, strings.Split(inner, "/")...)...)
}
