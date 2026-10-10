package widget

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/emoji"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Emoji picker tokens: the size of each emoji's cell, and of the emoji in it.
var (
	EmojiCell = theme.Length("emoji.cell", 36)
	EmojiSize = theme.Length("emoji.size", 24)
)

// The picker's grid: emoji a row, and rows that show before it scrolls.
const (
	emojiColumns = 8
	emojiRows    = 7
)

// EmojiPicker finds an emoji. It opens in a popup with a search field, a tab for each group of emoji, and the emoji
// in their groups; typing narrows them to those whose names hold the words typed. The emoji under the pointer shows
// large with its name at the foot. Only emoji the system's fonts draw show.
//
// A click on an emoji, or Enter for the first one showing, picks it. Escape, a click outside or the window losing the
// keyboard closes the picker, and the keyboard goes back where it was.
type EmojiPicker struct {
	// OnPick runs on the UI goroutine with the emoji chosen, once the picker has closed. A non-nil result is sent
	// to the application as an intent from the node that opened the picker.
	OnPick func(emoji string, u *gunim.UI) gunim.Intent
	// Recent holds the emoji picked last, the latest first, which show in a group of their own at the top. The
	// picker keeps it; an application may set it from what it saved, to keep it from one run to the next.
	Recent []string

	popup *gunim.Popup
	card  *emojiCard
	// back is what had the keyboard before the picker opened, and opener the node it opened from.
	back, opener gunim.Node
}

// NewEmojiPicker returns a closed picker with no recent emoji.
func NewEmojiPicker() *EmojiPicker { return &EmojiPicker{} }

// Open opens the picker in a popup attached to anchor, a rectangle in opener's space.
func (e *EmojiPicker) Open(opener gunim.Node, anchor geom.Rect, u *gunim.UI) {
	if e.IsOpen() {
		return
	}
	e.back, e.opener = u.Focused(), opener
	e.card = newEmojiCard(e)
	e.popup = u.OpenPopup(opener, e.card, gunim.PopupOptions{Anchor: anchor, Max: geom.Sz(800, 900), Dismiss: e.Close})
	e.card.filter("", u)
	u.Focus(e.card.field)
	u.Cue(gunim.CueOpen, opener)
}

// Close closes the picker with nothing picked, and gives the keyboard back.
func (e *EmojiPicker) Close(u *gunim.UI) {
	if e.popup != nil {
		u.Cue(gunim.CueClose, e.opener)
	}
	e.shut(u)
}

// shut closes the picker, and gives the keyboard back.
func (e *EmojiPicker) shut(u *gunim.UI) {
	if e.popup == nil {
		return
	}
	e.popup.Close()
	e.popup = nil
	if e.back != nil {
		u.Focus(e.back)
	}
}

// IsOpen reports whether the picker is open.
func (e *EmojiPicker) IsOpen() bool { return e.popup != nil && e.popup.Open() }

// maxRecent is how many emoji the recently used group holds: three rows.
const maxRecent = 3 * emojiColumns

// pick closes the picker and hands over the emoji chosen, which goes to the front of the recently used.
func (e *EmojiPicker) pick(s string, u *gunim.UI) {
	e.Recent = append([]string{s}, slices.DeleteFunc(slices.Clone(e.Recent), func(r string) bool { return r == s })...)
	e.Recent = e.Recent[:min(len(e.Recent), maxRecent)]
	e.shut(u)
	act(u, e.opener, gunim.CuePress, e.OnPick, s)
}

// emojiShown returns the emoji of es that the system's fonts draw.
func emojiShown(es []emoji.Emoji) []emoji.Emoji {
	out := make([]emoji.Emoji, 0, len(es))
	for _, e := range es {
		if text.EmojiShows(e.Text) {
			out = append(out, e)
		}
	}
	return out
}

// pickerRow is a row of the picker's list: a group's title, or up to a row's worth of emoji.
type pickerRow struct {
	title string
	emoji []emoji.Emoji
}

