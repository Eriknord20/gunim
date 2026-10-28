package filemanager

import (
	"cmp"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// The trash: a page over the browser that lists what the trash holds,
// where the items selected go back where they came from, or are deleted
// for good, and the trash is emptied. Escape closes it.

func registerTrashCan(w *gunim.Window) {
	gunim.RegisterView(w, "trash-can", func(Trash) *trashPage { return newTrashPage() }, (*trashPage).set)
}

// The page's columns, by their place in the grid.
const (
	trashColName = iota
	trashColFrom
	trashColDeleted
	trashColSize
)

// trashPage is the trash page.
type trashPage struct {
	anim.Group
	state   Trash
	in      *anim.Float
	closing bool

	back    *widget.IconButton
	title   *widget.Label
	restore *widget.Button
	remove  *widget.Button
	empty   *widget.Button
	again   *widget.IconButton
	grid    *widget.DataGrid
	menu    *widget.ContextMenu
	// msg says why the grid is empty, over it, and foot what the trash
	// holds, or what came of the last act.
	msg  *widget.Label
	foot *widget.Label

	// rows are the state's rows in the order shown, sorted by the column
	// sort, down where desc is set.
	rows []TrashRow
	sort int
	desc bool
	// menuActs are what the items of the context menu do.
	menuActs []string
}

func newTrashPage() *trashPage {
	pg := &trashPage{in: anim.NewFloat(0), sort: trashColDeleted, desc: true}
	pg.Add(pg.in)
	pg.back = widget.NewIconButton(icon.ArrowLeft, "Back to the files (Escape)")
	pg.back.OnClick = func(u *gunim.UI) gunim.Intent { pg.close(u); return nil }
	pg.title = widget.NewLabel("Trash")
	pg.title.Face, pg.title.Size, pg.title.MaxLines = widget.BoldFont, TitleText, 1
	pg.restore = widget.NewButton("Restore")
	pg.restore.Icon = icon.Undo2
	pg.restore.Tooltip = "Put the items selected back where they came from"
	pg.restore.OnClick = func(u *gunim.UI) gunim.Intent { return pg.act(TrashRestore) }
	pg.remove = widget.NewButton("Delete permanently")
	pg.remove.Icon = icon.Trash2
	pg.remove.Tooltip = "Delete the items selected for good (Delete)"
	pg.remove.OnClick = func(u *gunim.UI) gunim.Intent { return pg.act(TrashDelete) }
	pg.empty = widget.NewButton("Empty")
	pg.empty.Kind = widget.ButtonDanger
	pg.empty.Tooltip = "Delete everything in the trash for good"
	pg.empty.OnClick = func(u *gunim.UI) gunim.Intent {
		return TrashAct{Seq: pg.state.Seq, Act: TrashEmpty}
	}
	pg.again = widget.NewIconButton(icon.RefreshCw, "Read the trash again (F5)")
	pg.again.OnClick = func(u *gunim.UI) gunim.Intent {
		return TrashAct{Seq: pg.state.Seq, Act: TrashRead}
	}
	pg.grid = widget.NewDataGrid(
		widget.GridColumn{Title: "Name"},
		widget.GridColumn{Title: "Original location"},
		widget.GridColumn{Title: "Date deleted", Width: 130, Fit: true},
		widget.GridColumn{Title: "Size", Width: 80, End: true, Fit: true},
	)
	pg.grid.Multi = true
	pg.grid.Row = pg.row
	pg.grid.OnSelectRows = func([][2]int, int, *gunim.UI) gunim.Intent {
		pg.enable()
		return nil
	}
	pg.grid.OnHeader = func(c int, u *gunim.UI) gunim.Intent {
		pg.sortBy(c, u)
		return nil
	}
	pg.grid.Copy = func(i int) string {
		if i < 0 || i >= len(pg.rows) {
			return ""
		}
		return pg.rows[i].Path
	}
	pg.menu = widget.NewContextMenu(pg.grid, nil)
	pg.menu.Prepare = func(geom.Point, *gunim.UI) bool {
		items, acts := pg.menuItems()
		pg.menuActs = acts
		pg.menu.SetItems(items)
		return len(items) > 0
	}
	pg.menu.OnPick = func(i int, u *gunim.UI) gunim.Intent { return pg.menuPicked(i, u) }
	pg.msg = widget.NewLabel("")
	pg.msg.Color, pg.msg.Align = Faint, text.AlignCenter
	pg.foot = widget.NewLabel("")
	pg.foot.Size, pg.foot.MaxLines = SmallText, 2
	return pg
}

// set takes the page's state.
func (pg *trashPage) set(s Trash, u *gunim.UI) {
	first := pg.state.Seq == 0
	keep := pg.selectedKeys()
	pg.state = s
	title := s.Title
	if s.Where != "" {
		title += " on " + s.Where
	}
	pg.title.Text = title
	pg.empty.Label = "Empty " + trashNoun(s.Title)
	pg.arrange()
	pg.grid.SetRows(len(pg.rows), u)
	// The items selected stay selected, where they are still there.
	var sel [][2]int
	for i, r := range pg.rows {
		if slices.Contains(keep, r.Key) {
			sel = append(sel, [2]int{i, i + 1})
		}
	}
	cursor := -1
	if len(sel) > 0 {
		cursor = sel[0][0]
	}
	pg.grid.SetSelectedRows(sel, cursor, u)
	switch {
	case s.Err != "":
		pg.msg.Color, pg.msg.Text = ErrorInk, s.Err
	case s.Reading && len(s.Rows) == 0:
		pg.msg.Color, pg.msg.Text = Faint, "Reading the "+trashNoun(s.Title)+"…"
	case len(s.Rows) == 0:
		pg.msg.Color, pg.msg.Text = Faint, "The "+trashNoun(s.Title)+" is empty."
	default:
		pg.msg.Text = ""
	}
	pg.foot.Color, pg.foot.Text = Faint, pg.summary()
	if s.Note != "" {
		pg.foot.Color, pg.foot.Text = Faint, s.Note
		if s.NoteErr {
			pg.foot.Color = ErrorInk
		}
	}
	pg.enable()
	if first {
		for _, n := range []gunim.Node{pg.back, pg.title, pg.restore, pg.remove, pg.empty, pg.again, pg.menu, pg.msg, pg.foot} {
			u.Insert(pg, n)
		}
		if s.Take {
			u.Focus(pg.grid)
		}
	}
	u.Invalidate()
}

// summary says how many items the trash holds, and the bytes of its
// files.
func (pg *trashPage) summary() string {
	if len(pg.state.Rows) == 0 {
		return ""
	}
	var bytes int64
	for _, r := range pg.state.Rows {
		bytes += max(r.Bytes, 0)
	}
	return plural(len(pg.state.Rows), "item") + " · " + humanBytes(bytes) + " in files"
}

// arrange sorts the rows by the column picked, and shows its arrow.
func (pg *trashPage) arrange() {
	pg.rows = slices.Clone(pg.state.Rows)
	slices.SortStableFunc(pg.rows, func(a, b TrashRow) int {
		var c int
		switch pg.sort {
		case trashColName:
			c = cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case trashColFrom:
			c = cmp.Compare(strings.ToLower(a.From), strings.ToLower(b.From))
		case trashColDeleted:
			c = cmp.Compare(a.DeletedUnix, b.DeletedUnix)
		case trashColSize:
			c = cmp.Compare(a.Bytes, b.Bytes)
		}
		if pg.desc {
			c = -c
		}
		return c
	})
	for i := range pg.grid.Columns {
		pg.grid.Columns[i].Sort = 0
	}
	pg.grid.Columns[pg.sort].Sort = 1
	if pg.desc {
		pg.grid.Columns[pg.sort].Sort = -1
	}
}

// sortBy sorts the rows by column c, or the other way round where they
// are already, and keeps the items selected.
func (pg *trashPage) sortBy(c int, u *gunim.UI) {
	if c < 0 || c >= len(pg.grid.Columns) {
		return
	}
	keep := pg.selectedKeys()
	if c == pg.sort {
		pg.desc = !pg.desc
	} else {
		// Dates and sizes start with the latest and the largest.
		pg.sort, pg.desc = c, c == trashColDeleted || c == trashColSize
	}
	pg.arrange()
	var sel [][2]int
	for i, r := range pg.rows {
		if slices.Contains(keep, r.Key) {
			sel = append(sel, [2]int{i, i + 1})
		}
	}
	cursor := -1
	if len(sel) > 0 {
		cursor = sel[0][0]
	}
	pg.grid.SetSelectedRows(sel, cursor, u)
	u.Invalidate()
}

// row is row i of the grid.
func (pg *trashPage) row(i int) (widget.GridRow, bool) {
	if i < 0 || i >= len(pg.rows) {
		return widget.GridRow{}, false
	}
	r := pg.rows[i]
	name := []widget.GridSpan{{Text: " ", Fill: TintToken(r.Tint)}, {Text: "  "}, {Text: r.Name}}
	if r.Dir {
		name[2].Face = widget.BoldFont
	}
	return widget.GridRow{Cells: [][]widget.GridSpan{
		name,
		{{Text: r.From, Ink: Faint}},
		{{Text: r.Deleted, Ink: Faint}},
		{{Text: r.Size, Ink: Faint}},
	}}, true
}

// selectedKeys are the keys of the items selected.
func (pg *trashPage) selectedKeys() []string {
	var keys []string
	for _, run := range pg.grid.SelectedRows() {
		for i := run[0]; i < run[1] && i < len(pg.rows); i++ {
			keys = append(keys, pg.rows[i].Key)
		}
	}
	return keys
}

// enable lets the buttons be clicked where they have something to do.
func (pg *trashPage) enable() {
	none := len(pg.grid.SelectedRows()) == 0
	busy := pg.state.Busy
	pg.restore.Disabled = none || busy
	pg.remove.Disabled = none || busy
	pg.empty.Disabled = busy || len(pg.state.Rows) == 0 && pg.state.Err == "" && !pg.state.Reading
}

// act asks for act on the items selected, where there are any and the
// page is not busy.
func (pg *trashPage) act(act string) gunim.Intent {
	keys := pg.selectedKeys()
	if len(keys) == 0 || pg.state.Busy {
		return nil
	}
	return TrashAct{Seq: pg.state.Seq, Keys: keys, Act: act}
}

// menuItems are the items of the context menu of the items selected, and
// the acts they do.
func (pg *trashPage) menuItems() (items []widget.MenuItem, acts []string) {
	if len(pg.grid.SelectedRows()) == 0 {
		return nil, nil
	}
	busy := pg.state.Busy
	items = []widget.MenuItem{
		{Label: "Restore", Disabled: busy},
		{Label: "Show original folder"},
		{Label: "Copy original path", Hint: "Ctrl+C"},
		{Label: "Delete permanently", Hint: "Delete", Break: true, Disabled: busy},
	}
	return items, []string{TrashRestore, TrashShow, "copy", TrashDelete}
}

// menuPicked does what item i of the context menu asks.
func (pg *trashPage) menuPicked(i int, u *gunim.UI) gunim.Intent {
	if i < 0 || i >= len(pg.menuActs) {
		return nil
	}
	switch act := pg.menuActs[i]; act {
	case "copy":
		var paths []string
		for _, run := range pg.grid.SelectedRows() {
			for r := run[0]; r < run[1] && r < len(pg.rows); r++ {
				paths = append(paths, pg.rows[r].Path)
			}
		}
		u.SetClipboard(strings.Join(paths, "\n"))
		return nil
	case TrashShow:
		keys := pg.selectedKeys()
		if len(keys) == 0 {
			return nil
		}
		return TrashAct{Seq: pg.state.Seq, Keys: keys[:1], Act: TrashShow}
	default:
		return pg.act(act)
	}
}

// close takes the page away, and tells the application.
func (pg *trashPage) close(u *gunim.UI) {
	if pg.closing {
		return
	}
	pg.closing = true
	u.Remove(pg)
	u.Send(pg, TrashClosed{Seq: pg.state.Seq})
}

// Focusable implements [gunim.Focusable].
func (pg *trashPage) Focusable() bool { return !pg.closing }

// Transition implements [gunim.Transitioner]: the page rises into the
// browser and sinks away.
func (pg *trashPage) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		pg.in.Animate(1, Page.Get(f.Theme))
	case gunim.Exiting:
		pg.in.Animate(0, widget.Settle.Get(f.Theme))
	case gunim.Present:
	}
	return !pg.in.Active()
}

