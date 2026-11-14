package widget

import (
	"fmt"
	"image/color"
	"math"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"golang.org/x/image/font/gofont/goregular"
)

// cellsOf turns s into a row of plain cells.
func cellsOf(s string) []Cell {
	out := make([]Cell, 0, len(s))
	for _, r := range s {
		out = append(out, Cell{Rune: r})
	}
	return out
}

// newCellStage mounts a grid of 40 by 12 cells in a 400 by 300 space,
// with a line of text in every row.
func newCellStage(t testing.TB) (*gunim.Window, *CellGrid, func(int)) {
	g := NewCellGrid()
	g.Size = 14
	g.Resize(40, 12)
	for y := range 12 {
		g.SetRow(y, cellsOf(fmt.Sprintf("row %d: the quick brown fox", y)))
	}
	w := gunimtest.New(t, geom.Sz(400, 300), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "cells", func(struct{}) gunim.Node { return g }, nil)
	if err := w.Client().Mount(gunim.Root, "cells", "cells", nil); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(16_666_667)
		}
	}
	run(2)
	return w, g, run
}

// drawnOps returns ops up to the last that draws, past the ends of the layers round it, such as the grid's clip.
func drawnOps(ops []paint.Op) []paint.Op {
	for len(ops) > 1 {
		if _, end := ops[len(ops)-1].(*paint.LayerEndOp); !end {
			break
		}
		ops = ops[:len(ops)-1]
	}
	return ops
}

func TestAGridFitsWholeCellsToItsSpace(t *testing.T) {
	_, g, _ := newCellStage(t)
	cell := g.CellSize()
	if cell.W <= 0 || cell.H <= 0 {
		t.Fatalf("cells are %v", cell)
	}
	if cell.W != float32(math.Round(float64(cell.W))) || cell.H != float32(math.Round(float64(cell.H))) {
		t.Fatalf("cells are %v, want whole pixels at scale 1", cell)
	}
	cols, rows := g.Fit()
	if cols != int(400/cell.W) || rows != int(300/cell.H) {
		t.Fatalf("fit %d by %d cells of %v in 400 by 300", cols, rows, cell)
	}
}

func TestAGridRedrawsOnlyTheRowThatChanged(t *testing.T) {
	w, g, run := newCellStage(t)
	off := w.Offscreen()
	if d := off.Damage(); !d.Empty() {
		t.Fatalf("a frame with nothing new damaged %v", d)
	}
	// The same cells again change nothing.
	g.SetRow(5, cellsOf("row 5: the quick brown fox"))
	run(1)
	if d := off.Damage(); !d.Empty() {
		t.Fatalf("setting a row to what it held damaged %v", d)
	}
	g.SetRow(5, cellsOf("row 5: jumps over the lazy dog"))
	run(1)
	h := g.CellSize().H
	d := off.Damage()
	if d.Empty() || d.Min.Y < 5*h-2 || d.Max.Y > 6*h+2 {
		t.Fatalf("changing row 5, %v to %v down, damaged %v", 5*h, 6*h, d)
	}
}

func TestTheCursorGlidesAlongItsRowAndJumpsToAnother(t *testing.T) {
	_, g, run := newCellStage(t)
	g.SetCursor(Cursor{Col: 0, Row: 2, Visible: true})
	run(30)
	cell := g.CellSize()
	g.SetCursor(Cursor{Col: 6, Row: 2, Visible: true})
	last := g.at.Value().X
	for i := range 30 {
		run(1)
		x := g.at.Value().X
		if x < last {
			t.Fatalf("frame %d: the cursor moved back, from %v to %v", i, last, x)
		}
		last = x
	}
	if last != 6*cell.W {
		t.Fatalf("the cursor settled at %v, want %v", last, 6*cell.W)
	}
	g.SetCursor(Cursor{Col: 1, Row: 7, Visible: true})
	run(1)
	if at := g.at.Value(); at != geom.Pt(cell.W, 7*cell.H) {
		t.Fatalf("a frame after moving rows the cursor is at %v, want %v", at, geom.Pt(cell.W, 7*cell.H))
	}
}

