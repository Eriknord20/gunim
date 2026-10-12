package widget

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

type chose struct{ I int }

// labelsOf, hintsOf, iconsOf, checkedOf and disabledOf return a field of each item, in order.
func labelsOf(items []MenuItem) []string {
	return fieldOf(items, func(it MenuItem) string { return it.Label })
}
func hintsOf(items []MenuItem) []string {
	return fieldOf(items, func(it MenuItem) string { return it.Hint })
}
func iconsOf(items []MenuItem) []*icon.Icon {
	return fieldOf(items, func(it MenuItem) *icon.Icon { return it.Icon })
}
func checkedOf(items []MenuItem) []bool {
	return fieldOf(items, func(it MenuItem) bool { return it.Checked })
}
func disabledOf(items []MenuItem) []bool {
	return fieldOf(items, func(it MenuItem) bool { return it.Disabled })
}

func fieldOf[T any](items []MenuItem, f func(MenuItem) T) []T {
	out := make([]T, len(items))
	for i, it := range items {
		out[i] = f(it)
	}
	return out
}

func newPicker(t *testing.T) (*gunim.Window, func(int), *Dropdown) {
	t.Helper()
	d := NewDropdown(Labels("Apple", "Banana", "Cherry"))
	d.OnChange = func(i int, u *gunim.UI) gunim.Intent { return chose{i} }
	w, run := stage(t, &frame{child: d, size: geom.Sz(200, 36)})
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18)})
	run(1)
	return w, run, d
}

func TestDropdownOpensOnClickAndClosesOnSecondClick(t *testing.T) {
	w, run, d := newPicker(t)
	if !d.IsOpen() {
		t.Fatal("a click did not open the list")
	}
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1})
	run(1)
	if d.IsOpen() {
		t.Fatal("a second click on the drop-down left the list open")
	}
}

func TestDropdownPicksWithTheKeyboard(t *testing.T) {
	w, run, d := newPicker(t)
	for _, k := range []input.Key{input.KeyDown, input.KeyDown, input.KeyEnter} {
		w.Input(input.KeyPress{Key: k})
		run(1)
	}
	if d.IsOpen() || d.Selected() != 2 {
		t.Fatalf("open %v, selected %d; want closed on 2", d.IsOpen(), d.Selected())
	}
	select {
	case e := <-w.Client().Intents():
		if e.Intent != (chose{2}) {
			t.Fatalf("intent %v, want chose{2}", e.Intent)
		}
	default:
		t.Fatal("no intent for the change")
	}
}

func TestAPickRunsInTheWindowToo(t *testing.T) {
	w, run, d := newPicker(t)
	got := -1
	d.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		if u == nil {
			t.Fatal("OnChange ran without the UI")
		}
		got = i
		return chose{i}
	}
	for _, k := range []input.Key{input.KeyDown, input.KeyEnter} {
		w.Input(input.KeyPress{Key: k})
		run(1)
	}
	if got != 1 {
		t.Fatalf("picking Banana told the window %d", got)
	}
	if in := sent(w); len(in) != 1 || in[0] != (chose{1}) {
		t.Fatalf("picking Banana sent %v, want chose{1} once", in)
	}
}

func TestDropdownClosesOnEscapeAndOutsideClick(t *testing.T) {
	w, run, d := newPicker(t)
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	if d.IsOpen() {
		t.Fatal("Escape left the list open")
	}
	w.Input(input.KeyPress{Key: input.KeySpace})
	run(1)
	if !d.IsOpen() {
		t.Fatal("Space did not open the list")
	}
	w.Input(input.PointerDown{Pos: geom.Pt(500, 400), Clicks: 1})
	run(1)
	if d.IsOpen() {
		t.Fatal("a click outside left the list open")
	}
}

func TestTooltipShowsAfterItsDelay(t *testing.T) {
	b := NewButton("Hover me")
	tip := NewTooltip(b, "Hello")
	w, run := stage(t, &frame{child: tip, size: geom.Sz(200, 36)})
	w.Input(input.PointerMove{Pos: geom.Pt(20, 18), Time: time.Now()})
	run(1)
	if tip.tip.popup != nil {
		t.Fatal("the tooltip showed at once")
	}
	run(60) // a second
	if tip.tip.popup == nil {
		t.Fatal("the tooltip did not show after its delay")
	}
	w.Input(input.PointerLeave{Time: time.Now()})
	run(1)
	if tip.tip.popup != nil {
		t.Fatal("the tooltip stayed after the pointer left")
	}
}

