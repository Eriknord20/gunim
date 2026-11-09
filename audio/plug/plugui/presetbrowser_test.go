package plugui

import (
	"math"
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
	"github.com/marrasen/gunim/paint"
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

// sized lays its child out at size, as a window that gives the browser its room only once it opens.
type sized struct {
	kid  gunim.Node
	size geom.Size
}

func (s *sized) Children() []gunim.Node { return []gunim.Node{s.kid} }

func (s *sized) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(s.size))
	kids.At(0).Place(geom.Point{})
	return c.Max
}

func (s *sized) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// offCentre is how far row, a box in s's content, stands from the middle of a view h tall.
func offCentre(s *widget.Scroll, row geom.Rect, h float32) float32 {
	mid := (row.Min.Y+row.Max.Y)/2 - s.Offset()
	return float32(math.Abs(float64(mid - h/2)))
}

// A reveal asked for before the browser has room waits for it, then centres the preset playing and its group on
// that first layout, at once.
func TestARevealCentresThePresetPlayingOnTheFirstLayout(t *testing.T) {
	src := kits(60)
	for i := range 40 {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Group " + strconv.Itoa(100+i), Name: "More " + strconv.Itoa(i)})
	}
	src.cur = src.list[3+40]
	b := NewPresetBrowser(src)
	b.ShowPlaying(nil)
	w := gunimtest.New(t, geom.Sz(360, 480), nil)
	s := &sized{kid: b}
	gunim.RegisterView(w, "sized", func(struct{}) gunim.Node { return s }, nil)
	if err := w.Client().Mount(gunim.Root, "sized", "sized", nil); err != nil {
		t.Fatal(err)
	}
	frames(w, 2)
	gunim.RegisterPatch(w, "sized", func(s *sized, size geom.Size, u *gunim.UI) {
		s.size = size
		u.Invalidate()
	})
	if err := w.Client().Patch("sized", geom.Sz(360, 480)); err != nil {
		t.Fatal(err)
	}
	h := float32(480 - fieldH - colGap)
	row := b.presets.rowRect(b.presets.current())
	for f := range 30 {
		w.Frame(time.Second / 60)
		if d := offCentre(b.ps, row, h); d > rowH/2 {
			t.Fatalf("frame %d: the preset playing stands %v from the middle of its column", f, d)
		}
		if b.gs.Offset() > 0 {
			t.Fatalf("frame %d: the groups' column scrolled to %v, want its top, as Kits is near it", f, b.gs.Offset())
		}
	}
}

// A reveal of a group far down its column centres it too.
func TestARevealCentresTheGroup(t *testing.T) {
	src := &presetHost{}
	for i := range 60 {
		src.list = append(src.list, plug.Preset{Bank: "Factory", Group: "Group " + strconv.Itoa(100+i), Name: "Sound " + strconv.Itoa(i)})
	}
	src.cur = src.list[40]
	b := NewPresetBrowser(src)
	b.ShowPlaying(nil)
	w := mountBrowser(t, b)
	h := viewOf(t, w, b.gs)
	if d := offCentre(b.gs, b.groups.rowRect(b.groups.index("Factory", "Group 140")), h); d > rowH/2 {
		t.Fatalf("the group stands %v from the middle of its column", d)
	}
}

// A reveal asked for once the browser has been laid out glides the preset playing to the middle of its column.
func TestALaterRevealGlidesToTheMiddle(t *testing.T) {
	src := kits(60)
	src.cur = src.list[3]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Follow(u) })
	before := b.ps.Offset()
	src.cur = src.list[3+40]
	gunim.RegisterPatch(w, "browser", func(b *PresetBrowser, _ struct{}, u *gunim.UI) { b.ShowPlaying(u) })
	if err := w.Client().Patch("browser", struct{}{}); err != nil {
		t.Fatal(err)
	}
	h := float32(480 - fieldH - colGap)
	row := b.presets.rowRect(b.presets.current())
	frames(w, 2)
	if mid := b.ps.Offset(); mid <= before || offCentre(b.ps, row, h) < rowH {
		t.Fatalf("two frames in, the column stands at %v, want it on the way from %v", mid, before)
	}
	frames(w, 60)
	if d := offCentre(b.ps, row, h); d > rowH/2 {
		t.Fatalf("the preset playing stands %v from the middle of its column", d)
	}
}

