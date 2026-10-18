package dock

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/text"
)

// barTabs are the tabs the tests put on a bar on its own.
type barTabs struct {
	tabs  []Tab
	front string
}

// stageBar shows bar b, given tabs, in an offscreen window as wide as
// a title bar, and returns a function that runs frames and one that
// takes the intents sent.
func stageBar(t *testing.T, b *Bar, tabs barTabs) (w *gunim.Window, run func(int), sent func() []gunim.Intent) {
	t.Helper()
	w = gunimtest.New(t, geom.Sz(800, 36), nil)
	gunim.RegisterView(w, "v", func(barTabs) *Bar { return b }, func(b *Bar, s barTabs, u *gunim.UI) { b.SetTabs(s.tabs, s.front, u) })
	gunim.RegisterPatch(w, "v", func(_ *Bar, call uiCall, u *gunim.UI) { call(u) })
	if err := w.Client().Mount(gunim.Root, "v", "v", tabs, "v"); err != nil {
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

func threeTabs() barTabs {
	return barTabs{tabs: []Tab{
		{ID: "1", Face: Face{Title: "Jobs", Closable: true}},
		{ID: "2", Face: Face{Title: "Log", Closable: true}},
		{ID: "3", Face: Face{Title: "Files", Closable: true}},
	}, front: "1"}
}

func barTabAt(t *testing.T, b *Bar, id string) geom.Point {
	t.Helper()
	r, ok := b.TabRect(id)
	if !ok {
		t.Fatalf("no tab %s", id)
	}
	return r.Center()
}

// TestABarOnItsOwnSaysWhatTheUserAsks: a click shows a tab at once, the
// middle button closes one, the + asks for a new one and its second
// button opens the application's menu.
func TestABarOnItsOwnSaysWhatTheUserAsks(t *testing.T) {
	b := NewBar("g", 1)
	b.Plus, b.Plain = true, true
	menu := geom.Rect{}
	b.OnPlusMenu = func(at geom.Rect, _ *gunim.UI) gunim.Intent { menu = at; return nil }
	w, run, sent := stageBar(t, b, threeTabs())
	click(w, run, barTabAt(t, b, "2"), input.ButtonPrimary)
	if got := sent(); len(got) != 1 || got[0] != (Show{ID: "2"}) || b.Front() != "2" {
		t.Fatalf("a click sent %v, %s in front", got, b.Front())
	}
	click(w, run, barTabAt(t, b, "3"), input.ButtonMiddle)
	if got := sent(); len(got) != 1 || got[0] != (Close{ID: "3"}) {
		t.Fatalf("a middle click sent %v", got)
	}
	plus := b.PlusRect().Center()
	click(w, run, plus, input.ButtonPrimary)
	if got := sent(); len(got) != 1 || got[0] != (New{Group: "g", Win: 1}) {
		t.Fatalf("the + sent %v", got)
	}
	click(w, run, plus, input.ButtonSecondary)
	if menu != b.PlusRect() {
		t.Fatalf("the +'s second button opened the menu at %v", menu)
	}
}

// TestABarOnItsOwnTakesDrops: a tab dropped lands as a Move onto its
// group, at the place under the pointer; another drag it Takes goes to
// OnDrop, and one it does not passes by.
func TestABarOnItsOwnTakesDrops(t *testing.T) {
	b := NewBar("g", 1)
	type pane struct{ id string }
	var got Dropped
	b.Takes = func(data any) bool { _, ok := data.(pane); return ok }
	b.OnDrop = func(d Dropped, _ *gunim.UI) gunim.Intent { got = d; return nil }
	w, run, sent := stageBar(t, b, threeTabs())
	first, _ := b.TabRect("1")
	at := geom.Pt(first.Min.X+4, first.Center().Y)
	w.Input(input.Drop{Pos: at, Data: Drag{ID: "9", Win: 2}})
	run(1)
	if in := sent(); len(in) != 1 || in[0] != (Move{ID: "9", To: Target{At: "g", Win: 1, Index: 0}}) {
		t.Fatalf("a tab dropped first sent %v", in)
	}
	w.Input(input.Drop{Pos: at, Data: pane{"p7"}})
	run(1)
	if got.Data != (pane{"p7"}) || got.Index != 0 {
		t.Fatalf("a pane dropped first reached OnDrop as %+v", got)
	}
	w.Input(input.Drop{Pos: at, Data: "something else"})
	run(1)
	if in := sent(); len(in) != 0 {
		t.Fatalf("what the bar does not take sent %v", in)
	}
}

// TestABarOnItsOwnSaysATabLeft: a tab let go outside every window asks
// for one as large as DetachSize, and the bar says it was dropped until
// its tabs change.
func TestABarOnItsOwnSaysATabLeft(t *testing.T) {
	b := NewBar("g", 1)
	b.DetachSize = geom.Sz(900, 600)
	w, run, sent := stageBar(t, b, threeTabs())
	at := barTabAt(t, b, "2")
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(20, 30))})
	run(1)
	if b.Carried() != "2" {
		t.Fatalf("the bar carries %q", b.Carried())
	}
	onUIBar(t, w, run, func(u *gunim.UI) { b.Handle(input.DragEnd{Out: true, At: geom.Pt(1200, 300)}, u) })
	var d Detach
	for _, in := range sent() {
		if dt, ok := in.(Detach); ok {
			d = dt
		}
	}
	if d.ID != "2" || d.From != 1 || d.Size != b.DetachSize || b.Carried() != "" || b.Dropped() != "2" {
		t.Fatalf("let go outside, the bar sent %+v, carries %q, dropped %q", d, b.Carried(), b.Dropped())
	}
	// The same tabs again, as an unrelated update brings them, keep it.
	if err := w.Client().Update("v", threeTabs()); err != nil {
		t.Fatal(err)
	}
	run(1)
	if b.Dropped() != "2" {
		t.Fatal("the same tabs again forgot the tab dropped")
	}
	st := threeTabs()
	st.tabs = append(st.tabs[:1], st.tabs[2])
	if err := w.Client().Update("v", st); err != nil {
		t.Fatal(err)
	}
	run(1)
	if b.Dropped() != "" {
		t.Fatal("the tabs changed, and the bar still says a tab was dropped")
	}
}

