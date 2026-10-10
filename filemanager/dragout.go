package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The limits of dragging files of another file system out to the
// computer's programs: variables, for tests.
var (
	// dragOutMax is how many bytes a drag may fetch to carry out, and
	// dragOutMany how many files: past either it is refused, as a drag
	// cannot wait long, nor ask.
	dragOutMax  int64 = 500 << 20
	dragOutMany       = 10000
)

// errTooBig says a drag holds too much to fetch for other programs.
var errTooBig = errors.New("too big to drag out")

// fetchItem is an item a drag fetches: a file, or a folder and all it
// holds, which go to files and folders by the same names.
type fetchItem struct {
	remoteFile
	dir bool
	// files are a folder's files, and dirs its folders, each by its path
	// on the file system and its path in the folder, written with '/'.
	files []treeFile
	dirs  []string
}

// treeFile is a file in a folder a drag fetches: rel is its path in the
// folder, written with '/'.
type treeFile struct {
	remoteFile
	rel string
}

// fetchForDrag fetches the items of drag v, of the window's file system,
// to this computer, for the drag to carry out to other programs: files
// to the folder of copies fetched to open, a copy unchanged since it was
// fetched taken as it is, and folders with all they hold. It runs
// quietly, as an operation that shows only once it takes a while, and
// tells the window where the copies are, or why there are none.
func (a *app) fetchForDrag(v DragFetch) {
	if !a.fetches() || len(v.Paths) == 0 {
		a.patch(DragFetched{ID: v.ID, Err: "Cannot drag out"})
		return
	}
	fsys, copies := a.fs, a.hub.openCopies()
	ps := fsys.Paths()
	what := ps.Base(v.Paths[0])
	if len(v.Paths) > 1 {
		what = plural(len(v.Paths), "item")
	}
	id, ctx, cancel := a.newOp("Fetching "+what+" to drag out", OpCopy)
	d := &a.dnd
	if d.fetches == nil {
		d.fetches = map[int]context.CancelFunc{}
	}
	d.fetches[v.ID] = cancel
	paths := v.Paths
	a.ops.wg.Go(func() {
		local, err := fetchOut(ctx, fsys, copies, a, paths, func(p progress) {
			a.post(func() {
				a.progressed(id, p)
				a.patch(DragFetching{ID: v.ID, Bytes: p.bytes, Total: p.bytesTotal})
			})
		})
		stopped := ctx.Err() != nil
		a.post(func() {
			delete(d.fetches, v.ID)
			a.fetchedForDrag(id, v.ID, what, local, err, stopped)
			a.hub.watchCopies()
		})
	})
}

// dragFetchEnded stops the fetch of drag id, which has ended, where it
// still runs.
func (a *app) dragFetchEnded(id int) {
	if cancel, ok := a.dnd.fetches[id]; ok {
		cancel()
	}
}

// fetchedForDrag takes the end of operation id, which fetched what to
// local for the drag of ID drag, or stopped short with err. It tells the window,
// and shows nothing more unless the operation showed, as only a fetch
// that took a while does.
func (a *app) fetchedForDrag(id, drag int, what string, local []string, err error, stopped bool) {
	if r, ok := a.ops.running[id]; err != nil && !stopped && !errors.Is(err, context.Canceled) && (!ok || !r.visible) {
		// The drop hint says only that it failed, and the operation never
		// showed to say why.
		a.logLine("Fetching "+what+" to drag out", err.Error())
	}
	switch {
	case err == nil:
		a.patch(DragFetched{ID: drag, Paths: local})
	case errors.Is(err, errTooBig):
		a.patch(DragFetched{ID: drag, Err: "Too big to drag out"})
	default:
		a.patch(DragFetched{ID: drag, Err: "Cannot fetch " + what})
	}
	r, ok := a.ops.running[id]
	if !ok {
		return
	}
	delete(a.ops.running, id)
	r.cancel()
	a.dropDialogs(id)
	if !r.visible {
		return
	}
	a.patch(OpDone{ID: id, OK: err == nil})
	if err == nil {
		r.title = "Fetched " + what
		a.cheer(id, r)
	}
	a.publishOps()
	if err != nil && !stopped && !errors.Is(err, context.Canceled) {
		a.fail("Fetching " + what + " to drag out: " + err.Error() + ".")
	}
}

// fetchOut fetches the items at paths on fsys into copies, for owner,
// and returns where each is on this computer, in the order of paths.
// report hears how far it has got. It fetches nothing where the items
// hold more than a drag may carry.
func fetchOut(ctx context.Context, fsys FS, copies *openCopies, owner *app, paths []string, report func(progress)) ([]string, error) {
	items, total, count, err := listOut(ctx, fsys, paths)
	if err != nil {
		return nil, err
	}
	var done int64
	n := 0
	var last time.Time
	tell := func(current string, now bool, k int64) {
		if t := time.Now(); now || t.Sub(last) >= 50*time.Millisecond {
			last = t
			report(progress{items: n, itemsTotal: count, bytes: done + k, bytesTotal: total, current: lastName(current)})
		}
	}
	local := make([]string, 0, len(items))
	for _, it := range items {
		if !it.dir {
			l, ok := copies.lookup(fsys.ID(), it.remoteFile)
			if !ok {
				tell(it.path, true, 0)
				l, err = copies.fetch(ctx, fsys, it.remoteFile, owner, func(k int64, now bool) { tell(it.path, now, k) })
				if err != nil {
					return nil, err
				}
			}
			done += it.size
			n++
			local = append(local, l)
			continue
		}
		l, err := copies.fetchTree(ctx, fsys, it, func(f treeFile, k int64, now bool) {
			tell(f.path, now, k)
		}, func(f treeFile) {
			done += f.size
			n++
		})
		if err != nil {
			return nil, err
		}
		local = append(local, l)
	}
	tell("", true, 0)
	return local, nil
}

