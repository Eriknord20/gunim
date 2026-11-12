package widget

import (
	"hash/fnv"
	"image/color"
	"slices"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// NavList tokens.
var (
	// NavRowHeight is a row's least height, NavRowRoom the room above and below its line of text together, which
	// makes a row taller than that under large text, and NavGap the room between rows.
	NavRowHeight = theme.Length("nav.row", 40)
	NavRowRoom   = theme.Length("nav.row.room", 22)
	NavGap       = theme.Length("nav.gap", 2)
	// NavPadding is the room either end of a row, and NavRadius rounds its pill and hover.
	NavPadding = theme.Length("nav.padding", 12)
	NavRadius  = theme.Length("nav.radius", 6)
	// NavMark and NavMarkWidth are the height and width of the accent mark at the pill's left.
	NavMark      = theme.Length("nav.mark", 16)
	NavMarkWidth = theme.Length("nav.mark.width", 3)
	// NavWidth is the list's width where it is given no bound.
	NavWidth = theme.Length("nav.width", 220)
	// NavHover lights the row under the pointer, and NavSelected fills the pill behind the chosen row.
	NavHover    = theme.Color("nav.hover", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e})
	NavSelected = theme.Color("nav.selected", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16})
	// NavSelectedInk colours the chosen row's label and icon where a theme sets it, as a contrast theme does, and
	// else [Ink].
	NavSelectedInk = theme.Foreground("nav.selected.ink", color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0xff})
)

// NavItem is one row of a [NavList].
type NavItem struct {
	// Key names the row across SetItems, so the chosen row, the focus ring, a badge and a screen reader's hold stay
	// on it as rows move. A row with no Key keeps its place. Keys are meant to be unique: where one repeats, the first
	// row with it, old or new, is the one followed, and the others count as rows with no match.
	Key   Key
	Label string
	Icon  *icon.Icon
	// Count shows in a badge at the row's end, as the number of problems on a page. Zero shows none. CountLabel is
	// what a screen reader says for it, such as "4 files", and the number alone when empty.
	Count      int
	CountLabel string
	// CountInk colours the badge, and is the theme's [Accent] when unset.
	CountInk theme.Token[color.NRGBA]
	// Trail is a quiet icon at the row's end, as for a row that opens a window of its own.
	Trail *icon.Icon
	// Acts makes the row act without being chosen, as one that opens a window of its own does: a click, Enter or
	// Space sends OnActivate, and the pill stays where it is.
	Acts bool
	// Foot puts the row, and the rows after it, at the bottom of the list, when the list is taller than its rows.
	Foot bool
}

// NavList is a column of icon-and-label rows for moving between an application's pages, one of them chosen.
//
// A pill glides behind the chosen row, with an accent mark at its left, and the chosen label turns bold. A row's count
// grows in and out in a badge at its end. A click chooses a row. With focus, Up and Down move the focus ring between
// rows, Home and End go to the ends, and Enter or Space chooses the row with the ring. A row marked Acts acts in
// place of being chosen. Disabled, it fades faint and takes no clicks or keys. A label cut short shows whole in a
// tooltip. Rows grow with large text.
type NavList struct {
	Control
	// Label names the list for a screen reader, such as "Pages". It is not drawn, and shows no popup as Tooltip does;
	// the list takes Tooltip as its name where Label is empty.
	Label string
	// OnSelect runs on the UI goroutine when the user chooses a row; a non-nil result is sent to the application as
	// the list's intent.
	OnSelect func(i int, u *gunim.UI) gunim.Intent
	// OnActivate runs on the UI goroutine when the user activates a row marked Acts, as one that opens a window; a
	// non-nil result is sent to the application as the list's intent.
	OnActivate func(i int, u *gunim.UI) gunim.Intent

	items    []NavItem
	selected int
	// foot is the first row at the bottom, or -1.
	foot int
	// cursor is the row the focus ring is on, and ringY where the ring is drawn, gliding between rows.
	cursor int
	ringY  *anim.Float
	// lag is how far the pill trails the chosen row, springing to nothing.
	lag *anim.Float
	// hot is the row under the pointer, or -1; hotV lights it, and was is the row left, fading out with wasV.
	hot, was   int
	hotV, wasV *anim.Float
	// badges hold the badges growing in or out, by row, until they settle.
	badges map[int]*navBadge
	click  Clicker
	tip    PartTip
	shaped map[int]*navText
	// size is the list's size from the last layout, and shown the rows painted last.
	size     geom.Size
	from, to int
	// rowH and gap are a row's height and the gap after it, from the last layout, and line measures a line of text.
	rowH, gap float32
	line      shapedText
}

