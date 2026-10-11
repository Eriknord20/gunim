package widget

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

type segmentChosen struct{ I int }

type selectTwo struct{}

func TestASegmentedControlChoosesByClickAndArrows(t *testing.T) {
	s := NewSegmented("One", "Two", "Three")
	s.OnChange = func(i int, u *gunim.UI) gunim.Intent { return segmentChosen{i} }
	w, run := stage(t, &frame{child: Row(s), size: geom.Sz(600, 40)})
	run(1)
	click(w, s.width*2.5, 14)
	run(1)
	if got := sent(w); s.Selected() != 2 || len(got) != 1 || got[0] != (segmentChosen{2}) {
		t.Fatalf("a click on Three segmentChosen %d and sent %v", s.Selected(), got)
	}
	run(60)
	if at := s.pill.Value(); at < 1.99 || at > 2.01 {
		t.Fatalf("the pill rests at %v, want on Three", at)
	}
	press := func(k input.Key) {
		w.Input(input.KeyPress{Key: k})
		run(1)
	}
	press(input.KeyLeft)
	press(input.KeyLeft)
	press(input.KeyLeft)
	if got := sent(w); s.Selected() != 0 || len(got) != 2 || got[1] != (segmentChosen{0}) {
		t.Fatalf("Left three times from Three segmentChosen %d and sent %v, want One, and nothing past the end", s.Selected(), got)
	}
	press(input.KeyEnd)
	if s.Selected() != 2 {
		t.Fatalf("End segmentChosen %d, want the last", s.Selected())
	}
	sent(w)

	// Set from the view, it sends nothing.
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ selectTwo, u *gunim.UI) { s.SetSelected(1, u) })
	if err := w.Client().Patch("stage", selectTwo{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if got := sent(w); s.Selected() != 1 || len(got) != 0 {
		t.Fatalf("SetSelected(1) segmentChosen %d and sent %v", s.Selected(), got)
	}
	if at := s.pill.Value(); at <= 1 || at >= 2 {
		t.Fatalf("a frame after SetSelected(1) the pill is at %v, want on its way from Three", at)
	}
}

func TestTabReachesASegmentedControl(t *testing.T) {
	b, s := NewButton("B"), NewSegmented()
	s.Icons = []*icon.Icon{icon.List, icon.LayoutGrid}
	w, run := stage(t, &frame{child: Column(b, s), size: geom.Sz(400, 200)})
	focused := focusProbe(t, w, run)
	tab(w, run, 0)
	tab(w, run, 0)
	if f := focused(); f != s {
		t.Fatalf("two Tabs put the keyboard on %T, want the segmented control", f)
	}
	w.Input(input.KeyPress{Key: input.KeyRight})
	run(30)
	if s.Selected() != 1 || s.ring.Value() < 0.99 {
		t.Fatalf("Right segmentChosen %d with the ring %v grown; want the second, the ring all the way", s.Selected(), s.ring.Value())
	}
	info := s.Access()
	if len(info.Parts) != 2 || info.Parts[0].Name != "list" || !info.Parts[1].State.Has(access.StateChecked) {
		t.Fatalf("the control reads as %+v, want two options named by their icons, the second checked", info.Parts)
	}
}

func TestASegmentedControlThatKeepsFocusLeavesTheKeyboardOnAClick(t *testing.T) {
	field, s := NewTextField(), NewSegmented("One", "Two")
	s.KeepFocus = true
	w, run := stage(t, &frame{child: Column(field, s), size: geom.Sz(300, 200)})
	focused := focusProbe(t, w, run)
	click(w, 20, 10)
	run(1)
	click(w, s.width*1.5, FieldHeight.Default()+Gap.Default()+10)
	run(1)
	if f := focused(); f != field || s.Selected() != 1 {
		t.Fatalf("a click on Two chose %d and moved the keyboard to %T; want Two, the keyboard left in the field",
			s.Selected(), f)
	}
}

func TestASegmentedFitsTheWidthItIsGiven(t *testing.T) {
	s := NewSegmented("Auto", "VNG", "PPG", "AHD", "Rebuild everything")
	s.OnChange = func(i int, u *gunim.UI) gunim.Intent { return segmentChosen{i} }
	w, run := stage(t, &frame{child: &sized{w: 200, child: s}, size: geom.Sz(400, 100)})
	run(2)
	if s.size.W != 200 || s.width*float32(s.Len()) > 200+0.01 {
		t.Fatalf("given 200, the control is %v wide with options %v wide", s.size.W, s.width)
	}
	// The last option is at the right end of the track, and takes a click.
	click(w, 195, s.size.H/2)
	run(1)
	if s.Selected() != 4 {
		t.Fatalf("a click at the right end chose %d, want the last", s.Selected())
	}
}

func TestASegmentedInANarrowRowDrawsInsideItsBox(t *testing.T) {
	s := NewSegmented("Monday", "Tuesday", "Wednesday", "Thursday", "Friday")
	s.SetSelected(4, nil)
	s.OnChange = func(i int, u *gunim.UI) gunim.Intent { return segmentChosen{i} }
	w, run := stage(t, &frame{child: Row(s), size: geom.Sz(200, 100)})
	run(1)
	if s.size.W > 200 {
		t.Fatalf("in a 200 px row the control is %v wide", s.size.W)
	}
	if bad := spills(painted(s, s.size), s.size, ringReach); len(bad) > 0 {
		t.Fatalf("the control drew past its %v box: %v", s.size, bad)
	}
	// Every option takes a click on its own share.
	for i := range 4 {
		click(w, (float32(i)+0.5)*s.size.W/5, s.size.H/2)
		run(1)
		if s.Selected() != i {
			t.Fatalf("a click on option %d's share chose %d", i, s.Selected())
		}
	}
}

// sized lays its child out exactly w wide.
type sized struct {
	w     float32
	child gunim.Node
}

func (z *sized) Children() []gunim.Node { return []gunim.Node{z.child} }

func (z *sized) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	s := k.Layout(gunim.Tight(geom.Sz(z.w, 28)))
	k.Place(geom.Point{})
	return s
}

