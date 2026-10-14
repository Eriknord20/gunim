package filemanager

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The room of the tree of Disk usage: its rows, the step in of each
// level, the strip of column titles, and its columns.
const (
	usageRowH    = 26
	usageIndent  = 18
	usageTitlesH = 28
	usageColW    = 92
	usageShareW  = 120
	usageFilesW  = 76
	usagePad     = 12
	// usageNameW is the least room the names keep beside the columns.
	usageNameW = 180
)

// usageList is the tree of Disk usage: the folder in focus, and the
// folders open under it, each item with its size and its share of its
// folder. Up and Down move the selection, Right and Left open and shut a
// folder, and Enter shows it in the boxes.
type usageList struct {
	pg   *usagePage
	list *widget.VirtualList
	menu *widget.ContextMenu
	keys []widget.Key
	at   map[string]int
	// menuKey is the item the context menu is for, and menuActs what its
	// items do.
	menuKey  string
	menuActs []string
	// aim is the row the tree scrolls to, to the middle with aimCenter,
	// as it is laid out until aimUntil; th is the theme it was laid out in.
	aim       string
	aimCenter bool
	aimUntil  time.Time
	th        *theme.Live
	// hot is the row under the pointer, and ring says the keyboard came
	// by Tab, so a ring shows round the tree.
	hot     string
	ring    bool
	focused bool
	box     geom.Size
}

func newUsageList(pg *usagePage) *usageList {
	l := &usageList{pg: pg, at: map[string]int{}}
	l.list = widget.NewVirtualList(func(k widget.Key) gunim.Node { return newUsageRow(l, string(k)) })
	l.list.Estimate = usageRowH
	l.list.Spacing = noGap
	l.menu = widget.NewContextMenu(l.list, nil)
	l.menu.Prepare = func(_ geom.Point, u *gunim.UI) bool {
		key := l.hot
		if _, ok := pg.item(key); !ok {
			return false
		}
		pg.pickRow(key, u)
		items, acts := pg.menuItems(key)
		l.menuKey, l.menuActs = key, acts
		l.menu.SetItems(items)
		return len(items) > 0
	}
	l.menu.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		pg.menuPicked(l.menuKey, l.menuActs, i, u)
		return nil
	}
	return l
}

// update shows the rows of the state the page took; was is the state before.
func (l *usageList) update(was Usage, u *gunim.UI) {
	l.rebuild(u)
	st := l.pg.state
	if was.Seq != st.Seq || was.Focus != st.Focus {
		key := l.pg.sel
		if _, ok := l.at[key]; !ok {
			key = st.Focus
		}
		l.aimAt(key, true, u)
	}
}

// aimFor is how long the tree keeps the row it scrolls to in its place,
// while the rows above it, just opened, grow to their heights.
const aimFor = 700 * time.Millisecond

// aimAt scrolls the tree to the row of key: to the middle with center,
// and otherwise only as far as brings it into view, a row clear of the
// fades at the ends. The scroll is worked out as the tree is laid out,
// again and again for a moment, as rows that just opened above it grow.
func (l *usageList) aimAt(key string, center bool, u *gunim.UI) {
	l.aim, l.aimCenter, l.aimUntil = key, center, time.Now().Add(aimFor)
	u.Invalidate()
}

// aimNow scrolls to the row aimed at, in a view h high, and reports
// whether it should go on doing so next frame.
func (l *usageList) aimNow(h float32) bool {
	i, ok := l.at[l.aim]
	if !ok || time.Now().After(l.aimUntil) {
		l.aim = ""
		return false
	}
	top := float32(i) * usageRowH
	off := l.list.Offset()
	var y float32
	switch margin := min(float32(usageRowH), max(0, (h-usageRowH)/2)); {
	case l.aimCenter:
		y = top + usageRowH/2 - h/2
	case top < off+margin:
		y = top - margin
	case top+usageRowH > off+h-margin:
		y = top + usageRowH + margin - h
	default:
		return true
	}
	l.list.ScrollTo(max(0, y), widget.Quick.Get(l.th))
	return true
}

