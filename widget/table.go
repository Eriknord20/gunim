package widget

import (
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Table tokens.
var (
	TableRowHeight = theme.Length("table.row", 26)
	TableHeader    = theme.Color("table.header", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
	// TableCursor lights the row the keys are on, and TableMark the rows
	// marked.
	TableCursor = theme.Color("table.cursor", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x40})
	TableMark   = theme.Color("table.mark", color.NRGBA{R: 0xe8, G: 0xb3, B: 0x4a, A: 0x30})
	// TableStrong colours a strong row's text, such as a folder's.
	TableStrong = theme.Color("table.strong", color.NRGBA{R: 0x8c, G: 0xb8, B: 0xff, A: 0xff})
	tableNoGap  = theme.Length("table.gap", 0)
)

// TableColumn is one column of a [Table].
type TableColumn struct {
	Title string
	// Width is the column's width in logical pixels. Zero shares out
	// what the other columns leave.
	Width float32
	// End lines the column's text up at its right edge, as numbers are.
	End bool
	// Face is the face the column's cells are set in, and the theme's [Font] when unset.
	Face theme.Token[*text.Face]
}

// TableRow is what a [Table] shows for one row: a line of text for each
// column. A strong row is set apart, as a folder is among files; a
// faint row is dimmed, as a hidden file is.
type TableRow struct {
	Cells  []string
	Strong bool
	Faint  bool
	// Accent draws the row in the theme's accent colour, as for a link.
	Accent bool
	// Icon, when set, is drawn before the first cell's text, in IconInk
	// or else the row's ink, as a file's kind is.
	Icon    *icon.Icon
	IconInk *theme.Token[color.NRGBA]
}

// Table shows rows of cells under a header of column titles, and
// scrolls through them, building only the rows in view, so a table of
// a hundred thousand rows costs what the rows on screen do.
//
// The rows ask Row for their cells as they draw, so the data can change
// under the same keys. A cursor row, lit, follows the arrow keys, Page
// Up and Page Down, Home and End, and the pointer; Enter or a double
// click activates it. Space marks it and moves on, and the marked rows
// are tinted. Typing a row's first letters moves the cursor to it; see
// [Table.SetKeys] for when the table reads the names. A click on a column's title asks for the rows sorted by
// it, and again for the other way round. Columns wider than the table
// scroll sideways, the header with them, by a sideways wheel, the wheel
// with Shift held, or Left and Right.
type Table struct {
	Columns []TableColumn
	Row     func(Key) TableRow
	// OnActivate runs on the UI goroutine with the row Enter or a double
	// click activates, and OnSort with the column clicked and the
	// direction asked for. A non-nil result is sent to the application as
	// the table's intent, as with OnDragEnd.
	OnActivate func(key Key, u *gunim.UI) gunim.Intent
	OnSort     func(column int, descending bool, u *gunim.UI) gunim.Intent
	// DragRows, when set, lets rows be dragged: a press on a row and a
	// move of a few pixels drags the data it returns, with ghost under
	// the pointer, held grab from its top left. keys are the rows
	// marked when the row pressed is one of them, and that row alone
	// otherwise; at is where the press was, in the table's space. A nil
	// data drags nothing. OnDragEnd hears how the drag ended.
	DragRows  func(keys []Key, at geom.Point) (data any, ghost gunim.Node, grab geom.Point)
	OnDragEnd func(e input.DragEnd, u *gunim.UI) gunim.Intent

	list   *VirtualList
	header *tableHeader
	keys   []Key
	index  map[Key]int
	cursor int
	marked map[Key]bool
	// sorted is the column sorted by, or -1, and descending its
	// direction, as the header shows them.
	sorted     int
	descending bool
	// xs holds each column's left edge and width, from the last layout.
	xs      [][2]float32
	focused bool
	cue     ringCue
	// typed is what has been typed to find a row, and typedAt when the
	// last of it was.
	typed   string
	typedAt time.Time
	// rowH and headH are a row's height and the header's, from the last
	// layout. lift is a press on a row that may become a drag.
	rowH, headH, width float32
	lift               tableLift
	// left is how far the columns are scrolled sideways, and wide how wide they are, from the last layout.
	left, wide float32
	// names holds each row's first cell in lower case, for finding a row by typing, and named says it is made for
	// the keys set last.
	names []string
	named bool
	// viewH is the height the rows show in, from the last layout, and parts the row and column of each part a screen
	// reader was last told of, -1 for the titles.
	viewH float32
	parts [][2]int
}

// tableLift is a press on a row, at, in the table's space, which a move
// far enough makes a drag; dragging says it has.
type tableLift struct {
	key      Key
	at       geom.Point
	armed    bool
	dragging bool
}

// RowAt returns the row drawn at p, in the table's space. While rows
// come, go or move, it is the row drawn there now, and none in the room
// a leaving row still takes.
func (t *Table) RowAt(p geom.Point) (Key, bool) {
	if t.rowH <= 0 || p.Y < t.headH {
		return "", false
	}
	k, _, _, ok := t.list.drawnAt(p.Y - t.headH)
	if _, in := t.index[k]; !ok || !in {
		return "", false
	}
	return k, true
}

// RowRect returns where row key is drawn, in the table's space, which
// may be outside the part in view.
func (t *Table) RowRect(key Key) (geom.Rect, bool) {
	i, ok := t.index[key]
	if !ok || t.rowH <= 0 {
		return geom.Rect{}, false
	}
	if top, h, ok := t.list.drawn(key); ok {
		return geom.Rc(0, t.headH+top, t.width, h), true
	}
	y := t.headH + float32(i)*t.rowH - t.list.Offset()
	return geom.Rc(0, y, t.width, t.rowH), true
}

// Header returns the height of the column titles, as last laid out.
func (t *Table) Header() float32 { return t.headH }

// NewTable returns an empty table of columns.
func NewTable(columns ...TableColumn) *Table {
	t := &Table{Columns: columns, index: map[Key]int{}, marked: map[Key]bool{}, sorted: -1}
	t.list = NewVirtualList(func(k Key) gunim.Node { return newTableRow(t, k) })
	t.list.Spacing = tableNoGap
	t.header = &tableHeader{t: t}
	return t
}

// SetKeys makes keys the table's rows, in order. The cursor stays on
// its row while the row stays, and marks on theirs.
//
// Finding a row by typing reads each row's first cell through Row once
// for the keys set last, as the first letter is typed. Where the first
// cells change under the same keys, as a file renamed in place, call
// SetKeys again, with the same keys, for typing to find the new names.
func (t *Table) SetKeys(keys []Key, u *gunim.UI) {
	var at Key
	if t.cursor >= 0 && t.cursor < len(t.keys) {
		at = t.keys[t.cursor]
	}
	t.keys, t.named = keys, false
	clear(t.index)
	for i, k := range keys {
		t.index[k] = i
	}
	for k := range t.marked {
		if _, ok := t.index[k]; !ok {
			delete(t.marked, k)
		}
	}
	if i, ok := t.index[at]; ok {
		t.cursor = i
	} else {
		t.cursor = min(t.cursor, len(keys)-1)
	}
	t.cursor = max(t.cursor, 0)
	t.list.SetKeys(keys, u)
}

// SetSorted shows the header's arrow on column, pointing the way the
// rows run; -1 shows none.
func (t *Table) SetSorted(column int, descending bool) {
	t.sorted, t.descending = column, descending
}

// Cursor returns the cursor's row, and false for an empty table.
func (t *Table) Cursor() (Key, bool) {
	if t.cursor < 0 || t.cursor >= len(t.keys) {
		return "", false
	}
	return t.keys[t.cursor], true
}

// SetCursor puts the cursor on key's row, scrolling it into view.
func (t *Table) SetCursor(key Key, u *gunim.UI) {
	if i, ok := t.index[key]; ok {
		t.move(i, u)
	}
}

// JumpTo puts the cursor on key and the view on it at once, at the top
// when it is in the first screenful. It is for a table showing rows that
// are new rather than moved, as a file list does for a folder just
// opened: its rows appear in place instead of gliding in from wherever
// the rows before were scrolled to.
func (t *Table) JumpTo(key Key, u *gunim.UI) {
	i, ok := t.index[key]
	if !ok {
		i = 0
	}
	t.cursor = i
	if len(t.keys) == 0 {
		t.cursor = 0
	}
	h := TableRowHeight.Get(u.Theme())
	y := float32(t.cursor) * h
	page := t.list.viewport
	if y+h <= page || page <= 0 {
		y = 0
	} else {
		y -= (page - h) / 2
	}
	t.list.jumpToY(y)
	u.Invalidate()
}

// Offset is how far the table is scrolled, for putting it back later
// with ShowAt.
func (t *Table) Offset() float32 { return t.list.Offset() }

// ShowAt is JumpTo with the view put back where it was, offset down,
// as a file list does going back to a folder: the rows are where the
// user left them. The cursor's row is kept in view.
func (t *Table) ShowAt(key Key, offset float32, u *gunim.UI) {
	t.JumpTo(key, u)
	h := TableRowHeight.Get(u.Theme())
	y := float32(t.cursor) * h
	page := t.list.viewport
	if page > 0 {
		offset = min(max(offset, y+h-page), y)
	}
	t.list.jumpToY(max(0, offset))
	// Put back as it was, the rows come at once, rather than growing
	// in as a folder new to the list does.
	t.list.still = true
}

// Marked returns the marked rows, in the table's order.
func (t *Table) Marked() []Key {
	var out []Key
	for _, k := range t.keys {
		if t.marked[k] {
			out = append(out, k)
		}
	}
	return out
}

// ClearMarks unmarks every row.
func (t *Table) ClearMarks() { clear(t.marked) }

// SetMarked marks the rows keys, and no others; a key the table does
// not hold is left out.
func (t *Table) SetMarked(keys []Key) {
	clear(t.marked)
	for _, k := range keys {
		if _, ok := t.index[k]; ok {
			t.marked[k] = true
		}
	}
}

func (t *Table) move(i int, u *gunim.UI) {
	if len(t.keys) == 0 {
		return
	}
	t.cursor = min(max(i, 0), len(t.keys)-1)
	h := TableRowHeight.Get(u.Theme())
	t.list.revealContent(geom.Rc(0, float32(t.cursor)*h, 1, h), u)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (t *Table) Children() []gunim.Node { return []gunim.Node{t.header, t.list} }

// Focusable implements [gunim.Focusable].
func (t *Table) Focusable() bool { return true }

// Handle implements [gunim.Handler]: the keys that move the cursor,
// mark and activate. Other keys go on, to the application's shortcuts.
func (t *Table) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.DragEnd:
		t.lift = tableLift{}
		u.Invalidate()
		if t.OnDragEnd != nil {
			send(u, t, t.OnDragEnd(e, u))
		}
		return true
	case input.FocusGained:
		t.focused = true
		u.Invalidate()
		return true
	case input.FocusRing:
		t.cue.follow(e)
		u.Invalidate()
		return true
	case input.FocusLost:
		t.focused = false
		u.Invalidate()
		return true
	case input.TextInput:
		t.find(e, u)
		return true
	case input.Scroll:
		// A wheel turned sideways under no row, such as below the last; a row has had one turned both ways.
		if e.Delta.Y != 0 && !e.Mods.Has(input.ModShift) {
			return false
		}
		return t.wheel(e, u)
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
			return false
		}
		// A typed press leaves it to the text, which finds a row; Space
		// while finding goes into the name.
		if e.Typed && (e.Key != input.KeySpace || t.finding(e.Time)) {
			return true
		}
		page := max(1, int(t.pageRows(u)))
		switch e.Key {
		case input.KeyUp:
			t.move(t.cursor-1, u)
		case input.KeyDown:
			t.move(t.cursor+1, u)
		case input.KeyPageUp:
			t.move(t.cursor-page, u)
		case input.KeyPageDown:
			t.move(t.cursor+page, u)
		case input.KeyHome:
			t.move(0, u)
		case input.KeyEnd:
			t.move(len(t.keys)-1, u)
		case input.KeyLeft, input.KeyRight:
			// Scroll columns wider than the table sideways, and are left to the application otherwise.
			dx := ScrollLine.Get(u.Theme())
			if e.Key == input.KeyLeft {
				dx = -dx
			}
			return t.scrollBy(dx, u)
		case input.KeyEnter, input.KeyKPEnter:
			if k, ok := t.Cursor(); ok && t.OnActivate != nil {
				act(u, t, gunim.CuePress, t.OnActivate, k)
			}
		case input.KeyBackspace:
			// Takes back a letter of a name being found, and is left to
			// the application otherwise.
			if !t.finding(when(e.Time)) {
				return false
			}
			typed := []rune(t.typed)
			t.typed = string(typed[:len(typed)-1])
			t.typedAt = when(e.Time)
			t.findTyped(u)
		case input.KeyEscape:
			// Gives up finding, and is left to the application when
			// nothing is being found.
			if !t.finding(when(e.Time)) {
				return false
			}
			t.typed = ""
		case input.KeySpace, input.KeyInsert:
			if k, ok := t.Cursor(); ok {
				t.marked[k] = !t.marked[k]
				if !t.marked[k] {
					delete(t.marked, k)
				}
				t.move(t.cursor+1, u)
			}
		default:
			return false
		}
		return true
	}
	return false
}

