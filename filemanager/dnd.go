package filemanager

import (
	"errors"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// FileDrag is what a drag of files carries: the items' paths, whether
// each is a folder, and the volume they are on. Any view of files can
// start one.
type FileDrag struct {
	Paths []string
	Dirs  []bool
	// FS is the ID of the file system the items are on: empty for the
	// computer's own, as for files from another program.
	FS string
	// Volume is the volume the items are on, and empty where it is not
	// known, as for files from another program.
	Volume string
	// Style is how the items' file system writes paths: the zero value,
	// SystemPaths, for the computer's own.
	Style PathStyle
	// scripted marks a drag a script started, which never leaves the
	// window.
	scripted bool
	// fetch, when not nil, is the copy of the items the program fetches
	// to this computer, for the drag to carry out to other programs.
	fetch *dragFetch
}

// ExportFiles implements [gunim.FileExporter]: the computer's own files
// go to other programs as they are, and those of another file system as
// the copies fetched of them. While the copies are on their way, it
// says so and the drag waits.
func (d FileDrag) ExportFiles() ([]string, error) {
	switch {
	case d.scripted:
		return nil, errScripted
	case d.FS == "":
		return d.Paths, nil
	case d.fetch == nil:
		return nil, errElsewhere
	}
	return d.fetch.export()
}

// dragFetch is the copy of a drag's items fetched to this computer, as
// the window hears of it: on its way until done, and then the copies'
// paths, or why there are none.
type dragFetch struct {
	id    int
	done  bool
	local []string
	err   string
	// bytes of total are fetched so far, where total is known.
	bytes, total int64
}

// export returns the copies' paths, or an error that says why the drag
// cannot leave yet, or at all, for the picture under the pointer to
// show.
func (f *dragFetch) export() ([]string, error) {
	switch {
	case !f.done:
		return nil, &gunim.ExportError{Hint: f.hint(), Wait: true}
	case f.err != "" || len(f.local) == 0:
		why := f.err
		if why == "" {
			why = "Nothing to drag out"
		}
		return nil, &gunim.ExportError{Hint: widget.DropHint{Text: why}, Err: errors.New(why)}
	}
	return slices.Clone(f.local), nil
}

// hint says how far the fetch has got, in words and on a bar, while it
// runs.
func (f *dragFetch) hint() widget.DropHint {
	h := widget.DropHint{Text: "Fetching…", Effect: widget.DropCopy}
	if f.total > 0 {
		h.Text = "Fetching… " + humanBytes(f.bytes) + " of " + humanBytes(f.total)
		h.Bar, h.Progress = true, float32(min(max(float64(f.bytes)/float64(f.total), 0), 1))
	}
	return h
}

// errScripted keeps a drag a script started inside the window, and
// errElsewhere a drag of files other programs cannot reach.
var (
	errScripted  = errors.New("a drag a script started stays in the window")
	errElsewhere = errors.New("files of another file system than the computer's own stay in the window")
)

// fileDragOf returns what a drop carries as files: a drag from a window
// of the app, or files from another program.
func fileDragOf(d input.Drop) (FileDrag, bool) {
	if fd, ok := d.Data.(FileDrag); ok && len(fd.Paths) > 0 {
		return fd, true
	}
	if f, ok := d.Data.(input.Files); ok && len(f.Paths) > 0 {
		return FileDrag{Paths: f.Paths}, true
	}
	if d.Data == nil && len(d.Paths) > 0 {
		return FileDrag{Paths: d.Paths}, true
	}
	return FileDrag{}, false
}

// dropPlan works out what dropping d into the folder dir, on volume vol
// of the file system of ID fs, does with mods held: a move on one volume
// and a copy across volumes, Ctrl forcing a copy and Shift a move. Items
// from another file system are copied unless Shift is held, where
// transfers says the program carries them across, and refused where it
// cannot. It returns false with the reason in the hint when the drop is
// refused.
func dropPlan(ps PathStyle, fs string, transfers bool, d FileDrag, dir, vol, volErr string, mods input.Mods) (DropFiles, widget.DropHint, bool) {
	return planDrop(dropTarget{fs: fs, ps: ps, dir: dir, name: ps.placeName(dir), vol: vol, volErr: volErr, own: true},
		transfers, d, mods)
}

// dropTarget is a folder a drop can go into: dir, called name, on the
// file system of ID fs, which writes paths as ps. own says the file
// system is the window's; a place or a favourite in the sidebar may be
// on another. vol is the folder's volume, empty where it is not known,
// and volErr why it could not be read.
type dropTarget struct {
	fs        string
	ps        PathStyle
	dir, name string
	vol       string
	volErr    string
	own       bool
}

// planDrop works out what dropping d into the folder of t does, as
// dropPlan says. Where the items and the folder are both on the
// window's file system, the window copies or moves them itself;
// anywhere else, the program carries them, where transfers says it can.
func planDrop(t dropTarget, transfers bool, d FileDrag, mods input.Mods) (DropFiles, widget.DropHint, bool) {
	across := d.FS != t.fs
	switch {
	case (across || !t.own) && !transfers:
		if !t.own {
			return DropFiles{}, widget.DropHint{Text: "Cannot drop on another file system"}, false
		}
		return DropFiles{}, widget.DropHint{Text: "Cannot drop from another file system"}, false
	case t.volErr != "":
		return DropFiles{}, widget.DropHint{Text: "Cannot read " + t.name}, false
	}
	for _, p := range d.Paths {
		// Paths of another file system say nothing of folders here.
		if across {
			break
		}
		if t.ps.Same(t.ps.Dir(p), t.dir) {
			return DropFiles{}, widget.DropHint{Text: "Already in " + t.name}, false
		}
		if within(t.ps, t.dir, p) {
			return DropFiles{}, widget.DropHint{Text: "Cannot go inside itself"}, false
		}
	}
	copying := across || d.Volume == "" || t.vol == "" || d.Volume != t.vol
	switch {
	case mods.Has(input.ModControl):
		copying = true
	case mods.Has(input.ModShift):
		copying = false
	}
	plan := DropFiles{Paths: slices.Clone(d.Paths), Into: t.dir, Copy: copying, FS: d.FS, To: t.fs, Away: !t.own,
		Style: d.Style}
	if copying {
		return plan, widget.DropHint{Text: "Copy to " + t.name, Effect: widget.DropCopy}, true
	}
	return plan, widget.DropHint{Text: "Move to " + t.name, Effect: widget.DropMove}, true
}

// within reports whether path is dir itself or lies inside it.
func within(ps PathStyle, path, dir string) bool {
	return ps.Same(path, dir) || ps.inside(path, dir)
}

// pinPlan works out what dropping d on the favourites of a window on the
// file system of ID fs does: it pins the folders not pinned yet.
func pinPlan(ps PathStyle, fs string, d FileDrag, favs []string) (PinFolders, widget.DropHint, bool) {
	if d.FS != fs {
		return PinFolders{}, widget.DropHint{Text: "Cannot pin from another file system"}, false
	}
	known := len(d.Dirs) == len(d.Paths)
	var paths []string
	for i, p := range d.Paths {
		if known && !d.Dirs[i] {
			continue
		}
		if !slices.ContainsFunc(favs, func(f string) bool { return ps.Same(f, p) }) {
			paths = append(paths, p)
		}
	}
	switch {
	case len(paths) > 0:
		return PinFolders{Paths: paths}, widget.DropHint{Text: "Pin to favourites", Effect: widget.DropLink}, true
	case known && !slices.Contains(d.Dirs, true):
		return PinFolders{}, widget.DropHint{Text: "Only folders can be pinned"}, false
	}
	return PinFolders{}, widget.DropHint{Text: "Already a favourite"}, false
}

// spotKey names a place a drag can land on: a folder row, the folder
// showing, a place in the sidebar, the favourites, or a folder of the
// path.
type spotKey struct {
	kind, path string
}

// dndView is the window's side of drag and drop: the drop zones, what
// the app says about volumes and the clipboard, and the plan for the
// drop under the pointer.
type dndView struct {
	b       *browser
	listing *widget.DropZone
	side    *widget.DropZone
	crumbs  *widget.DropZone
	// sideMenu is the context menu of the sidebar's places.
	sideMenu *sideMenu
	// withMenu is the listing's context menu last opened on one file,
	// whose item withAt opens the Open with submenu, and withAsked the
	// file the program was asked the programs of, until they come.
	// withApps are the programs that came last, for the file withPath.
	withMenu  *widget.ContextMenu
	withAt    int
	withAsked string
	withPath  string
	withApps  []OpenWithApp
	vols      map[string]string
	volErrs   map[string]string
	clip      ClipState
	// plan is what a drop on the spot found last does: a DropFiles or a
	// PinFolders.
	plan gunim.Intent
	// scripting is set while a script drags, and scriptAt is where its
	// pointer is.
	scripting bool
	scriptAt  geom.Point
	// u is the window's UI, for a drag starting to ask the program to
	// fetch its items.
	u *gunim.UI
	// fetches are the fetches of drags to carry out, by their IDs, until
	// their drags end, and fetchSeq numbers them. dragging is the fetch
	// of the drag going on, or 0.
	fetches  map[int]*dragFetch
	fetchSeq int
	dragging int
}

func newDndView(b *browser) *dndView {
	v := &dndView{b: b, vols: map[string]string{}, volErrs: map[string]string{}}
	v.listing = v.zone(b.listing, v.listingSpot)
	v.sideMenu = newSideMenu(b)
	v.side = v.zone(v.sideMenu.m, v.sideSpot)
	v.crumbs = v.zone(b.path, v.crumbSpot)
	return v
}

// zone wraps child in a drop zone that finds its spots with spot.
func (v *dndView) zone(child gunim.Node, spot func(input.Drop, *gunim.UI) (widget.DropSpot, bool)) *widget.DropZone {
	z := widget.NewDropZone(child)
	z.Spot = spot
	z.OnDrop = func(widget.DropSpot, input.Drop, *gunim.UI) gunim.Intent { return v.plan }
	z.OnOpen = func(s widget.DropSpot, u *gunim.UI) gunim.Intent {
		if k, ok := s.Key.(spotKey); ok {
			return Navigate{Path: k.path}
		}
		return nil
	}
	return z
}

func registerDnd(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, s Volumes, _ *gunim.UI) {
		b.dnd.vols, b.dnd.volErrs = s.Of, s.Errs
	})
	gunim.RegisterPatch(w, "browser", func(b *browser, s ClipState, _ *gunim.UI) { b.dnd.clip = s })
	gunim.RegisterPatch(w, "browser", func(b *browser, o OpenWithMenu, u *gunim.UI) { b.dnd.openWithMenu(o, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, s ScriptDrag, u *gunim.UI) { b.dnd.script(w, s, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, s DragFetched, _ *gunim.UI) { b.dnd.fetched(s) })
	gunim.RegisterPatch(w, "browser", func(b *browser, s DragFetching, _ *gunim.UI) { b.dnd.fetching(s) })
}

// fetchOut has the program start fetching the items of d, a drag about
// to start, to this computer, where they are of a file system other
// programs cannot reach, so the drag can carry them out. It returns the
// fetch, or nil where the drag needs none.
func (v *dndView) fetchOut(d FileDrag) *dragFetch {
	if d.scripted || d.FS == "" || !v.b.shell.Fetches || v.u == nil {
		return nil
	}
	if v.fetches == nil {
		v.fetches = map[int]*dragFetch{}
	}
	v.fetchSeq++
	f := &dragFetch{id: v.fetchSeq}
	v.fetches[f.id] = f
	v.dragging = f.id
	v.u.Send(v.listing, DragFetch{ID: f.id, Paths: slices.Clone(d.Paths)})
	return f
}

// fetched takes what the program says of a drag's fetch.
func (v *dndView) fetched(s DragFetched) {
	f, ok := v.fetches[s.ID]
	if !ok {
		return
	}
	f.done, f.local, f.err = true, s.Paths, s.Err
}

// fetching takes how far the program says a drag's fetch has got.
func (v *dndView) fetching(s DragFetching) {
	if f, ok := v.fetches[s.ID]; ok && !f.done {
		f.bytes, f.total = s.Bytes, s.Total
	}
}

// dragEnded is the end of a drag of rows: its fetch, needed no more, may
// stop.
func (v *dndView) dragEnded(input.DragEnd) gunim.Intent {
	id := v.dragging
	v.dragging = 0
	if id == 0 {
		return nil
	}
	delete(v.fetches, id)
	return DragFetchEnd{ID: id}
}

// spot fills in a spot for dropping d into the folder dir.
func (v *dndView) spot(d input.Drop, key spotKey, r geom.Rect, dir, vol string, opens bool) widget.DropSpot {
	fd, _ := fileDragOf(d)
	sh := v.b.shell
	plan, hint, ok := dropPlan(sh.Paths, sh.FS, sh.Transfers, fd, dir, v.vols[vol], v.volErrs[vol], d.Mods)
	v.plan = plan
	if !ok {
		v.plan = nil
	}
	return widget.DropSpot{Key: key, Rect: r, Radius: 6, Hint: hint, Refused: !ok, Opens: opens}
}

// listingSpot finds the spot of the listing under a drop: the folder
// row under it, or else the folder showing.
func (v *dndView) listingSpot(d input.Drop, u *gunim.UI) (widget.DropSpot, bool) {
	l := v.b.listing
	if _, ok := fileDragOf(d); !ok || l.cur == nil || l.path == "" {
		return widget.DropSpot{}, false
	}
	zr, ok := u.Bounds(v.listing)
	gr, ok2 := u.Bounds(l.cur.items())
	if !ok || !ok2 {
		return widget.DropSpot{}, false
	}
	off := gr.Min.Sub(zr.Min)
	if row := l.cur.itemAt(d.Pos.Sub(off)); row >= 0 {
		if r, ok := l.cur.view(row); ok && r.Dir {
			rr, _ := l.cur.itemRect(row)
			dir := v.b.shell.Paths.Join(l.path, r.Name)
			// A folder is on the volume of the folder showing. A link
			// to one is on the volume the app found for it, or on none
			// known while it looks.
			vol := l.path
			if r.Kind == KindLink {
				vol = dir
			}
			return v.spot(d, spotKey{"row", dir}, rr.Add(off), dir, vol, true), true
		}
	}
	whole := gr.Add(zr.Min.Mul(-1)).Inset(geom.Uniform(3))
	return v.spot(d, spotKey{"here", l.path}, whole, l.path, l.path, false), true
}

// sideSpot finds the spot of the sidebar under a drop: a place or a
// favourite, which takes the items unless it is on another file system,
// or the rest of the favourites' section, heading and all, which pins
// them.
func (v *dndView) sideSpot(d input.Drop, u *gunim.UI) (widget.DropSpot, bool) {
	s := v.b.side
	fd, ok := fileDragOf(d)
	zr, ok2 := u.Bounds(v.side)
	if !ok || !ok2 {
		return widget.DropSpot{}, false
	}
	at := d.Pos.Add(zr.Min)
	for _, l := range s.lists() {
		for _, k := range l.Keys() {
			n, found := l.Row(k)
			pr, isPlace := n.(*placeRow)
			if !found || !isPlace {
				continue
			}
			if r, drawn := u.Bounds(n); drawn && r.Contains(at) {
				if pr.item.Kind == "trash" {
					// The trash is no folder to drop into.
					v.plan = nil
					return widget.DropSpot{Key: spotKey{"place", pr.item.Path}, Rect: r.Add(zr.Min.Mul(-1)), Radius: 6,
						Hint: widget.DropHint{Text: "Press Delete to move items to the trash"}, Refused: true}, true
				}
				if pr.item.away {
					return v.awaySpot(d, fd, pr.item, k, r.Add(zr.Min.Mul(-1))), true
				}
				dir := pr.item.Path
				return v.spot(d, spotKey{"place", dir}, r.Add(zr.Min.Mul(-1)), dir, dir, true), true
			}
		}
	}
	fr, ok := u.Bounds(s.favSec)
	if !ok {
		return widget.DropSpot{}, false
	}
	// The last section reaches down to the bottom of the sidebar.
	bottom := fr.Max.Y + 4
	if len(s.order) > 0 && s.order[len(s.order)-1] == FavouritesSection {
		bottom = max(bottom, zr.Max.Y-8)
	}
	section := geom.Rect{Min: geom.Pt(zr.Min.X+4, fr.Min.Y-4), Max: geom.Pt(zr.Max.X-4, bottom)}
	if !section.Contains(at) {
		return widget.DropSpot{}, false
	}
	var favs []string
	for _, k := range s.favs.Keys() {
		if i, known := s.items[k]; known && !i.away {
			favs = append(favs, i.Path)
		}
	}
	plan, hint, ok := pinPlan(v.b.shell.Paths, v.b.shell.FS, fd, favs)
	v.plan = plan
	if !ok {
		v.plan = nil
	}
	return widget.DropSpot{Key: spotKey{"pin", ""}, Rect: section.Add(zr.Min.Mul(-1)), Radius: 8, Hint: hint,
		Refused: !ok}, true
}

// awaySpot fills in a spot for dropping d on it, a place or a favourite
// on another file system than the window's, at r: the items go into its
// folder through the program. A place whose folder is not known yet, as
// a server's before it is reached, takes nothing.
func (v *dndView) awaySpot(d input.Drop, fd FileDrag, it placeItem, k widget.Key, r geom.Rect) widget.DropSpot {
	v.plan = nil
	spot := widget.DropSpot{Key: spotKey{"away", string(k)}, Rect: r, Radius: 6}
	if it.Path == "" {
		spot.Hint, spot.Refused = widget.DropHint{Text: "Cannot drop on " + it.Name + " until it is open"}, true
		return spot
	}
	// The items' own paths tell where they are on the place's file
	// system, where it is theirs.
	t := dropTarget{fs: it.FS, ps: fd.Style, dir: it.Path, name: it.Name}
	plan, hint, ok := planDrop(t, v.b.shell.Transfers, fd, d.Mods)
	if ok {
		v.plan = plan
	}
	spot.Hint, spot.Refused = hint, !ok
	return spot
}

// crumbSpot finds the folder of the path under a drop.
func (v *dndView) crumbSpot(d input.Drop, u *gunim.UI) (widget.DropSpot, bool) {
	zr, ok := u.Bounds(v.crumbs)
	if _, ok2 := fileDragOf(d); !ok || !ok2 {
		return widget.DropSpot{}, false
	}
	at := d.Pos.Add(zr.Min)
	for _, c := range v.b.path.addr.Places(u) {
		if c.Rect.Contains(at) {
			return v.spot(d, spotKey{"crumb", c.Path}, c.Rect.Add(zr.Min.Mul(-1)), c.Path, c.Path, !c.Last), true
		}
	}
	return widget.DropSpot{}, false
}

// dragRows starts a drag of the rows selected: their paths, and a
// picture of their names.
func (pg *listingPage) dragRows(sel [][2]int, _ geom.Point) (any, gunim.Node, geom.Point) {
	dir := pg.b.listing.path
	d := FileDrag{Volume: pg.b.dnd.vols[dir], FS: pg.b.shell.FS, Style: pg.b.shell.Paths, scripted: pg.b.dnd.scripting}
	var items []Row
	for _, r := range sel {
		for i := r[0]; i < r[1]; i++ {
			row, ok := pg.view(i)
			if !ok {
				// A row not read yet cannot be named.
				return nil, nil, geom.Point{}
			}
			d.Paths = append(d.Paths, pg.b.shell.Paths.Join(dir, row.Name))
			d.Dirs = append(d.Dirs, row.Dir)
			items = append(items, row)
		}
	}
	if len(items) == 0 {
		return nil, nil, geom.Point{}
	}
	d.fetch = pg.b.dnd.fetchOut(d)
	grab := geom.Pt(22, 16)
	g := widget.NewDragGhost(newDragCard(items), grab)
	if n := len(items); n > 1 {
		g.Badge, g.Stack = plural(n, "item"), n-1
	}
	return d, g, grab
}

// dragCard is what a drag of files shows: the first few items' marks and
// names, and how many more there are.
type dragCard struct {
	items []Row
	more  int
	runs  []text.Run
	size  float32
}

// cardRows is how many items a drag card names.
const cardRows = 3

func newDragCard(items []Row) *dragCard {
	c := &dragCard{items: items}
	if len(items) > cardRows {
		c.items, c.more = items[:cardRows], len(items)-cardRows
	}
	return c
}

// cardLine is the height of one line of a drag card.
const cardLine = 24

// Layout implements [gunim.Node].
func (c *dragCard) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	size := widget.TextSize.Get(f.Theme) * 0.95
	if c.size != size || len(c.runs) == 0 {
		c.size = size
		c.runs = c.runs[:0]
		for _, it := range c.items {
			c.runs = append(c.runs, text.Default().Shape(it.Name, size))
		}
		if c.more > 0 {
			c.runs = append(c.runs, text.Default().Shape("and "+plural(c.more, "more item"), size*0.9))
		}
	}
	w := float32(0)
	for _, r := range c.runs {
		w = max(w, r.Advance)
	}
	return cs.Constrain(geom.Sz(min(w+44, 240), float32(len(c.runs))*cardLine+12))
}