func TestABlockCursorShowsItsCharacterInTheCellsBackground(t *testing.T) {
	w, g, run := newCellStage(t)
	bg := color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}
	row := cellsOf("abc")
	row[1].BG = bg
	g.SetRow(0, row)
	g.SetCursor(Cursor{Col: 1, Row: 0, Visible: true, Color: color.NRGBA{R: 0xff, A: 0xff}})
	run(30)
	ops := drawnOps(w.Offscreen().Ops())
	last, ok := ops[len(ops)-1].(*paint.TextOp)
	if !ok || last.Color != bg || len(last.Glyphs) != 1 {
		t.Fatalf("the last op is %+v, want b in the cell's background", ops[len(ops)-1])
	}
}

func TestASqueezedGridDrawsItsCellsInsideItsBox(t *testing.T) {
	g := NewCellGrid()
	g.Size = 14
	g.Resize(80, 30)
	for y := range 30 {
		g.SetRow(y, cellsOf(fmt.Sprintf("row %d: the quick brown fox jumps over the lazy dog, again and again", y)))
	}
	g.SetCursor(Cursor{Col: 70, Row: 29, Visible: true})
	box := geom.Sz(150, 95)
	w, run := stage(t, &frame{child: g, size: box})
	for i := range 5 {
		run(1)
		depth, cells := 0, 0
		for _, op := range w.Offscreen().Ops() {
			switch op := op.(type) {
			case *paint.LayerOp:
				if op.Opts.Clip {
					b := geom.Rect{Min: op.Transform.Apply(op.Opts.Bounds.Min), Max: op.Transform.Apply(op.Opts.Bounds.Max)}
					if b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > box.W || b.Max.Y > box.H {
						t.Fatalf("frame %d: the grid clips to %v, past its %v box", i, b, box)
					}
				}
				depth++
			case *paint.LayerEndOp:
				depth--
			case *paint.CellsOp, *paint.TextOp:
				cells++
				if depth == 0 {
					t.Fatalf("frame %d: the grid drew %T outside a clip to its box", i, op)
				}
			}
		}
		if cells == 0 {
			t.Fatalf("frame %d: the grid drew no cells", i)
		}
	}
}

func TestAWideCharacterCoversTwoCells(t *testing.T) {
	w, g, run := newCellStage(t)
	red := color.NRGBA{R: 0xff, A: 0xff}
	g.SetRow(0, []Cell{{Rune: '中', Wide: true, BG: red}, {}, {Rune: 'x'}})
	run(1)
	row, ok := cellsRow(w.Offscreen().Ops(), 0)
	if !ok || len(row.Cells) < 3 || row.Cells[0].BG != red || row.Cells[1].BG != red || row.Cells[2].BG == red {
		t.Fatalf("the first row's cells are %+v, want the wide character's background over two", row)
	}
}

// cellsRow returns the cells of row y among ops.
func cellsRow(ops []paint.Op, y int) (*paint.CellsOp, bool) {
	for _, op := range ops {
		if c, ok := op.(*paint.CellsOp); ok && c.Row == y {
			return c, true
		}
	}
	return nil, false
}

// BenchmarkAGridScrollingAFullScreen draws a 200 by 60 terminal whose
// every row changes each frame, as it does while text scrolls past.
func BenchmarkAGridScrollingAFullScreen(b *testing.B) {
	g := NewCellGrid()
	g.Size = 14
	g.Resize(200, 60)
	w := gunimtest.New(b, geom.Sz(1700, 1100), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "cells", func(struct{}) gunim.Node { return g }, nil)
	if err := w.Client().Mount(gunim.Root, "cells", "cells", nil); err != nil {
		b.Fatal(err)
	}
	lines := make([][]Cell, 61)
	for i := range lines {
		row := cellsOf(fmt.Sprintf("%4d %-190s", i, "func (g *CellGrid) drawRow(cells []Cell, ink color.NRGBA) rowPaint { m := g.metrics"))
		for x := range row {
			row[x].FG = color.NRGBA{R: uint8(40 * (x / 20)), G: 0xc0, B: 0x80, A: 0xff}
		}
		lines[i] = row
	}
	w.Frame(16_666_667)
	b.ResetTimer()
	for i := range b.N {
		for y := range 60 {
			g.SetRow(y, lines[(y+i)%61])
		}
		w.Frame(16_666_667)
	}
}

