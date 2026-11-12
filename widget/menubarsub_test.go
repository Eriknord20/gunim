package widget

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// barSubPick is a pick in a submenu of a menubar's menu.
type barSubPick struct {
	menu int
	path string
}

// subBar is a menubar whose File menu has an item, Open recent, with a submenu, and in it More, with a submenu of its
// own, over a text field that has the keyboard.
type subBar struct {
	t     *testing.T
	w     *gunim.Window
	b     *Menubar
	field *TextField
	run   func(int)
	picks []barPick
	subs  []barSubPick
}

func newSubBar(t *testing.T, compact bool) *subBar {
	t.Helper()
	s := &subBar{t: t}
	s.b = NewMenubar(
		BarMenu{Title: "&File", Items: []MenuItem{
			{Label: "&New", Hint: "Ctrl+N"},
			{Label: "Open &recent", Sub: &Submenu{Items: []MenuItem{
				{Label: "&a.txt"},
				{Label: "&b.txt", Hint: "Ctrl+1"},
				{Label: "&More", Break: true, Sub: &Submenu{Items: Labels("&c.txt", "&d.txt")}},
				{Label: "&Gone", Disabled: true},
			}}},
			{Label: "&Quit", Break: true},
		}},
		BarMenu{Title: "&Edit", Items: Labels("&Copy", "&Paste")},
		BarMenu{Title: "&View", Items: Labels("&Sidebar")},
	)
	s.b.Compact = compact
	s.b.OnPick = func(m, i int, _ *gunim.UI) gunim.Intent { s.picks = append(s.picks, barPick{m, i}); return nil }
	s.b.OnPickSub = func(m int, path []int, _ *gunim.UI) gunim.Intent {
		s.subs = append(s.subs, barSubPick{m, fmt.Sprint(path)})
		return nil
	}
	s.field = NewTextField()
	col := Column(s.b, s.field)
	col.Cross = CrossStretch
	s.w, s.run = stage(t, &frame{child: col, size: geom.Sz(600, 400), keysGoOn: true})
	s.w.Input(input.PointerDown{Pos: geom.Pt(100, 60), Clicks: 1, Time: time.Now()})
	s.w.Input(input.PointerUp{Pos: geom.Pt(100, 60), Time: time.Now()})
	s.run(2)
	return s
}

// forms runs f for the wide bar and the compact one.
func forms(t *testing.T, f func(t *testing.T, compact bool)) {
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "wide", true: "compact"}[compact], func(t *testing.T) { f(t, compact) })
	}
}

// openFile opens File by the pointer: a click on its title, or on a compact bar a click on the button and the pointer
// onto File's line, resting there until its menu is in place.
func (s *subBar) openFile() {
	s.t.Helper()
	sp := s.b.span(0)
	clickAt(s.w, s.run, geom.Pt((sp[0]+sp[1])/2, 15))
	s.run(2)
	if s.b.Compact {
		s.b.listPopup.Input(input.PointerMove{Pos: s.b.list.RowRect(0).Center().Add(s.b.panel.off), Time: time.Now()})
	}
	s.run(40)
	if s.b.open != 0 || s.b.menu == nil {
		s.t.Fatalf("File did not open: menu %d is open", s.b.open)
	}
}

// openFileByKeys opens File by a click and has the keys work it, with nothing in it lit: on a compact bar, Down onto
// File's line and Right into its menu, which lights its first item.
func (s *subBar) openFileByKeys() {
	s.t.Helper()
	sp := s.b.span(0)
	clickAt(s.w, s.run, geom.Pt((sp[0]+sp[1])/2, 15))
	s.run(2)
	if s.b.Compact {
		typeKeys(s.w, s.run, 0, input.KeyDown, input.KeyRight)
		s.b.menu.Highlight(-1)
	}
	s.run(20)
	if s.b.open != 0 || s.b.menu == nil {
		s.t.Fatalf("File did not open: menu %d is open", s.b.open)
	}
}

// at returns the popup a bar's menu is in, and where row i of it is in that popup's window.
func (s *subBar) at(i int) (*gunim.Popup, geom.Point) {
	r := s.b.menu.RowRect(i)
	p := geom.Pt(r.Min.X+20, r.Center().Y)
	if s.b.Compact {
		return s.b.listPopup, p.Add(s.b.panel.sideCard().Min)
	}
	return s.b.popup, p
}

