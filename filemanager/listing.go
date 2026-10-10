package filemanager

import (
	"strings"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// maxBlocks is how many blocks of rows a page keeps.
const maxBlocks = 64

func registerListing(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, l Listing, u *gunim.UI) {
		b.title.setListing(l, u)
		b.path.setListing(l, u)
		b.listing.setListing(l, u)
	})
	gunim.RegisterPatch(w, "browser", func(b *browser, r RowBlock, u *gunim.UI) { b.listing.rows(r, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, s Selection, u *gunim.UI) { b.listing.selection(s, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, s Bands, u *gunim.UI) { b.listing.bands(s, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, _ FocusListing, u *gunim.UI) { b.focusListing(u) })
}

// columns are the grid's columns, in the order of SortBy.
func columns() []widget.GridColumn {
	return []widget.GridColumn{
		{Title: "Name"},
		{Title: "Size", Width: 96, End: true},
		{Title: "Modified", Width: 150},
		{Title: "Type", Width: 150},
	}
}

// listingArea is the main area: a deck of listing pages, one for each
// folder gone into, the one showing on top.
type listingArea struct {
	b      *browser
	deck   *deck
	cur    *listingPage
	path   string
	widths []float32
	view   ViewMode
}

func newListingArea(b *browser) *listingArea {
	return &listingArea{b: b, deck: &deck{}}
}

// grid returns the grid showing, or nil.
func (a *listingArea) grid() *widget.DataGrid {
	if a.cur == nil {
		return nil
	}
	return a.cur.grid
}

// setListing shows l: on a new page that slides in when it is another
// folder, and on the page showing when it is the same one sorted,
// filtered or read again.
func (a *listingArea) setListing(l Listing, u *gunim.UI) {
	if a.cur == nil || l.Path != a.path {
		// The first folder takes the keyboard in a window of its own; in
		// a pane, the program gives it when the user turns to the pane.
		focused := a.cur == nil && !a.b.shell.Pane || a.cur != nil && u.Focused() == a.cur.focusNode()
		if a.cur != nil {
			a.widths = a.cur.widths()
		}
		a.path = l.Path
		// A file's own icon is of its folder alone: those of the folder
		// left go, as the app half forgets it sent them.
		for k := range a.b.icons {
			if strings.HasPrefix(k, "file:") {
				delete(a.b.icons, k)
			}
		}
		a.cur = newListingPage(a.b, a.widths)
		a.cur.icons.show(a.view.Icons && a.b.shell.Paths.Same(a.view.Path, l.Path), false, u)
		a.deck.show(a.cur, l.Travel, u)
		if focused {
			u.Focus(a.cur.focusNode())
		}
	}
	a.cur.set(l, u)
}

func (a *listingArea) rows(r RowBlock, u *gunim.UI) {
	if a.cur != nil && r.Gen == a.cur.gen {
		a.cur.leave(u)
		a.cur.blocks[r.Start] = r.Rows
		delete(a.cur.asked, r.Start)
		a.cur.forget()
		if a.cur.arrive {
			a.cur.arrive = false
			a.cur.grid.Arrive(u)
			a.cur.icons.arrive(u)
		}
		u.Invalidate()
	}
}

func (a *listingArea) selection(s Selection, u *gunim.UI) {
	if a.cur == nil || s.Gen != a.cur.gen {
		return
	}
	g := a.cur.grid
	if s.Slide {
		g.SlideSelectedRows(s.Runs, s.Cursor, u)
	} else {
		g.SetSelectedRows(s.Runs, s.Cursor, u)
	}
	if s.Cursor >= 0 {
		top, vis := g.Top(), g.Visible()
		if float64(s.Cursor) < top || float64(s.Cursor) >= top+vis-1 {
			g.ScrollTo(float64(s.Cursor)-vis/2, u)
		}
	}
	a.cur.icons.selection(s, u)
}

func (a *listingArea) bands(s Bands, u *gunim.UI) {
	if a.cur == nil || s.Gen != a.cur.gen {
		return
	}
	a.cur.setBands(s.Bands)
	u.Invalidate()
}

// selectAll and selectNone work the grid as Ctrl+A and Escape do.
func (a *listingArea) selectAll(u *gunim.UI) {
	if a.cur != nil && a.cur.icons.on {
		a.cur.icons.selectAll(u)
		return
	}
	if g := a.grid(); g != nil && g.Rows() > 0 {
		g.SetSelectedRows([][2]int{{0, g.Rows()}}, 0, u)
		u.Send(g, Selected{Gen: a.cur.gen, Runs: [][2]int{{0, g.Rows()}}, Cursor: 0})
	}
}

func (a *listingArea) selectNone(u *gunim.UI) {
	if a.cur != nil && a.cur.icons.on {
		a.cur.icons.selectNone(u)
		return
	}
	if g := a.grid(); g != nil {
		g.SetSelectedRows(nil, -1, u)
		u.Send(g, Selected{Gen: a.cur.gen, Cursor: -1})
	}
}

// Children implements [gunim.Composite].
func (a *listingArea) Children() []gunim.Node { return []gunim.Node{a.deck} }

// Layout implements [gunim.Node].
func (a *listingArea) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (a *listingArea) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// listingPage is one folder's listing: the grid, its overview strip, a
// line that says why there is nothing to show, and a bar while the
// folder is read.
type listingPage struct {
	anim.Group
	b      *browser
	grid   *widget.DataGrid
	strip  *widget.Overview
	msg    *widget.Label
	bar    *widget.ProgressBar
	menu   *widget.ContextMenu
	gen    int
	filter string
	// blocks are the rows of gen by their first, stale the rows of the gen
	// before, shown until the new ones arrive, and asked the blocks asked
	// for.
	blocks, stale map[int][]Row
	asked         map[int]bool
	first         int
	firstNames    []string
	loading       bool
	// arrive has the next rows come in one after another, as they do in a
	// folder just opened or sorted anew; sorted is the sort they came in.
	arrive bool
	sorted [2]int
	// stripIn runs from 0 to 1 as the overview strip comes in, which it
	// does only while the rows do not all fit.
	stripIn *anim.Float
	icons   *iconView
	// left holds the rows to leave once the rows of gen arrive.
	left *RowsLeft
}

func newListingPage(b *browser, widths []float32) *listingPage {
	pg := &listingPage{b: b, blocks: map[int][]Row{}, stale: map[int][]Row{}, asked: map[int]bool{}, gen: -1,
		stripIn: anim.NewFloat(0), arrive: true, sorted: [2]int{-1, -1}}
	pg.Add(pg.stripIn)
	cols := columns()
	for i, w := range widths {
		if i < len(cols) && cols[i].Width > 0 {
			cols[i].Width = w
		}
	}
	g := widget.NewDataGrid(cols...)
	g.Multi = true
	g.NoBar = true
	g.Glide = true
	g.Row = pg.row
	g.OnView = func(first, count int, _ *gunim.UI) gunim.Intent { return pg.need(first, count) }
	g.OnSelectRows = func(sel [][2]int, cursor int, u *gunim.UI) gunim.Intent {
		return Selected{Gen: pg.gen, Runs: sel, Cursor: cursor}
	}
	g.OnActivate = func(row int, u *gunim.UI) gunim.Intent { return Activated{Gen: pg.gen, Row: row} }
	g.OnType = func(text string, u *gunim.UI) gunim.Intent { return Typed{Gen: pg.gen, Text: text} }
	g.OnHeader = func(c int, u *gunim.UI) gunim.Intent { return SortClicked{Column: c} }
	g.OnCopy = func([][2]int, *gunim.UI) gunim.Intent { return Command{Name: CmdCopy} }
	g.DragRows = pg.dragRows
	g.OnDragEnd = func(e input.DragEnd, u *gunim.UI) gunim.Intent { return pg.b.dnd.dragEnded(e) }
	pg.grid = g
	pg.strip = widget.NewOverview(g)
	pg.strip.Top = widget.GridHeaderHeight
	pg.strip.Readout = pg.readout
	pg.menu = pg.contextMenu(g, g.RowAt, g.SelectedRows)
	pg.msg = widget.NewLabel("")
	pg.msg.Color = Faint
	pg.bar = widget.NewProgressBar()
	pg.icons = newIconView(pg)
	return pg
}

// widths returns the columns' widths, for the next page to keep.
func (pg *listingPage) widths() []float32 {
	out := make([]float32, len(pg.grid.Columns))
	for i, c := range pg.grid.Columns {
		out[i] = c.Width
	}
	return out
}

// set takes a listing of the page's folder.
func (pg *listingPage) set(l Listing, u *gunim.UI) {
	if l.Gen != pg.gen {
		// The rows showing stay until the new ones arrive.
		if len(pg.blocks) > 0 {
			pg.stale = pg.blocks
		}
		pg.blocks = map[int][]Row{}
		clear(pg.asked)
		pg.gen = l.Gen
	}
	pg.filter = strings.ToLower(l.Filter)
	if sorted := [2]int{int(l.Sort), map[bool]int{false: 0, true: 1}[l.Desc]}; sorted != pg.sorted {
		pg.sorted, pg.arrive = sorted, true
	}
	pg.loading = l.Loading
	// The bar moves only while it shows, so the window can rest.
	pg.bar.Indeterminate = l.Loading
	for i := range pg.grid.Columns {
		pg.grid.Columns[i].Sort = 0
		if SortBy(i) == l.Sort {
			pg.grid.Columns[i].Sort = map[bool]int{false: 1, true: -1}[l.Desc]
		}
	}
	pg.grid.SetRows(l.Total, u)
	pg.icons.grid.SetLen(l.Total, u)
	switch {
	case l.Err != "":
		pg.msg.Color = ErrorInk
		pg.msg.Text = l.Err
	case l.Loading:
		pg.msg.Text = ""
	case l.All == 0:
		pg.msg.Color = Faint
		pg.msg.Text = "This folder is empty."
	case l.Total == 0 && l.Filter != "":
		pg.msg.Color = Faint
		pg.msg.Text = "Nothing here matches “" + l.Filter + "”."
	case l.Total == 0:
		pg.msg.Color = Faint
		pg.msg.Text = "Everything here is hidden. Show hidden files with Ctrl+H."
	default:
		pg.msg.Text = ""
	}
	u.Invalidate()
}

func (pg *listingPage) setBands(bs []Band) {
	parts := make([]widget.OverviewBand, len(bs))
	pg.firstNames = pg.firstNames[:0]
	for i, b := range bs {
		parts[i] = widget.OverviewBand{Parts: overviewParts(b.Shares)}
		pg.firstNames = append(pg.firstNames, b.First)
	}
	pg.strip.Bands = parts
}

func (pg *listingPage) readout(b int) string {
	if b < 0 || b >= len(pg.firstNames) {
		return ""
	}
	return pg.firstNames[b]
}

// need asks for the blocks around the rows in view that the page lacks.
func (pg *listingPage) need(first, count int) gunim.Intent {
	pg.first = first
	var starts []int
	from := max(0, first-blockRows) / blockRows * blockRows
	for start := from; start < first+count+blockRows && start < pg.grid.Rows(); start += blockRows {
		if _, ok := pg.blocks[start]; !ok && !pg.asked[start] {
			pg.asked[start] = true
			starts = append(starts, start)
		}
	}
	if len(starts) == 0 {
		return nil
	}
	return NeedRows{Gen: pg.gen, Starts: starts}
}

// forget drops the blocks farthest from the view once there are many.
func (pg *listingPage) forget() {
	for len(pg.blocks) > maxBlocks {
		far, farthest := -1, -1
		for start := range pg.blocks {
			if d := abs(start - pg.first); d > farthest {
				far, farthest = start, d
			}
		}
		delete(pg.blocks, far)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// view returns row i, from the new rows or the stale ones.
func (pg *listingPage) view(i int) (Row, bool) {
	start := i / blockRows * blockRows
	for _, m := range []map[int][]Row{pg.blocks, pg.stale} {
		if block, ok := m[start]; ok && i-start < len(block) {
			return block[i-start], true
		}
	}
	return Row{}, false
}

// row is how the grid draws row i.
func (pg *listingPage) row(i int) (widget.GridRow, bool) {
	r, ok := pg.view(i)
	if !ok {
		return widget.GridRow{}, false
	}
	name := []widget.GridSpan{
		{Text: " ", Fill: TintToken(r.Tint)},
		{Text: "  "},
	}
	if r.IconKey != "" {
		// Windows' icon, in place of the colour's chip, or until it comes,
		// or where it could not be read, a clear one, so the names line up.
		img := clearIcon()
		if ic := pg.b.icons[r.IconKey]; ic.Small != nil {
			img = ic.Small
		}
		name = []widget.GridSpan{{Image: img, Faint: r.Hidden || r.Broken}, {Text: " "}}
	}
	// Before the name, as Explorer's Status column is, so a long name
	// does not push it out of sight.
	if mark, ok := cloudMark(r.Cloud); ok {
		name = append(name, mark, widget.GridSpan{Text: "  "})
	}
	at := len(name)
	name = append(name, widget.GridSpan{Text: r.Name, Faint: r.Hidden || r.Broken, Marks: marks(r.Name, pg.filter)})
	if r.Kind == KindLink {
		name = append(name, widget.GridSpan{Text: "  ↗", Ink: Caption})
	}
	if r.Dir {
		name[at].Face = widget.BoldFont
	}
	return widget.GridRow{Cells: [][]widget.GridSpan{
		name,
		{{Text: r.Size, Ink: Faint}},
		{{Text: r.Modified, Ink: Faint}},
		{{Text: r.Type, Ink: Faint}},
	}}, true
}

// cloudMark is the mark of how a cloud provider keeps an item, as
// Explorer shows it: a cloud for one online only, a tick for one on this
// device, and a pin for one always kept there. A folder whose
// state cannot be told keeps the mark's place, so the names line up.
func cloudMark(c CloudState) (widget.GridSpan, bool) {
	switch c {
	case CloudOnline:
		return widget.GridSpan{Icon: icon.Cloud, Ink: Caption}, true
	case CloudLocal:
		return widget.GridSpan{Icon: icon.CircleCheck, Ink: CloudLocalInk}, true
	case CloudPinned:
		return widget.GridSpan{Icon: icon.Pin, Ink: CloudPinnedInk}, true
	case CloudFolder:
		return widget.GridSpan{Icon: icon.CircleCheck, Ink: CloudBlank}, true
	case CloudNone:
	}
	return widget.GridSpan{}, false
}

// marks returns where filter is found in name, in runes, ignoring case.
func marks(name, filter string) [][2]int {
	if filter == "" {
		return nil
	}
	lower := strings.ToLower(name)
	at := strings.Index(lower, filter)
	if at < 0 {
		return nil
	}
	start := utf8.RuneCountInString(lower[:at])
	return [][2]int{{start, start + utf8.RuneCountInString(filter)}}
}

// Children implements [gunim.Composite].
func (pg *listingPage) Children() []gunim.Node {
	return []gunim.Node{pg.menu, pg.strip, pg.msg, pg.bar, pg.icons}
}

// Layout implements [gunim.Node].
func (pg *listingPage) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	grid, strip, msg, bar := kids.At(0), kids.At(1), kids.At(2), kids.At(3)
	fits := float64(pg.grid.Rows()) <= pg.grid.Visible() || pg.icons.on
	pg.stripIn.Animate(map[bool]float32{false: 1, true: 0}[fits], widget.Settle.Get(f.Theme))
	sw := widget.OverviewWidth.Get(f.Theme) * min(max(pg.stripIn.Value(), 0), 1)
	grid.Layout(gunim.Tight(geom.Sz(c.Max.W-sw, c.Max.H)))
	grid.Place(geom.Point{})
	strip.Layout(gunim.Tight(geom.Sz(widget.OverviewWidth.Get(f.Theme), c.Max.H)))
	strip.Place(geom.Pt(c.Max.W-sw, 0))
	head := widget.GridHeaderHeight.Get(f.Theme)
	ms := msg.Layout(gunim.Loose(geom.Sz(max(0, c.Max.W-64), c.Max.H)))
	msg.Place(geom.Pt((c.Max.W-ms.W)/2, head+max(24, (c.Max.H-head)/3-ms.H/2)))
	bar.Layout(gunim.Tight(geom.Sz(c.Max.W, 2)))
	bar.Place(geom.Pt(0, head))
	icons := kids.At(4)
	icons.Layout(gunim.Tight(c.Max))
	icons.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (pg *listingPage) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.Background.Get(f.Theme)))
	pg.icons.paintDetails(p, box, kids.At(0))
	if pg.stripIn.Value() > 0.001 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
			kids.At(1).Paint(p)
		}()
	}
	kids.At(4).Paint(p)
	if pg.msg.Text != "" {
		kids.At(2).Paint(p)
	}
	if pg.loading {
		kids.At(3).Paint(p)
	}
}
