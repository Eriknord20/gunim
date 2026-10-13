package filemanager

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/widget"
)

// memStore is a favourites store that counts what it is given.
type memStore struct {
	mu    sync.Mutex
	favs  []Favourite
	saves int
}

func (s *memStore) Load() ([]Favourite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.favs), nil
}

func (s *memStore) Save(favs []Favourite) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.favs, s.saves = slices.Clone(favs), s.saves+1
	return nil
}

func (s *memStore) saved() []Favourite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.favs)
}

func TestTheCallerGivesThePlacesWithTheirGroups(t *testing.T) {
	var mu sync.Mutex
	var visits []string
	h := newHarnessWith(t, func(o *Options) {
		dir := o.Dir
		o.Places = func() ([]Place, error) {
			return []Place{
				{Name: "Here", Path: dir, Kind: "home", Group: "This computer"},
				{Name: "Server home", Path: "/home/me", Kind: "home", Group: "server", Note: "Connected", FS: "sftp://server"},
			}, nil
		}
		o.Visit = func(w *Window, fs, path string, _ bool) {
			mu.Lock()
			defer mu.Unlock()
			if w == nil {
				t.Error("Visit is not told which window asked")
			}
			visits = append(visits, fs+" "+path)
		}
	}, "a.txt")
	h.until("the places arrive", func() bool { return len(h.b.side.placeKeys()) == 2 })
	if got := h.b.side.titles(); !slices.Equal(got, []string{"THIS COMPUTER", "FAVOURITES", "SERVER"}) {
		t.Fatalf("the headings say %q", got)
	}
	keys := h.b.side.placeKeys()
	away := h.b.side.items[keys[1]]
	if !away.away || away.Note != "Connected" {
		t.Fatalf("the server's place is %+v, want it away", away)
	}
	if _, under := h.b.side.byID[GroupSection("server")].list.Row(keys[1]); !under {
		t.Fatal("the server's place is not under its heading")
	}
	h.frames(30)
	row := h.bounds(func(b *browser) gunim.Node { return b.side.placeRow(keys[1]) })
	h.click(geom.Pt(row.Center().X, row.Max.Y-10))
	h.until("the click asks to visit the server", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Equal(visits, []string{"sftp://server /home/me"})
	})
	if !SystemPaths.Same(h.a.nav.path, h.dir) {
		t.Fatalf("the window went to %s itself", h.a.nav.path)
	}
}

func TestTheCallersStoreKeepsTheFavourites(t *testing.T) {
	store := &memStore{}
	h := newHarnessWith(t, func(o *Options) {
		store.favs = []Favourite{{Path: filepath.Join(o.Dir, "old"), Name: "Kept"}}
		o.Favourites = store
	}, "old/", "work/")
	h.until("the stored favourite shows", func() bool { return h.b.side.favs.Len() == 1 })
	h.pick("work")
	h.do(Command{Name: CmdPin})
	want := []Favourite{{Path: filepath.Join(h.dir, "old"), Name: "Kept"}, {Path: filepath.Join(h.dir, "work"), Color: "red"}}
	h.until("the store holds both", func() bool { return slices.Equal(store.saved(), want) })
	h.until("the sidebar shows both", func() bool { return h.b.side.favs.Len() == 2 })
	n, _ := h.b.side.favs.Row(widget.Key(filepath.Join(h.dir, "old")))
	if r, ok := n.(*placeRow); !ok || r.item.Name != "Kept" {
		t.Fatal("the favourite does not show the name it was given")
	}
	if len(h.a.prefs.Favourites) != 0 {
		t.Fatal("the favourites went to the settings file too")
	}
}

