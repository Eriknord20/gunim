package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// layRadio lays g out width wide under live, and paints it through a clip 400 high from top.
func layRadio(g *RadioGroup, live *theme.Live, width, top float32) {
	f := gunim.Frame{Scale: 1, Theme: live}
	g.Layout(gunim.Loose(geom.Sz(width, 0)), f, gunim.Children{})
	var p paint.Painter
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, top, width, 400), Opacity: 1, Clip: true})
	g.Paint(&p, f, g.size, gunim.Children{})
	end()
}

func TestARadioGroupMeasuresAgainAsTheThemeChanges(t *testing.T) {
	g := NewRadioGroup(restoreChoices()...)
	live := theme.NewLive(Dark())
	layRadio(g, live, 360, 0)
	_, before := g.span(0)

	mono := text.GoMono(true, false)
	live.Use(theme.Make("roomy",
		theme.Set(RadioPadding, geom.Insets{Top: 40, Right: 16, Bottom: 40, Left: 16}),
		theme.Set(BoldFont, mono)))
	for range 120 {
		live.Step(time.Second / 60)
	}
	layRadio(g, live, 360, 0)
	if _, after := g.span(0); after < before+56-0.5 {
		t.Errorf("with 56 more padding the first card is %v high, was %v: its measure did not follow", after, before)
	}
	if face := g.labels[0].p.Lines[0].Run.Face; face != mono {
		t.Errorf("the label is set in %v after the bold face changed, want the new one", face)
	}
	if out := spills(paintedIn(g, g.size, live), g.size, 3); len(out) > 0 {
		t.Errorf("after the change the group draws outside its box: %v", out)
	}
}

func TestARadioGroupsActiveChoiceCountsFromTheFirstInView(t *testing.T) {
	g := NewRadioGroup(manyChoices(40)...)
	live := theme.NewLive(Dark())
	layRadio(g, live, 360, 0)
	top, _ := g.span(5)
	layRadio(g, live, 360, top)
	g.SetSelected(7, nil)
	if info := g.Access(); info.Active != 7-g.from+1 || info.Parts[info.Active-1].Name != "Choice 7" {
		t.Fatalf("scrolled to choice %d with choice 7 chosen, Active is %d, want the part that is choice 7",
			g.from, info.Active)
	}
	g.SetSelected(1, nil)
	if info := g.Access(); info.Active != 0 {
		t.Fatalf("with choice 1 chosen, out of view, Active is %d, want 0 for none", info.Active)
	}
}

func TestALongLabelWrapsInPlaceOfBeingCut(t *testing.T) {
	items := restoreChoices()
	items[0].Label = "Put them back in Music projects on the server in the basement, where they were last week"
	g := NewRadioGroup(items...)
	live := theme.NewLive(Dark())
	layRadio(g, live, 300, 0)
	label := g.labels[0].p
	if len(label.Lines) < 2 || label.Truncated {
		t.Fatalf("the long label takes %d lines, cut %v; want it wrapped onto more than one, whole",
			len(label.Lines), label.Truncated)
	}
	_, h := g.span(0)
	if need := label.Size.H + g.details[0].p.Size.H; h < need {
		t.Fatalf("the card is %v high for a label and a detail %v high", h, need)
	}
	if out := spills(paintedIn(g, g.size, live), g.size, 3); len(out) > 0 {
		t.Fatalf("the group draws outside its box: %v", out)
	}
}

func TestANewWidthMeasuresOnlyTheChoicesInView(t *testing.T) {
	g := NewRadioGroup(manyChoices(100_000)...)
	live := theme.NewLive(Dark())
	layRadio(g, live, 360, 0)
	shown := g.to - g.from + 1
	for w := float32(361); w < 371; w++ {
		g.measures = 0
		layRadio(g, live, w, 0)
		if g.measures > 2*(shown+1) {
			t.Fatalf("at width %v the group measured %d choices, with %d in view", w, g.measures, shown)
		}
	}
}

func BenchmarkRadioGroupResize100000(b *testing.B) {
	g := NewRadioGroup(manyChoices(100_000)...)
	live := theme.NewLive(Dark())
	layRadio(g, live, 360, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		layRadio(g, live, 300+float32(i%100), 0)
	}
}
