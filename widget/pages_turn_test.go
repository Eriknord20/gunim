package widget_test

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// seen is how solid a page may be drawn and still not be read: 2% of
// its ink, five steps of 255. A page going out waits at 1% (pageGone)
// before the next comes in, so this leaves room either side.
const seen = 0.02

// sheet is a page that fills its box with a colour of its own, so a
// frame's ops tell the pages apart, and notes where it was drawn and
// the presses it took.
type sheet struct {
	ink     color.NRGBA
	at      geom.Point
	presses int
}

func (s *sheet) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(200, 100))
}

func (s *sheet) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	s.at = p.Transform().Apply(geom.Point{})
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(s.ink))
}

func (s *sheet) Handle(e input.Event, _ *gunim.UI) bool {
	if _, ok := e.(input.PointerDown); ok {
		s.presses++
		return true
	}
	return false
}

func newSheets(n int) []*sheet {
	out := make([]*sheet, n)
	for i := range out {
		out[i] = &sheet{ink: color.NRGBA{R: uint8(10 + i), G: 0x40, B: 0x80, A: 0xff}}
	}
	return out
}

// alphas returns how solid the last frame drew each sheet, through the
// layers round it; a sheet not drawn is 0.
func alphas(ops []paint.Op, sheets []*sheet) []float32 {
	out := make([]float32, len(sheets))
	stack := []float32{1}
	for _, op := range ops {
		at := stack[len(stack)-1]
		switch o := op.(type) {
		case *paint.LayerOp:
			stack = append(stack, at*o.Opts.Opacity)
		case *paint.LayerEndOp:
			stack = stack[:len(stack)-1]
		case *paint.RRectOp:
			for i, s := range sheets {
				if o.Fill.Solid == s.ink {
					out[i] = max(out[i], at)
				}
			}
		}
	}
	return out
}

// turnStage mounts pages showing sheets[0] at (0, 0) of a 400×300
// window and returns a UI to show pages through and a function that
// runs one 240 Hz frame. Frames that short keep a page's honest motion
// small from one to the next, so a jump stands out.
func turnStage(t *testing.T, sheets []*sheet, inTurn bool) (*widget.Pages, *gunim.UI, *gunim.Window, func()) {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(400, 300), nil)
	w.PaintWhenLooked(false)
	var pages *widget.Pages
	var ui *gunim.UI
	gunim.RegisterView(w, "pages",
		func(struct{}) *widget.Pages {
			pages = widget.NewPages(sheets[0])
			pages.InTurn = inTurn
			return pages
		},
		func(_ *widget.Pages, _ struct{}, u *gunim.UI) { ui = u })
	if err := w.Client().Mount(gunim.Root, "pages", "pages", struct{}{}, "pages"); err != nil {
		t.Fatal(err)
	}
	frame := func() { w.Frame(time.Second / 240) }
	for range 4 {
		frame()
	}
	if err := w.Client().Publish("pages", struct{}{}); err != nil {
		t.Fatal(err)
	}
	frame()
	if ui == nil {
		t.Fatal("the view was never updated")
	}
	return pages, ui, w, frame
}

// watch checks one frame: no two sheets drawn at once above seen, and
// none whose alpha or place jumped since the frame before.
type watch struct {
	t      *testing.T
	w      *gunim.Window
	sheets []*sheet
	alpha  []float32
	at     []geom.Point
	frame  int
}

func newWatch(t *testing.T, w *gunim.Window, sheets []*sheet) *watch {
	wt := &watch{t: t, w: w, sheets: sheets}
	wt.alpha = alphas(w.Offscreen().Ops(), sheets)
	for _, s := range sheets {
		wt.at = append(wt.at, s.at)
	}
	return wt
}

