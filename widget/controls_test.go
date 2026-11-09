package widget

import (
	"fmt"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// A field that shares a name with a method of anim.Group hides it, and
// the engine then never steps the widget. These fail to compile if one
// does.
var (
	_ gunim.Animator = (*Button)(nil)
	_ gunim.Animator = (*Checkbox)(nil)
	_ gunim.Animator = (*Switch)(nil)
	_ gunim.Animator = (*Slider)(nil)
	_ gunim.Animator = (*Tabs)(nil)
	_ gunim.Animator = (*Tree)(nil)
	_ gunim.Animator = (*treeRow)(nil)
	_ gunim.Animator = (*Menu)(nil)
	_ gunim.Animator = (*Dropdown)(nil)
	_ gunim.Animator = (*Image)(nil)
	_ gunim.Animator = (*Scroll)(nil)
	_ gunim.Animator = (*VirtualList)(nil)
	_ gunim.Animator = (*Hero)(nil)
	_ gunim.Animator = (*CodeEditor)(nil)
)

type (
	flipped struct{ On bool }
	slid    struct{ V float32 }
	tabbed  struct{ I int }
)

// sent drains the window's intents.
func sent(w *gunim.Window) []gunim.Intent {
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

func click(w *gunim.Window, x, y float32) {
	w.Input(input.PointerDown{Pos: geom.Pt(x, y), Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(x, y), Time: time.Now()})
}

func TestCheckboxFlipsOnClickAndSpace(t *testing.T) {
	c := NewCheckbox("Tick")
	c.OnChange = func(on bool, u *gunim.UI) gunim.Intent { return flipped{on} }
	w, run := stage(t, &frame{child: c, size: geom.Sz(200, 28)})
	click(w, 10, 14)
	run(1)
	if !c.Checked() {
		t.Fatal("a click left the checkbox off")
	}
	w.Input(input.KeyPress{Key: input.KeySpace})
	run(60)
	if c.Checked() || c.lit.Value() != 0 {
		t.Fatalf("Space left the checkbox on at %v", c.lit.Value())
	}
	if got := sent(w); len(got) != 2 || got[0] != (flipped{true}) || got[1] != (flipped{false}) {
		t.Fatalf("intents %v, want on then off", got)
	}
}

func TestSwitchStartsWhereOnSays(t *testing.T) {
	s := NewSwitch("Power")
	s.SetChecked(true, nil)
	_, run := stage(t, &frame{child: s, size: geom.Sz(200, 28)})
	run(1)
	if v := s.lit.Value(); v != 1 {
		t.Fatalf("a switch made on starts at %v, want 1", v)
	}
}

func TestSliderFollowsPointerAndKeys(t *testing.T) {
	s := NewSlider(0, 100)
	s.Snap = 1
	s.OnChange = func(v float32, u *gunim.UI) gunim.Intent { return slid{v} }
	w, run := stage(t, &frame{child: s, size: geom.Sz(218, 28)})
	// The track runs from 9 to 209, inside the knob's half at each end.
	w.Input(input.PointerDown{Pos: geom.Pt(109, 14), Clicks: 1})
	run(1)
	if s.Value() != 50 {
		t.Fatalf("a press mid-track set %v, want 50", s.Value())
	}
	w.Input(input.PointerMove{Pos: geom.Pt(159, 14)})
	w.Input(input.PointerUp{Pos: geom.Pt(159, 14)})
	run(60)
	if s.Value() != 75 || s.at.Value() != 0.75 {
		t.Fatalf("a drag to three quarters left %v with the knob at %v", s.Value(), s.at.Value())
	}
	for _, k := range []input.Key{input.KeyRight, input.KeyRight, input.KeyEnd, input.KeyPageDown} {
		w.Input(input.KeyPress{Key: k})
	}
	run(1)
	if s.Value() != 90 {
		t.Fatalf("keys left %v, want 90", s.Value())
	}
	got := sent(w)
	if last := got[len(got)-1]; last != (slid{90}) {
		t.Fatalf("last intent %v, want slid{90}", last)
	}
}

func TestTabsShowOnePageAtATime(t *testing.T) {
	a, b := &recorder{}, &recorder{}
	tabs := NewTabs([]string{"One", "Two"}, a, b)
	tabs.OnChange = func(i int, u *gunim.UI) gunim.Intent { return tabbed{i} }
	w, run := stage(t, &frame{child: tabs, size: geom.Sz(300, 200)})
	click(w, 150, 100)
	run(1)
	if len(a.events) == 0 || len(b.events) != 0 {
		t.Fatal("a click on the page reached the wrong one")
	}
	// The second title starts after the first, "One" and its padding.
	click(w, tabs.spans[1][0]+5, 10)
	run(60)
	if tabs.Selected() != 1 {
		t.Fatalf("a click on the second title left tab %d", tabs.Selected())
	}
	a.events, b.events = nil, nil
	click(w, 150, 100)
	run(1)
	if len(b.events) == 0 || len(a.events) != 0 {
		t.Fatal("after the switch, a click on the page reached the old one")
	}
	// The click on the page took focus from the titles; a click on the
	// chosen title gives it back.
	click(w, tabs.spans[1][0]+5, 10)
	w.Input(input.KeyPress{Key: input.KeyLeft})
	run(1)
	if tabs.Selected() != 0 {
		t.Fatal("Left on the focused titles did not go back a tab")
	}
	if got := sent(w); len(got) != 2 || got[0] != (tabbed{1}) || got[1] != (tabbed{0}) {
		t.Fatalf("intents %v, want tab 1 then 0", got)
	}
}

// layoutCounter is a page that counts its layouts.
type layoutCounter struct{ laid int }

func (l *layoutCounter) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	l.laid++
	return c.Max
}

func (l *layoutCounter) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

func TestTabsLayOutOnlyThePagesShowing(t *testing.T) {
	pages := []*layoutCounter{{}, {}, {}}
	tabs := NewTabs([]string{"One", "Two", "Three"}, pages[0], pages[1], pages[2])
	w, run := stage(t, &frame{child: tabs, size: geom.Sz(300, 200)})
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c chooseTab, u *gunim.UI) { tabs.SetSelected(c.I, u) })
	run(10)
	if pages[1].laid != 0 || pages[2].laid != 0 {
		t.Fatalf("with the first page showing, the others were laid out %d and %d times", pages[1].laid, pages[2].laid)
	}
	if err := w.Client().Patch("stage", chooseTab{1}); err != nil {
		t.Fatal(err)
	}
	// While the second page slides in, the first is laid out as it fades.
	run(1)
	if pages[0].laid < 11 || pages[1].laid != 1 || pages[2].laid != 0 {
		t.Fatalf("as the second page slides in, the pages were laid out %d, %d and %d times", pages[0].laid, pages[1].laid, pages[2].laid)
	}
	run(120)
	was := pages[0].laid
	run(10)
	if pages[0].laid != was || pages[2].laid != 0 {
		t.Fatalf("with the second page showing, the first was laid out %d more times and the third %d", pages[0].laid-was, pages[2].laid)
	}
}