// navBadge is a badge growing in or shrinking out, keeping its words while it goes.
type navBadge struct {
	shown *anim.Float
	text  string
}

// navText is a row's shaped words.
type navText struct {
	label, count, ell shapedText
	cut               bool
}

// NewNavList returns a list of items, the first chosen.
func NewNavList(items ...NavItem) *NavList {
	n := &NavList{Control: newControl(), hot: -1, was: -1, ringY: anim.NewFloat(0), lag: anim.NewFloat(0),
		hotV: anim.NewFloat(0), wasV: anim.NewFloat(0), badges: map[int]*navBadge{}, shaped: map[int]*navText{}}
	n.Add(n.ringY, n.lag, n.hotV, n.wasV)
	n.SetItems(items, nil)
	return n
}

// Items returns the rows.
func (n *NavList) Items() []NavItem { return n.items }

// SetItems makes items the rows, and sends no intent. The chosen row and the focus ring stay on their rows, found by
// Key, and the ring glides with its row. Where their row goes, they keep its place: the row now there is chosen, or
// has the ring, kept inside the list. Once laid out, a count that comes or goes grows or shrinks its badge.
func (n *NavList) SetItems(items []NavItem, u *gunim.UI) {
	from := n.oldRows(items)
	badges := map[int]*navBadge{}
	for i, was := range from {
		if b := n.badges[was]; was >= 0 && b != nil {
			badges[i] = b
		}
	}
	if n.laid && u != nil {
		for i, was := range from {
			if was < 0 {
				continue
			}
			had, has := n.items[was].Count > 0, items[i].Count > 0
			if had == has {
				continue
			}
			b := badges[i]
			if b == nil {
				b = &navBadge{shown: anim.NewFloat(anim.OnOff(had))}
				badges[i] = b
				n.Add(b.shown)
			}
			if had {
				b.text = strconv.Itoa(n.items[was].Count)
				b.shown.Animate(0, Settle.Get(u.Theme()))
			} else {
				b.shown.Animate(1, Quick.Get(u.Theme()))
			}
		}
	}
	for i, b := range n.badges {
		if !slices.Contains(from, i) {
			n.Remove(b.shown)
		}
	}
	n.badges = badges
	selected, cursor := n.selected, n.cursor
	n.selected, n.cursor = 0, 0
	for i, was := range from {
		if was == selected {
			n.selected = i
		}
		if was == cursor {
			n.cursor = i
		}
	}
	if !slices.Contains(from, selected) {
		n.selected = min(max(selected, 0), max(len(items)-1, 0))
	}
	if !slices.Contains(from, cursor) {
		n.cursor = min(max(cursor, 0), max(len(items)-1, 0))
	}
	n.items = items
	n.foot = -1
	for i, it := range items {
		if it.Foot {
			n.foot = i
			break
		}
	}
	clear(n.shaped)
	if n.laid {
		// The ring goes with its row, gliding on where it was gliding.
		if to := n.top(n.cursor); n.ringY.Active() && u != nil {
			n.ringY.Animate(to, Quick.Get(u.Theme()))
		} else {
			n.ringY.Jump(to)
		}
	}
	u.Invalidate()
}

