package widget

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// barStage mounts a dialog titled title, with a spot for its body and set up by set, lets it settle, and returns
// the spot, the dialog's panel and the ops of the last frame.
func barStage(t *testing.T, title string, set func(*Dialog)) (*spot, geom.Rect, []paint.Op) {
	d, body, w := barWindow(t, title, set)
	panel := d.panel(geom.Sz(800, 600), gunim.Frame{})
	return body, panel, w.Offscreen().Ops()
}

// barWindow is barStage's window.
func barWindow(t *testing.T, title string, set func(*Dialog)) (*Dialog, *spot, *gunim.Window) {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	body := newSpot(100, 40)
	gunim.RegisterView(w, "dialog", func(title string) *Dialog {
		d = NewDialog(title)
		d.Body = body
		set(d)
		return d
	}, func(d *Dialog, title string, _ *gunim.UI) { d.Title = title })
	if err := w.Client().Mount(gunim.Root, "dialog", "dialog", title); err != nil {
		t.Fatal(err)
	}
	for range 120 {
		w.Frame(time.Second / 60)
	}
	return d, body, w
}

// barFills returns the rectangles painted in the title bar's fill, in window space.
func barFills(ops []paint.Op) []geom.Rect {
	var out []geom.Rect
	for _, op := range ops {
		if r, ok := op.(*paint.RRectOp); ok && r.Fill.Solid == MenubarFill.Default() && r.Fill.Gradient == nil {
			out = append(out, geom.Rect{Min: r.Transform.Apply(r.Rect.Min), Max: r.Transform.Apply(r.Rect.Max)})
		}
	}
	return out
}

// textsIn counts the text ops in ops whose tops lie between top and bottom.
func textsIn(ops []paint.Op, top, bottom float32) int {
	n := 0
	for _, op := range ops {
		if o, ok := op.(*paint.TextOp); ok && o.Transform.F >= top && o.Transform.F < bottom {
			n++
		}
	}
	return n
}

func TestADialogShowsItsTitleInATitleBar(t *testing.T) {
	body, panel, ops := barStage(t, "Rename the file", func(*Dialog) {})
	bar, pad := max(TitleBarCompactHeight.Default(), 0.8*ButtonHeight.Default()), DialogPadding.Default()
	fills := barFills(ops)
	if len(fills) == 0 || near(fills[0].Min.Y, panel.Min.Y) != nil || fills[0].Min.X != panel.Min.X || fills[0].Max.X != panel.Max.X {
		t.Fatalf("the title bar's fill is at %v, want across the top of the panel %v", fills, panel)
	}
	if n := textsIn(ops, panel.Min.Y, panel.Min.Y+bar); n != 1 {
		t.Fatalf("the title bar holds %d texts, want the title", n)
	}
	if err := near(body.at.Y, panel.Min.Y+bar+pad); err != nil {
		t.Fatalf("the body starts at %v, want under the title bar: %v", body.at.Y, err)
	}
}

func TestADialogWithoutATitleHasNoTitleBar(t *testing.T) {
	body, panel, ops := barStage(t, "", func(*Dialog) {})
	if fills := barFills(ops); len(fills) != 0 {
		t.Fatalf("a dialog with no title painted a title bar at %v", fills)
	}
	if err := near(body.at.Y, panel.Min.Y+DialogPadding.Default()); err != nil {
		t.Fatalf("the body starts at %v, want at the top of the panel: %v", body.at.Y, err)
	}
}

func TestADialogsHostCanLeaveTheTitleBarOut(t *testing.T) {
	body, panel, ops := barStage(t, "me@server asks", func(d *Dialog) { d.NoTitleBar, d.Icon = true, icon.LogIn })
	if fills := barFills(ops); len(fills) != 0 {
		t.Fatalf("with NoTitleBar, the dialog painted a title bar at %v", fills)
	}
	if n := textsIn(ops, panel.Min.Y, body.at.Y); n != 0 {
		t.Fatalf("with NoTitleBar, the dialog drew %d texts above its body, want no title", n)
	}
	if len(maskOps(opList(ops))) != 0 {
		t.Fatalf("with NoTitleBar, the dialog drew %d icons", len(maskOps(opList(ops))))
	}
	if err := near(body.at.Y, panel.Min.Y+DialogPadding.Default()); err != nil {
		t.Fatalf("the body starts at %v, want at the top of the panel: %v", body.at.Y, err)
	}
}

// A dialog's title can change after it opens, and the bar shows the new one; set to nothing, the bar goes.
func TestADialogsTitleBarFollowsSetTitle(t *testing.T) {
	d, body, w := barWindow(t, "One", func(*Dialog) {})
	set := func(title string) {
		if err := w.Client().Update("dialog", title); err != nil {
			t.Fatal(err)
		}
		w.Frame(time.Second / 60)
	}
	set("A longer title")
	if d.titleText.s != "A longer title" || len(barFills(w.Offscreen().Ops())) == 0 {
		t.Fatalf("after SetTitle the bar shows %q", d.titleText.s)
	}
	set("")
	panel := d.panel(geom.Sz(800, 600), gunim.Frame{})
	if fills := barFills(w.Offscreen().Ops()); len(fills) != 0 || near(body.at.Y, panel.Min.Y+DialogPadding.Default()) != nil {
		t.Fatalf("with the title gone, the bar is at %v and the body at %v", fills, body.at.Y)
	}
}

