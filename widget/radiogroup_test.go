package widget

import (
	"fmt"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

type choiceMade struct{ I int }

type chooseRadio struct{ I int }

// restoreChoices are the two choices of Hjortron's restore dialog.
func restoreChoices() []RadioItem {
	return []RadioItem{
		{Label: "Put them back in Music projects",
			Detail: "Your PCs get the old versions. The newer ones stay in the history."},
		{Label: "Copy them to a folder on this PC", Detail: "Your libraries stay as they are."},
		{Label: "Do nothing yet"},
	}
}

// stageRadio mounts g 360 wide, with a patch that chooses from the view.
func stageRadio(t *testing.T, g *RadioGroup) (w *gunim.Window, run func(int)) {
	t.Helper()
	w, run = stage(t, &frame{child: Column(NewSized(g, 360, 0)), size: geom.Sz(500, 600)})
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c chooseRadio, u *gunim.UI) { g.SetSelected(c.I, u) })
	return w, run
}

func TestARadioGroupChoosesByClickAndTheDotSpringsIn(t *testing.T) {
	g := NewRadioGroup(restoreChoices()...)
	g.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return choiceMade{i} }
	w, run := stageRadio(t, g)
	run(1)
	top, h := g.span(1)
	click(w, 100, top+h/2)
	run(1)
	if got := sent(w); g.Selected() != 1 || len(got) != 1 || got[0] != (choiceMade{1}) {
		t.Fatalf("a click on the second card chose %d and sent %v", g.Selected(), got)
	}
	// The new dot grows from nothing and the old one empties, on every frame, never back past where it began.
	lastOff := g.off.Value()
	for f := range 60 {
		if v := g.off.Value(); v > lastOff+0.001 || v < -0.01 {
			t.Fatalf("frame %d: the card left is %v chosen, after %v; want it emptying", f, v, lastOff)
		}
		lastOff = g.off.Value()
		if v := g.on.Value(); v < 0 {
			t.Fatalf("frame %d: the chosen dot is at %v", f, v)
		}
		run(1)
	}
	if g.on.Value() < 0.99 || g.off.Value() > 0.01 {
		t.Fatalf("settled, the chosen dot is %v and the one left %v; want 1 and 0", g.on.Value(), g.off.Value())
	}
	// A click in the gap between cards chooses nothing.
	click(w, 100, top-RadioGap.Default()/2)
	run(1)
	if got := sent(w); g.Selected() != 1 || len(got) != 0 {
		t.Fatalf("a click between cards chose %d and sent %v", g.Selected(), got)
	}
}

func TestARadioGroupChoosesWithTheArrows(t *testing.T) {
	b, g := NewButton("B"), NewRadioGroup(restoreChoices()...)
	g.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return choiceMade{i} }
	w, run := stage(t, &frame{child: Column(b, NewSized(g, 360, 0)), size: geom.Sz(500, 600)})
	focused := focusProbe(t, w, run)
	tab(w, run, 0)
	tab(w, run, 0)
	if f := focused(); f != g {
		t.Fatalf("two Tabs put the keyboard on %T, want the group", f)
	}
	key(w, run, input.KeyDown, 0)
	key(w, run, input.KeyRight, 0)
	key(w, run, input.KeyDown, 0)
	if got := sent(w); g.Selected() != 2 || len(got) != 2 {
		t.Fatalf("three steps down chose %d and sent %v; want the last, and nothing past it", g.Selected(), got)
	}
	key(w, run, input.KeyHome, 0)
	run(60)
	top, h := g.span(0)
	if g.Selected() != 0 || g.ring.Value() < 0.99 || g.ringTop.Value() != top || g.ringH.Value() != h {
		t.Fatalf("Home chose %d with the ring at %v, %v high, %v grown; want the first card's %v, %v",
			g.Selected(), g.ringTop.Value(), g.ringH.Value(), g.ring.Value(), top, h)
	}
	key(w, run, input.KeyUp, 0)
	if got := sent(w); g.Selected() != 0 || len(got) != 1 {
		t.Fatalf("Up from the first chose %d and sent %v; want it to stay", g.Selected(), got)
	}
}

