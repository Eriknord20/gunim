package widget

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

type gridRows struct {
	Sel    [][2]int
	Cursor int
}

// multiGrid stages a grid of 100 rows with Multi on, and returns a click
// on row i with mods.
func multiGrid(t *testing.T) (g *DataGrid, w *gunim.Window, run func(int), clickRow func(i int, mods input.Mods)) {
	t.Helper()
	g = NewDataGrid(GridColumn{Title: "Name"})
	g.Multi = true
	g.Row = func(int) (GridRow, bool) { return GridRow{Cells: [][]GridSpan{{{Text: "row"}}}}, true }
	g.OnSelectRows = func(sel [][2]int, cursor int, u *gunim.UI) gunim.Intent { return gridRows{sel, cursor} }
	g.rows = 100
	w, run = stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	clickRow = func(i int, mods input.Mods) {
		y := GridHeaderHeight.Default() + GridRowHeight.Default()*float32(i) + 5
		w.Input(input.PointerDown{Pos: geom.Pt(50, y), Button: input.ButtonPrimary, Clicks: 1, Mods: mods, Time: time.Now()})
		w.Input(input.PointerUp{Pos: geom.Pt(50, y), Button: input.ButtonPrimary, Mods: mods, Time: time.Now()})
		run(1)
	}
	return g, w, run, clickRow
}

func lastRows(t *testing.T, w *gunim.Window) [][2]int {
	t.Helper()
	return lastChange(t, w).Sel
}

func lastChange(t *testing.T, w *gunim.Window) gridRows {
	t.Helper()
	var last gridRows
	found := false
	for _, v := range sent(w) {
		if r, ok := v.(gridRows); ok {
			last, found = r, true
		}
	}
	if !found {
		t.Fatal("no change of the rows selected was sent")
	}
	return last
}

func TestCtrlClickAddsAndTakesAwayRows(t *testing.T) {
	g, w, _, clickRow := multiGrid(t)
	clickRow(2, 0)
	clickRow(5, input.ModControl)
	clickRow(6, input.ModControl)
	if got, want := lastRows(t, w), [][2]int{{2, 3}, {5, 7}}; !slices.Equal(got, want) {
		t.Fatalf("after a click on 2 and Ctrl+clicks on 5 and 6, rows %v are selected, want %v", got, want)
	}
	clickRow(5, input.ModControl)
	if got, want := lastRows(t, w), [][2]int{{2, 3}, {6, 7}}; !slices.Equal(got, want) {
		t.Fatalf("after a second Ctrl+click on 5, rows %v are selected, want %v", got, want)
	}
	if g.IsSelected(5) || !g.IsSelected(6) || !g.IsSelected(2) {
		t.Fatal("IsSelected disagrees with the rows sent")
	}
}

func TestShiftClickSelectsARunFromTheAnchor(t *testing.T) {
	_, w, _, clickRow := multiGrid(t)
	clickRow(4, 0)
	clickRow(8, input.ModShift)
	if got, want := lastRows(t, w), [][2]int{{4, 9}}; !slices.Equal(got, want) {
		t.Fatalf("a Shift+click on 8 from 4 selected %v, want %v", got, want)
	}
	clickRow(1, input.ModShift)
	if got, want := lastRows(t, w), [][2]int{{1, 5}}; !slices.Equal(got, want) {
		t.Fatalf("a Shift+click on 1 from 4 selected %v, want %v", got, want)
	}
	clickRow(10, input.ModControl)
	clickRow(12, input.ModControl|input.ModShift)
	if got, want := lastRows(t, w), [][2]int{{1, 5}, {10, 13}}; !slices.Equal(got, want) {
		t.Fatalf("Ctrl+Shift+click added %v, want %v", got, want)
	}
}

