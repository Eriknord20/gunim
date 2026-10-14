package plugui

import (
	"fmt"
	"image/color"
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
	b.groups = &groupList{b: b, hover: -1, pill: anim.NewFloat(0), lit: anim.NewFloat(0), hoverY: anim.NewFloat(0)}
	b.presets = &presetList{b: b, hover: -1, pill: anim.NewFloat(0), shown: anim.NewFloat(0), lit: anim.NewFloat(0), hoverY: anim.NewFloat(0)}
	b.Add(b.groups.pill, b.groups.lit, b.groups.hoverY, b.presets.pill, b.presets.shown, b.presets.lit, b.presets.hoverY)
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
	} else {
		b.glide(u)
	}
	b.reveal(u)
}

// reveal scrolls the columns to the group shown and the preset playing.
func (b *PresetBrowser) reveal(u *gunim.UI) {
	if u == nil {
		return
	}
	if i := b.groups.index(b.bank, b.group); i >= 0 {
		b.gs.Reveal(b.groups.rowRect(i), u)
	}
	if i := b.presets.current(); i >= 0 {
		b.ps.Reveal(b.presets.rowRect(i), u)
	}
}

// showGroup shows the presets of a group: while no search runs, the
// group's alone; during one, scrolled to the group's.
func (b *PresetBrowser) showGroup(bank, group string, u *gunim.UI) {
	if b.query != "" {
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

// Skip plays the preset by from the one playing, in the group listed,
// or in what the search finds, round from the last to the first.
func (b *PresetBrowser) Skip(by int, u *gunim.UI) {
	var list []plug.Preset
	for _, r := range b.presets.rows {
		if !r.header {
			list = append(list, r.p)
		}
	}
	if len(list) == 0 {
		return
	}
	i := -1
	if cur, ok := b.current(); ok {
		for j, p := range list {
			if p.Same(cur) {
				i = j
				break
			}
		}
	}
	if i < 0 && by < 0 {
		i = 0
	}
	i = ((i+by)%len(list) + len(list)) % len(list)
	b.pick(list[i], u)
	b.reveal(u)
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
func (b *PresetBrowser) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(0).Layout(gunim.Tight(geom.Sz(size.W, fieldH)))
	kids.At(0).Place(geom.Point{})
	top := float32(fieldH + colGap)
	h := max(size.H-top, 0)
	gw := float32(int(size.W * groupsW))
	kids.At(1).Layout(gunim.Tight(geom.Sz(gw, h)))
	kids.At(1).Place(geom.Pt(0, top))
	kids.At(2).Layout(gunim.Tight(geom.Sz(max(size.W-gw-colGap, 0), h)))
	kids.At(2).Place(geom.Pt(gw+colGap, top))
	return size
}

// Paint implements [gunim.Node]: the columns in wells.
func (b *PresetBrowser) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	ink := widget.Ink.Get(f.Theme)
	top := float32(fieldH + colGap)
	gw := float32(int(box.W * groupsW))
	for _, r := range []geom.Rect{geom.Rc(0, top, gw, box.H-top), geom.Rc(gw+colGap, top, box.W-gw-colGap, box.H-top)} {
		p.RRect(r, 10, paint.Solid(audioui.Faded(ink, 0.035)))
	}
	for i := range kids.Len() {
		kids.At(i).Paint(p)
	}
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
}

// groupList is the column of groups, under their banks' captions, each
// with how many presets it holds, or the search finds in it.
type groupList struct {
	b     *PresetBrowser
	rows  []listRow
	hover int
	// pill is the row the pill marking the group shown stands on, lit
	// how far the pill under the pointer shows, at hoverY.
	pill, lit, hoverY *anim.Float
	size              geom.Size
}

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
	h := float32(8)
	if n := len(g.rows); n > 0 {
		h = g.rows[n-1].y + g.rows[n-1].h + 8
	}
	g.size = geom.Sz(c.Max.W, h)
	return g.size
}

// at returns the row at y, or -1.
func (g *groupList) at(y float32) int { return rowAt(g.rows, y) }