// Handle implements [gunim.Handler]: Escape closes the page, Delete
// deletes the items selected for good, after asking, and F5 reads the
// trash again. It keeps every other key from the browser behind it, whose
// keys act on the files.
func (pg *trashPage) Handle(e input.Event, u *gunim.UI) bool {
	if pg.closing {
		return false
	}
	switch e := e.(type) {
	case input.KeyPress:
		switch {
		case e.Key == input.KeyEscape && e.Mods == 0:
			pg.close(u)
		case e.Key == input.KeyF5 && e.Mods == 0:
			u.Send(pg, TrashAct{Seq: pg.state.Seq, Act: TrashRead})
		case e.Key == input.KeyDelete && (e.Mods == 0 || e.Mods == input.ModShift):
			if in := pg.act(TrashDelete); in != nil {
				u.Send(pg, in)
			}
		default:
			return browserKey(e)
		}
		return true
	case input.HistoryStep:
		// The mouse's Back button goes back to the files.
		if !e.Forward {
			pg.close(u)
		}
		return true
	case input.PointerDown, input.PointerUp, input.PointerMove, input.Scroll:
		// Nothing under the page hears the pointer.
		return true
	}
	return false
}

// The page's room: the height of its head and its foot.
const (
	trashHeadH = 52
	trashFootH = 34
)

