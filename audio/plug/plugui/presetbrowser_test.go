package plugui

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// presetHost is a source of presets that plays the one picked.
type presetHost struct {
	list   []plug.Preset
	cur    plug.Preset
	picked []string
}

func (s *presetHost) Presets() []plug.Preset { return s.list }

func (s *presetHost) Current() (plug.Preset, bool) { return s.cur, s.cur.Name != "" }

func (s *presetHost) Pick(p plug.Preset, _ *gunim.UI) {
	s.cur = p
	s.picked = append(s.picked, p.Name)
}

func newPresetHost() *presetHost {
	ps := []plug.Preset{
		{Bank: "User", Group: "Mine", Name: "Warm"},
		{Bank: "Factory", Group: "Bass", Name: "Sub"},
		{Bank: "Factory", Group: "Bass", Name: "Growl", About: "a dirty saw"},
		{Bank: "Factory", Group: "Pads", Name: "Glass"},
	}
	return &presetHost{list: ps, cur: ps[2]}
}

var _ gunim.Animator = (*PresetBrowser)(nil)

// mountBrowser shows b alone in a window, and returns the window.
func mountBrowser(t *testing.T, b *PresetBrowser) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(360, 480), nil)
	gunim.RegisterView(w, "browser", func(struct{}) gunim.Node { return b }, nil)
	if err := w.Client().Mount(gunim.Root, "browser", "browser", nil); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		w.Frame(time.Second / 60)
	}
	return w
}

func TestTheBrowserShowsTheGroupOfThePresetPlaying(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	mountBrowser(t, b)
	if b.bank != "Factory" || b.group != "Bass" {
		t.Fatalf("the browser shows %s/%s, want the group of the preset playing", b.bank, b.group)
	}
	i := b.groups.index("Factory", "Bass")
	if got := b.groups.pill.Value(); got != float32(i) {
		t.Fatalf("the groups' pill stands on row %v, want %d", got, i)
	}
	if got := b.presets.pill.Value(); got != float32(b.presets.current()) {
		t.Fatalf("the presets' pill stands on row %v, want %d", got, b.presets.current())
	}
}

func TestSkipsGoRoundTheGroup(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	gunim.RegisterPatch(w, "browser", func(b *PresetBrowser, by int, u *gunim.UI) { b.Skip(by, u) })
	for _, by := range []int{1, 1, -1} {
		if err := w.Client().Patch("browser", by); err != nil {
			t.Fatal(err)
		}
		w.Frame(time.Second / 60)
	}
	want := []string{"Sub", "Growl", "Sub"}
	if len(src.picked) != 3 || src.picked[0] != want[0] || src.picked[1] != want[1] || src.picked[2] != want[2] {
		t.Fatalf("the skips picked %v, want %v", src.picked, want)
	}
}

// frames runs n frames of w.
func frames(w *gunim.Window, n int) {
	for range n {
		w.Frame(time.Second / 60)
	}
}

// viewOf is the height of scroll s as w last showed it.
func viewOf(t *testing.T, w *gunim.Window, s *widget.Scroll) float32 {
	t.Helper()
	var view geom.Rect
	gunim.RegisterPatch(w, "browser", func(_ *PresetBrowser, _ struct{}, u *gunim.UI) { view, _ = u.Bounds(s) })
	if err := w.Client().Patch("browser", struct{}{}); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return view.Size().H
}

// shows fails unless row, a box in s's content, stands wholly in s's
// view.
func shows(t *testing.T, w *gunim.Window, s *widget.Scroll, row geom.Rect, what string) {
	t.Helper()
	h := viewOf(t, w, s)
	if top, bottom := row.Min.Y-s.Offset(), row.Max.Y-s.Offset(); top < 0 || bottom > h {
		t.Fatalf("%s stands at %v to %v in a view %v tall, scrolled to %v", what, top, bottom, h, s.Offset())
	}
}

