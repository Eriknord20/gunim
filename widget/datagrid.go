package widget

import (
	"image/color"
	"math"
	"slices"
	"sort"
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

// DataGrid tokens.
var (
	GridRowHeight    = theme.Length("grid.row", 22)
	GridHeaderHeight = theme.Length("grid.header", 26)
	GridTextSize     = theme.Length("grid.text.size", 12.5)
	// GridCellPadding is the room at either side of a cell's text.
	GridCellPadding = theme.Length("grid.cell.padding", 6)
	// GridCursor lights the selected row, and GridRule marks a row that
	// starts something new, such as a session in a log.
	GridCursor = theme.Color("grid.cursor", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x38})
	GridRule   = theme.Color("grid.rule", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x90})
	// GridPending draws a row that has not arrived yet.
	GridPending = theme.Color("grid.pending", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x14})
	// GridMark lights the text a span marks.
	GridMark = theme.Color("grid.mark", color.NRGBA{R: 0xe8, G: 0xb3, B: 0x4a, A: 0x60})
	// GridChipRadius rounds a span drawn on a fill.
	GridChipRadius = theme.Length("grid.chip.radius", 4)
	// GridIconGap is the space between a span's icon and its text.
	GridIconGap = theme.Length("grid.icon.gap", 4)
	// GridBarWidth is the width of the scrollbar's track.
	GridBarWidth = theme.Length("grid.bar.width", 10)
)

// GridColumn is one column of a [DataGrid].
type GridColumn struct {
	Title string
	// Width is the column's width in logical pixels. Zero takes what the
	// other columns leave, and at least GridMinFill.
	Width float32
	// End lines the column's text up at its right edge, as numbers are.
	End bool
	// Face is the face the column is set in, and the theme's [Font] when
	// unset.
	Face theme.Token[*text.Face]
	// Closable shows a cross on the title under the pointer, which sends
	// the grid's OnClose.
	Closable bool
	// Sort shows an arrow by the title: 1 for rows sorted up by the column,
	// -1 for down, and 0 for none.
	Sort int
}

// GridMinFill is the narrowest a column with no width gets.
const GridMinFill = 120

// GridSpan is a piece of a cell's text in a colour and face of its own.
type GridSpan struct {
	Text string
	// Icon, when set, draws before the text in the span's ink, as tall as the text; a span with an icon and no
	// text shows the icon alone.
	Icon *icon.Icon
	// Image, when set and Icon is not, draws a picture before the text in
	// its own colours, a little taller than the text, as a file's icon
	// in a list.
	Image *paint.Image
	// Ink is the text's colour, and the theme's [Ink] when unset.
	Ink theme.Token[color.NRGBA]
	// Fill, when set, draws the span on a rounded chip of that colour.
	Fill theme.Token[color.NRGBA]
	// Face is the span's face, and the column's when unset.
	Face theme.Token[*text.Face]
	// Faint draws the span at half strength.
	Faint bool
	// Marks are runs of the text to highlight, such as what a search found,
	// each a start and an end in runes, the end left out.
	Marks [][2]int
	// OnClick makes the span a link: it runs on the UI goroutine when the
	// span is clicked, and OnCtrlClick, when set, in its place for a click
	// with Ctrl held. A non-nil result is sent as the grid's intent.
	OnClick, OnCtrlClick func(u *gunim.UI) gunim.Intent
}

// GridRow is what a [DataGrid] shows for one row: the spans of each
// column's cell, in order.
type GridRow struct {
	Cells [][]GridSpan
	// Tint, when set, colours the row's background.
	Tint theme.Token[color.NRGBA]
	// Rule draws a line along the row's top.
	Rule bool
	// Dim draws the whole row at half strength, as for a row about to be
	// replaced.
	Dim bool
}