func TestSettingARadioGroupsChoiceSendsNothing(t *testing.T) {
	g := NewRadioGroup(restoreChoices()...)
	g.SetSelected(2, nil)
	g.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return choiceMade{i} }
	w, run := stageRadio(t, g)
	if g.on.Value() != 1 || g.was != -1 {
		t.Fatalf("set before the first layout, the dot is at %v with %d left; want it there at once", g.on.Value(), g.was)
	}
	if err := w.Client().Patch("stage", chooseRadio{0}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if got := sent(w); g.Selected() != 0 || len(got) != 0 {
		t.Fatalf("SetSelected(0) chose %d and sent %v", g.Selected(), got)
	}
	if v := g.on.Value(); v <= 0 || v >= 1 {
		t.Fatalf("a frame after SetSelected(0) the dot is at %v, want on its way in", v)
	}
}

func TestARadioGroupWrapsItsDetailAndDrawsInsideItsBox(t *testing.T) {
	items := restoreChoices()
	items[0].Detail += " A longer line of explanation wraps onto a second line, and the card grows to hold it."
	g := NewRadioGroup(items...)
	_, run := stageRadio(t, g)
	run(1)
	if g.details[0].p.Size.H <= g.details[1].p.Size.H {
		t.Fatalf("the long explanation is %v high and the short one %v; want it wrapped onto more lines",
			g.details[0].p.Size.H, g.details[1].p.Size.H)
	}
	if _, h0 := g.span(0); h0 <= g.size.H/4 {
		t.Fatalf("the card with the long explanation is %v high in a group %v high", h0, g.size.H)
	}
	if out := spills(painted(g, g.size), g.size, 3); len(out) > 0 {
		t.Fatalf("the group draws outside its box: %v", out)
	}
}

func TestARadioGroupReadsAsCheckableChoices(t *testing.T) {
	g := NewRadioGroup(restoreChoices()...)
	g.SetSelected(1, nil)
	_, run := stageRadio(t, g)
	run(1)
	info := g.Access()
	if info.Role != access.RoleGroup || len(info.Parts) != 3 {
		t.Fatalf("the group reads as %v with %d parts, want a group of 3", info.Role, len(info.Parts))
	}
	for i, part := range info.Parts {
		if part.Role != access.RoleRadioButton {
			t.Fatalf("choice %d reads as %v, want a radio button", i, part.Role)
		}
	}
	p := info.Parts[1]
	if p.Name != "Copy them to a folder on this PC" || p.Description != "Your libraries stay as they are." ||
		!p.State.Has(access.StateChecked) {
		t.Fatalf("the second choice reads as %+v, want its label, its explanation, and checked", p)
	}
	if info.Parts[0].State.Has(access.StateChecked) {
		t.Fatal("the first choice reads as checked too")
	}
}

func TestADisabledRadioGroupTakesNoClicks(t *testing.T) {
	g := NewRadioGroup(restoreChoices()...)
	g.Disabled = true
	g.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return choiceMade{i} }
	w, run := stageRadio(t, g)
	run(1)
	top, h := g.span(1)
	click(w, 100, top+h/2)
	run(1)
	if got := sent(w); g.Selected() != 0 || len(got) != 0 {
		t.Fatalf("a click on a disabled group chose %d and sent %v", g.Selected(), got)
	}
}

// manyChoices returns n choices.
func manyChoices(n int) []RadioItem {
	items := make([]RadioItem, n)
	for i := range items {
		items[i] = RadioItem{Label: fmt.Sprintf("Choice %d", i), Detail: "What it does."}
	}
	return items
}

// paintRadio lays g out and paints it through a clip 400 high, as a scroll shows it, and returns the ops.
func paintRadio(g *RadioGroup, p *paint.Painter) []paint.Op {
	g.Layout(gunim.Loose(geom.Sz(360, 0)), gunim.Frame{Scale: 1}, gunim.Children{})
	p.Reset()
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, 360, 400), Opacity: 1, Clip: true})
	g.Paint(p, gunim.Frame{Scale: 1}, g.size, gunim.Children{})
	end()
	return p.Ops()
}

func TestAHugeRadioGroupPaintsOnlyTheChoicesInView(t *testing.T) {
	var p paint.Painter
	few := len(paintRadio(NewRadioGroup(manyChoices(100)...), &p))
	many := len(paintRadio(NewRadioGroup(manyChoices(100_000)...), &p))
	if few != many {
		t.Fatalf("100 choices paint %d ops and 100 000 paint %d, want the same: only the choices in view", few, many)
	}
}

func BenchmarkRadioGroup100000(b *testing.B) {
	g := NewRadioGroup(manyChoices(100_000)...)
	var p paint.Painter
	paintRadio(g, &p)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		paintRadio(g, &p)
	}
}