// oldRows returns, for each of items, the row it was before: the row with its Key, or for a row with no Key the row in
// its place, or -1 for none.
func (n *NavList) oldRows(items []NavItem) []int {
	byKey := map[Key]int{}
	for i, it := range n.items {
		if _, seen := byKey[it.Key]; it.Key != "" && !seen {
			byKey[it.Key] = i
		}
	}
	from := make([]int, len(items))
	for i, it := range items {
		from[i] = -1
		switch was, ok := byKey[it.Key]; {
		case it.Key != "" && ok:
			from[i] = was
			// Only the first new row with a key follows the old one.
			delete(byKey, it.Key)
		case it.Key == "" && i < len(n.items) && n.items[i].Key == "":
			from[i] = i
		}
	}
	return from
}

// Cursor returns the row the focus ring is on.
func (n *NavList) Cursor() int { return n.cursor }

// SetCursor puts the focus ring on row i, where it shows with focus, and sends no intent. Once laid out the ring glides
// there and the row scrolls into view; before that, or with a nil u, it jumps.
func (n *NavList) SetCursor(i int, u *gunim.UI) {
	if i < 0 || i >= len(n.items) {
		return
	}
	if !n.laid || u == nil {
		n.cursor = i
		n.ringY.Jump(n.top(i))
		return
	}
	n.moveCursor(i, u)
}

// Selected returns the chosen row, and 0 for a list with no rows.
func (n *NavList) Selected() int { return n.selected }

// SetSelected chooses row i and sends no intent, and brings the focus ring along where it was on the chosen row. Once
// laid out the pill glides to it; before that, or with a nil u, it jumps.
func (n *NavList) SetSelected(i int, u *gunim.UI) {
	if i < 0 || i >= len(n.items) || i == n.selected {
		return
	}
	from := n.top(n.selected) + n.lag.Value()
	// A ring left on the chosen row goes along with it.
	if n.cursor == n.selected {
		n.SetCursor(i, u)
	}
	n.selected = i
	if !n.laid || u == nil {
		n.lag.Jump(0)
		return
	}
	n.lag.Jump(from - n.top(i))
	n.lag.Animate(0, Settle.Get(u.Theme()))
	u.Invalidate()
}

// choose chooses row i, or acts for a row marked Acts, and tells the application.
func (n *NavList) choose(i int, u *gunim.UI) {
	n.moveCursor(i, u)
	if i >= 0 && i < len(n.items) && n.items[i].Acts {
		if n.OnActivate != nil {
			act(u, n, gunim.CuePress, n.OnActivate, i)
		}
		return
	}
	if i < 0 || i >= len(n.items) || i == n.selected {
		return
	}
	n.SetSelected(i, u)
	act(u, n, gunim.CueSelect, n.OnSelect, i)
}

// moveCursor puts the focus ring on row i, and scrolls the row into view.
func (n *NavList) moveCursor(i int, u *gunim.UI) {
	if len(n.items) == 0 {
		return
	}
	n.cursor = min(max(i, 0), len(n.items)-1)
	n.ringY.Animate(n.top(n.cursor), Quick.Get(u.Theme()))
	u.RevealRect(n, geom.Rc(0, n.top(n.cursor), n.size.W, n.rowH))
	u.Invalidate()
}

// pitch is a row's height and the gap after it.
func (n *NavList) pitch() float32 { return n.rowH + n.gap }

// rows is the height the rows take, without the room the foot rows are pushed down by.
func (n *NavList) rows() float32 {
	if len(n.items) == 0 {
		return 0
	}
	return float32(len(n.items))*n.pitch() - n.gap
}

// spare is how far the foot rows sit below the rows before them.
func (n *NavList) spare() float32 {
	if n.foot < 0 {
		return 0
	}
	return max(0, n.size.H-n.rows())
}

// top is where row i starts.
func (n *NavList) top(i int) float32 {
	y := float32(i) * n.pitch()
	if n.foot >= 0 && i >= n.foot {
		y += n.spare()
	}
	return y
}