// rebuild makes the rows from the items and the folders open.
func (l *usageList) rebuild(u *gunim.UI) {
	pg := l.pg
	l.keys = l.keys[:0]
	clear(l.at)
	if len(pg.state.Items) == 0 {
		l.list.SetKeys(nil, u)
		return
	}
	var walk func(i int)
	walk = func(i int) {
		it := pg.state.Items[i]
		l.at[it.Key] = len(l.keys)
		l.keys = append(l.keys, widget.Key(it.Key))
		if i != 0 && !pg.open[it.Key] {
			return
		}
		for _, k := range pg.kids[i] {
			walk(k)
		}
	}
	walk(0)
	l.list.SetKeys(l.keys, u)
}

// depth is how deep the item of key is under the folder in focus.
func (l *usageList) depth(key string) int {
	f := l.pg.state.Focus
	key, _, rest := strings.Cut(key, "\x00")
	d := 0
	if key != f {
		rel := key
		if f != "" {
			rel = strings.TrimPrefix(key, f+"/")
		}
		d = strings.Count(rel, "/") + 1
	}
	if rest {
		d++
	}
	return d
}

// opens reports whether the item can open in the tree.
func (l *usageList) opens(it UsageItem) bool {
	return it.Dir && it.Rest == 0 && it.Key != l.pg.state.Focus && (it.More || len(l.pg.kids[l.pg.index[it.Key]]) > 0)
}

// toggle opens or shuts the folder of key.
func (l *usageList) toggle(key string, u *gunim.UI) {
	pg := l.pg
	it, ok := pg.item(key)
	if !ok || !l.opens(it) {
		return
	}
	open := !pg.open[key]
	if open {
		pg.open[key] = true
	} else {
		delete(pg.open, key)
	}
	u.Send(pg, UsageOpened{Seq: pg.state.Seq, Key: key, Open: open})
	l.rebuild(u)
	u.Invalidate()
}

// show opens the folders the item of key is in here alone, with no word
// to the application, whose items for them it has already, and scrolls
// its row to the middle of the tree.
func (l *usageList) show(key string, u *gunim.UI) {
	pg := l.pg
	i, ok := pg.index[key]
	if !ok {
		return
	}
	opened := false
	for p := pg.state.Items[i].Parent; p > 0; p = pg.state.Items[p].Parent {
		k := pg.state.Items[p].Key
		if !pg.open[k] {
			pg.open[k] = true
			opened = true
		}
	}
	if opened {
		l.rebuild(u)
	}
	l.aimAt(key, true, u)
}

// move moves the selection by n rows.
func (l *usageList) move(n int, u *gunim.UI) {
	if len(l.keys) == 0 {
		return
	}
	i, ok := l.at[l.pg.sel]
	switch {
	case !ok:
		i = 0
	default:
		i = min(max(i+n, 0), len(l.keys)-1)
	}
	l.pg.pick(string(l.keys[i]), u)
}

// openMenu opens the context menu of the row selected, under it, as the
// Menu key asks.
func (l *usageList) openMenu(u *gunim.UI) {
	row, ok := l.list.Row(widget.Key(l.pg.sel))
	if !ok {
		return
	}
	r, ok := u.Bounds(row)
	m, mok := u.Bounds(l.menu)
	if !ok || !mok {
		return
	}
	l.hot = l.pg.sel
	l.menu.Open(geom.Pt(r.Min.X-m.Min.X+usagePad+40, r.Max.Y-m.Min.Y), u)
}

// Children implements [gunim.Composite].
func (l *usageList) Children() []gunim.Node { return []gunim.Node{l.menu} }

// Focusable implements [gunim.Focusable].
func (l *usageList) Focusable() bool { return true }

