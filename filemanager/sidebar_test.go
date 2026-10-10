package filemanager

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// clickWith presses and lets go at p with mods held, as the clicks-th click
// of a run of them.
func (h *harness) clickWith(p geom.Point, mods input.Mods, clicks int) {
	h.w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Mods: mods, Clicks: clicks, Time: time.Now()})
	h.w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Mods: mods, Time: time.Now()})
	h.frames(2)
}

// placeAt is where to click the row of the sidebar's places with key k.
func (h *harness) placeAt(k widget.Key) geom.Point {
	h.t.Helper()
	r := h.bounds(func(b *browser) gunim.Node { return b.side.placeRow(k) })
	return geom.Pt(r.Center().X, r.Max.Y-10)
}

// placeKeys returns the keys of the places of every section but the
// favourites, in the order shown.
func (s *sidebar) placeKeys() []widget.Key {
	var out []widget.Key
	for _, l := range s.lists() {
		if l != s.favs {
			out = append(out, l.Keys()...)
		}
	}
	return out
}

// placeRow returns the row of the place or favourite with key k, or nil.
func (s *sidebar) placeRow(k widget.Key) gunim.Node {
	for _, l := range s.lists() {
		if n, ok := l.Row(k); ok {
			return n
		}
	}
	return nil
}

// titles returns the headings of the sections, in the order shown.
func (s *sidebar) titles() []string {
	out := make([]string, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id].head.title)
	}
	return out
}

// firstHead returns the heading of the first section.
func (s *sidebar) firstHead() gunim.Node { return s.byID[s.order[0]].head }

func TestCtrlClickOnAPlaceOpensOneWindow(t *testing.T) {
	var mu sync.Mutex
	var visits []string
	var opened []string
	h := newHarnessWith(t, func(o *Options) {
		dir := o.Dir
		o.Places = func() ([]Place, error) {
			return []Place{
				{Name: "Work", Path: filepath.Join(dir, "work"), Kind: "home"},
				{Name: "Server", Path: "/home/me", Kind: "home", Group: "Servers", FS: "sftp://server"},
			}, nil
		}
		o.Visit = func(_ *Window, fs, path string, newWindow bool) {
			mu.Lock()
			defer mu.Unlock()
			what := "here"
			if newWindow {
				what = "new"
			}
			visits = append(visits, what+" "+fs+" "+path)
		}
	}, "work/", "a.txt")
	h.a.hub.open = func(o Options) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, o.Dir)
		return nil
	}
	h.until("the places arrive", func() bool { return len(h.b.side.placeKeys()) == 2 })
	h.frames(30)
	work, srv := placeKey(Place{Path: filepath.Join(h.dir, "work")}), placeKey(Place{FS: "sftp://server", Path: "/home/me"})
	seen := func(what string, got *[]string, want ...string) {
		t.Helper()
		h.until(what, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return slices.Equal(*got, want)
		})
		h.frames(10)
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(*got, want) {
			t.Fatalf("%s: got %q", what, *got)
		}
	}

	h.clickWith(h.placeAt(work), input.ModControl, 1)
	seen("a Ctrl+click opens the place in a window", &opened, filepath.Join(h.dir, "work"))
	if !SystemPaths.Same(h.a.nav.path, h.dir) {
		t.Fatalf("the window went to %s itself", h.a.nav.path)
	}

	at := h.placeAt(work)
	h.clickWith(at, input.ModControl, 1)
	h.clickWith(at, input.ModControl, 2)
	seen("a Ctrl+double-click opens one window more", &opened, filepath.Join(h.dir, "work"), filepath.Join(h.dir, "work"))

	at = h.placeAt(srv)
	h.clickWith(at, input.ModControl, 1)
	seen("a Ctrl+click elsewhere asks for a new window", &visits, "new sftp://server /home/me")
	h.clickWith(at, input.ModControl, 1)
	h.clickWith(at, input.ModControl, 2)
	seen("a Ctrl+double-click elsewhere asks once", &visits, "new sftp://server /home/me", "new sftp://server /home/me")
	h.clickWith(at, 0, 1)
	h.clickWith(at, 0, 2)
	seen("a double-click elsewhere asks once, for this window", &visits, "new sftp://server /home/me",
		"new sftp://server /home/me", "here sftp://server /home/me")

	h.clickWith(h.placeAt(work), 0, 1)
	h.until("a click goes to the place", func() bool { return SystemPaths.Same(h.a.nav.path, filepath.Join(h.dir, "work")) })
	seen("a click opens no window", &opened, filepath.Join(h.dir, "work"), filepath.Join(h.dir, "work"))
}