// at returns the row at y, or -1 for none, as in a gap or past the ends.
func (n *NavList) at(y float32) int {
	pitch := n.pitch()
	if pitch <= 0 || y < 0 {
		return -1
	}
	if n.foot >= 0 && y >= n.top(n.foot) {
		y -= n.spare()
	} else if n.foot >= 0 && y >= float32(n.foot)*pitch {
		return -1
	}
	i := int(y / pitch)
	if i >= len(n.items) || y-float32(i)*pitch >= n.rowH {
		return -1
	}
	return i
}

// near returns the row at or just before y, kept to the rows there are.
func (n *NavList) near(y float32) int {
	pitch := n.pitch()
	if len(n.items) == 0 || pitch <= 0 {
		return 0
	}
	var i int
	switch {
	case n.foot >= 0 && y >= n.top(n.foot):
		i = n.foot + int((y-n.top(n.foot))/pitch)
	case n.foot >= 0:
		i = min(int(max(y, 0)/pitch), n.foot-1)
	default:
		i = int(max(y, 0) / pitch)
	}
	return min(max(i, 0), len(n.items)-1)
}

// Handle implements [gunim.Handler].
func (n *NavList) Handle(e input.Event, u *gunim.UI) bool {
	n.showTip(e, u, n)
	th := u.Theme()
	if n.Disabled {
		n.setHot(-1, th)
		return n.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		n.setHot(n.at(e.Pos.Y), th)
		n.tip.Handle(e, u, n, n.tipText(n.hot))
		return false
	case input.PointerMove:
		n.setHot(n.at(e.Pos.Y), th)
		n.tip.Handle(e, u, n, n.tipText(n.hot))
		return false
	case input.PointerLeave:
		n.setHot(-1, th)
		n.tip.Handle(e, u, n, "")
		return false
	case input.PointerDown:
		n.tip.Handle(e, u, n, "")
		if e.Button != input.ButtonPrimary {
			return false
		}
		n.click.Press(e, n.at(e.Pos.Y))
	case input.PointerUp:
		if i := n.at(e.Pos.Y); n.click.Release(e, i) && i >= 0 && e.Pos.X >= 0 && e.Pos.X < n.size.W {
			n.choose(i, u)
		}
	case input.FocusGained:
		// The ring comes back on the row it was left on.
		n.ringY.Jump(n.top(n.cursor))
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) || e.Mods.Has(input.ModSuper) {
			return false
		}
		switch e.Key {
		case input.KeyUp:
			n.moveCursor(n.cursor-1, u)
		case input.KeyDown:
			n.moveCursor(n.cursor+1, u)
		case input.KeyHome:
			n.moveCursor(0, u)
		case input.KeyEnd:
			n.moveCursor(len(n.items)-1, u)
		case input.KeyEnter, input.KeyKPEnter, input.KeySpace:
			n.choose(n.cursor, u)
		default:
			return false
		}
	default:
		if !n.ringFollows(e, th) {
			return false
		}
	}
	u.Invalidate()
	return true
}

// setHot lights row i, under the pointer, or none for -1: it lights quickly, and the row left fades out more slowly.
func (n *NavList) setHot(i int, th *theme.Live) {
	if i == n.hot {
		return
	}
	if n.hot >= 0 {
		n.was = n.hot
		n.wasV.Jump(n.hotV.Value())
		n.wasV.Animate(0, Settle.Get(th))
	}
	n.hot = i
	n.hotV.Jump(0)
	if i >= 0 {
		n.hotV.Animate(1, Quick.Get(th))
	}
}

// tipText is row i's whole label where it was cut short.
func (n *NavList) tipText(i int) string {
	if t := n.shaped[i]; t != nil && t.cut && i < len(n.items) {
		return n.items[i].Label
	}
	return ""
}

// Step implements [anim.Stepper]: it drops the badges that have settled.
func (n *NavList) Step(dt time.Duration) bool {
	moving := n.Group.Step(dt)
	for i, b := range n.badges {
		if !b.shown.Active() {
			n.Remove(b.shown)
			delete(n.badges, i)
		}
	}
	return moving
}