// emojiCard is the open picker: the field, the tabs, the list and the foot.
type emojiCard struct {
	anim.Group
	p     *EmojiPicker
	field *TextField
	tabs  *emojiTabs
	list  *VirtualList
	// shown is the list in the card, which a new query swaps for a new one.
	shown gunim.Node
	rows  map[Key]pickerRow
	// first is the first emoji showing, which Enter picks, and hot the one under the pointer.
	first, hot emoji.Emoji
	hotRun     text.Run
	name       text.Run
	in         *anim.Float

	transparent        bool
	margin, pad, footH float32
	card               geom.Rect
	// room and wide are how tall and how wide the screen lets the picker's window be, from FitPopup, or 0 where
	// nothing says. cell is the size of an emoji's cell, smaller where the screen is narrow.
	room, wide float32
	cell       float32
}

// FitPopup implements [gunim.PopupFitter]: the picker keeps to the taller of the room below its anchor and above
// it, showing fewer rows, and to the screen's width, with smaller cells.
func (c *emojiCard) FitPopup(r driver.Room) {
	c.room, c.wide = 0, 0
	if room := max(r.Below, r.Above); !math.IsInf(float64(room), 1) {
		c.room = room
	}
	if wide := r.Left + r.Right; !math.IsInf(float64(wide), 1) {
		c.wide = wide
	}
}

func newEmojiCard(p *EmojiPicker) *emojiCard {
	c := &emojiCard{p: p, field: NewTextField(), in: anim.NewFloat(0), rows: map[Key]pickerRow{}}
	c.field.Placeholder = "Search emoji"
	c.field.OnChange = func(q string, u *gunim.UI) gunim.Intent {
		c.filter(q, u)
		return nil
	}
	c.list = NewVirtualList(func(k Key) gunim.Node { return &emojiRow{c: c, row: c.rows[k], hover: -1} })
	c.list.Spacing = zeroSpacing
	c.list.Estimate = EmojiCell.Default()
	c.tabs = &emojiTabs{c: c, hover: -1, at: anim.NewFloat(0)}
	c.tabs.Add(c.tabs.at)
	c.shown = c.list
	c.Add(c.in)
	return c
}

// zeroSpacing sets the picker's rows against each other.
var zeroSpacing = theme.Length("emoji.spacing", 0)

// filter shows the emoji whose names hold every word of q, or all of them in their groups when q is empty.
func (c *emojiCard) filter(q string, u *gunim.UI) {
	c.rows = map[Key]pickerRow{}
	var keys []Key
	add := func(k Key, r pickerRow) {
		c.rows[k] = r
		keys = append(keys, k)
	}
	rowsOf := func(prefix string, es []emoji.Emoji) {
		for i := 0; i < len(es); i += emojiColumns {
			add(Key(prefix+strconv.Itoa(i)), pickerRow{emoji: es[i:min(i+emojiColumns, len(es))]})
		}
	}
	c.first = emoji.Emoji{}
	c.tabs.groups = c.tabs.groups[:0]
	if strings.TrimSpace(q) == "" {
		var recent []emoji.Emoji
		for _, r := range c.p.Recent {
			if e, ok := emoji.Lookup(r); ok && text.EmojiShows(r) {
				recent = append(recent, e)
			}
		}
		if len(recent) > 0 {
			c.first = recent[0]
			add("h:recent", pickerRow{title: "Recently used"})
			c.tabs.groups = append(c.tabs.groups, tabGroup{key: "h:recent", icon: icon.Clock, name: "Recently used"})
			rowsOf("r:", recent)
		}
		for gi, g := range emoji.Groups() {
			shown := emojiShown(g.Emoji)
			if len(shown) == 0 {
				continue
			}
			if c.first.Text == "" {
				c.first = shown[0]
			}
			head := Key("h:" + strconv.Itoa(gi))
			add(head, pickerRow{title: g.Name})
			c.tabs.groups = append(c.tabs.groups, tabGroup{key: head, icon: iconOf(g.Name), name: g.Name})
			rowsOf("g:"+strconv.Itoa(gi)+":", shown)
		}
	} else {
		found := emojiShown(emoji.Search(q))
		if len(found) > 0 {
			c.first = found[0]
		}
		rowsOf("s:"+q+":", found)
	}
	// A new set of rows starts at the top.
	c.list = NewVirtualList(c.list.build)
	c.list.Spacing = zeroSpacing
	c.list.Estimate = EmojiCell.Get(u.Theme())
	c.list.SetKeys(keys, u)
	c.swapList(u)
	c.tabs.laid = false
	u.Invalidate()
}

