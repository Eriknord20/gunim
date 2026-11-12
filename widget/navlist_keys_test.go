package widget

import (
	"fmt"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
)

type (
	dropFocus struct{}
	cursorNav struct{ I int }
)

// keyedPages are navPages with a Key each.
func keyedPages() []NavItem {
	items := navPages()
	for i, k := range []Key{"overview", "storage", "activity", "cantsync", "settings"} {
		items[i].Key = k
	}
	return items
}

// movedPages are keyedPages with a row come in at the top, and Activity and Can't sync changed places.
func movedPages() []NavItem {
	was := keyedPages()
	return []NavItem{{Key: "shared", Label: "Shared with me", Icon: icon.Folder}, was[0], was[1], was[3], was[2], was[4]}
}

// stageKeyedNav stages n with patches that drop focus, and that put its ring on a row.
func stageKeyedNav(t *testing.T, n *NavList) (w *gunim.Window, run func(int)) {
	t.Helper()
	w, run = stageNav(t, n)
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ dropFocus, u *gunim.UI) { u.Focus(nil) })
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c cursorNav, u *gunim.UI) { n.SetCursor(c.I, u) })
	return w, run
}

func patchNav(t *testing.T, w *gunim.Window, v any) {
	t.Helper()
	if err := w.Client().Patch("stage", v); err != nil {
		t.Fatal(err)
	}
}

// keyOf returns the key of the row the ring is on.
func keyOf(n *NavList) Key { return n.Items()[n.Cursor()].Key }

// followRing runs frames after rows moved, and fails on any frame where the drawn ring heads away from the cursor's
// row by more than a spring's settling, or rests anywhere else.
func followRing(t *testing.T, n *NavList, run func(int), what string) {
	t.Helper()
	to := n.top(n.Cursor())
	last := n.ringY.Value()
	for f := range 60 {
		run(1)
		y := n.ringY.Value()
		// A spring may pass its row by a pixel or two as it settles.
		if abs32(y-to) > abs32(last-to)+0.01 && abs32(y-to) > 2 {
			t.Fatalf("%s, frame %d: the ring went from %v to %v, away from its row at %v", what, f, last, y, to)
		}
		if y == last && abs32(y-to) > 0.01 {
			t.Fatalf("%s, frame %d: the ring rests at %v, not on its row at %v", what, f, y, to)
		}
		last = y
	}
	if abs32(last-to) > 0.01 {
		t.Fatalf("%s: a second on, the ring is at %v, not on its row at %v", what, last, to)
	}
}

func TestANavListsRingGoesWithItsRowWhenRowsMoveMidGlide(t *testing.T) {
	n := NewNavList(keyedPages()...)
	w, run := stageKeyedNav(t, n)
	run(1)
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(1)
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	if !n.ringY.Active() || keyOf(n) != "activity" {
		t.Fatalf("setup: the ring should be gliding to activity, is on %q, gliding %v", keyOf(n), n.ringY.Active())
	}
	patchNav(t, w, setNavItems{movedPages()})
	run(1)
	if keyOf(n) != "activity" || n.Items()[n.Selected()].Key != "overview" {
		t.Fatalf("after the rows moved the ring is on %q and the choice on %q; want activity and overview",
			keyOf(n), n.Items()[n.Selected()].Key)
	}
	followRing(t, n, run, "rows moved mid-glide")

	// SetCursor and SetItems in one frame.
	patchNav(t, w, setNavItems{keyedPages()})
	run(60)
	patchNav(t, w, cursorNav{0})
	patchNav(t, w, setNavItems{movedPages()})
	run(1)
	if keyOf(n) != "overview" {
		t.Fatalf("SetCursor(0) then the rows moving left the ring on %q, want overview", keyOf(n))
	}
	followRing(t, n, run, "SetCursor and rows moving in one frame")

	// Rows with no Key keep their place, as before.
	plain := NewNavList(navPages()...)
	plain.SetCursor(2, nil)
	plain.SetItems(append([]NavItem{{Label: "Shared with me"}}, navPages()...), nil)
	if plain.Cursor() != 2 {
		t.Fatalf("with no keys the ring went to row %d, want it to keep its place, 2", plain.Cursor())
	}
}