// Layout implements [gunim.Node]: as tall as its rows, or as tall as it is given with foot rows, and as wide as it
// is given.
func (n *NavList) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	line := n.line.shape(faceIn(Font, th), "Ag", TextSize.Get(th)).Height()
	n.rowH, n.gap = max(NavRowHeight.Get(th), line+NavRowRoom.Get(th)), NavGap.Get(th)
	h := n.rows()
	if n.foot >= 0 && c.Max.H > 0 {
		h = max(h, c.Max.H)
	}
	w := c.Max.W
	if w <= 0 {
		w = NavWidth.Get(th)
	}
	if !n.laid {
		n.lag.Jump(0)
	}
	n.follow(th)
	n.size = c.Constrain(geom.Sz(w, h))
	if !n.ringY.Active() {
		n.ringY.Jump(n.top(n.cursor))
	}
	return n.size
}

// Paint implements [gunim.Node]: the pill, then the rows in view over it.
func (n *NavList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer n.faint(p, box)()
	th := f.Theme
	rowH := n.rowH
	radius := NavRadius.Get(th)
	view := geom.Rect{Max: box.Point()}
	if v, ok := p.Visible(); ok {
		view.Min.Y, view.Max.Y = max(view.Min.Y, v.Min.Y), min(view.Max.Y, v.Max.Y)
	}
	if len(n.items) > 0 {
		y := n.top(n.selected) + n.lag.Value()
		p.RRect(geom.Rc(0, y, box.W, rowH), radius, paint.Solid(NavSelected.Get(th)))
		mark, mw := min(NavMark.Get(th), rowH), NavMarkWidth.Get(th)
		p.RRect(geom.Rc(0, y+(rowH-mark)/2, mw, mark), mw/2, paint.Solid(Accent.Get(th)))
	}
	n.from, n.to = n.near(view.Min.Y), n.near(view.Max.Y)
	for i := range n.shaped {
		if i < n.from || i > n.to {
			delete(n.shaped, i)
		}
	}
	for i := n.from; i <= n.to && i < len(n.items); i++ {
		top := n.top(i)
		if top >= view.Max.Y || top+rowH <= view.Min.Y {
			continue
		}
		n.paintRow(p, th, i, geom.Rc(0, top, box.W, rowH))
	}
	if len(n.items) > 0 {
		n.paintRing(p, geom.Rc(0, n.ringY.Value(), box.W, rowH), radius, th)
	}
}