// onUIBar runs fn on the UI goroutine of a window stageBar made, and a
// frame.
func onUIBar(t *testing.T, w *gunim.Window, run func(int), fn func(u *gunim.UI)) {
	t.Helper()
	if err := w.Client().Patch("v", uiCall(fn)); err != nil {
		t.Fatal(err)
	}
	run(1)
}

// TestABarsTabsOpenAndCloseInMotion: a tab opened grows in where it
// lands, and one closed shrinks away while the tabs after it slide into
// its place. The bar's first tabs are simply there.
func TestABarsTabsOpenAndCloseInMotion(t *testing.T) {
	b := NewBar("g", 1)
	two := threeTabs()
	two.tabs = two.tabs[:2]
	w, run, _ := stageBar(t, b, two)
	if r, open := b.drawnAt(1); r != b.boxes[1] || open != 1 {
		t.Fatalf("a first tab is drawn at %v, %v open, not where it is", r, open)
	}
	update := func(s barTabs) {
		if err := w.Client().Update("v", s); err != nil {
			t.Fatal(err)
		}
	}
	update(threeTabs())
	run(2)
	r, open := b.drawnAt(2)
	if r.Size().W >= b.boxes[2].Size().W || open >= 1 || r.Min.X != b.boxes[2].Min.X {
		t.Fatalf("an opened tab starts at %v, %v open, want narrower than %v", r, open, b.boxes[2])
	}
	run(60)
	if r, open := b.drawnAt(2); r != b.boxes[2] || open != 1 {
		t.Fatalf("settled, the opened tab is at %v, %v open, want %v", r, open, b.boxes[2])
	}
	// The middle tab closes: it shrinks where it was, and the last
	// slides left into its place.
	was := b.boxes[2]
	st := threeTabs()
	st.tabs = append(st.tabs[:1], st.tabs[2])
	update(st)
	run(2)
	if len(b.gone) != 1 || b.gone[0].tab.id != "2" {
		t.Fatalf("closing, the bar draws %d gone tabs", len(b.gone))
	}
	if r, _ := b.drawnAt(1); r.Min.X <= b.boxes[1].Min.X || r.Min.X >= was.Min.X {
		t.Fatalf("the last tab is at %v, want between %v and %v", r, was, b.boxes[1])
	}
	run(60)
	if len(b.gone) != 0 {
		t.Fatalf("settled, %d closed tabs are still drawn", len(b.gone))
	}
	if r, _ := b.drawnAt(1); r != b.boxes[1] {
		t.Fatalf("settled, the last tab is at %v, want %v", r, b.boxes[1])
	}
}