// swapList puts c.list in place of the list the card shows.
func (c *emojiCard) swapList(u *gunim.UI) {
	if c.shown != nil && c.shown != c.list {
		u.Remove(c.shown)
		u.Insert(c, c.list)
	}
	c.shown = c.list
}

// Children implements [gunim.Composite].
func (c *emojiCard) Children() []gunim.Node { return []gunim.Node{c.field, c.tabs, c.list} }

// PopupPadding implements [gunim.PopupPadder].
func (c *emojiCard) PopupPadding() geom.Insets { return geom.Uniform(c.margin) }

// Transition implements [gunim.Transitioner].
func (c *emojiCard) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		c.in.Animate(1, Bounce.Get(f.Theme))
	case gunim.Exiting:
		c.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !c.in.Active()
}

// Handle implements [gunim.Handler]: Enter picks the first emoji showing, and Escape closes.
func (c *emojiCard) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	switch k.Key {
	case input.KeyEnter, input.KeyKPEnter:
		if c.first.Text != "" {
			c.p.pick(c.first.Text, u)
		}
	case input.KeyEscape:
		c.p.Close(u)
	default:
		return false
	}
	return true
}

// shownGroup returns the tab of the group the list shows, or scrolls to: the last whose title is at or above the
// list's target top, or the last group once the list is at its end.
func (c *emojiCard) shownGroup() int {
	l := c.list
	if len(c.tabs.groups) == 0 || len(l.order) == 0 {
		return 0
	}
	if l.AtEnd() && l.Offset() > 0 {
		return len(c.tabs.groups) - 1
	}
	shown := 0
	for i, g := range c.tabs.groups {
		if at := slices.Index(l.order, g.key); at >= 0 && at < len(l.entry) && float32(l.tops.sum(at)) <= l.target+2 {
			shown = i
		}
	}
	return shown
}

// setHot shows e large, with its name, at the foot; a group's tab shows its name alone.
func (c *emojiCard) setHot(e emoji.Emoji, u *gunim.UI) {
	if e == c.hot {
		return
	}
	c.hot = e
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (c *emojiCard) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	c.transparent = f.Transparent
	c.margin = 0
	if f.Transparent {
		c.margin = MenuMargin.Get(th)
	}
	c.pad = MenuPadding.Get(th) * 2
	// Too wide for its box or the screen, the cells shrink to fit a row of emoji.
	wide := cs.Max.W
	if c.wide > 0 {
		wide = min(wide, c.wide)
	}
	cell := max(min(EmojiCell.Get(th), (wide-2*c.margin-2*c.pad)/emojiColumns), 8)
	c.cell = cell
	inner := cell * emojiColumns
	x, y := c.margin+c.pad, c.margin+c.pad
	var kidsAt []gunim.Child
	for k := range kids.All {
		kidsAt = append(kidsAt, k)
	}
	field, tabs := kidsAt[0], kidsAt[1]
	fh := field.Layout(gunim.Tight(geom.Sz(inner, FieldHeight.Get(th)))).H
	field.Place(geom.Pt(x, y))
	y += fh + c.pad/2
	th2 := tabs.Layout(gunim.Tight(geom.Sz(inner, cell))).H
	tabs.Place(geom.Pt(x, y))
	y += th2 + c.pad/2
	c.footH = cell * 1.2
	// Too tall for its box or the screen, the list shows fewer rows, and scrolls the rest.
	tall := cs.Max.H
	if c.room > 0 {
		tall = min(tall, c.room)
	}
	lh := max(cell, min(cell*emojiRows, tall-c.margin-y-c.footH-c.pad))
	for _, k := range kidsAt[2:] {
		k.Layout(gunim.Tight(geom.Sz(inner, lh)))
		k.Place(geom.Pt(x, y))
	}
	y += lh
	c.name = faceIn(Font, th).Shape(c.hot.Name, TextSize.Get(th))
	c.hotRun = text.Default().Shape(c.hot.Text, cell*0.8)
	h := y + c.footH + c.pad - c.margin
	w := inner + 2*c.pad
	c.card = geom.Rc(c.margin, c.margin, w, h)
	return cs.Constrain(geom.Sz(w+2*c.margin, h+2*c.margin))
}