func (wt *watch) check() []float32 {
	wt.t.Helper()
	wt.frame++
	a := alphas(wt.w.Offscreen().Ops(), wt.sheets)
	shown := -1
	for i, v := range a {
		if v > seen {
			if shown >= 0 {
				wt.t.Fatalf("frame %d: sheets %d and %d drawn at once, %.3f and %.3f", wt.frame, shown, i, a[shown], v)
			}
			shown = i
		}
		// At 240 Hz a page fades by under 0.1 a frame; a pop is far more.
		if d := v - wt.alpha[i]; d > 0.15 || d < -0.15 {
			wt.t.Fatalf("frame %d: sheet %d's alpha jumped from %.3f to %.3f", wt.frame, i, wt.alpha[i], v)
		}
		// It slides under 2.5 px a frame; a turn of side is up to 48.
		if v > 0 && wt.alpha[i] > 0 {
			if d := wt.sheets[i].at.Sub(wt.at[i]); d.X*d.X+d.Y*d.Y > 3*3 {
				wt.t.Fatalf("frame %d: sheet %d jumped from %v to %v", wt.frame, i, wt.at[i], wt.sheets[i].at)
			}
		}
	}
	wt.alpha = a
	for i, s := range wt.sheets {
		wt.at[i] = s.at
	}
	return a
}

// Taking turns, the page going fades and slides up and out before the
// next comes in from below, so no frame shows both; and the change takes
// about as long as the cross-fade.
func TestPagesInTurnNeverDrawTwoPagesAtOnce(t *testing.T) {
	took := map[bool]int{}
	for _, inTurn := range []bool{false, true} {
		sheets := newSheets(2)
		pages, ui, w, frame := turnStage(t, sheets, inTurn)
		wt := newWatch(t, w, sheets)
		pages.Show(sheets[1], geom.Pt(0, 1), ui)
		sawOldGo, sawNewCome := false, false
		for i := range 240 {
			frame()
			var a []float32
			if inTurn {
				a = wt.check()
			} else {
				a = alphas(w.Offscreen().Ops(), sheets)
			}
			if inTurn && a[0] > 0 && a[0] < 1 {
				if sheets[0].at.Y > 0 {
					t.Fatalf("frame %d: the page going moved down to %v, not up and away", i, sheets[0].at)
				}
				sawOldGo = sawOldGo || sheets[0].at.Y < -1
			}
			if a[1] > 0 && a[1] < 1 {
				if sheets[1].at.Y < 0 {
					t.Fatalf("frame %d: the page coming is above its place, at %v", i, sheets[1].at)
				}
				sawNewCome = sawNewCome || sheets[1].at.Y > 1
			}
			if a[1] >= 0.99 && took[inTurn] == 0 {
				took[inTurn] = i + 1
			}
		}
		if inTurn && !sawOldGo {
			t.Fatal("the page going faded where it stood, not sliding away")
		}
		if !sawNewCome {
			t.Fatal("the page coming did not slide in from below")
		}
		if pages.Page() != sheets[1] {
			t.Fatalf("the page shown is %v", pages.Page())
		}
		if _, ok := ui.Bounds(sheets[0]); ok {
			t.Fatal("the page before is still mounted")
		}
		if a := alphas(w.Offscreen().Ops(), sheets); a[0] != 0 || a[1] != 1 || sheets[1].at != (geom.Point{}) {
			t.Fatalf("at rest the pages are drawn %v, the shown one at %v", a, sheets[1].at)
		}
	}
	t.Logf("frames at 240 Hz to the new page: cross-fade %d, in turn %d", took[false], took[true])
	if took[false] == 0 || took[true] == 0 || float32(took[true]) > 1.5*float32(took[false]) {
		t.Fatalf("frames at 240 Hz to the new page: cross-fade %d, in turn %d", took[false], took[true])
	}
}

// Three changes a few frames apart end on the last page, at rest, with
// the others gone, and never two pages drawn at once on the way.
func TestPagesInTurnEndOnTheLastOfFastChanges(t *testing.T) {
	for _, gap := range []int{1, 6, 14, 24} {
		sheets := newSheets(4)
		pages, ui, w, frame := turnStage(t, sheets, true)
		wt := newWatch(t, w, sheets)
		for i := 1; i < 4; i++ {
			pages.Show(sheets[i], geom.Pt(0, 1), ui)
			for range gap {
				frame()
				wt.check()
			}
		}
		for range 240 {
			frame()
			wt.check()
		}
		if pages.Page() != sheets[3] {
			t.Fatalf("gap %d: the page shown is %v", gap, pages.Page())
		}
		for i := range 3 {
			if _, ok := ui.Bounds(sheets[i]); ok {
				t.Fatalf("gap %d: sheet %d is still mounted", gap, i)
			}
		}
		if a := alphas(w.Offscreen().Ops(), sheets); a[3] != 1 || a[0]+a[1]+a[2] != 0 || sheets[3].at != (geom.Point{}) {
			t.Fatalf("gap %d: at rest the sheets are drawn %v, the last at %v", gap, a, sheets[3].at)
		}
		if !w.Still() {
			t.Fatalf("gap %d: still animating at rest", gap)
		}
	}
}

