package widget

import (
	"slices"
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/text"
)

// openPicker stages a button that opens an emoji picker, clicks it, and returns the picker, the window, and what
// was picked so far.
func openPicker(t *testing.T) (picker *EmojiPicker, w *gunim.Window, run func(int), picks *[]string) {
	t.Helper()
	if !text.EmojiShows("\U0001F44D") {
		t.Skip("no colour emoji font here")
	}
	var picked []string
	picker = &EmojiPicker{OnPick: func(s string, _ *gunim.UI) gunim.Intent { picked = append(picked, s); return nil }}
	button := NewButton("Emoji")
	button.OnClick = func(u *gunim.UI) gunim.Intent { picker.Open(button, geom.Rc(0, 0, 80, 36), u); return nil }
	w, run = stage(t, &frame{child: button, size: geom.Sz(120, 36)})
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary})
	run(10)
	if !picker.IsOpen() {
		t.Fatal("the picker did not open")
	}
	return picker, w, run, &picked
}

func TestTheEmojiPickerShowsTheGroups(t *testing.T) {
	picker, _, _, _ := openPicker(t)
	c := picker.card
	if len(c.tabs.groups) < 5 {
		t.Fatalf("%d group tabs, want the groups of emoji", len(c.tabs.groups))
	}
	if c.first.Text != "\U0001F600" {
		t.Fatalf("the first emoji is %q, want the grinning face", c.first.Text)
	}
	if _, ok := c.rows["h:0"]; !ok {
		t.Fatal("the first group has no title row")
	}
}

func TestTypingNarrowsThePickerAndEnterPicks(t *testing.T) {
	picker, w, run, picked := openPicker(t)
	w.Input(input.TextInput{Text: "thumbs"})
	run(3)
	for k := range picker.card.rows {
		if !strings.HasPrefix(string(k), "s:") {
			t.Fatalf("row %q shows while searching", k)
		}
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(3)
	if len(*picked) != 1 || (*picked)[0] != "\U0001F44D" {
		t.Fatalf("picked %q, want thumbs up", *picked)
	}
	if picker.IsOpen() {
		t.Fatal("the picker is still open after picking")
	}
}

func TestEscapeClosesThePicker(t *testing.T) {
	picker, w, run, picked := openPicker(t)
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(3)
	if picker.IsOpen() || len(*picked) != 0 {
		t.Fatalf("after Escape: open %v, picked %q", picker.IsOpen(), *picked)
	}
}

func TestPickedEmojiGoFirstInRecentlyUsed(t *testing.T) {
	picker, w, run, picked := openPicker(t)
	pick := func(q string) {
		w.Input(input.TextInput{Text: q})
		run(3)
		w.Input(input.KeyPress{Key: input.KeyEnter})
		run(3)
	}
	pick("thumbs")
	reopen := func() {
		w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary})
		run(10)
	}
	reopen()
	pick("rocket")
	reopen()
	pick("thumbs")
	if want := []string{"\U0001F44D", "\U0001F680"}; !slices.Equal(picker.Recent, want) {
		t.Fatalf("recent %q, want thumbs up before the rocket, once each", picker.Recent)
	}
	if len(*picked) != 3 {
		t.Fatalf("picked %q, want three picks", *picked)
	}
	reopen()
	c := picker.card
	if len(c.tabs.groups) == 0 || c.tabs.groups[0].name != "Recently used" || c.first.Text != "\U0001F44D" {
		t.Fatalf("first tab %+v and first emoji %q, want the recently used group first", c.tabs.groups[0], c.first.Text)
	}
	if r := c.rows["r:0"]; len(r.emoji) != 2 {
		t.Fatalf("recently used row %+v, want the two picked", r)
	}
}

func TestTheTabsBarFollowsTheGroupShown(t *testing.T) {
	picker, _, run, _ := openPicker(t)
	c := picker.card
	if got := c.shownGroup(); got != 0 {
		t.Fatalf("group %d shows at the start, want the first", got)
	}
	at := slices.Index(c.list.order, c.tabs.groups[2].key)
	c.list.ScrollTo(float32(c.list.tops.sum(at)), Quick.Default())
	run(90)
	if got := c.tabs.shown; got != 2 {
		t.Fatalf("the bar is under tab %d, want the third, scrolled to", got)
	}
}

func TestTheTabsBarGoesStraightToAGroupToTheLeft(t *testing.T) {
	picker, _, run, _ := openPicker(t)
	c := picker.card
	scrollTo := func(g int) {
		at := slices.Index(c.list.order, c.tabs.groups[g].key)
		c.list.ScrollTo(float32(c.list.tops.sum(at)), Quick.Default())
	}
	scrollTo(4)
	run(90)
	// Scroll back to the third group, as its tab does. The bar may settle with a sliver of overshoot, but never
	// visits the second tab.
	scrollTo(2)
	last := float32(4)
	for range 90 {
		run(1)
		v := c.tabs.at.Value()
		if v < 2-0.05 || v > last+0.05 {
			t.Fatalf("the bar went to %v on its way from the fifth tab to the third", v)
		}
		last = v
	}
	if c.tabs.shown != 2 {
		t.Fatalf("the bar is under tab %d, want the third", c.tabs.shown)
	}
}