// when is an event's time, or now for one that carries none.
func when(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now()
	}
	return at
}

// findPause is how long typing rests before a new name starts.
const findPause = time.Second

func (t *Table) finding(now time.Time) bool {
	return t.typed != "" && now.Sub(t.typedAt) < findPause
}

// find moves the cursor to the first row whose first cell starts with
// what has been typed, whatever its case. Typing after a pause starts
// a new name.
func (t *Table) find(e input.TextInput, u *gunim.UI) {
	now := e.Time
	if now.IsZero() {
		now = time.Now()
	}
	if !t.finding(now) {
		t.typed = ""
		// A name never starts with a space: that one marked a row.
		if strings.TrimSpace(e.Text) == "" {
			return
		}
	}
	t.typed += strings.ToLower(e.Text)
	t.typedAt = now
	t.findTyped(u)
}

// findTyped moves the cursor to the first row whose first cell starts
// with what has been typed. The first cells are read once for the keys
// set last, as the first letter is typed.
func (t *Table) findTyped(u *gunim.UI) {
	if t.Row == nil || t.typed == "" {
		return
	}
	if !t.named {
		t.names = t.names[:0]
		for _, k := range t.keys {
			name := ""
			if row := t.Row(k); len(row.Cells) > 0 {
				name = strings.ToLower(row.Cells[0])
			}
			t.names = append(t.names, name)
		}
		t.named = true
	}
	for i, name := range t.names {
		if strings.HasPrefix(name, t.typed) {
			t.move(i, u)
			return
		}
	}
}

