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

// The IDs of the sections of the sections harness.
var (
	thisComputer = GroupSection("This computer")
	servers      = GroupSection("Servers")
)

// newSectionsHarness opens a window in hub whose places are in two
// groups, This computer and Servers, with the folder work pinned. Its
// settings are kept in base.
func newSectionsHarness(t *testing.T, hub *Hub, base string) *harness {
	t.Helper()
	dir := filepath.Join(base, "dir")
	tree(t, dir, "work/", "a.txt")
	h := openHarnessWith(t, hub, base, dir, func(o *Options) {
		o.Places = func() ([]Place, error) {
			return []Place{
				{Name: "Here", Path: dir, Kind: "home", Group: "This computer"},
				{Name: "Server", Path: "/", Kind: "drive", Group: "Servers", FS: "srv"},
			}, nil
		}
	})
	if len(h.a.favs) == 0 {
		h.pick("work")
		h.do(Command{Name: CmdPin})
	}
	h.until("the sections show", func() bool {
		return len(h.b.side.order) == 3 && h.b.side.favs.Len() == 1 && len(h.b.side.placeKeys()) == 2
	})
	h.frames(60)
	return h
}

// headAt returns the middle of the heading of the section of ID id.
func (h *harness) headAt(id string) geom.Point {
	h.t.Helper()
	return h.bounds(func(b *browser) gunim.Node { return b.side.byID[id].head }).Center()
}

// drawn reports whether n was drawn in the last frame.
func (h *harness) drawn(n func(b *browser) gunim.Node) bool {
	h.t.Helper()
	var ok bool
	h.ui(func(b *browser, u *gunim.UI) { _, ok = u.Bounds(n(b)) })
	h.frames(1)
	h.ui(func(b *browser, u *gunim.UI) { _, ok = u.Bounds(n(b)) })
	return ok
}

func TestTheSectionsComeInTheirOrderUnderTheirHeadings(t *testing.T) {
	h := newSectionsHarness(t, nil, t.TempDir())
	if got := h.b.side.order; !slices.Equal(got, []string{thisComputer, FavouritesSection, servers}) {
		t.Fatalf("the sections are %q, want this computer, the favourites and the servers", got)
	}
	if got := h.b.side.titles(); !slices.Equal(got, []string{"THIS COMPUTER", "FAVOURITES", "SERVERS"}) {
		t.Fatalf("the headings say %q", got)
	}
	// Each heading sits above its places.
	here := h.bounds(func(b *browser) gunim.Node { return b.side.placeRow(b.side.placeKeys()[0]) })
	fav := h.bounds(func(b *browser) gunim.Node { return b.side.placeRow(b.side.favs.Keys()[0]) })
	srv := h.bounds(func(b *browser) gunim.Node { return b.side.placeRow(b.side.placeKeys()[1]) })
	if !(h.headAt(thisComputer).Y < here.Min.Y && here.Max.Y < h.headAt(FavouritesSection).Y &&
		h.headAt(FavouritesSection).Y < fav.Min.Y && fav.Max.Y < h.headAt(servers).Y && h.headAt(servers).Y < srv.Min.Y) {
		t.Fatal("the headings and the places are not in their order down the sidebar")
	}
}

func TestSectionOrderPutsTheFavouritesSecondAndNewGroupsLast(t *testing.T) {
	for _, c := range []struct {
		groups, saved, want []string
	}{
		{nil, nil, []string{FavouritesSection}},
		{[]string{"A"}, nil, []string{GroupSection("A"), FavouritesSection}},
		{[]string{"A", "B", "C"}, nil, []string{GroupSection("A"), FavouritesSection, GroupSection("B"), GroupSection("C")}},
		// A group the order does not know comes after those it does,
		// and one it knows that is gone is passed by.
		{[]string{"A", "B", "C"}, []string{GroupSection("B"), GroupSection("Gone"), FavouritesSection},
			[]string{GroupSection("B"), FavouritesSection, GroupSection("A"), GroupSection("C")}},
	} {
		if got := sectionOrder(c.groups, c.saved); !slices.Equal(got, c.want) {
			t.Errorf("groups %q saved as %q are shown as %q, want %q", c.groups, c.saved, got, c.want)
		}
	}
	// A section not shown as the user rearranges keeps its place after
	// the one it came after.
	got := keepAbsent([]string{"a", "gone", "b", "c"}, []string{"c", "a", "b"})
	if want := []string{"c", "a", "gone", "b"}; !slices.Equal(got, want) {
		t.Fatalf("the order kept is %q, want %q", got, want)
	}
}