// Asked for again while it goes out, a page turns and comes back from
// where it is, at whatever point of the change and from whatever side
// it is asked to come.
func TestPagesInTurnGoBackMidChange(t *testing.T) {
	for _, back := range []geom.Point{geom.Pt(0, -1), geom.Pt(0, 1), geom.Pt(1, 0)} {
		for _, after := range []int{2, 10, 20, 30, 45} {
			goBack(t, back, after)
		}
	}
}

// goBack shows the second sheet, and after so many frames the first
// again, coming from back, checking every frame.
func goBack(t *testing.T, back geom.Point, after int) {
	t.Helper()
	sheets := newSheets(2)
	pages, ui, w, frame := turnStage(t, sheets, true)
	wt := newWatch(t, w, sheets)
	pages.Show(sheets[1], geom.Pt(0, 1), ui)
	for range after {
		frame()
		wt.check()
	}
	pages.Show(sheets[0], back, ui)
	for range 240 {
		frame()
		wt.check()
	}
	if pages.Page() != sheets[0] {
		t.Fatalf("back from %v after %d frames: the page shown is %v", back, after, pages.Page())
	}
	if _, ok := ui.Bounds(sheets[1]); ok {
		t.Fatalf("back from %v after %d frames: the page left is still mounted", back, after)
	}
	if a := alphas(w.Offscreen().Ops(), sheets); a[0] != 1 || a[1] != 0 || sheets[0].at != (geom.Point{}) {
		t.Fatalf("back from %v after %d frames: at rest the sheets are drawn %v, the first at %v", back, after, a, sheets[0].at)
	}
}

// The page going out takes no press, even where it is still drawn.
func TestPagesInTurnThePageGoingTakesNoInput(t *testing.T) {
	sheets := newSheets(2)
	pages, ui, w, frame := turnStage(t, sheets, true)
	pages.Show(sheets[1], geom.Pt(0, 1), ui)
	frame()
	frame()
	if a := alphas(w.Offscreen().Ops(), sheets); a[0] <= seen {
		t.Fatalf("the page going is already gone: %v", a)
	}
	w.Input(input.PointerDown{Pos: geom.Pt(50, 50), Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(50, 50), Time: time.Now()})
	frame()
	if sheets[0].presses != 0 {
		t.Fatal("the page going took a press")
	}
}

// Going back to the page going out, without InTurn, turns it round where
// it stands as it fades back in.
func TestPagesGoBackToThePageGoing(t *testing.T) {
	sheets := newSheets(2)
	pages, ui, w, frame := turnStage(t, sheets, false)
	pages.Show(sheets[1], geom.Pt(0, 1), ui)
	for range 8 {
		frame()
	}
	was := alphas(w.Offscreen().Ops(), sheets)[0]
	pages.Show(sheets[0], geom.Pt(0, -1), ui)
	frame()
	if a := alphas(w.Offscreen().Ops(), sheets)[0]; a-was > 0.15 || was-a > 0.15 || sheets[0].at != (geom.Point{}) {
		t.Fatalf("turned round, the page went from %.3f to %.3f, at %v", was, a, sheets[0].at)
	}
	for range 240 {
		frame()
	}
	if pages.Page() != sheets[0] || alphas(w.Offscreen().Ops(), sheets)[0] != 1 {
		t.Fatal("the page did not come back")
	}
	if _, ok := ui.Bounds(sheets[1]); ok {
		t.Fatal("the page left is still mounted")
	}
}