// Text returns the row as plain text: its cells' spans joined, with a
// tab between cells.
func (r GridRow) Text() string {
	var b strings.Builder
	for i, cell := range r.Cells {
		if i > 0 {
			b.WriteByte('\t')
		}
		for _, s := range cell {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// DataGrid shows rows of styled cells by index, drawing only the rows in
// view, so a grid of millions of rows costs what the rows on screen do.
//
// The grid asks Row for each row it draws. A row that has not arrived
// yet draws as a placeholder, and OnView tells the application which
// rows are in view, so it can send them. The grid scrolls on a spring,
// sideways too when the columns are wider than it, and its scrollbar can
// be dragged. Columns resize from the edges of their titles.
//
// One row can be selected: by a click, which on the selected row clears
// it, and by the arrow keys, Page Up and Page Down, Home and End. Escape
// clears it, and Ctrl+C copies it. With Multi, several rows can be
// selected at once. A press with the secondary button selects the row
// under it, unless it is selected already, and leaves the press to a
// context menu around the grid.
type DataGrid struct {
	Columns []GridColumn
	// Row returns row i, and false while it has not arrived.
	Row func(i int) (GridRow, bool)
	// The On callbacks run on the UI goroutine and may act in the window
	// through u; a non-nil result is sent to the application as the
	// grid's intent.
	//
	// OnView turns the rows in view, first to first+count, into an intent.
	// It runs from the grid's layout, with the window's UI.
	OnView func(first, count int, u *gunim.UI) gunim.Intent
	// OnSelect turns a change of selection into an intent; row is -1
	// when nothing is selected. With Multi, row is the row the keyboard
	// is on.
	OnSelect func(row int, u *gunim.UI) gunim.Intent
	// Multi lets several rows be selected: Ctrl with a click adds a row or
	// takes it away, Shift with a click or a key selects the rows from the
	// last one picked, and Ctrl+A selects every row. A click below the
	// rows clears the selection.
	Multi bool
	// OnSelectRows turns a change of the rows selected, with Multi, into
	// an intent. sel holds runs of rows, each a first row and an end row
	// left out, in order, and cursor is the row the keyboard is on, or -1.
	OnSelectRows func(sel [][2]int, cursor int, u *gunim.UI) gunim.Intent
	// OnClick turns a click on a row into an intent, each click, whether or
	// not it changes the selection.
	OnClick func(row int, u *gunim.UI) gunim.Intent
	// OnActivate turns a double click or Enter on a row into an intent.
	OnActivate func(row int, u *gunim.UI) gunim.Intent
	// OnType, when set, turns the text typed at the grid into an intent, to find the row it names: text typed close
	// together adds up, as [TypeAhead] gathers it, and [FindTyped] finds it.
	OnType func(text string, u *gunim.UI) gunim.Intent
	typed  TypeAhead
	// OnResize turns a column resized by a drag into an intent.
	OnResize func(column int, width float32, u *gunim.UI) gunim.Intent
	// OnHeader turns a click on a column's title into an intent, as to sort by it.
	OnHeader func(column int, u *gunim.UI) gunim.Intent
	// OnClose turns a click on a closable column's cross into an intent.
	OnClose func(column int, u *gunim.UI) gunim.Intent
	// Copy returns the text Ctrl+C copies for row i, and the row's
	// [GridRow.Text] when nil. Without Copy, Ctrl+C looks at the first
	// [MostCopiedRows] rows selected and takes those that have arrived,
	// a line each, and leaves the clipboard as it was when none has.
	Copy func(i int) string
	// OnCopy, when set, turns Ctrl+C on the selected rows into an intent
	// in place of copying their text, as for a list of files, or for a
	// grid whose rows arrive as they come into view, which copies the
	// selection whole from where its rows come from.
	OnCopy func(sel [][2]int, u *gunim.UI) gunim.Intent
	// OnCopied, when set, turns a copy Ctrl+C made itself into an intent
	// saying how many rows it copied of how many were selected, so an
	// application can say when rows were left out: those still to
	// arrive, or past MostCopiedRows.
	OnCopied func(copied, selected int, u *gunim.UI) gunim.Intent
	// NoHeader hides the column titles, and NoBar the scrollbar, for a
	// grid that shows its position some other way.
	NoHeader bool
	NoBar    bool
	// Glide has a selection of one row, or of rows next to each other,
	// slide and stretch to where a key, a click or [DataGrid.SlideSelectedRows]
	// puts it, in place of jumping there. An edge that would cross more
	// than the view jumps.
	Glide bool
	// DragRows, when set, lets the rows selected be dragged: a press on
	// one and a move of a few pixels drags the data it returns, with
	// ghost under the pointer, held grab from its top left. at is where
	// the press was, in the grid's space. A nil data drags nothing.
	DragRows func(sel [][2]int, at geom.Point) (data any, ghost gunim.Node, grab geom.Point)
	// OnDragEnd, when set, hears how a drag of the rows ended.
	OnDragEnd func(e input.DragEnd, u *gunim.UI) gunim.Intent

	// lift is a press that may become a drag, and away how far the rows
	// dragged have dimmed.
	lift gridLift
	away *anim.Float

	rows     int
	selected int
	focused  bool
	// glideAt is where the top and bottom edges of a selection sliding
	// to the rows glideTo are, in rows, and glideVel their speeds in rows
	// a second; glideOn is set while it slides.
	glideAt, glideVel [2]float64
	glideTo           [2]int
	glideOn           bool
	cue               ringCue
	// runs are the rows selected with Multi, and anchor the row a
	// selection with Shift runs from.
	runs   [][2]int
	anchor int

	// top is the row at the top of the view, with a fraction for a row
	// partly scrolled off, goal where the spring carries it, and vel its
	// speed in rows a second.
	top, goal, vel float64
	// left is how far the columns are scrolled sideways.
	left float32

	// From the last layout: the view's size, the rows it holds, and each
	// column's left edge and width, before scrolling sideways.
	view      geom.Size
	header    float32
	rowH      float32
	xs        [][2]float32
	contentW  float32
	sentFirst int
	sentCount int

	// drag is what the pointer holds, and grab where it took hold.
	drag     gridDrag
	dragCol  int
	grab     float32
	grabFrom float64
	hoverCol int

	pending bool
	pulse   float64

	// arrival is how long ago Arrive was called, in seconds, and
	// arriveTop the row at the top of the view then; arriving is set
	// until the last row in view has come in.
	arrival   float64
	arriveTop int
	arriving  bool
	// gone holds the rows Leave took away, by their index before, in
	// order, leaving those of them in view, and left how long ago Leave
	// was called, in seconds.
	gone    []int
	leaving []gridLeaving
	leftAgo float64

	// th is the window's live theme, kept from Layout for Step.
	th *theme.Live

	// links are where the last paint drew spans that are links.
	links []gridLink
	// cuts are the cells the last paint cut short, and tip shows the one under the pointer whole.
	cuts []gridCut
	tip  PartTip

	// partRow and partCol are the row and the column of each part the
	// grid last told a screen reader of, -1 where it has none.
	partRow, partCol []int

	shapes map[spanKey]*spanShape
	frame  uint64
	cut    map[cutKey]text.Run
}

type gridDrag uint8

const (
	dragNone gridDrag = iota
	dragBar
	dragEdge
)

// gridLift is a press on a row that may become a drag of the rows
// selected.
type gridLift struct {
	row   int
	at    geom.Point
	mods  input.Mods
	armed bool
	// later is set when the release, not the press, changes the
	// selection, as for a press on a row already selected.
	later    bool
	dragging bool
}

type spanKey struct {
	row       int
	col, span int32
}

type spanShape struct {
	face *text.Face
	s    string
	size float32
	run  text.Run
	seen uint64
}

type cutKey struct {
	face *text.Face
	size float32
}

// NewDataGrid returns an empty grid of columns.
func NewDataGrid(columns ...GridColumn) *DataGrid {
	return &DataGrid{
		Columns:   columns,
		selected:  -1,
		anchor:    -1,
		sentFirst: -1,
		hoverCol:  -1,
		shapes:    map[spanKey]*spanShape{},
		away:      anim.NewFloat(0),
		cut:       map[cutKey]text.Run{},
	}
}

// Rows returns how many rows the grid has.
func (g *DataGrid) Rows() int { return g.rows }

// SetRows sets how many rows the grid has. The view stays where it is,
// inside the new rows, and a selection past the end is cleared.
func (g *DataGrid) SetRows(n int, u *gunim.UI) {
	g.rows = max(n, 0)
	if g.selected >= g.rows {
		g.selected = -1
	}
	if g.anchor >= g.rows {
		g.anchor = -1
	}
	g.runs = clipRuns(g.runs, g.rows)
	g.goal = g.clampTop(g.goal)
	g.top = g.clampTop(g.top)
	g.sentFirst = -1
	if g.glideTo[1] > g.rows {
		g.glideOn = false
	}
	u.Invalidate()
}

// Selected returns the selected row, or -1 when there is none.
func (g *DataGrid) Selected() int { return g.selected }

// SetSelected selects row i, or clears the selection for -1, and sends no
// intent. The view stays where it is; [DataGrid.Reveal] brings the row
// into it.
func (g *DataGrid) SetSelected(i int, u *gunim.UI) {
	if i < 0 || i >= g.rows {
		i = -1
	}
	g.selected, g.anchor = i, i
	g.runs = nil
	if i >= 0 {
		g.runs = [][2]int{{i, i + 1}}
	}
	if !g.gliding() {
		g.glideOn = false
	}
	if u != nil {
		u.Invalidate()
	}
}

// Reveal scrolls the view just far enough to show row i whole. Once the
// grid is laid out the view glides there; before that, or with a nil u,
// it jumps.
func (g *DataGrid) Reveal(i int, u *gunim.UI) {
	if i < 0 || i >= g.rows {
		return
	}
	g.reveal(i)
	if u == nil || g.rowH <= 0 {
		g.top, g.vel = g.goal, 0
		return
	}
	u.Invalidate()
}

// SelectedRows returns the rows selected, as runs of a first row and an
// end row left out, in order.
func (g *DataGrid) SelectedRows() [][2]int {
	if !g.Multi {
		if g.selected < 0 {
			return nil
		}
		return [][2]int{{g.selected, g.selected + 1}}
	}
	return slices.Clone(g.runs)
}

// SetSelectedRows selects the rows in sel, runs of a first row and an end
// row left out, and puts the keyboard on row cursor, -1 for none, without
// telling OnSelect or OnSelectRows. It is for a grid with Multi.
func (g *DataGrid) SetSelectedRows(sel [][2]int, cursor int, u *gunim.UI) {
	var runs [][2]int
	for _, r := range sel {
		runs = addRun(runs, r[0], r[1])
	}
	g.runs = clipRuns(runs, g.rows)
	if cursor < 0 || cursor >= g.rows {
		cursor = -1
	}
	g.selected, g.anchor = cursor, cursor
	if !g.gliding() {
		g.glideOn = false
	}
	u.Invalidate()
}

// SlideSelectedRows selects the rows as [DataGrid.SetSelectedRows] does,
// and with Glide, has the selection slide there from where it was, as
// for a row the program found by the name the user typed.
func (g *DataGrid) SlideSelectedRows(sel [][2]int, cursor int, u *gunim.UI) {
	was, had := g.block()
	moving := g.gliding()
	g.SetSelectedRows(sel, cursor, u)
	g.glideOn = moving
	g.slideFrom(was, had)
}

// IsSelected reports whether row i is selected.
func (g *DataGrid) IsSelected(i int) bool {
	if !g.Multi {
		return i >= 0 && i == g.selected
	}
	return hasRun(g.runs, i)
}

// Top returns the row at the top of the view, with a fraction for a row
// partly scrolled off.
func (g *DataGrid) Top() float64 { return g.top }

// Visible returns how many rows the view holds.
func (g *DataGrid) Visible() float64 {
	if g.rowH <= 0 {
		return 0
	}
	return float64(g.view.H-g.header) / float64(g.rowH)
}

// ScrollTo carries the view on its spring until row is at the top.
func (g *DataGrid) ScrollTo(row float64, u *gunim.UI) {
	g.goal = g.clampTop(row)
	u.Invalidate()
}

// JumpTo puts row at the top of the view at once, or in the middle with
// center, for rows that are new rather than moved.
func (g *DataGrid) JumpTo(row float64, center bool, u *gunim.UI) {
	if center {
		row -= g.Visible() / 2
	}
	g.goal = g.clampTop(row)
	g.top, g.vel = g.goal, 0
	u.Invalidate()
}

func (g *DataGrid) clampTop(row float64) float64 {
	return max(0, min(row, float64(g.rows)-g.Visible()))
}

// reveal scrolls just far enough to show row i whole.
func (g *DataGrid) reveal(i int) {
	vis := g.Visible()
	switch {
	case float64(i) < g.goal:
		g.goal = g.clampTop(float64(i))
	case float64(i+1) > g.goal+vis:
		g.goal = g.clampTop(float64(i+1) - vis)
	}
}

func (g *DataGrid) selectAndTell(i int, u *gunim.UI) {
	if g.rows == 0 {
		return
	}
	if i >= 0 {
		i = min(max(i, 0), g.rows-1)
	}
	if i == g.selected {
		if i >= 0 {
			g.reveal(i)
		}
		return
	}
	g.selected = i
	if i >= 0 {
		g.reveal(i)
	}
	if g.OnSelect != nil {
		g.send(g.OnSelect(i, u), u)
	}
	u.Invalidate()
}

// Focusable implements [gunim.Focusable].
func (g *DataGrid) Focusable() bool { return true }

// Step implements [gunim.Animator].
func (g *DataGrid) Step(dt time.Duration) bool {
	moving := false
	if g.top != g.goal || g.vel != 0 {
		g.top, g.vel = Quick.Get(g.th).Follow(g.top, g.vel, g.goal, dt)
		// Still once it is a hundredth of a pixel away and all but stopped.
		px := float64(max(g.rowH, 1))
		if math.Abs(g.top-g.goal)*px < 0.01 && math.Abs(g.vel)*px < 1 {
			g.top, g.vel = g.goal, 0
		}
		moving = true
	}
	// Rows still to come pulse while the grid is painted: one out of
	// sight lets the window rest. Paint marks them again, and the
	// engine's step of no time, after a frame is painted, leaves the mark
	// for the next frame's.
	if g.pending {
		g.pulse += dt.Seconds()
		moving = true
		if dt > 0 {
			g.pending = false
		}
	}
	if len(g.gone) > 0 {
		g.leftAgo += dt.Seconds()
		if g.leftAgo > leaveTime {
			g.gone, g.leaving = nil, nil
		}
		moving = true
	}
	if g.away != nil && g.away.Step(dt) {
		moving = true
	}
	if g.glideOn {
		px := float64(max(g.rowH, 1))
		still := true
		for k := range 2 {
			to := float64(g.glideTo[k])
			g.glideAt[k], g.glideVel[k] = Quick.Get(g.th).Follow(g.glideAt[k], g.glideVel[k], to, dt)
			if math.Abs(g.glideAt[k]-to)*px < 0.01 && math.Abs(g.glideVel[k])*px < 1 {
				g.glideAt[k], g.glideVel[k] = to, 0
			} else {
				still = false
			}
		}
		g.glideOn = !still
		moving = true
	}
	if g.arriving {
		g.arrival += dt.Seconds()
		if g.arrival > arriveStagger*(g.Visible()+1)+arriveTime {
			g.arriving = false
		}
		moving = true
	}
	return moving
}

// Arrive and its timing: each row takes arriveTime to come in, the next
// row starting arriveStagger after the one above it.
const (
	arriveTime    = 0.26
	arriveStagger = 0.018
)

// Arrive has the rows in view come in one after another from the top,
// each fading up and sliding down into place, as for rows that replaced
// the ones before.
func (g *DataGrid) Arrive(u *gunim.UI) {
	g.arrival, g.arriveTop, g.arriving = 0, int(math.Floor(g.top)), true
	u.Invalidate()
}

// leaveTime is how long a row takes to leave.
const leaveTime = 0.3

// gridLeaving is a row in view on its way out: its index before it left,
// what it showed, and whether it was selected.
type gridLeaving struct {
	at       int
	row      GridRow
	selected bool
}

// Leave has the rows that were at the indexes in gone collapse and fade,
// while the rows below them slide up into their place. Call it once the
// rows have changed, with Row still returning the rows as they were
// before, which the grid keeps for the rows in view.
func (g *DataGrid) Leave(gone []int, u *gunim.UI) {
	g.gone, g.leaving, g.leftAgo = nil, nil, 0
	if g.Row == nil || len(gone) == 0 {
		return
	}
	g.gone = slices.Clone(gone)
	slices.Sort(g.gone)
	g.gone = slices.Compact(g.gone)
	first, last := int(math.Floor(g.top)), int(math.Ceil(g.top+g.Visible()))+1
	from, _ := slices.BinarySearch(g.gone, first)
	for _, i := range g.gone[from:] {
		if i > last {
			break
		}
		if row, ok := g.Row(i); ok {
			g.leaving = append(g.leaving, gridLeaving{at: i, row: row, selected: g.IsSelected(i)})
		}
	}
	u.Invalidate()
}

// Leaving returns how many rows in view are on their way out.
func (g *DataGrid) Leaving() int { return len(g.leaving) }

// shut returns how far the rows leaving have shut, from 0 to 1.
func (g *DataGrid) shut() float32 {
	t := float32(min(g.leftAgo/leaveTime, 1))
	return 1 - (1-t)*(1-t)*(1-t)
}

// before returns the index row i of the rows now had before Leave, and
// how many rows in view that are leaving sat above it.
func (g *DataGrid) before(i int) (at, above int) {
	// gone[j]-j is how many rows that stay sat above the j-th row gone, which only grows: the rows gone above
	// row i are those it counts no more than i of.
	at = i + sort.Search(len(g.gone), func(j int) bool { return g.gone[j]-j > i })
	above, _ = slices.BinarySearchFunc(g.leaving, at, func(l gridLeaving, at int) int { return l.at - at })
	return at, above
}

// rowY returns where row i sits in the grid, below the rows still
// leaving above it.
func (g *DataGrid) rowY(i int) float32 {
	y := g.header + float32((float64(i)-g.top)*float64(g.rowH))
	if len(g.gone) == 0 {
		return y
	}
	_, above := g.before(i)
	return y + float32(above)*(1-g.shut())*g.rowH
}

// arrived returns how far row i has come in, from 0 to 1.
func (g *DataGrid) arrived(i int) float32 {
	if !g.arriving {
		return 1
	}
	t := (g.arrival - float64(max(i-g.arriveTop, 0))*arriveStagger) / arriveTime
	t = min(max(t, 0), 1)
	return float32(1 - (1-t)*(1-t)*(1-t))
}

// Layout implements [gunim.Node]. The grid fills the space it is given.
func (g *DataGrid) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	g.view = c.Max
	g.th = th
	g.rowH = GridRowHeight.Get(th)
	g.header = 0
	if !g.NoHeader {
		g.header = GridHeaderHeight.Get(th)
	}
	viewW := g.bodyWidth(th)
	fixed, fills := float32(0), 0
	for _, col := range g.Columns {
		if col.Width > 0 {
			fixed += col.Width
		} else {
			fills++
		}
	}
	fill := max(GridMinFill, (viewW-fixed)/float32(max(fills, 1)))
	g.xs = g.xs[:0]
	x := float32(0)
	for _, col := range g.Columns {
		w := col.Width
		if w <= 0 {
			w = fill
		}
		g.xs = append(g.xs, [2]float32{x, w})
		x += w
	}
	g.contentW = x
	g.left = max(0, min(g.left, g.contentW-viewW))
	g.goal = g.clampTop(g.goal)
	if !g.dragging() {
		g.top = g.clampTop(g.top)
	}

	if g.OnView != nil && g.rowH > 0 {
		first := int(math.Floor(g.top))
		count := int(math.Ceil(g.Visible())) + 1
		if first != g.sentFirst || count != g.sentCount {
			g.sentFirst, g.sentCount = first, count
			if v := g.OnView(first, count, f.UI()); v != nil {
				f.Send(g, v)
			}
		}
	}
	return c.Max
}

func (g *DataGrid) dragging() bool { return g.drag != dragNone }

// bodyWidth is the width the columns show in, less the scrollbar.
func (g *DataGrid) bodyWidth(th *theme.Live) float32 {
	if g.NoBar {
		return g.view.W
	}
	return max(0, g.view.W-GridBarWidth.Get(th))
}

// Paint implements [gunim.Node].
func (g *DataGrid) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	// Drawn last, over the rest
	defer func() {
		if g.cue.whole {
			GroupRing(p, geom.Rect{Max: box.Point()}, 0, 1, th)
		}
	}()
	g.frame++
	g.pending = false
	g.links = g.links[:0]
	g.cuts = g.cuts[:0]
	bodyW := g.bodyWidth(th)
	size := GridTextSize.Get(th)
	pad := GridCellPadding.Get(th)

	if g.rowH > 0 && g.Row != nil {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, g.header, bodyW, box.H-g.header), Opacity: 1, Clip: true})()
			first := int(math.Floor(g.top))
			last := min(g.rows, int(math.Ceil(g.top+g.Visible()))+1)
			g.paintGlide(p, th, bodyW)
			if len(g.gone) > 0 {
				g.paintLeaving(p, th, first, last, bodyW, size, pad)
				return
			}
			for i := first; i < last; i++ {
				y := g.header + float32((float64(i)-g.top)*float64(g.rowH))
				g.paintRow(p, th, i, y, bodyW, size, pad)
			}
		}()
	}
	if !g.NoHeader {
		g.paintHeader(p, th, bodyW, size, pad)
	}
	if !g.NoBar {
		g.paintBar(p, th)
	}
	// Forget the shapes of rows that left the view, once there are many.
	if len(g.shapes) > 4096 {
		for k, s := range g.shapes {
			if s.seen != g.frame {
				delete(g.shapes, k)
			}
		}
	}
}