func TestAClickOnAHeadingClosesItsSectionInEveryWindowOfTheSettings(t *testing.T) {
	base := t.TempDir()
	hub := &Hub{}
	h := newSectionsHarness(t, hub, base)
	other := newSectionsHarness(t, hub, base)
	here := func(b *browser) gunim.Node { return b.side.placeRow(b.side.placeKeys()[0]) }

	h.click(h.headAt(thisComputer))
	h.until("the section is kept closed", func() bool { return slices.Equal(h.a.prefs.SidebarCollapsed, []string{thisComputer}) })
	h.frames(60)
	if h.drawn(here) {
		t.Fatal("the place of a section closed is still drawn")
	}
	if !h.drawn(func(b *browser) gunim.Node { return b.side.byID[thisComputer].head }) {
		t.Fatal("the heading of a section closed is not drawn")
	}
	p, err := loadPrefs(filepath.Join(base, "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.SidebarCollapsed, []string{thisComputer}) {
		t.Fatalf("the settings keep %q closed", p.SidebarCollapsed)
	}
	other.until("the other window closes the section too", func() bool { return other.b.side.collapsed[thisComputer] })
	other.frames(60)
	if other.drawn(here) {
		t.Fatal("the other window still draws the place")
	}

	// Enter on the heading opens it again.
	h.ui(func(b *browser, u *gunim.UI) { u.Focus(b.side.byID[thisComputer].head) })
	h.press(input.KeyEnter, 0)
	h.until("the section is kept open", func() bool { return len(h.a.prefs.SidebarCollapsed) == 0 })
	h.frames(60)
	if !h.drawn(here) {
		t.Fatal("the place of a section opened again is not drawn")
	}
}

func TestADragOfAHeadingMovesItsSection(t *testing.T) {
	base := t.TempDir()
	hub := &Hub{}
	h := newSectionsHarness(t, hub, base)
	other := newSectionsHarness(t, hub, base)
	from, to := h.headAt(servers), h.headAt(thisComputer)
	h.w.Input(input.PointerMove{Pos: from, Time: time.Now()})
	h.w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	h.frames(2)
	for i := 1; i <= 10; i++ {
		at := geom.Pt(from.X, from.Y+(to.Y-12-from.Y)*float32(i)/10)
		h.w.Input(input.PointerMove{Pos: at, Time: time.Now()})
		h.frames(2)
	}
	h.w.Input(input.PointerUp{Pos: geom.Pt(to.X, to.Y-12), Button: input.ButtonPrimary, Time: time.Now()})
	want := []string{servers, thisComputer, FavouritesSection}
	h.until("the order is kept", func() bool { return slices.Equal(h.a.prefs.SidebarOrder, want) })
	h.frames(60)
	if !slices.Equal(h.b.side.order, want) || h.b.side.collapsed[servers] {
		t.Fatalf("the sidebar shows %q, closed %v", h.b.side.order, h.b.side.collapsed)
	}
	if h.headAt(servers).Y > h.headAt(thisComputer).Y {
		t.Fatal("the servers' heading is not at the top")
	}
	p, err := loadPrefs(filepath.Join(base, "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.SidebarOrder, want) {
		t.Fatalf("the settings keep the order %q", p.SidebarOrder)
	}
	other.until("the other window takes the order", func() bool { return slices.Equal(other.b.side.order, want) })

	// The keyboard walks the headings in their new order.
	h.ui(func(b *browser, u *gunim.UI) { u.Focus(b.side.byID[servers].head) })
	h.press(input.KeyDown, 0)
	h.press(input.KeyDown, 0)
	if h.focused() != h.b.side.byID[thisComputer].head {
		t.Fatal("Down from the servers' place did not go to the next heading")
	}
}

func TestAHeadingsMenuMovesItsSection(t *testing.T) {
	h := newSectionsHarness(t, nil, t.TempDir())
	h.rightClick(h.headAt(FavouritesSection))
	items, _ := h.sideMenuShown()
	if !slices.Equal(items, []string{"Collapse", "Move up", "Move down"}) {
		t.Fatalf("the heading's menu is %v", items)
	}
	h.ui(func(b *browser, u *gunim.UI) { b.dnd.sideMenu.m.OnPick(1, u) })
	want := []string{FavouritesSection, thisComputer, servers}
	h.until("the favourites move up", func() bool { return slices.Equal(h.a.prefs.SidebarOrder, want) })
	h.frames(30)

	// The menu key opens the menu of the heading with the keyboard,
	// which cannot move the first section up.
	h.ui(func(b *browser, u *gunim.UI) { u.Focus(b.side.byID[FavouritesSection].head) })
	h.press(input.KeyMenu, 0)
	var off []bool
	h.ui(func(b *browser, _ *gunim.UI) { off = disabledOf(b.dnd.sideMenu.m.Items()) })
	if items, _ := h.sideMenuShown(); !slices.Equal(items, []string{"Collapse", "Move up", "Move down"}) ||
		!slices.Equal(off, []bool{false, true, false}) {
		t.Fatalf("the heading's menu is %v, dimmed %v", items, off)
	}
	h.ui(func(b *browser, u *gunim.UI) { b.dnd.sideMenu.m.OnPick(0, u) })
	h.until("the favourites close", func() bool { return slices.Equal(h.a.prefs.SidebarCollapsed, []string{FavouritesSection}) })
	h.rightClick(h.headAt(FavouritesSection))
	if items, _ := h.sideMenuShown(); items[0] != "Expand" {
		t.Fatalf("the menu of a section closed is %v", items)
	}
}

func TestLocalPlacesAreHomeTheDrivesAndTheTrash(t *testing.T) {
	ps, err := LocalPlaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) == 0 || ps[0].Kind != "home" {
		t.Fatalf("the places are %+v, want home first", ps)
	}
	if _, err := systemTrashCan(); err == nil {
		if last := ps[len(ps)-1]; last.Kind != "trash" || last.Path != TrashPath {
			t.Fatalf("the last place is %+v, want the trash", last)
		}
		ps = ps[:len(ps)-1]
	}
	for _, p := range ps[1:] {
		if p.Kind != "drive" {
			t.Errorf("%s is a place of kind %s, want only drives between home and the trash", p.Name, p.Kind)
		}
	}
	for _, f := range DefaultFavourites() {
		if f.FS != "" || !slices.Contains(FavouriteIcons, f.Icon) || !slices.Contains(FavouriteColors, f.Color) {
			t.Errorf("the default favourite %+v lacks an icon or a colour", f)
		}
	}
}

func TestTheDefaultFavouritesComeOnceAndStayUnpinned(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	tree(t, dir, "Docs/", "Old/", "a.txt")
	docs, old := filepath.Join(dir, "Docs"), filepath.Join(dir, "Old")
	defaults := func(o *Options) {
		o.defaults = func() []Favourite { return []Favourite{{Path: docs, Icon: "file-text", Color: "blue"}} }
	}
	// The settings of an older version pinned Old, with no colour.
	if err := savePrefs(filepath.Join(root, "prefs.json"), prefs{Favourites: []string{old}}); err != nil {
		t.Fatal(err)
	}
	h := openHarnessWith(t, nil, root, dir, defaults)
	want := []Favourite{{Path: docs, Icon: "file-text", Color: "blue"}, {Path: old, Color: "indigo"}}
	h.until("the defaults come first", func() bool { return slices.Equal(h.a.favs, want) })
	h.until("the sidebar shows them", func() bool { return h.b.side.favs.Len() == 2 })
	p, err := loadPrefs(filepath.Join(root, "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.FavSeeded || p.FavIcons[docs] != "file-text" || p.FavColors[docs] != "blue" || p.FavColors[old] != "indigo" {
		t.Fatalf("the settings hold %+v", p)
	}
	h.do(Unpin{Path: docs})
	h.until("the default is unpinned", func() bool { return len(h.a.favs) == 1 })

	// A window opened later does not bring it back.
	again := openHarnessWith(t, &Hub{}, root, dir, defaults)
	again.idle()
	if !slices.Equal(again.a.favs, []Favourite{{Path: old, Color: "indigo"}}) {
		t.Fatalf("a later window has the favourites %+v", again.a.favs)
	}
	// Pinned again, it comes back as it was.
	again.pick("Docs")
	again.do(Command{Name: CmdPin})
	again.until("Docs is pinned again", func() bool { return len(again.a.favs) == 2 })
	if f := again.a.favs[1]; f.Icon != "file-text" || f.Color != "blue" {
		t.Fatalf("Docs came back as %+v", f)
	}
}

func TestNewFavouritesTakeColoursOfTheirOwn(t *testing.T) {
	h := newHarness(t, "a/", "b/", "c/", "d/")
	for _, n := range []string{"a", "b"} {
		h.pick(n)
		h.do(Command{Name: CmdPin})
	}
	h.pick("c", "d")
	h.do(Command{Name: CmdPin})
	got := make([]string, 0, len(h.a.favs))
	for _, f := range h.a.favs {
		got = append(got, f.Color)
	}
	if !slices.Equal(got, []string{"red", "orange", "yellow", "green"}) {
		t.Fatalf("the favourites have the colours %q", got)
	}
	// After the default colours, the next is one none has.
	defaults := make([]Favourite, 0, 7)
	for _, c := range []string{"teal", "blue", "green", "purple", "pink", "orange"} {
		defaults = append(defaults, Favourite{Color: c})
	}
	if c := nextColor(defaults); c != "yellow" {
		t.Fatalf("after the defaults the colour is %s", c)
	}
	if c := nextColor(append(defaults, Favourite{Color: "yellow"})); c != "indigo" {
		t.Fatalf("after the defaults and yellow the colour is %s", c)
	}
}

func TestEveryFavouriteHasItsMachineWithAStoreOfAnyFileSystem(t *testing.T) {
	var visits []string
	var mu sync.Mutex
	store := newAnyStore()
	store.names[""] = "This PC"
	h := newAnyFSHarness(t, store, &visits, &mu)
	h.until("both favourites show", func() bool { return h.b.side.favs.Len() == 2 })
	for _, k := range h.b.side.favs.Keys() {
		it := h.b.side.items[k]
		want := map[string]string{"": "This PC", server: "Server"}[it.FS]
		if it.Note != want {
			t.Errorf("the favourite %s says %q, want %q", it.Path, it.Note, want)
		}
	}
}

func TestTheEditFavouriteDialogChangesTheNameColourAndIcon(t *testing.T) {
	h := newMenuHarness(t, nil, "work/")
	h.w.Offscreen().ListenForAccess()
	work := filepath.Join(h.dir, "work")
	h.pick("work")
	h.do(Command{Name: CmdPin})
	h.until("the favourite shows", func() bool { return h.b.side.favs.Len() == 1 })
	h.frames(30)
	// The menu offers it, and F2 on the favourite opens it too.
	h.rightClick(h.bounds(func(b *browser) gunim.Node { return b.side.placeRow(widget.Key(work)) }).Center())
	h.ui(func(b *browser, u *gunim.UI) {
		i := slices.Index(labelsOf(b.dnd.sideMenu.m.Items()), "Edit favourite…")
		b.dnd.sideMenu.m.OnPick(i, u)
	})
	h.until("the dialog is asked for", func() bool { return len(h.a.ops.dialogs) == 1 })
	h.until("the dialog shows", func() bool { h.frames(1); return h.dialogShown() })
	h.frames(30)
	e, _ := h.a.ops.dialogs[0].state.(FavouriteEdit)
	if e.Name != "work" || e.Folder != "work" || e.Color != "red" || e.Icon != "" {
		t.Fatalf("the dialog shows %+v", e)
	}
	// The name is selected to begin with, so typing replaces it.
	h.w.Input(input.TextInput{Text: "Job", Time: time.Now()})
	h.frames(2)
	h.press(input.KeyTab, 0)
	h.press(input.KeyRight, 0)
	h.press(input.KeyRight, 0)
	h.press(input.KeyTab, 0)
	h.press(input.KeyEnd, 0)
	h.press(input.KeyEnter, 0)
	want := Favourite{Path: work, Name: "Job", Color: "yellow", Icon: "terminal"}
	h.until("the favourite takes what the dialog says", func() bool { return len(h.a.favs) == 1 && h.a.favs[0] == want })
	h.until("the sidebar shows it", func() bool {
		it := h.b.side.items[widget.Key(work)]
		return it.Name == "Job" && it.Color == "yellow" && it.Icon == "terminal"
	})
	p, err := loadPrefs(filepath.Join(h.root, "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.FavNames[work] != "Job" || p.FavColors[work] != "yellow" || p.FavIcons[work] != "terminal" {
		t.Fatalf("the settings hold %+v", p)
	}

	// F2 on the favourite opens the dialog, and Escape leaves it as it was.
	h.ui(func(b *browser, u *gunim.UI) { u.Focus(b.side.favs) })
	h.press(input.KeyF2, 0)
	h.until("F2 opens the dialog", func() bool { return len(h.a.ops.dialogs) == 1 })
	h.frames(30)
	h.press(input.KeyEscape, 0)
	h.until("Escape closes it", func() bool { return len(h.a.ops.dialogs) == 0 })
	if h.a.favs[0] != want {
		t.Fatalf("Escape changed the favourite to %+v", h.a.favs[0])
	}
}

func TestEveryColourAndIconOfAFavouriteCanBeDrawn(t *testing.T) {
	dark, light := theme.NewLive(darkTheme()), theme.NewLive(lightTheme())
	for _, c := range FavouriteColors {
		tok, ok := favColors[c]
		if !ok {
			t.Errorf("the colour %s has no token", c)
			continue
		}
		if tok.Get(dark) == tok.Get(light) {
			t.Errorf("the colour %s is the same in the light theme as in the dark", c)
		}
	}
	for _, n := range FavouriteIcons {
		if ic, ok := favIcons[n]; !ok || ic.Name != n {
			t.Errorf("the icon %s is not drawn as itself", n)
		}
	}
	if favIcon("") == nil || favColor("").Key() != TintToken(TintFolder).Key() {
		t.Error("a favourite without an icon or a colour is not drawn as a folder")
	}
}