// cellOr is the size of an emoji's cell as last laid out, or the theme's before the first layout.
func (c *emojiCard) cellOr(th *theme.Live) float32 {
	if c.cell > 0 {
		return c.cell
	}
	return EmojiCell.Get(th)
}

// Covers implements [gunim.Shaped].
func (c *emojiCard) Covers(p geom.Point) bool { return !c.transparent || c.card.Contains(p) }

// CoverRects implements [gunim.RegionShaped].
func (c *emojiCard) CoverRects() []geom.Rect { return coverCard(c.transparent, c.card) }

// coverCard is where a popup's card kept in a window at its largest takes the pointer: the card, where the window
// shows what is behind it, and else, nil, the whole window, which is the card's colour all over.
func coverCard(transparent bool, card geom.Rect) []geom.Rect {
	if !transparent {
		return nil
	}
	return []geom.Rect{card}
}

// Paint implements [gunim.Node]: a card like a menu's, fading in and unfolding from its top edge, with the emoji
// under the pointer and its name at its foot.
func (c *emojiCard) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	th := f.Theme
	radius := MenuRadius.Get(th)
	t := c.in.Value()
	if c.transparent {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: c.card.Max.Add(geom.Pt(c.margin, c.margin))}, Opacity: min(1, max(0, t))})()
		defer p.Push(paint.Scale(0.96+0.04*t, geom.Pt(c.card.Center().X, c.card.Min.Y)))()
		p.ShadowRRect(c.card, radius, paint.Solid(MenuFill.Get(th)), paint.Shadow{
			Offset: geom.Pt(0, 3), Blur: c.margin * 0.6, Color: MenuShadow.Get(th),
		})
	} else {
		radius = 0
		p.RRect(c.card, 0, paint.Solid(MenuFill.Get(th)))
	}
	p.RRectStroke(c.card, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: MenuBorder.Get(th)})
	for k := range kids.All {
		k.Paint(p)
	}
	foot := geom.Rc(c.card.Min.X+c.pad, c.card.Max.Y-c.pad-c.footH, c.card.Size().W-2*c.pad, c.footH)
	p.RRect(geom.Rc(foot.Min.X, foot.Min.Y, foot.Size().W, 1), 0, paint.Solid(MenuBorder.Get(th)))
	if c.hot.Name == "" {
		if c.first.Text == "" {
			none := faceIn(Font, th).Shape("No emoji found", TextSize.Get(th))
			none.Paint(p, geom.Pt(foot.Min.X+4, foot.Center().Y-none.Height()/2), PaletteHint.Get(th))
		}
		return
	}
	mid := foot.Center().Y + 2
	x := foot.Min.X + 2
	if c.hot.Text != "" {
		c.hotRun.Paint(p, geom.Pt(x, mid-c.hotRun.Height()/2), Ink.Get(th))
		x += c.hotRun.Advance + 8
	}
	if room := foot.Max.X - x; c.name.Advance > room {
		c.name = cutRunWith(c.name, faceIn(Font, th).Shape("…", c.name.Size), room)
	}
	c.name.Paint(p, geom.Pt(x, mid-c.name.Height()/2), Ink.Get(th))
}

