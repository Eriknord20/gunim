package widget

import (
	"image/color"
	"math"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Menu is a list of items to pick from, shown in a popup: the list a
// [Dropdown] opens and the menu a [ContextMenu] opens.
//
// A highlight glides to the item under the pointer, or to the one the
// arrow keys reach. The menu fades and unfolds from its top as it
// opens, and fades as it closes. Where the display server cannot blend
// windows, the menu fills its window and appears at once.
//
// A menu taller than the room it is given, or than the screen leaves
// it, scrolls: by the wheel, a finger, its bar, and the keys, which
// keep the highlight in view. It draws only the rows in view, so a
// list of many thousands costs about what a short one does a frame. A
// menu wider than its box or the screen cuts its items' text short.
//
// Its items are [MenuItem]s, given to [NewMenu] and changed with
// [Menu.SetItems]. The menu works out what it needs of them once, as they
// are set: its width, and where its lines and captions are.
//
// An item with a submenu shows a chevron at its right, and opens its
// submenu beside the menu once the pointer rests on it, or at once on a
// click, Enter or Right. Left and Escape close the submenu again. The
// submenu stays open while the pointer crosses other items on its way
// there. It opens on the menu's right, or on its left where the screen
// runs out, and moves up to stay on the screen.
type Menu struct {
	anim.Group

	// OnPick runs on the UI goroutine with the index of the item picked,
	// by a click, Enter, Space or its access key. It may act in the
	// window through u, as closing the popup the menu is in; a non-nil
	// result is sent to the application as the menu's intent.
	OnPick func(i int, u *gunim.UI) gunim.Intent
	// OnPickSub runs, in the same way, when an item of a submenu is
	// picked, with its path: the item of this menu whose submenu holds
	// it, then the item in that submenu, and on down for a submenu in a
	// submenu.
	OnPickSub func(path []int, u *gunim.UI) gunim.Intent
	// OnHighlight runs, in the same way, when the pointer or a key moves
	// the highlight, with the item it moved to, or -1.
	OnHighlight func(i int, u *gunim.UI) gunim.Intent
	// MinWidth is the narrowest the menu gets, so a drop-down's list is
	// at least as wide as the drop-down.
	MinWidth float32
	// AccessKeys has the items mark their access keys with a & before the letter, as "&Open", which is not drawn.
	// Without a mark an item's key is its first letter. Typing a key picks its item; see [Menu.Key].
	AccessKeys bool

	// cues underlines the access keys.
	cues bool

	// list is the items, with what the menu needs to know of them. fresh says they were set since the last layout.
	list  *menuList
	fresh bool

	hot int
	// picks counts the lines picked, so a highlight moved on the way to
	// a pick is not told of after it: the pick has closed the menu.
	picks int
	glide bool
	hotY  *anim.Float
	hotOn *anim.Float
	in    *anim.Float

	// shown holds the rows in view shaped, by item. Rows that leave the view are dropped.
	shown map[int]*menuRow
	// ell and smallEll are an ellipsis for an item's text and for a caption's, for text cut short.
	ell, smallEll shapedText
	// widest is the widest item's text with its hint, measured once for what widthOf names.
	widest  float32
	widthOf menuWidth
	// gut and iconSpace are the room before the items' text for ticks and for icons, from the last layout.
	gut, iconSpace float32
	// card is where the menu is drawn in its box, row and pad the row
	// height and the space around the rows, all from the last layout.
	card        geom.Rect
	margin      float32
	row, pad    float32
	transparent bool
	// bare draws only the highlight and the rows, fading, for a card drawn round them by something else: the menu
	// beside a compact menubar's list.
	bare bool
	// pointer and wasPointer are where the pointer last moved on the menu, and where it was the move before.
	pointer, wasPointer geom.Point
	// rest is where the pointer last was on the menu, and inside says it is on the menu now. follow has the highlight
	// follow the row under it while the wheel or the bar scrolls the rows.
	rest           geom.Point
	inside, follow bool
	// untold says the highlight followed the pointer in a layout, from toldFrom, and OnHighlight is yet to hear of it.
	untold   bool
	toldFrom int
	// scroll scrolls the rows when they are taller than the card, in the space whose top is the first row's.
	scroll scrolling
	// room and wide are how tall and how wide the screen lets the menu's window be, from FitPopup, or 0 where nothing
	// says.
	room, wide float32
	// cover lets the menu cover its anchor, as a context menu covers the point pressed: it slides up the screen to fit,
	// and keeps to the screen's whole height rather than the taller side of its anchor.
	cover bool

	// sub is the submenu open, or nil. parent is the menu this one is the submenu of, or nil, and side the room the
	// screen leaves beside the parent's item, from FitPopup.
	sub    *subMenu
	parent *Menu
	side   driver.Room
	// chosen is the path of a drop-down's chosen item, from this menu's items: the submenu of its first item opens with
	// the next highlighted.
	chosen []int
	// wait cancels a submenu waiting to open, or to close for another item, as the pointer rests; waitFor is the item
	// it waits on.
	wait    func()
	waitFor int
	// leaving says the menu is on its way out, and opens no submenu.
	leaving bool
}

// menuRow is an item's text and hint, shaped.
type menuRow struct{ label, hint shapedText }

// MenuItem is a line of a [Menu], and of the menus of a [Dropdown], a [MenuButton], a [ContextMenu] and a
// [BarMenu].
type MenuItem struct {
	// Label is the item's text. With [Menu.AccessKeys] it may mark its access key with a & before the letter.
	Label string
	// Hint shows at the right, such as the item's shortcut.
	Hint string
	// Icon shows before the label, in the label's colour.
	Icon *icon.Icon
	// Swatch shows a dot of a colour before the label, where there is no icon, as for a choice of calendars by
	// their colours. A colour with no alpha shows none.
	Swatch color.NRGBA
	// Checked puts a tick before the item.
	Checked bool
	// Disabled dims the item, and the pointer and the keys pass it by.
	Disabled bool
	// Caption makes the item a caption over the group below it: drawn small and dim, and passed by as a disabled
	// item is.
	Caption bool
	// Break draws a line above the item, grouping the menu. A line above the first item draws nothing.
	Break bool
	// Sub makes the item open a submenu of its items, beside the menu, in place of being picked. The item shows a
	// chevron at the right in place of a hint. A [Menu] and a [ContextMenu] open it; a pick in it runs their OnPickSub.
	Sub *Submenu
}

// Submenu is the items of a [MenuItem]'s submenu. It is held by pointer, so items stay comparable.
type Submenu struct {
	Items []MenuItem
}

// subItems returns the items of the item's submenu, or nil for an item with none.
func (it *MenuItem) subItems() []MenuItem {
	if it.Sub == nil {
		return nil
	}
	return it.Sub.Items
}

// Labels returns an item for each label, for a menu of plain lines.
func Labels(labels ...string) []MenuItem {
	items := make([]MenuItem, len(labels))
	for i, s := range labels {
		items[i].Label = s
	}
	return items
}

// menuList is a list of items with what a menu needs to know of them, worked out once as they are set: the lines
// and captions sorted for search, whether any item has a tick and any an icon, and the items that can be widest.
// A drop-down shares its list with the menu it opens.
type menuList struct {
	items []MenuItem
	// lines are the items a line goes above, past the first, and heads the captions, both in order.
	lines, heads []int
	ticks, icons bool
	// longest are the items with the most runes, and hinted those with a hint, which together hold the widest.
	longest, hinted []int
	// keys lists the items by their access keys, made on the first key typed.
	keys map[rune][]int
}

// newMenuList returns items with what a menu needs to know of them.
func newMenuList(items []MenuItem) *menuList {
	l := &menuList{items: items}
	// One pass: a long list is read from memory once.
	top := newRuneTop(menuMeasured)
	var ticks, icons bool
	for i := range items {
		it := &items[i]
		top.see(i, it.Label)
		// Most items are plain lines, with none of these.
		if it.Hint != "" || it.Icon != nil || it.Swatch != (color.NRGBA{}) || it.Checked || it.Caption || it.Break ||
			len(it.subItems()) > 0 {
			if it.Break && i > 0 {
				l.lines = append(l.lines, i)
			}
			if it.Caption {
				l.heads = append(l.heads, i)
			}
			if it.Hint != "" || len(it.subItems()) > 0 {
				l.hinted = append(l.hinted, i)
			}
			ticks = ticks || it.Checked
			icons = icons || it.Icon != nil || it.Swatch.A > 0
		}
	}
	l.ticks, l.icons, l.longest = ticks, icons, top.top
	return l
}

// menuWidth is what a list of items was measured from: the list, the face and size, whether the access keys' marks
// were left out, and whether the labels alone were, with no hints.
type menuWidth struct {
	list       *menuList
	face       *text.Face
	size       float32
	access     bool
	labelsOnly bool
}

// NewMenu returns a menu of items, with nothing highlighted.
func NewMenu(items []MenuItem) *Menu {
	m := &Menu{
		list:  newMenuList(items),
		fresh: true,
		hot:   -1,
		hotY:  anim.NewFloat(0),
		hotOn: anim.NewFloat(0),
		in:    anim.NewFloat(0),

		scroll: newScrolling(),
	}
	m.Add(m.hotY, m.hotOn, m.in, &m.scroll)
	return m
}

// Items returns the menu's items.
func (m *Menu) Items() []MenuItem { return m.list.items }

// SetItems makes items the menu's items. The menu keeps the slice: a change to it goes through SetItems again. The
// highlight stays on its item where that is still enabled, and the view and the highlight come back among the rows
// at once.
func (m *Menu) SetItems(items []MenuItem) { m.setList(newMenuList(items)) }

// setList makes l the menu's list.
func (m *Menu) setList(l *menuList) {
	if l != m.list {
		m.list, m.fresh = l, true
	}
}

// len returns how many items there are.
func (m *Menu) len() int { return len(m.list.items) }

func flag(list []bool, i int) bool { return i >= 0 && i < len(list) && list[i] }

// headOf returns the first element of s, or nil, so two slices of the same array and length compare equal.
func headOf[T any](s []T) *T {
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

// sameSlice reports whether a and b are the same elements of the same array.
func sameSlice[T any](a, b []T) bool { return len(a) == len(b) && headOf(a) == headOf(b) }

// isCaption reports whether item i is a caption.
func (m *Menu) isCaption(i int) bool { return i >= 0 && i < m.len() && m.list.items[i].Caption }

func (m *Menu) enabled(i int) bool {
	return i >= 0 && i < m.len() && !m.list.items[i].Disabled && !m.list.items[i].Caption
}

// step returns the enabled item from i on, going by dir, or from
// staying where it is when there is none.
func (m *Menu) step(from, i, dir int) int {
	for ; i >= 0 && i < m.len(); i += dir {
		if m.enabled(i) {
			return i
		}
	}
	return from
}

// around returns the next enabled item from the highlight, going dir, and round from one end to the other, as menus
// do on Windows. With nothing highlighted, Down starts at the first item and Up at the last.
func (m *Menu) around(dir int) int {
	n := m.len()
	at := m.hot
	if at < 0 && dir < 0 {
		at = n
	}
	for k := 1; k <= n; k++ {
		i := ((at+dir*k)%n + n) % n
		if m.enabled(i) {
			return i
		}
	}
	return m.hot
}

// Highlight moves the highlight to item i, gliding from where it was.
// A negative i, or a disabled item, takes it away.
func (m *Menu) Highlight(i int) {
	m.follow = false
	if i >= m.len() {
		i = m.len() - 1
	}
	if !m.enabled(i) {
		i = -1
	}
	m.hot = i
	if i < 0 {
		m.hotOn.Animate(0, anim.Snappy)
		return
	}
	m.hotOn.Animate(1, anim.Snappy)
	if !m.glide {
		// Before the first layout there is nowhere to glide from.
		return
	}
	m.hotY.Animate(m.rowTop(i), anim.Snappy)
	m.reveal(i)
}

// reveal scrolls just far enough to bring item i fully into view.
func (m *Menu) reveal(i int) {
	if !m.scroll.scrollable() || i < 0 || i >= m.len() {
		return
	}
	top, at := m.top(i), m.scroll.base()
	switch {
	case top < at:
		m.scroll.ScrollTo(top, Quick.Get(m.scroll.th))
	case top+m.row > at+m.scroll.viewport:
		m.scroll.ScrollTo(top+m.row-m.scroll.viewport, Quick.Get(m.scroll.th))
	}
}

// FitPopup implements [gunim.PopupFitter]: the menu keeps to the taller of the room below its anchor and above it,
// or, one that covers its anchor, to the screen's height, and scrolls what does not fit. It keeps to the screen's
// width, and cuts long items short.
func (m *Menu) FitPopup(r driver.Room) {
	m.room, m.wide = 0, 0
	if m.parent != nil {
		m.fitBeside(r)
		return
	}
	room := max(r.Below, r.Above)
	if m.cover {
		room = r.Below + r.Above
	}
	if !math.IsInf(float64(room), 1) {
		m.room = room
	} else if m.cover {
		// Nothing says where the screen ends: the menu keeps to a height most screens have.
		m.room = coverRoom
	}
	if wide := r.Left + r.Right; !math.IsInf(float64(wide), 1) {
		m.wide = wide
	}
}

// coverRoom is the height a menu that covers its anchor keeps to where nothing says where the screen ends.
const coverRoom = 480

// DragsTouch implements [gunim.TouchDragger]: a finger on the bar's thumb drags it, and anywhere else scrolls the
// rows.
func (m *Menu) DragsTouch() bool { return m.scroll.DragsTouch() }

// scrolled returns e with its position moved into the scroll's space, whose top is the first row's.
func (m *Menu) scrolled(e input.Event) input.Event {
	d := geom.Pt(0, m.card.Min.Y+m.pad)
	switch e := e.(type) {
	case input.PointerEnter:
		e.Pos = e.Pos.Sub(d)
		return e
	case input.PointerMove:
		e.Pos = e.Pos.Sub(d)
		return e
	case input.PointerDown:
		e.Pos = e.Pos.Sub(d)
		return e
	case input.PointerUp:
		e.Pos = e.Pos.Sub(d)
		return e
	case input.Scroll:
		e.Pos = e.Pos.Sub(d)
		return e
	}
	return e
}

// Highlighted returns the highlighted item, or -1.
func (m *Menu) Highlighted() int { return m.hot }

// Key moves the highlight with the arrow keys, Home and End, and picks
// the highlighted item with Enter or Space. With AccessKeys, a letter
// picks the item whose key it is, or moves the highlight among several.
// It is for the node that opened the menu, which keeps the keyboard, to
// pass keys on. It reports whether it used the key.
//
// With a submenu open, Right on an item with a submenu opens it and
// moves the keys there, Left closes it and brings them back, and Escape
// closes the submenu the keys are in, or one open beside it. The menu
// passes the other keys on to the submenu that has the keys.
func (m *Menu) Key(k input.KeyPress, u *gunim.UI) bool {
	if m.subKey(k) {
		return true
	}
	if km := m.keyMenu(); km != m {
		return km.Key(k, u)
	}
	defer m.toldUnlessPicked(m.hot, m.picks, u)
	switch k.Key {
	case input.KeyDown:
		m.Highlight(m.around(1))
		m.keyedAway()
	case input.KeyUp:
		m.Highlight(m.around(-1))
		m.keyedAway()
	case input.KeyHome:
		m.Highlight(m.step(m.hot, 0, 1))
		m.keyedAway()
	case input.KeyEnd:
		m.Highlight(m.step(m.hot, m.len()-1, -1))
		m.keyedAway()
	case input.KeyRight:
		if !m.hasSub(m.hot) {
			return false
		}
		m.openSub(m.hot, true, u)
	case input.KeyEnter, input.KeyKPEnter, input.KeySpace:
		switch {
		case m.hasSub(m.hot):
			m.openSub(m.hot, true, u)
		case m.enabled(m.hot) && m.OnPick != nil:
			m.pick(m.hot, u)
		}
	default:
		return m.AccessKeys && m.pickByKey(k, u)
	}
	return true
}

// pickByKey picks the item whose access key k types. With several such items it moves the highlight to the next.
func (m *Menu) pickByKey(k input.KeyPress, u *gunim.UI) bool {
	r := keyRune(k)
	if r == 0 || k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) {
		return false
	}
	hits := m.keyed(r)
	// The first two enabled items with the key after the highlight, going round
	var on []int
	from, _ := slices.BinarySearch(hits, m.hot+1)
	for n := range hits {
		if i := hits[(from+n)%len(hits)]; m.enabled(i) {
			if on = append(on, i); len(on) == 2 {
				break
			}
		}
	}
	if len(on) == 0 {
		return false
	}
	m.Highlight(on[0])
	m.keyedAway()
	switch {
	case len(on) > 1:
	case m.hasSub(on[0]):
		m.openSub(on[0], true, u)
	case m.OnPick != nil:
		m.pick(on[0], u)
	}
	return true
}

// keyed returns the items whose access key is r, in order, from a list of the items by their keys, made once for
// the items.
func (m *Menu) keyed(r rune) []int {
	l := m.list
	if l.keys == nil {
		l.keys = map[rune][]int{}
		for i := range l.items {
			if _, key, _ := accessKey(l.items[i].Label); key != 0 {
				l.keys[key] = append(l.keys[key], i)
			}
		}
	}
	return l.keys[r]
}

// label returns item i as it is drawn: without its access key's mark, with AccessKeys.
func (m *Menu) label(i int) string {
	if !m.AccessKeys {
		return m.list.items[i].Label
	}
	shown, _, _ := accessKey(m.list.items[i].Label)
	return shown
}

// PopupPadding implements [gunim.PopupPadder]: the room around the
// menu for its shadow.
func (m *Menu) PopupPadding() geom.Insets { return geom.Uniform(m.margin) }

// Transition implements [gunim.Transitioner].
func (m *Menu) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		m.in.Animate(1, Quick.Get(f.Theme))
	case gunim.Exiting:
		m.in.Animate(0, Quick.Get(f.Theme))
		m.leaving = true
		m.stopWait()
	case gunim.Present:
	}
	return !m.in.Active()
}