// point moves the pointer onto row i of the bar's menu.
func (s *subBar) point(i int) {
	pp, p := s.at(i)
	pp.Input(input.PointerMove{Pos: p, Time: time.Now()})
	s.run(1)
}

// clickRow clicks row i of the bar's menu.
func (s *subBar) clickRow(i int) {
	pp, p := s.at(i)
	pp.Input(input.PointerMove{Pos: p, Time: time.Now()})
	pp.Input(input.PointerDown{Pos: p, Clicks: 1, Time: time.Now()})
	pp.Input(input.PointerUp{Pos: p, Time: time.Now()})
	s.run(1)
}

// clickSub clicks row j of submenu sm.
func (s *subBar) clickSub(sm *subMenu, j int) {
	r := sm.menu.RowRect(j)
	p := geom.Pt(r.Min.X+20, r.Center().Y)
	sm.popup.Input(input.PointerMove{Pos: p, Time: time.Now()})
	sm.popup.Input(input.PointerDown{Pos: p, Clicks: 1, Time: time.Now()})
	sm.popup.Input(input.PointerUp{Pos: p, Time: time.Now()})
	s.run(1)
}

func (s *subBar) keys(keys ...input.Key) { typeKeys(s.w, s.run, 0, keys...) }

// letter types the letter r, as a key of its own.
func (s *subBar) letter(r rune, mods input.Mods) {
	s.w.Input(input.KeyPress{Key: input.KeyA + input.Key(r-'a'), Mods: mods, Char: r})
	s.run(1)
}

// closed checks the bar closed all its menus and gave the keyboard back to the field.
func (s *subBar) closed(subs ...*subMenu) {
	s.t.Helper()
	if s.b.IsOpen() || s.b.menu != nil {
		s.t.Fatal("the bar's menus stayed open")
	}
	for _, sm := range subs {
		if sm.popup.Open() {
			s.t.Fatal("a submenu stayed open")
		}
	}
	s.run(30)
	s.w.Input(input.TextInput{Text: "hi"})
	s.run(1)
	if got := s.field.Text(); got != "hi" {
		s.t.Fatalf("after the menus closed, typing reached %q, want the field", got)
	}
}

func TestABarMenusSubmenuOpensOnceThePointerRests(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.point(1)
		s.run(5)
		if s.b.menu.subOpen() >= 0 {
			t.Fatal("the submenu opened before the pointer rested")
		}
		s.run(20)
		if s.b.menu.subOpen() != 1 {
			t.Fatal("the submenu did not open as the pointer rested on Open recent")
		}
		if got := labelsOf(s.b.menu.sub.menu.Items()); !slices.Equal(got, []string{"&a.txt", "&b.txt", "&More", "&Gone"}) {
			t.Fatalf("the submenu has %v", got)
		}
		// Resting on another item closes it again, and the bar's menu stays.
		s.point(2)
		s.run(30)
		if s.b.menu.subOpen() >= 0 || s.b.open != 0 {
			t.Fatalf("on Quit, submenu %d is open, and menu %d", s.b.menu.subOpen(), s.b.open)
		}
	})
}

func TestABarMenusSubmenuItemIsNotPickedByAClick(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.clickRow(1)
		if s.b.menu == nil || s.b.menu.subOpen() != 1 || !s.b.IsOpen() {
			t.Fatal("a click on Open recent did not open its submenu at once, with the menu open")
		}
		if len(s.picks) != 0 || len(s.subs) != 0 {
			t.Fatalf("a click on Open recent picked %v and %v", s.picks, s.subs)
		}
	})
}

func TestAClickInABarMenusSubmenuPicksByItsPathAndClosesTheMenus(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.clickRow(1)
		s.run(20)
		sm := s.b.menu.sub
		s.clickSub(sm, 1)
		if want := []barSubPick{{0, "[1 1]"}}; !slices.Equal(s.subs, want) || len(s.picks) != 0 {
			t.Fatalf("a click on b.txt sent %v and %v, want %v", s.subs, s.picks, want)
		}
		s.closed(sm)
	})
}