// tabGroup is a group's tab: the key of its title's row, its icon, and its name.
type tabGroup struct {
	key  Key
	icon *icon.Icon
	name string
}

// groupIcons are the icons of the groups' tabs, by the groups' names.
var groupIcons = map[string]*icon.Icon{
	"Smileys & Emotion": icon.Smile,
	"People & Body":     icon.Users,
	"Animals & Nature":  icon.PawPrint,
	"Food & Drink":      icon.Coffee,
	"Travel & Places":   icon.Plane,
	"Activities":        icon.Trophy,
	"Objects":           icon.Lightbulb,
	"Symbols":           icon.Heart,
	"Flags":             icon.Flag,
}

// iconOf returns the icon of the group named name.
func iconOf(name string) *icon.Icon {
	if ic, ok := groupIcons[name]; ok {
		return ic
	}
	return icon.Shapes
}

// emojiTabs is the row of the picker's groups, each an icon, with a bar under the group the list shows. A click
// scrolls to the group, and the pointer on a tab names its group at the foot.
type emojiTabs struct {
	anim.Group
	c      *emojiCard
	groups []tabGroup
	hover  int
	cell   float32
	// at carries the bar to the group the list shows, by the group's place in the row.
	at    *anim.Float
	laid  bool
	shown int
	click Clicker
}

// Layout implements [gunim.Node].
func (t *emojiTabs) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	t.cell = cs.Max.W / float32(max(len(t.groups), emojiColumns))
	if len(t.groups) == 0 {
		return geom.Sz(cs.Max.W, 0)
	}
	now := t.c.shownGroup()
	switch {
	case !t.laid:
		t.at.Jump(float32(now))
	case now != t.shown:
		t.at.Animate(float32(now), Quick.Get(f.Theme))
	}
	t.laid, t.shown = true, now
	return geom.Sz(cs.Max.W, t.c.cellOr(f.Theme))
}

// Paint implements [gunim.Node].
func (t *emojiTabs) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	s := IconSize.Get(th)
	for i, g := range t.groups {
		cellBox := geom.Rc(float32(i)*t.cell, 0, t.cell, box.H-3)
		if i == t.hover {
			p.RRect(cellBox.Inset(geom.Uniform(2)), 6, paint.Solid(MenuHot.Get(th)))
		}
		ink := PaletteHint.Get(th)
		if i == t.shown {
			ink = Ink.Get(th)
		}
		c := cellBox.Center()
		paintIcon(p, th, g.icon, geom.Rc(c.X-s/2, c.Y-s/2, s, s), ink, 1)
	}
	// A rule under the row, and the accent's bar on it under the group shown.
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(MenuBorder.Get(th)))
	w := t.cell * 0.6
	x := t.at.Value()*t.cell + (t.cell-w)/2
	p.RRect(geom.Rc(x, box.H-3, w, 3), 1.5, paint.Solid(Accent.Get(th)))
}

// at returns the tab under pt, or -1.
func (t *emojiTabs) tabAt(pt geom.Point) int {
	if t.cell <= 0 || pt.X < 0 {
		return -1
	}
	if i := int(pt.X / t.cell); i < len(t.groups) {
		return i
	}
	return -1
}

