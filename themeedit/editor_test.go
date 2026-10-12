package themeedit

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// probe is a patch that hands a test the window's UI.
type probe struct{}

// heard is what the editor told the program.
type heard struct {
	changes []theme.Theme
	commits []theme.Theme
}

// last returns the theme the last change sent.
func (h *heard) last(t *testing.T) theme.Theme {
	t.Helper()
	if len(h.changes) == 0 {
		t.Fatal("OnChange never ran")
	}
	return h.changes[len(h.changes)-1]
}

// cursorSection is the first tab of a terminal: its cursor, its accent
// and the gap between its panes.
var cursorSection = Section{Title: "Terminal", Fields: []Field{
	{Key: widget.Caret.Key(), Label: "Cursor", Detail: "How the cursor moves along a line.",
		Presets: []Preset{{Label: "Glides", Value: widget.Caret.Default()}, {Label: "Jumps", Value: Instant}}},
	{Key: widget.Accent.Key(), Label: "Accent", Detail: "The colour of focus and selection."},
	{Key: widget.Gap.Key(), Label: "Gap", Detail: "The room between things in a row."},
}}

// edStage mounts an editor of o in a window size big and returns it,
// the window, its UI, a way to run frames, and what the editor tells
// the program.
func edStageSized(t *testing.T, size geom.Size, o Options) (*Editor, *gunim.Window, *gunim.UI, func(int), *heard) {
	t.Helper()
	h := &heard{}
	o.OnChange = func(th theme.Theme, _ *gunim.UI) gunim.Intent { h.changes = append(h.changes, th); return nil }
	o.OnCommit = func(over theme.Theme, _ *gunim.UI) gunim.Intent { h.commits = append(h.commits, over); return nil }
	e := New(o)
	w := gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "ed", func(struct{}) gunim.Node { return e }, nil)
	if err := w.Client().Mount(gunim.Root, "ed", "ed", nil); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	var u *gunim.UI
	gunim.RegisterPatch(w, "ed", func(_ gunim.Node, _ probe, ui *gunim.UI) { u = ui })
	if err := w.Client().Patch("ed", probe{}); err != nil {
		t.Fatal(err)
	}
	run(2)
	if u == nil {
		t.Fatal("no UI")
	}
	return e, w, u, run, h
}

// edStage is edStageSized in a window wide enough for the preview
// beside the controls.
func edStage(t *testing.T, o Options) (*Editor, *gunim.Window, *gunim.UI, func(int), *heard) {
	t.Helper()
	return edStageSized(t, geom.Sz(1200, 800), o)
}

// key presses k with mods, lets it go, and runs a frame.
func key(w *gunim.Window, run func(int), k input.Key, mods input.Mods) {
	w.Input(input.KeyPress{Key: k, Mods: mods})
	w.Input(input.KeyRelease{Key: k, Mods: mods})
	run(1)
}

// valueIn returns key's value in th, read as a window would.
func valueIn[T any](th theme.Theme, tok theme.Token[T]) T { return tok.Get(theme.NewLive(th)) }

func TestTheEditorListsTheChosenFieldsAndEveryToken(t *testing.T) {
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	if got, want := e.Chosen(), []string{"motion.caret", "accent", "layout.gap"}; !slices.Equal(got, want) {
		t.Fatalf("Chosen lists %v, want %v", got, want)
	}
	for _, k := range e.Chosen() {
		if len(e.chosen[k]) != 1 {
			t.Fatalf("%q has %d rows on the first tab", k, len(e.chosen[k]))
		}
	}
	if got := e.tabs.Titles; !slices.Equal(got, []string{"Basics", "All values"}) {
		t.Fatalf("the tabs are %v", got)
	}
	listed := e.Listed()
	tokens := make([]string, 0, len(theme.Tokens()))
	for _, info := range theme.Tokens() {
		tokens = append(tokens, info.Key)
	}
	if len(listed) != len(tokens) || len(listed) < 200 {
		t.Fatalf("All values lists %d keys, want all %d tokens", len(listed), len(tokens))
	}
	sorted := slices.Clone(listed)
	slices.Sort(sorted)
	if !slices.Equal(sorted, tokens) {
		t.Fatal("All values does not list every token once")
	}
	// The groups are headed in words, and a group of one goes under
	// Other, which comes last.
	titles := make([]string, 0, len(e.all.keys))
	for _, k := range e.all.keys {
		g := e.all.items[k]
		titles = append(titles, g.title)
		if len(g.keys) == 1 && g.title != otherGroup {
			t.Fatalf("the group %q has one row", g.title)
		}
		if g.title != Words(g.title) || strings.Contains(g.title, ".") {
			t.Fatalf("a group is headed %q", g.title)
		}
	}
	if !slices.Contains(titles, "Button") || titles[len(titles)-1] != otherGroup {
		t.Fatalf("the groups are %v", titles)
	}
	ShowAll := func() { e.ShowAllValues(u); run(5) }
	ShowAll()
	if n := len(e.all.rowsBuilt()); n == 0 || n > 80 {
		t.Fatalf("All values built %d rows, want those in view alone", n)
	}
	// A row in a group is named without its group's word, its key under
	// it.
	r := e.all.rowOf(t, "address.chevron")
	if r.line.title.Text != "Chevron" || r.line.detail.Text != "address.chevron" {
		t.Fatalf("address.chevron's row says %q over %q", r.line.title.Text, r.line.detail.Text)
	}
}