func TestTheKeysOpenABarMenusSubmenuAndPickInIt(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		for _, open := range []input.Key{input.KeyEnter, input.KeyRight} {
			s := newSubBar(t, compact)
			s.openFileByKeys()
			s.keys(input.KeyDown, input.KeyDown, open)
			sm := s.b.menu.sub
			if sm == nil || sm.item != 1 || !sm.keys || sm.menu.Highlighted() != 0 {
				t.Fatalf("%v on Open recent did not open its submenu with the keys on a.txt", open)
			}
			if s.b.open != 0 {
				t.Fatalf("%v on Open recent opened menu %d", open, s.b.open)
			}
			s.keys(input.KeyDown, input.KeyEnter)
			if want := []barSubPick{{0, "[1 1]"}}; !slices.Equal(s.subs, want) || len(s.picks) != 0 {
				t.Fatalf("Enter on b.txt sent %v and %v, want %v", s.subs, s.picks, want)
			}
			s.closed(sm)
		}
	})
}

func TestLeftAndEscapeComeOutOfABarMenusSubmenusOneAtATime(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		for _, out := range []input.Key{input.KeyLeft, input.KeyEscape} {
			s := newSubBar(t, compact)
			s.openFileByKeys()
			// Into Open recent, then More, whose c.txt opens nothing: Right stays on it, in File.
			s.keys(input.KeyDown, input.KeyDown, input.KeyRight, input.KeyDown, input.KeyDown, input.KeyRight, input.KeyRight)
			sm := s.b.menu.sub
			if sm == nil || sm.menu.sub == nil || !sm.menu.sub.keys || sm.menu.sub.menu.Highlighted() != 0 {
				t.Fatal("Right on More did not open its submenu with the keys on c.txt")
			}
			if s.b.open != 0 || (compact && !s.b.inMenu) {
				t.Fatalf("Right in a submenu moved to menu %d, the keys in it %v", s.b.open, s.b.inMenu)
			}
			more := sm.menu.sub
			s.keys(out)
			if sm.menu.sub != nil || more.popup.Open() || !sm.keys || s.b.menu.sub != sm {
				t.Fatalf("%v in More did not close it alone, back to Open recent", out)
			}
			if sm.menu.Highlighted() != 2 {
				t.Fatalf("%v in More left Open recent's highlight on %d, want More", out, sm.menu.Highlighted())
			}
			s.keys(out)
			if s.b.menu.sub != nil || sm.popup.Open() || s.b.open != 0 || s.b.menu.Highlighted() != 1 {
				t.Fatalf("%v in Open recent did not close it alone, back to File: menu %d open", out, s.b.open)
			}
			if compact && !s.b.inMenu {
				t.Fatalf("%v in Open recent took the keys back to the list", out)
			}
			s.keys(out)
			switch {
			case out == input.KeyEscape:
				// The bar was opened by a click: Escape closes it.
				s.closed()
			case compact:
				if s.b.open != 0 || s.b.inMenu {
					t.Fatalf("Left in File left menu %d open, the keys in it %v; want them on the list", s.b.open, s.b.inMenu)
				}
			default:
				if s.b.open != 2 {
					t.Fatalf("Left in File opened menu %d, want View", s.b.open)
				}
			}
		}
	})
}

func TestLeftClosesABarMenusSubmenuOpenedByThePointer(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.point(1)
		s.run(30)
		sm := s.b.menu.sub
		if sm == nil || sm.keys {
			t.Fatal("the submenu did not open by the pointer, with the keys left on File")
		}
		s.keys(input.KeyRight)
		if s.b.open != 0 || s.b.menu.sub != sm || !sm.keys {
			t.Fatalf("Right on Open recent moved to menu %d, the keys into its submenu %v", s.b.open, sm.keys)
		}
		s.keys(input.KeyLeft)
		if s.b.open != 0 || s.b.menu.sub != nil || sm.popup.Open() {
			t.Fatalf("Left in the submenu left menu %d open, the submenu open %v", s.b.open, s.b.menu.sub != nil)
		}
	})
}