func TestATooltipsWordsTurnOverAsTheyChange(t *testing.T) {
	// A button that turns through settings, its tooltip saying which: a
	// press, which the button takes, leaves the tooltip up.
	b := NewButton("Turn")
	tip := NewTooltip(b, "As far through")
	w, run := stage(t, &frame{child: tip, size: geom.Sz(200, 36)})
	w.Input(input.PointerMove{Pos: geom.Pt(20, 18), Time: time.Now()})
	run(60)
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18), Time: time.Now()})
	tip.Text = "At the same time"
	run(2)
	c := tip.tip.card
	if c == nil || c.text != "At the same time" || c.was != "As far through" || c.turn.Value() >= 1 {
		t.Fatal("its text changed, the tooltip's words do not turn over")
	}
	run(60)
	if c.turn.Value() < 0.99 {
		t.Fatalf("a second on, the words are %.2f turned", c.turn.Value())
	}
}

func TestADropdownKeepsWithinItsMaxWidth(t *testing.T) {
	d := NewDropdown(Labels("All sessions", "#12  2024-02-15 10:17:39+01:00  v5.4.1  3 err"))
	d.MaxWidth = 150
	d.SetSelected(1, nil)
	stage(t, &frame{child: Row(d), size: geom.Sz(600, 100)})
	if d.size.W != 150 {
		t.Fatalf("the drop-down is %v wide, want its MaxWidth of 150", d.size.W)
	}
	if w := d.shown.run.Advance; w <= 150-2*FieldPadding.Default()-chevron {
		t.Fatalf("the uncut item is only %v wide; the test needs one that is cut", w)
	}
}

func TestAnOpenDropdownShowsItsItemsAsTheyAreNow(t *testing.T) {
	_, run, d := newPicker(t)
	d.SetItems(Labels("Date", "Apple", "Banana", "Cherry"))
	run(1)
	m := d.menu
	at := slices.Index(labelsOf(m.Items()), "Cherry")
	r := m.RowRect(at)
	p := geom.Pt(r.Min.X+20, r.Center().Y)
	d.popup.Input(input.PointerMove{Pos: p, Time: time.Now()})
	d.popup.Input(input.PointerDown{Pos: p, Clicks: 1, Time: time.Now()})
	d.popup.Input(input.PointerUp{Pos: p, Time: time.Now()})
	run(2)
	if got := d.Items()[d.Selected()].Label; got != "Cherry" {
		t.Fatalf("a click on the Cherry shown chose %q", got)
	}
}

func TestADropdownDisabledWhileFocusedClosesAndLetsItsRingGo(t *testing.T) {
	d := NewDropdown(Labels("Apple", "Banana"))
	col := Column(d, NewTextField())
	w, run := stage(t, &frame{child: col, size: geom.Sz(300, 200)})
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(20)
	if d.ring.Value() < 0.99 {
		t.Fatalf("Tab to the drop-down rings it %v", d.ring.Value())
	}
	w.Input(input.KeyPress{Key: input.KeySpace})
	run(1)
	if !d.IsOpen() {
		t.Fatal("Space did not open the list")
	}
	d.Disabled = true
	run(1)
	if d.IsOpen() {
		t.Fatal("the list stayed open as the drop-down was disabled")
	}
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(60)
	if d.ring.Value() > 0.01 {
		t.Fatalf("the focus gone, the disabled drop-down's ring is %v", d.ring.Value())
	}
}

// cutEnd returns where the first text in ops that ends in an ellipsis ends, in the painter's space, and false where
// none does.
func cutEnd(ops []paint.Op) (float32, bool) {
	face := faceIn(Font, nil)
	for _, op := range ops {
		tx, ok := op.(*paint.TextOp)
		if !ok || len(tx.Glyphs) == 0 {
			continue
		}
		ell := face.Shape("…", tx.Size)
		if last := tx.Glyphs[len(tx.Glyphs)-1]; last.ID == ell.Glyphs[0].ID {
			return tx.Transform.C + last.At.X + ell.Advance, true
		}
	}
	return 0, false
}

