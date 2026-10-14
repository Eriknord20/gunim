package themeedit

import (
	"slices"
	"testing"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// keysOf returns the keys of each section's fields, by title.
func keysOf(sections []Section) map[string][]string {
	out := map[string][]string{}
	for _, s := range sections {
		for _, f := range s.Fields {
			out[s.Title] = append(out[s.Title], f.Key)
		}
	}
	return out
}

func TestTheAnimationsFieldGoesFirstUnderMotionOrInAMotionOfItsOwn(t *testing.T) {
	scale := theme.Scale.Key()
	motion := Section{Title: "Motion", Fields: []Field{{Key: widget.Quick.Key()}}}
	got := keysOf(withScale([]Section{cursorSection, motion}))
	if want := []string{scale, widget.Quick.Key()}; !slices.Equal(got["Motion"], want) {
		t.Errorf("under Motion are %v, want %v", got["Motion"], want)
	}
	if len(motion.Fields) != 1 {
		t.Error("the program's section was changed")
	}
	got = keysOf(withScale([]Section{cursorSection}))
	if want := []string{scale}; !slices.Equal(got["Motion"], want) {
		t.Errorf("with no Motion, a Motion of its own has %v", got["Motion"])
	}
	placed := Section{Title: "Look", Fields: []Field{{Key: scale, Label: "Speed"}}}
	if got := withScale([]Section{cursorSection, placed}); len(got) != 2 || len(keysOf(got)["Motion"]) != 0 {
		t.Errorf("placed by the program, it was added again: %v", keysOf(got))
	}
	if got := withScale(nil); len(got) != 0 {
		t.Errorf("with no sections, there are %d", len(got))
	}
}

func TestTheAnimationsSliderMovesByTwentiethsAndPlays(t *testing.T) {
	e, _, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	c, ok := e.chosen[theme.Scale.Key()][0].ctl.(*numberControl)
	if !ok || c.slider == nil {
		t.Fatalf("Animations is a %T, with no slider", e.chosen[theme.Scale.Key()][0].ctl)
	}
	if c.whole || c.slider.Snap != 0.05 || c.slider.Max != MaxScale {
		t.Fatalf("the slider snaps to %v up to %v, whole %v", c.slider.Snap, c.slider.Max, c.whole)
	}
	c.slider.SetValue(2.5, u)
	c.slider.OnCommit(2.5, u)
	run(1)
	if got := valueIn(h.last(t), theme.Scale); got != 2.5 {
		t.Fatalf("Animations is %v, want 2.5", got)
	}
	if got := e.preview.caption.Text; got != "Playing Animations" {
		t.Fatalf("the preview says %q", got)
	}
}

func TestThePreviewMovesAtTheScaleEdited(t *testing.T) {
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	t.Cleanup(func() { anim.SetScale(1) })
	caret := func() float32 { return widget.Caret.In(e.preview.worn).Response }
	worn := func() float32 { return widget.Caret.Get(e.preview.themed.ThemeScope()).Response }
	if err := e.Set(theme.Scale.Key(), float32(3), true, u); err != nil {
		t.Fatal(err)
	}
	run(1)
	if got, want := worn(), 3*caret(); abs(got-want) > 1e-4 {
		t.Fatalf("with the program at 1, the preview's cursor takes %v, want %v", got, want)
	}
	// The program takes the theme: its scale is 3 now, and the preview
	// lets it do the stretching.
	u.UseTheme(e.Theme())
	if anim.Scale() != 3 {
		t.Fatalf("the program's scale is %v after taking the theme", anim.Scale())
	}
	run(2)
	if got, want := worn(), caret(); abs(got-want) > 1e-4 {
		t.Fatalf("with the program at 3, the preview's cursor takes %v, want %v", got, want)
	}
}

func TestACursorWithARangeIsASliderThatKeepsItsBounce(t *testing.T) {
	glides := widget.Caret.Default()
	section := Section{Title: "Terminal", Fields: []Field{{
		Key: widget.Caret.Key(), Label: "Cursor", Min: 0, Max: 1,
		Presets: []Preset{{Label: "Jumps", Value: Instant}, {Label: "Glides", Value: glides}},
	}}}
	bouncy := anim.Spring{Response: 0.2, Damping: 0.5}
	e, _, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{section},
		Overrides: theme.Make("", theme.Set(widget.Caret, bouncy))})
	c, ok := e.chosen[widget.Caret.Key()][0].ctl.(*springSlider)
	if !ok {
		t.Fatalf("the cursor is a %T", e.chosen[widget.Caret.Key()][0].ctl)
	}
	if c.slider.Value() != 0.2 || c.words.Text != "200 ms" {
		t.Fatalf("it shows %v as %q", c.slider.Value(), c.words.Text)
	}
	if !c.slider.HasRest || c.slider.Rest != glides.Response {
		t.Fatalf("it rests at %v, %v", c.slider.Rest, c.slider.HasRest)
	}
	c.slider.SetValue(0.75, u)
	c.slider.OnCommit(0.75, u)
	run(1)
	if got := valueIn(h.last(t), widget.Caret); got != (anim.Spring{Response: 0.75, Damping: 0.5}) {
		t.Fatalf("the cursor is %+v", got)
	}
	if c.words.Text != "750 ms" || e.preview.caption.Text != "Playing Cursor" {
		t.Fatalf("it says %q, the preview %q", c.words.Text, e.preview.caption.Text)
	}
	for v, want := range map[float32]string{0: "Jumps", glides.Response: "Glides", 1: "1 s"} {
		c.say(v)
		if c.words.Text != want {
			t.Errorf("%v says %q, want %q", v, c.words.Text, want)
		}
	}
}