func TestSearchAndFiltersNarrowAllValues(t *testing.T) {
	over := theme.Make("mine", theme.Set(widget.Gap, 20), theme.Set(widget.Accent, color.NRGBA{R: 0xff, A: 0xff}))
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Overrides: over, Sections: []Section{cursorSection}})
	e.Search("caret", u)
	run(2)
	got := e.Listed()
	if !slices.Contains(got, "motion.caret") {
		t.Fatalf("a search for caret lists %v", got)
	}
	// A section's label finds its token, and every word must match.
	e.Search("CURSOR motion", u)
	if got := e.Listed(); !slices.Equal(got, []string{"motion.caret"}) {
		t.Fatalf("a search for the label lists %v", got)
	}
	e.Search("no such token anywhere", u)
	if got := e.Listed(); len(got) != 0 {
		t.Fatalf("a search for nothing lists %v", got)
	}
	e.Search("", u)
	if len(e.Listed()) != len(theme.Tokens()) {
		t.Fatal("an empty search does not list every token")
	}

	// Changed lists the overrides alone, and its count says so.
	e.SetFilter(FilterChanged, u)
	if got := e.Listed(); !slices.Equal(sortedOf(got), []string{"accent", "layout.gap"}) {
		t.Fatalf("Changed lists %v", got)
	}
	if got := e.all.filters.Items[FilterChanged]; got != "Changed 2" {
		t.Fatalf("the Changed filter says %q", got)
	}
	// A kind lists its own.
	for f, kinds := range map[Filter][]theme.Kind{
		FilterColours: {theme.KindColor, theme.KindForeground},
		FilterMotion:  {theme.KindSpring},
		FilterSizes:   {theme.KindLength, theme.KindNumber, theme.KindInsets},
	} {
		e.SetFilter(f, u)
		got := e.Listed()
		if len(got) == 0 {
			t.Fatalf("%v lists nothing", f)
		}
		for _, k := range got {
			if !slices.Contains(kinds, e.tokens[k].Kind) {
				t.Fatalf("%v lists %q, a %v", f, k, e.tokens[k].Kind)
			}
		}
	}
	// The search and the filter work together.
	e.SetFilter(FilterColours, u)
	e.Search("button primary", u)
	for _, k := range e.Listed() {
		if !strings.HasPrefix(k, "button.primary") || kindFilter(e.tokens[k].Kind) != FilterColours {
			t.Fatalf("colours searched for button primary lists %q", k)
		}
	}
}

func TestPickingAPresetSetsTheSpringAndPlaysIt(t *testing.T) {
	e, w, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	ctl := ctlOf[*springControl](t, e.chosen["motion.caret"][0])
	seg := ctl.presets.seg
	if seg.Selected() != 0 || !slices.Equal(seg.Items, []string{"Glides", "Jumps"}) {
		t.Fatalf("the cursor shows %v, item %d, want Glides of Glides and Jumps", seg.Items, seg.Selected())
	}
	u.Focus(seg)
	key(w, run, input.KeyRight, 0)
	if got := valueIn(h.last(t), widget.Caret); got != Instant {
		t.Fatalf("Jumps gives the theme a caret of %+v, want instant", got)
	}
	if len(h.commits) != 1 || !h.commits[0].Has("motion.caret") {
		t.Fatalf("OnCommit heard %v", h.commits)
	}
	r := e.chosen["motion.caret"][0]
	if !e.Overrides().Has("motion.caret") || !r.changed || r.reset.Disabled {
		t.Fatal("the cursor does not show as changed")
	}
	// The pick played the cursor in the preview.
	if e.spec.term.jumps == 0 || e.preview.caption.Text == "" {
		t.Fatal("picking Jumps did not play the cursor")
	}
	if ctl.fold.Open() {
		t.Fatal("a preset shows the sliders")
	}
	// A value none of the presets has shows as Custom, with the sliders
	// open under the row.
	if err := e.Set("motion.caret", anim.Spring{Response: 0.3, Damping: 0.5}, true, u); err != nil {
		t.Fatal(err)
	}
	if !ctl.presets.custom || seg.Selected() != 2 || seg.Items[2] != "Custom" || !ctl.fold.Open() {
		t.Fatalf("a spring of its own shows %v, item %d, open %v", seg.Items, seg.Selected(), ctl.fold.Open())
	}
	if got := ctl.speed.Value(); got != 0.3 {
		t.Fatalf("Speed shows %v", got)
	}
	if got := ctl.bounce.Value(); abs(got-0.5) > 1e-4 {
		t.Fatalf("Bounce shows %v", got)
	}
	if speedWords(0.25) != "250 ms" || bounceWords(0) != "None" || bounceWords(0.4) != "40%" {
		t.Fatal("the sliders' words are wrong")
	}
}

