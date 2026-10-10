package filemanager

// trashless takes the trash away, for a file system that turned out to
// have none, and tells the window, whose menus then say Delete.
func (a *app) trashless() {
	a.trash = nil
	a.shell.NoTrash = true
	a.publishShell()
}

// showFS turns the window to the folder dir on fsys, or its home folder
// when dir is empty. The folder it showed goes into the history, which
// crosses file systems, unless this is a step back or forward, which
// moves through the history instead. What else the window knew of the
// file system it showed goes: the index of the palette, the thumbnails,
// the volumes, the clipboard, the places and the favourites. The places
// shown stay until those of fsys are found, so the sidebar does not
// empty and fill again, and its rows that stay keep still.
func (a *app) showFS(fsys FS, dir string) {
	if fsys == nil {
		fsys = LocalFS()
	}
	if a.nav.late(fsys.ID(), dir) {
		// The user went on from that step back or forward.
		return
	}
	from, h := a.here(), a.nav.hop
	a.nav.hop = nil
	a.closeViewer()
	a.search.stop()
	// The counts go on, so the answers of the old index are dropped.
	a.search = searchState{gen: a.search.gen + 1, seq: a.search.seq}
	a.opts.FS, a.fs, a.ps = fsys, fsys, fsys.Paths()
	a.trash, _ = fsys.(Trasher)
	a.thumbs.cache, a.thumbs.order, a.thumbs.bytes, a.thumbs.queue = map[thumbKey]thumbDone{}, nil, 0, nil
	a.dnd.vols, a.dnd.volErrs = nil, nil
	a.nav.path = ""
	a.nav.space, a.nav.spaceErr = space{}, nil

	a.syncClip()

	a.hub.shows(a, fsys.ID())
	a.shell.FS, a.shell.Paths, a.shell.NoTrash = fsys.ID(), a.ps, a.trash == nil
	a.shell.Where, a.shell.Fetches = a.where(), a.fetches()
	a.shell.OpenWith = a.openWithOn()
	a.publishShell()
	a.loadFavourites()
	a.loadPlaces()
	if h != nil && h.id == fsys.ID() && h.path == dir && a.hopped(h, from) {
		a.navigate(dir, h.step, false)
		// The folder left is on another file system: only the name
		// remembered can be gone back to.
		a.pickAgain(h.cursor, "")
		return
	}
	if dir == "" {
		home, err := a.home()
		if err != nil {
			// Back still returns to the folder left.
			a.left(from)
			return
		}
		dir = home
	}
	if from.fs.ID() != fsys.ID() || !a.ps.Same(from.path, dir) {
		a.left(from)
	}
	a.navigate(dir, 0, true)
}

// left puts the folder at from, which the window left, into the history.
func (a *app) left(from visited) {
	if from.path != "" {
		a.nav.back = append(a.nav.back, from)
		a.nav.fwd = nil
	}
}