// opList holds ops, for helpers that read a window's.
type opList []paint.Op

func (o opList) Ops() []paint.Op { return o }

// near returns an error when a is more than half a pixel from b.
func near(a, b float32) error {
	if d := a - b; d < -0.5 || d > 0.5 {
		return errNear{a, b}
	}
	return nil
}

type errNear struct{ a, b float32 }

func (e errNear) Error() string { return "off by more than half a pixel" }

// compactTitleBar shows title in a compact title bar with no buttons, as a small window's, and returns the ops of
// the window and the bar's height.
func compactTitleBar(t *testing.T, title string) (ops []paint.Op, height float32) {
	t.Helper()
	var bar *TitleBar
	gunim.RegisterTitleBar(func() gunim.TitleBar {
		bar = NewTitleBar("")
		bar.Compact, bar.NoMinimize, bar.NoMaximize, bar.NoClose = true, true, true, true
		return bar
	})
	t.Cleanup(func() { gunim.RegisterTitleBar(func() gunim.TitleBar { return NewTitleBar("") }) })
	w := gunimtest.New(t, geom.Sz(600, 300), nil)
	w.PaintWhenLooked(false)
	w.MakeChromeless(true)
	sp := newSpot(100, 50)
	type do struct{}
	gunim.RegisterView(w, "app", func(struct{}) gunim.Node { return sp }, nil)
	gunim.RegisterPatch(w, "app", func(_ gunim.Node, _ do, u *gunim.UI) {
		u.SetTitle(title)
		r, _ := u.Bounds(sp)
		height = r.Min.Y
	})
	if err := w.Client().Mount(gunim.Root, "app", "app", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if err := w.Client().Patch("app", do{}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	return w.Offscreen().Ops(), height
}

// titleOp returns the one text op whose top lies between top and bottom.
func titleOp(t *testing.T, ops []paint.Op, top, bottom float32) *paint.TextOp {
	t.Helper()
	var found []*paint.TextOp
	for _, op := range ops {
		if o, ok := op.(*paint.TextOp); ok && o.Transform.F >= top && o.Transform.F < bottom {
			found = append(found, o)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d texts lie between %v and %v, want the title alone", len(found), top, bottom)
	}
	return found[0]
}

// A dialog's title bar is a compact window title bar's twin: in its fill, with the title in the same size, face and
// colour, in the middle of it, and no line under it. Where the theme's buttons stand taller, the bar stands taller
// with them, to four fifths of a button.
func TestADialogsTitleBarMatchesACompactWindowTitleBar(t *testing.T) {
	ops, windowBar := compactTitleBar(t, "Rename the file")
	win := titleOp(t, ops, 0, windowBar)

	_, panel, dops := barStage(t, "Rename the file", func(*Dialog) {})
	fills := barFills(dops)
	if len(fills) == 0 {
		t.Fatal("the dialog painted no title bar")
	}
	// The first fill runs below the bar by the panel's corners, and the panel's own fill covers that part again.
	if h := TitleBarCompactHeight.Default(); h != windowBar {
		t.Fatalf("a compact window bar is %v tall, the window's %v", h, windowBar)
	}
	dialogBar := max(windowBar, 0.8*ButtonHeight.Default())
	dlg := titleOp(t, dops, panel.Min.Y, panel.Min.Y+max(windowBar, 0.8*ButtonHeight.Default()))
	if dlg.Size != win.Size || dlg.Color != win.Color || dlg.Glyphs[0].Face != win.Glyphs[0].Face {
		t.Fatalf("the dialog's title is %v in %v, face %d, and the window's %v in %v, face %d",
			dlg.Size, dlg.Color, dlg.Glyphs[0].Face, win.Size, win.Color, win.Glyphs[0].Face)
	}
	if err := near(dlg.Transform.F-panel.Min.Y, win.Transform.F+(dialogBar-windowBar)/2); err != nil {
		t.Fatalf("the dialog's title sits %v down its bar, and the window's %v: %v", dlg.Transform.F-panel.Min.Y, win.Transform.F, err)
	}
	for _, op := range dops {
		r, ok := op.(*paint.RRectOp)
		if !ok || r.Fill.Solid != DialogBorder.Default() {
			continue
		}
		if top := r.Transform.Apply(r.Rect.Min).Y; top > panel.Min.Y+1 && top < panel.Min.Y+windowBar+2 {
			t.Fatalf("a line in the border's colour lies under the title bar at %v", top)
		}
	}
}

// In gunim's own themes the title bar's fill is darker than the dialog's, so the bar reads as apart from the body
// with no line under it.
func TestTheTitleBarFillIsDarkerThanADialogs(t *testing.T) {
	for _, th := range []theme.Theme{Dark(), Light()} {
		live := theme.NewLive(th)
		bar, panel := lightness(MenubarFill.Get(live)), lightness(DialogFill.Get(live))
		if panel-bar < 4 {
			t.Errorf("in the %s theme the title bar's fill is %.1f light and the dialog's %.1f, want it darker", th.Name, bar, panel)
		}
	}
}

// lightness is c's lightness, from 0 to 255, weighed as the eye does.
func lightness(c color.NRGBA) float32 {
	return 0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B)
}