// textAt returns the text in ops whose glyphs are those of run, and false where none is.
func textAt(ops []paint.Op, run text.Run) (*paint.TextOp, bool) {
	for _, op := range ops {
		if tx, ok := op.(*paint.TextOp); ok && len(tx.Glyphs) == len(run.Glyphs) && len(run.Glyphs) > 0 &&
			tx.Glyphs[0].ID == run.Glyphs[0].ID && tx.Glyphs[len(tx.Glyphs)-1].ID == run.Glyphs[len(run.Glyphs)-1].ID {
			return tx, true
		}
	}
	return nil, false
}

func TestAMenuKeepsToItsBoxAndCutsALongItemShortBeforeItsHint(t *testing.T) {
	// A list of completions, in its box of 360 by 320
	m := NewMenu([]MenuItem{{Label: "Short"}, {Label: strings.Repeat("A very long completion label ", 20), Hint: "Ctrl+Shift+L"}})
	m.Layout(gunim.Loose(geom.Sz(360, 320)), gunim.Frame{Scale: 1}, gunim.Children{})
	if m.card.Max.X > 360 {
		t.Fatalf("the menu reaches %v, past its box's 360", m.card.Max.X)
	}
	ops := painted(m, geom.Sz(360, 320))
	hint := faceIn(Font, nil).Shape("Ctrl+Shift+L", TextSize.Default()*0.9)
	h, ok := textAt(ops, hint)
	if !ok {
		t.Fatal("the hint is not drawn")
	}
	if left, right := h.Transform.C, h.Transform.C+hint.Advance; right > m.card.Max.X || left < m.card.Min.X {
		t.Fatalf("the hint runs from %v to %v, outside the card %v", left, right, m.card)
	}
	end, ok := cutEnd(ops)
	if !ok {
		t.Fatal("the long item is not cut short")
	}
	if end > h.Transform.C {
		t.Fatalf("the long item runs to %v, into its hint at %v", end, h.Transform.C)
	}
}

func TestAMenuKeepsToTheScreensWidth(t *testing.T) {
	d := NewDropdown(Labels("Short", strings.Repeat("A very long item ", 40)))
	d.MaxWidth = 150
	w, run := stage(t, &frame{child: d, size: geom.Sz(150, 36)})
	w.Offscreen().SetWorkArea(geom.Rc(0, 0, 300, 600))
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18)})
	run(20)
	m := d.menu
	if right := m.card.Max.X + m.margin; right > 300 {
		t.Fatalf("the menu's window is %v wide, on a screen 300 wide", right)
	}
	if size := d.popup.Offscreen().Size(); size.W > 300 {
		t.Fatalf("the menu's window is %v, on a screen 300 wide", size)
	}
	if _, ok := cutEnd(painted(m, geom.Sz(m.card.Max.X+m.margin, m.card.Max.Y+m.margin))); !ok {
		t.Fatal("the long item is not cut short")
	}
}

func TestATooltipKeepsToTheScreensWidth(t *testing.T) {
	tip := NewTooltip(NewButton("Hover me"), strings.Repeat("word ", 400))
	w, run := stage(t, &frame{child: tip, size: geom.Sz(200, 36)})
	w.Offscreen().SetWorkArea(geom.Rc(0, 0, 300, 600))
	w.Input(input.PointerMove{Pos: geom.Pt(20, 18), Time: time.Now()})
	run(60)
	if tip.tip.popup == nil {
		t.Fatal("the tooltip did not show")
	}
	if size := tip.tip.popup.Offscreen().Size(); size.W > 300 {
		t.Fatalf("the tooltip's window is %v, on a screen 300 wide", size)
	}
}