func TestRefreshFindsThePlacesAgain(t *testing.T) {
	var mu sync.Mutex
	note := "Connecting"
	places := func() ([]Place, error) {
		mu.Lock()
		defer mu.Unlock()
		return []Place{{Name: "Server", Path: "/", Kind: "drive", FS: "sftp://server", Note: note}}, nil
	}
	w := newHarnessWith(t, func(o *Options) { o.Places = places }, "a.txt")
	w.until("the place shows", func() bool { return len(w.b.side.placeKeys()) == 1 })
	mu.Lock()
	note = "Connected"
	mu.Unlock()
	w.a.hub.Refresh()
	w.until("the place says it connected", func() bool {
		for _, it := range w.b.side.items {
			if it.Note == "Connected" {
				return true
			}
		}
		return false
	})
}

func TestAGroupListedTwiceIsOneSection(t *testing.T) {
	h := newHarnessWith(t, func(o *Options) {
		dir := o.Dir
		o.Places = func() ([]Place, error) {
			return []Place{
				{Name: "One", Path: dir, Group: "A"},
				{Name: "Two", Path: "/", Group: "B"},
				{Name: "One again", Path: dir, Group: "A"},
			}, nil
		}
	}, "a.txt")
	h.until("the places arrive", func() bool { return len(h.b.side.placeKeys()) == 3 })
	if got := h.b.side.titles(); !slices.Equal(got, []string{"A", "FAVOURITES", "B"}) {
		t.Fatalf("the headings are %q", got)
	}
	if n := h.b.side.byID[GroupSection("A")].list.Len(); n != 2 {
		t.Fatalf("the section of A holds %d places, want both", n)
	}
}

func TestAnOlderLookupOfThePlacesIsDropped(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	h := newHarnessWith(t, func(o *Options) {
		o.Places = func() ([]Place, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 2 {
				<-release
				return []Place{{Name: "Old", Path: "/"}}, nil
			}
			return []Place{{Name: "New", Path: "/"}}, nil
		}
	}, "a.txt")
	open := openOnCleanup(t, release)
	h.until("the first lookup answers", func() bool { return len(h.a.places) == 1 })
	// One lookup at a time, on the window's own goroutine: the second
	// begins once the first is waiting, so the one waiting is the older.
	h.a.post(h.a.loadPlaces)
	h.until("the older lookup asks", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls == 2
	})
	h.a.post(h.a.loadPlaces)
	h.until("the newest lookup answers", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls == 3 && len(h.a.places) == 1 && h.a.placesGen == 3
	})
	h.idle()
	open()
	h.idle()
	if h.a.places[0].Name != "New" {
		t.Fatalf("the places are %+v, from the older lookup", h.a.places)
	}
}

// funcStore is a favourites store that cannot be compared, as it holds a
// func.
type funcStore struct {
	m    *memStore
	note func()
}

func (s funcStore) Load() ([]Favourite, error) { return s.m.Load() }

func (s funcStore) Save(favs []Favourite) error { return s.m.Save(favs) }

func TestWindowsShareAStoreAndSkipOneThatCannotBeCompared(t *testing.T) {
	root := t.TempDir()
	one, two, three := filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "three")
	for _, d := range []string{one, two, three} {
		tree(t, d, "work/")
	}
	shared := &memStore{}
	odd := funcStore{m: &memStore{}, note: func() {}}
	hub := &Hub{}
	w1 := openHarnessWith(t, hub, root, one, func(o *Options) { o.Favourites = shared })
	w2 := openHarnessWith(t, hub, root, two, func(o *Options) { o.Favourites = shared })
	w3 := openHarnessWith(t, hub, root, three, func(o *Options) { o.Favourites = odd })
	w1.pick("work")
	w1.do(Command{Name: CmdPin})
	w2.until("the other window of the store shows the favourite", func() bool { return w2.b.side.favs.Len() == 1 })
	w3.pick("work")
	w3.do(Command{Name: CmdPin})
	w3.until("the odd store's window shows its own", func() bool { return w3.b.side.favs.Len() == 1 })
	w1.idle()
	if len(w1.a.favs) != 1 || !SystemPaths.Same(w1.a.favs[0].Path, filepath.Join(one, "work")) {
		t.Fatalf("the odd store's favourite reached a window of another store: %+v", w1.a.favs)
	}
}

