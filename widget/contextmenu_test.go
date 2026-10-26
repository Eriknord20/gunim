package widget

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

type menuPicked struct{ item string }

// contextStage holds a context menu whose items depend on where it is
// pressed: the top half has Open, Paste and Delete, with Paste disabled,
// and the bottom half none.
func contextStage(t *testing.T) (w *gunim.Window, c *ContextMenu, run func(int)) {
	t.Helper()
	c = NewContextMenu(&block{h: 300}, nil)
	c.Prepare = func(at geom.Point, _ *gunim.UI) bool {
		if at.Y >= 150 {
			return false
		}
		c.SetItems([]MenuItem{
			{Label: "Open", Hint: "Enter"},
			{Label: "Paste", Hint: "Ctrl+V", Disabled: true},
			{Label: "Delete", Hint: "Delete", Break: true},
		})
		return true
	}
	c.OnPick = func(i int, u *gunim.UI) gunim.Intent { return menuPicked{c.Items()[i].Label} }
	w, run = stage(t, &frame{child: c, size: geom.Sz(400, 300)})
	return w, c, run
}

func TestAContextMenuTakesItsItemsFromWhereItOpens(t *testing.T) {
	w, c, run := contextStage(t)
	w.Input(input.PointerDown{Pos: geom.Pt(50, 200), Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	run(2)
	if c.Focusable() {
		t.Fatal("a press where Prepare says no opened a menu")
	}
	w.Input(input.PointerUp{Pos: geom.Pt(50, 200), Button: input.ButtonSecondary, Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(50, 50), Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(50, 50), Button: input.ButtonSecondary, Time: time.Now()})
	run(10)
	if !c.Focusable() || c.menu == nil {
		t.Fatal("the menu did not open")
	}
	if items := c.menu.Items(); !slices.Equal(hintsOf(items), []string{"Enter", "Ctrl+V", "Delete"}) || !items[1].Disabled {
		t.Fatalf("the menu has hints %v and disabled %v", hintsOf(items), disabledOf(items))
	}
	// Down from Open passes the disabled Paste by.
	w.Input(input.KeyPress{Key: input.KeyDown, Time: time.Now()})
	w.Input(input.KeyPress{Key: input.KeyDown, Time: time.Now()})
	w.Input(input.KeyPress{Key: input.KeyEnter, Time: time.Now()})
	run(2)
	if got := sent(w); len(got) != 1 || got[0] != (menuPicked{"Delete"}) {
		t.Fatalf("the keys picked %v, want Delete", got)
	}
}

func TestAContextMenuCanActInsideTheWindow(t *testing.T) {
	w, c, run := contextStage(t)
	var local []int
	c.OnPick = func(i int, _ *gunim.UI) gunim.Intent {
		local = append(local, i)
		return nil
	}
	w.Input(input.PointerDown{Pos: geom.Pt(50, 50), Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(50, 50), Button: input.ButtonSecondary, Time: time.Now()})
	run(5)
	w.Input(input.KeyPress{Key: input.KeyDown, Time: time.Now()})
	w.Input(input.KeyPress{Key: input.KeyEnter, Time: time.Now()})
	run(2)
	if !slices.Equal(local, []int{0}) {
		t.Fatalf("Picked heard %v, want the first item", local)
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("an OnPick of nil sent %v", got)
	}
}

type subPicked struct{ path string }

// subStage holds a context menu of Open, Open with, whose submenu has Editor, Viewer and Choose another app…, and
// Delete. A pick in the submenu sends its path.
func subStage(t *testing.T) (w *gunim.Window, c *ContextMenu, run func(int)) {
	t.Helper()
	c = NewContextMenu(&block{h: 300}, []MenuItem{
		{Label: "Open"},
		{Label: "Open with", Sub: &Submenu{Items: []MenuItem{
			{Label: "Editor"}, {Label: "Viewer"}, {Label: "Choose another app…", Break: true},
		}}},
		{Label: "Delete"},
		{Label: "Rename"},
	})
	c.OnPick = func(i int, u *gunim.UI) gunim.Intent { return menuPicked{c.Items()[i].Label} }
	c.OnPickSub = func(path []int, _ *gunim.UI) gunim.Intent { return subPicked{fmt.Sprint(path)} }
	w, run = stage(t, &frame{child: c, size: geom.Sz(400, 300)})
	rightClick(w, 50, 50)
	run(10)
	if !c.Focusable() {
		t.Fatal("the menu did not open")
	}
	return w, c, run
}

// pointAt moves the pointer onto item i of the context menu's menu, from its left part.
func pointAt(c *ContextMenu, i int, run func(int)) {
	r := c.menu.RowRect(i)
	c.popup.Input(input.PointerMove{Pos: geom.Pt(r.Min.X+20, r.Center().Y), Time: time.Now()})
	run(1)
}

func TestAnItemWithASubmenuShowsAChevronInPlaceOfAHint(t *testing.T) {
	m := NewMenu([]MenuItem{{Label: "Open", Hint: "Enter"}, {Label: "Open with", Hint: "Ctrl+W", Sub: &Submenu{Items: Labels("Editor")}}})
	m.Layout(gunim.Loose(geom.Sz(400, 300)), gunim.Frame{Scale: 1}, gunim.Children{})
	ops := painted(m, geom.Sz(400, 300))
	var chevrons []*paint.MaskOp
	for _, op := range masksIn(ops) {
		if s, ok := op.Shape.(icon.Stroke); ok && s.Icon == icon.ChevronRight {
			chevrons = append(chevrons, op)
		}
	}
	if len(chevrons) != 1 {
		t.Fatalf("the menu draws %d chevrons, want 1", len(chevrons))
	}
	if r, row := chevrons[0].Rect, m.RowRect(1); r.Center().Y != row.Center().Y || r.Max.X > m.card.Max.X || r.Min.X < m.card.Max.X-40 {
		t.Fatalf("the chevron is at %v, not at the right of the row %v", r, row)
	}
	if _, ok := textAt(ops, faceIn(Font, nil).Shape("Ctrl+W", TextSize.Default()*0.9)); ok {
		t.Fatal("the item with a submenu draws its hint")
	}
	if _, ok := textAt(ops, faceIn(Font, nil).Shape("Enter", TextSize.Default()*0.9)); !ok {
		t.Fatal("the item without a submenu lost its hint")
	}
}

func TestASubmenuOpensOnceThePointerRestsOnItsItem(t *testing.T) {
	_, c, run := subStage(t)
	pointAt(c, 1, run)
	run(5)
	if c.menu.subOpen() >= 0 {
		t.Fatal("the submenu opened before the pointer rested")
	}
	run(20)
	if c.menu.subOpen() != 1 || c.menu.sub.popup == nil {
		t.Fatal("the submenu did not open as the pointer rested on its item")
	}
	if got := labelsOf(c.menu.sub.menu.Items()); !slices.Equal(got, []string{"Editor", "Viewer", "Choose another app…"}) {
		t.Fatalf("the submenu has %v", got)
	}
	// Resting on another item closes it.
	pointAt(c, 3, run)
	run(30)
	if c.menu.subOpen() >= 0 {
		t.Fatal("the submenu stayed open as the pointer rested on another item")
	}
}

func TestASubmenuOpensAtOnceOnAClick(t *testing.T) {
	_, c, run := subStage(t)
	r := c.menu.RowRect(1)
	p := geom.Pt(r.Min.X+20, r.Center().Y)
	c.popup.Input(input.PointerMove{Pos: p, Time: time.Now()})
	c.popup.Input(input.PointerDown{Pos: p, Clicks: 1, Time: time.Now()})
	c.popup.Input(input.PointerUp{Pos: p, Time: time.Now()})
	run(1)
	if c.menu.subOpen() != 1 || !c.Focusable() {
		t.Fatalf("a click on the item opened submenu %d, the menu open %v", c.menu.subOpen(), c.Focusable())
	}
}

func TestTheKeysWorkASubmenu(t *testing.T) {
	w, c, run := subStage(t)
	typeKeys(w, run, 0, input.KeyDown, input.KeyDown, input.KeyRight)
	s := c.menu.sub
	if s == nil || s.item != 1 || !s.keys || s.menu.Highlighted() != 0 {
		t.Fatal("Right on Open with did not open its submenu with the keys on Editor")
	}
	typeKeys(w, run, 0, input.KeyDown, input.KeyLeft)
	if c.menu.sub != nil || !c.Focusable() || c.menu.Highlighted() != 1 {
		t.Fatal("Left did not close the submenu back to Open with")
	}
	typeKeys(w, run, 0, input.KeyEnter)
	if c.menu.sub == nil || !c.menu.sub.keys {
		t.Fatal("Enter on Open with did not open its submenu")
	}
	typeKeys(w, run, 0, input.KeyEscape)
	if c.menu.sub != nil || !c.Focusable() {
		t.Fatal("Escape did not close the submenu alone")
	}
	typeKeys(w, run, 0, input.KeyRight, input.KeyDown, input.KeyEnter)
	if got := sent(w); len(got) != 1 || got[0] != (subPicked{"[1 1]"}) {
		t.Fatalf("Enter on Viewer sent %v", got)
	}
	run(2)
	if c.Focusable() {
		t.Fatal("a pick in the submenu left the menu open")
	}
	rightClick(w, 50, 50)
	run(10)
	typeKeys(w, run, 0, input.KeyDown, input.KeyDown, input.KeyRight, input.KeyEscape, input.KeyEscape)
	if c.Focusable() {
		t.Fatal("a second Escape did not close the menu")
	}
}

func TestAPickInASubmenuIsReportedByItsPath(t *testing.T) {
	w, c, run := subStage(t)
	pointAt(c, 1, run)
	run(30)
	s := c.menu.sub
	if s == nil {
		t.Fatal("the submenu did not open")
	}
	r := s.menu.RowRect(2)
	p := geom.Pt(r.Min.X+20, r.Center().Y)
	s.popup.Input(input.PointerMove{Pos: p, Time: time.Now()})
	s.popup.Input(input.PointerDown{Pos: p, Clicks: 1, Time: time.Now()})
	s.popup.Input(input.PointerUp{Pos: p, Time: time.Now()})
	run(2)
	if got := sent(w); len(got) != 1 || got[0] != (subPicked{"[1 2]"}) {
		t.Fatalf("a click on Choose another app… sent %v", got)
	}
	if c.Focusable() {
		t.Fatal("the pick left the menu open")
	}
}

func TestASubmenuStaysOpenAsThePointerHeadsForIt(t *testing.T) {
	for _, aim := range []bool{true, false} {
		_, c, run := subStage(t)
		pointAt(c, 1, run)
		run(30)
		s := c.menu.sub
		if s == nil {
			t.Fatal("the submenu did not open")
		}
		// Down and right across Delete, toward the submenu, or straight down onto it.
		from, to := c.menu.RowRect(1), c.menu.RowRect(2)
		x := from.Max.X - 40
		c.popup.Input(input.PointerMove{Pos: geom.Pt(x, from.Center().Y), Time: time.Now()})
		if aim {
			c.popup.Input(input.PointerMove{Pos: geom.Pt(x+20, to.Min.Y+4), Time: time.Now()})
		} else {
			c.popup.Input(input.PointerMove{Pos: geom.Pt(x, to.Center().Y), Time: time.Now()})
		}
		run(21)
		if open := c.menu.sub == s; open != aim {
			t.Fatalf("heading for the submenu %v, it is open %v after %v", aim, open, subDelay)
		}
		if !aim {
			continue
		}
		r := s.menu.RowRect(0)
		s.popup.Input(input.PointerMove{Pos: geom.Pt(r.Min.X+20, r.Center().Y), Time: time.Now()})
		run(60)
		if c.menu.sub != s || c.menu.Highlighted() != 1 {
			t.Fatalf("on the submenu, it is open %v with the menu's highlight on %d", c.menu.sub == s, c.menu.Highlighted())
		}
	}
}

func TestASubmenuOpensLeftWhereTheScreenEndsOnTheRight(t *testing.T) {
	for _, flip := range []bool{false, true} {
		w, c, run := subStage(t)
		card := c.menu.card
		if flip {
			// Room for the menu, and 30 past it on the right
			c.popup.Offscreen().SetWorkArea(geom.Rect{Min: geom.Pt(-600, -400), Max: geom.Pt(card.Max.X+30, 1000)})
		}
		typeKeys(w, run, 0, input.KeyDown, input.KeyDown, input.KeyRight)
		run(5)
		s := c.menu.sub
		if s == nil {
			t.Fatal("the submenu did not open")
		}
		sm := s.menu
		at := s.popup.Offscreen().Anchor().Min
		right := at.X + sm.card.Max.X
		left := at.X + sm.card.Min.X
		top := at.Y + sm.card.Min.Y
		if flip && right != card.Min.X {
			t.Fatalf("near the screen's right edge, the submenu's card ends at %v, not at the menu's left edge %v", right, card.Min.X)
		}
		if !flip && left != card.Max.X {
			t.Fatalf("the submenu's card starts at %v, not at the menu's right edge %v", left, card.Max.X)
		}
		if row, first := c.menu.RowRect(1).Min.Y, top+sm.pad; row != first {
			t.Fatalf("the submenu's first row is at %v, not level with its item at %v", first, row)
		}
		if got, onLeft := sm.besideCard(s.anchor); onLeft != flip || got.Min.X != left || got.Min.Y != top {
			t.Fatalf("the menu takes the submenu's card to be at %v, left %v", got, onLeft)
		}
	}
}

func TestASubmenusItemsCanArriveWhileItIsOpen(t *testing.T) {
	w, c, run := subStage(t)
	items := c.Items()
	looking := slices.Clone(items)
	looking[1].Sub = &Submenu{Items: []MenuItem{{Label: "Looking…", Disabled: true}, {Label: "Choose another app…", Break: true}}}
	c.SetItems(looking)
	run(1)
	typeKeys(w, run, 0, input.KeyDown, input.KeyDown, input.KeyRight)
	s := c.menu.sub
	if s == nil || s.menu.Highlighted() != 1 {
		t.Fatal("Right did not open the submenu with the keys on Choose another app…")
	}
	found := slices.Clone(items)
	found[1].Sub = &Submenu{Items: []MenuItem{{Label: "Editor"}, {Label: "Viewer"}, {Label: "Choose another app…", Break: true}}}
	c.SetItems(found)
	run(2)
	if c.menu.sub != s {
		t.Fatal("the submenu closed as its items came")
	}
	if got := labelsOf(s.menu.Items()); !slices.Equal(got, []string{"Editor", "Viewer", "Choose another app…"}) {
		t.Fatalf("the submenu shows %v", got)
	}
	if s.menu.Highlighted() != 2 {
		t.Fatalf("the highlight moved to %d, off Choose another app…", s.menu.Highlighted())
	}
	typeKeys(w, run, 0, input.KeyEnter)
	if got := sent(w); len(got) != 1 || got[0] != (subPicked{"[1 2]"}) {
		t.Fatalf("Enter sent %v", got)
	}
}

// fileMenu is a file manager's context menu on a file: seventeen items in four groups.
func fileMenu() []MenuItem {
	labels := [][]string{
		{"Open", "Open with system", "Open with…", "Open in new window", "Show in system file manager", "Compare", "Send to"},
		{"Cut", "Copy", "Paste"},
		{"Rename", "Duplicate", "Move to trash", "Delete permanently"},
		{"Pin to favourites", "Copy path", "Properties"},
	}
	var items []MenuItem
	for _, group := range labels {
		for i, l := range group {
			items = append(items, MenuItem{Label: l, Break: i == 0 && len(items) > 0})
		}
	}
	return items
}

// openContextMenuIn opens a context menu of items, over all of a window 800 by 600 on a screen of the same size, by
// a press at at, and returns it with the size of the menu's window.
func openContextMenuIn(t *testing.T, items []MenuItem, at geom.Point) (*ContextMenu, geom.Size) {
	t.Helper()
	c := NewContextMenu(&block{h: 600}, items)
	w, run := stage(t, &frame{child: c, size: geom.Sz(800, 600)})
	w.Offscreen().SetWorkArea(geom.Rc(0, 0, 800, 600))
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonSecondary, Time: time.Now()})
	run(20)
	if c.menu == nil || c.popup == nil {
		t.Fatal("the menu did not open")
	}
	return c, c.popup.Offscreen().Size()
}