// Handle implements [gunim.Handler].
func (m *Menu) Handle(e input.Event, u *gunim.UI) bool {
	defer m.toldUnlessPicked(m.hot, m.picks, u)
	switch e := e.(type) {
	case input.PointerEnter:
		m.rest, m.inside = e.Pos, true
	case input.PointerMove:
		m.rest, m.inside = e.Pos, true
	case input.PointerLeave:
		m.inside, m.follow = false, false
	}
	if m.scroll.scrollable() {
		in := m.scrolled(e)
		if m.scroll.barEvent(in, u) {
			// The bar scrolls the rows, and the highlight follows the row under the pointer.
			m.follow = true
			return true
		}
		switch e := e.(type) {
		case input.Scroll:
			m.scroll.handle(in, u)
			m.follow = true
			return true
		case input.PointerUp:
			if m.scroll.ClaimsPointer(e.Pos) {
				// The end of a press on the bar's track, which paged.
				return true
			}
		}
	}
	switch e := e.(type) {
	case input.PointerEnter:
		// The pointer arriving without moving, as when the menu opens
		// under it, leaves the highlight where the keys put it. A move
		// follows any real arrival.
		m.pointerIn(u)
	case input.PointerMove:
		m.wasPointer, m.pointer = m.pointer, e.Pos
		m.pointerIn(u)
		if i := m.rowAt(e.Pos); i >= 0 {
			m.Highlight(i)
			m.hover(i, u)
		}
	case input.PointerDown:
	case input.PointerUp:
		switch i := m.rowAt(e.Pos); {
		case m.hasSub(i):
			m.openSub(i, false, u)
		case m.enabled(i) && m.OnPick != nil:
			m.pick(i, u)
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// followPointer moves the highlight to the row under the resting pointer, as the wheel or the bar scrolls the rows
// under it. The highlight lands on the row at once, as the rows move with it.
func (m *Menu) followPointer() {
	if !m.follow || !m.inside {
		return
	}
	i := m.rowAt(m.rest)
	if i < 0 {
		// Between two groups the highlight stays, as it does for a move.
		return
	}
	if !m.enabled(i) {
		i = -1
	}
	if i != m.hot {
		m.moveUntold(i)
	} else if i >= 0 {
		// A glide from a move before the scroll ends on the row, which the scroll carries on.
		m.hotY.Jump(m.rowTop(i))
	}
}

// moveUntold moves the highlight to i at once, from a layout, which has no UI to run OnHighlight with: the next
// event or key tells it.
func (m *Menu) moveUntold(i int) {
	if !m.untold {
		m.untold, m.toldFrom = true, m.hot
	}
	m.hot = i
	if i < 0 {
		m.hotOn.Animate(0, anim.Snappy)
		return
	}
	m.hotOn.Animate(1, anim.Snappy)
	m.hotY.Jump(m.rowTop(i))
}

// dismissed returns shut for a popup's Dismiss, with [gunim.CueClose]
// played first: the user sent the popup away without choosing in it.
func dismissed(n gunim.Node, shut func(*gunim.UI)) func(*gunim.UI) {
	return func(u *gunim.UI) {
		u.Cue(gunim.CueClose, n)
		shut(u)
	}
}

// pick picks item i, counting it.
func (m *Menu) pick(i int, u *gunim.UI) {
	m.picks++
	act(u, m, gunim.CuePress, m.OnPick, i)
}

// toldUnlessPicked runs OnHighlight when the highlight has moved from
// was, unless a line was picked since picks was counted: the owner is
// told of the pick, and whatever the pick closed is not asked about
// the highlight on the way to it. A highlight moved in a layout counts
// from where it was before that.
func (m *Menu) toldUnlessPicked(was, picks int, u *gunim.UI) {
	if m.untold {
		was, m.untold = m.toldFrom, false
	}
	if m.picks == picks {
		m.told(was, u)
	}
}

// told runs OnHighlight when the highlight has moved from was.
func (m *Menu) told(was int, u *gunim.UI) {
	if m.hot != was && m.OnHighlight != nil {
		send(u, m, m.OnHighlight(m.hot, u))
	}
}

// rowAt returns the item at p, in the menu's space, or -1.
func (m *Menu) rowAt(p geom.Point) int {
	if !m.card.Contains(p) || m.row <= 0 {
		return -1
	}
	y := p.Y - m.card.Min.Y - m.pad + m.scroll.Offset()
	if i := m.rowIn(y); i >= 0 && y < m.top(i)+m.row {
		return i
	}
	return -1
}

// rowIn returns the last item whose row starts at or above y, in the scroll's space, or -1.
func (m *Menu) rowIn(y float32) int {
	return sort.Search(m.len(), func(i int) bool { return m.top(i) > y }) - 1
}

// RowRect returns where item i is in the menu's space, from the last
// layout, as for a menu opening beside it.
func (m *Menu) RowRect(i int) geom.Rect {
	return geom.Rc(m.card.Min.X, m.rowY(i), m.card.Size().W, m.row)
}

// rowY returns the top of row i in the menu's space, where it is scrolled to now.
func (m *Menu) rowY(i int) float32 { return m.rowTop(i) - m.scroll.Offset() }

// rowTop returns the top of row i in the menu's space, as it is with the menu scrolled to its top.
func (m *Menu) rowTop(i int) float32 { return m.card.Min.Y + m.pad + m.top(i) }

// top returns the top of row i from the first row's: a row's height for each row above it, and a line's room for
// each break above it.
func (m *Menu) top(i int) float32 {
	breaks, _ := slices.BinarySearch(m.list.lines, i+1)
	return float32(i)*m.row + float32(breaks)*menuBreak
}

// content returns the height of the rows together.
func (m *Menu) content() float32 {
	if m.len() == 0 {
		return 0
	}
	return m.top(m.len()-1) + m.row
}

// menuBreak is the room a line between groups takes.
const menuBreak = 9

// menuTick is the room a tick takes before the items, when any has one.
const menuTick = 18

// menuHintGap is the room between an item's text and its hint.
const menuHintGap = 32

// menuSubRoom is the room after an item's text for the chevron of its submenu.
const menuSubRoom = 28

// measure returns the width of the widest item's text with its hint, in face at size. It measures the items again
// only when they, the face or the size change.
func (m *Menu) measure(face *text.Face, size float32) float32 {
	l := m.list
	key := menuWidth{list: l, face: face, size: size, access: m.AccessKeys, labelsOnly: len(l.hinted) == 0}
	if key == m.widthOf {
		return m.widest
	}
	w := float32(0)
	measure := func(i int) {
		line := face.Shape(m.label(i), size).Advance
		if len(l.items[i].subItems()) > 0 {
			line += menuSubRoom
		} else if h := l.items[i].Hint; h != "" {
			line += menuHintGap + face.Shape(h, size*0.9).Advance
		}
		w = max(w, line)
	}
	// Only the items that can be widest are shaped: the longest by
	// runes, and each with a hint, whose width the hint adds to.
	for _, i := range l.longest {
		measure(i)
	}
	for _, i := range l.hinted {
		measure(i)
	}
	m.widest, m.widthOf = w, key
	return w
}

// menuMeasured is how many of the longest items a menu or a drop-down shapes to find the widest: in a face of one script,
// the widest is among the longest by runes, by far.
const menuMeasured = 64

// longest returns the indices of the up to n of words with the most runes, so a long list is measured by shaping a
// few.
func longest(words []string, n int) []int {
	k := newRuneTop(n)
	for i, s := range words {
		k.see(i, s)
	}
	return k.top
}

// runeTop keeps the indices of the up to n strings it sees with the most runes, the most first.
type runeTop struct {
	n          int
	top, runes []int
	// floor is the fewest runes kept, once n are kept, and -1 before.
	floor int
}

func newRuneTop(n int) runeTop {
	return runeTop{n: n, top: make([]int, 0, n), runes: make([]int, 0, n), floor: -1}
}

// see shows the keeper string i, s. A string of no more bytes than the shortest kept has no more runes, so it is
// passed by unread.
func (k *runeTop) see(i int, s string) {
	if len(s) > k.floor {
		k.add(i, s)
	}
}

// add keeps string i, s, where it has more runes than the shortest kept.
func (k *runeTop) add(i int, s string) {
	r := utf8.RuneCountInString(s)
	if r <= k.floor {
		return
	}
	at := sort.Search(len(k.runes), func(j int) bool { return k.runes[j] < r })
	if len(k.top) < k.n {
		k.top, k.runes = append(k.top, 0), append(k.runes, 0)
	}
	copy(k.top[at+1:], k.top[at:])
	copy(k.runes[at+1:], k.runes[at:])
	k.top[at], k.runes[at] = i, r
	if len(k.top) == k.n {
		k.floor = k.runes[k.n-1]
	}
}

// Layout implements [gunim.Node].
func (m *Menu) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	m.transparent = f.Transparent
	m.margin = 0
	if f.Transparent && !m.bare {
		m.margin = MenuMargin.Get(th)
	}
	m.row, m.pad = MenuRowHeight.Get(th), MenuPadding.Get(th)
	m.gut, m.iconSpace = m.gutter(), m.iconRoom(th)
	w := max(m.MinWidth, m.gut+m.iconSpace+m.measure(faceIn(Font, th), TextSize.Get(th))+2*MenuRowPadding.Get(th))
	y := m.content()
	h := y + 2*m.pad
	limit := c.Max.H
	if m.room > 0 {
		room := m.room
		if m.cover {
			// The room counts the margin once, round the point covered; the window, margin and all, keeps to the screen.
			room -= 2 * m.margin
		}
		limit = min(limit, room)
	}
	if limit -= 2 * m.margin; limit > 0 && h > limit {
		// Too tall: the rows scroll, and the menu leaves room at the right for the bar.
		h = max(limit, m.row+2*m.pad)
		w += ScrollbarGrabWidth.Get(th)
	}
	// Too wide for its box or the screen, the menu keeps to them, and cuts its items' text short.
	wide := c.Max.W
	if m.wide > 0 {
		wide = min(wide, m.wide)
	}
	if wide -= 2 * m.margin; wide > 0 {
		w = min(w, wide)
	}
	m.card = geom.Rc(m.margin, m.margin, w, h)
	m.scroll.fit(y, geom.Sz(m.card.Max.X, h-2*m.pad), th)
	if m.fresh {
		m.newRows()
	}
	if m.hot >= 0 && !m.glide {
		m.hotY.Jump(m.rowTop(m.hot))
		// The menu opens with the highlighted row in its middle.
		m.scroll.jumpTo(m.scroll.clamp(m.top(m.hot) - (m.scroll.viewport-m.row)/2))
	}
	m.glide = true
	m.followPointer()
	m.placeSub()
	return c.Constrain(geom.Sz(w+2*m.margin, h+2*m.margin))
}

// newRows takes new items. The highlight goes to the nearest enabled item at or above it, and the view back among
// the rows, both at once: the new rows show where they are, and never slide in from where the old were.
func (m *Menu) newRows() {
	m.fresh = false
	if m.hot >= 0 && !m.enabled(m.hot) {
		m.moveUntold(m.step(-1, min(m.hot, m.len()-1), -1))
	}
	m.newSubRows()
	if !m.glide {
		return
	}
	m.scroll.jumpTo(m.scroll.clamp(m.scroll.target))
	if m.hot >= 0 {
		m.hotY.Jump(m.rowTop(m.hot))
	}
}

// inView returns the first item whose row the card shows, and the item after the last.
func (m *Menu) inView() (from, to int) {
	at := m.scroll.Offset() - m.pad
	from = max(0, m.rowIn(at))
	to = min(m.len(), m.rowIn(at+m.card.Size().H)+1)
	return from, to
}

// rowText returns item i's shaped text, kept while its row is in view.
func (m *Menu) rowText(i int) *menuRow {
	r := m.shown[i]
	if r == nil {
		if m.shown == nil {
			m.shown = map[int]*menuRow{}
		}
		r = &menuRow{}
		m.shown[i] = r
	}
	return r
}

// Paint implements [gunim.Node].
func (m *Menu) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, _ gunim.Children) {
	th := f.Theme
	card := m.card
	radius := MenuRadius.Get(th)
	t := m.in.Value()
	switch {
	case m.bare:
		defer p.Layer(paint.LayerOpts{Bounds: card, Opacity: min(1, max(0, t))})()
		if !m.transparent {
			radius = 0
		}
	case m.transparent:
		// Fade in and unfold from the top edge, which is the edge that
		// meets the anchor when the menu opens below it.
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: card.Max.Add(geom.Pt(m.margin, m.margin))}, Opacity: min(1, max(0, t))})()
		top := geom.Pt(card.Center().X, card.Min.Y)
		defer p.Push(paint.Scale(0.96+0.04*t, top))()
		shadow := MenuShadow.Get(th)
		p.ShadowRRect(card, radius, paint.Solid(MenuFill.Get(th)), paint.Shadow{
			Offset: geom.Pt(0, 3),
			Blur:   m.margin * 0.6,
			Color:  shadow,
		})
		p.RRectStroke(card, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: MenuBorder.Get(th)})
	default:
		radius = 0
		p.RRect(card, 0, paint.Solid(MenuFill.Get(th)))
		p.RRectStroke(card, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: MenuBorder.Get(th)})
	}

	bar := float32(0)
	if m.scroll.scrollable() {
		// The rows fade where more of them lie past the card's edge.
		defer m.paintBar(p, f)
		defer m.scroll.layer(p, card, th)()
		bar = ScrollbarGrabWidth.Get(th)
	}
	if on := m.hotOn.Value(); on > 0.01 {
		c := MenuHot.Get(th)
		c.A = uint8(float32(c.A) * min(on, 1))
		inset := m.pad
		y := m.hotY.Value() - m.scroll.Offset()
		p.RRect(geom.Rc(card.Min.X+inset, y, card.Size().W-2*inset, m.row), max(0, radius-inset), paint.Solid(c))
	}
	ink := Ink.Get(th)
	dim := ink
	dim.A /= 3
	hint := MenuHint.Get(th)
	dimHint := hint
	dimHint.A /= 3
	pad := MenuRowPadding.Get(th)
	face, size := faceIn(Font, th), TextSize.Get(th)
	from, to := m.inView()
	at, _ := slices.BinarySearch(m.list.lines, from)
	for _, i := range m.list.lines[at:] {
		if i > to || i >= m.len() {
			break
		}
		y := m.rowY(i) - menuBreak/2
		p.RRect(geom.Rc(card.Min.X+pad, y, card.Size().W-2*pad, 1), 0, paint.Solid(MenuBorder.Get(th)))
	}
	// The room for an item's text, less its hint's
	room := card.Size().W - 2*pad - m.gut - m.iconSpace - bar
	for i := from; i < to; i++ {
		r := m.rowText(i)
		col := ink
		if !m.enabled(i) {
			col = dim
		}
		if m.isCaption(i) {
			small := size * 0.85
			caption := r.label.shape(face, m.label(i), small)
			if fits := room + m.gut + m.iconSpace; caption.Advance > fits {
				caption = cutRunWith(caption, m.smallEll.shape(face, "…", small), fits)
			}
			caption.Paint(p, geom.Pt(card.Min.X+pad, m.rowY(i)+(m.row-caption.Height())/2), hint)
			continue
		}
		it := &m.list.items[i]
		if it.Checked {
			s := IconSize.Get(th)
			paintIcon(p, th, icon.Check, geom.Rc(card.Min.X+pad+m.gut/2-1-s/2, m.rowY(i)+(m.row-s)/2, s, s), col, 1)
		}
		x := card.Min.X + pad + m.gut
		if it.Icon != nil {
			s := IconSize.Get(th)
			paintIcon(p, th, it.Icon, geom.Rc(x, m.rowY(i)+(m.row-s)/2, s, s), col, 1)
		} else if it.Swatch.A > 0 {
			paintSwatch(p, th, it.Swatch, geom.Pt(x, m.rowY(i)+m.row/2))
		}
		fits := room
		if len(it.subItems()) > 0 {
			c := hint
			if !m.enabled(i) {
				c = dimHint
			}
			s := IconSize.Get(th)
			paintIcon(p, th, icon.ChevronRight, geom.Rc(card.Max.X-pad-bar-s+4, m.rowY(i)+(m.row-s)/2, s, s), c, 1)
			fits -= menuSubRoom
		} else if it.Hint != "" {
			h := r.hint.shape(face, it.Hint, size*0.9)
			c := hint
			if !m.enabled(i) {
				c = dimHint
			}
			h.Paint(p, geom.Pt(card.Max.X-pad-bar-h.Advance, m.rowY(i)+(m.row-h.Height())/2), c)
			fits -= menuHintGap + h.Advance
		}
		full := r.label.shape(face, m.label(i), size)
		run := full
		if run.Advance > fits {
			run = cutRunWith(full, m.ell.shape(face, "…", size), fits)
		}
		y := m.rowY(i) + (m.row-run.Height())/2
		run.Paint(p, geom.Pt(x+m.iconSpace, y), col)
		if m.cues && m.AccessKeys {
			// The key's line, where the key is in the part of the text shown
			_, _, key := accessKey(m.list.items[i].Label)
			if key >= 0 && (run.Advance == full.Advance || full.CaretX(key+1) <= run.Advance-m.ell.run.Advance) {
				underline(p, full, key, geom.Pt(x+m.iconSpace, y), col)
			}
		}
	}
	// The rows that have left the view let their text go
	for i := range m.shown {
		if i < from || i >= to {
			delete(m.shown, i)
		}
	}
}