// wheel scrolls the columns sideways for a sideways wheel, or one turned with Shift held, where they are wider than
// the table. It leaves the rest to the rows.
func (t *Table) wheel(e input.Scroll, u *gunim.UI) bool {
	dx, dy := e.Delta.X, e.Delta.Y
	if e.Mods.Has(input.ModShift) && dx == 0 {
		dx, dy = dy, 0
	}
	if dx == 0 || t.wide <= t.width {
		return false
	}
	t.scrollBy(-dx, u)
	return dy == 0
}

// scrollBy scrolls the columns sideways by dx, and reports whether they moved.
func (t *Table) scrollBy(dx float32, u *gunim.UI) bool {
	to := max(0, min(t.left+dx, t.wide-t.width))
	if to == t.left {
		return false
	}
	t.left = to
	u.Invalidate()
	return true
}

// pageRows is how many rows the table shows at once.
func (t *Table) pageRows(u *gunim.UI) float32 {
	h := TableRowHeight.Get(u.Theme())
	if b, ok := u.Bounds(t.list); ok && h > 0 {
		return float32(math.Floor(float64(b.Size().H / h)))
	}
	return 10
}

// Layout implements [gunim.Node]. The table fills the space it is
// given: the header along the top, the rows under it.
func (t *Table) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	own := c.Max
	// Every row is as tall, so a row not laid out yet is where it will
	// be: a view put back far down a table stays on the rows it showed.
	t.list.Estimate = TableRowHeight.Get(f.Theme)
	pad := MenuRowPadding.Get(f.Theme)
	// Fixed columns take their widths; the rest share what is left.
	fixed, shared := float32(0), 0
	for _, col := range t.Columns {
		if col.Width > 0 {
			fixed += col.Width
		} else {
			shared++
		}
	}
	// A shared column keeps a width to read, and the columns scroll sideways where that leaves them wider than the
	// table.
	share := max(0, own.W-fixed-2*pad) / float32(max(shared, 1))
	share = max(share, tableShared)
	t.xs = t.xs[:0]
	x := pad
	for _, col := range t.Columns {
		w := col.Width
		if w <= 0 {
			w = share
		}
		t.xs = append(t.xs, [2]float32{x, w})
		x += w
	}
	hh := TableRowHeight.Get(f.Theme)
	t.rowH, t.headH, t.width, t.wide, t.viewH = hh, hh, own.W, x+pad, max(0, own.H-hh)
	t.left = max(0, min(t.left, t.wide-own.W))
	kids.At(0).Layout(gunim.Tight(geom.Sz(own.W, hh)))
	kids.At(0).Place(geom.Point{})
	kids.At(1).Layout(gunim.Tight(geom.Sz(own.W, max(0, own.H-hh))))
	kids.At(1).Place(geom.Pt(0, hh))
	return own
}

