package dock

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// pane is a view the tests dock: it fills its place, and counts its
// frames.
type pane struct{ painted int }

func (p *pane) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size { return c.Max }
func (p *pane) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children)          { p.painted++ }

// stage shows d in an offscreen window, d's layout l, and returns a
// function that runs frames and one that takes the intents sent.
func stage(t *testing.T, d *Dock, l Layout) (w *gunim.Window, run func(int), sent func() []gunim.Intent) {
	t.Helper()
	w = gunimtest.New(t, geom.Sz(800, 500), nil)
	gunim.RegisterView(w, "v", func(Layout) *Dock { return d }, func(d *Dock, l Layout, u *gunim.UI) { d.SetLayout(l, u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", l, "v"); err != nil {
		t.Fatal(err)
	}
	run = func(k int) {
		for range k {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	sent = func() []gunim.Intent {
		var out []gunim.Intent
		for {
			select {
			case e := <-w.Client().Intents():
				out = append(out, e.Intent)
			default:
				return out
			}
		}
	}
	return w, run, sent
}

// hosting returns a dock holding a pane for each item.
func hosting() (d *Dock, panes map[string]*pane) {
	panes = map[string]*pane{}
	d = NewDock(0)
	d.Content = func(item string) gunim.Node {
		p := &pane{}
		panes[item] = p
		return p
	}
	return d, panes
}

// tabAt returns the middle of tab id on the bar of group g, in the
// window.
func tabAt(t *testing.T, d *Dock, id string) geom.Point {
	t.Helper()
	r, ok := d.TabRect(id)
	if !ok {
		t.Fatalf("no tab %s", id)
	}
	return r.Center()
}

// groupArea returns where group g's tab in front shows.
func groupArea(t *testing.T, d *Dock, g string) geom.Rect {
	t.Helper()
	for _, pg := range d.groups {
		if pg.id == g {
			return pg.area
		}
	}
	t.Fatalf("no group %s", g)
	return geom.Rect{}
}

// drag presses at from, drags to the point to in steps, and lets go.
func drag(w *gunim.Window, run func(int), from, to geom.Point) {
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		w.Input(input.PointerMove{Pos: geom.Pt(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f)})
		run(1)
	}
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(2)
}

func click(w *gunim.Window, run func(int), at geom.Point, b input.Button) {
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: b, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: b})
	run(1)
}

// TestAPickedTabShowsAtOnceAndFadesIn: before the application answers,
// the tab is in front, its view laid out, and its fade climbs on every
// frame from where it starts, never jumping back.
func TestAPickedTabShowsAtOnceAndFadesIn(t *testing.T) {
	d, panes := hosting()
	l := NewLayout(true, "a", "b", "c")
	w, run, sent := stage(t, d, l)
	if panes["a"] == nil || panes["b"] != nil {
		t.Fatal("the dock built a view behind its tab, or not the one in front")
	}
	click(w, run, tabAt(t, d, "c"), input.ButtonPrimary)
	if got := sent(); len(got) != 1 || got[0] != (Show{ID: "c"}) {
		t.Fatalf("sent %v, want Show c", got)
	}
	if !slices.Equal(d.Shown(), []string{"c"}) {
		t.Fatalf("shows %v", d.Shown())
	}
	if d.showing["c"].from != 1 {
		t.Errorf("c comes from %v, want 1, the right, where its tab stands", d.showing["c"].from)
	}
	last := float32(-1)
	for range 40 {
		v := d.showing["c"].on.Value()
		if v < last {
			t.Fatalf("the fade went back, from %v to %v", last, v)
		}
		last = v
		run(1)
	}
	if last < 0.99 || panes["c"].painted == 0 {
		t.Errorf("the view came to %v, painted %d times", last, panes["c"].painted)
	}
	if _, ok := d.Rect("a"); ok {
		t.Error("the tab behind still has a place")
	}
}

// TestATabDraggedToAPanesEdgeAsksToSplitIt: and the half it would take
// lights while it is held there.
func TestATabDraggedToAPanesEdgeAsksToSplitIt(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b", "c")
	l.Move("c", Target{At: "a", Side: Right})
	l.Show("a")
	w, run, sent := stage(t, d, l)
	right := l.GroupOf("c").ID
	area := groupArea(t, d, right)
	to := geom.Pt(area.Center().X, area.Max.Y-10)
	from := tabAt(t, d, "b")
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		w.Input(input.PointerMove{Pos: geom.Pt(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f)})
		run(1)
	}
	run(20)
	lit := d.drop.Value()
	if d.dropLit.Value() < 0.9 || lit.Min.Y < area.Center().Y-1 || lit.Size().W < area.Size().W-1 {
		t.Errorf("lit %v at %v; want the lower half of %v", lit, d.dropLit.Value(), area)
	}
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(2)
	want := Move{ID: "b", To: Target{At: right, Side: Bottom}}
	if got := sent(); !slices.Contains(got, gunim.Intent(want)) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

// TestATabDraggedAlongItsBarMovesThere: and one dropped where it was
// moves nowhere.
func TestATabDraggedAlongItsBarMovesThere(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b", "c")
	w, run, sent := stage(t, d, l)
	g := l.Windows[0].Root.ID
	end := tabAt(t, d, "c").Add(geom.Pt(40, 0))
	drag(w, run, tabAt(t, d, "a"), end)
	want := Move{ID: "a", To: Target{At: g, Index: 3}}
	if got := sent(); !slices.Contains(got, gunim.Intent(want)) {
		t.Errorf("sent %v, want %v", got, want)
	}
	drag(w, run, tabAt(t, d, "b"), tabAt(t, d, "b").Add(geom.Pt(12, 0)))
	for _, in := range sent() {
		if _, ok := in.(Move); ok {
			t.Errorf("a tab dropped where it was moved: %v", in)
		}
	}
}

// TestATabLetGoOutsideAsksForAWindow: at the place let go, as large as
// its group.
func TestATabLetGoOutsideAsksForAWindow(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b")
	w, run, sent := stage(t, d, l)
	from := tabAt(t, d, "b")
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	w.Input(input.PointerMove{Pos: from.Add(geom.Pt(30, 30))})
	run(1)
	w.Input(input.PointerMove{Pos: geom.Pt(1200, 900)})
	w.Input(input.PointerUp{Pos: geom.Pt(1200, 900), Button: input.ButtonPrimary})
	run(2)
	var got *Detach
	for _, in := range sent() {
		if dt, ok := in.(Detach); ok {
			got = &dt
		}
	}
	if got == nil {
		t.Fatal("no Detach sent")
	}
	if got.ID != "b" || got.Size != d.groups[0].whole.Size() {
		t.Errorf("sent %+v", *got)
	}
}

// TestADividerFollowsThePointerAndKeepsWhereItWasLetGo: every frame of
// the drag, and a double click evens it out.
func TestADividerFollowsThePointerAndKeepsWhereItWasLetGo(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Right})
	w, run, sent := stage(t, d, l)
	split := l.Windows[0].Root.ID
	dv := d.dividers[0].rect.Center()
	w.Input(input.PointerMove{Pos: dv})
	w.Input(input.PointerDown{Pos: dv, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for x := dv.X; x >= 200; x -= 25 {
		w.Input(input.PointerMove{Pos: geom.Pt(x, dv.Y)})
		run(1)
		if c := d.dividers[0].rect.Center().X; math.Abs(float64(c-x)) > 0.5 {
			t.Fatalf("the divider is at %v with the pointer at %v", c, x)
		}
	}
	w.Input(input.PointerUp{Pos: geom.Pt(200, dv.Y), Button: input.ButtonPrimary})
	run(1)
	got := sent()
	if len(got) != 1 {
		t.Fatalf("sent %v", got)
	}
	r, ok := got[0].(Resize)
	if !ok || r.ID != split || math.Abs(float64(r.Share-196.0/792)) > 0.01 {
		t.Errorf("sent %+v", got[0])
	}
	run(30)
	if c := d.dividers[0].rect.Center().X; math.Abs(float64(c-200)) > 0.5 {
		t.Errorf("let go, the divider went to %v", c)
	}
	at := d.dividers[0].rect.Center()
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if got := sent(); len(got) != 1 || got[0] != (Resize{ID: split, Share: 0.5}) {
		t.Errorf("a double click sent %v", got)
	}
}

// TestTheMiddleButtonAndTheCrossClose: a closable tab, and only one.
func TestTheMiddleButtonAndTheCrossClose(t *testing.T) {
	d, _ := hosting()
	d.Face = func(n *Node) Face { return Face{Title: n.ID, Closable: n.ID != "a"} }
	w, run, sent := stage(t, d, NewLayout(true, "a", "b"))
	click(w, run, tabAt(t, d, "a"), input.ButtonMiddle)
	click(w, run, tabAt(t, d, "b"), input.ButtonMiddle)
	if got := sent(); len(got) != 1 || got[0] != (Close{ID: "b"}) {
		t.Errorf("sent %v, want Close b alone", got)
	}
	click(w, run, tabAt(t, d, "b"), input.ButtonPrimary)
	sent()
	b := d.groups[0]
	i := slices.Index(b.bar.Tabs(), "b")
	click(w, run, b.top.Min.Add(crossIn(b.bar.boxes[i], TabPadding.Default()).Center()), input.ButtonPrimary)
	if got := sent(); len(got) != 1 || got[0] != (Close{ID: "b"}) {
		t.Errorf("the × sent %v", got)
	}
}

// TestTheKeysStepThroughTheTabsAndThePanes: Ctrl+Page Down goes round
// the tabs, and Ctrl+Tab the panes, marking the pane it goes to.
func TestTheKeysStepThroughTheTabsAndThePanes(t *testing.T) {
	d, _ := hosting()
	d.Keys = true
	l := NewLayout(true, "a", "b", "c")
	l.Move("c", Target{At: "a", Side: Bottom})
	l.Show("a")
	w, run, sent := stage(t, d, l)
	press := func(k input.Key, m input.Mods) {
		w.Input(input.KeyPress{Key: k, Mods: m})
		run(1)
	}
	press(input.KeyPageDown, input.ModControl)
	if got := sent(); len(got) != 1 || got[0] != (Show{ID: "b"}) {
		t.Fatalf("Ctrl+Page Down sent %v", got)
	}
	press(input.KeyPageDown, input.ModControl)
	if got := sent(); len(got) != 1 || got[0] != (Show{ID: "a"}) {
		t.Fatalf("Ctrl+Page Down round sent %v", got)
	}
	press(input.KeyTab, input.ModControl)
	if d.Active() != "c" {
		t.Errorf("Ctrl+Tab went to %s", d.Active())
	}
	if !d.groups[1].bar.mark.Active() && d.groups[1].bar.mark.Value() < 0.5 {
		t.Error("the pane the keys went to has no mark coming")
	}
	press(input.KeyTab, input.ModControl|input.ModShift)
	if d.Active() != "a" {
		t.Errorf("Ctrl+Shift+Tab went to %s", d.Active())
	}
}

// TestANewSplitSlidesOpen: the side new in the window grows from
// nothing, on every frame, to its share.
func TestANewSplitSlidesOpen(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a")
	w, run, _ := stage(t, d, l)
	l.Add("b", Target{At: "a", Side: Right})
	if err := w.Client().Update("v", l); err != nil {
		t.Fatal(err)
	}
	run(1)
	last := float32(math.Inf(1))
	for range 40 {
		x := d.dividers[0].rect.Min.X
		if x > last+0.01 {
			t.Fatalf("the divider went back, from %v to %v", last, x)
		}
		last = x
		run(1)
	}
	if math.Abs(float64(last-396)) > 1 {
		t.Errorf("the divider came to %v, want 396", last)
	}
}

// TestASideClosedSlidesAway: where a split's side closes, the side
// kept grows over its room on every frame, from where it stood to the
// whole window, and a side that moves to the other window goes alike.
func TestASideClosedSlidesAway(t *testing.T) {
	for _, way := range []string{"close", "detach"} {
		d, _ := hosting()
		l := NewLayout(true, "a")
		l.Add("b", Target{At: "a", Side: Right})
		w, run, _ := stage(t, d, l)
		run(40)
		g := d.l.GroupOf("a").ID
		if r := groupArea(t, d, g); math.Abs(float64(r.Max.X-396)) > 1 {
			t.Fatalf("a stands in %v before, want it to end at 396", r)
		}
		if way == "close" {
			l.Remove("b")
		} else {
			l.ToWindow("b", nil)
		}
		if err := w.Client().Update("v", l); err != nil {
			t.Fatal(err)
		}
		run(1)
		first := groupArea(t, d, g).Max.X
		if first > 700 {
			t.Fatalf("%s: a jumped to %v at once", way, first)
		}
		last := first
		for range 40 {
			x := groupArea(t, d, g).Max.X
			if x < last-0.01 {
				t.Fatalf("%s: a shrank back, from %v to %v", way, last, x)
			}
			last = x
			run(1)
		}
		if last != 800 {
			t.Errorf("%s: a came to end at %v, want 800", way, last)
		}
		if len(d.closing) != 0 {
			t.Errorf("%s: still closing %v once still", way, d.closing)
		}
	}
}

// TestASideClosingJumpsWithANilUIOrANewSplit: the side kept stops
// growing where a layout comes without a UI, which jumps it to the
// whole room, or where a new split takes it in, which slides the new
// side in alone, the side kept only ever shrinking.
func TestASideClosingJumpsWithANilUIOrANewSplit(t *testing.T) {
	for _, way := range []string{"nil", "split"} {
		d, _ := hosting()
		l := NewLayout(true, "a")
		l.Add("b", Target{At: "a", Side: Right})
		w, run, _ := stage(t, d, l)
		run(40)
		g := d.l.GroupOf("a").ID
		l.Remove("b")
		if err := w.Client().Update("v", l); err != nil {
			t.Fatal(err)
		}
		run(2)
		if len(d.closing) != 1 {
			t.Fatalf("%s: closing %v, want a's group", way, d.closing)
		}
		if way == "nil" {
			d.SetLayout(l, nil)
			if len(d.closing) != 0 {
				t.Errorf("nil: still closing %v", d.closing)
			}
			run(1)
			if x := groupArea(t, d, g).Max.X; x != 800 {
				t.Errorf("nil: a ends at %v, want 800 at once", x)
			}
			continue
		}
		l.Add("c", Target{At: g, Side: Right})
		if err := w.Client().Update("v", l); err != nil {
			t.Fatal(err)
		}
		run(1)
		if len(d.closing) != 0 {
			t.Errorf("split: still closing %v", d.closing)
		}
		last := groupArea(t, d, g).Max.X
		for range 40 {
			run(1)
			x := groupArea(t, d, g).Max.X
			if x > last+0.01 {
				t.Fatalf("split: a grew back, from %v to %v", last, x)
			}
			last = x
		}
		if math.Abs(float64(last-396)) > 1 {
			t.Errorf("split: a came to end at %v, want 396", last)
		}
	}
}

// TestDrawingTheBarsAloneLeavesTheViewsTheirPointer: without Content
// the dock takes the pointer on its dividers alone, and says where the
// application draws each view.
func TestDrawingTheBarsAloneLeavesTheViewsTheirPointer(t *testing.T) {
	d := NewDock(0)
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Bottom})
	stage(t, d, l)
	ra, okA := d.Rect("a")
	rb, okB := d.Rect("b")
	if !okA || !okB || ra.Max.Y >= rb.Min.Y {
		t.Fatalf("a at %v, b at %v", ra, rb)
	}
	if d.Covers(ra.Center()) {
		t.Error("the dock took the pointer over a view")
	}
	if !d.Covers(d.dividers[0].rect.Center()) {
		t.Error("the dock left its divider")
	}
}

