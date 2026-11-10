package filemanager

import (
	"cmp"
	"image/color"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
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
	title   *wordsLabel
	restore *widget.Button
	remove  *widget.Button
	empty   *widget.Button
	again   *widget.IconButton
	grid    *widget.DataGrid
	menu    *widget.ContextMenu
	// msg says why the grid is empty, over it, and foot what the trash
	// holds, or what came of the last act.
	msg  *wordsLabel
	foot *wordsLabel

	// rows are the state's rows in the order shown, sorted by the column
	// sort, down where desc is set.
	rows []TrashRow
	sort int
	desc bool
	// menuActs are what the items of the context menu do.
	menuActs []string
	// heading is the title, which a narrow page leaves out.
	heading string
	// msgFade, footFade and titleFade fade the page's words as they
	// change, and titled says the title has been laid out once.
	msgFade, footFade, titleFade *fader
	titled                       bool
}

func newTrashPage() *trashPage {
	pg := &trashPage{in: anim.NewFloat(0), sort: trashColDeleted, desc: true}
	pg.Add(pg.in)
	pg.back = widget.NewIconButton(icon.ArrowLeft, "Back to the files (Escape)")
	pg.back.OnClick = func(u *gunim.UI) gunim.Intent { pg.close(u); return nil }
	pg.title = &wordsLabel{widget.NewLabel("Trash")}
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
	// A click leaves the keyboard on the items, where its keys act.
	pg.again = widget.NewIconButton(icon.RefreshCw, "Read the trash again (F5)")
	for _, b := range []*widget.Button{pg.restore, pg.remove, pg.empty, &pg.again.Button} {
		b.KeepFocus = true
	}
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
	pg.msg = &wordsLabel{widget.NewLabel("")}
	pg.msg.Color, pg.msg.Align = Faint, text.AlignCenter
	pg.foot = &wordsLabel{widget.NewLabel("")}
	pg.foot.Size, pg.foot.MaxLines = SmallText, 2
	pg.msgFade, pg.footFade, pg.titleFade = newFader(pg.msg.Label), newFader(pg.foot.Label), newFader(pg.title.Label)
	pg.Add(pg.msgFade.in, pg.footFade.in, pg.titleFade.in)
	return pg
}

// wordsLabel is a label of the page's words, which leaves the pointer to
// what is under it: a message fading out over the rows is no reason for a
// click on them to go nowhere.
type wordsLabel struct{ *widget.Label }

// Covers implements [gunim.Shaped]: none of it.
func (*wordsLabel) Covers(geom.Point) bool { return false }

// fader fades a label's text as it changes: a text that goes fades out
// where it was, and only then does the next come in, from nothing.
type fader struct {
	label *widget.Label
	in    *anim.Float
	// to is the text it shows or is on its way to, and ink its colour.
	to  string
	ink theme.Token[color.NRGBA]
	// swap says the label still shows the words before to, fading out,
	// and at says where the label paints in the page, for its layer.
	swap bool
	at   geom.Rect
}

func newFader(l *widget.Label) *fader { return &fader{label: l, in: anim.NewFloat(0), ink: l.Color} }

// show fades words in, in ink, or the words showing out where words is
// empty. Without th, before the page is laid out, it shows at once.
func (f *fader) show(words string, ink theme.Token[color.NRGBA], th *theme.Live) {
	if words == f.to && ink.Key() == f.ink.Key() {
		return
	}
	f.to, f.ink = words, ink
	if th == nil {
		f.swap = false
		f.label.Text, f.label.Color = words, ink
		f.in.Jump(map[bool]float32{false: 0, true: 1}[words != ""])
		return
	}
	// Retarget, as words that go slowly can be cut short by others, and
	// those going quickly for others can be let go slowly after all.
	if words == "" {
		f.swap = false
		f.in.Retarget(0, widget.Settle.Get(th))
		return
	}
	if f.alpha() > 0 && f.label.Text != "" && (f.label.Text != words || f.label.Color.Key() != ink.Key()) {
		// Other words, or the same in another ink, showing go first; next
		// takes their place.
		f.swap = true
		f.in.Retarget(0, widget.Quick.Get(th))
		return
	}
	// Nothing showing, or the same words back while they fade: on from
	// where it is.
	f.swap = false
	f.label.Text, f.label.Color = words, ink
	f.in.Retarget(1, widget.Quick.Get(th))
}