// Handle implements [gunim.Handler]: the keys of the tree.
func (l *usageList) Handle(e input.Event, u *gunim.UI) bool {
	pg := l.pg
	switch e := e.(type) {
	case input.FocusGained:
		l.focused = true
		if _, ok := l.at[pg.sel]; !ok && len(l.keys) > 0 {
			pg.sel = string(l.keys[0])
		}
		u.Invalidate()
		return true
	case input.FocusLost:
		l.focused = false
		u.Invalidate()
		return true
	case input.FocusRing:
		l.ring = e.On && !e.Grouped
		u.Invalidate()
		return true
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) || e.Mods.Has(input.ModSuper) ||
			e.Mods.Has(input.ModShift) && e.Key != input.KeyF10 {
			return false
		}
		page := max(1, int(l.box.H/usageRowH)-1)
		it, _ := pg.item(pg.sel)
		switch e.Key {
		case input.KeyUp:
			l.move(-1, u)
		case input.KeyDown:
			l.move(1, u)
		case input.KeyPageUp:
			l.move(-page, u)
		case input.KeyPageDown:
			l.move(page, u)
		case input.KeyHome:
			l.move(-len(l.keys), u)
		case input.KeyEnd:
			l.move(len(l.keys), u)
		case input.KeyRight:
			switch {
			case l.opens(it) && !pg.open[it.Key]:
				l.toggle(it.Key, u)
			case it.Key == pg.state.Focus || pg.open[it.Key]:
				l.move(1, u)
			}
		case input.KeyLeft:
			if l.opens(it) && pg.open[it.Key] {
				l.toggle(it.Key, u)
			} else if it.Parent >= 0 {
				pg.pick(pg.state.Items[it.Parent].Key, u)
			}
		case input.KeyEnter, input.KeyKPEnter:
			if it.Dir && it.Rest == 0 {
				pg.focusOn(it.Key, u)
			}
		case input.KeyMenu, input.KeyF10:
			if e.Key == input.KeyF10 && !e.Mods.Has(input.ModShift) {
				return false
			}
			l.openMenu(u)
		default:
			return false
		}
		return true
	}
	return false
}

// Layout implements [gunim.Node]: the column titles, then the rows.
func (l *usageList) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	l.box = c.Max
	l.th = f.Theme
	h := max(0, c.Max.H-usageTitlesH-4)
	kids.At(0).Layout(gunim.Tight(geom.Sz(c.Max.W, h)))
	kids.At(0).Place(geom.Pt(0, usageTitlesH))
	if l.aim != "" && l.aimNow(h) {
		// Again next frame, with the rows' heights as they are then.
		f.UI().Invalidate()
	}
	return c.Max
}

// The columns of the tree.
const (
	colFiles = iota
	colFreed
	colOnline
	colDevice
	colSize
	colShare
)

// usageColumn is a column of the tree: what it shows, its title and its
// width. main says it shows what the tree is sized by.
type usageColumn struct {
	kind  int
	title string
	w     float32
	main  bool
}

// columns are the tree's columns, from the right.
func (l *usageList) columns() []usageColumn {
	m := l.pg.state.Measure
	cols := []usageColumn{{colFiles, "Files", usageFilesW, false}}
	if l.pg.state.Cloud {
		cols = append(cols,
			usageColumn{colFreed, "Can be freed", usageColW, false},
			usageColumn{colOnline, "Online only", usageColW, m == MeasureOnline},
			usageColumn{colDevice, "On this device", usageColW, m == MeasureDevice},
			usageColumn{colSize, "Size", usageColW, m == MeasureAll})
	} else {
		cols = append(cols, usageColumn{colSize, "Size", usageColW, true})
	}
	cols = append(cols, usageColumn{colShare, "Share", usageShareW, false})
	// Narrow, the columns that matter least give way to the names.
	room := l.box.W - usagePad - usageNameW
	for _, drop := range []int{colFreed, colFiles, colOnline, colDevice, colSize, colShare} {
		total := float32(0)
		for _, c := range cols {
			total += c.w
		}
		if total <= room {
			break
		}
		cols = slices.DeleteFunc(cols, func(c usageColumn) bool { return c.kind == drop && !c.main })
	}
	return cols
}

// Paint implements [gunim.Node]: the card, the column titles, and the
// rows.
func (l *usageList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	p.RRect(geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th), paint.Solid(widget.CardFill.Get(th)))
	face := widget.Font.Get(th)
	small := SmallText.Get(th)
	x := box.W - usagePad
	ink := Caption.Get(th)
	for _, c := range l.columns() {
		run := l.pg.texts.run(face, c.title, small)
		w := min(run.Advance, c.w-8)
		l.pg.texts.draw(p, th, face, c.title, small, geom.Pt(x-w, (usageTitlesH-run.Height())/2+2), ink, c.w-8)
		x -= c.w
	}
	l.pg.texts.draw(p, th, face, "Name", small, geom.Pt(usagePad, (usageTitlesH-l.pg.texts.run(face, "Name", small).Height())/2+2), ink, x)
	p.RRect(geom.Rc(0, usageTitlesH, box.W, 1), 0, paint.Solid(UsageTrack.Get(th)))
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, usageTitlesH, box.W, box.H-usageTitlesH), Opacity: 1, Clip: true})()
		kids.At(0).Paint(p)
	}()
	if l.focused && l.ring {
		widget.GroupRing(p, geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th), 1, th)
	}
}

