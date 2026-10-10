package widget

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

type gridView struct{ First, Count int }

type gridSelected struct{ Row int }

type gridResized struct {
	Column int
	Width  float32
}

func TestADataGridAsksOnlyForTheRowsInView(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	asked := map[int]int{}
	g.Row = func(i int) (GridRow, bool) {
		asked[i]++
		return GridRow{Cells: [][]GridSpan{{{Text: "row"}}}}, true
	}
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	g.rows = 1_000_000
	run(2)
	for i := range asked {
		if i > 13 {
			t.Fatalf("row %d was asked for, with only rows 0 to 13 in view", i)
		}
	}
	if len(asked) < 13 {
		t.Fatalf("%d rows asked for, want the 13 in view", len(asked))
	}
	_ = w
}

func TestADataGridSaysWhichRowsAreInView(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{}, false }
	g.OnView = func(first, count int, u *gunim.UI) gunim.Intent { return gridView{first, count} }
	g.rows = 10_000
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	got := sent(w)
	if len(got) != 1 || got[0] != (gridView{0, 14}) {
		t.Fatalf("intents %v, want rows 0 to 14 in view", got)
	}
	w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, -22*100)})
	run(120)
	got = sent(w)
	if len(got) == 0 || got[len(got)-1] != (gridView{100, 14}) {
		t.Fatalf("after scrolling 100 rows, intents %v, want the last to be rows 100 to 114", got)
	}
}

func TestADataGridKeepsItsPlaceFarDown(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{}, true }
	g.rows = 2_000_000
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	_ = w
	// Far enough down that a float32 offset in pixels moves in steps of 4.
	g.goal, g.top = 1_999_000.25, 1_999_000.25
	run(1)
	w.Input(input.Scroll{Pos: geom.Pt(100, 100), Delta: geom.Pt(0, -11)})
	run(120)
	if want := 1_999_000.75; g.Top() != want {
		t.Fatalf("half a row down from 1999000.25, the top is %v, want %v", g.Top(), want)
	}
}

func TestArrowKeysMoveTheSelectionAndSaySo(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{}, true }
	g.OnSelect = func(row int, u *gunim.UI) gunim.Intent { return gridSelected{row} }
	g.rows = 100
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	click(w, 100, 26+22*3+5)
	run(1)
	if i := g.Selected(); i != 3 {
		t.Fatalf("a click on the fourth row selected %v, want row 3", i)
	}
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyEnd})
	run(60)
	if i := g.Selected(); i != 99 {
		t.Fatalf("after End, row %v is selected, want 99", i)
	}
	if top := g.Top(); top+g.Visible() < 100 {
		t.Fatalf("after End, the view starts at row %v and does not reach row 99", top)
	}
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	got := sent(w)
	want := []gunim.Intent{gridSelected{3}, gridSelected{4}, gridSelected{99}, gridSelected{-1}}
	if len(got) != len(want) {
		t.Fatalf("intents %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("intents %v, want %v", got, want)
		}
	}
}

func TestDraggingATitlesEdgeResizesItsColumn(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Time", Width: 100}, GridColumn{Title: "Message"})
	g.OnResize = func(c int, w float32, u *gunim.UI) gunim.Intent { return gridResized{c, w} }
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	now := time.Now()
	w.Input(input.PointerDown{Pos: geom.Pt(99, 10), Button: input.ButtonPrimary, Clicks: 1, Time: now})
	w.Input(input.PointerMove{Pos: geom.Pt(159, 10), Time: now})
	w.Input(input.PointerUp{Pos: geom.Pt(159, 10), Button: input.ButtonPrimary, Time: now})
	run(1)
	if g.Columns[0].Width != 160 {
		t.Fatalf("the column is %v wide, want 160", g.Columns[0].Width)
	}
	if x := g.xs[1][0]; x != 160 {
		t.Fatalf("the next column starts at %v, want 160", x)
	}
	if got := sent(w); len(got) != 1 || got[0] != (gridResized{0, 160}) {
		t.Fatalf("intents %v, want the column resized to 160", got)
	}
}

func TestDraggingTheScrollbarScrolls(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{}, true }
	g.rows = 1000
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	_, thumb, ok := g.barRect(nil)
	if !ok {
		t.Fatal("no scrollbar on a grid of 1000 rows")
	}
	now := time.Now()
	from := thumb.Center()
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1, Time: now})
	w.Input(input.PointerMove{Pos: geom.Pt(from.X, 400), Time: now})
	run(1)
	if want := 1000 - g.Visible(); g.Top() != want {
		t.Fatalf("with the thumb dragged to the bottom, the top is %v, want %v", g.Top(), want)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(from.X, 400), Button: input.ButtonPrimary, Time: now})
}