// paintLeaving paints the rows first to last while rows leave: each row
// still below where the rows leaving were, and those rows shutting.
func (g *DataGrid) paintLeaving(p *paint.Painter, th *theme.Live, first, last int, bodyW, size, pad float32) {
	open := 1 - g.shut()
	top := g.header + float32(-g.top*float64(g.rowH))
	for i := max(0, first-len(g.leaving)-1); i < last; i++ {
		y := g.rowY(i)
		if y > g.view.H || y+g.rowH < g.header {
			continue
		}
		g.paintRow(p, th, i, y, bodyW, size, pad)
	}
	if open <= 0 {
		return
	}
	for k, l := range g.leaving {
		n, _ := slices.BinarySearch(g.gone, l.at)
		y := top + (float32(l.at-n)+float32(k)*open)*g.rowH
		band := geom.Rc(0, y, bodyW, g.rowH*open)
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: band, Opacity: open * open, Clip: true})()
			defer p.Push(paint.Translate(geom.Pt(0, -g.rowH*(1-open)/2)))()
			g.paintContent(p, th, -2-k, l.row, l.selected, false, y, bodyW, size, pad)
		}()
	}
}

func (g *DataGrid) paintRow(p *paint.Painter, th *theme.Live, i int, y, bodyW, size, pad float32) {
	row, ok := g.Row(i)
	band := geom.Rc(0, y, bodyW, g.rowH)
	if !ok {
		g.pending = true
		c := GridPending.Get(th)
		c.A = uint8(float32(c.A) * float32(0.65+0.35*math.Sin(g.pulse*4)))
		for _, x := range g.xs {
			left := x[0] - g.left + pad
			if left > bodyW || left+x[1] < 0 {
				continue
			}
			p.RRect(geom.Rc(left, y+g.rowH*0.3, max(0, x[1]*0.6-pad), g.rowH*0.4), g.rowH*0.2, paint.Solid(c))
		}
		return
	}
	if g.away != nil && g.IsSelected(i) {
		if a := g.away.Value(); a > 0.001 {
			defer p.Layer(paint.LayerOpts{Bounds: band, Opacity: 1 - 0.55*min(a, 1)})()
		}
	}
	if in := g.arrived(i); in < 1 {
		if in <= 0 {
			return
		}
		defer p.Layer(paint.LayerOpts{Bounds: band, Opacity: in})()
		defer p.Push(paint.Translate(geom.Pt(0, -6*(1-in))))()
	}
	cursor := g.Multi && g.cue.on && i == g.selected && countRuns(g.runs) > 1
	// A selection sliding is painted on its way, under the rows.
	selected := g.IsSelected(i) && !g.gliding()
	g.paintContent(p, th, i, row, selected, cursor, y, bodyW, size, pad)
}