// Paint implements [gunim.Node].
func (t *Table) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	// Drawn last, over the rest
	defer func() {
		if t.cue.whole {
			GroupRing(p, geom.Rect{Max: box.Point()}, 0, 1, th)
		}
	}()
	// The columns scrolled sideways keep inside the table.
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
	for k := range kids.All {
		k.Paint(p)
	}
}

// tableShared is the narrowest a column that shares the table's width becomes; past that the columns scroll
// sideways.
const tableShared = 64

// cellText shapes s for a cell width wide, as one line, and says how
// much of it shows there, and whether it fits: text that does not is
// cut where the cell ends, fading out before it, as [text.Run.PaintFaded]
// draws it.
func cellText(cache *shapedText, face *text.Face, s string, size, width float32) (run text.Run, shown float32, fits bool) {
	run = cache.shape(face, s, size)
	return run, min(run.Advance, max(width, 0)), run.Advance <= max(width, 0)+0.5
}

// tableHeader is a table's row of column titles.
type tableHeader struct {
	t      *Table
	titles []shapedText
	click  Clicker
}

// Layout implements [gunim.Node].
func (h *tableHeader) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (h *tableHeader) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	t := h.t
	th := f.Theme
	if len(h.titles) != len(t.Columns) {
		h.titles = make([]shapedText, len(t.Columns))
	}
	size := TextSize.Get(th) * 0.9
	col := TableHeader.Get(th)
	for i, c := range t.Columns {
		if i >= len(t.xs) {
			break
		}
		x, w := t.xs[i][0]-t.left, t.xs[i][1]
		if x > box.W || x+w < 0 {
			continue
		}
		// A sorted title leaves room in its column for its arrow.
		arrow := float32(0)
		if i == t.sorted {
			arrow = sortArrow + 4
		}
		room := w - 12 - arrow
		if room <= 0 {
			continue
		}
		run, shown, _ := cellText(&h.titles[i], faceIn(Font, th), c.Title, size, room)
		at := x
		if c.End {
			at = x + w - 12 - shown
		}
		run.PaintFaded(p, geom.Pt(at, (box.H-run.Height())/2), col, room, TextFade.Get(th))
		if i == t.sorted {
			// A small arrow beside the title, pointing the way the rows
			// run.
			ax := at + shown + 8
			if c.End {
				ax = at - 8
			}
			ay := box.H / 2
			paintSort(p, th, geom.Pt(ax, ay), !t.descending, col)
		}
	}
	line := MenuBorder.Get(th)
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(line))
}

