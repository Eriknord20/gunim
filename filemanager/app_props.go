package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/marrasen/gunim"
)

// properties shows the Properties dialog of the items selected, or of
// the folder showing when none is selected.
func (a *app) properties() {
	paths := a.selectedPaths()
	if len(paths) == 0 {
		paths = []string{a.nav.path}
	}
	fsys := a.fs
	go func() {
		p, count, err := readProps(fsys, paths)
		a.post(func() {
			if err != nil {
				a.fail("Reading the properties: " + err.Error())
				return
			}
			a.showProps(paths, p, count)
		})
	}()
}

// readProps reads what the Properties dialog says of paths on fsys, and
// whether what they hold is still to be counted.
func readProps(fsys FS, paths []string) (p Props, count bool, err error) {
	ps := fsys.Paths()
	p.Location = ps.Show(ps.Dir(paths[0]))
	if len(paths) > 1 {
		dirs := 0
		for _, path := range paths {
			info, lerr := fsys.Lstat(path)
			if lerr != nil {
				return p, false, lerr
			}
			if info.IsDir() {
				dirs++
			}
		}
		p.Title = "Properties of " + plural(len(paths), "item")
		p.Name = plural(len(paths), "item")
		switch dirs {
		case 0:
			p.Type = "Files"
		case len(paths):
			p.Type = "Folders"
		default:
			p.Type = "Files and folders"
		}
		p.Counting = true
		return p, true, nil
	}
	path := paths[0]
	info, err := fsys.Lstat(path)
	if err != nil {
		return p, false, err
	}
	e, err := statEntry(fsys, path)
	if err != nil {
		return p, false, err
	}
	p.Name, p.Title = ps.placeName(path), "Properties of "+ps.placeName(path)
	p.Type = e.Type
	if e.Dir {
		p.Counting = true
	} else {
		p.Size = sizeWords(info.Size())
	}
	p.Modified = fmtTime(info.ModTime())
	if tr, ok := fsys.(TimesReporter); ok {
		if created, accessed, ok := tr.Times(info); ok {
			if !created.IsZero() {
				p.Created = fmtTime(created)
			}
			p.Accessed = fmtTime(accessed)
		}
	}
	if at, ok := fsys.(Attributer); ok {
		if ro, hidden, ok, err := at.Attrs(path); err != nil {
			return p, false, err
		} else if ok {
			p.Attrs, p.ReadOnly, p.Hidden = true, ro, hidden
		}
	}
	return p, p.Counting, nil
}

// sizeWords says a size the way people read it, with the bytes exact.
func sizeWords(n int64) string {
	if n < 1024 {
		return humanBytes(n)
	}
	return fmt.Sprintf("%s (%s bytes)", humanBytes(n), count(int(n)))
}

// showProps shows the Properties dialog p of paths, and counts what they
// hold while it shows, when count is set.
func (a *app) showProps(paths []string, p Props, count bool) {
	a.ops.tokens++
	p.Token = a.ops.tokens
	ctx, cancel := context.WithCancel(a.ctx)
	path, was := paths[0], p
	d := &dialog{view: "props", state: p}
	d.answer = func(in gunim.Intent) {
		cancel()
		v, ok := in.(PropsApplied)
		if !ok || v.Token != was.Token || !was.Attrs || (v.ReadOnly == was.ReadOnly && v.Hidden == was.Hidden) {
			return
		}
		a.applyAttrs(path, v.ReadOnly, v.Hidden)
	}
	a.showDialog(d)
	if count {
		a.countProps(ctx, d, paths)
	}
}

// countProps counts what paths hold, and tells dialog d as it goes.
func (a *app) countProps(ctx context.Context, d *dialog, paths []string) {
	fsys := a.fs
	go func() {
		var items int
		var size int64
		last := time.Now()
		send := func(counting bool, err error) {
			c := PropsCounted{Size: sizeWords(size), Holds: plural(items, "item"), Counting: counting}
			if err != nil {
				c.Err = err.Error()
			}
			a.post(func() {
				p, ok := d.state.(Props)
				if !ok {
					return
				}
				c.Token = p.Token
				p.Size, p.Holds, p.Counting, p.Err = c.Size, c.Holds, c.Counting, c.Err
				d.state = p
				a.logShown(c)
				if len(a.ops.dialogs) > 0 && a.ops.dialogs[0] == d {
					a.send(a.c.Patch(string(d.id), c))
				}
			})
		}
		var err error
		for _, root := range paths {
			err = walkTree(ctx, fsys, root, func(p string, e fs.DirEntry, err error) error {
				if err != nil {
					return fmt.Errorf("reading %s: %w", p, err)
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if p != root || len(paths) > 1 {
					items++
				}
				if e.Type().IsRegular() {
					info, err := e.Info()
					if err != nil {
						return fmt.Errorf("reading %s: %w", p, err)
					}
					size += info.Size()
				}
				if time.Since(last) >= countEvery {
					last = time.Now()
					send(true, nil)
				}
				return nil
			})
			if err != nil {
				break
			}
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		send(false, err)
	}()
}

// errNoAttrs says a file system has no attributes to set.
var errNoAttrs = errors.New("the items here have no read-only and hidden attributes")

// applyAttrs sets the read-only and hidden attributes of path.
func (a *app) applyAttrs(path string, readOnly, hidden bool) {
	fsys := a.fs
	go func() {
		err := errNoAttrs
		if at, ok := fsys.(Attributer); ok {
			err = at.SetAttrs(path, readOnly, hidden)
		}
		a.post(func() {
			if err != nil {
				a.showError(ErrorBox{Title: "The attributes were not changed", Body: err.Error()})
				return
			}
			a.patch(Notice{Title: "Changed the attributes of " + a.ps.placeName(path), Kind: "success"})
			a.relist()
		})
	}()
}
