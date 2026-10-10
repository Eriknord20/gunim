package widget

import (
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// subMenu is the submenu a [Menu] has open beside one of its items.
type subMenu struct {
	// item is the item whose submenu it is, and menu and popup the submenu and the popup it is in.
	item  int
	menu  *Menu
	popup *gunim.Popup
	// keys says the keys work the submenu rather than the menu it is beside.
	keys bool
	// anchor is the item's row the popup was last put beside, in the menu's space.
	anchor geom.Rect
}

// subDelay is how long the pointer rests on an item before its submenu opens, or before the submenu open closes for
// another item.
const subDelay = 300 * time.Millisecond

// subAimWait is how long a submenu stays open while the pointer crosses other items on its way there.
const subAimWait = 400 * time.Millisecond

// hasSub reports whether item i is enabled and opens a submenu.
func (m *Menu) hasSub(i int) bool { return m.enabled(i) && len(m.list.items[i].subItems()) > 0 }

// subOpen returns the item whose submenu is open, or -1.
func (m *Menu) subOpen() int {
	if m.sub == nil {
		return -1
	}
	return m.sub.item
}

// keyMenu returns the menu the keys work: this one, or the submenu open that has them, on down.
func (m *Menu) keyMenu() *Menu {
	for m.sub != nil && m.sub.keys {
		m = m.sub.menu
	}
	return m
}

// subKey closes a submenu for Left or Escape, and reports whether it did. Left closes the submenu the keys are in,
// and gives them back to the menu it is beside. Escape closes that one too, or first a submenu open beside the menu
// that has the keys.
func (m *Menu) subKey(k input.KeyPress) bool {
	if k.Key != input.KeyLeft && k.Key != input.KeyEscape {
		return false
	}
	km := m.keyMenu()
	switch {
	case k.Key == input.KeyEscape && km.sub != nil:
		km.closeSub()
	case km != m:
		km.parent.closeSub()
	default:
		return false
	}
	return true
}

// keyedAway closes the submenu open when the keys move the highlight off its item.
func (m *Menu) keyedAway() {
	m.stopWait()
	if m.sub != nil && m.sub.item != m.hot {
		m.closeSub()
	}
}

// openSub opens item i's submenu beside it, closing any other. With keys, the keys move to it, onto its first item.
func (m *Menu) openSub(i int, keys bool, u *gunim.UI) {
	m.stopWait()
	if m.leaving || !m.hasSub(i) {
		return
	}
	if m.sub == nil || m.sub.item != i {
		m.closeSub()
		sm := NewMenu(m.list.items[i].subItems())
		sm.parent, sm.side = m, driver.NoRoomLimit
		sm.AccessKeys, sm.cues = m.AccessKeys, m.cues
		sm.OnPick = func(j int, u *gunim.UI) gunim.Intent {
			m.pickSub([]int{i, j}, u)
			return nil
		}
		sm.OnPickSub = func(path []int, u *gunim.UI) gunim.Intent {
			m.pickSub(append([]int{i}, path...), u)
			return nil
		}
		if len(m.chosen) > 1 && m.chosen[0] == i {
			sm.chosen = m.chosen[1:]
			sm.Highlight(sm.chosen[0])
		}
		s := &subMenu{item: i, menu: sm, anchor: m.subAnchor(i)}
		s.popup = u.OpenPopup(m, sm, gunim.PopupOptions{Anchor: s.anchor, Max: geom.Sz(600, 480), Beside: true})
		m.sub = s
	}
	if keys {
		m.enterSub()
	}
	u.Invalidate()
}

// enterSub moves the keys to the submenu open, onto its first item where none is highlighted.
func (m *Menu) enterSub() {
	m.sub.keys = true
	if sm := m.sub.menu; sm.hot < 0 {
		sm.Highlight(sm.step(-1, 0, 1))
	}
}

// closeSub closes the submenu open, and any open from it.
func (m *Menu) closeSub() {
	s := m.sub
	if s == nil {
		return
	}
	m.sub = nil
	s.menu.leaving = true
	s.menu.stopWait()
	s.popup.Close()
}

// pickSub counts a pick in a submenu, at path, and runs OnPickSub.
func (m *Menu) pickSub(path []int, u *gunim.UI) {
	m.picks++
	if m.OnPickSub != nil {
		send(u, m, m.OnPickSub(path, u))
	}
}

// stopWait cancels a submenu waiting to open or close.
func (m *Menu) stopWait() {
	if m.wait != nil {
		m.wait()
		m.wait = nil
	}
}

// hover follows the pointer onto item i. Once it rests there, the item's submenu opens, and a submenu open for
// another item closes. While the pointer heads for the submenu open, crossing other items, it waits longer.
func (m *Menu) hover(i int, u *gunim.UI) {
	switch {
	case m.sub != nil:
		// The pointer back on the menu: the keys follow it here.
		m.sub.keys = false
		if m.sub.item == i {
			m.stopWait()
			return
		}
	case !m.hasSub(i):
		m.stopWait()
		return
	}
	aim := m.sub != nil && m.aimingSub()
	if m.wait != nil && m.waitFor == i && !aim {
		// Resting on the item it waits on: the wait runs on.
		return
	}
	m.stopWait()
	delay := subDelay
	if aim {
		delay = subAimWait
	}
	m.waitFor = i
	m.wait = u.After(delay, m.settle)
}

// settle opens the submenu of the item the pointer rested on, closing any other, once the wait is over.
func (m *Menu) settle(u *gunim.UI) {
	m.wait = nil
	if m.leaving || (m.sub != nil && m.sub.item == m.hot) {
		return
	}
	m.closeSub()
	if m.hasSub(m.hot) {
		m.openSub(m.hot, false, u)
	}
	u.Invalidate()
}

// pointerIn tells the menus this one is a submenu of that the pointer is on it: each keeps its submenu open, with its
// item highlighted, and the keys follow the pointer here.
func (m *Menu) pointerIn(u *gunim.UI) {
	for c, p := m, m.parent; p != nil; c, p = p, p.parent {
		if p.sub == nil || p.sub.menu != c {
			return
		}
		p.stopWait()
		p.sub.keys = true
		if was := p.hot; was != p.sub.item {
			p.Highlight(p.sub.item)
			p.told(was, u)
		}
	}
}

// aimingSub reports whether the pointer's last move on the menu headed for the submenu open.
func (m *Menu) aimingSub() bool {
	card, left := m.sub.menu.besideCard(m.sub.anchor)
	return headsFor(m.wasPointer, m.pointer, card, left)
}

// headsFor reports whether the pointer's move between the two points heads for card, which lies right of where it
// was, or left of it where left says: into the wedge from where it was to the card's near corners, and not yet past
// the card's near edge.
func headsFor(from, to geom.Point, card geom.Rect, left bool) bool {
	if card.Empty() {
		return false
	}
	edge, step := card.Min.X, to.X-from.X
	if left {
		edge, step = card.Max.X, from.X-to.X
	}
	span := edge - from.X
	if left {
		span = from.X - edge
	}
	if step <= 0 || step >= span {
		return false
	}
	// The pointer's slope stays within the wedge's, from where it was to the card's near corners
	slope := (to.Y - from.Y) / step
	return slope >= (card.Min.Y-from.Y)/span && slope <= (card.Max.Y-from.Y)/span
}

// subAnchor returns what item i's submenu opens beside, in the menu's space: the item's row across the card, from
// the room above it, so the submenu's first item is level with it.
func (m *Menu) subAnchor(i int) geom.Rect {
	r := m.RowRect(i)
	return geom.Rect{Min: geom.Pt(m.card.Min.X, r.Min.Y-m.pad), Max: geom.Pt(m.card.Max.X, r.Max.Y)}
}

// placeSub moves the submenu open beside its item, where the item has moved, as the rows scroll.
func (m *Menu) placeSub() {
	if s := m.sub; s != nil {
		if a := m.subAnchor(s.item); a != s.anchor {
			s.anchor = a
			s.popup.Move(a)
		}
	}
}

// newSubRows gives the submenu open its items as they are now in the menu's, with its highlight kept on the item of
// the same label, or closes it where its item opens none now.
func (m *Menu) newSubRows() {
	s := m.sub
	if s == nil {
		return
	}
	if !m.hasSub(s.item) {
		m.closeSub()
		return
	}
	sm, items := s.menu, m.list.items[s.item].subItems()
	if sameSlice(sm.list.items, items) {
		return
	}
	label := ""
	if sm.hot >= 0 && sm.hot < sm.len() {
		label = sm.list.items[sm.hot].Label
	}
	sm.SetItems(items)
	if j := slices.IndexFunc(items, func(it MenuItem) bool { return it.Label == label }); label != "" && j >= 0 {
		sm.Highlight(j)
	}
}

// fitBeside takes the room the screen leaves beside the parent's item, for a submenu, which can move up as far as the
// screen's top, and opens on whichever side has more room.
func (m *Menu) fitBeside(r driver.Room) {
	m.side = r
	if room := r.Above + r.Below; !math.IsInf(float64(room), 1) {
		m.room = room
	}
	if wide := max(r.Left, r.Right); !math.IsInf(float64(wide), 1) {
		m.wide = wide
	}
}

// besideCard returns where the submenu's card is, beside a in its parent's space, as the popup puts it, and whether
// it is on a's left: see [gunim.PopupOptions.Beside].
func (m *Menu) besideCard(a geom.Rect) (geom.Rect, bool) {
	r := m.side
	w, h := m.card.Size().W, m.card.Size().H
	left := w+m.margin > r.Right && r.Left > r.Right
	x := a.Max.X
	if left {
		x = a.Min.X - w
	}
	y := a.Min.Y
	if over := h + m.margin - (r.Below - 1); over > 0 {
		y = max(y-over, a.Min.Y-r.Above+m.margin)
	}
	return geom.Rc(x, y, w, h), left
}