// Handle implements [gunim.Handler]: a click on a title sorts by it.
func (h *tableHeader) Handle(e input.Event, u *gunim.UI) bool {
	t := h.t
	switch e := e.(type) {
	case input.Scroll:
		return t.wheel(e, u)
	case input.PointerDown:
		i := h.titleAt(e.Pos)
		if i < 0 || e.Button != input.ButtonPrimary {
			return false
		}
		h.click.Press(e, i)
		return true
	case input.PointerUp:
		i := h.titleAt(e.Pos)
		if h.click.Release(e, i) {
			h.sort(i, u)
		}
		return true
	}
	return false
}

// sort asks for the rows sorted by column i, the other way round where they are already.
func (h *tableHeader) sort(i int, u *gunim.UI) {
	t := h.t
	desc := false
	if i == t.sorted {
		desc = !t.descending
	}
	t.SetSorted(i, desc)
	if t.OnSort != nil {
		send(u, t, t.OnSort(i, desc, u))
	}
	u.Invalidate()
}

// titleAt returns the column whose title is at p, or -1.
func (h *tableHeader) titleAt(p geom.Point) int {
	if p.Y < 0 || p.Y >= h.t.headH {
		return -1
	}
	for i, col := range h.t.xs {
		if x := col[0] - h.t.left; p.X >= x && p.X < x+col[1] {
			return i
		}
	}
	return -1
}