// paintBar draws the scroll bar down the card's right edge.
func (m *Menu) paintBar(p *paint.Painter, f gunim.Frame) {
	defer p.Push(paint.Translate(geom.Pt(0, m.card.Min.Y+m.pad)))()
	m.scroll.paintBar(p, f)
}

// iconRoom is the room icons take before the items' text, when any item has one or a swatch.
func (m *Menu) iconRoom(th *theme.Live) float32 { return m.list.iconRoom(th) }

// iconRoom is the room icons take before the items' text, when any item has one or a swatch.
func (l *menuList) iconRoom(th *theme.Live) float32 {
	if l.icons {
		return IconSize.Get(th) + IconGap.Get(th)
	}
	return 0
}

// paintSwatch draws a dot of c in the room of an icon whose left edge's middle is at.
func paintSwatch(p *paint.Painter, th *theme.Live, c color.NRGBA, at geom.Point) {
	s := IconSize.Get(th)
	d := s * 0.62
	p.RRect(geom.Rc(at.X+(s-d)/2, at.Y-d/2, d, d), d/2, paint.Solid(c))
}

// gutter is the room before the items' titles: a tick's, when any item has one.
func (m *Menu) gutter() float32 {
	if m.list.ticks {
		return menuTick
	}
	return 0
}

