package plugui

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"slices"
	"strconv"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// A PresetSource is what a [PresetBrowser] lists, and what it plays a
// preset with: a plugin's presets, and the host's.
type PresetSource interface {
	// Presets returns the presets, bank by bank and each bank group by
	// group, in the order the browser lists them.
	Presets() []plug.Preset
	// Current returns the preset playing, or false where none is.
	Current() (plug.Preset, bool)
	// Pick plays p. Picked again, the preset playing, it may only play
	// a note of it.
	Pick(p plug.Preset, u *gunim.UI)
}

// PresetBrowser picks a preset: a search field over two columns, the
// groups of each bank, and the presets of the group picked. A search
// lists the presets it finds in every group, under their groups' names,
// and counts them beside each group; a click on a group then scrolls to
// its presets. A click on a preset picks it.
//
// Up and Down play the preset before or after the one playing. In the groups they show the group before or after
// instead. Home, End, Page Up and Page Down move in the column with the keyboard. Tab reaches each column, and its
// focus ring fades in.
type PresetBrowser struct {
	anim.Group
	src PresetSource
	// BankIcon is the icon of a bank's caption; nil shows a library's.
	BankIcon func(bank string) *icon.Icon
	// Picked hears a preset picked, after the source plays it.
	Picked func(u *gunim.UI)

	// list is the presets as the source last gave them.
	list []plug.Preset
	// bank and group are the group the presets column shows, while no
	// search runs, and query the search.
	bank, group string
	query       string
	field       *widget.TextField
	groups      *groupList
	presets     *presetList
	gs, ps      *widget.Scroll
	// colH is how tall the columns show, as last laid out.
	colH float32
	// revealing is a reveal asked for and not yet laid out, revealJump says it puts new content in place at once,
	// and laid that the columns have had room before.
	revealing, revealJump, laid bool
}

// NewPresetBrowser returns a browser of src's presets.
func NewPresetBrowser(src PresetSource) *PresetBrowser {
	b := &PresetBrowser{src: src}
	b.field = widget.NewTextField()
	b.field.Icon = icon.Search
	b.field.Clearable = true
	b.field.OnChange = func(text string, u *gunim.UI) gunim.Intent {
		b.query = strings.TrimSpace(text)
		b.fill(u)
		b.ps.JumpTo(0)
		return nil
	}
	b.groups = &groupList{b: b, hover: -1, pill: anim.NewFloat(0), lit: anim.NewFloat(0), hoverY: anim.NewFloat(0),
		ring: anim.NewFloat(0), press: rowPress{down: anim.NewFloat(0)}}
	b.presets = &presetList{b: b, hover: -1, pill: anim.NewFloat(0), shown: anim.NewFloat(0), lit: anim.NewFloat(0),
		hoverY: anim.NewFloat(0), ring: anim.NewFloat(0), press: rowPress{click: widget.Clicker{Repeats: true}, down: anim.NewFloat(0)}}
	b.Add(b.groups.pill, b.groups.lit, b.groups.hoverY, b.groups.ring, b.groups.press.down, b.presets.pill,
		b.presets.shown, b.presets.lit, b.presets.hoverY, b.presets.ring, b.presets.press.down)
	b.gs, b.ps = widget.NewScroll(b.groups), widget.NewScroll(b.presets)
	b.Refresh(nil)
	return b
}

// Refresh reads the source's presets afresh, as after one is kept or
// let go, and shows the one playing.
func (b *PresetBrowser) Refresh(u *gunim.UI) {
	b.list = b.src.Presets()
	b.field.Placeholder = fmt.Sprintf("Search %d presets", len(b.list))
	if cur, ok := b.src.Current(); ok {
		b.bank, b.group = cur.Bank, cur.Group
	}
	if !b.hasGroup(b.bank, b.group) {
		b.bank, b.group = "", ""
		if len(b.list) > 0 {
			b.bank, b.group = b.list[0].Bank, b.list[0].Group
		}
	}
	b.fill(u)
}

// hasGroup reports whether a preset is filed in group of bank.
func (b *PresetBrowser) hasGroup(bank, group string) bool {
	for _, p := range b.list {
		if p.Bank == bank && p.Group == group {
			return true
		}
	}
	return false
}

// current is the preset playing, or false.
func (b *PresetBrowser) current() (plug.Preset, bool) { return b.src.Current() }

// banks are the banks, in the order the presets come.
func (b *PresetBrowser) banks() []string {
	var out []string
	for _, p := range b.list {
		if len(out) == 0 || out[len(out)-1] != p.Bank {
			out = append(out, p.Bank)
		}
	}
	return out
}