// paintContent paints row at y, under key in the shape cache: its tint,
// its selection and cursor, and its cells.
func (g *DataGrid) paintContent(p *paint.Painter, th *theme.Live, key int, row GridRow, selected, cursor bool,
	y, bodyW, size, pad float32) {
	band := geom.Rc(0, y, bodyW, g.rowH)
	if row.Tint.Key() != "" {
		p.RRect(band, 0, paint.Solid(row.Tint.Get(th)))
	}
	if selected {
		c := GridCursor.Get(th)
		if !g.focused {
			c.A = c.A * 2 / 3
		}
		p.RRect(band, 0, paint.Solid(c))
	}
	if cursor {
		p.RRectStroke(band.Inset(geom.Uniform(0.5)), 0, paint.Fill{}, paint.Stroke{Width: 1, Color: GridRule.Get(th)})
	}
	if row.Rule {
		p.RRect(geom.Rc(0, y, bodyW, 1), 0, paint.Solid(GridRule.Get(th)))
	}
	for c, spans := range row.Cells {
		if c >= len(g.xs) {
			break
		}
		x, w := g.xs[c][0]-g.left, g.xs[c][1]
		if x > bodyW || x+w < 0 {
			continue
		}
		g.paintCell(p, th, key, c, spans, row.Dim, x, y, w, size, pad)
	}
}