// Dropdown shows one item of a list, and opens the list in a popup to
// pick another. The list opens below the drop-down, or above it where
// the screen runs out, with the chosen item highlighted.
//
// An item with a [MenuItem.Sub] opens its submenu beside the list, and
// an item picked there becomes the one chosen, as a song in a list of
// categories. The chosen item is then a path, as [Menu.OnPickSub] gives
// one: [Dropdown.SelectedPath] reads it and [Dropdown.OnChangeSub] hears
// it change. Its submenu opens with it highlighted.
//
// With focus, Space, Enter and the arrow keys open the list; the arrow
// keys, Home and End move through it; Enter or Space picks; Escape and
// Tab close it.
type Dropdown struct {
	Control

	// selected is the chosen item's path: an item of the list, then of its submenu, and on down. It is never empty.
	selected []int
	// Label names the drop-down for a screen reader, as the label
	// beside it does on screen.
	Label string
	// MaxWidth caps the drop-down's width, cutting a longer item short with
	// an ellipsis. Zero leaves it as wide as its longest item.
	MaxWidth float32
	// OnChange runs on the UI goroutine when the user picks an item other
	// than the chosen one, with the new choice. It may act in the window
	// through u, such as filling in a form from a saved entry; a non-nil
	// result is sent to the application as the drop-down's intent.
	OnChange func(i int, u *gunim.UI) gunim.Intent
	// OnChangeSub runs in the same way when the user picks an item of a
	// submenu other than the chosen one, with its path; see
	// [Menu.OnPickSub].
	OnChangeSub func(path []int, u *gunim.UI) gunim.Intent

	turn *anim.Float

	popup *gunim.Popup
	menu  *Menu
	size  geom.Size
	shown shapedText
	ell   shapedText
	// list is the choices, which the drop-down's menu shares.
	list *menuList
	// widest is the widest item's width, measured once for what widthOf names, and icons the room for the icons.
	widest    float32
	widthOf   menuWidth
	iconSpace float32
}