func TestALongCellEndsInAnEllipsis(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message", Width: 80})
	long := "a message far too long to fit in eighty pixels"
	run := g.cutShort(Font.Default().Shape(long, 12), 60)
	if run.Advance > 60 {
		t.Fatalf("cut to 60, the run is %v wide", run.Advance)
	}
	last := run.Glyphs[len(run.Glyphs)-1]
	ell := Font.Default().Shape("…", 12).Glyphs[0]
	if last.ID != ell.ID {
		t.Fatalf("the cut run ends in glyph %d, want the ellipsis %d", last.ID, ell.ID)
	}
}

func TestPendingRowsKeepTheGridDrawing(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{}, false }
	g.rows = 10
	_, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	run(1)
	if !g.Step(time.Second / 60) {
		t.Fatal("a grid showing rows yet to arrive stops animating their placeholders")
	}
}

func TestAMarkLightsTheRunesItCovers(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Message"})
	g.Row = func(int) (GridRow, bool) {
		return GridRow{Cells: [][]GridSpan{{{Text: "find the needle here", Marks: [][2]int{{9, 15}}}}}}, true
	}
	g.rows = 1
	w, _ := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	run := Font.Default().Shape("find the needle here", GridTextSize.Default())
	want0, want1 := run.CaretX(9), run.CaretX(15)
	found := false
	for _, op := range w.Offscreen().Ops() {
		r, ok := op.(*paint.RRectOp)
		if !ok || r.Fill.Solid != GridMark.Default() {
			continue
		}
		found = true
		x := r.Transform.Apply(r.Rect.Min).X
		pad := GridCellPadding.Default()
		if d := x - (pad + want0); d < -0.5 || d > 0.5 || r.Rect.Size().W-(want1-want0) > 0.5 {
			t.Fatalf("the mark is at %v, %v wide; want %v, %v wide", x, r.Rect.Size().W, pad+want0, want1-want0)
		}
	}
	if !found {
		t.Fatal("no mark was drawn")
	}
}

type gridClicked struct{ Row int }

func TestEveryClickOnARowIsSent(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(int) (GridRow, bool) { return GridRow{}, true }
	g.OnClick = func(row int, u *gunim.UI) gunim.Intent { return gridClicked{row} }
	g.rows = 10
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	y := GridHeaderHeight.Default() + GridRowHeight.Default()*2 + 5
	click(w, 50, y)
	run(1)
	click(w, 50, y)
	run(1)
	if got := sent(w); len(got) != 2 || got[0] != (gridClicked{2}) || got[1] != (gridClicked{2}) {
		t.Fatalf("two clicks on row 2 sent %v", got)
	}
}

type gridSorted struct{ Column int }

func TestAClickOnATitleIsSent(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name", Width: 100, Sort: 1}, GridColumn{Title: "Size", Closable: true})
	g.OnHeader = func(c int, u *gunim.UI) gunim.Intent { return gridSorted{c} }
	g.OnClose = func(c int, u *gunim.UI) gunim.Intent { return gridResized{c, 0} }
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	click(w, 40, 10)
	run(1)
	click(w, 150, 10)
	run(1)
	if got := sent(w); len(got) != 2 || got[0] != (gridSorted{0}) || got[1] != (gridSorted{1}) {
		t.Fatalf("clicks on the titles sent %v", got)
	}
}

type idFound struct{ ID string }

type idOpened struct{ ID string }

func TestASpanThatIsALinkSendsItsIntent(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Text"})
	g.Row = func(int) (GridRow, bool) {
		return GridRow{Cells: [][]GridSpan{{{Text: `"id": `}, {Text: `"Ab3d"`, OnClick: Sends(idFound{"Ab3d"}), OnCtrlClick: Sends(idOpened{"Ab3d"})}}}}, true
	}
	g.rows = 1
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	if len(g.links) != 1 {
		t.Fatalf("the grid drew %d links, want 1", len(g.links))
	}
	at := g.links[0].r.Center()
	click(w, at.X, at.Y)
	run(1)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Mods: input.ModControl, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	run(1)
	if got := sent(w); len(got) != 2 || got[0] != (idFound{"Ab3d"}) || got[1] != (idOpened{"Ab3d"}) {
		t.Fatalf("a click and a Ctrl+click on the link sent %v", got)
	}
}