// listOut reads what the items at paths on fsys hold, all the way down
// their folders, and returns them with their bytes and their files in
// all. It stops with errTooBig once they pass dragOutMax or dragOutMany.
// Links are followed to files, and not to folders, which could lead
// round in a circle.
func listOut(ctx context.Context, fsys FS, paths []string) (items []fetchItem, total int64, count int, err error) {
	ps := fsys.Paths()
	add := func(size int64) error {
		total += size
		count++
		if total > dragOutMax || count > dragOutMany {
			return errTooBig
		}
		return nil
	}
	for _, p := range paths {
		info, err := fsys.Stat(p)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("reading %s: %w", ps.Show(p), err)
		}
		it := fetchItem{remoteFile: remoteFile{path: p, size: info.Size(), mod: info.ModTime()}, dir: info.IsDir()}
		if !it.dir {
			if err := add(it.size); err != nil {
				return nil, 0, 0, err
			}
			items = append(items, it)
			continue
		}
		var walk func(dir, rel string) error
		walk = func(dir, rel string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			es, err := fsys.ReadDir(ctx, dir)
			if err != nil {
				return fmt.Errorf("reading %s: %w", ps.Show(dir), err)
			}
			for _, e := range es {
				at, r := ps.Join(dir, e.Name()), e.Name()
				if rel != "" {
					r = rel + "/" + e.Name()
				}
				info, err := e.Info()
				if err != nil {
					return fmt.Errorf("reading %s: %w", ps.Show(at), err)
				}
				if info.Mode()&fs.ModeSymlink != 0 {
					if info, err = fsys.Stat(at); err != nil || info.IsDir() {
						// A link to nowhere, or to a folder, stays behind.
						continue
					}
				}
				switch {
				case info.IsDir():
					it.dirs = append(it.dirs, r)
					if err := walk(at, r); err != nil {
						return err
					}
				case info.Mode().IsRegular():
					if err := add(info.Size()); err != nil {
						return err
					}
					it.files = append(it.files, treeFile{remoteFile: remoteFile{path: at, size: info.Size(), mod: info.ModTime()}, rel: r})
				}
			}
			return nil
		}
		if err := walk(p, ""); err != nil {
			return nil, 0, 0, err
		}
		items = append(items, it)
	}
	return items, total, count, nil
}

// fetchTree fetches the folder it, with all it holds, into the hub's
// folder of copies, under its own name, and returns where it is. A
// folder fetched before is brought up to date: a file the same size and
// time as before stays as it is, and what the folder no longer holds
// goes. report hears the bytes of file f copied so far, and done each
// file once it is here.
func (c *openCopies) fetchTree(ctx context.Context, fsys FS, it fetchItem, report func(f treeFile, n int64, now bool), done func(f treeFile)) (string, error) {
	base, err := c.folder()
	if err != nil {
		return "", fmt.Errorf("making a temporary folder: %w", err)
	}
	top := filepath.Join(base, shortHash(fsys.ID()), "tree-"+shortHash(it.path), localName(fsys.Paths().Base(it.path)))
	if err := os.MkdirAll(top, 0o700); err != nil {
		return "", fmt.Errorf("making a temporary folder: %w", err)
	}
	keep := map[string]bool{top: true}
	// where is the path on this computer of rel, a path in the folder.
	where := func(rel string) string {
		p := top
		for _, name := range strings.Split(rel, "/") {
			p = filepath.Join(p, localName(name))
		}
		return p
	}
	for _, d := range it.dirs {
		p := where(d)
		keep[p] = true
		if err := os.MkdirAll(p, 0o700); err != nil {
			return "", fmt.Errorf("making a temporary folder: %w", err)
		}
	}
	for _, f := range it.files {
		p := where(f.rel)
		keep[p] = true
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Size() == f.size && info.ModTime().Equal(f.mod) {
			done(f)
			continue
		}
		if err := copyOut(ctx, fsys, f.path, p, func(n int64, now bool) { report(f, n, now) }); err != nil {
			return "", err
		}
		_ = os.Chtimes(p, time.Now(), f.mod)
		done(f)
	}
	// What the folder no longer holds goes, deepest first.
	var gone []string
	_ = filepath.WalkDir(top, func(p string, _ fs.DirEntry, err error) error {
		if err == nil && !keep[p] {
			gone = append(gone, p)
		}
		return nil
	})
	for i := len(gone) - 1; i >= 0; i-- {
		_ = os.RemoveAll(gone[i])
	}
	return top, nil
}