// TestTheRootBarGoesToTheTitleBar: with TitleBar the root group's tabs
// are on TopBar, and the dock lays out no bar of its own for them.
func TestTheRootBarGoesToTheTitleBar(t *testing.T) {
	d, _ := hosting()
	d.TitleBar = true
	l := NewLayout(false, "p1", "p2", "p3")
	l.Move("p2", Target{At: "p1", Side: Right})
	l.Show("p1")
	top := d.TopBar()
	stage(t, d, l)
	split := l.Windows[0].Root.Kids[0].ID
	if !slices.Equal(top.Tabs(), []string{split, "p3"}) || top.Front() != split {
		t.Errorf("the top bar has %v, %s in front", top.Tabs(), top.Front())
	}
	for _, g := range d.groups {
		if g.bar != nil {
			t.Errorf("the dock laid out a bar for %s", g.id)
		}
	}
	if r, _ := d.Rect("p1"); r.Min.Y != 0 {
		t.Errorf("the panes start at %v, not the top", r.Min.Y)
	}
}

// TestSettersTakeANilUI: before the dock is mounted.
func TestSettersTakeANilUI(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Right})
	d.SetLayout(l, nil)
	d.SetActive("b", nil)
	d.Refresh(nil)
	if d.Active() != "b" || len(d.Children()) != 4 {
		t.Errorf("active %s, %d children, want b and 4: two views and their bars", d.Active(), len(d.Children()))
	}
}

