package widget

import (
	"fmt"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

type pageChosen struct{ I int }

type chooseNav struct{ I int }

type setNavItems struct{ Items []NavItem }

// navPages are a window's pages, as Hjortron's main window has them.
func navPages() []NavItem {
	return []NavItem{
		{Label: "Overview", Icon: icon.Home},
		{Label: "Storage", Icon: icon.Folder},
		{Label: "Activity", Icon: icon.List},
		{Label: "Can't sync", Icon: icon.CircleAlert, Count: 4},
		{Label: "Settings", Icon: icon.Settings, Foot: true},
	}
}

// navBox lays its child out 220 wide, in the height it is given.
type navBox struct{ child gunim.Node }

func (z *navBox) Children() []gunim.Node { return []gunim.Node{z.child} }

func (z *navBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Constraints{Min: geom.Sz(220, 0), Max: geom.Sz(220, c.Max.H)})
	k.Place(geom.Point{})
	return s
}

func (z *navBox) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// stageNav mounts n in a column 600 high, and a patch that chooses a row and one that sets the rows, from the view.
func stageNav(t *testing.T, n *NavList) (w *gunim.Window, run func(int)) {
	t.Helper()
	w, run = stage(t, &frame{child: &navBox{child: n}, size: geom.Sz(400, 600)})
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c chooseNav, u *gunim.UI) { n.SetSelected(c.I, u) })
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c setNavItems, u *gunim.UI) { n.SetItems(c.Items, u) })
	return w, run
}

func TestANavListChoosesByClickAndThePillGlides(t *testing.T) {
	n := NewNavList(navPages()...)
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	w, run := stageNav(t, n)
	run(1)
	pitch := NavRowHeight.Default() + NavGap.Default()
	click(w, 100, 2*pitch+10)
	run(1)
	if got := sent(w); n.Selected() != 2 || len(got) != 1 || got[0] != (pageChosen{2}) {
		t.Fatalf("a click on Activity chose %d and sent %v", n.Selected(), got)
	}
	// The pill leaves Overview and comes to Activity on every frame, never past it and never back.
	last := n.top(0) + n.lag.Value()
	for f := range 60 {
		run(1)
		y := n.top(2) + n.lag.Value()
		if y < last-0.01 || y > n.top(2)+0.01 {
			t.Fatalf("frame %d: the pill is at %v, after %v; want it gliding down to %v", f, y, last, n.top(2))
		}
		last = y
	}
	if last < n.top(2)-0.01 {
		t.Fatalf("the pill rests at %v, want on Activity at %v", last, n.top(2))
	}
	// A click in the gap between two rows chooses nothing.
	click(w, 100, pitch-NavGap.Default()/2)
	run(1)
	if got := sent(w); n.Selected() != 2 || len(got) != 0 {
		t.Fatalf("a click between rows chose %d and sent %v", n.Selected(), got)
	}
}

func TestSettingANavListsChoiceSendsNothing(t *testing.T) {
	n := NewNavList(navPages()...)
	n.SetSelected(1, nil)
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	w, run := stageNav(t, n)
	if y := n.top(1) + n.lag.Value(); y != n.top(1) {
		t.Fatalf("set before the first layout, the pill is at %v, want on Storage at %v at once", y, n.top(1))
	}
	if err := w.Client().Patch("stage", chooseNav{3}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if got := sent(w); n.Selected() != 3 || len(got) != 0 {
		t.Fatalf("SetSelected(3) chose %d and sent %v", n.Selected(), got)
	}
	if y := n.top(3) + n.lag.Value(); y <= n.top(1) || y >= n.top(3) {
		t.Fatalf("a frame after SetSelected(3) the pill is at %v, want on its way from Storage", y)
	}
}

func TestANavListWorksFromTheKeyboard(t *testing.T) {
	b, n := NewButton("B"), NewNavList(navPages()...)
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	w, run := stage(t, &frame{child: Column(b, &navBox{child: n}), size: geom.Sz(400, 600)})
	focused := focusProbe(t, w, run)
	tab(w, run, 0)
	tab(w, run, 0)
	if f := focused(); f != n {
		t.Fatalf("two Tabs put the keyboard on %T, want the list", f)
	}
	key(w, run, input.KeyDown, 0)
	key(w, run, input.KeyDown, 0)
	if got := sent(w); n.cursor != 2 || n.Selected() != 0 || len(got) != 0 {
		t.Fatalf("Down twice put the ring on %d, chose %d and sent %v; want the ring on Activity and nothing chosen",
			n.cursor, n.Selected(), got)
	}
	key(w, run, input.KeyEnter, 0)
	if got := sent(w); n.Selected() != 2 || len(got) != 1 {
		t.Fatalf("Enter chose %d and sent %v, want Activity", n.Selected(), got)
	}
	key(w, run, input.KeyEnd, 0)
	key(w, run, input.KeySpace, 0)
	run(60)
	if n.Selected() != 4 || n.ring.Value() < 0.99 || n.ringY.Value() != n.top(4) {
		t.Fatalf("End and Space chose %d with the ring at %v, %v grown; want Settings, the ring on it",
			n.Selected(), n.ringY.Value(), n.ring.Value())
	}
	key(w, run, input.KeyHome, 0)
	key(w, run, input.KeyUp, 0)
	if n.cursor != 0 {
		t.Fatalf("Home and Up put the ring on %d, want the first row and nothing past it", n.cursor)
	}
}

func TestANavListsFootRowsSitAtTheBottom(t *testing.T) {
	n := NewNavList(navPages()...)
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	w, run := stageNav(t, n)
	run(1)
	if n.size.H != 600 {
		t.Fatalf("with a foot row, the list is %v high, want all of the 600 it is given", n.size.H)
	}
	if got, want := n.top(4), 600-NavRowHeight.Default(); got != want {
		t.Fatalf("Settings starts at %v, want at the bottom, %v", got, want)
	}
	click(w, 100, 590)
	run(1)
	if got := sent(w); n.Selected() != 4 || len(got) != 1 {
		t.Fatalf("a click at the bottom chose %d and sent %v, want Settings", n.Selected(), got)
	}
	// A click in the room between the rows and the foot chooses nothing.
	click(w, 100, 400)
	run(1)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("a click between the rows and the foot sent %v", got)
	}
}

