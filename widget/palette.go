package widget

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/match"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Palette tokens.
var (
	PaletteWidth     = theme.Length("palette.width", 560)
	PaletteRowHeight = theme.Length("palette.row", 32)
	// PaletteMark sits behind the letters a query matched.
	PaletteMark = theme.Color("palette.mark", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x50})
	// PaletteHint colours an item's hint, such as its shortcut.
	PaletteHint = theme.Color("palette.hint", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
)

// paletteRows is how many items show before the list scrolls, where the screen has room for them.
const paletteRows = 10

// PaletteItem is one thing a [Palette] offers: its title, a hint shown
// beside it, such as its shortcut, and other words it answers to.
type PaletteItem struct {
	Title string
	// Icon shows before the title, in its colour. Titles line up after a column for icons when any item has one.
	Icon *icon.Icon
	Hint string
	Also []string
	// Detail is said faintly after the title, such as the folder a file
	// is in.
	Detail string
	// Key keeps an item's row from one [Palette.SetItems] to the next, so
	// it moves to its new place; unset, the item's index is its key.
	Key Key
	// At holds the runes of Title to mark, for items ranked by the caller.
	At []int
	// Problem draws the title in the colour of something wrong.
	Problem bool
	// Checked draws a tick at the row's end, before the hint, as a menu
	// ticks an item: for a command that switches something, such as
	// Show Sidebar, to say which way it stands.
	Checked bool
}

// Palette finds one of a list of items as the user types part of its
// name, the way a command palette does. It opens in a popup with a
// field and the items, best match first, the matched letters marked.
// The list reorders as the query changes, items folding away and
// growing back, and the card grows and shrinks with it. With OnSearch set,
// the caller ranks the items, as for a search run in the background, and
// hands them over with SetItems.
//
// The field has the keyboard while the palette is open. Up and Down,
// Page Up and Page Down move the highlight, Enter picks, and Escape, a
// click outside or the window losing the keyboard closes it. Focus goes
// back where it was.
type Palette struct {
	Items       []PaletteItem
	Placeholder string
	// OnPick runs on the UI goroutine with the index of the item chosen,
	// once the palette has closed. An index past Items is Typed's item at
	// i-len(Items). A non-nil result is sent to the application as an
	// intent from the node that opened the palette, as are those of the
	// palette's other callbacks.
	OnPick func(i int, u *gunim.UI) gunim.Intent
	// Typed, when set, returns items made from what has been typed, such
	// as a value to match exactly; they show first, in the order given.
	Typed func(query string) []PaletteItem
	// OnSearch, when set, hears each query in place of the palette ranking
	// Items itself; the answer comes back through SetItems.
	OnSearch func(query string, u *gunim.UI) gunim.Intent
	// OnCtrlPick, when set, runs in place of OnPick for Ctrl+Enter.
	OnCtrlPick func(i int, u *gunim.UI) gunim.Intent
	// Status is a line under the field, such as how far a search has got.
	Status string
	// OnHighlight, when set, hears the item the highlight has moved to, by
	// the keys, the pointer or the list narrowing, as OnPick would be given
	// it, and -1 when no item is highlighted: for a palette that shows what
	// an item would do before it is picked, such as a theme.
	OnHighlight func(i int, u *gunim.UI) gunim.Intent
	// OnCancel, when set, runs as the palette closes with nothing picked:
	// Escape, a click outside, or Close. It undoes what OnHighlight showed.
	OnCancel func(u *gunim.UI) gunim.Intent
	// Key, when set, hears a key the palette and its field do not use,
	// such as a shortcut of the application's, and reports whether it
	// took it. A popup's keys reach only the popup, so this is how the
	// shortcut that opened the palette can close it again.
	Key func(k input.KeyPress, u *gunim.UI) bool

	popup *gunim.Popup
	card  *paletteCard
	back  gunim.Node
	// opener is the node the palette was opened from, which its intents come from.
	opener gunim.Node
	// typedItems holds Typed's items for the query last typed.
	typedItems []PaletteItem
	// told is the item OnHighlight was last told of, and picking says a pick
	// is closing the palette, which is no cancel.
	told    int
	picking bool
}