// NewDropdown returns a drop-down of items with the first chosen. An item's icon or swatch shows before it in the
// list, and before the chosen one on the drop-down itself.
func NewDropdown(items []MenuItem) *Dropdown {
	d := &Dropdown{
		Control:  newControl(),
		selected: []int{0},
		list:     newMenuList(items),
		turn:     anim.NewFloat(0),
	}
	d.Add(d.turn)
	return d
}

// Items returns the choices.
func (d *Dropdown) Items() []MenuItem { return d.list.items }

// SetItems makes items the choices. The drop-down keeps the slice: a change to it goes through SetItems again. The
// list open shows them at once.
func (d *Dropdown) SetItems(items []MenuItem) { d.list = newMenuList(items) }

// Selected returns the chosen item, or for an item of a submenu, the item of the list whose submenu holds it.
func (d *Dropdown) Selected() int { return d.selected[0] }

// SelectedPath returns the chosen item's path: the item of the list, then the item of its submenu, and on down. The
// drop-down keeps the slice; change a copy.
func (d *Dropdown) SelectedPath() []int { return d.selected }

// SetSelected chooses item i and sends no intent. The drop-down shows it at once, and the list open glides its
// highlight there; before the first layout, or with a nil u, the highlight jumps.
func (d *Dropdown) SetSelected(i int, u *gunim.UI) { d.SetSelectedPath([]int{i}, u) }

