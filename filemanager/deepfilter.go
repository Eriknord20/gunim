package filemanager

import (
	"context"
	"fmt"
	"time"
)

// deepBounds are how many items the walk for a filter that looks in the
// folders under the one showing takes at most, and for how long it
// walks.
var deepBounds = bounds{items: 200_000, time: 30 * time.Second}

// deepState is the tree under the folder showing, walked for a filter
// that looks in the folders under it too.
type deepState struct {
	// on says the filter looks in the folders under the one showing; it
	// does so only while it has text.
	on bool
	// root is the folder walked, and all the items under it, each named
	// by its path under root, in the folder's sort.
	root string
	all  []entry
	// next gathers a walk done again, as after a refresh, whose items
	// take the place of all once it ends, so the rows do not empty and
	// fill again.
	next  []entry
	again bool
	// walking is set while the walk runs, and stopped says why it
	// stopped early.
	walking bool
	stopped string
	errs    []walkError
	gen     int
	cancel  context.CancelFunc
	// shown is when the rows last took in what the walk found.
	shown time.Time
}

// stop stops the walk.
func (d *deepState) stop() {
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.walking = false
}

// forget stops the walk and drops what it found.
func (d *deepState) forget() {
	d.stop()
	d.gen++
	d.root, d.all, d.next, d.again, d.stopped, d.errs = "", nil, nil, false, "", nil
}

// deep reports whether the rows come from the tree under the folder
// showing: the filter looks there, and has text.
func (a *app) deep() bool { return a.tree.on && a.nav.filter != "" }

// entries are what the rows are filtered from: the folder's items, or
// those of the tree under it.
func (a *app) entries() []entry {
	if a.deep() {
		return a.tree.all
	}
	return a.nav.all
}

// keepDeep starts the walk of the tree under the folder showing when the
// filter has come to look there, and drops the tree when it has stopped.
func (a *app) keepDeep() {
	d := &a.tree
	switch {
	case !a.deep():
		if d.root != "" {
			d.forget()
		}
		if n := &a.nav; n.sort == SortFolder {
			// The column goes with the tree.
			n.sort, n.desc = SortName, false
			sortEntries(n.all, n.sort, n.desc)
		}
	case d.root == "" || !a.ps.Same(d.root, a.nav.path):
		a.walkDeep(false)
	}
}

// walkDeep walks the tree under the folder showing in the background.
// again keeps the items found so far showing until the walk ends.
func (a *app) walkDeep(again bool) {
	d := &a.tree
	d.stop()
	ctx, cancel := context.WithCancel(a.ctx)
	d.gen++
	again = again && d.root != "" && a.ps.Same(d.root, a.nav.path)
	if !again {
		d.all = nil
	}
	d.root, d.next, d.again, d.walking, d.stopped, d.errs, d.cancel = a.nav.path, nil, again, true, "", nil, cancel
	gen, fsys, root := d.gen, a.fs, d.root
	go walkEntries(ctx, fsys, root, deepBounds, func(es []entry, errs []walkError, stopped string, done bool) {
		a.post(func() { a.walked(gen, es, errs, stopped, done) })
	})
}

// walked takes a batch of the walk gen.
func (a *app) walked(gen int, es []entry, errs []walkError, stopped string, done bool) {
	d := &a.tree
	if gen != d.gen {
		return
	}
	a.nameIcons(d.root, es)
	d.errs = append(d.errs, errs...)
	if d.again {
		d.next = append(d.next, es...)
	} else {
		d.all = append(d.all, es...)
	}
	if done {
		d.stop()
		d.stopped = stopped
		if d.again {
			d.all, d.next, d.again = d.next, nil, false
		}
		if len(d.errs) > 0 {
			// Once for the walk: there may be a great many.
			a.logLine("Filtering under "+a.ps.Show(d.root), plural(len(d.errs), "folder")+" could not be read, the first "+
				a.ps.Show(a.ps.Join(d.root, d.errs[0].rel))+": "+d.errs[0].err.Error())
		}
	}
	// The rows take the items in as they come, at most every few
	// reports, as sorting and filtering them all again costs more as
	// they grow.
	if done || !d.again && time.Since(d.shown) > 4*indexReport {
		d.shown = time.Now()
		sortEntries(d.all, a.nav.sort, a.nav.desc)
		a.refilter()
	}
}

// walkEntries walks the tree under root on fsys, a folder at a time,
// shallow items first, and hands tell what it found every indexReport,
// stopping at the bounds. Each item is named by its path under root, and
// is hidden when a folder it is in is. It does not go into links. A
// folder it cannot read goes to tell as an error.
func walkEntries(ctx context.Context, fsys FS, root string, b bounds, tell func([]entry, []walkError, string, bool)) {
	ps := fsys.Paths()
	type folder struct {
		rel    string
		hidden bool
	}
	start := time.Now()
	last := start
	var batch []entry
	var errs []walkError
	seen := 0
	stopped := ""
	queue := []folder{{}}
	for len(queue) > 0 && stopped == "" {
		if ctx.Err() != nil {
			return
		}
		f := queue[0]
		queue = queue[1:]
		es, err := listDir(ctx, fsys, ps.Join(root, f.rel))
		if err != nil && ctx.Err() == nil {
			errs = append(errs, walkError{rel: f.rel, err: err})
		}
		for _, e := range es {
			name := e.Name
			e.Name = ps.Join(f.rel, name)
			e.at = len(e.Name) - len(name)
			e.Hidden = e.Hidden || f.hidden
			batch = append(batch, e)
			if e.Kind == KindFolder {
				queue = append(queue, folder{rel: e.Name, hidden: e.Hidden})
			}
			seen++
			if seen >= b.items {
				stopped = "Stopped at " + plural(seen, "item")
				break
			}
		}
		if stopped == "" && len(queue) > 0 && time.Since(start) > b.time {
			stopped = fmt.Sprintf("Stopped after %s, at %s", b.time.Round(time.Second), plural(seen, "item"))
		}
		if time.Since(last) >= indexReport {
			last = time.Now()
			tell(batch, errs, "", false)
			batch, errs = nil, nil
		}
	}
	tell(batch, errs, stopped, true)
}

// deepStatus says how far the walk has got, for the status bar, or
// nothing once it walked the whole tree.
func (a *app) deepStatus() string {
	d := &a.tree
	switch {
	case !a.deep():
		return ""
	case d.walking && !d.again:
		return "Looking through the folders under here: " + count(len(d.all)) + " items so far…"
	case d.walking:
		return "Looking through the folders under here again…"
	case d.stopped != "":
		return d.stopped + ". Type more, or filter a folder further down."
	}
	return ""
}