// NewPalette returns a closed palette of items.
func NewPalette(items ...PaletteItem) *Palette { return &Palette{Items: items} }

// Open opens the palette in a popup attached to anchor, a rectangle in
// opener's space: centred on it, just below its top edge.
func (p *Palette) Open(opener gunim.Node, anchor geom.Rect, u *gunim.UI) {
	if p.IsOpen() {
		return
	}
	p.back, p.opener = u.Focused(), opener
	p.told = -2
	p.card = newPaletteCard(p)
	w := PaletteWidth.Get(u.Theme())
	x := anchor.Center().X - w/2
	p.card.centre, p.card.at = anchor.Center().X, geom.Pt(x, anchor.Min.Y)
	p.popup = u.OpenPopup(opener, p.card, gunim.PopupOptions{
		Anchor:  geom.Rect{Min: geom.Pt(x, anchor.Min.Y), Max: geom.Pt(x+w, anchor.Min.Y)},
		Max:     geom.Sz(w+200, 800),
		Dismiss: p.Close,
	})
	p.card.filter("", u)
	u.Focus(p.card.field)
	u.Cue(gunim.CueOpen, opener)
}

// Close closes the palette, and gives the keyboard back.
func (p *Palette) Close(u *gunim.UI) {
	if p.popup == nil {
		return
	}
	p.popup.Close()
	p.popup = nil
	if p.back != nil {
		u.Focus(p.back)
	}
	if !p.picking {
		act0(u, p.opener, gunim.CueClose, p.OnCancel)
	}
}

// IsOpen reports whether the palette is open.
func (p *Palette) IsOpen() bool { return p.popup != nil && p.popup.Open() }

// SetItems makes items the palette's items, in the order given, for a
// palette whose OnSearch ranks them. Rows move to their new places.
func (p *Palette) SetItems(items []PaletteItem, u *gunim.UI) {
	was := Key("")
	if p.IsOpen() && p.card.hot > 0 {
		if i := p.card.hotIndex(); i >= 0 {
			was = p.keyOf(i)
		}
	}
	p.Items = items
	if p.IsOpen() {
		p.card.show(was, u)
	}
}

// SetStatus sets the line under the field.
func (p *Palette) SetStatus(s string, u *gunim.UI) {
	p.Status = s
	u.Invalidate()
}

// Query returns what is typed in the palette's field.
func (p *Palette) Query() string {
	if p.card == nil {
		return ""
	}
	return p.card.field.Text()
}

// SetQuery puts q in the palette's field, as if it were typed.
func (p *Palette) SetQuery(q string, u *gunim.UI) {
	if !p.IsOpen() {
		return
	}
	p.card.field.SetText(q, u)
	p.card.field.Select(len(q), len(q))
	p.card.filter(q, u)
}

func (p *Palette) choose(i int, ctrl bool, u *gunim.UI) {
	p.picking = true
	p.Close(u)
	p.picking = false
	pick := p.OnPick
	if ctrl && p.OnCtrlPick != nil {
		pick = p.OnCtrlPick
	}
	act(u, p.opener, gunim.CuePress, pick, i)
}