// Tab brings the keyboard to each column, and its focus ring fades in; a click leaves no ring.
func TestTheColumnsShowAFocusRing(t *testing.T) {
	b := NewPresetBrowser(newPresetHost())
	w := mountBrowser(t, b)
	var on []string
	for range 3 {
		w.Input(input.KeyPress{Key: input.KeyTab})
		w.Frame(time.Second / 60)
		if g, p := b.groups.ring.Value(), b.presets.ring.Value(); g > 0.99 || p > 0.99 {
			t.Fatalf("a frame after Tab, the rings are at %v and %v, want them fading in", g, p)
		}
		frames(w, 30)
		switch {
		case b.groups.ring.Value() > 0.99:
			on = append(on, "groups")
		case b.presets.ring.Value() > 0.99:
			on = append(on, "presets")
		default:
			on = append(on, "")
		}
	}
	if want := []string{"", "groups", "presets"}; !slices.Equal(on, want) {
		t.Fatalf("Tab ringed %q, want %q", on, want)
	}
	at := geom.Pt(20, fieldH+colGap+b.groups.rows[1].y+rowH/2)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	frames(w, 30)
	if g, p := b.groups.ring.Value(), b.presets.ring.Value(); g > 0.01 || p > 0.01 {
		t.Fatalf("after a click the rings are at %v and %v, want none", g, p)
	}
}

// groupsHost is a source of two banks of 20 groups.
func groupsHost() *presetHost {
	src := &presetHost{}
	for _, bank := range []string{"Factory", "User"} {
		for i := range 20 {
			g := bank + " " + strconv.Itoa(i)
			src.list = append(src.list, plug.Preset{Bank: bank, Group: g, Name: g + " a"}, plug.Preset{Bank: bank, Group: g, Name: g + " b"})
		}
	}
	src.cur = src.list[0]
	return src
}

// After a click on a group, Up and Down show the group before and after, across banks, and Home, End, Page Up and
// Page Down go to the ends and a page on.
func TestKeysMoveThroughTheGroups(t *testing.T) {
	src := groupsHost()
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	i := b.groups.index("Factory", "Factory 18")
	b.gs.JumpTo(b.groups.rows[i].y - 100)
	frames(w, 10)
	at := geom.Pt(20, fieldH+colGap+b.groups.rows[i].y-b.gs.Offset()+rowH/2)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	frames(w, 2)
	if b.group != "Factory 18" {
		t.Fatalf("the click showed %q, want Factory 18", b.group)
	}
	page := int((480-fieldH-colGap)/rowH) - 1
	for _, c := range []struct {
		key  input.Key
		want string
	}{
		{input.KeyDown, "Factory 19"},
		{input.KeyDown, "User 0"},
		{input.KeyUp, "Factory 19"},
		{input.KeyHome, "Factory 0"},
		{input.KeyUp, "Factory 0"},
		{input.KeyPageDown, "Factory " + strconv.Itoa(page)},
		{input.KeyEnd, "User 19"},
		{input.KeyDown, "User 19"},
		{input.KeyPageUp, "User " + strconv.Itoa(19-page)},
	} {
		w.Input(input.KeyPress{Key: c.key})
		frames(w, 20)
		if b.group != c.want {
			t.Fatalf("%v showed %q, want %q", c.key, b.group, c.want)
		}
		shows(t, w, b.gs, b.groups.rowRect(b.groups.index(b.bank, b.group)), b.group)
	}
	if len(src.picked) != 0 {
		t.Fatalf("moving through the groups picked %v, want nothing", src.picked)
	}
}

// With the keyboard in the presets, Home, End, Page Up and Page Down play the first, the last and a page on.
func TestHomeEndAndPagesPlayThePresets(t *testing.T) {
	src := kits(60)
	src.cur = src.list[3+10]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Follow(u) })
	row := b.presets.rows[b.presets.current()]
	w360 := float32(360)
	gw := float32(int(w360 * groupsW))
	at := geom.Pt(gw+colGap+20, fieldH+colGap+row.y-b.ps.Offset()+row.h/2)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	frames(w, 2)
	page := int((480-fieldH-colGap)/rowH) - 1
	for _, c := range []struct {
		key  input.Key
		want int
	}{
		{input.KeyPageDown, 10 + page},
		{input.KeyHome, 0},
		{input.KeyPageUp, 0},
		{input.KeyEnd, 59},
		{input.KeyPageDown, 59},
		{input.KeyPageUp, 59 - page},
	} {
		w.Input(input.KeyPress{Key: c.key})
		frames(w, 20)
		if want := "Kit " + strconv.Itoa(c.want); src.cur.Name != want {
			t.Fatalf("%v played %q, want %q", c.key, src.cur.Name, want)
		}
		shows(t, w, b.ps, b.presets.rowRect(b.presets.current()), src.cur.Name)
	}
}

// Tab into a column scrolled to the preset playing keeps that preset in view, not the column's top.
func TestTabIntoAColumnKeepsThePresetPlayingInView(t *testing.T) {
	src := kits(60)
	src.cur = src.list[3+40]
	b := NewPresetBrowser(src)
	b.ShowPlaying(nil)
	w := mountBrowser(t, b)
	for range 3 {
		w.Input(input.KeyPress{Key: input.KeyTab})
		frames(w, 30)
	}
	if b.presets.ring.Value() < 0.99 {
		t.Fatal("three Tabs didn't reach the presets")
	}
	shows(t, w, b.ps, b.presets.rowRect(b.presets.current()), "the preset playing")
	shows(t, w, b.gs, b.groups.rowRect(b.groups.index(b.bank, b.group)), "the group shown")
}