func TestArrivingRowsComeInFromTheTop(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(int) (GridRow, bool) { return GridRow{Cells: [][]GridSpan{{{Text: "row"}}}}, true }
	g.rows = 100
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	type arrive struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ arrive, u *gunim.UI) { g.Arrive(u) })
	if err := w.Client().Patch("stage", arrive{}); err != nil {
		t.Fatal(err)
	}
	run(6)
	first, last := g.arrived(0), g.arrived(12)
	if first <= last || first <= 0 || first >= 1 {
		t.Fatalf("100 ms in, the first row has come %v of the way and the thirteenth %v", first, last)
	}
	run(60)
	if g.arrived(12) != 1 || g.Step(time.Second/60) {
		t.Fatal("a second in, the rows have not all come in and settled")
	}
}

func TestLeavingRowsShutAndTheRowsBelowSlideUp(t *testing.T) {
	names := make([]string, 100)
	for i := range names {
		names[i] = strconv.Itoa(i)
	}
	shown := names
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{Cells: [][]GridSpan{{{Text: shown[i]}}}}, true }
	g.rows = len(names)
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	type leave struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ leave, u *gunim.UI) {
		g.Leave([]int{4, 3, 90}, u)
		shown = slices.Concat(names[:3], names[5:90], names[91:])
		g.SetRows(len(shown), u)
	})
	was := g.rowY(5)
	if err := w.Client().Patch("stage", leave{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if g.Leaving() != 2 {
		t.Fatalf("%d rows in view are leaving, want 2", g.Leaving())
	}
	if at, above := g.before(3); at != 5 || above != 2 {
		t.Fatalf("row 3 was row %d with %d leaving above it, want 5 and 2", at, above)
	}
	if y := g.rowY(3); y > was+0.01 || y < was-g.rowH {
		t.Fatalf("a frame in, the row after those leaving is at %v, want it to start from %v", y, was)
	}
	run(6)
	mid := g.rowY(3)
	if s := g.shut(); s <= 0 || s >= 1 || mid >= was || mid <= g.header+3*g.rowH {
		t.Fatalf("100 ms in, the rows have shut %v and the row below is at %v, between %v and %v", s, mid,
			g.header+3*g.rowH, was)
	}
	run(60)
	if len(g.gone) != 0 || g.rowY(3) != g.header+3*g.rowH || g.Step(time.Second/60) {
		t.Fatal("a second in, the rows have not all left and settled")
	}
}

// While rows leave, a point finds the row drawn under it, and RowRect
// says where a row is drawn; the room a leaving row still takes is no
// row's.
func TestAPointFindsTheRowDrawnThereWhileRowsLeave(t *testing.T) {
	names := make([]string, 100)
	for i := range names {
		names[i] = strconv.Itoa(i)
	}
	shown := names
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{Cells: [][]GridSpan{{{Text: shown[i]}}}}, true }
	g.rows = len(names)
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	type leave struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ leave, u *gunim.UI) {
		g.Leave([]int{3, 4}, u)
		shown = slices.Concat(names[:3], names[5:])
		g.SetRows(len(shown), u)
	})
	if err := w.Client().Patch("stage", leave{}); err != nil {
		t.Fatal(err)
	}
	run(6)
	drawn := g.rowY(3)
	settled := g.header + 3*g.rowH
	if drawn <= settled+2 {
		t.Fatalf("100 ms in, row 3 is drawn at %v, want it still below %v", drawn, settled)
	}
	if got := g.rowAtY(drawn + 1); got != 3 {
		t.Fatalf("a point on the row drawn as row 3 finds row %d", got)
	}
	if r, ok := g.RowRect(3); !ok || r.Min.Y != drawn {
		t.Fatalf("RowRect(3) is %v, want it at %v, where the row is drawn", r, drawn)
	}
	if got := g.rowAtY(settled + 1); got != -1 {
		t.Fatalf("a point in the room the leaving rows still take finds row %d", got)
	}
}

// The last row, drawn lower while a row above it leaves, is found at
// the point it is drawn, below where the rows end once settled.
func TestTheLastRowIsFoundWhileARowAboveItLeaves(t *testing.T) {
	names := []string{"0", "1", "2", "3", "4", "5"}
	shown := names
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(i int) (GridRow, bool) { return GridRow{Cells: [][]GridSpan{{{Text: shown[i]}}}}, true }
	g.rows = len(names)
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	type leave struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ leave, u *gunim.UI) {
		g.Leave([]int{2}, u)
		shown = slices.Concat(names[:2], names[3:])
		g.SetRows(len(shown), u)
	})
	if err := w.Client().Patch("stage", leave{}); err != nil {
		t.Fatal(err)
	}
	run(6)
	last := g.rows - 1
	drawn := g.rowY(last)
	if end := g.header + float32(g.rows)*g.rowH; drawn+g.rowH-1 < end {
		t.Fatalf("100 ms in, the last row's foot is at %v, want it below %v, where the rows end", drawn+g.rowH-1, end)
	}
	if got := g.rowAtY(drawn + g.rowH - 1); got != last {
		t.Fatalf("a point near the foot of the last row, drawn at %v, finds row %d, want %d", drawn, got, last)
	}
}