func TestTabsAskedForNoHeightKeepTheTallestPagesHeight(t *testing.T) {
	tabs := NewTabs([]string{"Short", "Tall"}, newSpot(300, 100), newSpot(300, 300))
	// A Scroll lays its child out with no height of its own, as a dialog measures its body.
	sc := NewScroll(tabs)
	w, run := stage(t, &frame{child: sc, size: geom.Sz(300, 600)})
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c chooseTab, u *gunim.UI) { tabs.SetSelected(c.I, u) })
	run(2)
	want := tabs.head + TabGap.Default() + 300
	if err := w.Client().Patch("stage", chooseTab{1}); err != nil {
		t.Fatal(err)
	}
	for f := range 60 {
		run(1)
		if got := sc.content; got != want {
			t.Fatalf("frame %d of the switch: the tabs are %v tall, want the tallest page's %v", f, got, want)
		}
	}
	if err := w.Client().Patch("stage", chooseTab{0}); err != nil {
		t.Fatal(err)
	}
	run(60)
	if got := sc.content; got != want {
		t.Fatalf("back on the short page the tabs are %v tall, want %v", got, want)
	}
}

// recorder fills the space it is given and takes every pointer press.
type recorder struct{ events []input.Event }

func (r *recorder) Handle(e input.Event, _ *gunim.UI) bool {
	if _, ok := e.(input.PointerDown); ok {
		r.events = append(r.events, e)
		return true
	}
	return false
}