// TestTheBarsTakeThePointerWithoutContent: drawing the views itself,
// the application still has the dock's bars take clicks.
func TestTheBarsTakeThePointerWithoutContent(t *testing.T) {
	d := NewDock(0)
	w, run, sent := stage(t, d, NewLayout(true, "a", "b"))
	click(w, run, tabAt(t, d, "b"), input.ButtonPrimary)
	if got := sent(); len(got) != 1 || got[0] != (Show{ID: "b"}) {
		t.Errorf("a click on a tab sent %v", got)
	}
}

// TestASecondButtonPutsTheDividerBack: where it was before the drag,
// sending nothing.
func TestASecondButtonPutsTheDividerBack(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Right})
	w, run, sent := stage(t, d, l)
	dv := d.dividers[0].rect.Center()
	w.Input(input.PointerMove{Pos: dv})
	w.Input(input.PointerDown{Pos: dv, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: geom.Pt(600, dv.Y)})
	run(1)
	w.Input(input.PointerDown{Pos: geom.Pt(600, dv.Y), Button: input.ButtonSecondary, Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(600, dv.Y), Button: input.ButtonPrimary})
	run(2)
	if c := d.dividers[0].rect.Center().X; math.Abs(float64(c-dv.X)) > 0.5 {
		t.Errorf("the divider stayed at %v, want %v", c, dv.X)
	}
	if got := sent(); len(got) != 0 {
		t.Errorf("sent %v", got)
	}
}