func TestTurningToAnotherFileSystemKeepsTheSidebarRows(t *testing.T) {
	other := t.TempDir()
	tree(t, other, "x.txt")
	store := newAnyStore()
	var mu sync.Mutex
	var visits []string
	h := newAnyFSHarness(t, store, &visits, &mu)
	// Once the window turns, the places are slow to come, as a server's
	// may be, and the window shows those it had meanwhile.
	gate := make(chan struct{})
	slow := false
	h.a.opts.Places = func() ([]Place, error) {
		mu.Lock()
		wait := slow
		mu.Unlock()
		if wait {
			<-gate
		}
		return []Place{
			{Name: "Here", Path: h.dir, Kind: "home", Group: "This computer"},
			{Name: "Slash", Path: "/", Kind: "drive", Group: "Servers", FS: "slash"},
			{Name: "Also here", Path: "/", Kind: "drive", Group: "This computer"},
		}, nil
	}
	h.a.loadPlaces()
	h.until("the places arrive", func() bool { return len(h.b.side.placeKeys()) == 3 && h.b.side.favs.Len() == 2 })
	h.frames(60)
	type row struct {
		key  widget.Key
		node gunim.Node
	}
	rows := func() []row {
		var out []row
		for _, k := range slices.Concat(h.b.side.placeKeys(), h.b.side.favs.Keys()) {
			out = append(out, row{k, h.b.side.placeRow(k)})
		}
		return out
	}
	before := rows()
	if before[1].key == before[2].key {
		t.Fatal("the same path on two file systems has one key")
	}

	mu.Lock()
	slow = true
	mu.Unlock()
	h.a.showFS(slashFS{root: other}, "/")
	steady := func() {
		t.Helper()
		for range 60 {
			h.pump()
			if got := rows(); !slices.Equal(got, before) {
				t.Fatalf("the sidebar's rows changed from %v to %v", before, got)
			}
		}
	}
	h.until("the window shows the other file system", func() bool { return slices.Equal(h.shown(), []string{"x.txt"}) })
	steady()
	close(gate)
	steady()
	here, slash := h.b.side.items[before[0].key], h.b.side.items[before[2].key]
	if !here.away || slash.away || !slash.current {
		t.Fatalf("after the turn the computer's place is %+v and the server's %+v", here, slash)
	}
	if fav := h.b.side.items[before[3].key]; !fav.away {
		t.Fatalf("the favourite of the computer is %+v, want it away", fav)
	}
}

// newMenuHarness opens a window on a folder made of spec, with the
// options set changes.
func newMenuHarness(t *testing.T, set func(o *Options), spec ...string) *harness {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	tree(t, dir, spec...)
	return openHarnessWith(t, nil, root, dir, set)
}