// usageRow is one row of the tree.
type usageRow struct {
	anim.Group
	l   *usageList
	key string
	hot *anim.Float
	// turn turns the chevron, 1 open, and pick lights the row selected.
	turn, pick *anim.Float
	laid       bool
	click      widget.Clicker
	box        geom.Size
}

func newUsageRow(l *usageList, key string) *usageRow {
	r := &usageRow{l: l, key: key, hot: anim.NewFloat(0), turn: anim.NewFloat(0), pick: anim.NewFloat(0)}
	r.Add(r.hot, r.turn, r.pick)
	return r
}

// Layout implements [gunim.Node].
func (r *usageRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	open, picked := float32(0), float32(0)
	if r.l.pg.open[r.key] {
		open = 1
	}
	if r.l.pg.sel == r.key {
		picked = 1
	}
	if !r.laid {
		r.turn.Jump(open)
		r.pick.Jump(picked)
		r.laid = true
	}
	r.turn.Animate(open, widget.Settle.Get(f.Theme))
	r.pick.Animate(picked, widget.Quick.Get(f.Theme))
	r.box = c.Constrain(geom.Sz(c.Max.W, usageRowH))
	return r.box
}

// Paint implements [gunim.Node].
func (r *usageRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	pg := r.l.pg
	it, ok := pg.item(r.key)
	if !ok {
		return
	}
	band := geom.Rc(4, 1, box.W-8, box.H-2)
	if v := min(max(r.hot.Value(), 0), 1); v > 0.01 || pg.hot == r.key {
		if pg.hot == r.key {
			v = 1
		}
		p.RRect(band, 6, paint.Solid(scaleA(widget.TreeHover.Get(th), v)))
	}
	if v := min(max(r.pick.Value(), 0), 1); v > 0.01 {
		fill := widget.TreeSelected.Get(th)
		if !r.l.focused {
			fill.A = uint8(float32(fill.A) * 0.7)
		}
		p.RRect(band, 6, paint.Solid(scaleA(fill, v)))
		edge := (box.H - 10) * v
		p.RRect(geom.Rc(4, box.H/2-edge/2, 3, edge), 1.5, paint.Solid(scaleA(widget.Accent.Get(th), v)))
	}
	face := widget.Font.Get(th)
	size := widget.TextSize.Get(th) * 0.93
	small := SmallText.Get(th)
	mid := box.H / 2
	ink := widget.Ink.Get(th)
	if it.Rest > 0 || it.Skipped != "" {
		ink = Faint.Get(th)
	}
	// The columns, from the right.
	x := box.W - usagePad
	cols := r.l.columns()
	whole := int64(0)
	if it.Parent >= 0 {
		whole = pg.sizeOf(pg.state.Items[it.Parent])
	}
	for _, c := range cols {
		var words string
		switch c.kind {
		case colFiles:
			words = count(it.Files)
		case colFreed:
			words = quietBytes(it.Local)
		case colOnline:
			words = quietBytes(it.Online)
		case colDevice:
			words = humanBytes(it.Size - it.Online)
		case colSize:
			words = humanBytes(it.Size)
		case colShare:
			r.share(p, th, geom.Rc(x-c.w, 0, c.w, box.H), pg.sizeOf(it), whole, it)
			x -= c.w
			continue
		}
		if c.main {
			words = humanBytes(pg.sizeOf(it))
		}
		if it.Reading && c.kind != colFiles && words != "" {
			words += "…"
		}
		run := pg.texts.run(face, words, small)
		w := min(run.Advance, c.w-8)
		cink := Faint.Get(th)
		if c.main {
			cink = ink
		}
		pg.texts.draw(p, th, face, words, small, geom.Pt(x-w, (box.H-run.Height())/2), cink, c.w-8)
		x -= c.w
	}
	// The name, set in by its depth, after the chevron and the mark.
	left := float32(usagePad) + float32(r.l.depth(r.key))*usageIndent
	const chev, side = 14, 16
	guide := widget.TreeGuide.Get(th)
	for d := range r.l.depth(r.key) {
		gx := float32(usagePad) + float32(d)*usageIndent + chev/2
		p.RRect(geom.Rc(gx, 0, 1, box.H), 0, paint.Solid(guide))
	}
	if r.l.opens(it) {
		centre := geom.Pt(left+chev/2, mid)
		func() {
			turn := 1 - min(max(r.turn.Value(), 0), 1)
			defer p.Push(paint.Rotate(-1.5707964*turn, centre))()
			widget.PaintIcon(p, th, icon.ChevronDown, geom.Rc(centre.X-chev/2, mid-chev/2, chev, chev), widget.Placeholder.Get(th))
		}()
	}
	left += chev + 4
	switch {
	case it.Rest > 0:
		widget.PaintIcon(p, th, icon.Ellipsis, geom.Rc(left, mid-side/2, side, side), Faint.Get(th))
	case it.Dir:
		ic := icon.Folder
		if pg.open[it.Key] || it.Key == pg.state.Focus {
			ic = icon.FolderOpen
		}
		widget.PaintIcon(p, th, ic, geom.Rc(left, mid-side/2, side, side), pg.markInk(th, it))
	default:
		d := float32(9)
		p.RRect(geom.Rc(left+(side-d)/2, mid-d/2, d, d), 2.5, paint.Solid(pg.markInk(th, it)))
	}
	left += side + 6
	name := it.Name
	if it.Err != "" {
		name += "  (could not be read)"
	} else if it.Skipped != "" {
		name += "  (" + strings.ToLower(it.Skipped[:1]) + it.Skipped[1:] + ")"
	}
	run := pg.texts.run(face, name, size)
	pg.texts.draw(p, th, face, name, size, geom.Pt(left, (box.H-run.Height())/2), ink, x-8-left)
}