// paletteCard is the palette's popup: the field over the list.
type paletteCard struct {
	anim.Group
	p     *Palette
	field *TextField
	// list builds only the rows in view: a palette holds hundreds of
	// commands, and each letter typed ranks them all again.
	list  *VirtualList
	found []match.Found
	// place is each item's place in found, by its key.
	place map[Key]int
	hot   int
	// ranked is the Items the matcher's items, keys and icons were taken from: items as the matcher takes them, keys
	// their keys, numbered which keys are made from the index, and icons0 whether any has an icon. pool is the
	// indices found by last, the query typed last, in order: a query that extends it finds only items among them.
	ranked   []PaletteItem
	items    []match.Item
	keys     []Key
	numbered []bool
	icons0   bool
	pool     []int
	last     string
	in       *anim.Float
	// height is the list's height, gliding as the list changes.
	height *anim.Float

	margin, pad, rowH, spacing float32
	fieldH                     float32
	card                       geom.Rect
	status                     shapedText
	transparent                bool
	// icons says an item has an icon, found as the items change rather than for each row every frame.
	icons bool
	// room and wide are how tall and how wide the screen lets the palette's window be, from FitPopup, or 0 where
	// nothing says, and edge the screen's left edge in the opener's space. shown is how many rows the list shows
	// before it scrolls.
	room, wide, edge float32
	shown            int
	// centre is the middle of the anchor the palette opened on, and at the top left of the card's anchor now, in the
	// opener's space: the card keeps centred on centre, on the screen.
	centre float32
	at     geom.Point
}

func newPaletteCard(p *Palette) *paletteCard {
	c := &paletteCard{p: p, field: NewTextField(), in: anim.NewFloat(0), height: anim.NewFloat(0), shown: paletteRows}
	c.list = NewVirtualList(func(k Key) gunim.Node { return newPaletteRow(c, k) })
	c.field.Placeholder = p.Placeholder
	c.field.OnChange = func(q string, u *gunim.UI) gunim.Intent {
		c.filter(q, u)
		return nil
	}
	c.Add(c.in, c.height)
	return c
}

// Children implements [gunim.Composite].
func (c *paletteCard) Children() []gunim.Node { return []gunim.Node{c.field, c.list} }

// PopupPadding implements [gunim.PopupPadder].
func (c *paletteCard) PopupPadding() geom.Insets { return geom.Uniform(c.margin) }

// FitPopup implements [gunim.PopupFitter]: the palette keeps to the taller of the room below its anchor and above
// it, and to the screen's width.
func (c *paletteCard) FitPopup(r driver.Room) {
	c.room, c.wide = 0, 0
	if room := max(r.Below, r.Above); !math.IsInf(float64(room), 1) {
		c.room = room
	}
	if wide := r.Left + r.Right; !math.IsInf(float64(wide), 1) {
		// The room was measured from the window's edge, the card's less its padding as it was then.
		c.wide, c.edge = wide, c.at.X-c.margin-r.Left
	}
}

// Transition implements [gunim.Transitioner].
func (c *paletteCard) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		c.in.Animate(1, Bounce.Get(f.Theme))
	case gunim.Exiting:
		c.in.Animate(0, Quick.Get(f.Theme))
	case gunim.Present:
	}
	return !c.in.Active()
}

// filter shows the items the query finds, keeping the highlight on the
// best.
func (c *paletteCard) filter(q string, u *gunim.UI) {
	if c.p.OnSearch != nil {
		send(u, c.p.opener, c.p.OnSearch(q, u))
		return
	}
	c.found = c.rank(q)
	c.p.typedItems = nil
	if c.p.Typed != nil {
		c.p.typedItems = c.p.Typed(q)
	}
	if n := len(c.p.typedItems); n > 0 {
		first := make([]match.Found, n, n+len(c.found))
		for k := range first {
			first[k] = match.Found{Index: len(c.p.Items) + k}
		}
		c.found = append(first, c.found...)
	}
	c.list.ScrollTo(0, Quick.Get(u.Theme()))
	c.refill(u)
}

// show shows the items as given, for a palette whose OnSearch ranks them.
// The highlight stays on the item under key was while the items hold it.
func (c *paletteCard) show(was Key, u *gunim.UI) {
	c.take()
	c.found = make([]match.Found, len(c.p.Items))
	for i, it := range c.p.Items {
		c.found[i] = match.Found{Index: i, At: it.At}
	}
	c.refill(u)
	if k, ok := c.place[was]; ok && was != "" {
		c.hot = k
		c.light(u)
		return
	}
	c.list.ScrollTo(0, Quick.Get(u.Theme()))
}

