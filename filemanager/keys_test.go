package filemanager

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// places returns the folders the path bar shows.
func (h *harness) places() []widget.AddressPlace {
	h.t.Helper()
	type ask struct{}
	var out []widget.AddressPlace
	gunim.RegisterPatch(h.w, "browser", func(b *browser, _ ask, u *gunim.UI) { out = b.path.addr.Places(u) })
	if err := h.w.Client().Patch(string(browserID), ask{}); err != nil {
		h.t.Fatal(err)
	}
	h.frames(1)
	return out
}

// onPlace is the index of the folder of the path with the keyboard, or -1.
func (h *harness) onPlace() int {
	h.t.Helper()
	return slices.IndexFunc(h.places(), func(p widget.AddressPlace) bool { return p.Focused })
}

// press presses k and lets it go.
func (h *harness) press(k input.Key, mods input.Mods) {
	h.w.Input(input.KeyPress{Key: k, Mods: mods, Time: time.Now()})
	h.w.Input(input.KeyRelease{Key: k, Mods: mods, Time: time.Now()})
	h.frames(2)
}

func TestTabReachesEveryPartOfTheWindowAndTheSidebarOpensAPlace(t *testing.T) {
	h := newHarness(t, "work/report.txt")
	h.do(Navigate{Path: filepath.Join(h.dir, "work")})
	h.do(Command{Name: CmdPin})
	h.do(Command{Name: CmdUp})
	h.until("the favourite shows and the folder is back", func() bool {
		return h.b.side.favs.Len() == 1 && len(h.shown()) == 1 && h.shown()[0] == "work"
	})
	h.frames(30)

	b := h.b
	want := map[gunim.Node]string{
		b.path.back:               "Back",
		b.path.filter:             "the filter",
		b.side.firstHead():        "the sidebar",
		b.listing.cur.focusNode(): "the listing",
		b.status.views.modes:      "the view switch",
	}
	seen := map[gunim.Node]bool{}
	var order []gunim.Node
	placeStops := 0
	for range 40 {
		h.press(input.KeyTab, 0)
		f := h.focused()
		if f == nil {
			t.Fatal("Tab left nothing with the keyboard")
		}
		if seen[f] {
			break
		}
		seen[f] = true
		order = append(order, f)
		if h.onPlace() >= 0 {
			placeStops++
		}
	}
	if seen[b.side.favs] {
		t.Error("Tab stopped at the favourites, want the sidebar one stop")
	}
	for n, name := range want {
		if !seen[n] {
			t.Errorf("Tab never reached %s", name)
		}
	}
	for _, nb := range []*widget.IconButton{b.path.back, b.path.fwd, b.path.up} {
		if seen[nb] == nb.Disabled {
			t.Errorf("Tab reached %s %v times, and it is disabled %v", nb.Tooltip, seen[nb], nb.Disabled)
		}
	}
	if !b.path.fwd.Disabled {
		t.Error("Forward is enabled with nowhere to go")
	}
	// The folders of the path are one stop
	if placeStops != 1 {
		t.Errorf("Tab stopped at %d folders of the path, want 1", placeStops)
	}
	// The path bar comes before the sidebar, and the sidebar before the listing.
	at := func(n gunim.Node) int { return slices.Index(order, n) }
	if at(b.path.back) > at(b.side.firstHead()) || at(b.side.firstHead()) > at(b.listing.cur.focusNode()) {
		t.Errorf("Tab went round in the order %v", order)
	}

	// Down walks from the first heading into its places, and on past the
	// favourites' heading into the favourites, which take Enter.
	for range 40 {
		if h.focused() == b.side.firstHead() {
			break
		}
		h.press(input.KeyTab, 0)
	}
	h.press(input.KeyDown, 0)
	places := b.side.byID[b.side.order[0]].list
	if h.focused() != places {
		t.Fatal("Down from the first heading did not go into its places")
	}
	h.press(input.KeyEnd, 0)
	h.press(input.KeyDown, 0)
	if h.focused() != b.side.favSec.head {
		t.Fatal("Down from the last place did not go on to the favourites' heading")
	}
	h.press(input.KeyDown, 0)
	if h.focused() != b.side.favs {
		t.Fatal("Down from the favourites' heading did not go on into the favourites")
	}
	h.press(input.KeyEnter, 0)
	h.until("Enter on the favourite opens it", func() bool { return slices.Equal(h.shown(), []string{"report.txt"}) })
}