// patch sends fn to the browser in w, and lets it run a second.
func patch(t *testing.T, w *gunim.Window, fn func(b *PresetBrowser, u *gunim.UI)) {
	t.Helper()
	gunim.RegisterPatch(w, "browser", func(b *PresetBrowser, _ struct{}, u *gunim.UI) { fn(b, u) })
	if err := w.Client().Patch("browser", struct{}{}); err != nil {
		t.Fatal(err)
	}
	frames(w, 60)
}

// kits is a host of one group of n presets, and of a group of a few
// before it.
func kits(n int) *presetHost {
	src := &presetHost{}
	for i := range 3 {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Bass", Name: "Bass " + strconv.Itoa(i)})
	}
	for i := range n {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Kits", Name: "Kit " + strconv.Itoa(i)})
	}
	return src
}

// TestTheNextPresetOfAScrolledColumnKeepsItInView: a step to the
// preset below the one playing, in a column scrolled down, scrolls only
// as far as the row needs. It once read the row's place in the list as
// its place in the view, and scrolled past it by as far again.
func TestTheNextPresetOfAScrolledColumnKeepsItInView(t *testing.T) {
	src := kits(60)
	src.cur = src.list[33]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	// Scrolled down, the preset playing a little below the view's top.
	b.ps.JumpTo(b.presets.rowRect(b.presets.current()).Min.Y - 60)
	frames(w, 10)
	before := b.ps.Offset()
	if before <= 0 {
		t.Fatalf("the column stands at %v, want it scrolled", before)
	}
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Skip(1, u) })
	row := b.presets.rowRect(b.presets.current())
	shows(t, w, b.ps, row, "the preset picked")
	if moved := b.ps.Offset() - before; moved > 2*row.Size().H {
		t.Fatalf("a step to the next preset scrolled the column %v, more than two rows", moved)
	}
}

// TestTheNextGroupOfAScrolledColumnKeepsItInView: the groups' column,
// scrolled down, scrolls only as far as the group of the preset playing
// needs, as the presets' does.
func TestTheNextGroupOfAScrolledColumnKeepsItInView(t *testing.T) {
	src := &presetHost{}
	for i := range 60 {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Group " + strconv.Itoa(100+i), Name: "Sound " + strconv.Itoa(i)})
	}
	src.cur = src.list[30]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	i := b.groups.index("Factory", "Group 130")
	b.gs.JumpTo(b.groups.rowRect(i).Min.Y - 60)
	frames(w, 10)
	before := b.gs.Offset()
	if before <= 0 {
		t.Fatalf("the groups' column stands at %v, want it scrolled", before)
	}
	src.cur = src.list[31]
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Follow(u) })
	row := b.groups.rowRect(b.groups.index("Factory", "Group 131"))
	shows(t, w, b.gs, row, "the group of the preset playing")
	if moved := b.gs.Offset() - before; moved > 2*row.Size().H {
		t.Fatalf("a step to the next group scrolled the column %v, more than two rows", moved)
	}
}

// TestFollowingIntoALongGroupShowsThePresetPlaying: a preset set
// elsewhere, far down a long group, shows once the browser follows it
// from a short group, though the column's scroll last held the short
// group's height.
func TestFollowingIntoALongGroupShowsThePresetPlaying(t *testing.T) {
	src := kits(60)
	src.cur = src.list[1]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	if b.group != "Bass" {
		t.Fatalf("the browser shows %q, want Bass", b.group)
	}
	src.cur = src.list[3+50]
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Follow(u) })
	shows(t, w, b.ps, b.presets.rowRect(b.presets.current()), "Kit 50")
}

// TestARevealWhileTheColumnStillScrollsLandsOnTheRow: a step back
// while the step before still scrolls the column lands on the row
// stepped to, not where that scroll was going.
func TestARevealWhileTheColumnStillScrollsLandsOnTheRow(t *testing.T) {
	src := kits(60)
	src.cur = src.list[len(src.list)-1]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Follow(u) })
	// Round to the first, then at once back to the last, before the
	// column has moved from the bottom.
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) {
		b.Skip(1, u)
		b.Skip(-1, u)
	})
	shows(t, w, b.ps, b.presets.rowRect(b.presets.current()), "the last preset")
}