// rank ranks the items for q. A query that extends the one before it ranks only what that one found: each letter
// added can only lose items.
func (c *paletteCard) rank(q string) []match.Found {
	c.take()
	var found []match.Found
	if c.last != "" && strings.HasPrefix(q, c.last) {
		some := make([]match.Item, len(c.pool))
		for k, i := range c.pool {
			some[k] = c.items[i]
		}
		found = match.Rank(some, q)
		for k := range found {
			found[k].Index = c.pool[found[k].Index]
		}
	} else {
		found = match.Rank(c.items, q)
	}
	// The pool keeps the order of Items, so ties rank as they would among all of them.
	in := make([]bool, len(c.items))
	for _, f := range found {
		in[f.Index] = true
	}
	c.pool = c.pool[:0]
	for i, ok := range in {
		if ok {
			c.pool = append(c.pool, i)
		}
	}
	c.last = q
	return found
}

// take reads Items again, as they may have changed in place: as the matcher takes them, their keys, and whether any
// has an icon. An item whose words have changed ranks the next query among all of them.
func (c *paletteCard) take() {
	same := c.items != nil && sameSlice(c.ranked, c.p.Items)
	if !same {
		c.items = make([]match.Item, len(c.p.Items))
		c.keys = make([]Key, len(c.p.Items))
		c.numbered = make([]bool, len(c.p.Items))
	}
	c.ranked = c.p.Items
	c.icons0 = false
	for i, it := range c.p.Items {
		if m := &c.items[i]; m.Title != it.Title || !sameSlice(m.Also, it.Also) {
			*m = match.Item{Title: it.Title, Also: it.Also}
			same = false
		}
		switch {
		case it.Key != "":
			c.keys[i], c.numbered[i] = it.Key, false
		case !c.numbered[i]:
			// Made once: a key for each item each letter typed is a lot of garbage.
			c.keys[i], c.numbered[i] = Key(strconv.Itoa(i)), true
		}
		c.icons0 = c.icons0 || it.Icon != nil
	}
	if !same {
		c.last, c.pool = "", nil
	}
}

// refill puts the items found in the list, the highlight on the first.
func (c *paletteCard) refill(u *gunim.UI) {
	c.hot = 0
	c.icons = c.icons0 || hasIcons(c.p.typedItems)
	c.place = make(map[Key]int, len(c.found))
	keys := make([]Key, len(c.found))
	for k, f := range c.found {
		if f.Index < len(c.keys) {
			keys[k] = c.keys[f.Index]
		} else {
			keys[k] = c.p.keyOf(f.Index)
		}
		c.place[keys[k]] = k
	}
	c.list.SetKeys(keys, u)
	c.light(u)
	rows := float32(min(len(c.found), paletteRows))
	c.height.Animate(rows*(c.rowOr(u)+ListSpacing.Get(u.Theme())), Settle.Get(u.Theme()))
}

// keyOf is item i's key in the list.
func (p *Palette) keyOf(i int) Key {
	if it := p.item(i); it.Key != "" {
		return it.Key
	}
	return Key(strconv.Itoa(i))
}

// hotIndex is the item highlighted, or -1.
func (c *paletteCard) hotIndex() int {
	if c.hot < 0 || c.hot >= len(c.found) {
		return -1
	}
	return c.found[c.hot].Index
}

func (c *paletteCard) rowOr(u *gunim.UI) float32 {
	if c.rowH > 0 {
		return c.rowH
	}
	return PaletteRowHeight.Get(u.Theme())
}