func TestABadgeGoesWithItsRowWhenRowsMove(t *testing.T) {
	n := NewNavList(keyedPages()...)
	w, run := stageNav(t, n)
	run(1)
	was := keyedPages()
	// Can't sync, with its count of 4, moves from row 3 to row 2. Activity moves from row 2 to row 4 and gets a count.
	activity := was[2]
	activity.Count = 7
	patchNav(t, w, setNavItems{[]NavItem{{Key: "shared", Label: "Shared with me"}, was[0], was[3], was[1], activity,
		was[4]}})
	last := float32(-1)
	for f := range 30 {
		run(1)
		b := n.badges[4]
		if b == nil {
			if f == 0 {
				t.Fatal("Activity's new count has no badge growing in at its new row, 4")
			}
		} else {
			// It may pass full size a little as it settles.
			if v := b.shown.Value(); v < last-0.001 && v < 0.99 {
				t.Fatalf("frame %d: Activity's badge shrank from %v to %v", f, last, v)
			} else {
				last = v
			}
		}
		// Can't sync kept its count, and Storage, now in its old place, has none: neither grows or shrinks a badge.
		for _, i := range []int{2, 3} {
			if b := n.badges[i]; b != nil && b.shown.Active() {
				t.Fatalf("frame %d: row %d, %s, has a badge on the move", f, i, n.Items()[i].Label)
			}
		}
	}
	if last < 0.5 {
		t.Fatalf("Activity's badge grew only to %v", last)
	}
}

func TestAScreenReadersHoldStaysOnItsRowWhenRowsMove(t *testing.T) {
	n := NewNavList(keyedPages()...)
	_, run := stageNav(t, n)
	run(1)
	held := n.Access().Parts[2]
	if held.Name != "Activity" {
		t.Fatalf("setup: part 2 is %q", held.Name)
	}
	n.SetItems(movedPages(), nil)
	run(1)
	found := false
	keys := map[uint64]bool{}
	for _, p := range n.Access().Parts {
		if p.Key == held.Key {
			found = true
			if p.Name != "Activity" {
				t.Fatalf("after the rows moved, the part a screen reader held as Activity is %q", p.Name)
			}
		}
		if keys[p.Key] {
			t.Fatalf("two parts share the key %d", p.Key)
		}
		keys[p.Key] = true
	}
	if !found {
		t.Fatal("after the rows moved, no part has the key Activity had")
	}
	plain := NewNavList(navPages()...)
	_, run = stageNav(t, plain)
	run(1)
	if k := plain.Access().Parts[2].Key; k != 3 {
		t.Fatalf("a row with no Key has part key %d, want its place, 3", k)
	}
}

// scrolledNav stages 100 keyed rows in a Scroll 300 high.
func scrolledNav(t *testing.T) (*NavList, *gunim.Window, func(int)) {
	t.Helper()
	items := manyPages(100)
	for i := range items {
		items[i].Key = Key(fmt.Sprint("page", i))
	}
	n := NewNavList(items...)
	n.Label = "Pages"
	w, run := stage(t, &frame{child: NewScroll(&navBox{child: n}), size: geom.Sz(220, 300)})
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, c cursorNav, u *gunim.UI) { n.SetCursor(c.I, u) })
	run(1)
	return n, w, run
}

// activeName returns the name of the part a screen reader hears as active, or "" for none.
func activeName(info access.Info) string {
	if info.Active <= 0 || info.Active > len(info.Parts) {
		return ""
	}
	return info.Parts[info.Active-1].Name
}

func TestSetCursorScrollsItsRowIntoViewAndAScreenReaderHearsIt(t *testing.T) {
	n, w, run := scrolledNav(t)
	if name := activeName(n.Access()); name != "Page 0" {
		t.Fatalf("setup: the active part is %q", name)
	}
	patchNav(t, w, cursorNav{60})
	run(60)
	info := n.Access()
	if n.from == 0 || activeName(info) != "Page 60" || info.Active != 60-n.from+1 {
		t.Fatalf("after SetCursor(60) the rows in view start at %d, and the active part is %d, %q; want row 60 in "+
			"view and heard", n.from, info.Active, activeName(info))
	}
	// The arrow keys scroll too.
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(1)
	w.Input(input.KeyPress{Key: input.KeyEnd})
	run(60)
	if activeName(n.Access()) != "Page 99" {
		t.Fatalf("after End the active part is %q, want Page 99 in view", activeName(n.Access()))
	}
	// A ring out of view has no active part.
	n.cursor = 0
	if a := n.Access().Active; a != 0 {
		t.Fatalf("with the ring on row 0, scrolled away, Active is %d, want 0", a)
	}
}