// paintCell sets a cell's spans one after another, cutting the last that
// fits short with an ellipsis.
func (g *DataGrid) paintCell(p *paint.Painter, th *theme.Live, i, c int, spans []GridSpan, dim bool,
	x, y, w, size, pad float32) {
	cutShort := func() { g.cuts = append(g.cuts, gridCut{r: geom.Rc(x, y, w, g.rowH), row: i, col: c}) }
	colFace := faceIn(g.Columns[c].Face, th)
	chipPad := float32(4)
	runs := make([]text.Run, len(spans))
	total := float32(0)
	for k, s := range spans {
		face := colFace
		if s.Face.Key() != "" {
			face = faceIn(s.Face, th)
		}
		runs[k] = g.shape(spanKey{row: i, col: int32(c), span: int32(k)}, face, s.Text, size)
		total += runs[k].Advance + spanIconWidth(s, size, th)
		if s.Fill.Key() != "" {
			total += 2 * chipPad
		}
	}
	pen := x + pad
	limit := x + w - pad
	if g.Columns[c].End && total < limit-pen {
		pen = limit - total
	}
	for k, s := range spans {
		run := runs[k]
		ink := Ink.Get(th)
		if s.Ink.Key() != "" {
			ink = s.Ink.Get(th)
		}
		if s.Faint || dim {
			ink.A /= 2
		}
		chip := s.Fill.Key() != ""
		room := limit - pen
		if chip {
			room -= 2 * chipPad
		}
		iconW := spanIconWidth(s, size, th)
		if iconW > room {
			cutShort()
			return
		}
		room -= iconW
		cut := run.Advance > room
		// A gap that does not fit ends the cell rather than showing a lone ellipsis
		if cut && strings.TrimSpace(s.Text) == "" {
			cutShort()
			return
		}
		if cut {
			run = g.cutShort(run, room)
			cutShort()
		}
		ty := y + (g.rowH-run.Height())/2
		if chip {
			fill := s.Fill.Get(th)
			if s.Faint || dim {
				fill.A /= 2
			}
			p.RRect(geom.Rc(pen, y+3, iconW+run.Advance+2*chipPad, g.rowH-6), GridChipRadius.Get(th), paint.Solid(fill))
			pen += chipPad
		}
		start := pen
		switch {
		case s.Icon != nil:
			paintIcon(p, th, s.Icon, geom.Rc(pen, y+(g.rowH-size)/2, size, size), ink, 1)
			pen += iconW
		case s.Image != nil:
			side := min(spanImageSide(size), g.rowH-2)
			opacity := float32(1)
			if s.Faint || dim {
				opacity = 0.5
			}
			p.Image(s.Image, geom.Rc(pen, y+(g.rowH-side)/2, side, side), paint.ImageOpts{Opacity: opacity})
			pen += iconW
		}
		for _, m := range s.Marks {
			x0, x1 := min(run.CaretX(m[0]), run.Advance), min(run.CaretX(m[1]), run.Advance)
			if x1 > x0 {
				p.RRect(geom.Rc(pen+x0, ty, x1-x0, run.Height()), 2, paint.Solid(GridMark.Get(th)))
			}
		}
		run.Paint(p, geom.Pt(pen, ty), ink)
		if s.OnClick != nil {
			g.links = append(g.links, gridLink{r: geom.Rc(start, y, pen-start+run.Advance, g.rowH), on: s.OnClick, ctrl: s.OnCtrlClick})
			if run.Advance > 0 {
				p.RRect(geom.Rc(pen, ty+run.Ascent+1.5, run.Advance, 1), 0, paint.Solid(ink))
			}
		}
		pen += run.Advance
		if chip {
			pen += chipPad
		}
		if cut || pen >= limit {
			if !cut && k < len(spans)-1 {
				cutShort()
			}
			return
		}
	}
}

// gridCut is a cell, row and col, that a paint cut short at r.
type gridCut struct {
	r        geom.Rect
	row, col int
}

// tipAt is the whole text of the cell cut short at p, or nothing.
func (g *DataGrid) tipAt(p geom.Point) string {
	if g.Row == nil || p.Y < g.header {
		return ""
	}
	for _, c := range g.cuts {
		if !c.r.Contains(p) {
			continue
		}
		row, ok := g.Row(c.row)
		if !ok || c.col >= len(row.Cells) {
			return ""
		}
		var b strings.Builder
		for _, s := range row.Cells[c.col] {
			b.WriteString(s.Text)
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// spanIconWidth is the room span s's icon takes at text size, with the gap before any text.
func spanIconWidth(s GridSpan, size float32, th *theme.Live) float32 {
	var w float32
	switch {
	case s.Icon != nil:
		w = size
	case s.Image != nil:
		w = spanImageSide(size)
	default:
		return 0
	}
	if s.Text == "" {
		return w
	}
	return w + GridIconGap.Get(th)
}

// spanImageSide is how wide and tall a span's picture is at text size.
func spanImageSide(size float32) float32 { return size * 1.35 }

// shape returns s shaped in face at size, from the grid's cache when it
// was shaped for the same span before.
func (g *DataGrid) shape(k spanKey, face *text.Face, s string, size float32) text.Run {
	sh, ok := g.shapes[k]
	if !ok {
		sh = &spanShape{}
		g.shapes[k] = sh
	}
	if sh.face != face || sh.s != s || sh.size != size || !ok {
		sh.face, sh.s, sh.size, sh.run = face, s, size, face.Shape(s, size)
	}
	sh.seen = g.frame
	return sh.run
}

// cutShort returns run cut to the glyphs that fit in room with an ellipsis
// after them.
func (g *DataGrid) cutShort(run text.Run, room float32) text.Run {
	key := cutKey{run.Face, run.Size}
	ell, ok := g.cut[key]
	if !ok {
		ell = run.Face.Shape("…", run.Size)
		g.cut[key] = ell
	}
	return cutRunWith(run, ell, room)
}

func (g *DataGrid) paintHeader(p *paint.Painter, th *theme.Live, bodyW, size, pad float32) {
	h := g.header
	p.RRect(geom.Rc(0, 0, g.view.W, h), 0, paint.Solid(DialogFill.Get(th)))
	ink := TableHeader.Get(th)
	face := faceIn(Font, th)
	// The titles keep to the grid's body, as the rows do, scrolled sideways or past its right edge.
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, bodyW, h), Opacity: 1, Clip: true})
	for c, col := range g.Columns {
		if c >= len(g.xs) {
			break
		}
		x, w := g.xs[c][0]-g.left, g.xs[c][1]
		if x > bodyW || x+w < 0 {
			continue
		}
		run := g.shape(spanKey{row: -1, col: int32(c)}, face, col.Title, size)
		// right is where the title may reach: the column's edge, less the cross and the arrow after the title.
		right := x + w - pad
		closing := col.Closable && c == g.hoverCol
		if closing {
			right -= h / 2
		}
		if col.Sort != 0 {
			right -= sortArrow + 2
		}
		if room := right - x - pad; run.Advance > room {
			run = g.cutShort(run, room)
		}
		at := x + pad
		if col.End {
			at = right - run.Advance
		}
		run.Paint(p, geom.Pt(at, (h-run.Height())/2), ink)
		if col.Sort != 0 {
			paintSort(p, th, geom.Pt(at+run.Advance+sortArrow/2+2, h/2), col.Sort > 0, ink)
		}
		if closing {
			paintCross(p, th, geom.Pt(x+w-pad-h/4, h/2), h/4, ink)
		}
		if c < len(g.Columns)-1 {
			p.RRect(geom.Rc(x+w-1, h*0.25, 1, h*0.5), 0, paint.Solid(MenuBorder.Get(th)))
		}
	}
	end()
	p.RRect(geom.Rc(0, h-1, g.view.W, 1), 0, paint.Solid(MenuBorder.Get(th)))
}

// barRect returns the scrollbar's track, and its thumb within it.
func (g *DataGrid) barRect(th *theme.Live) (track, thumb geom.Rect, ok bool) {
	vis := g.Visible()
	if g.NoBar || g.rows == 0 || float64(g.rows) <= vis {
		return geom.Rect{}, geom.Rect{}, false
	}
	bw := GridBarWidth.Get(th)
	track = geom.Rc(g.view.W-bw, g.header, bw, g.view.H-g.header)
	length := max(24, track.Size().H*float32(vis/float64(g.rows)))
	span := track.Size().H - length
	at := float32(g.top / (float64(g.rows) - vis))
	thumb = geom.Rc(track.Min.X+2, track.Min.Y+span*max(0, min(at, 1)), bw-4, length)
	return track, thumb, true
}

func (g *DataGrid) paintBar(p *paint.Painter, th *theme.Live) {
	_, thumb, ok := g.barRect(th)
	if !ok {
		return
	}
	col := ScrollbarColor.Get(th)
	if g.drag == dragBar {
		col.A = uint8(min(255, int(col.A)*2))
	}
	p.RRect(thumb, thumb.Size().W/2, paint.Solid(col))
}