func TestAltAndALetterOpenTheFilesMenus(t *testing.T) {
	h := newHarness(t, "a.txt")
	h.w.Input(input.KeyPress{Key: input.KeyLeftAlt, Mods: input.ModAlt, Time: time.Now()})
	h.press(input.KeyV, input.ModAlt)
	h.w.Input(input.KeyRelease{Key: input.KeyLeftAlt, Time: time.Now()})
	h.frames(2)
	if !h.b.title.bar.IsOpen() {
		t.Fatal("Alt+V left the menus shut")
	}
	h.press(input.KeyEscape, 0)
	h.frames(20)
	if h.b.title.bar.IsOpen() {
		t.Fatal("Escape left a menu open")
	}
}

func TestTheArrowsMoveAlongThePathAndTabComesBackThere(t *testing.T) {
	h := newHarness(t, "a.txt")
	n := len(h.places())
	if n < 3 {
		t.Fatalf("the path has %d folders, want at least 3", n)
	}
	for range 40 {
		if h.onPlace() == 0 {
			break
		}
		h.press(input.KeyTab, 0)
	}
	if h.onPlace() != 0 {
		t.Fatal("Tab never reached the first folder of the path")
	}
	h.press(input.KeyRight, 0)
	if h.onPlace() != 1 {
		t.Fatal("Right did not move to the second folder")
	}
	h.press(input.KeyEnd, 0)
	if h.onPlace() != n-1 {
		t.Fatal("End did not move to the last folder")
	}
	h.press(input.KeyLeft, 0)
	at := h.onPlace()
	h.press(input.KeyTab, 0)
	h.press(input.KeyTab, input.ModShift)
	if h.onPlace() != at {
		t.Fatal("Shift+Tab back into the path did not come back to the folder it left")
	}
	h.press(input.KeyHome, 0)
	if h.onPlace() != 0 {
		t.Fatal("Home did not move to the first folder")
	}
}

func TestTheMousesSideButtonsGoBackAndForward(t *testing.T) {
	h := newHarness(t, "sub/x.txt")
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens", func() bool { return slices.Equal(h.shown(), []string{"x.txt"}) })
	side := func(b input.Button, over gunim.Node) {
		r := h.bounds(func(*browser) gunim.Node { return over })
		at := r.Center()
		h.w.Input(input.PointerDown{Pos: at, Button: b, Time: time.Now()})
		h.w.Input(input.PointerUp{Pos: at, Button: b, Time: time.Now()})
		h.frames(2)
	}
	// Back over the listing, forward over the sidebar, and back again over the preview
	side(input.ButtonBack, h.b.listing)
	h.until("Back goes to the folder before", func() bool { return slices.Equal(h.shown(), []string{"sub"}) })
	side(input.ButtonForward, h.b.side)
	h.until("Forward goes into the folder again", func() bool { return slices.Equal(h.shown(), []string{"x.txt"}) })
	side(input.ButtonBack, h.b.preview)
	h.until("Back over the preview goes back", func() bool { return slices.Equal(h.shown(), []string{"sub"}) })
}