// tableRow is one row of a table, drawing its cells from the table's
// Row.
type tableRow struct {
	anim.Group
	t     *Table
	key   Key
	lit   *anim.Float
	on    bool
	cells []shapedText
}

func newTableRow(t *Table, k Key) *tableRow {
	r := &tableRow{t: t, key: k, lit: anim.NewFloat(0)}
	r.Add(r.lit)
	return r
}

// Layout implements [gunim.Node]. The cursor's light fades to it.
func (r *tableRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	i, ok := r.t.index[r.key]
	on := ok && i == r.t.cursor
	if on != r.on {
		r.on = on
		to := float32(0)
		if on {
			to = 1
		}
		r.lit.Animate(to, Quick.Get(f.Theme))
	}
	return c.Constrain(geom.Sz(c.Max.W, TableRowHeight.Get(f.Theme)))
}

// Paint implements [gunim.Node].
func (r *tableRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	t := r.t
	th := f.Theme
	band := geom.Rc(4, 1, box.W-8, box.H-2)
	if t.marked[r.key] {
		p.RRect(band, 5, paint.Solid(TableMark.Get(th)))
	}
	if v := min(max(r.lit.Value(), 0), 1); v > 0.01 {
		c := TableCursor.Get(th)
		if !t.focused {
			c.A /= 2
		}
		c.A = uint8(float32(c.A) * v)
		p.RRect(band, 5, paint.Solid(c))
	}
	if t.Row == nil {
		return
	}
	row := t.Row(r.key)
	if len(r.cells) != len(t.Columns) {
		r.cells = make([]shapedText, len(t.Columns))
	}
	ink := Ink.Get(th)
	switch {
	case row.Accent:
		ink = Accent.Get(th)
	case row.Strong:
		ink = TableStrong.Get(th)
	case row.Faint:
		ink.A /= 2
	}
	size := TextSize.Get(th)
	if t.lift.dragging && (t.lift.key == r.key || t.marked[t.lift.key] && t.marked[r.key]) {
		// On its way elsewhere: dimmed while it is dragged.
		ink.A /= 2
	}
	for i, s := range row.Cells {
		if i >= len(t.xs) {
			break
		}
		x, w := t.xs[i][0]-t.left, t.xs[i][1]
		if x > box.W || x+w < 0 {
			continue
		}
		if i == 0 && row.Icon != nil && w >= 16 {
			// The icon first, the text after it, where the column has room for it.
			const side = 16
			c := ink
			if row.IconInk != nil {
				c = row.IconInk.Get(th)
				c.A = min(c.A, ink.A)
			}
			drawIcon(p, th, row.Icon, geom.Rc(x, (box.H-side)/2, side, side), c, 1)
			x, w = x+side+6, w-side-6
		}
		room := w - 12
		if room <= 0 {
			continue
		}
		run, shown, _ := cellText(&r.cells[i], faceIn(t.Columns[i].Face, th), s, size, room)
		at := x
		if t.Columns[i].End {
			at = x + w - 12 - shown
		}
		run.PaintFaded(p, geom.Pt(at, (box.H-run.Height())/2), ink, room, TextFade.Get(th))
	}
}