// Paint implements [gunim.Node].
func (c *dragCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true,
		Radius: widget.CardRadius.Get(th)})()
	for i, r := range c.runs {
		y := 6 + float32(i)*cardLine
		ink := widget.Ink.Get(th)
		if i < len(c.items) {
			p.RRect(geom.Rc(12, y+(cardLine-12)/2, 12, 12), 3.5, paint.Solid(TintToken(c.items[i].Tint).Get(th)))
		} else {
			ink = Faint.Get(th)
		}
		r.Paint(p, geom.Pt(32, y+(cardLine-r.Height())/2), ink)
	}
}

// scriptTarget finds where in the window a script's name is: a row, a
// place, a folder of the path, "favourites" for below them, or
// "heading:" and a section's title, such as heading:Favourites.
func scriptTarget(b *browser, name string, u *gunim.UI) (geom.Point, bool) {
	l := b.listing
	if l.cur != nil {
		if gr, ok := u.Bounds(l.cur.items()); ok {
			for i := range l.cur.grid.Rows() {
				if r, ok := l.cur.view(i); ok && r.Name == name {
					if rr, ok := l.cur.itemRect(i); ok {
						return gr.Min.Add(geom.Pt(rr.Min.X+48, rr.Center().Y)), true
					}
				}
			}
		}
	}
	if title, ok := strings.CutPrefix(name, "heading:"); ok {
		for _, c := range b.side.byID {
			if strings.EqualFold(c.head.title, title) {
				if r, ok := u.Bounds(c.head); ok {
					return geom.Pt(r.Min.X+40, r.Center().Y), true
				}
			}
		}
		return geom.Point{}, false
	}
	for _, lst := range b.side.lists() {
		for _, k := range lst.Keys() {
			n, _ := lst.Row(k)
			if pr, ok := n.(*placeRow); ok && pr.item.Name == name {
				if r, ok := u.Bounds(n); ok {
					return geom.Pt(r.Min.X+40, r.Center().Y), true
				}
			}
		}
	}
	for _, c := range b.path.addr.Places(u) {
		if c.Name == name {
			return geom.Pt(c.Rect.Min.X+8, c.Rect.Center().Y), true
		}
	}
	if name == "favourites" {
		if r, ok := u.Bounds(b.side.favSec); ok {
			return geom.Pt(r.Min.X+40, r.Max.Y-4), true
		}
	}
	return geom.Point{}, false
}

// script drags as a person would, for a script's steps.
func (v *dndView) script(w *gunim.Window, s ScriptDrag, u *gunim.UI) {
	mods := input.Mods(s.Mods)
	if s.Step == "drop" {
		w.Input(input.PointerUp{Pos: v.scriptAt, Button: input.ButtonPrimary, Mods: mods})
		v.scripting = false
		return
	}
	at, ok := scriptTarget(v.b, s.Name, u)
	if !ok {
		return
	}
	switch s.Step {
	case "start":
		v.scripting = true
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		at = at.Add(geom.Pt(14, 8))
		w.Input(input.PointerMove{Pos: at, Mods: mods})
	case "over":
		if strings.HasPrefix(s.Name, "heading:") {
			// A heading dragged onto another goes past it, as a hand
			// would carry it.
			if at.Y < v.scriptAt.Y {
				at.Y -= 8
			} else {
				at.Y += 8
			}
		}
		// Two steps, so the picture swings.
		mid := v.scriptAt.Add(at).Mul(0.5)
		w.Input(input.PointerMove{Pos: mid, Mods: mods})
		w.Input(input.PointerMove{Pos: at, Mods: mods})
	case "menu":
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonSecondary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonSecondary})
	}
	v.scriptAt = at
}