// TestATitleThatDoesNotFitGivesWayToItsShortOne: as a server's folder
// without the server. Either way the tab says it cut the title, for the
// tooltip to show it whole.
func TestATitleThatDoesNotFitGivesWayToItsShortOne(t *testing.T) {
	face := text.Default()
	full, short := face.Shape("Skylake: build", 13), face.Shape("build", 13)
	b := &Bar{titles: []text.Run{full}, shorts: []text.Run{short}}
	if got, cut := b.fitTitle(0, 500); got.Advance != full.Advance || cut {
		t.Errorf("with room the tab says %v wide, cut %v, want all of its title", got.Advance, cut)
	}
	if got, cut := b.fitTitle(0, full.Advance-2); got.Advance != short.Advance || !cut {
		t.Errorf("short of room the tab says %v wide, cut %v, want the short %v, cut", got.Advance, cut, short.Advance)
	}
	b.shorts[0] = face.Shape("", 13)
	if got, cut := b.fitTitle(0, 30); got.Advance != full.Advance || !cut {
		t.Errorf("a title with nothing shorter says %v wide, cut %v, want all of it, faded, and cut", got.Advance, cut)
	}
}

// TestATabDroppedWhereItStandsMovesNothing: on a bar on its own too.
func TestATabDroppedWhereItStandsMovesNothing(t *testing.T) {
	b := NewBar("g", 1)
	w, run, sent := stageBar(t, b, threeTabs())
	at := barTabAt(t, b, "2")
	w.Input(input.Drop{Pos: at, Data: Drag{ID: "2", Win: 1}})
	run(1)
	for _, in := range sent() {
		if _, ok := in.(Move); ok {
			t.Fatalf("a tab dropped on itself sent %v", in)
		}
	}
	if b.Front() != "2" {
		t.Errorf("dropped on itself, %s is in front, want 2", b.Front())
	}
}

// TestASecondButtonOnThePlusEndsATabsPress: and opens no menu.
func TestASecondButtonOnThePlusEndsATabsPress(t *testing.T) {
	b := NewBar("g", 1)
	b.Plus = true
	opened := false
	b.OnPlusMenu = func(geom.Rect, *gunim.UI) gunim.Intent { opened = true; return nil }
	w, run, _ := stageBar(t, b, threeTabs())
	at := barTabAt(t, b, "2")
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerDown{Pos: b.PlusRect().Center(), Button: input.ButtonSecondary, Clicks: 1})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(30, 10))})
	run(1)
	if b.Carried() != "" || opened {
		t.Errorf("after a second button, the tab is carried %q, the menu opened %v", b.Carried(), opened)
	}
}

// TestATitleBarsBarDrawsOnTheTitleBarsFill: whatever the dock's root.
func TestATitleBarsBarDrawsOnTheTitleBarsFill(t *testing.T) {
	d := NewDock(0)
	d.TitleBar, d.Plus = true, true
	top := d.TopBar()
	d.SetLayout(Layout{Windows: []Window{{Root: &Node{ID: "a", Kind: Item}, Keep: true}}}, nil)
	if !top.Plain || !top.Plus {
		t.Errorf("the title bar's bar has Plain %v and Plus %v", top.Plain, top.Plus)
	}
}