func TestANavListsCountGrowsIn(t *testing.T) {
	items := navPages()
	items[3].Count = 0
	n := NewNavList(items...)
	w, run := stageNav(t, n)
	run(1)
	items = navPages()
	if err := w.Client().Patch("stage", setNavItems{items}); err != nil {
		t.Fatal(err)
	}
	last := float32(0)
	for f := range 40 {
		run(1)
		b := n.badges[3]
		if b == nil {
			break
		}
		if v := b.shown.Value(); v < last-0.01 {
			t.Fatalf("frame %d: the badge shrank from %v to %v as it grew in", f, last, v)
		} else {
			last = v
		}
	}
	if n.badges[3] != nil || last < 0.5 {
		t.Fatalf("the badge never settled, at %v", last)
	}
	items = navPages()
	items[3].Count = 0
	if err := w.Client().Patch("stage", setNavItems{items}); err != nil {
		t.Fatal(err)
	}
	run(2)
	b := n.badges[3]
	if b == nil || b.text != "4" || b.shown.Value() >= 1 || b.shown.Value() <= 0 {
		t.Fatalf("two frames after its count went, the badge is %+v; want it shrinking, still saying 4", b)
	}
}

func TestANavListDrawsInsideItsBox(t *testing.T) {
	items := navPages()
	items[1].Label = "A page with a name far too long for the list to show whole"
	n := NewNavList(items...)
	n.SetSelected(1, nil)
	w, run := stageNav(t, n)
	run(1)
	box := n.size
	ops := painted(n, box)
	if out := spills(ops, box, 3); len(out) > 0 {
		t.Fatalf("the list draws outside its box: %v", out)
	}
	if got := n.tipText(1); got != items[1].Label {
		t.Fatalf("the cut label's tooltip says %q, want the whole label", got)
	}
	if got := n.tipText(0); got != "" {
		t.Fatalf("a label that fits has the tooltip %q", got)
	}
	_ = w
}

func TestANavListReadsAsAListWithTheChosenRowSelected(t *testing.T) {
	n := NewNavList(navPages()...)
	n.SetSelected(2, nil)
	_, run := stageNav(t, n)
	run(1)
	info := n.Access()
	if info.Role != access.RoleList || len(info.Parts) != 5 {
		t.Fatalf("the list reads as %v with %d parts, want a list of 5", info.Role, len(info.Parts))
	}
	if p := info.Parts[2]; p.Name != "Activity" || !p.State.Has(access.StateSelected) {
		t.Fatalf("the third row reads as %+v, want Activity, selected", p)
	}
	if p := info.Parts[3]; p.Description != "4" {
		t.Fatalf("the row with a count reads as %+v, want its count as its description", p)
	}
}

func TestADisabledNavListTakesNoClicks(t *testing.T) {
	n := NewNavList(navPages()...)
	n.Disabled = true
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	w, run := stageNav(t, n)
	run(1)
	click(w, 100, 60)
	run(1)
	if got := sent(w); n.Selected() != 0 || len(got) != 0 {
		t.Fatalf("a click on a disabled list chose %d and sent %v", n.Selected(), got)
	}
}

// manyPages returns n rows.
func manyPages(n int) []NavItem {
	items := make([]NavItem, n)
	for i := range items {
		items[i] = NavItem{Label: fmt.Sprintf("Page %d", i), Icon: icon.Folder, Count: i % 3}
	}
	return items
}

func TestAHugeNavListPaintsOnlyTheRowsInView(t *testing.T) {
	count := func(items []NavItem) int {
		n := NewNavList(items...)
		n.Layout(gunim.Loose(geom.Sz(220, 400)), gunim.Frame{Scale: 1}, gunim.Children{})
		var p paint.Painter
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, 220, 400), Opacity: 1, Clip: true})
		n.Paint(&p, gunim.Frame{Scale: 1}, geom.Sz(220, 400), gunim.Children{})
		end()
		return len(p.Ops())
	}
	if few, many := count(manyPages(100)), count(manyPages(100_000)); few != many {
		t.Fatalf("100 rows paint %d ops and 100 000 paint %d, want the same: only the rows in view", few, many)
	}
}

func BenchmarkNavList100000(b *testing.B) {
	n := NewNavList(manyPages(100_000)...)
	var p paint.Painter
	b.ReportAllocs()
	for range b.N {
		n.Layout(gunim.Loose(geom.Sz(220, 400)), gunim.Frame{Scale: 1}, gunim.Children{})
		p.Reset()
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, 220, 400), Opacity: 1, Clip: true})
		n.Paint(&p, gunim.Frame{Scale: 1}, geom.Sz(220, 400), gunim.Children{})
		end()
	}
}