// light scrolls the highlighted row into view; each row lights itself
// as it is laid out.
func (c *paletteCard) light(u *gunim.UI) {
	if c.hot >= 0 && c.hot < len(c.found) {
		step := c.rowOr(u) + ListSpacing.Get(u.Theme())
		y := float32(c.hot) * step
		c.list.revealContent(geom.Rc(0, y, 1, step), u)
	}
	if i := c.hotIndex(); c.p.OnHighlight != nil && i != c.p.told {
		c.p.told = i
		send(u, c.p.opener, c.p.OnHighlight(i, u))
	}
	u.Invalidate()
}

func (c *paletteCard) move(by int, u *gunim.UI) {
	if len(c.found) == 0 {
		return
	}
	c.hot = min(max(c.hot+by, 0), len(c.found)-1)
	c.light(u)
}

func (c *paletteCard) pick(ctrl bool, u *gunim.UI) {
	if c.hot >= 0 && c.hot < len(c.found) {
		c.p.choose(c.found[c.hot].Index, ctrl, u)
	}
}

// Handle implements [gunim.Handler]: the keys the field passes on.
func (c *paletteCard) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	switch k.Key {
	case input.KeyUp:
		c.move(-1, u)
	case input.KeyDown:
		c.move(1, u)
	case input.KeyPageUp:
		c.move(-c.shown, u)
	case input.KeyPageDown:
		c.move(c.shown, u)
	case input.KeyEnter, input.KeyKPEnter:
		c.pick(k.Mods.Has(input.ModControl), u)
	case input.KeyEscape:
		c.p.Close(u)
	default:
		return c.p.Key != nil && c.p.Key(k, u)
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
func (c *paletteCard) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	c.transparent = f.Transparent
	c.margin = 0
	if f.Transparent {
		c.margin = MenuMargin.Get(th)
	}
	c.pad, c.rowH, c.spacing = MenuPadding.Get(th)*2, PaletteRowHeight.Get(th), ListSpacing.Get(th)
	// Too wide for its box or the screen, the palette keeps to them, and cuts its items' text short.
	wide := cs.Max.W
	if c.wide > 0 {
		wide = min(wide, c.wide)
	}
	w := max(min(PaletteWidth.Get(th), wide-2*c.margin), 2*c.pad+1)
	c.centreOn(w)
	inner := w - 2*c.pad
	fh := kids.At(0).Layout(gunim.Tight(geom.Sz(inner, FieldHeight.Get(th)))).H
	kids.At(0).Place(geom.Pt(c.margin+c.pad, c.margin+c.pad))
	c.fieldH = fh
	if c.p.Status != "" {
		// The status line sits under the field.
		fh += paletteStatus(th)
	}
	// Too tall for its box or the screen, the list shows fewer rows and scrolls the rest. It counts on room for the
	// status line, so the rows shown stay the same as the line comes and goes.
	tall := cs.Max.H
	if c.room > 0 {
		tall = min(tall, c.room)
	}
	step := c.rowH + c.spacing
	c.shown = int(min(paletteRows, max(1, (tall-2*c.margin-3*c.pad-c.fieldH-paletteStatus(th))/step)))
	lh := min(max(0, c.height.Value()), float32(c.shown)*step)
	kids.At(1).Layout(gunim.Tight(geom.Sz(inner, lh)))
	gap := min(c.pad, lh)
	kids.At(1).Place(geom.Pt(c.margin+c.pad, c.margin+c.pad+fh+gap))
	h := c.pad + fh + gap + lh + c.pad
	c.card = geom.Rc(c.margin, c.margin, w, h)
	// Where the window can show what is behind it, it is kept at the
	// palette's tallest and the card grows and shrinks inside it: a
	// window resized every frame of the card's motion is slow, and on
	// Windows very slow. Below the card, the pointer passes through.
	if c.transparent {
		h = max(h, 3*c.pad+c.fieldH+paletteStatus(th)+float32(c.shown)*step)
	}
	return cs.Constrain(geom.Sz(w+2*c.margin, h+2*c.margin))
}