// SetSelectedPath chooses the item at path, as [Dropdown.SelectedPath] gives it, and sends no intent. It does as
// [Dropdown.SetSelected] does, and an empty path chooses the first item.
func (d *Dropdown) SetSelectedPath(path []int, u *gunim.UI) {
	if len(path) == 0 {
		path = []int{0}
	}
	if slices.Equal(path, d.selected) {
		return
	}
	d.selected = slices.Clone(path)
	if d.menu == nil || !d.IsOpen() {
		return
	}
	m := d.menu
	m.chosen = d.selected
	m.Highlight(d.selected[0])
	if u == nil {
		m.hotY.Jump(m.hotY.Target())
		m.hotOn.Jump(m.hotOn.Target())
		return
	}
	u.Invalidate()
}

// IsOpen reports whether the list is open.
func (d *Dropdown) IsOpen() bool { return d.popup != nil && d.popup.Open() }

// Handle implements [gunim.Handler].
func (d *Dropdown) Handle(e input.Event, u *gunim.UI) bool {
	d.showTip(e, u, d)
	th := u.Theme()
	if d.Disabled {
		return d.handleDisabled(e, u, d.close)
	}
	switch e := e.(type) {
	case input.FocusLost:
		d.ring.Animate(0, RingFade.Get(th))
		d.close(u)
	case input.PointerEnter:
		d.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		d.hover.Animate(0, Settle.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if d.IsOpen() {
			d.close(u)
		} else {
			d.open(u)
		}
	case input.PointerUp:
	case input.FocusRing:
		d.ring.Animate(ringTo(e), RingFade.Get(th))
	case input.KeyPress:
		return d.key(e, u)
	default:
		return false
	}
	return true
}

func (d *Dropdown) key(k input.KeyPress, u *gunim.UI) bool {
	if !d.IsOpen() {
		switch k.Key {
		case input.KeySpace, input.KeyEnter, input.KeyKPEnter, input.KeyDown, input.KeyUp:
			d.open(u)
			return true
		default:
			return false
		}
	}
	switch k.Key {
	case input.KeyEscape:
		u.Cue(gunim.CueClose, d)
		d.close(u)
		return true
	case input.KeyTab:
		d.close(u)
		return false // focus moves on
	default:
		return d.menu.Key(k, u)
	}
}

// listMenu returns the menu of the drop-down's items, with the chosen one highlighted.
func (d *Dropdown) listMenu() *Menu {
	m := NewMenu(nil)
	d.sync(m)
	m.chosen = d.selected
	m.Highlight(d.selected[0])
	return m
}

// chosen returns the chosen item, or nil where its path is past the items.
func (d *Dropdown) chosen() *MenuItem {
	items := d.list.items
	for n, i := range d.selected {
		if i < 0 || i >= len(items) {
			return nil
		}
		if n == len(d.selected)-1 {
			return &items[i]
		}
		items = items[i].subItems()
	}
	return nil
}

// sync gives the menu the drop-down's items as they are now, with their width as the drop-down measured it.
func (d *Dropdown) sync(m *Menu) {
	m.setList(d.list)
	m.MinWidth = d.size.W
	if m.widthOf != d.widthOf && d.widthOf.face != nil {
		m.widest, m.widthOf = d.widest, d.widthOf
	}
}

func (d *Dropdown) open(u *gunim.UI) {
	m := d.listMenu()
	m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		d.close(u)
		if !slices.Equal(d.selected, []int{i}) {
			d.selected = []int{i}
			if d.OnChange != nil {
				send(u, d, d.OnChange(i, u))
			}
		}
		return nil
	}
	m.OnPickSub = func(path []int, u *gunim.UI) gunim.Intent {
		d.close(u)
		if !slices.Equal(d.selected, path) {
			d.selected = slices.Clone(path)
			if d.OnChangeSub != nil {
				send(u, d, d.OnChangeSub(path, u))
			}
		}
		return nil
	}
	d.menu = m
	u.Cue(gunim.CueOpen, d)
	d.popup = u.OpenPopup(d, m, gunim.PopupOptions{
		Anchor:  geom.Rect{Max: d.size.Point()},
		Max:     geom.Sz(600, 480),
		Dismiss: dismissed(d, d.close),
	})
	d.turn.Animate(1, Quick.Get(u.Theme()))
}

func (d *Dropdown) close(u *gunim.UI) { d.shut(u.Theme()) }

// shut closes the list.
func (d *Dropdown) shut(th *theme.Live) {
	if d.popup != nil {
		d.popup.Close()
		d.popup = nil
	}
	d.turn.Animate(0, Quick.Get(th))
}

// Layout implements [gunim.Node]. The drop-down is as wide as its
// longest item. The list open keeps to the items as they are now, and
// closes as the drop-down is disabled.
func (d *Dropdown) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	w := d.measure(faceIn(Font, th), TextSize.Get(th))
	pad := FieldPadding.Get(th)
	d.iconSpace = d.list.iconRoom(th)
	w += 2*pad + chevron + pad + d.iconSpace
	if d.MaxWidth > 0 {
		w = min(w, d.MaxWidth)
	}
	d.size = c.Constrain(geom.Sz(w, FieldHeight.Get(th)))
	d.follow(th)
	if d.Disabled && d.popup != nil {
		d.shut(th)
	}
	if d.menu != nil && d.IsOpen() {
		d.sync(d.menu)
	}
	return d.size
}

// measure returns the widest item's width in face at size. It measures the items again only when they, the face or
// the size change.
func (d *Dropdown) measure(face *text.Face, size float32) float32 {
	l := d.list
	key := menuWidth{list: l, face: face, size: size, labelsOnly: true}
	if key != d.widthOf {
		w := float32(0)
		for _, i := range l.longest {
			w = max(w, face.Shape(l.items[i].Label, size).Advance)
		}
		// An item of a submenu shows on the drop-down once it is chosen.
		if leaves := subLabels(l.items, nil); len(leaves) > 0 {
			for _, i := range longest(leaves, menuMeasured) {
				w = max(w, face.Shape(leaves[i], size).Advance)
			}
		}
		d.widest, d.widthOf = w, key
	}
	return d.widest
}

// subLabels appends to out the labels of the items in the submenus of items, and in theirs, on down.
func subLabels(items []MenuItem, out []string) []string {
	for i := range items {
		for _, it := range items[i].subItems() {
			out = append(out, it.Label)
		}
		out = subLabels(items[i].subItems(), out)
	}
	return out
}

// chevron is the width of the drop-down's arrow.
const chevron = 10

// Paint implements [gunim.Node].
func (d *Dropdown) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer d.faint(p, box)()
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := FieldRadius.Get(th)
	d.paintRing(p, r, radius, th)
	fill := anim.Mix(anim.ColorCodec, ButtonFill.Get(th), ButtonHover.Get(th), d.hover.Value())
	p.RRectStroke(r, radius, paint.Solid(fill), paint.Stroke{Width: 1, Color: FieldBorder.Get(th)})

	pad := FieldPadding.Get(th)
	if it := d.chosen(); it != nil {
		x := pad
		// The room for an icon is the list's: an item of a submenu shows its icon where the list has icons.
		switch {
		case d.iconSpace == 0:
		case it.Icon != nil:
			s := IconSize.Get(th)
			paintIcon(p, th, it.Icon, geom.Rc(x, (box.H-s)/2, s, s), Ink.Get(th), 1)
		case it.Swatch.A > 0:
			paintSwatch(p, th, it.Swatch, geom.Pt(x, box.H/2))
		}
		x += d.iconSpace
		run := d.shown.shape(faceIn(Font, th), it.Label, TextSize.Get(th))
		if room := box.W - x - pad - chevron - pad; run.Advance > room {
			run = cutRunWith(run, d.ell.shape(faceIn(Font, th), "…", TextSize.Get(th)), room)
		}
		run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), Ink.Get(th))
	}

	// The chevron turns over as the list opens.
	c := geom.Pt(box.W-pad-chevron/2, box.H/2)
	defer p.Push(paint.Rotate(math.Pi*d.turn.Value(), c))()
	paintChevron(p, th, c, Ink.Get(th))
}