// search returns the presets that match the query, word by word.
func (b *PresetBrowser) search() []plug.Preset {
	words := strings.Fields(strings.ToLower(b.query))
	var out []plug.Preset
	for _, p := range b.list {
		if p.Matches(words) {
			out = append(out, p)
		}
	}
	return out
}

// fill lists the groups and the presets as the search and the group
// picked have them, and springs the pills to the current preset and its
// group.
func (b *PresetBrowser) fill(u *gunim.UI) {
	b.groups.fill()
	b.presets.fill()
	b.glide(u)
}

// glide springs the pills to the group shown and the preset playing.
func (b *PresetBrowser) glide(u *gunim.UI) {
	sp := anim.Snappy
	if u != nil {
		sp = widget.Quick.Get(u.Theme())
	}
	if i := b.groups.index(b.bank, b.group); i >= 0 {
		b.groups.pill.Animate(float32(i), sp)
	}
	if i := b.presets.current(); i >= 0 {
		if b.presets.shown.Value() < 0.01 {
			b.presets.pill.Jump(float32(i))
		}
		b.presets.pill.Animate(float32(i), sp)
		b.presets.shown.Animate(1, sp)
	} else {
		b.presets.shown.Animate(0, sp)
	}
	if u != nil {
		u.Invalidate()
	}
}

// Follow shows the preset playing, as set elsewhere: its group, while
// no search runs, scrolled to.
func (b *PresetBrowser) Follow(u *gunim.UI) {
	cur, ok := b.current()
	if b.query == "" && ok && (cur.Bank != b.bank || cur.Group != b.group) && b.hasGroup(cur.Bank, cur.Group) {
		b.bank, b.group = cur.Bank, cur.Group
		b.fill(u)
		b.jumpToCurrent()
	} else {
		b.glide(u)
	}
	b.inView(u)
}

// Reveal implements [gunim.Revealer]: as Tab brings the keyboard to a column, the columns show the group shown and
// the preset playing, in place of the column's top.
func (b *PresetBrowser) Reveal(_ geom.Rect, u *gunim.UI) { b.inView(u) }

// ShowPlaying centres the preset playing and its group at the next layout with room for them: at once on the
// browser's first such layout, and gliding after.
func (b *PresetBrowser) ShowPlaying(u *gunim.UI) {
	b.revealing = true
	u.Invalidate()
}

// centre scrolls the columns so the group shown and the preset playing stand in their middles, at once or gliding
// on Quick.
func (b *PresetBrowser) centre(jump bool, th *theme.Live) {
	for _, c := range [...]struct {
		s    *widget.Scroll
		rows []listRow
		i    int
	}{{b.gs, b.groups.rows, b.groups.index(b.bank, b.group)}, {b.ps, b.presets.rows, b.presets.current()}} {
		if c.i < 0 {
			continue
		}
		r := c.rows[c.i]
		y := min(max(r.y+r.h/2-b.colH/2, 0), max(listHeight(c.rows)-b.colH, 0))
		if jump {
			c.s.JumpTo(y)
		} else {
			c.s.ScrollTo(y, widget.Quick.Get(th))
		}
	}
}

// inView scrolls the columns to the group shown and the preset playing.
func (b *PresetBrowser) inView(u *gunim.UI) {
	if u == nil {
		return
	}
	if i := b.groups.index(b.bank, b.group); i >= 0 {
		b.gs.RevealContent(b.groups.rowRect(i), u)
	}
	if i := b.presets.current(); i >= 0 {
		b.ps.RevealContent(b.presets.rowRect(i), u)
	}
}

// jumpToCurrent puts the presets column, new content, where the preset
// playing shows, at once, as a group picked opens at its top. The
// column's scroll still holds the old group's height, so a scroll
// there would stop short of a row past it.
func (b *PresetBrowser) jumpToCurrent() {
	if b.revealing {
		b.revealJump = true
		return
	}
	y := float32(0)
	if i := b.presets.current(); i >= 0 {
		const room = 8
		r := b.presets.rows[i]
		y = max(0, r.y+r.h+room-b.colH)
	}
	b.ps.JumpTo(y)
}

// showGroup shows the presets of a group: while no search runs, the
// group's alone; during one, scrolled to the group's.
func (b *PresetBrowser) showGroup(bank, group string, u *gunim.UI) {
	if b.query != "" {
		b.bank, b.group = bank, group
		b.glide(u)
		if i := b.presets.headerIndex(bank, group); i >= 0 {
			b.ps.ScrollTo(b.presets.rowRect(i).Min.Y, widget.Settle.Get(u.Theme()))
		}
		return
	}
	if bank == b.bank && group == b.group {
		return
	}
	b.bank, b.group = bank, group
	b.fill(u)
	b.ps.JumpTo(0)
}