func TestASearchFindsWhatAPresetIs(t *testing.T) {
	b := NewPresetBrowser(newPresetHost())
	b.query = "dirty"
	b.fill(nil)
	var names []string
	for _, r := range b.presets.rows {
		if !r.header {
			names = append(names, r.p.Name)
		}
	}
	if len(names) != 1 || names[0] != "Growl" {
		t.Fatalf("a search for what it sounds like finds %v", names)
	}
	b.query = "factory"
	b.fill(nil)
	if len(b.presets.rows) != 0 {
		t.Fatal("a search finds presets by their bank's name")
	}
}

func TestABrowserOfNoPresetsShowsNoGroup(t *testing.T) {
	b := NewPresetBrowser(&presetHost{})
	mountBrowser(t, b)
	if len(b.groups.rows) != 0 || len(b.presets.rows) != 0 || b.groups.index(b.bank, b.group) >= 0 {
		t.Fatal("a browser of no presets lists rows")
	}
}

// listNamed is the list named name in the window's tree.
func listNamed(t *testing.T, w *gunim.Window, name string) *access.Node {
	t.Helper()
	var found *access.Node
	var walk func(n *access.Node)
	walk = func(n *access.Node) {
		if n.Role == access.RoleList && n.Name == name {
			found = n
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(w.Access().Root)
	if found == nil {
		t.Fatalf("the window shows no list %q", name)
	}
	return found
}

// rowNamed is the row of list named name.
func rowNamed(t *testing.T, list *access.Node, name string) *access.Node {
	t.Helper()
	for _, k := range list.Children {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("the list %q has no row %q", list.Name, name)
	return nil
}

// Each row of the columns says what it shows: the banks and groups as
// captions, a group with how many presets it holds, and a preset by its
// name and where it is filed, the one playing selected. A press on a
// group shows its presets; a press on a preset picks it.
func TestTheRowsSayWhatTheyShowAndAPressActs(t *testing.T) {
	src := newPresetHost()
	src.list = slices.Insert(src.list, 3, plug.Preset{Bank: "Factory", Group: "Bass", Name: "Kit", Drums: true})
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	groups := listNamed(t, w, "Groups")
	if h := rowNamed(t, groups, "Factory"); h.Role != access.RoleHeading {
		t.Fatalf("the bank's caption is %v", h.Role)
	}
	if g := rowNamed(t, groups, "Bass"); g.Role != access.RoleListItem || g.Description != "3 presets" || g.Value != "" || !g.State.Has(access.StateSelected) {
		t.Fatalf("the group shown says %+v", g.Info)
	}
	presets := listNamed(t, w, "Presets")
	if h := rowNamed(t, presets, "Bass"); h.Role != access.RoleHeading {
		t.Fatalf("the group's caption is %v", h.Role)
	}
	if p := rowNamed(t, presets, "Growl"); p.Role != access.RoleListItem || p.Description != "Factory · Bass" || !p.State.Has(access.StateSelected) {
		t.Fatalf("the preset playing says %+v", p.Info)
	}
	if p := rowNamed(t, presets, "Kit"); p.Description != "Factory · Bass · drums" || p.State.Has(access.StateSelected) {
		t.Fatalf("a drum kit says %+v", p.Info)
	}
	w.Input(access.Request{ID: rowNamed(t, presets, "Sub").ID, Action: access.ActionPress})
	w.Frame(time.Second / 60)
	if len(src.picked) != 1 || src.picked[0] != "Sub" {
		t.Fatalf("a press on Sub picked %v", src.picked)
	}
	w.Input(access.Request{ID: rowNamed(t, listNamed(t, w, "Groups"), "Pads").ID, Action: access.ActionPress})
	w.Frame(time.Second / 60)
	if b.group != "Pads" {
		t.Fatalf("a press on Pads shows %s", b.group)
	}
	rowNamed(t, listNamed(t, w, "Presets"), "Glass")
}

// Only the rows in view are listed, as the column scrolls.
func TestOnlyTheRowsInViewAreListed(t *testing.T) {
	src := &presetHost{}
	for i := range 60 {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Keys", Name: "Keys " + strconv.Itoa(i+1)})
	}
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	presets := listNamed(t, w, "Presets")
	if n := len(presets.Children); n < 5 || n > 30 {
		t.Fatalf("%d of 61 rows listed", n)
	}
	if first := presets.Children[0].Name; first != "Keys" {
		t.Fatalf("the first row listed is %q", first)
	}
	b.ps.JumpTo(26 * 40)
	for range 5 {
		w.Frame(time.Second / 60)
	}
	names := map[string]bool{}
	for _, k := range listNamed(t, w, "Presets").Children {
		names[k.Name] = true
	}
	if names["Keys 1"] || !names["Keys 41"] {
		t.Fatalf("scrolled down, the rows listed are %v", names)
	}
	inView(t, listNamed(t, w, "Presets"))
}

// inView fails unless every row of list lies in the column's view, under
// the search field and inside the window, a row half scrolled away cut
// to the part that shows.
func inView(t *testing.T, list *access.Node) {
	t.Helper()
	for _, k := range list.Children {
		if k.Bounds.Min.Y < fieldH+colGap-0.5 || k.Bounds.Max.Y > 480.5 || k.Bounds.Empty() {
			t.Fatalf("the row %q stands at %v, out of view", k.Name, k.Bounds)
		}
	}
}

// The groups column lists the groups in view as it scrolls too.
func TestOnlyTheGroupsInViewAreListed(t *testing.T) {
	src := &presetHost{}
	for i := range 60 {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Group " + strconv.Itoa(i+1), Name: "Sound"})
	}
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	groups := listNamed(t, w, "Groups")
	if n := len(groups.Children); n < 5 || n > 30 || groups.Children[0].Name != "Factory" {
		t.Fatalf("%d of 61 rows listed, the first %q", n, groups.Children[0].Name)
	}
	inView(t, groups)
	b.gs.JumpTo(26*40 + 13)
	for range 5 {
		w.Frame(time.Second / 60)
	}
	names := map[string]bool{}
	groups = listNamed(t, w, "Groups")
	for _, k := range groups.Children {
		names[k.Name] = true
	}
	if names["Factory"] || names["Group 1"] || !names["Group 42"] {
		t.Fatalf("scrolled down, the groups listed are %v", names)
	}
	inView(t, groups)
}

// During a search the presets column lists what it finds under their
// groups' captions, and no group is selected: each counts what it finds.
func TestASearchListsWhatItFindsUnderTheirGroups(t *testing.T) {
	b := NewPresetBrowser(newPresetHost())
	w := mountBrowser(t, b)
	b.query = "dirty"
	b.fill(nil)
	w.Frame(time.Second / 60)
	presets := listNamed(t, w, "Presets")
	if len(presets.Children) != 2 || presets.Children[0].Role != access.RoleHeading || presets.Children[0].Name != "Bass" || presets.Children[1].Name != "Growl" {
		t.Fatalf("the search lists %d rows", len(presets.Children))
	}
	for _, g := range listNamed(t, w, "Groups").Children {
		if g.State.Has(access.StateSelected) {
			t.Fatalf("during a search, the group %q is selected", g.Name)
		}
	}
	if g := rowNamed(t, listNamed(t, w, "Groups"), "Bass"); g.Description != "1 preset" {
		t.Fatalf("during a search, Bass says %q", g.Description)
	}
	if g := rowNamed(t, listNamed(t, w, "Groups"), "Pads"); g.Description != "0 presets" {
		t.Fatalf("during a search, Pads says %q", g.Description)
	}
}

// A press acts on the row it was read from, wherever that row now
// stands, and on none once it has gone, though the rows changed since
// the tree was read, with no frame between.
func TestAPressAfterTheRowsChangeActsOnTheRowItWasFor(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	growl := rowNamed(t, listNamed(t, w, "Presets"), "Growl").ID
	// Pads shows its presets: Growl is gone from the column.
	w.Input(access.Request{ID: rowNamed(t, listNamed(t, w, "Groups"), "Pads").ID, Action: access.ActionPress})
	w.Input(access.Request{ID: growl, Action: access.ActionPress})
	w.Frame(time.Second / 60)
	if b.group != "Pads" || len(src.picked) != 0 {
		t.Fatalf("the press on Growl, gone, picked %v; the group shown is %s", src.picked, b.group)
	}
	// Back on Bass, its presets come in a new order.
	b.showGroup("Factory", "Bass", nil)
	w.Frame(time.Second / 60)
	sub := rowNamed(t, listNamed(t, w, "Presets"), "Sub").ID
	src.list[1], src.list[2] = src.list[2], src.list[1]
	b.Refresh(nil)
	w.Input(access.Request{ID: sub, Action: access.ActionPress})
	w.Frame(time.Second / 60)
	if len(src.picked) != 1 || src.picked[0] != "Sub" {
		t.Fatalf("the press on Sub, moved, picked %v", src.picked)
	}
}

// Two presets of one name in one group, as two folders a bank is read
// from may hold, are two rows apart: a press on each picks it.
func TestTwoPresetsOfOneNameAreRowsApart(t *testing.T) {
	src := &presetHost{list: []plug.Preset{
		{Bank: "Factory", Group: "Bass", Name: "Sub", Pitch: 1},
		{Bank: "Factory", Group: "Bass", Name: "Sub", Pitch: 2},
	}}
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	presets := listNamed(t, w, "Presets")
	if len(presets.Children) != 3 || presets.Children[1].ID == presets.Children[2].ID {
		t.Fatalf("the two Subs list as %d rows, IDs apart %v", len(presets.Children), len(presets.Children) == 3 && presets.Children[1].ID != presets.Children[2].ID)
	}
	for i, want := range []float64{1, 2} {
		w.Input(access.Request{ID: listNamed(t, w, "Presets").Children[1+i].ID, Action: access.ActionPress})
		w.Frame(time.Second / 60)
		if src.cur.Pitch != want {
			t.Fatalf("a press on Sub %d picked the one of pitch %v", i+1, src.cur.Pitch)
		}
	}
}

// A click on a preset puts the keyboard in the browser, where Down and Up play the preset after and before; they do so
// from the search field too.
func TestUpAndDownPlayThePresetsBeside(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	i := slices.IndexFunc(b.presets.rows, func(r listRow) bool { return !r.header && r.p.Name == "Sub" })
	if i < 0 {
		t.Fatal("Sub isn't listed")
	}
	w360 := float32(360)
	gw := float32(int(w360 * groupsW))
	at := geom.Pt(gw+colGap+20, fieldH+colGap+b.presets.rows[i].y+b.presets.rows[i].h/2)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	frames(w, 2)
	for _, k := range []input.Key{input.KeyDown, input.KeyUp} {
		w.Input(input.KeyPress{Key: k})
		frames(w, 1)
	}
	if want := []string{"Sub", "Growl", "Sub"}; !slices.Equal(src.picked, want) {
		t.Fatalf("the click and the keys picked %v, want %v", src.picked, want)
	}
	at = geom.Pt(40, fieldH/2)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	frames(w, 2)
	w.Input(input.KeyPress{Key: input.KeyDown})
	frames(w, 1)
	if want := []string{"Sub", "Growl", "Sub", "Growl"}; !slices.Equal(src.picked, want) {
		t.Fatalf("Down in the search field picked %v, want %v", src.picked, want)
	}
}