// paintChevron draws icon.ChevronDown centred on c.
func paintChevron(p *paint.Painter, th *theme.Live, c geom.Point, ink color.NRGBA) {
	s := IconSize.Get(th)
	paintIcon(p, th, icon.ChevronDown, geom.Rc(c.X-s/2, c.Y-s/2, s, s), ink, 1)
}

// ContextMenu opens a menu at the pointer when its child is clicked
// with the secondary button, or at a point [ContextMenu.Open] gives, as
// for the Menu key. The menu fades and unfolds from its top as it opens,
// and its highlight glides from item to item, as a [Menu]'s does.
//
// While the menu is open, the context menu holds the keyboard: Up,
// Down, Home and End move the highlight, Enter or Space picks, and
// Escape or Tab closes the menu. It hands the keyboard back once the
// menu closes. An item with a submenu opens it on Right, Enter or Space,
// or as the pointer rests on it; Left and Escape close the submenu.
type ContextMenu struct {
	// Prepare, when set, runs as the secondary button goes down at at, in
	// the context menu's space, before the menu opens. It may set the items
	// for the place pressed, and returning false opens no menu.
	Prepare func(at geom.Point, u *gunim.UI) bool
	// OnPick runs on the UI goroutine with the item picked, once the menu
	// has closed. It may act in the window through u, such as copying to
	// the clipboard; a non-nil result is sent to the application as the
	// context menu's intent.
	OnPick func(i int, u *gunim.UI) gunim.Intent
	// OnPickSub runs in the same way with the path of an item picked in
	// a submenu: the item whose submenu holds it, then the item in that
	// submenu, and on down; see [Menu.OnPickSub].
	OnPickSub func(path []int, u *gunim.UI) gunim.Intent

	child gunim.Node
	popup *gunim.Popup
	menu  *Menu
	list  *menuList
	// back is what had focus before the menu opened.
	back gunim.Node
}

// NewContextMenu returns child with a context menu of items.
func NewContextMenu(child gunim.Node, items []MenuItem) *ContextMenu {
	return &ContextMenu{list: newMenuList(items), child: child}
}

// Items returns the menu's items.
func (c *ContextMenu) Items() []MenuItem { return c.list.items }

// SetItems makes items the menu's items, as Prepare may for the place pressed. The context menu keeps the slice: a
// change to it goes through SetItems again. The menu open shows them at once, and so does a submenu open, as for
// items that arrive after the menu opened.
func (c *ContextMenu) SetItems(items []MenuItem) {
	c.list = newMenuList(items)
	if c.menu != nil {
		c.menu.setList(c.list)
	}
}

// Children implements [gunim.Composite].
func (c *ContextMenu) Children() []gunim.Node { return []gunim.Node{c.child} }

// Focusable implements [gunim.Focusable]. The context menu takes focus
// only while its menu is open.
func (c *ContextMenu) Focusable() bool { return c.popup != nil && c.popup.Open() }

// Handle implements [gunim.Handler].
func (c *ContextMenu) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonSecondary {
			return false
		}
		if c.Prepare != nil && !c.Prepare(e.Pos, u) {
			return false
		}
		c.show(e.Pos, u)
	case input.KeyPress:
		if !c.Focusable() {
			return false
		}
		switch e.Key {
		case input.KeyEscape, input.KeyTab:
			if e.Key == input.KeyEscape && c.menu.Key(e, u) {
				// A submenu closed, and the menu stays.
				break
			}
			u.Cue(gunim.CueClose, c)
			c.close(u)
		default:
			c.menu.Key(e, u)
		}
	case input.FocusLost:
		c.close(u)
	default:
		return false
	}
	return true
}

// Open opens the menu at at, in the context menu's space, as a press of the
// secondary button there does, for a key such as the Menu key.
func (c *ContextMenu) Open(at geom.Point, u *gunim.UI) {
	if c.Prepare != nil && !c.Prepare(at, u) {
		return
	}
	c.show(at, u)
}

func (c *ContextMenu) show(at geom.Point, u *gunim.UI) {
	c.close(u)
	m := NewMenu(nil)
	m.setList(c.list)
	m.cover = true
	m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		c.close(u)
		if c.OnPick != nil {
			send(u, c, c.OnPick(i, u))
		}
		return nil
	}
	m.OnPickSub = func(path []int, u *gunim.UI) gunim.Intent {
		c.close(u)
		if c.OnPickSub != nil {
			send(u, c, c.OnPickSub(path, u))
		}
		return nil
	}
	c.menu = m
	u.Cue(gunim.CueOpen, c)
	c.popup = u.OpenPopup(c, m, gunim.PopupOptions{
		Anchor: geom.Rect{Min: at, Max: at},
		// As tall as the screen lets it be: the menu slides up to show all its items, and scrolls only past that.
		Max:     geom.Sz(600, 4096),
		Dismiss: dismissed(c, c.close),
	})
	if f := u.Focused(); f != c {
		c.back = f
	}
	u.Focus(c)
}

func (c *ContextMenu) close(u *gunim.UI) {
	if c.popup == nil {
		return
	}
	c.popup.Close()
	c.popup = nil
	if u.Focused() == c {
		back := c.back
		c.back = nil
		u.Focus(back)
	}
}

// Layout implements [gunim.Node].
func (c *ContextMenu) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(cs)
	k.Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (c *ContextMenu) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// Tooltip shows a line of text in a popup below the pointer once it
// has rested on the tooltip's child for Delay, and hides it when the
// pointer leaves. Text changed while it shows turns over in place: the
// words before slide up and away as the new come up under them, and the
// popup glides to their width.
type Tooltip struct {
	Text  string
	Delay time.Duration

	child gunim.Node
	tip   tipper
}

// NewTooltip returns child with a tooltip of s.
func NewTooltip(child gunim.Node, s string) *Tooltip {
	return &Tooltip{Text: s, Delay: tipDelay, child: child}
}

// tipDelay is how long the pointer rests on a node before its tooltip shows.
const tipDelay = 600 * time.Millisecond

// tipLines is how many lines a wrapped tooltip shows before it ends with an ellipsis.
const tipLines = 12

// Children implements [gunim.Composite].
func (t *Tooltip) Children() []gunim.Node { return []gunim.Node{t.child} }

// Handle implements [gunim.Handler].
func (t *Tooltip) Handle(e input.Event, u *gunim.UI) bool {
	t.tip.handle(e, u, t, t.Text, t.Delay)
	return false
}

// PartTip shows a tooltip about the part of a node under the pointer, such as an event in a calendar, once the
// pointer rests on it. The node passes each pointer event to Handle with the text for the part under the pointer,
// or an empty text where there is none. Moving to another part hides the tooltip and waits again.
type PartTip struct {
	tip tipper
}

// Handle follows the pointer event e over owner, with text the tooltip of the part under it.
func (p *PartTip) Handle(e input.Event, u *gunim.UI, owner gunim.Node, text string) {
	if text != p.tip.text {
		p.tip.hide(u)
		p.tip.text = text
	}
	if text == "" {
		if _, left := e.(input.PointerLeave); left {
			p.tip.quiet = false
		}
		return
	}
	if _, moved := e.(input.PointerMove); moved && p.tip.popup == nil && p.tip.stop == nil && !p.tip.quiet {
		p.tip.owner, p.tip.delay, p.tip.at = owner, tipDelay, e.(input.PointerMove).Pos
		p.tip.wait(u)
		return
	}
	p.tip.handle(e, u, owner, text, tipDelay)
}

