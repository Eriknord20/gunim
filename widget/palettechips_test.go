package widget

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
)

// An item with chips is picked with the chip lit: the item's own at
// first, then the one Tab, Shift+Tab, Right and Left move the light to.
func TestAPalettePicksTheChipLit(t *testing.T) {
	w, o, run := newPaletteStage(t)
	type pick struct{ i, chip int }
	var chips []pick
	o.p.Items = append(o.p.Items, PaletteItem{Title: "skylake", Chips: []string{"Terminal", "Files", "Connection Log"}, Chip: 1})
	o.p.OnChip = func(i, chip int, _ *gunim.UI) gunim.Intent { chips = append(chips, pick{i, chip}); return nil }
	focusOpener(w, run)
	open := func(q string) {
		t.Helper()
		w.Input(input.KeyPress{Key: input.KeyF1})
		run(20)
		w.Input(input.TextInput{Text: q})
		run(20)
		if f := o.p.card.found; len(f) == 0 || f[0].Index != 4 {
			t.Fatalf("typing %q found %v, want skylake first", q, f)
		}
	}
	keys := func(ks ...input.KeyPress) {
		for _, k := range ks {
			w.Input(k)
			run(5)
		}
	}
	tab, back := input.KeyPress{Key: input.KeyTab}, input.KeyPress{Key: input.KeyTab, Mods: input.ModShift}
	right, left := input.KeyPress{Key: input.KeyRight}, input.KeyPress{Key: input.KeyLeft}
	enter := input.KeyPress{Key: input.KeyEnter}

	open("sky")
	keys(enter)
	open("sky")
	keys(tab, tab, enter)
	open("sky")
	keys(back, back, enter)
	// Right goes along the chips from the end of what is typed, and
	// stops at the last; Left moves the caret first.
	open("sky")
	keys(right, right, right, left, enter)
	want := []pick{{4, 1}, {4, 0}, {4, 2}, {4, 2}}
	if len(chips) != len(want) {
		t.Fatalf("picked chips %v, want %v", chips, want)
	}
	for k := range want {
		if chips[k] != want[k] {
			t.Fatalf("picked chips %v, want %v", chips, want)
		}
	}
	if len(o.picked) != 0 {
		t.Fatalf("OnPick heard %v for an item with chips", o.picked)
	}
}