// next brings in the words to, once those before them have faded out.
// The page asks it each layout, which every frame of a fade has.
func (f *fader) next(th *theme.Live) {
	if !f.swap || f.alpha() > 0 {
		return
	}
	f.swap = false
	f.label.Text, f.label.Color = f.to, f.ink
	f.in.Animate(1, widget.Quick.Get(th))
}

// alpha is how far the text shows, from 0 to 1.
func (f *fader) alpha() float32 { return min(max(f.in.Value(), 0), 1) }

// set takes the page's state.
func (pg *trashPage) set(s Trash, u *gunim.UI) {
	first := pg.state.Seq == 0
	fresh := first || s.RowsGen != pg.state.RowsGen
	keep := keySet(pg.selectedKeys())
	var th *theme.Live
	if !first {
		th = u.Theme()
	}
	if fresh && !first {
		// The rows that went, as restored or deleted, leave while the
		// grid still draws them as they were.
		// Where none went, or the rows changed otherwise, rows still
		// leaving from before would stand where the new rows are.
		gone, ok := pg.gone(s.Rows)
		if !ok {
			gone = nil
		}
		pg.grid.Leave(gone, u)
	}
	pg.state = s
	title := s.Title
	if s.Where != "" {
		title += " on " + s.Where
	}
	pg.heading = title
	pg.empty.Label = "Empty " + trashNoun(s.Title)
	if fresh {
		pg.arrange()
		pg.grid.SetRows(len(pg.rows), u)
		// The items selected stay selected, where they are still there.
		pg.reselect(keep, u)
	}
	switch {
	case s.Err != "":
		pg.msgFade.show(s.Err, ErrorInk, th)
	case s.Reading && len(s.Rows) == 0:
		pg.msgFade.show("Reading the "+trashNoun(s.Title)+"…", Faint, th)
	case len(s.Rows) == 0:
		pg.msgFade.show("The "+trashNoun(s.Title)+" is empty.", Faint, th)
	default:
		pg.msgFade.show("", Faint, th)
	}
	switch {
	case s.Note == "":
		pg.footFade.show(pg.summary(), Faint, th)
	case s.NoteErr:
		pg.footFade.show(s.Note, ErrorInk, th)
	default:
		pg.footFade.show(s.Note, Faint, th)
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

// arrange sorts the rows by the column picked, and shows its arrow. A
// column of words sorts by each row's words in lower case, made once.
func (pg *trashPage) arrange() {
	type sorted struct {
		row   TrashRow
		lower string
	}
	rows := make([]sorted, len(pg.state.Rows))
	for i, r := range pg.state.Rows {
		rows[i].row = r
		switch pg.sort {
		case trashColName:
			rows[i].lower = strings.ToLower(r.Name)
		case trashColFrom:
			rows[i].lower = strings.ToLower(r.From)
		}
	}
	slices.SortStableFunc(rows, func(a, b sorted) int {
		var c int
		switch pg.sort {
		case trashColName, trashColFrom:
			c = cmp.Compare(a.lower, b.lower)
		case trashColDeleted:
			c = cmp.Compare(a.row.DeletedUnix, b.row.DeletedUnix)
		case trashColSize:
			c = cmp.Compare(a.row.Bytes, b.row.Bytes)
		}
		if pg.desc {
			c = -c
		}
		return c
	})
	pg.rows = pg.rows[:0]
	for _, r := range rows {
		pg.rows = append(pg.rows, r.row)
	}
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
	keep := keySet(pg.selectedKeys())
	// Rows still leaving stand where they were, which a sort changes.
	pg.grid.Leave(nil, u)
	if c == pg.sort {
		pg.desc = !pg.desc
	} else {
		// Dates and sizes start with the latest and the largest.
		pg.sort, pg.desc = c, c == trashColDeleted || c == trashColSize
	}
	pg.arrange()
	pg.reselect(keep, u)
	u.Invalidate()
}

// reselect selects the rows of the keys in keep.
func (pg *trashPage) reselect(keep map[string]bool, u *gunim.UI) {
	var sel [][2]int
	for i, r := range pg.rows {
		if keep[r.Key] {
			sel = append(sel, [2]int{i, i + 1})
		}
	}
	cursor := -1
	if len(sel) > 0 {
		cursor = sel[0][0]
	}
	pg.grid.SetSelectedRows(sel, cursor, u)
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

// gone are the indexes of the rows shown whose items rows no longer has.
// It is false where none went, or where rows has items the rows shown do
// not: the grid closes the gaps of rows leaving over the rows that stay,
// in the order they had, which only holds where rows are those shown less
// the ones gone.
func (pg *trashPage) gone(rows []TrashRow) ([]int, bool) {
	have := make(map[string]bool, len(rows))
	for _, r := range rows {
		have[r.Key] = true
	}
	var out []int
	for i, r := range pg.rows {
		if !have[r.Key] {
			out = append(out, i)
		}
	}
	return out, len(out) > 0 && len(pg.rows)-len(out) == len(rows)
}

// keySet is keys as a set.
func keySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
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
	// Not before the trash lists something, as it may not open at all.
	pg.empty.Disabled = busy || len(pg.state.Rows) == 0
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

// The page's room: the height of its head and its foot, and the least
// width its title shows in.
const (
	trashHeadH      = 52
	trashFootH      = 34
	trashTitleLeast = 90
)

// Layout implements [gunim.Node]: the head along the top, with the
// buttons at its right, the grid under it, and the foot along the bottom.
// The message, when there is one, goes in the middle of the grid.
func (pg *trashPage) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	for _, fade := range []*fader{pg.msgFade, pg.footFade, pg.titleFade} {
		fade.next(f.Theme)
	}
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
			// Narrow, the title fades out and gives its room to the
			// buttons; a width of 0 would be no limit. The first layout
			// shows it as the width has it at once.
			room := right - left
			th := f.Theme
			if !pg.titled {
				th, pg.titled = nil, true
			}
			if room < trashTitleLeast {
				pg.titleFade.show("", widget.Ink, th)
			} else {
				pg.titleFade.show(pg.heading, widget.Ink, th)
			}
			s := k.Layout(gunim.Constraints{Max: geom.Sz(max(room, 1), 0)})
			at := geom.Pt(left, (trashHeadH-s.H)/2)
			k.Place(at)
			pg.titleFade.at = inkOf(pg.title.Label, f, at, s)
		case gunim.Node(pg.menu):
			k.Layout(gunim.Tight(geom.Sz(w, bodyH)))
			k.Place(geom.Pt(usageMargin, bodyTop))
		case gunim.Node(pg.msg):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, w-40), bodyH)})
			at := geom.Pt(usageMargin+(w-s.W)/2, bodyTop+min(bodyH/2, 120))
			k.Place(at)
			pg.msgFade.at = inkOf(pg.msg.Label, f, at, s)
		case gunim.Node(pg.foot):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(w, trashFootH)})
			at := geom.Pt(usageMargin, box.H-trashFootH+(trashFootH-s.H)/2)
			k.Place(at)
			pg.footFade.at = inkOf(pg.foot.Label, f, at, s)
		}
	}
	return box
}

// inkOf is where label, placed at the point at and laid out at s,
// paints: all of its lines, which run on below s where the page has less
// height for them. A fade's layer smaller than that would leave the lines
// below it drawn at the alpha they first had.
func inkOf(label *widget.Label, f gunim.Frame, at geom.Point, s geom.Size) geom.Rect {
	full := label.Layout(gunim.Constraints{Max: geom.Sz(s.W, 0)}, f, gunim.Children{})
	return geom.Rect{Min: at, Max: at.Add(geom.Sz(s.W, max(s.H, full.H)).Point())}
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
		var fade *fader
		switch k.Node() {
		case gunim.Node(pg.msg):
			fade = pg.msgFade
		case gunim.Node(pg.foot):
			fade = pg.footFade
		case gunim.Node(pg.title):
			fade = pg.titleFade
		}
		if fade == nil {
			k.Paint(p)
			continue
		}
		if a := fade.alpha(); a > 0 {
			// A layer the size of the words, not of the page.
			done := p.Layer(paint.LayerOpts{Bounds: fade.at, Opacity: a})
			k.Paint(p)
			done()
		}
	}
}