// Handle implements [gunim.Handler].
func (t *emojiTabs) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if i := t.tabAt(e.Pos); i != t.hover {
			t.hover = i
			if i >= 0 {
				t.c.setHot(emoji.Emoji{Name: t.groups[i].name}, u)
			}
			u.Invalidate()
		}
	case input.PointerLeave:
		t.hover = -1
		u.Invalidate()
	case input.PointerDown:
		i := t.tabAt(e.Pos)
		t.click.Press(e, i)
		return i >= 0 && e.Button == input.ButtonPrimary
	case input.PointerUp:
		if i := t.tabAt(e.Pos); t.click.Release(e, i) {
			t.c.list.ScrollToKey(t.groups[i].key, u)
		}
		return true
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (t *emojiTabs) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// emojiRow is a row of the picker's list: a group's title, or a row of emoji to pick from.
type emojiRow struct {
	c     *emojiCard
	row   pickerRow
	runs  []text.Run
	title text.Run
	size  float32
	cell  float32
	hover int
	click Clicker
}

// Layout implements [gunim.Node].
func (r *emojiRow) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	r.cell = r.c.cellOr(th)
	if r.row.title != "" {
		r.title = faceIn(BoldFont, th).Shape(r.row.title, TextSize.Get(th)*0.85)
		return geom.Sz(cs.Max.W, r.title.Height()+10)
	}
	if size := min(EmojiSize.Get(th), r.cell*2/3); size != r.size || len(r.runs) != len(r.row.emoji) {
		r.size = size
		r.runs = r.runs[:0]
		for _, e := range r.row.emoji {
			r.runs = append(r.runs, text.Default().Shape(e.Text, size))
		}
	}
	return geom.Sz(cs.Max.W, r.cell)
}

// Paint implements [gunim.Node].
func (r *emojiRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	if r.row.title != "" {
		r.title.Paint(p, geom.Pt(4, box.H-r.title.Height()-3), PaletteHint.Get(th))
		return
	}
	for i, run := range r.runs {
		cellBox := geom.Rc(float32(i)*r.cell, 0, r.cell, r.cell)
		if i == r.hover {
			p.RRect(cellBox.Inset(geom.Uniform(1)), 8, paint.Solid(MenuHot.Get(th)))
		}
		c := cellBox.Center()
		run.Paint(p, geom.Pt(c.X-run.Advance/2, c.Y-run.Height()/2), Ink.Get(th))
	}
}

// at returns the emoji under pt, or -1.
func (r *emojiRow) at(pt geom.Point) int {
	if r.cell <= 0 || pt.X < 0 || pt.Y < 0 || pt.Y >= r.cell {
		return -1
	}
	if i := int(pt.X / r.cell); i < len(r.row.emoji) {
		return i
	}
	return -1
}

// Handle implements [gunim.Handler]: an emoji lights under the pointer and shows at the foot, and a click picks it.
func (r *emojiRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if i := r.at(e.Pos); i != r.hover {
			r.hover = i
			if i >= 0 {
				r.c.setHot(r.row.emoji[i], u)
			}
			u.Invalidate()
		}
	case input.PointerLeave:
		r.hover = -1
		u.Invalidate()
	case input.PointerDown:
		i := r.at(e.Pos)
		r.click.Press(e, i)
		return i >= 0 && e.Button == input.ButtonPrimary
	case input.PointerUp:
		// A click picks the emoji it lets go on, the one it pressed, so a
		// finger that lands on one to scroll the list picks nothing.
		if i := r.at(e.Pos); r.click.Release(e, i) {
			r.c.p.pick(r.row.emoji[i].Text, u)
		}
		return true
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (r *emojiRow) Cursor(pt geom.Point) input.Cursor {
	if r.at(pt) >= 0 {
		return input.CursorHand
	}
	return input.CursorArrow
}

// LoadEmoji finds which emoji the system's fonts draw, which a picker otherwise finds as it first opens, taking a
// moment. An application can call it from a goroutine as it starts.
func LoadEmoji() {
	for _, g := range emoji.Groups() {
		for _, e := range g.Emoji {
			text.EmojiShows(e.Text)
		}
	}
}