func TestAPickInASubmenuOfABarMenusSubmenuHasTheWholePath(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFileByKeys()
		s.keys(input.KeyDown, input.KeyDown, input.KeyEnter, input.KeyDown, input.KeyDown, input.KeyEnter, input.KeyDown)
		sm := s.b.menu.sub
		more := sm.menu.sub
		// Gone is disabled: Down from More passes it by, round to a.txt.
		if more == nil || more.menu.Highlighted() != 1 {
			t.Fatal("Enter on More did not open its submenu, the keys on d.txt after Down")
		}
		s.keys(input.KeyEnter)
		if want := []barSubPick{{0, "[1 2 1]"}}; !slices.Equal(s.subs, want) {
			t.Fatalf("Enter on d.txt sent %v, want %v", s.subs, want)
		}
		s.closed(sm, more)
	})
}

func TestAccessKeysWorkInABarMenusSubmenus(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.letter('f', input.ModAlt)
		s.run(20)
		if s.b.open != 0 {
			t.Fatalf("Alt+F opened menu %d", s.b.open)
		}
		s.letter('r', 0)
		sm := s.b.menu.sub
		if sm == nil || sm.item != 1 || !sm.keys {
			t.Fatal("R did not open Open recent's submenu with the keys in it")
		}
		if len(s.picks) != 0 {
			t.Fatalf("R picked %v", s.picks)
		}
		if !sm.menu.cues {
			t.Fatal("a submenu of a bar opened by the keys does not underline its access keys")
		}
		s.letter('m', 0)
		s.letter('c', 0)
		if want := []barSubPick{{0, "[1 2 0]"}}; !slices.Equal(s.subs, want) {
			t.Fatalf("M then C sent %v, want %v", s.subs, want)
		}
		s.closed(sm)
	})
}

func TestMovingToAnotherMenuClosesABarMenusSubmenu(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.clickRow(1)
		s.run(20)
		menu, sm := s.b.menu, s.b.menu.sub
		if compact {
			s.b.listPopup.Input(input.PointerMove{Pos: s.b.list.RowRect(0).Center().Add(s.b.panel.off), Time: time.Now()})
			s.run(1)
			s.b.listPopup.Input(input.PointerMove{Pos: s.b.list.RowRect(1).Center().Add(s.b.panel.off), Time: time.Now()})
		} else {
			edit := s.b.span(1)
			s.w.Input(input.PointerMove{Pos: geom.Pt((edit[0]+edit[1])/2, 15), Time: time.Now()})
		}
		s.run(1)
		if s.b.open != 1 || s.b.menu == menu {
			t.Fatalf("the pointer onto Edit left menu %d open", s.b.open)
		}
		if sm.popup.Open() {
			t.Fatal("the pointer onto Edit left File's submenu open")
		}
		// It fades out with File's menu, frame by frame.
		was := menuShown(sm.menu)
		for f := range 30 {
			s.run(1)
			v := menuShown(sm.menu)
			if v > was || was-v > 0.5 {
				t.Fatalf("frame %d: the submenu went from %v to %v", f, was, v)
			}
			was = v
		}
		if was > 0.01 {
			t.Fatalf("the submenu is still at %v after 30 frames", was)
		}
	})
}

func TestAPressOutsideClosesABarMenuAndItsSubmenu(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.clickRow(1)
		s.run(20)
		sm := s.b.menu.sub
		// A press in the submenu is inside the bar's menus.
		r := sm.menu.RowRect(3)
		p := geom.Pt(r.Min.X+20, r.Center().Y)
		sm.popup.Input(input.PointerDown{Pos: p, Clicks: 1, Time: time.Now()})
		sm.popup.Input(input.PointerUp{Pos: p, Time: time.Now()})
		s.run(2)
		if !s.b.IsOpen() || !sm.popup.Open() || len(s.subs) != 0 {
			t.Fatalf("a press on the disabled Gone closed the menus %v, or picked %v", !s.b.IsOpen(), s.subs)
		}
		clickAt(s.w, s.run, geom.Pt(300, 300))
		s.run(2)
		if s.b.IsOpen() || sm.popup.Open() {
			t.Fatal("a press outside left the menus open")
		}
	})
}