// pick plays p.
func (b *PresetBrowser) pick(p plug.Preset, u *gunim.UI) {
	b.src.Pick(p, u)
	b.glide(u)
	if b.Picked != nil {
		b.Picked(u)
	}
}

// Skip plays the preset by from the one playing, in the order the browser lists them, on into the group after or
// before and round from the last to the first; during a search, through what it finds. With another group shown than
// the playing preset's, it plays the first preset of that group, or its last going back. The browser shows the group
// of the preset it plays.
func (b *PresetBrowser) Skip(by int, u *gunim.UI) {
	list, i := b.listed()
	if b.query == "" {
		list, i = b.all()
	}
	if len(list) == 0 {
		return
	}
	shown := func(p plug.Preset) bool { return p.Bank == b.bank && p.Group == b.group }
	if i < 0 || b.query == "" && !shown(list[i]) {
		// Step into the group shown, as one clicked: from just before its first preset, or just after its last.
		lo, hi := slices.IndexFunc(list, shown), 0
		for j := range list {
			if shown(list[j]) {
				hi = j
			}
		}
		if i = lo - 1; by < 0 {
			i = hi + 1
		}
		if lo < 0 {
			i = -1
			if by < 0 {
				i = 0
			}
		}
	}
	i = ((i+by)%len(list) + len(list)) % len(list)
	b.pick(list[i], u)
	b.Follow(u)
}

// all returns every preset, group by group in the order the groups column lists them, and the index of the one
// playing among them, or -1.
func (b *PresetBrowser) all() (list []plug.Preset, at int) {
	type key struct{ bank, group string }
	of := map[key][]plug.Preset{}
	for _, p := range b.list {
		k := key{p.Bank, p.Group}
		of[k] = append(of[k], p)
	}
	at = -1
	cur, ok := b.current()
	list = make([]plug.Preset, 0, len(b.list))
	for _, r := range b.groups.rows {
		if r.header {
			continue
		}
		for _, p := range of[key{r.bank, r.group}] {
			if ok && at < 0 && p.Same(cur) {
				at = len(list)
			}
			list = append(list, p)
		}
	}
	return list, at
}

// listed returns the presets the column lists, and the index of the one playing among them, or -1.
func (b *PresetBrowser) listed() (list []plug.Preset, at int) {
	at = -1
	cur, ok := b.current()
	for _, r := range b.presets.rows {
		if !r.header {
			if ok && at < 0 && r.p.Same(cur) {
				at = len(list)
			}
			list = append(list, r.p)
		}
	}
	return list, at
}

// page is how many rows a page key moves: a column's height, less a row.
func (b *PresetBrowser) page() int { return max(int(b.colH/rowH)-1, 1) }

// moveTo is where key moves a cursor at of n rows, a page of page apart: a row on, a page on or an end, kept inside
// the rows; false for any other key.
func moveTo(key input.Key, at, n, page int) (int, bool) {
	switch key {
	case input.KeyUp:
		at--
	case input.KeyDown:
		at++
	case input.KeyPageUp:
		at -= page
	case input.KeyPageDown:
		at += page
	case input.KeyHome:
		at = 0
	case input.KeyEnd:
		at = n - 1
	default:
		return 0, false
	}
	return min(max(at, 0), n-1), true
}

// followRing springs ring to show the focus ring as e says, and reports whether e was a focus event.
func followRing(ring *anim.Float, e input.Event, u *gunim.UI) bool {
	on := float32(0)
	switch e := e.(type) {
	case input.FocusRing:
		if e.On {
			on = 1
		}
	case input.FocusLost:
	default:
		return false
	}
	ring.Animate(on, widget.RingFade.Get(u.Theme()))
	u.Invalidate()
	return true
}

// Handle implements [gunim.Handler]: Up and Down, with the keyboard in the browser, play the preset before or after
// the one playing.
func (b *PresetBrowser) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok || k.Mods != 0 || k.Key != input.KeyUp && k.Key != input.KeyDown {
		return false
	}
	by := 1
	if k.Key == input.KeyUp {
		by = -1
	}
	b.Skip(by, u)
	return true
}

// Children implements [gunim.Composite].
func (b *PresetBrowser) Children() []gunim.Node { return []gunim.Node{b.field, b.gs, b.ps} }

// The browser's sizes.
const (
	fieldH  = 32
	rowH    = 26
	headH   = 30
	colGap  = 10
	groupsW = 0.42
)