// Layout implements [gunim.Node]: the head along the top, with the
// buttons at its right, the grid under it, and the foot along the bottom.
// The message, when there is one, goes in the middle of the grid.
func (pg *trashPage) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	w := max(0, box.W-2*usageMargin)
	right := box.W - usageMargin
	for _, n := range []gunim.Node{pg.again, pg.empty, pg.remove, pg.restore} {
		for k := range kids.All {
			if k.Node() != n {
				continue
			}
			s := k.Layout(gunim.Constraints{Max: geom.Sz(200, 40)})
			right -= s.W
			k.Place(geom.Pt(right, (trashHeadH-s.H)/2))
			right -= usageGap
		}
	}
	left := float32(usageMargin + 38)
	bodyTop := float32(trashHeadH)
	bodyH := max(0, box.H-bodyTop-trashFootH)
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(pg.back):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(40, 40)})
			k.Place(geom.Pt(usageMargin-6, (trashHeadH-s.H)/2))
		case gunim.Node(pg.title):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, right-left), 0)})
			k.Place(geom.Pt(left, (trashHeadH-s.H)/2))
		case gunim.Node(pg.menu):
			k.Layout(gunim.Tight(geom.Sz(w, bodyH)))
			k.Place(geom.Pt(usageMargin, bodyTop))
		case gunim.Node(pg.msg):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, w-40), bodyH)})
			k.Place(geom.Pt(usageMargin+(w-s.W)/2, bodyTop+min(bodyH/2, 120)))
		case gunim.Node(pg.foot):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(w, trashFootH)})
			k.Place(geom.Pt(usageMargin, box.H-trashFootH+(trashFootH-s.H)/2))
		}
	}
	return box
}

// Paint implements [gunim.Node]: the page over the browser, rising into
// it as it opens.
func (pg *trashPage) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(pg.in.Value(), 0), 1)
	r := geom.Rect{Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: in})()
	defer p.Push(paint.Translate(geom.Pt(0, 14*(1-in))))()
	p.RRect(r, 0, paint.Solid(PaneFill.Get(f.Theme)))
	for k := range kids.All {
		k.Paint(p)
	}
}