// Cursor implements [gunim.CursorShaper]: resize arrows over a column's
// edge.
func (g *DataGrid) Cursor(pt geom.Point) input.Cursor {
	if g.drag == dragEdge || g.edgeAt(pt) >= 0 {
		return input.CursorResizeH
	}
	for _, l := range g.links {
		if l.r.Contains(pt) {
			return input.CursorHand
		}
	}
	return input.CursorArrow
}

// edgeAt returns the column whose right edge in the header is under pt,
// or -1.
func (g *DataGrid) edgeAt(pt geom.Point) int {
	if g.NoHeader || pt.Y < 0 || pt.Y >= g.header {
		return -1
	}
	for c, x := range g.xs {
		if g.Columns[c].Width <= 0 {
			continue
		}
		edge := x[0] + x[1] - g.left
		if pt.X >= edge-4 && pt.X <= edge+4 {
			return c
		}
	}
	return -1
}

// columnAt returns the column under x, or -1.
func (g *DataGrid) columnAt(x float32) int {
	for c, xs := range g.xs {
		if x >= xs[0]-g.left && x < xs[0]+xs[1]-g.left {
			return c
		}
	}
	return -1
}

func (g *DataGrid) rowAtY(y float32) int {
	if y < g.header || g.rowH <= 0 {
		return -1
	}
	i := int(math.Floor(g.top + float64((y-g.header)/g.rowH)))
	if len(g.gone) == 0 {
		if i < 0 || i >= g.rows {
			return -1
		}
		return i
	}
	// While rows leave, the rows below them are drawn lower, by what has
	// yet to shut: the row drawn at y is this one or one above it, and
	// may be the last row, drawn below where the rows end. Where y falls
	// in the room a leaving row still takes, no row is there.
	i = min(i, g.rows-1)
	if i < 0 {
		return -1
	}
	for i > 0 && g.rowY(i) > y {
		i--
	}
	if y < g.rowY(i) || y >= g.rowY(i)+g.rowH {
		return -1
	}
	return i
}

// RowAt returns the row at p, in the grid's space, or -1 over the
// titles or below the rows.
func (g *DataGrid) RowAt(p geom.Point) int {
	if g.th == nil {
		return -1
	}
	if p.X < 0 || p.X >= g.bodyWidth(g.th) {
		return -1
	}
	return g.rowAtY(p.Y)
}

// RowRect returns where row i shows, in the grid's space, as the last
// layout put it, and false when it is out of view.
func (g *DataGrid) RowRect(i int) (geom.Rect, bool) {
	if g.th == nil || i < 0 || i >= g.rows || g.rowH <= 0 {
		return geom.Rect{}, false
	}
	y := g.rowY(i)
	if y+g.rowH <= g.header || y >= g.view.H {
		return geom.Rect{}, false
	}
	return geom.Rc(0, y, g.bodyWidth(g.th), g.rowH), true
}

// EdgeScroll implements [gunim.EdgeScroller]: a drag held near the top
// or the bottom of the rows scrolls them.
func (g *DataGrid) EdgeScroll(p geom.Point, dt time.Duration, u *gunim.UI) geom.Point {
	if g.rowH <= 0 {
		return geom.Point{}
	}
	v := gunim.EdgeSpeed(p.Y, g.header, g.view.H)
	if v == 0 {
		return geom.Point{}
	}
	secs := float32(min(dt, 50*time.Millisecond).Seconds())
	from := g.top
	to := g.clampTop(from + float64(v*secs/g.rowH))
	if to == from {
		return geom.Point{}
	}
	g.top, g.goal, g.vel = to, to, 0
	u.Invalidate()
	return geom.Pt(0, float32(from-to)*g.rowH)
}

// startDrag drags the rows selected, for a press that moved far enough.
func (g *DataGrid) startDrag(u *gunim.UI) {
	data, ghost, grab := g.DragRows(g.SelectedRows(), g.lift.at)
	if data == nil {
		g.lift = gridLift{}
		return
	}
	g.lift.dragging, g.lift.later = true, false
	u.StartDrag(g, data, ghost, grab)
	g.away.Animate(1, Quick.Get(u.Theme()))
}

func (g *DataGrid) send(v gunim.Intent, u *gunim.UI) {
	if v != nil {
		u.Send(g, v)
	}
}

// Handle implements [gunim.Handler].
func (g *DataGrid) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	if g.typed.take(e, u, g, g.OnType) {
		return true
	}
	switch e.(type) {
	case input.PointerDown, input.Scroll, input.PointerLeave:
		g.tip.Handle(e, u, g, "")
	}
	switch e := e.(type) {
	case input.FocusGained:
		g.focused = true
		u.Invalidate()
		return true
	case input.FocusRing:
		g.cue.follow(e)
		u.Invalidate()
		return true
	case input.FocusLost:
		g.focused = false
		u.Invalidate()
		return true
	case input.Scroll:
		dx, dy := e.Delta.X, e.Delta.Y
		if e.Mods.Has(input.ModShift) && dx == 0 {
			dx, dy = dy, 0
		}
		if dy != 0 && g.rowH > 0 {
			g.goal = g.clampTop(g.goal - float64(dy/g.rowH))
		}
		if dx != 0 {
			g.left = max(0, min(g.left-dx, g.contentW-g.bodyWidth(th)))
			// The titles moved under the pointer, which stayed.
			g.hoverAt(e.Pos, u)
		}
		u.Invalidate()
		return true
	case input.PointerDown:
		return g.press(e, u)
	case input.PointerMove:
		return g.move(e, u)
	case input.PointerUp:
		if g.lift.armed {
			g.release(u)
			return true
		}
		if g.drag == dragNone {
			return false
		}
		if g.drag == dragEdge && g.OnResize != nil {
			g.send(g.OnResize(g.dragCol, g.Columns[g.dragCol].Width, u), u)
		}
		g.drag = dragNone
		u.Invalidate()
		return true
	case input.PointerLeave:
		if g.hoverCol >= 0 {
			g.hoverCol = -1
			u.Invalidate()
		}
	case input.DragEnd:
		g.lift = gridLift{}
		g.away.Animate(0, Settle.Get(th))
		if g.OnDragEnd != nil {
			g.send(g.OnDragEnd(e, u), u)
		}
		return true
	case input.KeyPress:
		return g.key(e, u)
	}
	return false
}

func (g *DataGrid) press(e input.PointerDown, u *gunim.UI) bool {
	if e.Button == input.ButtonSecondary {
		// A context menu around the grid acts on the row pressed.
		if i := g.rowAtY(e.Pos.Y); i >= 0 && !g.IsSelected(i) && (g.NoHeader || e.Pos.Y >= g.header) {
			if g.Multi {
				g.pick(i, 0, u)
			} else {
				g.selectAndTell(i, u)
			}
		}
		return false
	}
	if e.Button != input.ButtonPrimary {
		return false
	}
	th := u.Theme()
	if c := g.edgeAt(e.Pos); c >= 0 {
		g.drag, g.dragCol, g.grab = dragEdge, c, e.Pos.X-g.Columns[c].Width
		return true
	}
	if track, thumb, ok := g.barRect(th); ok && track.Contains(e.Pos) {
		if !thumb.Contains(e.Pos) {
			// A press on the track brings the thumb's middle there.
			span := track.Size().H - thumb.Size().H
			at := (e.Pos.Y - track.Min.Y - thumb.Size().H/2) / max(span, 1)
			g.JumpTo(float64(at)*(float64(g.rows)-g.Visible()), false, u)
		}
		g.drag, g.grab, g.grabFrom = dragBar, e.Pos.Y, g.top
		u.Invalidate()
		return true
	}
	if !g.NoHeader && e.Pos.Y < g.header {
		c := g.columnAt(e.Pos.X)
		if c >= 0 && g.Columns[c].Closable && g.OnClose != nil {
			x := g.xs[c][0] + g.xs[c][1] - g.left
			if e.Pos.X >= x-GridCellPadding.Get(th)-g.header/2 {
				g.send(g.OnClose(c, u), u)
				return true
			}
		}
		if c >= 0 && g.OnHeader != nil {
			g.send(g.OnHeader(c, u), u)
		}
		return true
	}
	i := g.rowAtY(e.Pos.Y)
	if i < 0 {
		if g.Multi && e.Pos.Y >= g.header && !e.Mods.Has(input.ModControl) && !e.Mods.Has(input.ModShift) {
			g.selectAndTell(-1, u)
			g.setRuns(nil, u)
		}
		return true
	}
	for _, l := range g.links {
		if l.r.Contains(e.Pos) {
			if e.Mods.Has(input.ModControl) && l.ctrl != nil {
				g.send(l.ctrl(u), u)
			} else {
				g.send(l.on(u), u)
			}
			return true
		}
	}
	if g.OnClick != nil {
		g.send(g.OnClick(i, u), u)
	}
	if e.Clicks == 2 {
		if g.OnActivate != nil {
			act(u, g, gunim.CuePress, g.OnActivate, i)
		}
		return true
	}
	if g.DragRows != nil {
		// A press on a row selected may start a drag of the selection, so
		// the release changes the selection instead.
		g.lift = gridLift{row: i, at: e.Pos, mods: e.Mods, armed: true}
		if g.IsSelected(i) && !e.Mods.Has(input.ModShift) && (!e.Focusing || g.Multi) {
			g.lift.later = true
			return true
		}
	}
	g.choose(i, e.Mods, e.Focusing, u)
	return true
}