func TestAnOutlineCursorIsHollow(t *testing.T) {
	w, g, run := newCellStage(t)
	red := color.NRGBA{R: 0xff, A: 0xff}
	g.SetCursor(Cursor{Col: 2, Row: 1, Visible: true, Shape: CursorOutline, Color: red})
	run(30)
	ops := drawnOps(w.Offscreen().Ops())
	last, ok := ops[len(ops)-1].(*paint.RRectOp)
	if !ok || last.Fill.Solid.A != 0 || last.Stroke.Color != red || last.Stroke.Width <= 0 {
		t.Fatalf("the last op is %+v, want a red outline with nothing inside", ops[len(ops)-1])
	}
}

func TestABlinkFadesTheCursorOutAndBack(t *testing.T) {
	_, g, run := newCellStage(t)
	g.SetCursor(Cursor{Col: 1, Row: 1, Visible: true})
	run(30)
	g.SetCursor(Cursor{Col: 1, Row: 1, Visible: true, Blinked: true})
	last := g.lit.Value()
	for i := range 30 {
		run(1)
		if v := g.lit.Value(); v > last+0.001 {
			t.Fatalf("frame %d: fading out, the cursor brightened from %v to %v", i, last, v)
		}
		last = g.lit.Value()
	}
	if last > 0.01 {
		t.Fatalf("blinked off, the cursor is still at %v", last)
	}
	g.SetCursor(Cursor{Col: 1, Row: 1, Visible: true})
	run(30)
	if v := g.lit.Value(); v < 0.99 {
		t.Fatalf("blinked on, the cursor came back to %v", v)
	}
}

// Faces changed after the grid has been drawn take effect: the cells
// are measured again and drawn in the new face.
func TestAGridTakesNewFaces(t *testing.T) {
	_, g, run := newCellStage(t)
	mono := g.CellSize()
	regular, err := text.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	g.Faces = [4]*text.Face{regular}
	run(2)
	if g.CellSize() == mono {
		t.Fatalf("in Go Regular, the cells are still Go Mono's %v", mono)
	}
}

// A rule of box-drawing characters is drawn to the cell, so it runs
// unbroken from the first cell's left edge to the last one's right.
func TestABoxRuleJoinsAcrossCells(t *testing.T) {
	w, g, run := newCellStage(t)
	green := color.NRGBA{G: 0xff, A: 0xff}
	g.SetRow(0, []Cell{{Rune: '─', FG: green}, {Rune: '┼', FG: green}, {Rune: '─', FG: green}})
	run(1)
	row, ok := cellsRow(w.Offscreen().Ops(), 0)
	if !ok {
		t.Fatal("the row drew no cells")
	}
	pats := row.Patterns
	// A pixel row covered all the way across in each cell's pattern.
	across := func(n uint16) map[int]bool {
		out := map[int]bool{}
		if n == 0 || int(n) > len(pats.Masks) {
			return out
		}
		m := pats.Masks[n-1]
		for y := range pats.H {
			full := true
			for x := range pats.W {
				full = full && m[y*pats.W+x] == 255
			}
			out[y] = full
		}
		return out
	}
	for y := range pats.H {
		joined := true
		for _, c := range row.Cells[:3] {
			joined = joined && c.FG == green && across(c.Pattern)[y]
		}
		if joined {
			return
		}
	}
	t.Fatalf("no line of pixels runs through all three cells' patterns: %+v", row.Cells[:3])
}