// paintRow paints row i in r: its hover, icon, label and what sits at its end.
func (n *NavList) paintRow(p *paint.Painter, th *theme.Live, i int, r geom.Rect) {
	it := n.items[i]
	radius := NavRadius.Get(th)
	lit := float32(0)
	switch i {
	case n.hot:
		lit = n.hotV.Value()
	case n.was:
		lit = n.wasV.Value()
	}
	if i == n.selected {
		lit = 0
	}
	if lit > 0.01 {
		p.RRect(r, radius, paint.Solid(paint.Faded(NavHover.Get(th), min(lit, 1))))
	}
	t := n.shaped[i]
	if t == nil {
		t = &navText{}
		n.shaped[i] = t
	}
	pad := NavPadding.Get(th)
	ink := Ink.Get(th)
	quiet := Placeholder.Get(th)
	// On the pill, where a theme sets an ink for it, the row takes that ink as the pill covers it.
	on := float32(0)
	if th.Sets(NavSelectedInk.Key()) {
		pill := n.top(n.selected) + n.lag.Value()
		on = max(0, min(r.Max.Y, pill+n.rowH)-max(r.Min.Y, pill)) / max(n.rowH, 1)
		sel := NavSelectedInk.Get(th)
		ink, quiet = anim.Mix(anim.ColorCodec, ink, sel, on), anim.Mix(anim.ColorCodec, quiet, sel, on)
	}
	x, right := r.Min.X+pad, r.Max.X-pad
	mid := r.Min.Y + r.Size().H/2
	size := IconSize.Get(th)
	if it.Icon != nil {
		PaintIcon(p, th, it.Icon, geom.Rc(x, mid-size/2, size, size), ink)
		x += size + ControlGap.Get(th)
	}
	shown, words := anim.OnOff(it.Count > 0), strconv.Itoa(it.Count)
	if b := n.badges[i]; b != nil {
		shown = min(max(b.shown.Value(), 0), 1)
		if it.Count <= 0 {
			words = b.text
		}
	}
	switch {
	case shown > 0.001:
		bt := BadgeSize.Get(th)
		run := t.count.shape(faceIn(Font, th), words, bt)
		bh := BadgeHeight.Get(th)
		w := (run.Advance + 2*BadgePadding.Get(th)) * shown
		badge := geom.Rc(right-w, mid-bh/2, w, bh)
		bink := Accent.Get(th)
		if it.CountInk.Key() != "" {
			bink = it.CountInk.Get(th)
		}
		if on > 0 {
			bink = anim.Mix(anim.ColorCodec, bink, ink, on)
		}
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: badge, Opacity: shown})()
			fill := bink
			fill.A = uint8(float32(fill.A) * min(max(BadgeTint.Get(th), 0), 1))
			p.RRect(badge, bh/2, paint.Solid(fill))
			run.PaintFaded(p, geom.Pt(badge.Min.X+BadgePadding.Get(th), mid-run.Height()/2), bink,
				max(w-BadgePadding.Get(th), 0), TextFade.Get(th))
		}()
		right -= w + ControlGap.Get(th)
	case it.Trail != nil:
		PaintIcon(p, th, it.Trail, geom.Rc(right-size, mid-size/2, size, size), quiet)
		right -= size + ControlGap.Get(th)
	}
	face := faceIn(Font, th)
	if i == n.selected {
		face = faceIn(BoldFont, th)
	}
	run := t.label.shape(face, it.Label, TextSize.Get(th))
	t.cut = run.Advance > right-x
	if right > x {
		run = fitRun(run, &t.ell, right-x)
		run.Paint(p, geom.Pt(x, mid-run.Height()/2), ink)
	}
}

// Access implements [gunim.Accessible]: a list with an item for each row in view, the chosen one selected.
func (n *NavList) Access() access.Info {
	info := access.Info{Role: access.RoleList, Name: n.accessName(n.Label), State: n.accessState()}
	// Active counts the parts, which start at the first row in view.
	if n.cursor >= n.from && n.cursor <= n.to && n.cursor < len(n.items) {
		info.Active = n.cursor - n.from + 1
	}
	for i := n.from; i <= n.to && i < len(n.items); i++ {
		it := n.items[i]
		part := access.Info{Role: access.RoleListItem, Name: it.Label, State: n.accessState(), Key: partKey(it.Key, i),
			Actions: []string{access.ActionPress}}
		part.Bounds = geom.Rc(0, n.top(i), n.size.W, n.rowH)
		switch {
		case it.Count > 0 && it.CountLabel != "":
			part.Description = it.CountLabel
		case it.Count > 0:
			part.Description = strconv.Itoa(it.Count)
		}
		if i == n.selected {
			part.State |= access.StateSelected
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// partKey is the key of row i's part for a screen reader: from the row's Key, so a hold on it follows the row, and
// from its place for a row with none.
func partKey(k Key, i int) uint64 {
	if k == "" {
		return uint64(i) + 1
	}
	h := fnv.New64a()
	h.Write([]byte(k))
	// The top bit keeps it apart from the keys of rows with none.
	return h.Sum64() | 1<<63
}

// AccessAct implements [gunim.AccessActor]: pressing a row chooses it.
func (n *NavList) AccessAct(r access.Request, u *gunim.UI) bool {
	i := n.from + r.Part
	if r.Action != access.ActionPress || r.Part < 0 || i >= len(n.items) || i > n.to || n.Disabled {
		return false
	}
	n.choose(i, u)
	return true
}