// Layout implements [gunim.Node]: the field across the top, and the
// columns under it.
func (b *PresetBrowser) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(geom.Sz(size.W, fieldH)))
	kids.At(0).Place(geom.Point{})
	top := float32(fieldH + colGap)
	h := max(size.H-top, 0)
	b.colH = h
	reveal := b.revealing && h >= rowH
	jump := reveal && (!b.laid || b.revealJump)
	if jump {
		b.centre(true, f.Theme)
	}
	gw := float32(int(size.W * groupsW))
	kids.At(1).Layout(gunim.Tight(geom.Sz(gw, h)))
	kids.At(1).Place(geom.Pt(0, top))
	kids.At(2).Layout(gunim.Tight(geom.Sz(max(size.W-gw-colGap, 0), h)))
	kids.At(2).Place(geom.Pt(gw+colGap, top))
	if reveal && !jump {
		b.centre(false, f.Theme)
	}
	if h >= rowH {
		b.laid = true
		b.revealing, b.revealJump = false, false
	}
	return size
}

// Paint implements [gunim.Node]: the columns in wells, the one with the keyboard ringed.
func (b *PresetBrowser) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	ink := widget.Ink.Get(f.Theme)
	top := float32(fieldH + colGap)
	gw := float32(int(box.W * groupsW))
	wells := [...]geom.Rect{geom.Rc(0, top, gw, box.H-top), geom.Rc(gw+colGap, top, box.W-gw-colGap, box.H-top)}
	for _, r := range wells {
		p.RRect(r, 10, paint.Solid(paint.Faded(ink, 0.035)))
	}
	for i := range kids.Len() {
		kids.At(i).Paint(p)
	}
	widget.FocusRing(p, wells[0], 10, b.groups.ring.Value(), f.Theme)
	widget.FocusRing(p, wells[1], 10, b.presets.ring.Value(), f.Theme)
}

// bankIcon draws the icon of bank in r.
func (b *PresetBrowser) bankIcon(p *paint.Painter, f gunim.Frame, bank string, r geom.Rect, c color.NRGBA) {
	ic := icon.Library
	if b.BankIcon != nil {
		ic = b.BankIcon(bank)
	}
	widget.PaintIcon(p, f.Theme, ic, r, c)
}

// listRow is a row of a column: a bank's or a group's caption, or an
// item.
type listRow struct {
	header      bool
	bank, group string
	label       string
	count       int
	p           plug.Preset
	y, h        float32
	// key names the row by what it shows, apart from every other row of
	// its column (keyRows).
	key uint64
}

// groupList is the column of groups, under their banks' captions, each
// with how many presets it holds, or the search finds in it.
type groupList struct {
	b     *PresetBrowser
	rows  []listRow
	hover int
	// parts are the keys of the rows the access tree last listed.
	parts []uint64
	// pill is the row the pill marking the group shown stands on, lit
	// how far the pill under the pointer shows, at hoverY, and ring how
	// far the focus ring shows.
	pill, lit, hoverY, ring *anim.Float
	size                    geom.Size
	press                   rowPress
}

// rowPress follows a click on a column's rows: the row pressed lights a little more, and the click lands on the
// release over the same row.
type rowPress struct {
	click widget.Clicker
	// down is how far the press shows, and held says it is still down.
	down *anim.Float
	held bool
}

// start takes e, a press on row i or on none for a negative i, and reports whether it took it.
func (r *rowPress) start(e input.PointerDown, i int, u *gunim.UI) bool {
	r.click.Press(e, i)
	if e.Button != input.ButtonPrimary {
		return false
	}
	if r.held = i >= 0; !r.held {
		r.down.Animate(0, widget.Quick.Get(u.Theme()))
		return false
	}
	r.down.Animate(1, widget.Quick.Get(u.Theme()))
	u.Invalidate()
	return true
}

// end takes e, the release over row i, and reports whether the click lands and whether a press was held.
func (r *rowPress) end(e input.PointerUp, i int, u *gunim.UI) (lands, held bool) {
	if !r.held {
		return false, false
	}
	r.held = false
	r.down.Animate(0, widget.Quick.Get(u.Theme()))
	u.Invalidate()
	return r.click.Release(e, i), true
}

// Focusable implements [gunim.Focusable]: a click or Tab puts the keyboard in the groups.
func (g *groupList) Focusable() bool { return true }

func (g *groupList) fill() {
	var words []string
	if g.b.query != "" {
		words = strings.Fields(strings.ToLower(g.b.query))
	}
	g.rows = g.rows[:0]
	y := float32(4)
	for _, bank := range g.b.banks() {
		g.rows = append(g.rows, listRow{header: true, bank: bank, label: strings.ToUpper(bank), y: y, h: headH})
		y += headH
		for _, p := range g.b.list {
			if p.Bank != bank {
				continue
			}
			n := len(g.rows) - 1
			if g.rows[n].header || g.rows[n].group != p.Group {
				g.rows = append(g.rows, listRow{bank: bank, group: p.Group, label: p.Group, y: y, h: rowH})
				y += rowH
				n++
			}
			if p.Matches(words) {
				g.rows[n].count++
			}
		}
	}
	keyRows(g.rows)
}