func TestTypingAtTheListingGoesToTheNameTyped(t *testing.T) {
	h := newHarness(t, "apple.txt", "banana.txt", "berry.txt", "cherry.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 4 })
	h.do(FocusListing{})
	h.frames(2)
	typeText := func(s string) {
		h.w.Input(input.TextInput{Text: s, Time: time.Now()})
		h.frames(2)
	}
	typeText("b")
	h.until("b goes to banana", func() bool { return h.a.nav.cursor == "banana.txt" })
	typeText("e")
	h.until("be goes on to berry", func() bool { return h.a.nav.cursor == "berry.txt" })
	if len(h.a.nav.sel) != 1 || !h.a.nav.sel["berry.txt"] {
		t.Fatalf("the selection is %v, want berry alone", h.a.nav.sel)
	}
	// A pause starts the text again
	h.frames(70)
	typeText("a")
	h.until("after a pause, a goes to apple", func() bool { return h.a.nav.cursor == "apple.txt" })
}

func TestTheSelectionSlidesToANameTypedAndToAClickedRow(t *testing.T) {
	h := newHarness(t, "apple.txt", "banana.txt", "berry.txt", "cherry.txt")
	gunim.RegisterPatch(h.w, "browser", func(b *browser, p inUI, u *gunim.UI) { p.fn(b, u) })
	h.until("the rows arrive", func() bool { return len(h.shown()) == 4 })
	h.do(FocusListing{})
	h.press(input.KeyDown, 0)
	h.frames(60)
	g := h.b.listing.cur.grid
	if g.Sliding() {
		t.Fatal("the selection is still sliding a second after Down")
	}
	h.w.Input(input.TextInput{Text: "c", Time: time.Now()})
	h.until("c goes to cherry", func() bool { return h.a.nav.cursor == "cherry.txt" })
	if !g.Sliding() {
		t.Fatal("the selection jumped to the name typed")
	}
	h.frames(60)
	h.w.Input(input.PointerDown{Pos: h.point("banana.txt"), Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	h.w.Input(input.PointerUp{Pos: h.point("banana.txt"), Button: input.ButtonPrimary, Time: time.Now()})
	h.frames(1)
	if !g.Sliding() {
		t.Fatal("the selection jumped to the row clicked")
	}
	h.until("the click selects banana", func() bool { return h.a.nav.cursor == "banana.txt" })
}

func TestCtrlAndAClickOpenAFolderInANewWindow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var opened []string
	hb := &Hub{open: func(o Options) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, o.Dir)
		return nil
	}}
	h := openHarness(t, hb, root, dir)
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(opened)
	}
	click := func(at geom.Point, mods input.Mods) {
		h.w.Input(input.PointerMove{Pos: at, Mods: mods, Time: time.Now()})
		h.w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Mods: mods, Time: time.Now()})
		h.w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Mods: mods, Time: time.Now()})
		h.frames(2)
	}
	up := h.bounds(func(b *browser) gunim.Node { return b.path.up })
	click(up.Center(), input.ModControl)
	h.until("Ctrl and Up ask for the parent in a new window", func() bool {
		o := got()
		return len(o) == 1 && SystemPaths.Same(o[0], filepath.Join(root, "a"))
	})
	if !SystemPaths.Same(h.a.nav.path, dir) {
		t.Fatalf("Ctrl and Up moved this window to %s", h.a.nav.path)
	}
	// A folder of the path, Ctrl+clicked
	places := h.places()
	a := places[len(places)-2]
	click(geom.Pt(a.Rect.Min.X+8, a.Rect.Center().Y), input.ModControl)
	h.until("Ctrl and a folder of the path ask for it in a new window", func() bool { return len(got()) == 2 })
	// A plain click on Up goes up here
	click(up.Center(), 0)
	h.until("a plain click on Up goes up in this window", func() bool { return SystemPaths.Same(h.a.nav.path, filepath.Join(root, "a")) })
	if len(got()) != 2 {
		t.Fatalf("a plain click asked for a window: %v", got())
	}
}

// The banner's Dismiss link, shut away with the banner, hands the keyboard to the listing.
func TestDismissingTheBannerByKeyboardSendsTheKeyboardToTheListing(t *testing.T) {
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.a.fail("Something broke")
	h.frames(30)
	gunim.RegisterPatch(h.w, "browser", func(b *browser, p inUI, u *gunim.UI) { p.fn(b, u) })
	h.ui(func(b *browser, u *gunim.UI) { u.Focus(b.banner.dismiss) })
	if f := h.focused(); f != h.b.banner.dismiss {
		t.Fatalf("the keyboard is on %T, want the banner's Dismiss link", f)
	}
	h.press(input.KeyEnter, 0)
	h.frames(30)
	if f := h.focused(); f != h.b.listing.cur.focusNode() {
		t.Fatalf("with the banner dismissed, the keyboard is on %T, want the listing", f)
	}
}