func TestFocusingANavListAgainKeepsTheRingWhereItWasLeft(t *testing.T) {
	n := NewNavList(keyedPages()...)
	w, run := stageKeyedNav(t, n)
	run(1)
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(1)
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	if n.Cursor() != 2 || n.Selected() != 0 {
		t.Fatalf("two Downs left the ring on %d and the choice on %d, want 2 and 0", n.Cursor(), n.Selected())
	}
	// Focus goes elsewhere, and Tab brings it back.
	patchNav(t, w, dropFocus{})
	run(1)
	w.Input(input.KeyPress{Key: input.KeyTab})
	run(60)
	if n.Cursor() != 2 || n.ringY.Value() != n.top(2) {
		t.Fatalf("focused again, the ring is on row %d, drawn at %v; want it where it was left, row 2 at %v",
			n.Cursor(), n.ringY.Value(), n.top(2))
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if n.Selected() != 2 {
		t.Fatalf("Enter on the ring's row chose %d, want 2", n.Selected())
	}
}

func TestSetCursorPutsANavListsRingOnARow(t *testing.T) {
	n := NewNavList(keyedPages()...)
	n.SetCursor(3, nil)
	if n.Cursor() != 3 || n.ringY.Value() != n.top(3) {
		t.Fatal("SetCursor before layout did not jump the ring to row 3")
	}
	n.SetCursor(9, nil)
	if n.Cursor() != 3 {
		t.Fatalf("SetCursor to a row past the end moved the ring to %d", n.Cursor())
	}
	w, run := stageKeyedNav(t, n)
	run(1)
	patchNav(t, w, cursorNav{1})
	run(1)
	if !n.ringY.Active() {
		t.Fatal("once laid out, SetCursor jumped the ring rather than gliding it")
	}
	run(60)
	if n.Cursor() != 1 || n.ringY.Value() != n.top(1) || n.Selected() != 0 {
		t.Fatalf("SetCursor(1) put the ring on %d and the choice on %d, want 1 and 0", n.Cursor(), n.Selected())
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("SetCursor sent %v", got)
	}

	// A ring on the chosen row goes along when the app chooses another; one left elsewhere stays.
	patchNav(t, w, chooseNav{1})
	run(1)
	patchNav(t, w, chooseNav{4})
	run(60)
	if n.Cursor() != 4 || n.ringY.Value() != n.top(4) {
		t.Fatalf("the ring was on the chosen row, and stayed on %d when row 4 was chosen", n.Cursor())
	}
	patchNav(t, w, cursorNav{2})
	patchNav(t, w, chooseNav{0})
	run(60)
	if n.Cursor() != 2 {
		t.Fatalf("the ring was left on row 2, and went to %d when row 0 was chosen", n.Cursor())
	}
}

func TestWhenTheRingsRowOrTheChosenRowGoesTheyKeepItsPlace(t *testing.T) {
	n := NewNavList(keyedPages()...)
	n.OnSelect = func(i int, _ *gunim.UI) gunim.Intent { return pageChosen{i} }
	n.SetSelected(1, nil)
	n.SetCursor(2, nil)
	w, run := stageKeyedNav(t, n)
	run(1)
	items := keyedPages()
	// Storage, the chosen row, and Activity, the ring's row, go.
	patchNav(t, w, setNavItems{[]NavItem{items[0], items[3], items[4]}})
	run(1)
	if n.Selected() != 1 || keyOf(n) != "settings" {
		t.Fatalf("with their rows gone, the choice is on %d and the ring on %q; want row 1 and settings, row 2",
			n.Selected(), keyOf(n))
	}
	if got := sent(w); len(got) != 0 {
		t.Fatalf("the chosen row going sent %v; want nothing", got)
	}
	followRing(t, n, run, "the ring's row gone")
}

func TestARepeatedKeyFollowsItsFirstRow(t *testing.T) {
	// Repeated in the new rows: the first new row with the key takes the choice.
	n := NewNavList(keyedPages()...)
	n.SetSelected(1, nil)
	items := keyedPages()
	items[3].Key = "storage"
	n.SetItems(items, nil)
	if n.Selected() != 1 {
		t.Fatalf("with storage repeated in the new rows the choice went to %d, want the first, 1", n.Selected())
	}
	// Repeated in the old rows, the first chosen: a row come in at the top moves the choice down with it.
	n = NewNavList(items...)
	n.SetSelected(1, nil)
	n.SetItems(append([]NavItem{{Key: "shared", Label: "Shared with me"}}, items...), nil)
	if n.Selected() != 2 || n.Items()[2].Label != "Storage" {
		t.Fatalf("with storage repeated in the old rows the choice went to %d, want Storage at 2", n.Selected())
	}
}

func TestANavListsLabelNamesItWithoutATooltip(t *testing.T) {
	n := NewNavList(navPages()...)
	n.Label = "Pages"
	_, run := stageNav(t, n)
	run(1)
	if name := n.Access().Name; name != "Pages" || n.Tooltip != "" {
		t.Fatalf("with Label set the list reads as %q, with tooltip %q; want Pages and no tooltip", name, n.Tooltip)
	}
	n.Tooltip = "Move between pages"
	if name := n.Access().Name; name != "Pages" {
		t.Fatalf("with both set the list reads as %q, want its Label", name)
	}
	n.Label = ""
	if name := n.Access().Name; name != "Move between pages" {
		t.Fatalf("with Label empty the list reads as %q, want its tooltip", name)
	}
}