func (g *groupList) index(bank, group string) int {
	for i, r := range g.rows {
		if !r.header && r.bank == bank && r.group == group {
			return i
		}
	}
	return -1
}

func (g *groupList) rowRect(i int) geom.Rect { return geom.Rc(0, g.rows[i].y, g.size.W, g.rows[i].h) }

// Layout implements [gunim.Node]: as tall as its rows.
func (g *groupList) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	g.size = geom.Sz(c.Max.W, listHeight(g.rows))
	return g.size
}

// at returns the row at y, or -1.
func (g *groupList) at(y float32) int { return rowAt(g.rows, y) }

// Handle implements [gunim.Handler]: the pointer lights a group, and a
// click shows its presets on the release. Up, Down, Home, End, Page Up and Page Down
// show the group they move to.
func (g *groupList) Handle(e input.Event, u *gunim.UI) bool {
	if followRing(g.ring, e, u) {
		return true
	}
	switch e := e.(type) {
	case input.KeyPress:
		return e.Mods == 0 && g.move(e.Key, u)
	case input.PointerMove:
		g.setHover(g.at(e.Pos.Y), u)
		return false
	case input.PointerLeave:
		g.setHover(-1, u)
		return false
	case input.PointerDown:
		return g.press.start(e, g.item(e.Pos.Y), u)
	case input.PointerUp:
		i := g.item(e.Pos.Y)
		lands, held := g.press.end(e, i, u)
		if lands {
			u.Cue(gunim.CuePress, g)
			g.b.showGroup(g.rows[i].bank, g.rows[i].group, u)
		}
		return held
	}
	return false
}

// item returns the group at y, or -1 for a caption or nothing.
func (g *groupList) item(y float32) int {
	if i := g.at(y); i >= 0 && !g.rows[i].header {
		return i
	}
	return -1
}

// move shows the group key moves to from the group shown, and reports whether key moves.
func (g *groupList) move(key input.Key, u *gunim.UI) bool {
	var rows []int
	at := -1
	for i, r := range g.rows {
		if !r.header {
			if r.bank == g.b.bank && r.group == g.b.group {
				at = len(rows)
			}
			rows = append(rows, i)
		}
	}
	if len(rows) == 0 {
		return false
	}
	to, ok := moveTo(key, at, len(rows), g.b.page())
	if !ok {
		return false
	}
	r := g.rows[rows[to]]
	g.b.showGroup(r.bank, r.group, u)
	g.b.gs.RevealContent(g.rowRect(rows[to]), u)
	return true
}

func (g *groupList) setHover(i int, u *gunim.UI) {
	hoverRow(g.rows, &g.hover, i, g.lit, g.hoverY, u)
}

// Paint implements [gunim.Node].
func (g *groupList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	ink, accent := widget.Ink.Get(f.Theme), widget.Accent.Get(f.Theme)
	vis, culled := p.Visible()
	inset := float32(6)
	if a := g.lit.Value(); a > 0.01 {
		p.RRect(geom.Rc(inset, g.hoverY.Value(), box.W-2*inset, rowH), 7, paint.Solid(paint.Faded(ink, 0.06*a+0.06*g.press.down.Value())))
	}
	if g.index(g.b.bank, g.b.group) >= 0 {
		y := rowY(g.rows, g.pill.Value())
		p.RRect(geom.Rc(inset, y, box.W-2*inset, rowH), 7, paint.Solid(paint.Faded(accent, 0.2)))
		p.RRect(geom.Rc(inset, y+6, 3, rowH-12), 1.5, paint.Solid(accent))
	}
	cur, playing := g.b.current()
	for i, r := range g.rows {
		if culled && (r.y+r.h < vis.Min.Y || r.y > vis.Max.Y) {
			continue
		}
		if r.header {
			g.b.bankIcon(p, f, r.bank, geom.Rc(14, r.y+11, 12, 12), paint.Faded(ink, 0.45))
			audioui.Shaped(r.label, 9, true, false).Paint(p, geom.Pt(32, r.y+12), paint.Faded(ink, 0.45))
			continue
		}
		c := paint.Faded(ink, 0.82)
		count := paint.Faded(ink, 0.35)
		if g.b.query != "" && r.count == 0 {
			c, count = paint.Faded(ink, 0.3), paint.Faded(ink, 0.18)
		}
		if i == g.index(g.b.bank, g.b.group) && g.b.query == "" {
			c = ink
		}
		num := audioui.Shaped(strconv.Itoa(r.count), 10, false, true)
		run := audioui.Shaped(r.label, 12, false, false)
		room := box.W - 46 - num.Advance - 10
		run.PaintFaded(p, geom.Pt(20, r.y+(rowH-run.Height())/2), c, room, 18)
		num.Paint(p, geom.Pt(box.W-16-num.Advance, r.y+(rowH-num.Height())/2), count)
		if playing && r.bank == cur.Bank && r.group == cur.Group {
			// The group of the preset playing has a dot.
			p.RRect(geom.Rc(box.W-26-num.Advance, r.y+rowH/2-2.5, 5, 5), 2.5, paint.Solid(accent))
		}
	}
}