func (z *sized) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

func TestAFittedSegmentedControlGivesEachOptionItsOwnWidth(t *testing.T) {
	s := NewSegmented("All 280", "Changed 2", "Other 5")
	s.Fit = true
	w, run := stage(t, &frame{child: Row(s), size: geom.Sz(600, 40)})
	run(1)
	if len(s.lefts) != 4 {
		t.Fatalf("the options' edges are %v", s.lefts)
	}
	all, changed := s.lefts[1]-s.lefts[0], s.lefts[2]-s.lefts[1]
	if changed <= all {
		t.Fatalf("Changed is %v wide and All %v: not each its own width", changed, all)
	}
	if s.lefts[3] != s.size.W {
		t.Fatalf("the options end at %v, the control at %v", s.lefts[3], s.size.W)
	}
	// A click hits the option drawn there, and the pill takes its span.
	click(w, s.lefts[1]+changed-4, 14)
	run(60)
	if s.Selected() != 1 {
		t.Fatalf("a click near Changed's right edge chose %d", s.Selected())
	}
	if x, wd := s.span(s.pill.Value()); x != s.lefts[1] || wd != changed {
		t.Fatalf("the pill spans %v, %v, want Changed's %v, %v", x, wd, s.lefts[1], changed)
	}
	if b := s.Access().Parts[2].Bounds; b.Min.X != s.lefts[2] {
		t.Fatalf("Other's bounds start at %v, want %v", b.Min.X, s.lefts[2])
	}
}

func TestASegmentedTrackStandsApartFromALightCard(t *testing.T) {
	light := theme.NewLive(Light())
	if SegmentedTrack.Get(light) == CardFill.Get(light) || SegmentedTrack.Get(light) == FieldFill.Get(light) {
		t.Fatal("in the light theme the track is the card's colour")
	}
	dark := theme.NewLive(Dark())
	if SegmentedTrack.Get(dark) == CardFill.Get(dark) {
		t.Fatal("in the dark theme the track is the card's colour")
	}
}

func TestASegmentedOptionsLabelFadesInUnderThePointerAndOut(t *testing.T) {
	s := NewSegmented("One", "Two", "Three")
	w, run := stage(t, &frame{child: Row(s), size: geom.Sz(600, 40)})
	run(1)
	w.Input(input.PointerMove{Pos: geom.Pt(s.width*1.5, 14)})
	run(1)
	if g := s.glow[1].Value(); g <= 0 || g >= 1 {
		t.Fatalf("a frame after the pointer came over Two its label has lit %v, want on its way", g)
	}
	run(60)
	if g := s.glow[1].Value(); g < 0.99 {
		t.Fatalf("Two's label has lit %v under the pointer, want fully", g)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(s.width*2.5, 14)})
	run(1)
	if g, h := s.glow[1].Value(), s.glow[2].Value(); g <= 0 || g >= 1 || h <= 0 {
		t.Fatalf("a frame after the pointer moved on to Three, Two has lit %v and Three %v, want Two fading and Three lighting", g, h)
	}
	run(120)
	if g := s.glow[1].Value(); g > 0.01 {
		t.Fatalf("Two's label is still lit %v after the pointer left it", g)
	}
}