func (r *recorder) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

func (r *recorder) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

func TestTabsTakePagesMountedUnderThemAndSkipDisabledOnes(t *testing.T) {
	tabs := NewTabs([]string{"One", "Two", "Three"})
	tabs.Disabled = []bool{false, true}
	w := gunimtest.New(t, geom.Sz(600, 400), nil)
	gunim.RegisterView(w, "tabs", func(struct{}) *Tabs { return tabs }, nil)
	pages := []*spot{newSpot(10, 10), newSpot(10, 10), newSpot(10, 10)}
	for i, p := range pages {
		gunim.RegisterView(w, fmt.Sprint("page", i), func(struct{}) *spot { return p }, nil)
	}
	c := w.Client()
	if err := c.Mount(gunim.Root, "tabs", "tabs", nil); err != nil {
		t.Fatal(err)
	}
	for i := range pages {
		if err := c.Mount("tabs", gunim.ID(fmt.Sprint("page", i)), fmt.Sprint("page", i), nil); err != nil {
			t.Fatal(err)
		}
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	if tabs.count != 3 {
		t.Fatalf("the tabs hold %d pages, want the 3 mounted", tabs.count)
	}
	two := tabs.spans[1]
	click(w, (two[0]+two[1])/2, 10)
	run(1)
	if tabs.Selected() != 0 {
		t.Fatalf("a click on a disabled tab chose tab %d", tabs.Selected())
	}
	w.Input(input.KeyPress{Key: input.KeyRight})
	run(1)
	if tabs.Selected() != 2 {
		t.Fatalf("Right from the first tab chose tab %d, want 2, past the disabled one", tabs.Selected())
	}
}

func TestTabsCanBeSelectedBeforeTheirPagesArrive(t *testing.T) {
	tabs := NewTabs([]string{"One", "Two", "Three"})
	w := gunimtest.New(t, geom.Sz(600, 400), nil)
	gunim.RegisterView(w, "tabs", func(struct{}) *Tabs { return tabs }, func(tb *Tabs, _ struct{}, u *gunim.UI) { tb.SetSelected(2, u) })
	page := newSpot(10, 10)
	gunim.RegisterView(w, "page", func(struct{}) *spot { return page }, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "tabs", "tabs", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if tabs.Selected() != 2 {
		t.Fatalf("selecting a tab whose page has not arrived chose %d", tabs.Selected())
	}
}

func TestFaderRunsUpTheHeight(t *testing.T) {
	s := NewVerticalSlider(0, 100)
	s.Snap = 1
	s.OnChange = func(v float32, u *gunim.UI) gunim.Intent { return slid{v} }
	w, run := stage(t, &frame{child: s, size: geom.Sz(28, 218)})
	// The track runs from 9 at the top to 209 at the bottom, inside the
	// knob's half at each end, and the top of it is the maximum.
	w.Input(input.PointerDown{Pos: geom.Pt(14, 109), Clicks: 1})
	run(1)
	if s.Value() != 50 {
		t.Fatalf("a press mid-track set %v, want 50", s.Value())
	}
	w.Input(input.PointerMove{Pos: geom.Pt(14, 9)})
	w.Input(input.PointerUp{Pos: geom.Pt(14, 9)})
	run(60)
	if s.Value() != 100 || s.at.Value() != 1 {
		t.Fatalf("a drag to the top left %v with the knob at %v", s.Value(), s.at.Value())
	}
	// Up still raises and down still lowers, as on a horizontal one.
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	if s.Value() != 99 {
		t.Fatalf("pressing down left %v, want 99", s.Value())
	}
}

func TestFaderFillsTheHeightItIsGiven(t *testing.T) {
	s := NewVerticalSlider(0, 1)
	w, run := stage(t, &frame{child: s, size: geom.Sz(40, 300)})
	run(1)
	_ = w
	if s.size.H != 300 {
		t.Errorf("a fader in 300 of height took %v", s.size.H)
	}
	if s.size.W > 40 {
		t.Errorf("a fader took %v of width, more than it was given", s.size.W)
	}
}

type committed struct{ v float32 }

func TestASliderHeldByItsKnobMovesAsFarAsThePointer(t *testing.T) {
	s := NewSlider(-100, 100)
	s.SetValue(0, nil)
	s.OnCommit = func(v float32, u *gunim.UI) gunim.Intent { return committed{v} }
	w, run := stage(t, &frame{child: s, size: geom.Sz(218, 28)})
	run(1)
	// The knob is in the middle, at 109; a press beside its middle takes
	// hold of it there, with no jump.
	w.Input(input.PointerDown{Pos: geom.Pt(113, 14), Clicks: 1})
	run(1)
	if s.Value() != 0 {
		t.Fatalf("a press on the knob moved it to %v", s.Value())
	}
	// 50 of 200 across the track is a quarter of the range, 50.
	w.Input(input.PointerMove{Pos: geom.Pt(163, 14)})
	run(1)
	if v := s.Value(); v < 49.9 || v > 50.1 {
		t.Fatalf("a drag of a quarter of the track set %v, want 50", v)
	}
	// With Shift, a tenth as far: 50 more across is 5 more.
	w.Input(input.PointerMove{Pos: geom.Pt(213, 14), Mods: input.ModShift})
	run(1)
	if v := s.Value(); v < 54.9 || v > 55.1 {
		t.Fatalf("a fine drag set %v, want 55", v)
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("committed %v before the drag was let go", got)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(213, 14)})
	run(1)
	if got := sent(w); len(got) != 1 || got[0].(committed).v < 54.9 {
		t.Fatalf("letting go committed %v, want once, at 55", got)
	}
}

func TestADoubleClickSendsASliderBackToRest(t *testing.T) {
	s := NewSlider(-100, 100)
	s.Rest, s.HasRest = 0, true
	s.SetValue(60, nil)
	s.OnCommit = func(v float32, u *gunim.UI) gunim.Intent { return committed{v} }
	w, run := stage(t, &frame{child: s, size: geom.Sz(218, 28)})
	run(1)
	w.Input(input.PointerDown{Pos: geom.Pt(30, 14), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(30, 14)})
	w.Input(input.PointerDown{Pos: geom.Pt(30, 14), Clicks: 2})
	w.Input(input.PointerUp{Pos: geom.Pt(30, 14)})
	run(2)
	if s.Value() != 0 {
		t.Fatalf("a double click left %v, want rest, 0", s.Value())
	}
	// The knob glides back, and the readout can count along with it.
	if sh := s.Shown(); sh == 0 {
		t.Fatal("the knob jumped back to rest")
	}
	run(60)
	if sh := s.Shown(); sh < -0.5 || sh > 0.5 {
		t.Fatalf("the knob settled at %v, want 0", sh)
	}
	got := sent(w)
	if last, ok := got[len(got)-1].(committed); !ok || last.v != 0 {
		t.Fatalf("the double click committed %v, want 0 last", got)
	}
}

func TestASliderSetFromOutsideKeepsItsValueOffTheSteps(t *testing.T) {
	s := NewSlider(-5, 5)
	s.Snap = 0.05
	s.SetValue(1.49, nil)
	if s.Value() != 1.49 {
		t.Fatalf("Set(1.49) on a slider of steps of 0.05 left %v", s.Value())
	}
	s.SetValue(9, nil)
	if s.Value() != 5 {
		t.Fatalf("Set(9) on a slider to 5 left %v", s.Value())
	}
}

func TestASliderThatKeepsFocusLeavesTheKeyboardBe(t *testing.T) {
	s := NewSlider(0, 1)
	s.KeepFocus = true
	if s.FocusOnPress() {
		t.Fatal("a slider set to keep focus takes it on a press")
	}
}

func TestSliderTellsTheWindowAsItMoves(t *testing.T) {
	s := NewSlider(0, 100)
	s.Snap = 1
	var seen []float32
	s.OnChange = func(v float32, _ *gunim.UI) gunim.Intent {
		seen = append(seen, v)
		return nil
	}
	w, run := stage(t, &frame{child: s, size: geom.Sz(218, 28)})
	w.Input(input.PointerDown{Pos: geom.Pt(109, 14), Clicks: 1})
	run(1)
	w.Input(input.KeyPress{Key: input.KeyRight})
	run(1)
	if len(seen) != 2 || seen[0] != 50 || seen[1] != 51 {
		t.Errorf("OnMove saw %v, want 50 then 51", seen)
	}
}

// TestATabsPageStartsAGapBelowTheTitles lays tabs out and finds the page
// a gap below the line under the titles, so its first row stands clear.
func TestATabsPageStartsAGapBelowTheTitles(t *testing.T) {
	page := newSpot(300, 100)
	tabs := NewTabs([]string{"One", "Two"}, page, newSpot(300, 100))
	_, run := stage(t, &frame{child: tabs, size: geom.Sz(300, 300)})
	run(60)
	if want := tabs.head + TabGap.Default(); page.at.Y != want {
		t.Fatalf("the page is drawn at %v, want %v: the titles' %v and a gap of %v", page.at.Y, want, tabs.head, TabGap.Default())
	}
}

// TestArrowsWithCtrlAltOrSuperPassTheSliderAndTabsBy checks the two leave
// those keys to the window's shortcuts, such as Alt+Left in a DAW.
func TestArrowsWithCtrlAltOrSuperPassTheSliderAndTabsBy(t *testing.T) {
	mods := []input.Mods{input.ModAlt, input.ModControl, input.ModSuper}
	s := NewSlider(0, 100)
	s.Snap = 1
	w, run := stage(t, &frame{child: s, size: geom.Sz(218, 28)})
	w.Input(input.PointerDown{Pos: geom.Pt(109, 14), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(109, 14)})
	run(1)
	for _, m := range mods {
		for _, k := range []input.Key{input.KeyLeft, input.KeyRight, input.KeyEnd, input.KeyPageDown} {
			w.Input(input.KeyPress{Key: k, Mods: m})
		}
	}
	run(1)
	if s.Value() != 50 {
		t.Fatalf("the keys with Ctrl, Alt or Super moved the slider to %v", s.Value())
	}
	w.Input(input.KeyPress{Key: input.KeyLeft})
	run(1)
	if s.Value() != 49 {
		t.Fatalf("plain Left took the slider to %v, want 49", s.Value())
	}

	tabs := NewTabs([]string{"One", "Two"}, &recorder{}, &recorder{})
	w, run = stage(t, &frame{child: tabs, size: geom.Sz(300, 200)})
	click(w, tabs.spans[0][0]+5, 10)
	run(1)
	for _, m := range mods {
		w.Input(input.KeyPress{Key: input.KeyRight, Mods: m})
	}
	run(1)
	if tabs.Selected() != 0 {
		t.Fatalf("Right with Ctrl, Alt or Super went to tab %d", tabs.Selected())
	}
	w.Input(input.KeyPress{Key: input.KeyRight})
	run(1)
	if tabs.Selected() != 1 {
		t.Fatal("plain Right on the focused titles did not go to the next tab")
	}
}