// Access implements [gunim.Accessible]: the groups read as a list of
// the rows in view, each bank's caption a heading, and each group by
// its name, with how many presets it holds or the search finds in it,
// the group shown selected.
func (g *groupList) Access() access.Info {
	info := access.Info{Role: access.RoleList, Name: "Groups"}
	shown := -1
	if g.b.query == "" {
		shown = g.index(g.b.bank, g.b.group)
	}
	info.Parts, g.parts = rowParts(g.rows, g.b.gs.Offset(), g.b.colH, g.size.W, g.parts, func(i int, r listRow) access.Info {
		if r.header {
			return access.Info{Role: access.RoleHeading, Name: r.bank}
		}
		about := strconv.Itoa(r.count) + " presets"
		if r.count == 1 {
			about = "1 preset"
		}
		part := access.Info{Role: access.RoleListItem, Name: r.label, Description: about, Actions: []string{access.ActionPress}}
		if i == shown {
			part.State = access.StateSelected
		}
		return part
	})
	return info
}

// AccessAct implements [gunim.AccessActor]: a press on a group shows
// its presets, as a click does.
func (g *groupList) AccessAct(r access.Request, u *gunim.UI) bool {
	i, ok := rowOfPart(g.rows, g.parts, r)
	if !ok || g.rows[i].header {
		return false
	}
	g.b.showGroup(g.rows[i].bank, g.rows[i].group, u)
	return true
}

// presetList is the column of presets: the group's, or those a search
// finds, under their groups' captions.
type presetList struct {
	b     *PresetBrowser
	rows  []listRow
	hover int
	// parts are the keys of the rows the access tree last listed.
	parts []uint64
	// pill is the row the pill marking the preset playing stands on,
	// shown how far it shows; lit is how far the pill under the pointer
	// shows, at hoverY, and ring how far the focus ring shows.
	pill, shown, lit, hoverY, ring *anim.Float
	size                           geom.Size
	press                          rowPress
}

// Focusable implements [gunim.Focusable]: a click or Tab puts the keyboard in the presets.
func (l *presetList) Focusable() bool { return true }

func (l *presetList) fill() {
	l.rows = l.rows[:0]
	y := float32(4)
	add := func(r listRow) {
		r.y = y
		y += r.h
		l.rows = append(l.rows, r)
	}
	defer func() { keyRows(l.rows) }()
	if l.b.query == "" {
		if l.b.group == "" && l.b.bank == "" {
			return
		}
		add(listRow{header: true, bank: l.b.bank, group: l.b.group, label: l.b.group, h: headH})
		for _, p := range l.b.list {
			if p.Bank == l.b.bank && p.Group == l.b.group {
				add(listRow{bank: p.Bank, group: p.Group, p: p, label: p.Name, h: rowH})
			}
		}
		return
	}
	for _, p := range l.b.search() {
		if n := len(l.rows); n == 0 || l.rows[n-1].bank != p.Bank || l.rows[n-1].group != p.Group {
			add(listRow{header: true, bank: p.Bank, group: p.Group, label: p.Group, h: headH})
		}
		add(listRow{bank: p.Bank, group: p.Group, p: p, label: p.Name, h: rowH})
	}
}

// current returns the row of the preset playing, or -1.
func (l *presetList) current() int {
	cur, ok := l.b.current()
	if !ok {
		return -1
	}
	for i, r := range l.rows {
		if !r.header && r.p.Same(cur) {
			return i
		}
	}
	return -1
}

// headerIndex returns the row of a group's caption, or -1.
func (l *presetList) headerIndex(bank, group string) int {
	for i, r := range l.rows {
		if r.header && r.bank == bank && r.group == group {
			return i
		}
	}
	return -1
}

func (l *presetList) rowRect(i int) geom.Rect { return geom.Rc(0, l.rows[i].y, l.size.W, l.rows[i].h) }

