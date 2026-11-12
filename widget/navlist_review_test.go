package widget

import (
	"image/color"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

type windowOpened struct{ I int }

// navTexts returns the colours of the text ops among ops whose lines start between top and bottom.
func navTexts(ops []paint.Op, top, bottom float32) []color.NRGBA {
	var out []color.NRGBA
	for _, op := range ops {
		if o, ok := op.(*paint.TextOp); ok && o.Transform.F >= top && o.Transform.F < bottom {
			out = append(out, o.Color)
		}
	}
	return out
}

// laidNav lays n out 220 wide in th and returns the live theme.
func laidNav(n *NavList, th theme.Theme) *theme.Live {
	live := theme.NewLive(th)
	n.Layout(gunim.Loose(geom.Sz(220, 0)), gunim.Frame{Scale: 1, Theme: live}, gunim.Children{})
	return live
}

func TestTheChosenRowTakesItsOwnInkWhereAThemeSetsIt(t *testing.T) {
	ink := color.NRGBA{R: 0x26, G: 0x3b, B: 0x50, A: 0xff}
	items := navPages()
	items[1].Count = 3
	n := NewNavList(items...)
	n.SetSelected(1, nil)
	live := laidNav(n, theme.Make("contrast", theme.Set(NavSelectedInk, ink)))
	ops := paintedIn(n, n.size, live)
	chosen := navTexts(ops, n.top(1), n.top(1)+n.rowH)
	if len(chosen) != 2 || chosen[0] != ink || chosen[1] != ink {
		t.Fatalf("the chosen row's badge and label are drawn in %v, want both in its own ink %v", chosen, ink)
	}
	if other := navTexts(ops, n.top(0), n.top(0)+n.rowH); slices.Contains(other, ink) {
		t.Fatalf("a row not chosen is drawn in %v, want the ink", other)
	}

	// Without the token set, the chosen row keeps the ink, as before.
	live = laidNav(n, Dark())
	ops = paintedIn(n, n.size, live)
	if chosen := navTexts(ops, n.top(1), n.top(1)+n.rowH); !slices.Contains(chosen, Ink.Default()) {
		t.Fatalf("in the dark theme the chosen row is drawn in %v, want the ink", chosen)
	}
}

func TestANavListsActiveRowCountsFromTheFirstRowInView(t *testing.T) {
	n := NewNavList(manyPages(40)...)
	laidNav(n, Dark())
	pitch := n.rowH + n.gap
	paintFrom := func(top float32) {
		var p paint.Painter
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, top, 220, 4*pitch), Opacity: 1, Clip: true})
		n.Paint(&p, gunim.Frame{Scale: 1}, n.size, gunim.Children{})
		end()
	}
	paintFrom(5 * pitch)
	n.cursor = 7
	info := n.Access()
	if info.Active != 3 || info.Parts[info.Active-1].Name != "Page 7" {
		t.Fatalf("scrolled to row 5, with the ring on row 7, Active is %d, want 3: the third part, Page 7", info.Active)
	}
	n.cursor = 2
	if info := n.Access(); info.Active != 0 {
		t.Fatalf("with the ring on row 2, out of view, Active is %d, want 0 for none", info.Active)
	}
}

func TestARowThatActsLeavesThePillWhereItIs(t *testing.T) {
	items := navPages()
	items[2] = NavItem{Label: "Time travel", Icon: icon.History, Trail: icon.ExternalLink, Acts: true}
	n := NewNavList(items...)
	n.SetSelected(1, nil)
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	n.OnActivate = func(i int, _ *gunim.UI) gunim.Intent { return windowOpened{i} }
	w, run := stageNav(t, n)
	run(1)
	pitch := n.rowH + n.gap
	click(w, 100, 2*pitch+10)
	run(30)
	if got := sent(w); n.Selected() != 1 || len(got) != 1 || got[0] != (windowOpened{2}) {
		t.Fatalf("a click on Time travel chose %d and sent %v; want Storage still chosen, and the row activated",
			n.Selected(), got)
	}
	if lag := n.lag.Value(); lag != 0 {
		t.Fatalf("the pill moved by %v", lag)
	}
	// From the keyboard too.
	n.cursor = 2
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if got := sent(w); n.Selected() != 1 || len(got) != 1 || got[0] != (windowOpened{2}) {
		t.Fatalf("Enter on Time travel chose %d and sent %v", n.Selected(), got)
	}
}

func TestANavListsRowsGrowWithLargeText(t *testing.T) {
	n := NewNavList(navPages()...)
	big := TextSize.Default() * 2.25
	live := laidNav(n, theme.Make("large", theme.Set(TextSize, big)))
	line := faceIn(BoldFont, live).Shape("Overview", big)
	if n.rowH < line.Height()+NavRowRoom.Default()-0.01 {
		t.Fatalf("at 225%% a row is %v high for a line %v high", n.rowH, line.Height())
	}
	ops := paintedIn(n, n.size, live)
	if out := spills(ops, n.size, 3); len(out) > 0 {
		t.Fatalf("at 225%% the list draws outside its box: %v", out)
	}
	for i := range len(navPages()) - 1 {
		for _, op := range ops {
			o, ok := op.(*paint.TextOp)
			if !ok {
				continue
			}
			top := o.Transform.F - line.Ascent
			if top >= n.top(i) && top < n.top(i)+n.rowH && top+line.Height() > n.top(i)+n.rowH+0.5 {
				t.Errorf("row %d's text runs past its bottom", i)
			}
		}
	}
}

func TestACountReadsAsItsLabel(t *testing.T) {
	items := navPages()
	items[3].CountLabel = "4 files"
	n := NewNavList(items...)
	laidNav(n, Dark())
	paintedIn(n, n.size, nil)
	if got := n.Access().Parts[3].Description; got != "4 files" {
		t.Fatalf("the count reads as %q, want its label", got)
	}
}