func TestAFileMenuOpenedLowInTheWindowShowsAllItsItems(t *testing.T) {
	// About 520 tall: more than the room above or below the press, but not the screen's.
	c, size := openContextMenuIn(t, fileMenu(), geom.Pt(300, 380))
	if c.menu.scroll.scrollable() {
		t.Fatalf("a menu %v tall scrolls, on a screen 600 tall", c.menu.content()+2*c.menu.pad)
	}
	if size.H > 600 {
		t.Fatalf("the menu's window is %v, on a screen 600 tall", size)
	}
}

func TestAContextMenuTallerThanTheScreenScrolls(t *testing.T) {
	c, size := openContextMenuIn(t, Labels(slices.Repeat([]string{"Item"}, 40)...), geom.Pt(300, 380))
	if !c.menu.scroll.scrollable() {
		t.Fatal("40 items on a screen 600 tall do not scroll")
	}
	if size.H > 600 {
		t.Fatalf("the menu's window is %v, on a screen 600 tall", size)
	}
}

// A test finds an item of an open menu in the window's accessibility
// tree, where the menu shows below where it opened, and clicks it there,
// as a person would: the click reaches the menu, which picks it.
func TestAnOpenMenusItemIsFoundAndClickedThroughTheWindowsTree(t *testing.T) {
	w, _, run := contextStage(t)
	w.Inject(input.PointerDown{Pos: geom.Pt(50, 50), Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Inject(input.PointerUp{Pos: geom.Pt(50, 50), Button: input.ButtonSecondary, Time: time.Now()})
	run(10)
	var open *access.Node
	var walk func(n *access.Node)
	walk = func(n *access.Node) {
		if n.Role == access.RoleMenuItem && n.Name == "Open" {
			open = n
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(w.Access().Root)
	if open == nil {
		t.Fatal("the window's tree holds no Open")
	}
	// The menu opens at the press, so its first item shows near it,
	// not in the window's corner.
	if b := open.Bounds; b.Min.X < 40 || b.Min.Y < 40 || open.Description != "Enter" {
		t.Fatalf("Open shows at %v, saying %q", b, open.Description)
	}
	at := open.Bounds.Center()
	w.Inject(input.PointerMove{Pos: at, Time: time.Now()})
	w.Inject(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Inject(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	run(2)
	if got := sent(w); len(got) != 1 || got[0] != (menuPicked{"Open"}) {
		t.Fatalf("the click picked %v, want Open", got)
	}
}