// Layout implements [gunim.Node]: as tall as its rows.
func (l *presetList) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	l.size = geom.Sz(c.Max.W, listHeight(l.rows))
	return l.size
}

func (l *presetList) at(y float32) int { return rowAt(l.rows, y) }

// Handle implements [gunim.Handler]: the pointer lights a preset, and a
// click picks it on the release. Home, End, Page Up and Page Down play the preset they
// move to; Up and Down go on to the browser.
func (l *presetList) Handle(e input.Event, u *gunim.UI) bool {
	if followRing(l.ring, e, u) {
		return true
	}
	switch e := e.(type) {
	case input.KeyPress:
		if e.Mods != 0 || e.Key == input.KeyUp || e.Key == input.KeyDown {
			return false
		}
		list, at := l.b.listed()
		if len(list) == 0 {
			return false
		}
		to, ok := moveTo(e.Key, at, len(list), l.b.page())
		if !ok {
			return false
		}
		l.b.pick(list[to], u)
		l.b.inView(u)
		return true
	case input.PointerMove:
		l.setHover(l.at(e.Pos.Y), u)
		return false
	case input.PointerLeave:
		l.setHover(-1, u)
		return false
	case input.PointerDown:
		return l.press.start(e, l.item(e.Pos.Y), u)
	case input.PointerUp:
		i := l.item(e.Pos.Y)
		lands, held := l.press.end(e, i, u)
		if lands {
			u.Cue(gunim.CuePress, l)
			l.b.pick(l.rows[i].p, u)
		}
		return held
	}
	return false
}

// item returns the preset at y, or -1 for a caption or nothing.
func (l *presetList) item(y float32) int {
	if i := l.at(y); i >= 0 && !l.rows[i].header {
		return i
	}
	return -1
}

func (l *presetList) setHover(i int, u *gunim.UI) {
	hoverRow(l.rows, &l.hover, i, l.lit, l.hoverY, u)
}

// Paint implements [gunim.Node].
func (l *presetList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	ink, accent := widget.Ink.Get(f.Theme), widget.Accent.Get(f.Theme)
	vis, culled := p.Visible()
	inset := float32(6)
	if a := l.lit.Value(); a > 0.01 {
		p.RRect(geom.Rc(inset, l.hoverY.Value(), box.W-2*inset, rowH), 7, paint.Solid(paint.Faded(ink, 0.06*a+0.06*l.press.down.Value())))
	}
	if a := l.shown.Value(); a > 0.01 {
		y := rowY(l.rows, l.pill.Value())
		p.ShadowRRect(geom.Rc(inset, y, box.W-2*inset, rowH), 7, paint.Solid(paint.Faded(accent, 0.28*a)),
			paint.Shadow{Blur: 10, Color: paint.Faded(accent, 0.25*a)})
	}
	cur := l.current()
	switch {
	case len(l.b.list) == 0:
		run := audioui.Shaped("No presets", 12, false, false)
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, 40), paint.Faded(ink, 0.45))
	case len(l.rows) <= 1 && l.b.query != "":
		run := audioui.Shaped("No preset matches", 12, false, false)
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, 40), paint.Faded(ink, 0.45))
	}
	for i, r := range l.rows {
		if culled && (r.y+r.h < vis.Min.Y || r.y > vis.Max.Y) {
			continue
		}
		if r.header {
			l.b.bankIcon(p, f, r.bank, geom.Rc(14, r.y+11, 12, 12), paint.Faded(ink, 0.45))
			run := audioui.Shaped(strings.ToUpper(r.label), 9, true, false)
			run.PaintFaded(p, geom.Pt(32, r.y+12), paint.Faded(ink, 0.5), box.W-44, 18)
			continue
		}
		c := paint.Faded(ink, 0.85)
		if i == cur {
			c = ink
			widget.PaintIcon(p, f.Theme, icon.AudioLines, geom.Rc(box.W-30, r.y+(rowH-14)/2, 14, 14), accent)
		}
		run := audioui.Shaped(r.label, 12, i == cur, false)
		run.PaintFaded(p, geom.Pt(20, r.y+(rowH-run.Height())/2), c, box.W-58, 18)
		if r.p.Drums {
			widget.PaintIcon(p, f.Theme, icon.Drum, geom.Rc(box.W-48, r.y+(rowH-12)/2, 12, 12), paint.Faded(ink, 0.3))
		}
	}
}