func TestCustomRevealsASpringsSlidersInAllValues(t *testing.T) {
	e, w, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	e.ShowAllValues(u)
	e.Search("motion.quick", u)
	run(5)
	r := e.all.rowOf(t, "motion.quick")
	spring := ctlOf[*springControl](t, r)
	seg := spring.presets.seg
	if got := seg.Items; !slices.Equal(got, []string{"Instant", "Snappy", "Gentle", "Bouncy", "Custom"}) {
		t.Fatalf("a spring offers %v", got)
	}
	if seg.Selected() != 1 || spring.fold.Open() {
		t.Fatalf("Snappy shows item %d, open %v", seg.Selected(), spring.fold.Open())
	}
	// Custom opens the sliders and changes nothing.
	u.Focus(seg)
	key(w, run, input.KeyEnd, 0)
	run(3)
	if !spring.fold.Open() || len(h.changes) != 0 {
		t.Fatalf("Custom left the sliders open %v, and sent %d changes", spring.fold.Open(), len(h.changes))
	}
	if !slices.Contains(r.stops(), gunim.Node(spring.bounce)) {
		t.Fatal("Tab does not reach the open sliders")
	}
	u.Focus(spring.bounce)
	key(w, run, input.KeyRight, 0)
	want := anim.Spring{Response: anim.Snappy.Response, Damping: 1 - (1 - anim.Snappy.Damping + 0.01)}
	if got := valueIn(h.last(t), widget.Quick); abs(got.Damping-want.Damping) > 1e-4 || got.Response != want.Response {
		t.Fatalf("Right on Bounce gives the theme %+v, want %+v", got, want)
	}
	if seg.Selected() != 4 {
		t.Fatalf("the presets show %d, want Custom", seg.Selected())
	}
	// The commit played the motion, quick: the switch flipped.
	if e.preview.caption.Text == "" || e.spec.sync.Checked() {
		t.Fatal("the change did not play the quick motion")
	}
}

func TestEditingAColourAndANumberChangesTheTheme(t *testing.T) {
	e, w, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})

	// A colour, through its button's picker.
	colour := ctlOf[*colorControl](t, e.chosen["accent"][0])
	if !colour.button.Hex {
		t.Fatal("the accent's swatch does not show its hex")
	}
	u.Focus(colour.button)
	key(w, run, input.KeyEnter, 0)
	run(5)
	if !colour.button.IsOpen() {
		t.Fatal("Enter opened no picker")
	}
	key(w, run, input.KeyLeft, input.ModShift)
	got := valueIn(h.last(t), widget.Accent)
	if got == widget.Accent.Default() || got != colour.button.Value() {
		t.Fatalf("an edit in the picker gives the theme an accent of %v; the button shows %v", got, colour.button.Value())
	}
	key(w, run, input.KeyEscape, 0)
	run(30)

	// A number with no range is a field alone.
	num := ctlOf[*numberControl](t, e.chosen["layout.gap"][0])
	if num.slider != nil || num.field == nil {
		t.Fatal("the gap shows a slider and a field")
	}
	u.Focus(num.field)
	key(w, run, input.KeyUp, 0)
	if got := valueIn(h.last(t), widget.Gap); got != widget.Gap.Default()+1 {
		t.Fatalf("Up in the gap's field gives the theme a gap of %v", got)
	}
	if got := e.Overrides().Keys(); !slices.Equal(got, []string{"accent", "layout.gap"}) {
		t.Fatalf("the overrides are %v", got)
	}
}

func TestANumberWithARangeIsASliderWithAField(t *testing.T) {
	sec := Section{Title: "Look", Fields: []Field{{Key: widget.Gap.Key(), Label: "Gap", Min: 0, Max: 32}}}
	e, w, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{sec}})
	num := ctlOf[*numberControl](t, e.chosen["layout.gap"][0])
	if num.slider == nil || num.field == nil {
		t.Fatal("a gap with a range is not a slider with a field")
	}
	if num.field.Text() != "8" {
		t.Fatalf("the slider's field reads %q", num.field.Text())
	}
	u.Focus(num.slider)
	key(w, run, input.KeyRight, 0)
	if got := valueIn(h.last(t), widget.Gap); got <= widget.Gap.Default() || num.field.Value() != float64(got) {
		t.Fatalf("Right on the slider gives the theme a gap of %v and the field %v", got, num.field.Value())
	}
	u.Focus(num.field)
	key(w, run, input.KeyUp, 0)
	if got := valueIn(h.last(t), widget.Gap); num.slider.Value() != got || num.field.Value() != float64(got) {
		t.Fatalf("Up in the field gives a gap of %v, the slider %v", got, num.slider.Value())
	}
}