func TestATooltipOfManyWordsWraps(t *testing.T) {
	tip := NewTooltip(NewButton("Hover me"), strings.Repeat("word ", 400))
	w, run := stage(t, &frame{child: tip, size: geom.Sz(200, 36)})
	w.Input(input.PointerMove{Pos: geom.Pt(20, 18), Time: time.Now()})
	run(60)
	if tip.tip.popup == nil {
		t.Fatal("the tooltip did not show")
	}
	pad := TooltipPadding.Default()
	if size, most := tip.tip.popup.Offscreen().Size(), TooltipMaxWidth.Default()+pad.Left+pad.Right+2*MenuMargin.Default(); size.W > most {
		t.Fatalf("the tooltip's window is %v, wider than %v", size, most)
	}
}

func TestAMenuButtonCutsItsTitleShortBeforeItsChevron(t *testing.T) {
	b := NewMenuButton("Show all the columns", Labels("Time", "Level"))
	spy := &opsSpy{child: b, size: geom.Sz(90, 36)}
	stage(t, spy)
	pad := FieldPadding.Default()
	end, ok := cutEnd(spy.ops)
	if !ok {
		t.Fatal("the title is not cut short")
	}
	if room := 90 - pad - chevron - pad; end > room {
		t.Fatalf("the title runs to %v, past %v, where the chevron's room starts", end, room)
	}
}

// opsSpy lays its child out at size, and keeps what it paints.
type opsSpy struct {
	child gunim.Node
	size  geom.Size
	ops   []paint.Op
}

func (s *opsSpy) Children() []gunim.Node { return []gunim.Node{s.child} }

func (s *opsSpy) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	k.Layout(gunim.Tight(s.size))
	k.Place(geom.Point{})
	return c.Max
}

func (s *opsSpy) Paint(_ *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	var p paint.Painter
	kids.At(0).Paint(&p)
	s.ops = p.Ops()
}

func TestSetItemsMeasuresTheItemsAgainInTheSameSlice(t *testing.T) {
	items := Labels("One", "Two")
	m := NewMenu(items)
	d := NewDropdown(items)
	f := gunim.Frame{Scale: 1}
	c := gunim.Loose(geom.Sz(800, 400))
	narrow, short := m.Layout(c, f, gunim.Children{}).W, d.Layout(c, f, gunim.Children{}).W
	items[1].Label = strings.Repeat("Two ", 20)
	m.SetItems(items)
	d.SetItems(items)
	if w := m.Layout(c, f, gunim.Children{}).W; w <= narrow {
		t.Fatalf("with a longer item set again the menu is %v wide, as it was before", w)
	}
	if w := d.Layout(c, f, gunim.Children{}).W; w <= short {
		t.Fatalf("with a longer item set again the drop-down is %v wide, as it was before", w)
	}
	if got := m.Items()[1].Label; got != items[1].Label {
		t.Fatalf("Items gives %q", got)
	}
}

func TestLabelsMakesPlainItems(t *testing.T) {
	got := Labels("Cut", "Copy")
	if len(got) != 2 || got[0] != (MenuItem{Label: "Cut"}) || got[1] != (MenuItem{Label: "Copy"}) {
		t.Fatalf("Labels gave %v", got)
	}
}

func TestATooltipHiddenStaysDownUntilThePointerComesBack(t *testing.T) {
	b := NewButton("Open")
	tip := NewTooltip(b, "Hello")
	w, run := stage(t, &frame{child: tip, size: geom.Sz(200, 36)})
	w.Input(input.PointerMove{Pos: geom.Pt(20, 18), Time: time.Now()})
	run(60)
	if tip.tip.popup == nil {
		t.Fatal("the tooltip did not show after its delay")
	}
	b.OnClick = func(u *gunim.UI) gunim.Intent { tip.Hide(u); return nil }
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary})
	run(1)
	if tip.tip.popup != nil {
		t.Fatal("hidden, the tooltip stayed up")
	}
	w.Input(input.PointerMove{Pos: geom.Pt(24, 18), Time: time.Now()})
	run(60)
	if tip.tip.popup != nil {
		t.Fatal("hidden, the tooltip came back before the pointer left")
	}
	w.Input(input.PointerLeave{Time: time.Now()})
	w.Input(input.PointerMove{Pos: geom.Pt(20, 18), Time: time.Now()})
	run(60)
	if tip.tip.popup == nil {
		t.Fatal("the pointer back, the tooltip stayed down")
	}
}