func TestCtrlASelectsEveryRowAndEscapeClears(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	clickRow(3, 0)
	w.Input(input.KeyPress{Key: input.KeyA, Mods: input.ModControl})
	run(1)
	if got, want := lastRows(t, w), [][2]int{{0, 100}}; !slices.Equal(got, want) {
		t.Fatalf("Ctrl+A selected %v, want %v", got, want)
	}
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	if got := lastRows(t, w); len(got) != 0 {
		t.Fatalf("Escape left %v selected", got)
	}
	if g.Selected() >= 0 {
		t.Fatal("Escape left the keyboard on a row")
	}
}

func TestShiftArrowsGrowTheSelection(t *testing.T) {
	_, w, run, clickRow := multiGrid(t)
	clickRow(5, 0)
	w.Input(input.KeyPress{Key: input.KeyDown, Mods: input.ModShift})
	w.Input(input.KeyPress{Key: input.KeyDown, Mods: input.ModShift})
	run(1)
	if got, want := lastChange(t, w), [][2]int{{5, 8}}; !slices.Equal(got.Sel, want) || got.Cursor != 7 {
		t.Fatalf("two Shift+Down from 5 selected %v with the keyboard on %d, want %v on 7", got.Sel, got.Cursor, want)
	}
	w.Input(input.KeyPress{Key: input.KeyUp})
	run(1)
	if got, want := lastRows(t, w), [][2]int{{6, 7}}; !slices.Equal(got, want) {
		t.Fatalf("Up without Shift selected %v, want %v", got, want)
	}
}

func TestAClickBelowTheRowsClearsAMultipleSelection(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.rows = 3
	clickRow(0, 0)
	clickRow(2, input.ModShift)
	click(w, 50, 250)
	run(1)
	if got := lastRows(t, w); len(got) != 0 {
		t.Fatalf("a click below the rows left %v selected", got)
	}
}

func TestSetRowsCutsTheSelectionShort(t *testing.T) {
	g, w, run, _ := multiGrid(t)
	type act struct{ do func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, a act, u *gunim.UI) { a.do(u) })
	on := func(do func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", act{do}); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	on(func(u *gunim.UI) { g.SetSelectedRows([][2]int{{50, 60}, {1, 3}}, 50, u) })
	if got, want := g.SelectedRows(), [][2]int{{1, 3}, {50, 60}}; !slices.Equal(got, want) {
		t.Fatalf("SetSelectedRows kept %v, want %v", got, want)
	}
	on(func(u *gunim.UI) { g.SetRows(55, u) })
	if got, want := g.SelectedRows(), [][2]int{{1, 3}, {50, 55}}; !slices.Equal(got, want) {
		t.Fatalf("after cutting the grid to 55 rows, %v are selected, want %v", got, want)
	}
}

func TestRunsStayInOrderAndApart(t *testing.T) {
	var runs [][2]int
	runs = addRun(runs, 10, 12)
	runs = addRun(runs, 2, 4)
	runs = addRun(runs, 4, 6)
	runs = addRun(runs, 11, 20)
	if want := [][2]int{{2, 6}, {10, 20}}; !slices.Equal(runs, want) {
		t.Fatalf("runs %v, want %v", runs, want)
	}
	runs = removeRun(runs, 15)
	if want := [][2]int{{2, 6}, {10, 15}, {16, 20}}; !slices.Equal(runs, want) {
		t.Fatalf("after taking 15 away, runs %v, want %v", runs, want)
	}
	for i, want := range map[int]bool{1: false, 2: true, 5: true, 6: false, 15: false, 16: true, 19: true, 20: false} {
		if hasRun(runs, i) != want {
			t.Fatalf("hasRun(%d) is %v, want %v", i, !want, want)
		}
	}
	if n := countRuns(runs); n != 13 {
		t.Fatalf("the runs hold %d rows, want 13", n)
	}
}

type rowsCopied struct{ Sel [][2]int }

func TestOnCopyTakesCtrlCInPlaceOfText(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.OnCopy = func(sel [][2]int, u *gunim.UI) gunim.Intent { return rowsCopied{sel} }
	clickRow(2, 0)
	clickRow(4, input.ModShift)
	sent(w)
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	got := sent(w)
	if len(got) != 1 {
		t.Fatalf("Ctrl+C sent %v, want one intent", got)
	}
	if c, ok := got[0].(rowsCopied); !ok || !slices.Equal(c.Sel, [][2]int{{2, 5}}) {
		t.Fatalf("Ctrl+C sent %v, want rows 2 to 4", got[0])
	}
}