// A grid whose corner falls between device pixels still puts every
// cell on whole device pixels, so half blocks side by side and row on
// row meet with no seam of background between them.
func TestCellsLandOnDevicePixelsWherever(t *testing.T) {
	const scale = 1.5
	g := NewCellGrid()
	g.Size = 14
	g.Resize(4, 3)
	g.measure(14, scale)
	top, bottom := color.NRGBA{B: 0xff, A: 0xff}, color.NRGBA{G: 0x80, A: 0xff}
	for y := range 3 {
		row := make([]Cell, 4)
		for x := range row {
			row[x] = Cell{Rune: '▀', FG: top, BG: bottom}
		}
		g.SetRow(y, row)
	}
	for _, at := range []geom.Point{geom.Pt(10.3, 20.7), geom.Pt(0.5, 0.25), geom.Pt(161.7, 29.9)} {
		var p paint.Painter
		func() {
			defer p.Push(paint.Translate(at))()
			g.Paint(&p, gunim.Frame{Scale: scale}, geom.Sz(100, 100), gunim.Children{})
		}()
		whole := func(v float32) bool { return math.Abs(float64(v*scale)-math.Round(float64(v*scale))) < 1e-3 }
		rows := 0
		for _, op := range p.Ops() {
			c, ok := op.(*paint.CellsOp)
			if !ok {
				continue
			}
			rows++
			if c.Cells[0].Pattern == 0 || c.Cells[0].FG != top || c.Cells[0].BG != bottom {
				t.Fatalf("at %v a half block is drawn as %+v", at, c.Cells[0])
			}
			for _, v := range []float32{c.Transform.C + c.At.X, c.Transform.F + c.At.Y, c.Size.W, c.Size.H} {
				if !whole(v) {
					t.Fatalf("at %v a row's cells land at %v device pixels: %v, %v under %v", at, v*scale, c.At, c.Size, c.Transform)
				}
			}
		}
		if rows != 3 {
			t.Fatalf("at %v the grid drew %d rows of cells, want 3", at, rows)
		}
	}
}

// A hidden cursor, as an animation hides it, blinks and moves without
// animating: nothing on screen changes, so no frame is drawn for it.
func TestAHiddenCursorAnimatesNothing(t *testing.T) {
	_, g, run := newCellStage(t)
	for i := range 4 {
		g.SetCursor(Cursor{Col: 3 + i, Row: 2, Visible: false, Blinked: i%2 == 0})
		run(1)
		if g.lit.Active() || g.at.Active() {
			t.Fatalf("step %d: the hidden cursor is animating", i)
		}
	}
	// Shown again, it takes its place and fades as before.
	g.SetCursor(Cursor{Col: 9, Row: 2, Visible: true, Blinked: true})
	run(1)
	if !g.lit.Active() {
		t.Fatal("the shown cursor did not fade for its blink")
	}
}

// paintGrid paints g into p as one frame, at at.
func paintGrid(p *paint.Painter, g *CellGrid, at geom.Point) {
	p.Reset()
	defer p.Push(paint.Translate(at))()
	g.Paint(p, gunim.Frame{Scale: 1}, geom.Sz(400, 300), gunim.Children{})
}

// A frame in which nothing of the grid changed draws it the same, and
// damages nothing; a row that changed damages that row alone; a grid
// that moved is drawn where it went.
func TestAStillGridDrawsTheSameFrame(t *testing.T) {
	_, g, _ := newCellStage(t)
	var p paint.Painter
	paintGrid(&p, g, geom.Point{})
	first := len(p.Ops())
	paintGrid(&p, g, geom.Point{})
	if len(p.Ops()) != first {
		t.Fatalf("the still frame holds %d commands, want %d", len(p.Ops()), first)
	}
	if d := p.Damage(); !d.Empty() {
		t.Fatalf("the still frame damaged %v", d)
	}
	g.SetRow(3, cellsOf("row 3 changed"))
	paintGrid(&p, g, geom.Point{})
	h := g.CellSize().H
	if d := p.Damage(); d.Empty() || d.Min.Y < 3*h-2 || d.Max.Y > 4*h+2 {
		t.Fatalf("changing row 3 damaged %v, want rows %v to %v", d, 3*h, 4*h)
	}
	paintGrid(&p, g, geom.Pt(0, 50))
	for _, op := range p.Ops() {
		if r, ok := op.(*paint.RRectOp); ok && r.Transform.F < 50 {
			t.Fatalf("after the grid moved, a command was drawn at %v", r.Transform)
		}
	}
}

func TestAGridTakesTheNewInkAfterAThemeSwitch(t *testing.T) {
	w, g, run := newCellStage(t)
	light := color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}
	w.RegisterTheme(theme.Make("light", theme.Set(Ink, light)))
	if err := w.Client().SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	run(240)
	for y, line := range g.lines {
		for _, r := range line.drawn.runs {
			if r.c != light {
				t.Fatalf("row %d draws in %v after the switch, want the new ink %v", y, r.c, light)
			}
		}
	}
}