// choose changes the selection for a click on row i.
func (g *DataGrid) choose(i int, mods input.Mods, focusing bool, u *gunim.UI) {
	if g.Multi {
		g.pick(i, mods, u)
		return
	}
	was, had := g.block()
	defer g.slideFrom(was, had)
	if i == g.selected && !focusing {
		g.selectAndTell(-1, u)
	} else {
		g.selectAndTell(i, u)
	}
}

// release ends a press that may have become a drag, and changes the
// selection now when the press left it for later.
func (g *DataGrid) release(u *gunim.UI) {
	l := g.lift
	g.lift = gridLift{}
	if l.later && !l.dragging {
		g.choose(l.row, l.mods, false, u)
	}
}

func (g *DataGrid) move(e input.PointerMove, u *gunim.UI) bool {
	if g.lift.armed && !g.lift.dragging {
		if d := e.Pos.Sub(g.lift.at); d.X*d.X+d.Y*d.Y >= pickUp*pickUp && g.IsSelected(g.lift.row) {
			g.startDrag(u)
		}
		return true
	}
	switch g.drag {
	case dragEdge:
		g.Columns[g.dragCol].Width = max(24, e.Pos.X-g.grab)
		u.Invalidate()
		return true
	case dragBar:
		track, thumb, ok := g.barRect(u.Theme())
		if ok {
			span := float64(max(track.Size().H-thumb.Size().H, 1))
			rows := float64(g.rows) - g.Visible()
			g.goal = g.clampTop(g.grabFrom + float64(e.Pos.Y-g.grab)/span*rows)
			g.top, g.vel = g.goal, 0
		}
		u.Invalidate()
		return true
	case dragNone:
	}
	g.tip.Handle(e, u, g, g.tipAt(e.Pos))
	g.hoverAt(e.Pos, u)
	return false
}

// hoverAt lights the column title at pos, the pointer's place, if any.
func (g *DataGrid) hoverAt(pos geom.Point, u *gunim.UI) {
	hover := -1
	if !g.NoHeader && pos.Y < g.header {
		hover = g.columnAt(pos.X)
	}
	if hover != g.hoverCol {
		g.hoverCol = hover
		u.Invalidate()
	}
}