// Hide hides the tooltip, such as while the part under the pointer is dragged.
func (p *PartTip) Hide(u *gunim.UI) { p.tip.hide(u) }

// tipper shows a tooltip for the node it serves.
type tipper struct {
	text  string
	delay time.Duration
	at    geom.Point
	stop  func()
	popup *gunim.Popup
	// owner is the node the popup opens from.
	owner gunim.Node
	// quiet keeps the tooltip down after a press or a scroll, until the pointer leaves.
	quiet bool
	// card is the popup's card while it shows, whose words turn over as the text changes.
	card *tip
}

// retext turns the words of the tooltip showing over to text, where they differ.
func (t *tipper) retext(text string) {
	t.text = text
	if t.card != nil && t.card.text != text && text != "" {
		t.card.turnTo(text)
	}
}

// handle shows the tooltip text for owner once the pointer rests on it for delay, and hides it when the pointer
// leaves, presses or scrolls. After a press or a scroll it stays down until the pointer leaves and comes back.
func (t *tipper) handle(e input.Event, u *gunim.UI, owner gunim.Node, text string, delay time.Duration) {
	t.owner, t.delay = owner, delay
	t.retext(text)
	switch e := e.(type) {
	case input.PointerEnter:
		t.at, t.quiet = e.Pos, false
		t.wait(u)
	case input.PointerMove:
		t.at = e.Pos
		if t.popup == nil && !t.quiet {
			t.wait(u) // the delay starts again while the pointer moves
		}
	case input.PointerLeave:
		t.quiet = false
		t.hide(u)
	case input.PointerDown, input.Scroll:
		t.quiet = true
		t.hide(u)
	}
}

func (t *tipper) wait(u *gunim.UI) {
	if t.stop != nil {
		t.stop()
	}
	t.stop = u.After(t.delay, t.show)
}

func (t *tipper) show(u *gunim.UI) {
	t.stop = nil
	if t.popup != nil || t.text == "" {
		return
	}
	// Below the pointer, clear of it, or above it where the screen runs
	// out.
	t.card = newTip(t.text)
	t.popup = u.OpenPopup(t.owner, t.card, gunim.PopupOptions{
		Anchor: geom.Rect{Min: t.at.Sub(geom.Pt(0, 4)), Max: t.at.Add(geom.Pt(0, 22))},
	})
}

func (t *tipper) hide(*gunim.UI) {
	if t.stop != nil {
		t.stop()
		t.stop = nil
	}
	if t.popup != nil {
		t.popup.Close()
		t.popup, t.card = nil, nil
	}
}

// Layout implements [gunim.Node]. The text set since the last layout
// turns over in the tooltip showing.
func (t *Tooltip) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	t.tip.retext(t.Text)
	k := kids.At(0)
	s := k.Layout(cs)
	k.Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node].
func (t *Tooltip) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// tip is a tooltip's popup: a small card of text that fades in, and
// whose words turn over as they change: was, the words before, slide up
// and away as turn runs to 1, and text comes up under them.
type tip struct {
	text, was   string
	in, turn    *anim.Float
	run, wasRun shapedText
	// para is the text wrapped, for words wider than [TooltipMaxWidth], which shows them without turning over.
	para        laidText
	wrapped     bool
	margin      float32
	transparent bool
	// wide is how wide the screen lets the tooltip's window be, from FitPopup, or 0 where nothing says.
	wide float32
}

// tipNarrowest is the narrowest a tooltip's words wrap to, on the narrowest screen.
const tipNarrowest = 80

// FitPopup implements [gunim.PopupFitter]: the tooltip's words wrap to the screen's width, where it is narrower
// than [TooltipMaxWidth].
func (t *tip) FitPopup(r driver.Room) {
	t.wide = 0
	if wide := r.Left + r.Right; !math.IsInf(float64(wide), 1) {
		t.wide = wide
	}
}

func newTip(text string) *tip {
	return &tip{text: text, in: anim.NewFloat(0), turn: anim.NewFloat(1)}
}

// turnTo turns the words over to s.
func (t *tip) turnTo(s string) {
	t.was, t.text = t.text, s
	t.turn.Jump(0)
	t.turn.Animate(1, anim.Spring{Response: 0.3, Damping: 0.85})
}

// Step implements [gunim.Animator].
func (t *tip) Step(dt time.Duration) bool {
	in := t.in.Step(dt)
	return t.turn.Step(dt) || in
}

// Transition implements [gunim.Transitioner].
func (t *tip) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		t.in.Animate(1, Quick.Get(f.Theme))
	case gunim.Exiting:
		t.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !t.in.Active()
}

// PopupPadding implements [gunim.PopupPadder].
func (t *tip) PopupPadding() geom.Insets { return geom.Uniform(t.margin) }

// Layout implements [gunim.Node].
func (t *tip) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	t.transparent = f.Transparent
	t.margin = 0
	if f.Transparent {
		t.margin = MenuMargin.Get(th)
	}
	run := t.run.shape(faceIn(Font, th), t.text, TooltipSize.Get(th))
	pad := TooltipPadding.Get(th)
	widest := TooltipMaxWidth.Get(th)
	if t.wide > 0 {
		widest = max(min(widest, t.wide-pad.Left-pad.Right-2*t.margin), tipNarrowest)
	}
	t.wrapped = run.Advance > widest
	if t.wrapped {
		t.turn.Jump(1)
		para := t.para.wrap(faceIn(Font, th), t.text, TooltipSize.Get(th), widest, tipLines)
		return c.Constrain(geom.Sz(para.Size.W+pad.Left+pad.Right+2*t.margin,
			para.Size.H+pad.Top+pad.Bottom+2*t.margin))
	}
	// As wide as the wider of the words, while they turn over.
	w := run.Advance
	if t.turn.Value() < 1 {
		w = max(w, t.wasRun.shape(faceIn(Font, th), t.was, TooltipSize.Get(th)).Advance)
	}
	return c.Constrain(geom.Sz(w+pad.Left+pad.Right+2*t.margin, run.Height()+pad.Top+pad.Bottom+2*t.margin))
}

// Paint implements [gunim.Node].
func (t *tip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	pad := TooltipPadding.Get(th)
	card := geom.Rect{Min: geom.Pt(t.margin, t.margin), Max: geom.Pt(box.W-t.margin, box.H-t.margin)}
	// Turning over, the card glides from the width of the words before
	// to the width of the new.
	turn := min(max(t.turn.Value(), 0), 1)
	if turn < 1 {
		from, to := t.wasRun.run.Advance, t.run.run.Advance
		card.Max.X = card.Min.X + from + (to-from)*turn + pad.Left + pad.Right
	}
	if t.transparent {
		radius := TooltipRadius.Get(th)
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(1, max(0, t.in.Value()))})()
		p.ShadowRRect(card, radius, paint.Solid(TooltipFill.Get(th)), paint.Shadow{
			Offset: geom.Pt(0, 2), Blur: t.margin * 0.5, Color: MenuShadow.Get(th),
		})
	} else {
		p.RRect(card, 0, paint.Solid(TooltipFill.Get(th)))
	}
	at, ink := card.Min.Add(geom.Pt(pad.Left, pad.Top)), TooltipInk.Get(th)
	if t.wrapped {
		t.para.p.Paint(p, at, ink)
		return
	}
	if turn >= 1 {
		t.run.run.Paint(p, at, ink)
		return
	}
	// The words turning over, cut to the card.
	defer p.Layer(paint.LayerOpts{Bounds: card, Opacity: 1, Clip: true})()
	h := t.run.run.Height()
	t.wasRun.run.Paint(p, at.Sub(geom.Pt(0, h*turn)), fadedBy(ink, 1-turn))
	t.run.run.Paint(p, at.Add(geom.Pt(0, h*(1-turn))), fadedBy(ink, turn))
}