func TestASpanShowsItsIconBeforeItsText(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(int) (GridRow, bool) {
		return GridRow{Cells: [][]GridSpan{{{Text: "notes.txt", Icon: icon.FileText}}}}, true
	}
	g.rows = 1
	w, _ := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	masks := maskOps(w.Offscreen())
	if len(masks) != 1 || strokeOf(t, masks[0]).Icon != icon.FileText {
		t.Fatalf("the grid drew %d masks, want the span's icon", len(masks))
	}
	size := GridTextSize.Default()
	if r := masks[0].Rect; r.Size() != geom.Sz(size, size) {
		t.Fatalf("the icon is %v, want %v square, as tall as the text", r.Size(), size)
	}
}

func TestAnIconAloneIsALink(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name"})
	g.Row = func(int) (GridRow, bool) {
		return GridRow{Cells: [][]GridSpan{{{Icon: icon.Copy, OnClick: Sends(idFound{"copy"})}, {Text: " notes.txt"}}}}, true
	}
	g.rows = 1
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	if len(g.links) != 1 {
		t.Fatalf("the grid drew %d links, want the icon's", len(g.links))
	}
	if r := g.links[0].r; r.Size().W != GridTextSize.Default() {
		t.Fatalf("the icon's link is %v wide, want the icon's width", r.Size().W)
	}
	at := g.links[0].r.Center()
	click(w, at.X, at.Y)
	run(1)
	if got := sent(w); len(got) != 1 || got[0] != (idFound{"copy"}) {
		t.Fatalf("a click on the icon sent %v", got)
	}
}

func TestADataGridTipsACellCutShortWithItsWholeText(t *testing.T) {
	long := strings.Repeat("a value too long for its column ", 40)
	g := NewDataGrid(GridColumn{Title: "Short", Width: 120}, GridColumn{Title: "Long"})
	g.Row = func(i int) (GridRow, bool) {
		return GridRow{Cells: [][]GridSpan{{{Text: "fits"}}, {{Text: long}}}}, true
	}
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	g.rows = 3
	run(2)
	y := g.header + g.rowH/2
	w.Input(input.PointerMove{Pos: geom.Pt(20, y), Time: time.Now()})
	run(60)
	if g.tip.tip.popup != nil {
		t.Fatal("a cell that fits showed a tooltip")
	}
	w.Input(input.PointerMove{Pos: geom.Pt(300, y), Time: time.Now()})
	run(60)
	if g.tip.tip.popup == nil {
		t.Fatal("a cell cut short showed no tooltip")
	}
	if got := g.tip.tip.text; got != strings.TrimSpace(long) {
		t.Fatalf("the tooltip says %q, want the whole value", got)
	}
	card := g.tip.tip.card
	if !card.wrapped {
		t.Fatal("a tooltip far wider than the most a tooltip may be did not wrap")
	}
	w.Input(input.PointerLeave{Time: time.Now()})
	run(60)
	if g.tip.tip.popup != nil {
		t.Fatal("the tooltip stayed after the pointer left")
	}
}

// A sideways wheel moves the titles under a pointer at rest: the title lit is the one under it now.
func TestADataGridLightsTheTitleUnderThePointerAfterASidewaysWheel(t *testing.T) {
	cols := make([]GridColumn, 6)
	for i := range cols {
		cols[i] = GridColumn{Title: "Column " + strconv.Itoa(i), Width: 150, Closable: true}
	}
	g := NewDataGrid(cols...)
	g.Row = func(int) (GridRow, bool) { return GridRow{}, true }
	g.rows = 10
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	at := geom.Pt(100, g.header/2)
	w.Input(input.PointerMove{Pos: at, Time: time.Now()})
	run(1)
	if g.hoverCol != 0 {
		t.Fatalf("the pointer on the first title lights title %d", g.hoverCol)
	}
	for _, e := range []input.Scroll{
		{Pos: at, Delta: geom.Pt(-200, 0), Time: time.Now()},
		{Pos: at, Delta: geom.Pt(0, -150), Mods: input.ModShift, Time: time.Now()},
	} {
		w.Input(e)
		run(1)
		if want := g.columnAt(at.X); g.hoverCol != want || want <= 0 {
			t.Fatalf("scrolled sideways to %v, title %d is lit, want %d, now under the pointer", g.left, g.hoverCol, want)
		}
	}
}
