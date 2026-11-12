package widget

import (
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

func TestACheckedMenuItemShowsACheck(t *testing.T) {
	m := NewMenu([]MenuItem{{Label: "Hidden files", Checked: true}, {Label: "Extensions"}, {Label: "Preview", Checked: true}})
	w, _ := stage(t, Row(m))
	ms := maskOps(w.Offscreen())
	if len(ms) != 2 {
		t.Fatalf("the menu drew %d marks, want a check on each of two items", len(ms))
	}
	s := IconSize.Default()
	for k, i := range []int{0, 2} {
		if got := strokeOf(t, ms[k]).Icon; got != icon.Check {
			t.Fatalf("item %d is marked with %s, want a check", i, got.Name)
		}
		// In the middle of the tick's room, 4 px clear of the row's padding at the edge and of the text after it.
		want := geom.Rc(m.card.Min.X+MenuRowPadding.Default()+4, m.rowY(i)+(m.row-s)/2, s, s)
		if ms[k].Rect != want || ms[k].Color != Ink.Default() {
			t.Errorf("item %d's check is at %v in %v, want at %v in the ink", i, ms[k].Rect, ms[k].Color, want)
		}
	}
}

func TestADropdownsChevronIsLucidesAndTurnsAsItOpens(t *testing.T) {
	d := NewDropdown(Labels("One", "Two"))
	w, run := stage(t, Row(d))
	ms := maskOps(w.Offscreen())
	if len(ms) != 1 || strokeOf(t, ms[0]).Icon != icon.ChevronDown {
		t.Fatalf("the drop-down drew %d marks, want a chevron", len(ms))
	}
	s, pad := IconSize.Default(), FieldPadding.Default()
	c := geom.Pt(d.size.W-pad-chevron/2, d.size.H/2)
	if want := geom.Rc(c.X-s/2, c.Y-s/2, s, s); ms[0].Rect != want || ms[0].Transform.A != 1 {
		t.Fatalf("the chevron is at %v turned %v, want upright at %v", ms[0].Rect, ms[0].Transform.A, want)
	}
	click(w, d.size.W/2, d.size.H/2)
	run(60)
	m := maskOps(w.Offscreen())[0]
	if m.Transform.A > -0.99 {
		t.Fatalf("open, the chevron is turned to %v, want upside down", m.Transform.A)
	}
	if p := m.Transform.Apply(m.Rect.Center()); p.Sub(c).X*p.Sub(c).X+p.Sub(c).Y*p.Sub(c).Y > 0.01 {
		t.Fatalf("open, the chevron is centred on %v, want %v", p, c)
	}
}

func TestSortedColumnsShowAChevron(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name", Width: 120, Sort: 1}, GridColumn{Title: "Size", Width: 120, Sort: -1},
		GridColumn{Title: "Kind", Width: 120})
	w, _ := stage(t, &frame{child: g, size: geom.Sz(400, 200)})
	got := iconsDrawn(t, w)
	if len(got) != 2 || got[0] != icon.ChevronUp || got[1] != icon.ChevronDown {
		t.Fatalf("the grid's header drew %v, want a chevron up and one down", got)
	}
	if s := maskOps(w.Offscreen())[0].Rect.Size().W; abs32(s-IconSize.Default()*sortScale) > 0.01 {
		t.Fatalf("the sort chevron is %v wide, want %v", s, IconSize.Default()*sortScale)
	}

	tb := NewTable(TableColumn{Title: "Name", Width: 120}, TableColumn{Title: "Size", Width: 120})
	tb.SetSorted(0, false)
	w, run := stage(t, &frame{child: tb, size: geom.Sz(400, 200)})
	if got := iconsDrawn(t, w); len(got) != 1 || got[0] != icon.ChevronUp {
		t.Fatalf("the table sorted up drew %v, want a chevron up", got)
	}
	tb.SetSorted(0, true)
	w.Input(input.PointerMove{Pos: geom.Pt(1, 1)})
	run(1)
	if got := iconsDrawn(t, w); len(got) != 1 || got[0] != icon.ChevronDown {
		t.Fatalf("the table sorted down drew %v, want a chevron down", got)
	}
}

func TestAChipsCrossIsLucidesX(t *testing.T) {
	c := NewChip("", "Large")
	w, _ := stage(t, Row(c))
	ms := maskOps(w.Offscreen())
	if len(ms) != 1 || strokeOf(t, ms[0]).Icon != icon.X {
		t.Fatalf("the chip drew %d marks, want an X", len(ms))
	}
	h := ChipHeight.Default()
	if at := ms[0].Rect.Center(); abs32(at.X-c.crossX) > 0.01 || abs32(at.Y-h/2) > 0.01 {
		t.Fatalf("the X is centred on %v, want on the cross's place, %v", at, geom.Pt(c.crossX, h/2))
	}
	if s := strokeOf(t, ms[0]); abs32(s.Width*ms[0].Rect.Size().W-IconStroke.Default()*IconSize.Default()) > 0.01 {
		t.Fatalf("the X's strokes are %v wide on its grid, want as thick on screen as an icon's", s.Width)
	}
}

func TestAClosableColumnShowsAnXUnderThePointer(t *testing.T) {
	g := NewDataGrid(GridColumn{Title: "Name", Width: 120}, GridColumn{Title: "Kind", Width: 120, Closable: true})
	w, run := stage(t, &frame{child: g, size: geom.Sz(400, 200)})
	if got := iconsDrawn(t, w); len(got) != 0 {
		t.Fatalf("with the pointer away the header drew %v", got)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(180, g.header/2)})
	run(1)
	if got := iconsDrawn(t, w); len(got) != 1 || got[0] != icon.X {
		t.Fatalf("with the pointer on the closable column the header drew %v, want an X", got)
	}
}

func TestADropsEffectShowsLucidesSign(t *testing.T) {
	for _, c := range []struct {
		e  DropEffect
		ic *icon.Icon
	}{{DropMove, icon.ArrowRight}, {DropCopy, icon.Plus}, {DropLink, icon.Link}, {DropRefused, icon.Ban}} {
		var p paint.Painter
		paintEffect(&p, nil, c.e, geom.Pt(20, 20), ButtonStrongInk.Default())
		ops := p.Ops()
		if len(ops) != 1 {
			t.Fatalf("effect %d drew %d ops, want one sign", c.e, len(ops))
		}
		m, ok := ops[0].(*paint.MaskOp)
		if !ok || strokeOf(t, m).Icon != c.ic {
			t.Fatalf("effect %d drew %v, want %s", c.e, ops[0], c.ic.Name)
		}
		if m.Rect.Center() != geom.Pt(20, 20) || m.Rect.Size().W != effectSize {
			t.Fatalf("effect %d's sign is at %v, want %v square centred on the dot", c.e, m.Rect, effectSize)
		}
	}
}