func TestDraggingANumberCommitsOnceItIsLetGo(t *testing.T) {
	e, w, u, run, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	num := ctlOf[*numberControl](t, e.chosen["layout.gap"][0])
	r, ok := u.Bounds(num.field)
	if !ok {
		t.Fatal("the gap's field is not laid out")
	}
	at := r.Center()
	commits := len(h.commits)
	w.Input(input.PointerMove{Pos: at, Time: time.Now()})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	run(1)
	for i := range 5 {
		w.Input(input.PointerMove{Pos: at.Add(geom.Pt(0, -8*float32(i+1))), Time: time.Now()})
		run(1)
	}
	if got := valueIn(h.last(t), widget.Gap); got != widget.Gap.Default()+10 {
		t.Fatalf("a drag 40 pixels up gives a gap of %v", got)
	}
	if len(h.commits) != commits {
		t.Fatalf("the drag committed %d times on the way", len(h.commits)-commits)
	}
	w.Input(input.PointerUp{Pos: at.Add(geom.Pt(0, -40)), Button: input.ButtonPrimary, Time: time.Now()})
	run(1)
	if len(h.commits) != commits+1 {
		t.Fatalf("letting go committed %d times, want once", len(h.commits)-commits)
	}
}

func TestThePreviewWearsTheEditsAndTheControlsTheBase(t *testing.T) {
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	red := color.NRGBA{R: 0xff, A: 0xff}
	if err := e.Set("accent", red, true, u); err != nil {
		t.Fatal(err)
	}
	if err := e.Set("text.size", float32(20), true, u); err != nil {
		t.Fatal(err)
	}
	run(60)
	controls := e.themed.ThemeScope()
	preview := e.preview.themed.ThemeScope()
	if got := widget.Accent.Get(preview); got != red {
		t.Fatalf("the preview's accent is %v, want the edit's", got)
	}
	if got := widget.TextSize.Get(preview); got != 20 {
		t.Fatalf("the preview's text is %v, want the edit's", got)
	}
	if got := widget.Accent.Get(controls); got != widget.Accent.Default() {
		t.Fatalf("the controls' accent is %v, want the base's", got)
	}
	if got := widget.TextSize.Get(controls); got != widget.TextSize.Default() {
		t.Fatalf("the controls' text is %v, want the base's", got)
	}
	// A new base dresses the controls.
	e.SetBase(widget.Light(), u)
	run(90)
	if got, want := widget.Accent.Get(controls), valueIn(widget.Light(), widget.Accent); got != want {
		t.Fatalf("after a new base the controls' accent is %v, want %v", got, want)
	}
	if got := widget.Accent.Get(preview); got != red {
		t.Fatalf("after a new base the preview's accent is %v", got)
	}
}