// centreOn moves the popup so a card w wide is centred where the palette opened, and on the screen.
func (c *paletteCard) centreOn(w float32) {
	x := c.centre - w/2
	if c.wide > 0 {
		x = max(min(x, c.edge+c.wide-c.margin-w), c.edge+c.margin)
	}
	if x != c.at.X && c.p.popup != nil {
		c.at.X = x
		c.p.popup.Move(geom.Rect{Min: c.at, Max: geom.Pt(x+w, c.at.Y)})
	}
}

// Covers implements [gunim.Shaped]: the card is the palette's, and the
// rest of a window kept at its tallest is not.
func (c *paletteCard) Covers(p geom.Point) bool {
	return !c.transparent || c.card.Contains(p)
}

// CoverRects implements [gunim.RegionShaped].
func (c *paletteCard) CoverRects() []geom.Rect { return coverCard(c.transparent, c.card) }

// Paint implements [gunim.Node]: a card like a menu's, fading in and
// unfolding from its top edge.
func (c *paletteCard) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
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
	if c.p.Status != "" {
		face := faceIn(Font, th)
		run := c.status.shape(face, c.p.Status, TextSize.Get(th)*0.85)
		room := c.card.Size().W - 2*c.pad - MenuRowPadding.Get(th)
		if run.Advance > room {
			run = cutRunWith(run, face.Shape("…", run.Size), room)
		}
		y := c.card.Min.Y + c.pad + c.fieldH + (paletteStatus(th)-run.Height())/2
		run.Paint(p, geom.Pt(c.card.Min.X+c.pad+MenuRowPadding.Get(th), y), PaletteHint.Get(th))
	}
}

// paletteStatus is the height of the line under a palette's field.
func paletteStatus(th *theme.Live) float32 { return TextSize.Get(th) * 1.6 }

// paletteRow is one item in a palette's list.
type paletteRow struct {
	anim.Group
	c   *paletteCard
	key Key
	// index is the item's index, place its place in the list, and item and at the item and its marks as last laid
	// out, which the row keeps drawing as it leaves.
	index  int
	place  int
	item   PaletteItem
	at     []int
	hot    *anim.Float
	on     bool
	title  shapedText
	detail shapedText
	hint   shapedText
	// size is the row's size at its last layout, for telling a release
	// on it from one off it.
	size  geom.Size
	click Clicker
}

func newPaletteRow(c *paletteCard, key Key) *paletteRow {
	r := &paletteRow{c: c, key: key, index: -1, hot: anim.NewFloat(0)}
	r.Add(r.hot)
	r.refresh()
	return r
}

// refresh takes the row's item afresh, while the palette still holds it, and reports whether it does.
func (r *paletteRow) refresh() bool {
	k, ok := r.c.place[r.key]
	if ok {
		f := r.c.found[k]
		r.place, r.index, r.item, r.at = k, f.Index, r.c.p.item(f.Index), f.At
	}
	return ok
}

// Layout implements [gunim.Node]. The row lights as it is laid out, when
// it has become the one highlighted, or dims when it has stopped being.
func (r *paletteRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	r.refresh()
	if on := r.index >= 0 && r.c.hotIndex() == r.index; on != r.on {
		r.on = on
		to := float32(0)
		if on {
			to = 1
		}
		r.hot.Animate(to, Quick.Get(f.Theme))
	}
	r.size = c.Constrain(geom.Sz(c.Max.W, PaletteRowHeight.Get(f.Theme)))
	return r.size
}

