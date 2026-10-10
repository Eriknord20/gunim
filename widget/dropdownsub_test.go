package widget

import (
	"fmt"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// songStage holds a drop-down of the categories Calm and Rounds, each with songs in its submenu, and Greek Themes on
// its own. A pick of a song sends its path, and a pick of Greek Themes its index.
func songStage(t *testing.T) (w *gunim.Window, d *Dropdown, run func(int)) {
	t.Helper()
	d = NewDropdown([]MenuItem{
		{Label: "Calm", Sub: &Submenu{Items: Labels("Star Drift", "Candy Clouds")}},
		{Label: "Rounds", Sub: &Submenu{Items: Labels("A round song", "Keypad Round", "A round song made in code, long")}},
		{Label: "Greek Themes"},
	})
	d.SetSelectedPath([]int{0, 0}, nil)
	d.OnChange = func(i int, _ *gunim.UI) gunim.Intent { return chose{i} }
	d.OnChangeSub = func(path []int, _ *gunim.UI) gunim.Intent { return subPicked{fmt.Sprint(path)} }
	w, run = stage(t, &frame{child: d, size: geom.Sz(400, 36)})
	run(1)
	return w, d, run
}

// clickOpen opens the drop-down's list with a click, which gives it the keys.
func clickOpen(w *gunim.Window, run func(int)) {
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18)})
	run(1)
}

func TestADropdownPicksAnItemOfASubmenu(t *testing.T) {
	w, d, run := songStage(t)
	if got := d.Access().Value; got != "Star Drift" {
		t.Fatalf("the drop-down shows %q, want the song chosen, Star Drift", got)
	}
	clickOpen(w, run)
	typeKeys(w, run, 0, input.KeyDown, input.KeyRight)
	s := d.menu.sub
	if s == nil || s.item != 1 || !s.keys {
		t.Fatal("Right on Rounds did not open its submenu with the keys")
	}
	typeKeys(w, run, 0, input.KeyDown, input.KeyEnter)
	run(2)
	if d.IsOpen() {
		t.Fatal("a pick in the submenu left the list open")
	}
	if got := d.SelectedPath(); !slices.Equal(got, []int{1, 1}) || d.Selected() != 1 {
		t.Fatalf("the drop-down chose %v, Selected %d; want [1 1] and 1", got, d.Selected())
	}
	if got := sent(w); len(got) != 1 || got[0] != (subPicked{"[1 1]"}) {
		t.Fatalf("picking Keypad Round sent %v, want its path once", got)
	}
	if got := d.Access().Value; got != "Keypad Round" {
		t.Fatalf("the drop-down shows %q after the pick", got)
	}
}

func TestADropdownOpensWithTheChosenItemOfASubmenuHighlighted(t *testing.T) {
	w, d, run := songStage(t)
	d.SetSelectedPath([]int{1, 2}, nil)
	clickOpen(w, run)
	if d.menu.Highlighted() != 1 {
		t.Fatalf("the list opened on %d, want Rounds, 1", d.menu.Highlighted())
	}
	typeKeys(w, run, 0, input.KeyRight)
	if s := d.menu.sub; s == nil || s.menu.Highlighted() != 2 {
		t.Fatal("the submenu of Rounds did not open on the song chosen")
	}
	// Calm's submenu opens on its first song, as any submenu does.
	typeKeys(w, run, 0, input.KeyLeft, input.KeyUp, input.KeyRight)
	if s := d.menu.sub; s == nil || s.item != 0 || s.menu.Highlighted() != 0 {
		t.Fatal("the submenu of Calm did not open on its first song")
	}
	// Picking the song chosen again sends nothing.
	typeKeys(w, run, 0, input.KeyLeft, input.KeyDown, input.KeyRight, input.KeyEnter)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("picking the song chosen sent %v", got)
	}
	// An item of the list is chosen as before, through OnChange.
	typeKeys(w, run, 0, input.KeySpace, input.KeyDown, input.KeyEnter)
	if got := sent(w); len(got) != 1 || got[0] != (chose{2}) || !slices.Equal(d.SelectedPath(), []int{2}) {
		t.Fatalf("picking Greek Themes sent %v and chose %v", got, d.SelectedPath())
	}
}

func TestADropdownIsWideEnoughForTheItemsOfItsSubmenus(t *testing.T) {
	_, d, _ := songStage(t)
	size := d.Layout(gunim.Loose(geom.Sz(1000, 100)), gunim.Frame{Scale: 1}, gunim.Children{})
	long := faceIn(Font, nil).Shape("A round song made in code, long", TextSize.Default()).Advance
	if size.W < long {
		t.Fatalf("the drop-down is %v wide, narrower than its longest song, %v", size.W, long)
	}
}

func TestADropdownChoosesAPathAsItIsSet(t *testing.T) {
	d := NewDropdown([]MenuItem{{Label: "Calm", Sub: &Submenu{Items: Labels("Star Drift")}}, {Label: "Greek Themes"}})
	for _, c := range []struct {
		set, want []int
		value     string
	}{
		{[]int{0, 0}, []int{0, 0}, "Star Drift"},
		{nil, []int{0}, "Calm"},
		{[]int{1}, []int{1}, "Greek Themes"},
		{[]int{0, 5}, []int{0, 5}, ""},
	} {
		d.SetSelectedPath(c.set, nil)
		if got := d.SelectedPath(); !slices.Equal(got, c.want) || d.Access().Value != c.value {
			t.Fatalf("SetSelectedPath(%v) chose %v showing %q, want %v showing %q", c.set, got, d.Access().Value, c.want, c.value)
		}
	}
	d.SetSelected(1, nil)
	if !slices.Equal(d.SelectedPath(), []int{1}) {
		t.Fatalf("SetSelected(1) chose %v", d.SelectedPath())
	}
}