// rightClick presses and lets go of the secondary button at p.
func (h *harness) rightClick(p geom.Point) {
	h.w.Input(input.PointerMove{Pos: p, Time: time.Now()})
	h.w.Input(input.PointerDown{Pos: p, Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	h.w.Input(input.PointerUp{Pos: p, Button: input.ButtonSecondary, Time: time.Now()})
	h.frames(4)
}

// sideMenuShown returns the items of the sidebar's menu and where its
// lines go, or fails the test when it is not open.
func (h *harness) sideMenuShown() (items []string, breaks []int) {
	h.t.Helper()
	var open bool
	h.ui(func(b *browser, _ *gunim.UI) {
		m := b.dnd.sideMenu.m
		items, breaks, open = labelsOf(m.Items()), breaksOf(m.Items()), m.Focusable()
	})
	if !open {
		h.t.Fatal("the sidebar's menu did not open")
	}
	return items, breaks
}

// closeSideMenu closes the sidebar's menu with Escape.
func (h *harness) closeSideMenu() {
	h.w.Input(input.KeyPress{Key: input.KeyEscape})
	h.frames(3)
}

func TestEveryPlaceHasAMenuWithTheProgramsItems(t *testing.T) {
	type command struct {
		w     *Window
		place Place
		id    string
	}
	commands := make(chan command, 1)
	h := newMenuHarness(t, func(o *Options) {
		dir := o.Dir
		o.Places = func() ([]Place, error) {
			return []Place{
				{Name: "Here", Path: dir, Kind: "home"},
				{Name: "Sub", Path: filepath.Join(dir, "sub"), Kind: "documents"},
				{Name: "Server", Path: "/", Kind: "drive", Group: "Servers", FS: "srv", Lit: true},
			}, nil
		}
		o.PlaceMenu = func(p Place) []PlaceItem {
			switch {
			case p.Kind == "drive" && p.FS == "srv" && p.Lit:
				return []PlaceItem{{Label: "Disconnect", ID: "disconnect"}}
			case p.Kind == "favourite":
				return []PlaceItem{{Label: "Forget", ID: "forget"}}
			}
			return nil
		}
		o.PlaceCommand = func(w *Window, p Place, id string) { commands <- command{w, p, id} }
	}, "sub/")
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.do(Command{Name: CmdPin})
	h.do(Command{Name: CmdUp})
	h.until("the places and the favourite show", func() bool { return len(h.b.side.placeKeys()) == 3 && h.b.side.favs.Len() == 1 })
	h.frames(30)
	keys := h.b.side.placeKeys()

	// A place that is not the folder showing has its menu, and one on
	// another file system too.
	h.rightClick(h.placeAt(keys[1]))
	if items, _ := h.sideMenuShown(); !slices.Equal(items, []string{"Open", "Open in new window"}) {
		t.Fatalf("the menu of Sub is %v", items)
	}
	h.closeSideMenu()
	h.rightClick(h.placeAt(keys[2]))
	items, breaks := h.sideMenuShown()
	if !slices.Equal(items, []string{"Open", "Open in new window", "Disconnect"}) || !slices.Equal(breaks, []int{2}) {
		t.Fatalf("the menu of the server is %v with lines at %v", items, breaks)
	}
	h.ui(func(b *browser, u *gunim.UI) { b.dnd.sideMenu.m.OnPick(2, u) })
	h.frames(2)
	select {
	case c := <-commands:
		if c.id != "disconnect" || c.place.FS != "srv" || c.place.Name != "Server" || c.w == nil {
			t.Fatalf("PlaceCommand got %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Disconnect did not reach PlaceCommand")
	}

	// A favourite gets the program's items too.
	fav := h.bounds(func(b *browser) gunim.Node {
		n, _ := b.side.favs.Row(b.side.favs.Keys()[0])
		return n
	})
	h.rightClick(fav.Center())
	items, _ = h.sideMenuShown()
	if !slices.Equal(items, []string{"Open", "Open in new window", "Edit favourite…", "Unpin", "Forget"}) {
		t.Fatalf("the favourite's menu is %v", items)
	}
	h.ui(func(b *browser, u *gunim.UI) { b.dnd.sideMenu.m.OnPick(4, u) })
	h.frames(2)
	select {
	case c := <-commands:
		if c.id != "forget" || c.place.Kind != "favourite" || !SystemPaths.Same(c.place.Path, filepath.Join(h.dir, "sub")) {
			t.Fatalf("PlaceCommand got %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Forget did not reach PlaceCommand")
	}
}

func TestAPlaceElsewhereHasAMenuWithoutTheProgramsItems(t *testing.T) {
	h := newMenuHarness(t, func(o *Options) {
		dir := o.Dir
		o.Places = func() ([]Place, error) {
			return []Place{
				{Name: "Here", Path: dir, Kind: "home"},
				{Name: "Server", Path: "/", Kind: "drive", FS: "srv"},
			}, nil
		}
	}, "a.txt")
	h.until("the places show", func() bool { return len(h.b.side.placeKeys()) == 2 })
	h.frames(30)
	h.rightClick(h.placeAt(h.b.side.placeKeys()[1]))
	if items, _ := h.sideMenuShown(); !slices.Equal(items, []string{"Open", "Open in new window"}) {
		t.Fatalf("the menu of the server is %v", items)
	}
}

func TestALitPlaceIsMarkedGreen(t *testing.T) {
	th := theme.NewLive(darkTheme())
	server := Place{Name: "Server", Kind: "drive"}
	if got := placeMark(server).Get(th); got == PlaceLit.Get(th) {
		t.Fatal("a machine not connected is marked as lit")
	}
	server.Lit = true
	if got := placeMark(server).Get(th); got != PlaceLit.Get(th) {
		t.Fatalf("a machine connected is marked %v", got)
	}
}

func TestALongFavouriteNameIsCutShortAndWholeInItsTip(t *testing.T) {
	long := "Master of Ceremonies – Unnamed Episodes of the Year 2026"
	h := newHarness(t, long+"/x.txt", "short/y.txt")
	for _, name := range []string{long, "short"} {
		h.do(Navigate{Path: filepath.Join(h.dir, name)})
		h.do(Command{Name: CmdPin})
	}
	h.until("both favourites show", func() bool { return h.b.side.favs.Len() == 2 })
	h.frames(30)
	row := func(name string) *placeRow {
		n, _ := h.b.side.favs.Row(widget.Key(filepath.Join(h.dir, name)))
		r, ok := n.(*placeRow)
		if !ok {
			t.Fatalf("the favourite %s is not a row", name)
		}
		return r
	}
	r, box := row(long), h.bounds(func(b *browser) gunim.Node { n, _ := b.side.favs.Row(widget.Key(filepath.Join(h.dir, long))); return n })
	if r.cutName.Advance > box.Size().W-textLeft || len(r.cutName.Glyphs) >= len(r.name.Glyphs) {
		t.Fatalf("the name is %v wide in a row %v wide", r.cutName.Advance, box.Size().W)
	}
	if r.tipText() != long {
		t.Fatalf("the tip of the name cut short says %q", r.tipText())
	}
	if tip := row("short").tipText(); tip != "" {
		t.Fatalf("a name that fits has the tip %q", tip)
	}
}