func TestASecondaryPressSelectsTheRowUnlessItIsSelected(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	press := func(i int) {
		y := GridHeaderHeight.Default() + GridRowHeight.Default()*float32(i) + 5
		w.Input(input.PointerDown{Pos: geom.Pt(50, y), Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
		w.Input(input.PointerUp{Pos: geom.Pt(50, y), Button: input.ButtonSecondary, Time: time.Now()})
		run(1)
	}
	clickRow(2, 0)
	clickRow(4, input.ModShift)
	press(3)
	if got := g.SelectedRows(); !slices.Equal(got, [][2]int{{2, 5}}) {
		t.Fatalf("a secondary press on a selected row left %v selected, want 2 to 4", got)
	}
	press(9)
	if got := lastRows(t, w); !slices.Equal(got, [][2]int{{9, 10}}) {
		t.Fatalf("a secondary press on row 9 selected %v, want it alone", got)
	}
}

// Ctrl+C copies the selected rows that have arrived, a line each, and
// leaves out the rest rather than copying blank lines; however many
// rows are selected, it copies MostCopiedRows at most.
func TestCtrlCCopiesTheRowsThatHaveArrived(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	asked := 0
	g.Row = func(i int) (GridRow, bool) {
		asked++
		return GridRow{Cells: [][]GridSpan{{{Text: "row " + strconv.Itoa(i)}}}}, i%2 == 0
	}
	g.rows = 1_000_000
	clickRow(0, 0)
	asked = 0
	w.Input(input.KeyPress{Key: input.KeyA, Mods: input.ModControl})
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	text, err := w.Offscreen().Clipboard()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != MostCopiedRows/2 || lines[0] != "row 0" || lines[1] != "row 2" {
		t.Fatalf("Ctrl+C copied %d lines starting %q, want the %d that arrived of the first %d, from rows 0 and 2",
			len(lines), lines[:min(2, len(lines))], MostCopiedRows/2, MostCopiedRows)
	}
	if asked > MostCopiedRows+100 {
		t.Fatalf("Ctrl+C asked for %d rows, want no more than the %d it looks at, and the rows in view", asked, MostCopiedRows)
	}
}

// Ctrl+C with no selected row arrived leaves the clipboard as it was.
func TestCtrlCOfRowsStillComingLeavesTheClipboard(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.Row = func(i int) (GridRow, bool) { return GridRow{}, i == 0 }
	clickRow(0, 0)
	if err := w.Offscreen().SetClipboard("kept"); err != nil {
		t.Fatal(err)
	}
	w.Input(input.KeyPress{Key: input.KeyA, Mods: input.ModControl})
	g.runs = [][2]int{{1, 50}}
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	if text, _ := w.Offscreen().Clipboard(); text != "kept" {
		t.Fatalf("Ctrl+C of rows still coming left the clipboard %q, want it as it was", text)
	}
}

type rowsCopiedOf struct{ Copied, Selected int }

// OnCopied hears how many rows Ctrl+C copied of how many were selected.
func TestOnCopiedSaysHowManyRowsWereCopied(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.Row = func(i int) (GridRow, bool) { return GridRow{Cells: [][]GridSpan{{{Text: "row"}}}}, i%2 == 0 }
	g.OnCopied = func(copied, selected int, u *gunim.UI) gunim.Intent { return rowsCopiedOf{copied, selected} }
	clickRow(0, 0)
	w.Input(input.KeyPress{Key: input.KeyA, Mods: input.ModControl})
	sent(w)
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	var got []rowsCopiedOf
	for _, v := range sent(w) {
		if c, ok := v.(rowsCopiedOf); ok {
			got = append(got, c)
		}
	}
	if len(got) != 1 || got[0] != (rowsCopiedOf{50, 100}) {
		t.Fatalf("OnCopied heard %v, want 50 rows copied of 100", got)
	}
}

// between reports whether v is strictly between a and b.
func between(v, a, b float64) bool { return v > min(a, b) && v < max(a, b) }

func TestWithGlideTheSelectionSlidesAsTheKeysMoveIt(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.Glide = true
	clickRow(5, 0)
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	if !g.gliding() || !between(g.glideAt[0], 5, 6) || !between(g.glideAt[1], 6, 7) {
		t.Fatalf("a frame after Down from 5 the selection is at %v, sliding %v; want on its way to 6", g.glideAt, g.gliding())
	}
	// A key pressed again on the way goes on from where the slide is.
	at := g.glideAt[0]
	w.Input(input.KeyPress{Key: input.KeyDown})
	if !g.gliding() || g.glideAt[0] != at {
		t.Fatalf("Down again started the slide from %v, not from %v where it was", g.glideAt[0], at)
	}
	run(60)
	if g.gliding() || lastChange(t, w).Cursor != 7 || g.glideAt != [2]float64{7, 8} {
		t.Fatalf("the slide did not settle on 7: at %v, sliding %v", g.glideAt, g.gliding())
	}
	w.Input(input.KeyPress{Key: input.KeyEnd})
	run(1)
	if g.gliding() {
		t.Fatal("End slid the selection across more than the view")
	}

	g.Glide = false
	clickRow(2, 0)
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	if g.gliding() {
		t.Fatal("without Glide the selection slid")
	}
}

func TestWithGlideShiftStretchesTheSelection(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.Glide = true
	clickRow(5, 0)
	w.Input(input.KeyPress{Key: input.KeyDown, Mods: input.ModShift})
	run(1)
	if !g.gliding() || g.glideAt[0] != 5 || !between(g.glideAt[1], 6, 7) {
		t.Fatalf("a frame after Shift+Down from 5 the selection is at %v, sliding %v; want its end on its way to 7",
			g.glideAt, g.gliding())
	}
	run(60)
	w.Input(input.KeyPress{Key: input.KeyUp, Mods: input.ModShift})
	run(1)
	if !g.gliding() || g.glideAt[0] != 5 || !between(g.glideAt[1], 6, 7) {
		t.Fatalf("a frame after Shift+Up the selection is at %v, sliding %v; want its end on its way back to 6",
			g.glideAt, g.gliding())
	}
	run(60)
	clickRow(8, input.ModShift)
	run(1)
	if !g.gliding() || g.glideAt[0] != 5 || !between(g.glideAt[1], 6, 9) {
		t.Fatalf("a frame after Shift and a click on 8 the selection is at %v, sliding %v", g.glideAt, g.gliding())
	}
	clickRow(2, input.ModControl)
	if g.gliding() {
		t.Fatal("Ctrl and a click made two blocks, and one slid")
	}
}

func TestWithGlideAClickSlidesTheSelection(t *testing.T) {
	g, _, run, clickRow := multiGrid(t)
	g.Glide = true
	clickRow(2, 0)
	run(60)
	clickRow(6, 0)
	if !g.gliding() || !between(g.glideAt[0], 2, 6) {
		t.Fatalf("a frame after a click on 6 the selection is at %v, sliding %v; want on its way from 2", g.glideAt, g.gliding())
	}
}

func TestSlideSelectedRowsSlidesWhatTheProgramSelects(t *testing.T) {
	g, w, run, clickRow := multiGrid(t)
	g.Glide = true
	clickRow(2, 0)
	run(60)
	u := stageUI(t, w, run)
	g.SlideSelectedRows([][2]int{{9, 10}}, 9, u)
	run(1)
	if !g.gliding() || !between(g.glideAt[0], 2, 9) {
		t.Fatalf("a frame after SlideSelectedRows to 9 the selection is at %v, sliding %v", g.glideAt, g.gliding())
	}
	run(60)
	g.SetSelectedRows([][2]int{{3, 4}}, 3, u)
	if g.gliding() {
		t.Fatal("SetSelectedRows slid the selection")
	}
}