// TestATabDroppedOnANestedGroupJoinsIt: the innermost group under the
// pointer takes it, not the one round it.
func TestATabDroppedOnANestedGroupJoinsIt(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(false, "p1", "p2", "p4")
	l.Move("p2", Target{At: "p1", Side: Right})
	l.Add("p3", Target{At: "p2", Index: -1})
	l.Show("p1")
	l.Show("p2")
	_, _, _ = stage(t, d, l)
	inner := l.GroupOf("p2").ID
	r, _ := d.Rect("p2")
	to, _, split, ok := d.dropAt(r.Center(), Drag{ID: "p4"}, nil)
	if !ok || split || to.At != inner {
		t.Errorf("dropped at %+v, split %v, ok %v; want into %s", to, split, ok, inner)
	}
	// A tab holding the group cannot land on the group's bar.
	tab := l.Parent("p1").ID
	if b := d.bars[inner]; b == nil || b.lands(Drag{ID: tab}, 0) {
		t.Error("the bar took the tab it is in")
	}
}

// TestAStaleDividerOrABrokenLayoutCrashesNothing: a press on a divider
// the layout let go, and a split of one node.
func TestAStaleDividerOrABrokenLayoutCrashesNothing(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Right})
	w, run, _ := stage(t, d, l)
	dv := d.dividers[0].rect.Center()
	one := NewLayout(true, "a")
	one.Windows[0].Root = &Node{ID: "#9", Kind: Row, Kids: []*Node{{ID: "a", Kind: Item}}}
	if err := w.Client().Update("v", one); err != nil {
		t.Fatal(err)
	}
	w.Input(input.PointerDown{Pos: dv, Button: input.ButtonPrimary, Clicks: 1})
	run(2)
	if !slices.Equal(d.Shown(), []string{"a"}) {
		t.Errorf("shows %v", d.Shown())
	}
}