// Paint implements [gunim.Node].
func (r *paletteRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	if t := min(max(r.hot.Value(), 0), 1); t > 0.01 {
		hot := MenuHot.Get(th)
		hot.A = uint8(float32(hot.A) * t)
		p.RRect(geom.Rect{Max: box.Point()}, MenuRadius.Get(th)/2, paint.Solid(hot))
	}
	pad := MenuRowPadding.Get(th)
	size := TextSize.Get(th)
	item := r.item
	face := faceIn(Font, th)
	run := r.title.shape(face, item.Title, size)
	top := (box.H - run.Height()) / 2
	lead := pad
	if r.c.icons {
		lead += IconSize.Get(th) + IconGap.Get(th)
	}
	// The hint and the tick keep their places at the end, and a title too long for the rest is cut short.
	end := box.W - pad
	var hint text.Run
	if item.Hint != "" {
		hint = r.hint.shape(face, item.Hint, size*0.9)
		end -= hint.Advance + pad
	}
	if item.Checked {
		end -= IconSize.Get(th) + pad
	}
	if run.Advance > end-lead {
		run = cutRunWith(run, face.Shape("…", size), end-lead)
	}
	// The matched letters sit on marks, joined where they run on.
	mark := PaletteMark.Get(th)
	at := r.at
	for i := 0; i < len(at); {
		j := i
		for j+1 < len(at) && at[j+1] == at[j]+1 {
			j++
		}
		// Carets keep the whole title's places, so marks past a cut stop at its end.
		x0, x1 := min(run.CaretX(at[i]), run.Advance), min(run.CaretX(at[j]+1), run.Advance)
		if x1 > x0 {
			p.RRect(geom.Rc(lead+x0-1, top, x1-x0+2, run.Height()), 3, paint.Solid(mark))
		}
		i = j + 1
	}
	ink := Ink.Get(th)
	if item.Problem {
		ink = DialogProblem.Get(th)
	}
	if item.Icon != nil {
		s := IconSize.Get(th)
		paintIcon(p, th, item.Icon, geom.Rc(pad, (box.H-s)/2, s, s), ink, 1)
	}
	run.Paint(p, geom.Pt(lead, top), ink)
	if item.Hint != "" {
		hint.Paint(p, geom.Pt(box.W-pad-hint.Advance, (box.H-hint.Height())/2), PaletteHint.Get(th))
	}
	if item.Checked {
		s := IconSize.Get(th)
		paintIcon(p, th, icon.Check, geom.Rc(end+pad, (box.H-s)/2, s, s), ink, 1)
	}
	if x := lead + run.Advance + size*0.8; item.Detail != "" && end-x > size {
		detail := r.detail.shape(face, item.Detail, size*0.85)
		if detail.Advance > end-x {
			detail = cutRunWith(detail, face.Shape("…", detail.Size), end-x)
		}
		detail.Paint(p, geom.Pt(x, (box.H-detail.Height())/2), PaletteHint.Get(th))
	}
}

// Handle implements [gunim.Handler]: the pointer highlights and picks.
func (r *paletteRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		// A move alone: the palette opening under a pointer at rest, or
		// the rows shifting under it as the query narrows, leaves the
		// highlight on the best match, where Enter picks it.
		if r.refresh() && r.c.hot != r.place {
			r.c.hot = r.place
			r.c.light(u)
		}
		return false
	case input.PointerDown:
		r.click.Press(e, 0)
		return true
	case input.PointerUp:
		// A click picks this row's own item, while the palette still
		// holds it.
		_, held := r.c.place[r.key]
		if r.click.Release(e, over(e.Pos, r.size)) && held {
			r.c.p.choose(r.index, e.Mods.Has(input.ModControl), u)
		}
		return true
	}
	return false
}

// item returns item i, counting Typed's items after Items.
func (p *Palette) item(i int) PaletteItem {
	if i < len(p.Items) {
		return p.Items[i]
	}
	if k := i - len(p.Items); k < len(p.typedItems) {
		return p.typedItems[k]
	}
	return PaletteItem{}
}

// hasIcons reports whether any of items has an icon, so the titles leave room for them.
func hasIcons(items []PaletteItem) bool {
	for _, it := range items {
		if it.Icon != nil {
			return true
		}
	}
	return false
}