func TestThePreviewGoesAboveTheControlsWhenNarrow(t *testing.T) {
	e, _, _, _, _ := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	if e.preview.narrow {
		t.Fatal("a wide editor has the preview above the controls")
	}
	n, _, u, run, _ := edStageSized(t, geom.Sz(700, 900), Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	if !n.preview.narrow {
		t.Fatal("a narrow editor has the preview beside the controls")
	}
	pb, _ := u.Bounds(n.preview)
	tb, _ := u.Bounds(n.tabs)
	if pb.Max.Y > tb.Min.Y {
		t.Fatalf("the preview at %v is not above the tabs at %v", pb, tb)
	}
	// Its button folds it away.
	n.preview.toggle.OnClick(u)
	run(30)
	if n.preview.fold.Open() {
		t.Fatal("the preview does not fold")
	}
}

func TestPlayPicksADemoForEachMotion(t *testing.T) {
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	sc := e.preview.scene
	e.Play(widget.Settle.Key(), u)
	run(30)
	if sc.panel.Value() < 0.5 {
		t.Fatalf("settling did not slide the panel in: %v", sc.panel.Value())
	}
	e.Play(widget.Bounce.Key(), u)
	run(30)
	if sc.toast.Value() < 0.5 || sc.fade.Value() < 0.5 {
		t.Fatal("a bounce did not pop the toast")
	}
	e.Play(widget.HeroMotion.Key(), u)
	run(30)
	if sc.chipOn.Value() < 0.5 || sc.chip.Value() < 0.2 {
		t.Fatal("a motion of no demo of its own did not slide the chip")
	}
	if e.preview.caption.Text != "Playing Motion hero" {
		t.Fatalf("the preview says %q", e.preview.caption.Text)
	}
	// A key that is no motion plays nothing.
	e.preview.caption.Text = ""
	e.Play("accent", u)
	if e.preview.caption.Text != "" {
		t.Fatal("a colour played")
	}
	// The row's Play button plays its motion.
	ctl := ctlOf[*springControl](t, e.chosen["motion.caret"][0])
	e.spec.term.jumps = 0
	ctl.play.OnClick(u)
	if e.spec.term.jumps == 0 {
		t.Fatal("the cursor's Play did not jump the cursor")
	}

	// With a preview of the program's own, the cursor plays as any
	// motion does.
	own, _, u2, _, _ := edStage(t, Options{Base: widget.Dark(), Preview: widget.NewLabel("Mine")})
	own.Play(widget.Caret.Key(), u2)
	if own.preview.scene.chipOn.Target() != 1 {
		t.Fatal("the cursor did not slide the chip over the program's preview")
	}
}

func TestResetTakesAValueBackAndResetAllCanBeUndone(t *testing.T) {
	base := widget.Light()
	over := theme.Make("mine", theme.Set(widget.Gap, 20), theme.Set(widget.Accent, color.NRGBA{R: 0xff, A: 0xff}))
	e, w, u, run, h := edStage(t, Options{Base: base, Overrides: over, Sections: []Section{cursorSection}})
	r := e.chosen["layout.gap"][0]
	if r.reset.Disabled || !r.changed || !r.line.changed {
		t.Fatal("a changed row does not say so")
	}
	if r.reset.Tooltip != "Reset Gap to Light" {
		t.Fatalf("the reset button says %q", r.reset.Tooltip)
	}
	if e.count.Text != "2 changes" || e.title.Text != "Editing Light" {
		t.Fatalf("the header says %q, %q", e.title.Text, e.count.Text)
	}
	unchanged := e.chosen["motion.caret"][0]
	if !unchanged.reset.Disabled || unchanged.changed || slices.Contains(unchanged.stops(), gunim.Node(unchanged.reset)) {
		t.Fatal("an unchanged row offers a reset")
	}
	if num := ctlOf[*numberControl](t, r); num.field.Value() != 20 {
		t.Fatalf("the gap shows %v, want the override's 20", num.field.Value())
	}
	u.Focus(r.reset)
	key(w, run, input.KeyEnter, 0)
	if e.Overrides().Has("layout.gap") {
		t.Fatal("Reset left the override")
	}
	if got := valueIn(h.last(t), widget.Gap); got != valueIn(base, widget.Gap) {
		t.Fatalf("after Reset the theme's gap is %v, want the base's %v", got, valueIn(base, widget.Gap))
	}
	if len(h.commits) != 1 || h.commits[0].Has("layout.gap") || !h.commits[0].Has("accent") {
		t.Fatalf("OnCommit heard %v", h.commits)
	}
	if !r.reset.Disabled || r.changed {
		t.Fatal("a row back at the base still shows as changed")
	}
	if e.count.Text != "1 change" {
		t.Fatalf("the header says %q", e.count.Text)
	}

	// Reset all takes the rest, and can be undone until the next edit.
	u.Focus(e.resetAll)
	key(w, run, input.KeyEnter, 0)
	if e.Overrides().Len() != 0 || e.resetAll.Label != "Undo reset" || e.resetAll.Disabled {
		t.Fatalf("Reset all leaves %v, the button says %q", e.Overrides().Keys(), e.resetAll.Label)
	}
	if got := valueIn(h.last(t), widget.Accent); got != valueIn(base, widget.Accent) {
		t.Fatalf("after Reset all the accent is %v", got)
	}
	key(w, run, input.KeyEnter, 0)
	if !e.Overrides().Has("accent") || e.resetAll.Label != "Reset all" {
		t.Fatalf("Undo brought back %v", e.Overrides().Keys())
	}
	e.ResetAll(u)
	_ = e.Set("layout.gap", 3, true, u)
	if e.resetAll.Label != "Reset all" || e.undo != nil {
		t.Fatal("an edit after Reset all leaves Undo")
	}
	e.Reset("layout.gap", u)
	if !e.resetAll.Disabled {
		t.Fatal("Reset all is lit with no changes")
	}

	// A new base shows in the rows it does not override.
	e.SetBase(widget.Dark(), u)
	if c := ctlOf[*colorControl](t, e.chosen["accent"][0]); c.button.Value() != widget.Accent.Default() {
		t.Fatalf("after the base changed the accent shows %v", c.button.Value())
	}
}

func TestTabReachesEveryGroupOfAllValues(t *testing.T) {
	e, w, u, run, _ := edStage(t, Options{Base: widget.Dark()})
	e.SetFilter(FilterColours, u)
	run(10)
	a := e.all
	if a.groupRows(a.keys[0]) == nil {
		t.Fatal("the first group is not built")
	}
	// Tab from each group's last stop goes to the next group's first,
	// building it, down past what the view showed at first.
	for i := 0; i < 12 && i+1 < len(a.keys); i++ {
		g := a.items[a.keys[i]]
		stops := a.stops(a.keys[i])
		if len(stops) == 0 {
			t.Fatalf("group %q is not built", g.title)
		}
		// The keyboard is where the user would have it: in view.
		u.Focus(stops[len(stops)-1])
		u.Reveal(stops[len(stops)-1])
		run(30)
		key(w, run, input.KeyTab, 0)
		run(20)
		next := a.stops(a.keys[i+1])
		if len(next) == 0 {
			t.Fatalf("Tab did not build group %q", a.items[a.keys[i+1]].title)
		}
		if f := u.Focused(); f != next[0] {
			t.Fatalf("Tab from %q's last row went to %T, want %q's first", g.title, f, a.items[a.keys[i+1]].title)
		}
	}
	// Shift+Tab goes back to the last stop of the group before, though
	// the list has let it go since.
	j := 12
	stops := a.stops(a.keys[j])
	if len(stops) == 0 {
		t.Fatalf("group %q is not built", a.items[a.keys[j]].title)
	}
	u.Focus(stops[0])
	key(w, run, input.KeyTab, input.ModShift)
	run(20)
	prev := a.stops(a.keys[j-1])
	if len(prev) == 0 || u.Focused() != prev[len(prev)-1] {
		t.Fatal("Shift+Tab did not go back to the group before")
	}
}

func TestOverridesGoToJSONAndBack(t *testing.T) {
	e, _, u, _, h := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	_ = e.Set("motion.caret", Instant, true, u)
	_ = e.Set("accent", color.NRGBA{R: 1, G: 2, B: 3, A: 0x80}, true, u)
	_ = e.Set("card.padding", geom.Insets{Top: 1, Right: 2, Bottom: 3, Left: 4}, true, u)
	data, err := e.Export()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"accent": "#01020380"`) {
		t.Fatalf("the export reads\n%s", data)
	}

	other, _, u2, _, h2 := edStage(t, Options{Base: widget.Dark()})
	if err = other.Import(data, u2); err != nil {
		t.Fatal(err)
	}
	for _, k := range e.Overrides().Keys() {
		a, _ := e.Overrides().Value(k)
		b, ok := other.Overrides().Value(k)
		if !ok || !same(a, b) {
			t.Fatalf("%q came back as %v, want %v", k, b, a)
		}
	}
	if len(h2.changes) != 1 || len(h2.commits) != 1 {
		t.Fatal("Import does not tell the program")
	}

	// A file in between.
	path := filepath.Join(t.TempDir(), "theme.json")
	if err = WriteFile(path, e.Overrides()); err != nil {
		t.Fatal(err)
	}
	back, err := ReadFile(path)
	if err != nil || back.Len() != 3 {
		t.Fatalf("ReadFile gives %v, %v", back.Keys(), err)
	}

	// Values it cannot read are left out; the rest come in.
	err = other.Import([]byte(`{"accent": "#ff0000", "no.such.key": 1}`), u2)
	if err == nil || !strings.Contains(err.Error(), "no.such.key") {
		t.Fatalf("a bad key gives %v", err)
	}
	if got := valueIn(other.Theme(), widget.Accent); got != (color.NRGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("the good value came in as %v", got)
	}
	_ = h
}

func TestImportAndExportShowOnlyWhereTheProgramTakesThem(t *testing.T) {
	plain := New(Options{Base: widget.Dark()})
	if plain.more != nil {
		t.Fatal("an editor with no import or export has a menu for them")
	}
	both := New(Options{Base: widget.Dark(),
		OnImport: func(*gunim.UI) gunim.Intent { return nil },
		OnExport: func([]byte, *gunim.UI) gunim.Intent { return nil }})
	if both.more == nil || len(both.more.Items()) != 2 || both.more.Items()[0].Label != "Import…" {
		t.Fatal("an editor that imports and exports has no menu for them")
	}
}

func TestEveryKindOfRowBuilds(t *testing.T) {
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Choices: map[string][]Preset{
		widget.Font.Key(): {{Label: "Sans", Value: widget.Font.Default()}},
	}})
	e.ShowAllValues(u)
	run(10)
	got := map[string]*row{}
	for _, k := range []string{"card.padding", "text.font", "text.font.mono", "button.fill", "ink", "motion.bounce", "text.size"} {
		e.Search(k, u)
		run(10)
		r := e.all.rowOf(t, k)
		got[k] = r
		var want string
		switch k {
		case "card.padding":
			want = "*themeedit.insetsControl"
		case "text.font":
			want = "*themeedit.choiceControl"
		case "text.font.mono":
			want = "*themeedit.textControl"
		case "button.fill", "ink":
			want = "*themeedit.colorControl"
		case "motion.bounce":
			want = "*themeedit.springControl"
		default:
			want = "*themeedit.numberControl"
		}
		if got := typeName(r.ctl); got != want {
			t.Fatalf("%q has a %s, want a %s", k, got, want)
		}
	}
	if d := ctlOf[*choiceControl](t, got["text.font"]).drop; d.Selected() != 0 {
		t.Fatalf("the font's drop-down shows %d", d.Selected())
	}
	if l := ctlOf[*textControl](t, got["text.font.mono"]).label.Text; l == "" {
		t.Fatal("a font with no choices shows nothing")
	}
	if n := ctlOf[*numberControl](t, got["text.size"]); n.slider != nil {
		t.Fatal("a number in All values shows a slider")
	}
}

// rowOf returns the row All values shows for key, failing t where it
// shows none.
func (a *allValues) rowOf(t *testing.T, key string) *row {
	t.Helper()
	r := a.row(key)
	if r == nil {
		t.Fatalf("All values shows no row for %q", key)
	}
	return r
}

// rowsBuilt returns every row All values has built.
func (a *allValues) rowsBuilt() []*row {
	var out []*row
	for _, rows := range a.built {
		out = append(out, rows...)
	}
	return out
}

// ctlOf returns r's control as a T, failing t where it is another.
func ctlOf[T control](t *testing.T, r *row) T {
	t.Helper()
	c, ok := r.ctl.(T)
	if !ok {
		t.Fatalf("%q has a %T", r.key, r.ctl)
	}
	return c
}

// sortedOf returns a sorted copy of keys.
func sortedOf(keys []string) []string {
	out := slices.Clone(keys)
	slices.Sort(out)
	return out
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }

func abs(v float32) float32 { return max(v, -v) }

func TestAShortNarrowEditorStartsWithThePreviewFoldedAndPlayOpensIt(t *testing.T) {
	e, _, u, run, _ := edStageSized(t, geom.Sz(732, 540), Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	run(30)
	p := e.preview
	if !p.narrow || !p.short || !p.shut || p.fold.Open() {
		t.Fatal("a short narrow editor does not start with the preview folded away")
	}
	e.Play(widget.Settle.Key(), u)
	run(2)
	if !p.fold.Open() {
		t.Fatal("Play did not open the folded preview")
	}
	// Once played, it folds away again.
	run(int((2*hold + time.Second) / (time.Second / 60)))
	if p.fold.Open() {
		t.Fatal("the preview stayed open after playing")
	}
	// Opened by the user, it stays open.
	p.toggle.OnClick(u)
	run(30)
	if !p.fold.Open() {
		t.Fatal("the button did not open the preview")
	}
	e.Play(widget.Settle.Key(), u)
	run(int((2*hold + time.Second) / (time.Second / 60)))
	if !p.fold.Open() {
		t.Fatal("playing folded a preview the user opened")
	}
}

func TestRowsLineUpWithTheTabsAndAllValuesRowsAreEven(t *testing.T) {
	over := theme.Make("mine", theme.Set(widget.Gap, 20))
	e, _, u, run, _ := edStage(t, Options{Base: widget.Dark(), Overrides: over, Sections: []Section{cursorSection}})
	// The tabs' line ends where the cards do.
	if e.tabs.Inset.Key() != TabInset.Key() {
		t.Fatal("the tabs are not inset as the page is")
	}
	// The dot of a changed row is after its name, inside the card.
	r := e.chosen["layout.gap"][0]
	if r.line.titleAt.Min.X != 0 || r.line.titleAt.Size().W <= 0 || r.line.titleAt.Size().W > 60 {
		t.Fatalf("the title's words are at %v", r.line.titleAt)
	}
	// In All values every row with a field or a swatch is as tall.
	e.ShowAllValues(u)
	e.Search("address", u)
	run(20)
	heights := map[float32]bool{}
	for _, k := range []string{"address.chevron", "address.crumb", "address.height"} {
		b, ok := u.Bounds(e.all.rowOf(t, k).line)
		if !ok {
			t.Fatalf("no bounds for %q", k)
		}
		heights[b.Size().H] = true
	}
	if len(heights) != 1 {
		t.Fatalf("the rows are of heights %v", heights)
	}
	if n := ctlOf[*numberControl](t, e.all.rowOf(t, "address.height")); n.field.Height.Key() != CompactHeight.Key() {
		t.Fatal("a number in All values is not compact")
	}
}

// clickOn clicks the middle of n.
func clickOn(t *testing.T, w *gunim.Window, u *gunim.UI, run func(int), n gunim.Node) {
	t.Helper()
	r, ok := u.Bounds(n)
	if !ok {
		t.Fatalf("%T is not laid out", n)
	}
	at := r.Center()
	w.Input(input.PointerMove{Pos: at, Time: time.Now()})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	run(2)
}

// With OnSave, the edits are a draft: Save hands them to the program,
// and Discard takes them back to those last saved.
func TestADraftIsKeptOnlyBySave(t *testing.T) {
	var saves []theme.Theme
	saved := theme.Make("dark", theme.Set(widget.Gap, float32(20)))
	e, w, u, run, _ := edStage(t, Options{Base: widget.Dark(), Overrides: saved, Sections: []Section{cursorSection},
		OnSave: func(over theme.Theme, _ *gunim.UI) gunim.Intent { saves = append(saves, over); return nil }})
	if e.save == nil || e.discard == nil {
		t.Fatal("the header has no Save and Discard")
	}
	if e.Unsaved() || !e.save.Disabled || !e.discard.Disabled {
		t.Fatal("opened, the editor has changes to save")
	}
	red := color.NRGBA{R: 0xff, A: 0xff}
	if err := e.Set(widget.Accent.Key(), red, true, u); err != nil {
		t.Fatal(err)
	}
	run(1)
	if !e.Unsaved() || e.save.Disabled || e.discard.Disabled || !strings.Contains(e.count.Text, "not saved") {
		t.Fatalf("edited, unsaved %v, Save off %v, Discard off %v, the count says %q", e.Unsaved(), e.save.Disabled, e.discard.Disabled, e.count.Text)
	}
	if len(saves) != 0 {
		t.Fatal("an edit was saved before Save")
	}
	clickOn(t, w, u, run, e.discard)
	if e.Unsaved() || e.Overrides().Has(widget.Accent.Key()) || !e.Overrides().Has(widget.Gap.Key()) {
		t.Fatalf("discarded, the overrides are %v, want those saved", e.Overrides().Keys())
	}
	if err := e.Set(widget.Accent.Key(), red, true, u); err != nil {
		t.Fatal(err)
	}
	run(1)
	clickOn(t, w, u, run, e.save)
	if len(saves) != 1 || valueIn(saves[0], widget.Accent) != red || valueIn(saves[0], widget.Gap) != 20 {
		t.Fatalf("Save handed the program %v", saves)
	}
	if e.Unsaved() || !e.save.Disabled || strings.Contains(e.count.Text, "not saved") {
		t.Fatal("saved, the editor still has changes to save")
	}
	// Reset all is a draft too, and so is going back to what was saved.
	e.ResetAll(u)
	if !e.Unsaved() {
		t.Fatal("Reset all left nothing to save")
	}
	e.Undo(u)
	if e.Unsaved() {
		t.Fatal("undone back to the values saved, the editor has changes to save")
	}
	// Values the program gives are saved ones.
	e.SetOverrides(theme.Make("dark"), u)
	if e.Unsaved() {
		t.Fatal("given new overrides, the editor has changes to save")
	}
}

// Without OnSave, every edit is live, and there is nothing to save.
func TestWithoutOnSaveThereIsNoSave(t *testing.T) {
	e, _, u, _, _ := edStage(t, Options{Base: widget.Dark(), Sections: []Section{cursorSection}})
	if err := e.Set(widget.Accent.Key(), color.NRGBA{R: 0xff, A: 0xff}, true, u); err != nil {
		t.Fatal(err)
	}
	if e.save != nil || e.discard != nil || e.Unsaved() || strings.Contains(e.count.Text, "not saved") {
		t.Fatal("an editor without OnSave has a draft")
	}
}

// The preview's window is called Notes over the editor's own widgets,
// has no name over the program's own preview, and the name the program
// gives over either.
func TestThePreviewsWindowTakesTheProgramsName(t *testing.T) {
	for _, c := range []struct {
		what  string
		o     Options
		title string
	}{
		{"the editor's widgets", Options{Base: widget.Dark()}, "Notes"},
		{"the program's own preview", Options{Base: widget.Dark(), Preview: widget.NewLabel("Mine")}, ""},
		{"the program's own preview, named", Options{Base: widget.Dark(), Preview: widget.NewLabel("Mine"),
			PreviewTitle: "Studio"}, "Studio"},
		{"the editor's widgets, named", Options{Base: widget.Dark(), PreviewTitle: "Studio"}, "Studio"},
	} {
		e, _, _, _, _ := edStage(t, c.o)
		s := e.preview.scene
		if s.name != c.title || (s.title.Advance > 0) != (c.title != "") {
			t.Errorf("over %s, the window is called %q, %v wide, want %q", c.what, s.name, s.title.Advance, c.title)
		}
	}
}