// TestTheDockSaysWhereItsTabsAndDividersStand: where a click lands on
// them.
func TestTheDockSaysWhereItsTabsAndDividersStand(t *testing.T) {
	d, _ := hosting()
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Right})
	w, run, sent := stage(t, d, l)
	l.Add("c", Target{At: "b", Index: -1})
	l.Show("b")
	if err := w.Client().Update("v", l); err != nil {
		t.Fatal(err)
	}
	run(30)
	r, ok := d.TabRect("c")
	if !ok || r.Size().W <= 0 {
		t.Fatalf("tab c at %v, %v", r, ok)
	}
	click(w, run, r.Center(), input.ButtonPrimary)
	if got := sent(); len(got) != 1 || got[0] != (Show{ID: "c"}) {
		t.Errorf("a click in tab c's rect sent %v", got)
	}
	split := l.Windows[0].Root.ID
	dv, ok := d.DividerRect(split)
	if !ok {
		t.Fatalf("the divider at %v, %v", dv, ok)
	}
	if _, ok := d.TabRect("nothing"); ok {
		t.Error("a tab of nothing stands somewhere")
	}
	at := dv.Center()
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if got := sent(); len(got) != 1 {
		t.Errorf("a double click on the divider sent %v", got)
	}
}

// TestMarkAloneMarksALonePane: without it a window of one pane marks no
// tab; with it, the tab in front of that pane.
func TestMarkAloneMarksALonePane(t *testing.T) {
	for _, alone := range []bool{false, true} {
		d, _ := hosting()
		d.MarkAlone = alone
		stage(t, d, NewLayout(true, "a", "b"))
		if on := d.groups[0].bar.mark.Value() > 0.5; on != alone {
			t.Errorf("with MarkAlone %v, the tab is marked %v", alone, on)
		}
	}
}