// Handle implements [gunim.Handler]: a press puts the cursor on the
// row, and a double click activates it.
//
// With DragRows set, a press and a move of a few pixels drags the row,
// or the rows marked when it is one of them.
func (r *tableRow) Handle(e input.Event, u *gunim.UI) bool {
	t := r.t
	switch e := e.(type) {
	case input.Scroll:
		// A sideways wheel scrolls the columns, before the list takes the wheel to scroll the rows.
		return t.wheel(e, u)
	case input.PointerMove:
		if !t.lift.armed || t.lift.dragging || t.lift.key != r.key {
			return false
		}
		if d := r.inTable(e.Pos, u).Sub(t.lift.at); d.X*d.X+d.Y*d.Y >= pickUp*pickUp {
			t.startDrag(u)
		}
		return true
	case input.PointerUp:
		armed := t.lift.armed
		if !t.lift.dragging {
			t.lift = tableLift{}
		}
		return armed
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if i, ok := t.index[r.key]; ok {
			t.cursor = i
			u.Invalidate()
		}
		if e.Clicks == 2 && t.OnActivate != nil {
			t.lift = tableLift{}
			act(u, t, gunim.CuePress, t.OnActivate, r.key)
			return true
		}
		if t.DragRows != nil {
			t.lift = tableLift{key: r.key, at: r.inTable(e.Pos, u), armed: true}
		}
		return true
	}
	return false
}

// inTable returns p, in the row's space, in the table's.
func (r *tableRow) inTable(p geom.Point, u *gunim.UI) geom.Point {
	rb, ok1 := u.Bounds(r)
	tb, ok2 := u.Bounds(r.t)
	if !ok1 || !ok2 {
		return p
	}
	return p.Add(rb.Min.Sub(tb.Min))
}

// startDrag drags the row pressed, or the rows marked with it.
func (t *Table) startDrag(u *gunim.UI) {
	keys := []Key{t.lift.key}
	if t.marked[t.lift.key] {
		keys = t.Marked()
	}
	data, ghost, grab := t.DragRows(keys, t.lift.at)
	if data == nil {
		t.lift = tableLift{}
		return
	}
	t.lift.dragging = true
	u.StartDrag(t, data, ghost, grab)
	u.Invalidate()
}