func TestAnUnpinnedFavouriteKeepsItsName(t *testing.T) {
	h := newHarness(t, "work/")
	work := filepath.Join(h.dir, "work")
	h.pick("work")
	h.do(Command{Name: CmdPin})
	h.do(RenameFavourite{Path: work})
	h.until("the dialog shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, _ := h.a.ops.dialogs[0].state.(FavouriteEdit)
	h.answer(FavouriteEdited{Token: p.Token, Name: "Job", Color: "teal", Icon: "star", OK: true})
	h.do(Unpin{Path: work})
	h.do(Command{Name: CmdPin})
	if len(h.a.favs) != 1 || h.a.favs[0].Name != "Job" || h.a.favs[0].Color != "teal" || h.a.favs[0].Icon != "star" {
		t.Fatalf("the favourite came back as %+v", h.a.favs)
	}
}

func TestAWindowOnAnotherFileSystemLeavesTheSettingsItDidNotChange(t *testing.T) {
	root := t.TempDir()
	local, other := filepath.Join(root, "local"), filepath.Join(root, "other")
	tree(t, local, "work/")
	tree(t, other, "a.txt")
	hub := &Hub{}
	l := openHarnessWith(t, hub, root, local, nil)
	o := openHarnessWith(t, hub, root, other, onBareFS)
	o.do(Command{Name: CmdViewIcons})
	l.pick("work")
	l.do(Command{Name: CmdPin})
	o.do(Command{Name: CmdHidden})
	o.idle()
	p, err := loadPrefs(filepath.Join(root, "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Favourites, []string{filepath.Join(local, "work")}) || !p.ShowHidden {
		t.Fatalf("the settings hold %v and ShowHidden %v", p.Favourites, p.ShowHidden)
	}
	if l.a.viewMode(other).Icons {
		t.Fatal("the folder of the other file system set the view of the computer's own of the same path")
	}
}

func TestShowTurnsAWindowToAnotherFileSystem(t *testing.T) {
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	other := t.TempDir()
	tree(t, other, "x.txt", "y.txt")
	h.a.showFS(slashFS{root: other}, "/")
	h.until("the other file system shows", func() bool { return slices.Equal(h.shown(), []string{"x.txt", "y.txt"}) })
	if h.b.shell.FS != "slash" || h.b.shell.Paths != SlashPaths || !h.b.shell.NoTrash {
		t.Fatalf("the window's shell is %+v", h.b.shell)
	}
	h.do(Command{Name: CmdBack})
	h.until("back crosses to the computer's own files", func() bool {
		return h.a.fs.ID() == "" && h.a.nav.path == h.dir && slices.Equal(h.shown(), []string{"a.txt"})
	})
	h.do(Command{Name: CmdForward})
	h.until("forward crosses again", func() bool { return h.a.fs.ID() == "slash" && h.a.nav.path == "/" })
	if len(h.a.nav.back) != 1 || len(h.a.nav.fwd) != 0 {
		t.Fatalf("the history is %d back and %d forward", len(h.a.nav.back), len(h.a.nav.fwd))
	}
}

func TestHistoryAcrossFileSystemsGoesThroughVisit(t *testing.T) {
	other := t.TempDir()
	tree(t, other, "x.txt", "sub/y.txt")
	visits := make(chan [2]string, 4)
	var h *harness
	h = newHarnessWith(t, func(o *Options) {
		o.Visit = func(_ *Window, fs, path string, _ bool) {
			visits <- [2]string{fs, path}
			if fs == "fail" {
				return
			}
			h.a.post(func() {
				if fs == "slash" {
					h.a.showFS(slashFS{root: other}, path)
				} else {
					h.a.showFS(LocalFS(), path)
				}
			})
		}
	}, "a.txt")
	h.a.showFS(slashFS{root: other}, "/")
	h.until("the other file system shows", func() bool { return slices.Equal(h.shown(), []string{"sub", "x.txt"}) })
	h.do(Navigate{Path: "/sub"})
	h.until("its folder opens", func() bool { return slices.Equal(h.shown(), []string{"y.txt"}) })

	// Back within it, then back across: the program is asked, as it
	// may have to connect first.
	h.do(Command{Name: CmdBack})
	h.until("back within it", func() bool { return h.a.nav.path == "/" && slices.Equal(h.shown(), []string{"sub", "x.txt"}) })
	h.do(Command{Name: CmdBack})
	h.until("back across", func() bool { return h.a.fs.ID() == "" && h.a.nav.path == h.dir })
	if v := <-visits; v != [2]string{"", h.dir} {
		t.Fatalf("Visit was asked for %q", v)
	}
	if len(h.a.nav.back) != 0 || len(h.a.nav.fwd) != 2 {
		t.Fatalf("the history is %d back and %d forward", len(h.a.nav.back), len(h.a.nav.fwd))
	}
	h.do(Command{Name: CmdForward})
	h.until("forward across", func() bool { return h.a.fs.ID() == "slash" && h.a.nav.path == "/" })
	h.do(Command{Name: CmdForward})
	h.until("forward within it", func() bool { return h.a.nav.path == "/sub" })
	if len(h.a.nav.back) != 2 || len(h.a.nav.fwd) != 0 {
		t.Fatalf("the history is %d back and %d forward", len(h.a.nav.back), len(h.a.nav.fwd))
	}

	// A Visit that never shows leaves the history as it was.
	h.a.nav.back[0].fs = failFS{h.a.nav.back[0].fs}
	h.do(Command{Name: CmdBack})
	h.do(Command{Name: CmdBack})
	h.until("the Visit is asked for", func() bool { return len(visits) == 2 })
	h.frames(10)
	if h.a.fs.ID() != "slash" || len(h.a.nav.back) != 1 || len(h.a.nav.fwd) != 1 {
		t.Fatalf("on %q, the history is %d back and %d forward", h.a.fs.ID(), len(h.a.nav.back), len(h.a.nav.fwd))
	}

	// The user goes on, and the Show comes late: it is not taken.
	h.do(Navigate{Path: "/sub"})
	h.until("the folder opens", func() bool { return h.a.nav.path == "/sub" })
	late := h.a.nav.back[0]
	h.a.post(func() { h.a.showFS(late.fs, late.path) })
	h.frames(10)
	if h.a.fs.ID() != "slash" || h.a.nav.path != "/sub" || len(h.a.nav.back) != 2 {
		t.Fatalf("a late Show turned the window to %q %q, %d back", h.a.fs.ID(), h.a.nav.path, len(h.a.nav.back))
	}
}

func TestShowOfTheFolderShowingKeepsTheHistoryAsItIs(t *testing.T) {
	// Places of its own: the default ones are found reading the file
	// system the window shows, as it turns to another.
	h := newHarnessWith(t, func(o *Options) { o.Places = func() ([]Place, error) { return nil, nil } }, "a.txt")
	h.a.showFS(LocalFS(), h.dir)
	h.until("the folder shows again", func() bool { return len(h.shown()) == 1 })
	if len(h.a.nav.back) != 0 {
		t.Fatalf("the history goes back to %v", h.a.nav.back)
	}
	home, _ := LocalFS().Home()
	h.a.showFS(LocalFS(), "")
	h.until("home shows", func() bool { return !h.a.nav.loading && h.a.nav.path == home })
	h.frames(30)
	h.a.showFS(LocalFS(), "")
	h.until("home shows again", func() bool { return !h.a.nav.loading && h.a.nav.path == home })
	if len(h.a.nav.back) != 1 || h.a.nav.back[0].path != h.dir {
		t.Fatalf("the history goes back to %v", h.a.nav.back)
	}
}

// failFS is a file system whose Visit the test's program never shows.
type failFS struct{ FS }

func (failFS) ID() string { return "fail" }
