package widget

import (
	"strconv"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
)

// spill is a text or mask op as it shows, past the clip layers round it: what it is, and where it shows.
type spill struct {
	what string
	r    geom.Rect
}

// shownOps returns the text and mask ops among ops that show, each where the clip layers round it leave it
// showing. It takes the ops' transforms as moves only, and a run of text as reaching from its first glyph to a
// third of its size past its last.
func shownOps(ops []paint.Op) []spill {
	var out []spill
	clips := []geom.Rect{{Min: geom.Pt(-1e9, -1e9), Max: geom.Pt(1e9, 1e9)}}
	meet := func(a, b geom.Rect) geom.Rect {
		return geom.Rect{Min: geom.Pt(max(a.Min.X, b.Min.X), max(a.Min.Y, b.Min.Y)),
			Max: geom.Pt(min(a.Max.X, b.Max.X), min(a.Max.Y, b.Max.Y))}
	}
	for _, op := range ops {
		var what string
		var r geom.Rect
		switch o := op.(type) {
		case *paint.LayerOp:
			c := clips[len(clips)-1]
			if o.Opts.Clip {
				c = meet(c, o.Opts.Bounds.Add(geom.Pt(o.Transform.C, o.Transform.F)))
			}
			clips = append(clips, c)
			continue
		case *paint.LayerEndOp:
			clips = clips[:len(clips)-1]
			continue
		case *paint.TextOp:
			if len(o.Glyphs) == 0 {
				continue
			}
			x, y := o.Transform.C, o.Transform.F
			what = "text"
			r = geom.Rect{Min: geom.Pt(x+o.Glyphs[0].At.X, y), Max: geom.Pt(x+o.Glyphs[len(o.Glyphs)-1].At.X+o.Size/3, y+o.Size)}
			if o.FadeTo > o.FadeFrom {
				// Faded out by FadeTo, nothing past it shows.
				r.Max.X = min(r.Max.X, x+o.FadeTo)
			}
		case *paint.MaskOp:
			what = "mask"
			r = o.Rect.Add(geom.Pt(o.Transform.C, o.Transform.F))
		default:
			continue
		}
		if s := meet(r, clips[len(clips)-1]); s.Max.X > s.Min.X && s.Max.Y > s.Min.Y {
			out = append(out, spill{what, s})
		}
	}
	return out
}

// spillsOutside returns the text and mask ops among ops that show between top and bottom and reach across x
// outside from to to, by more than half a pixel.
func spillsOutside(ops []paint.Op, from, to, top, bottom float32) []spill {
	var out []spill
	for _, s := range shownOps(ops) {
		if s.r.Max.Y <= top || s.r.Min.Y >= bottom {
			continue
		}
		if s.r.Min.X < from-0.5 || s.r.Max.X > to+0.5 {
			out = append(out, s)
		}
	}
	return out
}

// A grid scrolled sideways draws its titles inside the grid, and a sorted title's arrow inside its column, even
// at the right edge of a column that lines its text up at its right.
func TestADataGridsTitlesKeepToTheGridAndTheirColumns(t *testing.T) {
	cols := make([]GridColumn, 6)
	for i := range cols {
		cols[i] = GridColumn{Title: "Column number " + strconv.Itoa(i), Width: 150, End: true, Sort: 1}
	}
	g := NewDataGrid(cols...)
	g.Row = func(int) (GridRow, bool) { return GridRow{}, true }
	g.rows = 10
	_, run := stage(t, &frame{child: g, size: geom.Sz(400, 300)})
	for _, left := range []float32{0, 75, 150, 500} {
		g.left = left
		run(1)
		ops := painted(g, geom.Sz(400, 300))
		bodyW := g.bodyWidth(nil)
		if s := spillsOutside(ops, 0, bodyW, 0, g.header); len(s) > 0 {
			t.Fatalf("scrolled to %v, the header draws %v outside 0 to %v", left, s, bodyW)
		}
		arrows := 0
		for _, m := range masksIn(ops) {
			if s, ok := m.Shape.(icon.Stroke); !ok || s.Icon != icon.ChevronUp {
				continue
			}
			arrows++
			r := m.Rect.Add(geom.Pt(m.Transform.C, 0))
			c := g.columnAt(r.Center().X)
			if x := g.xs[c][0] - g.left; r.Min.X < x || r.Max.X > x+g.xs[c][1] {
				t.Fatalf("scrolled to %v, an arrow spans %v to %v, past column %d's %v to %v", left, r.Min.X, r.Max.X,
					c, x, x+g.xs[c][1])
			}
		}
		if arrows == 0 {
			t.Fatalf("scrolled to %v, the header draws no arrows", left)
		}
	}
}