// Access implements [gunim.Accessible]: the presets read as a list of
// the rows in view, each group's caption a heading, and each preset by
// its name, described by where it is filed and its tags, as "Factory ·
// Kits · drums", the one playing selected.
func (l *presetList) Access() access.Info {
	info := access.Info{Role: access.RoleList, Name: "Presets"}
	cur := l.current()
	info.Parts, l.parts = rowParts(l.rows, l.b.ps.Offset(), l.b.colH, l.size.W, l.parts, func(i int, r listRow) access.Info {
		if r.header {
			return access.Info{Role: access.RoleHeading, Name: r.label}
		}
		about := r.p.Bank + " · " + r.p.Group
		if r.p.Drums {
			about += " · drums"
		}
		part := access.Info{Role: access.RoleListItem, Name: r.p.Name, Description: about, Actions: []string{access.ActionPress}}
		if i == cur {
			part.State = access.StateSelected
		}
		return part
	})
	return info
}

// AccessAct implements [gunim.AccessActor]: a press on a preset picks
// it, as a click does.
func (l *presetList) AccessAct(r access.Request, u *gunim.UI) bool {
	i, ok := rowOfPart(l.rows, l.parts, r)
	if !ok || l.rows[i].header {
		return false
	}
	l.b.pick(l.rows[i].p, u)
	return true
}

// rowParts are the parts of the rows of a column w wide that show, its
// scroll at top and h of it in view, each as part makes it from the
// row and its index, keyed by the row and cut to what of it shows; and
// the rows' keys in turn, kept in kept.
func rowParts(rows []listRow, top, h, w float32, kept []uint64, part func(i int, r listRow) access.Info) (parts []access.Info, keys []uint64) {
	keys = kept[:0]
	for i, r := range rows {
		y0, y1 := max(r.y, top), min(r.y+r.h, top+h)
		if y1 <= y0 {
			continue
		}
		p := part(i, r)
		p.Bounds, p.Key = geom.Rc(0, y0, w, y1-y0), r.key
		parts = append(parts, p)
		keys = append(keys, r.key)
	}
	return parts, keys
}

// rowOfPart is the row of rows a press on part r.Part is for: the row
// it showed as the column last listed its parts, wherever that row
// stands now, and false once it has gone.
func rowOfPart(rows []listRow, parts []uint64, r access.Request) (int, bool) {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(parts) {
		return 0, false
	}
	for i, row := range rows {
		if row.key == parts[r.Part] {
			return i, true
		}
	}
	return 0, false
}

// keyRows keys each of rows by what it shows, so a screen reader holding
// one keeps it as the column scrolls or fills again: a caption by its
// bank and group, a group by its bank and its name, a preset by where
// it is filed and its name. Rows that show the same, as two presets of
// one name a bank read from two folders holds, are keyed apart by how
// many came before.
func keyRows(rows []listRow) {
	seen := map[uint64]int{}
	for i := range rows {
		r := &rows[i]
		h := fnv.New64a()
		kind := "row"
		if r.header {
			kind = "caption"
		}
		for _, s := range [...]string{kind, r.bank, r.group, r.p.Name} {
			h.Write([]byte(s))
			h.Write([]byte{0})
		}
		base := h.Sum64()
		n := seen[base]
		seen[base]++
		h.Write([]byte(strconv.Itoa(n)))
		r.key = max(h.Sum64(), 1)
	}
}

// listHeight is how tall a column of rows is.
func listHeight(rows []listRow) float32 {
	if n := len(rows); n > 0 {
		return rows[n-1].y + rows[n-1].h + 8
	}
	return 8
}

// rowAt returns the row of rows at y, or -1.
func rowAt(rows []listRow, y float32) int {
	for i, r := range rows {
		if y >= r.y && y < r.y+r.h {
			return i
		}
	}
	return -1
}

// hoverRow lights row i of rows under the pointer, or none for -1 or a
// caption: the pill under the pointer glides to it.
func hoverRow(rows []listRow, hover *int, i int, lit, hoverY *anim.Float, u *gunim.UI) {
	if i >= 0 && rows[i].header {
		i = -1
	}
	if i == *hover {
		return
	}
	sp := widget.Quick.Get(u.Theme())
	if i >= 0 {
		if lit.Value() < 0.05 {
			hoverY.Jump(rows[i].y)
		}
		hoverY.Animate(rows[i].y, sp)
	}
	*hover = i
	on := float32(0)
	if i >= 0 {
		on = 1
	}
	lit.Animate(on, sp)
	u.Invalidate()
}

// rowY is the top of the row at the fractional index v, between rows.
func rowY(rows []listRow, v float32) float32 {
	if len(rows) == 0 {
		return 0
	}
	i := min(max(int(v), 0), len(rows)-1)
	j := min(i+1, len(rows)-1)
	t := min(max(v-float32(i), 0), 1)
	return rows[i].y + (rows[j].y-rows[i].y)*t
}