// Handle implements [gunim.Handler]: the pointer lights a group, and a
// click shows its presets.
func (g *groupList) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		g.setHover(g.at(e.Pos.Y), u)
		return false
	case input.PointerLeave:
		g.setHover(-1, u)
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		i := g.at(e.Pos.Y)
		if i < 0 || g.rows[i].header {
			return false
		}
		u.Cue(gunim.CuePress, g)
		g.b.showGroup(g.rows[i].bank, g.rows[i].group, u)
		return true
	}
	return false
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
		p.RRect(geom.Rc(inset, g.hoverY.Value(), box.W-2*inset, rowH), 7, paint.Solid(audioui.Faded(ink, 0.06*a)))
	}
	if g.index(g.b.bank, g.b.group) >= 0 {
		y := rowY(g.rows, g.pill.Value())
		p.RRect(geom.Rc(inset, y, box.W-2*inset, rowH), 7, paint.Solid(audioui.Faded(accent, 0.2)))
		p.RRect(geom.Rc(inset, y+6, 3, rowH-12), 1.5, paint.Solid(accent))
	}
	cur, playing := g.b.current()
	for i, r := range g.rows {
		if culled && (r.y+r.h < vis.Min.Y || r.y > vis.Max.Y) {
			continue
		}
		if r.header {
			g.b.bankIcon(p, f, r.bank, geom.Rc(14, r.y+11, 12, 12), audioui.Faded(ink, 0.45))
			audioui.Shaped(r.label, 9, true, false).Paint(p, geom.Pt(32, r.y+12), audioui.Faded(ink, 0.45))
			continue
		}
		c := audioui.Faded(ink, 0.82)
		count := audioui.Faded(ink, 0.35)
		if g.b.query != "" && r.count == 0 {
			c, count = audioui.Faded(ink, 0.3), audioui.Faded(ink, 0.18)
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

// Access implements [gunim.Accessible]: the groups read as a list.
func (g *groupList) Access() access.Info {
	return access.Info{Role: access.RoleList, Name: "Groups"}
}

// presetList is the column of presets: the group's, or those a search
// finds, under their groups' captions.
type presetList struct {
	b     *PresetBrowser
	rows  []listRow
	hover int
	// pill is the row the pill marking the preset playing stands on,
	// shown how far it shows; lit is how far the pill under the pointer
	// shows, at hoverY.
	pill, shown, lit, hoverY *anim.Float
	size                     geom.Size
}

func (l *presetList) fill() {
	l.rows = l.rows[:0]
	y := float32(4)
	add := func(r listRow) {
		r.y = y
		y += r.h
		l.rows = append(l.rows, r)
	}
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
	h := float32(8)
	if n := len(l.rows); n > 0 {
		h = l.rows[n-1].y + l.rows[n-1].h + 8
	}
	l.size = geom.Sz(c.Max.W, h)
	return l.size
}

func (l *presetList) at(y float32) int { return rowAt(l.rows, y) }

// Handle implements [gunim.Handler]: the pointer lights a preset, and a
// click picks it.
func (l *presetList) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		l.setHover(l.at(e.Pos.Y), u)
		return false
	case input.PointerLeave:
		l.setHover(-1, u)
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		i := l.at(e.Pos.Y)
		if i < 0 || l.rows[i].header {
			return false
		}
		u.Cue(gunim.CuePress, l)
		l.b.pick(l.rows[i].p, u)
		return true
	}
	return false
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
		p.RRect(geom.Rc(inset, l.hoverY.Value(), box.W-2*inset, rowH), 7, paint.Solid(audioui.Faded(ink, 0.06*a)))
	}
	if a := l.shown.Value(); a > 0.01 {
		y := rowY(l.rows, l.pill.Value())
		p.ShadowRRect(geom.Rc(inset, y, box.W-2*inset, rowH), 7, paint.Solid(audioui.Faded(accent, 0.28*a)),
			paint.Shadow{Blur: 10, Color: audioui.Faded(accent, 0.25*a)})
	}
	cur := l.current()
	switch {
	case len(l.b.list) == 0:
		run := audioui.Shaped("No presets", 12, false, false)
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, 40), audioui.Faded(ink, 0.45))
	case len(l.rows) <= 1 && l.b.query != "":
		run := audioui.Shaped("No preset matches", 12, false, false)
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, 40), audioui.Faded(ink, 0.45))
	}
	for i, r := range l.rows {
		if culled && (r.y+r.h < vis.Min.Y || r.y > vis.Max.Y) {
			continue
		}
		if r.header {
			l.b.bankIcon(p, f, r.bank, geom.Rc(14, r.y+11, 12, 12), audioui.Faded(ink, 0.45))
			run := audioui.Shaped(strings.ToUpper(r.label), 9, true, false)
			run.PaintFaded(p, geom.Pt(32, r.y+12), audioui.Faded(ink, 0.5), box.W-44, 18)
			continue
		}
		c := audioui.Faded(ink, 0.85)
		if i == cur {
			c = ink
			widget.PaintIcon(p, f.Theme, icon.AudioLines, geom.Rc(box.W-30, r.y+(rowH-14)/2, 14, 14), accent)
		}
		run := audioui.Shaped(r.label, 12, i == cur, false)
		run.PaintFaded(p, geom.Pt(20, r.y+(rowH-run.Height())/2), c, box.W-58, 18)
		if r.p.Drums {
			widget.PaintIcon(p, f.Theme, icon.Drum, geom.Rc(box.W-48, r.y+(rowH-12)/2, 12, 12), audioui.Faded(ink, 0.3))
		}
	}
}

// Access implements [gunim.Accessible]: the presets read as a list.
func (l *presetList) Access() access.Info {
	return access.Info{Role: access.RoleList, Name: "Presets"}
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