// share draws the share of whole that size is, as a bar and a number.
func (r *usageRow) share(p *paint.Painter, th *theme.Live, cell geom.Rect, size, whole int64, it UsageItem) {
	pg := r.l.pg
	if whole <= 0 {
		return
	}
	v := float32(float64(size) / float64(whole))
	face := widget.Font.Get(th)
	small := SmallText.Get(th)
	words := fmt.Sprintf("%.1f%%", v*100)
	run := pg.texts.run(face, words, small)
	barW := cell.Size().W - 52
	bar := geom.Rc(cell.Min.X+4, cell.Min.Y+(cell.Size().H-6)/2, barW, 6)
	p.RRect(bar, 3, paint.Solid(UsageTrack.Get(th)))
	if v > 0 {
		p.RRect(geom.Rc(bar.Min.X, bar.Min.Y, max(3, barW*min(v, 1)), 6), 3, paint.Solid(pg.markInk(th, it)))
	}
	run.Paint(p, geom.Pt(cell.Max.X-8-run.Advance, cell.Min.Y+(cell.Size().H-run.Height())/2), Faint.Get(th))
}

// quietBytes is n bytes, or nothing for none.
func quietBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	return humanBytes(n)
}

// scaleA is c with its alpha scaled by v.
func scaleA(c color.NRGBA, v float32) color.NRGBA {
	c.A = uint8(float32(c.A) * min(max(v, 0), 1))
	return c
}

// Handle implements [gunim.Handler]: the pointer lights the row, a click
// selects it or on its chevron opens it, and a double click on a folder
// shows it in the boxes.
func (r *usageRow) Handle(e input.Event, u *gunim.UI) bool {
	pg := r.l.pg
	switch e := e.(type) {
	case input.PointerEnter:
		r.hot.Animate(1, widget.Quick.Get(u.Theme()))
		r.l.hot = r.key
		pg.setHot(r.key, u)
		return false
	case input.PointerLeave:
		r.hot.Animate(0, widget.Settle.Get(u.Theme()))
		if r.l.hot == r.key {
			r.l.hot = ""
		}
		pg.setHot("", u)
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		r.click.Press(e, 0)
		it, ok := pg.item(r.key)
		if !ok {
			return true
		}
		left := float32(usagePad) + float32(r.l.depth(r.key))*usageIndent
		switch {
		case r.l.opens(it) && e.Pos.X >= left-4 && e.Pos.X < left+22:
			r.l.toggle(r.key, u)
		case e.Clicks == 2 && it.Dir && it.Rest == 0:
			pg.focusOn(r.key, u)
		default:
			// Where it is: a click scrolls nothing.
			pg.pickRow(r.key, u)
		}
		u.Focus(r.l)
		return true
	case input.PointerUp:
		r.click.Release(e, 0)
		return true
	}
	return false
}