func (g *DataGrid) key(e input.KeyPress, u *gunim.UI) bool {
	if e.Mods.Has(input.ModControl) {
		if e.Mods.Has(input.ModShift) {
			// Ctrl with Shift is left to the keys around the grid
			return false
		}
		switch {
		case e.Key == input.KeyC && g.OnCopy != nil:
			if sel := g.SelectedRows(); len(sel) > 0 {
				g.send(g.OnCopy(sel, u), u)
				return true
			}
			return false
		case e.Key == input.KeyC && g.Multi && len(g.runs) > 0:
			lines, copied := g.copyRuns()
			if copied > 0 {
				u.SetClipboard(lines)
			}
			g.copied(copied, g.selectedCount(), u)
			return true
		case e.Key == input.KeyC && !g.Multi && g.selected >= 0:
			copied := 0
			if line, ok := g.copyText(g.selected); ok {
				u.SetClipboard(line)
				copied = 1
			}
			g.copied(copied, 1, u)
			return true
		case e.Key == input.KeyA && g.Multi && g.rows > 0:
			was, had := g.block()
			g.setRuns([][2]int{{0, g.rows}}, u)
			g.slideFrom(was, had)
			return true
		}
		return false
	}
	if e.Mods.Has(input.ModAlt) {
		return false
	}
	page := max(1, int(g.Visible())-1)
	at := g.selected
	if at < 0 {
		at = int(math.Ceil(g.top)) - 1
	}
	if g.Multi && g.moveKey(e, at, page, u) {
		u.Invalidate()
		return true
	}
	was, had := g.block()
	switch e.Key {
	case input.KeyUp:
		g.selectAndTell(max(at-1, 0), u)
		g.slideFrom(was, had)
	case input.KeyDown:
		g.selectAndTell(at+1, u)
		g.slideFrom(was, had)
	case input.KeyPageUp:
		g.selectAndTell(max(at-page, 0), u)
		g.slideFrom(was, had)
	case input.KeyPageDown:
		g.selectAndTell(at+page, u)
		g.slideFrom(was, had)
	case input.KeyHome:
		g.selectAndTell(0, u)
		g.slideFrom(was, had)
	case input.KeyEnd:
		g.selectAndTell(g.rows-1, u)
		g.slideFrom(was, had)
	case input.KeyLeft:
		g.left = max(0, g.left-ScrollLine.Get(u.Theme()))
	case input.KeyRight:
		g.left = max(0, min(g.left+ScrollLine.Get(u.Theme()), g.contentW-g.bodyWidth(u.Theme())))
	case input.KeyEnter, input.KeyKPEnter:
		if g.selected < 0 || g.OnActivate == nil {
			return false
		}
		act(u, g, gunim.CuePress, g.OnActivate, g.selected)
	case input.KeyEscape:
		if g.selected < 0 && len(g.runs) == 0 {
			return false
		}
		g.selectAndTell(-1, u)
		if g.Multi {
			g.setRuns(nil, u)
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// moveKey moves the keyboard to another row for a key that does so, with
// Multi: with Shift the rows from the anchor to it are selected, and
// without, it alone.
func (g *DataGrid) moveKey(e input.KeyPress, at, page int, u *gunim.UI) bool {
	var to int
	switch e.Key {
	case input.KeyUp:
		to = at - 1
	case input.KeyDown:
		to = at + 1
	case input.KeyPageUp:
		to = at - page
	case input.KeyPageDown:
		to = at + page
	case input.KeyHome:
		to = 0
	case input.KeyEnd:
		to = g.rows - 1
	default:
		return false
	}
	if g.rows == 0 {
		return true
	}
	to = min(max(to, 0), g.rows-1)
	was, had := g.block()
	defer g.slideFrom(was, had)
	g.selectAndTell(to, u)
	if e.Mods.Has(input.ModShift) {
		if g.anchor < 0 {
			g.anchor = max(at, 0)
		}
		g.setRuns([][2]int{{min(g.anchor, to), max(g.anchor, to) + 1}}, u)
	} else {
		g.anchor = to
		g.setRuns([][2]int{{to, to + 1}}, u)
	}
	return true
}

// block returns the rows selected, a first row and an end row left out,
// when they are one or more rows next to each other.
func (g *DataGrid) block() ([2]int, bool) {
	if !g.Multi {
		return [2]int{g.selected, g.selected + 1}, g.selected >= 0
	}
	if len(g.runs) != 1 {
		return [2]int{}, false
	}
	return g.runs[0], true
}

// slideFrom has the selection, which was the rows was when had, slide
// to the rows selected now, with Glide: from where it is on its way
// when it was still sliding there. A selection that is not one block,
// was none, or has an edge that would cross more than the view, does
// not slide.
func (g *DataGrid) slideFrom(was [2]int, had bool) {
	to, ok := g.block()
	at := [2]float64{float64(was[0]), float64(was[1])}
	if g.glideOn && had && was == g.glideTo {
		at = g.glideAt
	} else {
		g.glideVel = [2]float64{}
	}
	if !g.Glide || !ok || !had || g.rowH <= 0 ||
		math.Abs(float64(to[0])-at[0]) > g.Visible() || math.Abs(float64(to[1])-at[1]) > g.Visible() {
		g.glideOn = false
		return
	}
	if at == [2]float64{float64(to[0]), float64(to[1])} {
		g.glideOn = false
		return
	}
	g.glideAt, g.glideTo, g.glideOn = at, to, true
}

// Sliding reports whether the selection is still on its way to the rows
// selected, with Glide.
func (g *DataGrid) Sliding() bool { return g.gliding() }

// gliding reports whether the selection is sliding to the rows selected.
func (g *DataGrid) gliding() bool {
	if !g.glideOn {
		return false
	}
	to, ok := g.block()
	return ok && to == g.glideTo
}

// paintGlide paints the selection on its way to a row, under the rows.
func (g *DataGrid) paintGlide(p *paint.Painter, th *theme.Live, bodyW float32) {
	if !g.gliding() {
		return
	}
	top := g.rowY(g.glideTo[0]) + float32((g.glideAt[0]-float64(g.glideTo[0]))*float64(g.rowH))
	end := g.rowY(g.glideTo[1]-1) + g.rowH + float32((g.glideAt[1]-float64(g.glideTo[1]))*float64(g.rowH))
	c := GridCursor.Get(th)
	if !g.focused {
		c.A = c.A * 2 / 3
	}
	p.RRect(geom.Rc(0, top, bodyW, max(0, end-top)), 0, paint.Solid(c))
}

// pick changes the selection for a click on row i, with Multi.
func (g *DataGrid) pick(i int, mods input.Mods, u *gunim.UI) {
	was, had := g.block()
	defer g.slideFrom(was, had)
	g.selectAndTell(i, u)
	switch {
	case mods.Has(input.ModShift):
		if g.anchor < 0 {
			g.anchor = i
		}
		from, to := min(g.anchor, i), max(g.anchor, i)+1
		if mods.Has(input.ModControl) {
			g.setRuns(addRun(slices.Clone(g.runs), from, to), u)
		} else {
			g.setRuns([][2]int{{from, to}}, u)
		}
	case mods.Has(input.ModControl):
		g.anchor = i
		if hasRun(g.runs, i) {
			g.setRuns(removeRun(g.runs, i), u)
		} else {
			g.setRuns(addRun(slices.Clone(g.runs), i, i+1), u)
		}
	default:
		g.anchor = i
		g.setRuns([][2]int{{i, i + 1}}, u)
	}
	u.Invalidate()
}

// setRuns selects runs, and tells OnSelectRows when that changes the
// selection.
func (g *DataGrid) setRuns(runs [][2]int, u *gunim.UI) {
	if slices.Equal(runs, g.runs) {
		return
	}
	g.runs = runs
	if g.OnSelectRows != nil {
		g.send(g.OnSelectRows(slices.Clone(runs), g.selected, u), u)
	}
	u.Invalidate()
}

// MostCopiedRows is the most selected rows Ctrl+C looks at itself,
// which it does on the UI goroutine. A grid that copies more sets
// OnCopy.
const MostCopiedRows = 10000

// copyRuns returns the text Ctrl+C copies for the rows selected with
// Multi: a line for each of the first MostCopiedRows that has arrived,
// and how many that was.
func (g *DataGrid) copyRuns() (lines string, n int) {
	var b strings.Builder
	looked, copied := 0, 0
	for _, r := range g.runs {
		for i := r[0]; i < r[1] && looked < MostCopiedRows; i++ {
			looked++
			line, ok := g.copyText(i)
			if !ok {
				continue
			}
			if copied > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
			copied++
		}
	}
	return b.String(), copied
}

// copyText returns the text Ctrl+C copies for row i, and false for a
// row that has not arrived.
func (g *DataGrid) copyText(i int) (string, bool) {
	if g.Copy != nil {
		return g.Copy(i), true
	}
	if g.Row == nil {
		return "", false
	}
	row, ok := g.Row(i)
	return row.Text(), ok
}

// sortArrow is the room the arrow by a sorted column's title takes.
const sortArrow = 12

// paintSort draws the chevron by a sorted column's title centred on at, pointing up for rows sorted up.
func paintSort(p *paint.Painter, th *theme.Live, at geom.Point, up bool, c color.NRGBA) {
	ic := icon.ChevronDown
	if up {
		ic = icon.ChevronUp
	}
	s := IconSize.Get(th) * sortScale
	paintSmallIcon(p, th, ic, geom.Rc(at.X-s/2, at.Y-s/2, s, s), c)
}

// sortScale is the size of a sort chevron against [IconSize].
const sortScale = 0.7

// gridLink is where a span that is a link was drawn, and what it sends.
type gridLink struct {
	r        geom.Rect
	on, ctrl func(u *gunim.UI) gunim.Intent
}

// addRun adds the rows from a to b, b left out, to runs, which are in
// order and apart, and keeps them so.
func addRun(runs [][2]int, a, b int) [][2]int {
	if a >= b {
		return runs
	}
	out := make([][2]int, 0, len(runs)+1)
	placed := false
	for _, r := range runs {
		switch {
		case r[1] < a:
			out = append(out, r)
		case r[0] > b:
			if !placed {
				out = append(out, [2]int{a, b})
				placed = true
			}
			out = append(out, r)
		default:
			a, b = min(a, r[0]), max(b, r[1])
		}
	}
	if !placed {
		out = append(out, [2]int{a, b})
	}
	return out
}

// removeRun takes row i out of runs.
func removeRun(runs [][2]int, i int) [][2]int {
	out := make([][2]int, 0, len(runs)+1)
	for _, r := range runs {
		if i < r[0] || i >= r[1] {
			out = append(out, r)
			continue
		}
		if r[0] < i {
			out = append(out, [2]int{r[0], i})
		}
		if i+1 < r[1] {
			out = append(out, [2]int{i + 1, r[1]})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hasRun reports whether row i is in runs.
func hasRun(runs [][2]int, i int) bool {
	k, found := slices.BinarySearchFunc(runs, i, func(r [2]int, i int) int {
		switch {
		case r[1] <= i:
			return -1
		case r[0] > i:
			return 1
		}
		return 0
	})
	return found && k < len(runs)
}

// clipRuns cuts runs off at row n.
func clipRuns(runs [][2]int, n int) [][2]int {
	var out [][2]int
	for _, r := range runs {
		if r[0] >= n {
			break
		}
		out = append(out, [2]int{r[0], min(r[1], n)})
	}
	return out
}

// countRuns returns how many rows runs hold.
func countRuns(runs [][2]int) int {
	n := 0
	for _, r := range runs {
		n += r[1] - r[0]
	}
	return n
}

// copied tells the application, through OnCopied, what Ctrl+C copied.
func (g *DataGrid) copied(copied, selected int, u *gunim.UI) {
	if g.OnCopied != nil {
		g.send(g.OnCopied(copied, selected, u), u)
	}
}

// selectedCount is how many rows the runs select.
func (g *DataGrid) selectedCount() int {
	n := 0
	for _, r := range g.runs {
		n += r[1] - r[0]
	}
	return n
}