// A submenu fades in as it opens, and out with the bar's menu as a pick in it closes them, on every frame: neither
// shows or goes at once.
func TestABarMenusSubmenuFadesInAndOutWithItsMenu(t *testing.T) {
	forms(t, func(t *testing.T, compact bool) {
		s := newSubBar(t, compact)
		s.openFile()
		s.clickRow(1)
		sm, menu := s.b.menu.sub, s.b.menu
		was, rising := menuShown(sm.menu), 0
		for f := range 30 {
			v := menuShown(sm.menu)
			if v < was || v-was > 0.5 {
				t.Fatalf("frame %d: the submenu opening went from %v to %v", f, was, v)
			}
			if v > was && v < 0.99 {
				rising++
			}
			was = v
			s.run(1)
		}
		if was < 0.99 || rising < 2 {
			t.Fatalf("the submenu came to %v, rising over %d frames", was, rising)
		}
		s.clickSub(sm, 0)
		if want := []barSubPick{{0, "[1 0]"}}; !slices.Equal(s.subs, want) {
			t.Fatalf("a click on a.txt sent %v, want %v", s.subs, want)
		}
		subWas, menuWas, both := float32(1), float32(1), 0
		for f := range 40 {
			sv, mv := menuShown(sm.menu), menuShown(menu)
			if sv > subWas || subWas-sv > 0.5 || mv > menuWas || menuWas-mv > 0.5 {
				t.Fatalf("frame %d: closing, the submenu went from %v to %v and the menu from %v to %v", f, subWas, sv, menuWas, mv)
			}
			if sv > 0.01 && sv < 0.99 && mv > 0.01 && mv < 0.99 {
				both++
			}
			subWas, menuWas = sv, mv
			s.run(1)
		}
		if subWas > 0.01 || menuWas > 0.01 || both < 2 {
			t.Fatalf("closing, the submenu came to %v and the menu to %v, fading together over %d frames", subWas, menuWas, both)
		}
	})
}

// A compact bar's menu opens beside the list, and its submenu beside the menu: on the right, or on the left where
// the screen ends, and moved up to stay on the screen.
func TestACompactBarMenusSubmenuOpensBesideItOnTheScreen(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		s := newSubBar(t, true)
		s.openFile()
		card := s.b.panel.sideCard()
		pw := s.b.listPopup.Offscreen()
		var bottom float32
		if narrow {
			// Room for the menu and 30 past it on the right, and the screen ending a row below Open recent.
			bottom = s.at1().Y + 2*s.b.menu.row
			pw.SetWorkArea(geom.Rect{Min: geom.Pt(-600, -400), Max: geom.Pt(card.Max.X+30, bottom)})
		}
		s.keys(input.KeyRight, input.KeyDown, input.KeyRight)
		s.run(20)
		sm := s.b.menu.sub
		if sm == nil {
			t.Fatal("Right on Open recent opened no submenu")
		}
		at := sm.popup.Offscreen().Anchor().Min
		c := sm.menu.card.Add(at)
		switch {
		case narrow && (c.Max.X < card.Min.X-1 || c.Max.X > card.Min.X+1):
			t.Fatalf("near the screen's right edge, the submenu's card is at %v, want against the menu's left edge, %v", c, card.Min.X)
		case !narrow && (c.Min.X < card.Max.X-1 || c.Min.X > card.Max.X+1):
			t.Fatalf("the submenu's card is at %v, want against the menu's right edge, %v", c, card.Max.X)
		case narrow && c.Max.Y > bottom+1:
			t.Fatalf("the submenu's card reaches down to %v, past the screen's bottom, %v", c.Max.Y, bottom)
		}
		if row := s.b.menu.RowRect(1).Add(card.Min); !narrow && (c.Min.Y+sm.menu.pad < row.Min.Y-1 || c.Min.Y+sm.menu.pad > row.Min.Y+1) {
			t.Fatalf("the submenu's first row is at %v, want level with Open recent, at %v", c.Min.Y+sm.menu.pad, row.Min.Y)
		}
	}
}

// at1 is where Open recent's row is in a compact bar's window.
func (s *subBar) at1() geom.Point {
	_, p := s.at(1)
	return p
}

// menuShown is how far menu m has faded in, as it is drawn.
func menuShown(m *Menu) float32 { return min(1, max(0, m.in.Value())) }
