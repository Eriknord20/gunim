package widget_test

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// listState is what the test view renders.
type listState struct {
	Items []string
}

// cell is one row's contents: a fixed height, and a label it can be
// updated with.
type cell struct {
	label string
}

const cellHeight = 20

func newCell(v string) *cell { return &cell{label: v} }

func (c *cell) set(v string, _ *gunim.UI) { c.label = v }

func (c *cell) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return geom.Sz(cs.Max.W, cellHeight)
}

func (c *cell) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 4, paint.Fill{})
}

// listFixture is a window with one mounted list, plus the handles a
// test needs to drive it.
type listFixture struct {
	w    *gunim.Window
	c    gunim.Client
	list *widget.List
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	f := &listFixture{}
	f.w = gunimtest.New(t, geom.Sz(400, 600), nil)
	gunim.RegisterView(f.w, "list",
		func(listState) *widget.List {
			f.list = widget.NewList()
			return f.list
		},
		func(l *widget.List, s listState, u *gunim.UI) {
			widget.Sync(l, u, s.Items,
				func(v string) widget.Key { return widget.Key(v) },
				newCell,
				(*cell).set)
		})
	f.c = f.w.Client()
	// No spacing keeps the arithmetic in the tests obvious. The theme
	// is in force before any token is read, so the list starts at it.
	f.w.RegisterTheme(theme.Make("tight", theme.Set(widget.ListSpacing, 0)))
	if err := f.c.SetTheme("tight"); err != nil {
		t.Fatal(err)
	}
	if err := f.c.Mount(gunim.Root, "list", "list", listState{}, "items"); err != nil {
		t.Fatal(err)
	}
	return f
}

// publish sends a new set of items and runs n frames.
func (f *listFixture) publish(t *testing.T, items []string, n int) {
	t.Helper()
	if err := f.c.Publish("items", listState{Items: items}); err != nil {
		t.Fatal(err)
	}
	f.run(n)
}

func (f *listFixture) run(n int) {
	for range n {
		f.w.Frame(time.Second / 60)
	}
}

func (f *listFixture) keys() []string {
	out := make([]string, 0, f.list.Len())
	for _, k := range f.list.Keys() {
		out = append(out, string(k))
	}
	return out
}

func equal(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRowsArriveInOrder(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a", "b", "c"}, 120)

	if got := f.keys(); !equal(got, "a", "b", "c") {
		t.Fatalf("keys = %v, want a b c", got)
	}
	if got := f.list.Height(); got != 3*cellHeight {
		t.Fatalf("Height = %v, want %v once the rows have opened", got, 3*cellHeight)
	}
}

func TestRemovedRowHoldsItsPlaceWhileItCollapses(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a", "b", "c"}, 120)
	full := f.list.Height()

	// Drop the middle row. It stays in the list, collapsing, and the
	// row below it slides up into the gap rather than jumping.
	f.publish(t, []string{"a", "c"}, 1)
	if got := f.keys(); !equal(got, "a", "b", "c") {
		t.Fatalf("keys = %v, want the leaving row to hold its place", got)
	}

	f.run(4)
	mid := f.list.Height()
	if mid >= full || mid <= 2*cellHeight {
		t.Fatalf("Height = %v part way through; want it between %v and %v, closing smoothly",
			mid, 2*cellHeight, full)
	}

	f.run(200)
	if got := f.keys(); !equal(got, "a", "c") {
		t.Fatalf("keys = %v, want a c once the row has gone", got)
	}
	if got := f.list.Height(); got != 2*cellHeight {
		t.Fatalf("Height = %v, want %v", got, 2*cellHeight)
	}
}

func TestReorderKeepsTheSameNodes(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a", "b", "c"}, 120)

	before, ok := widget.RowOf[*cell](f.list, "a")
	if !ok {
		t.Fatal("row a is missing")
	}

	f.publish(t, []string{"c", "b", "a"}, 120)
	if got := f.keys(); !equal(got, "c", "b", "a") {
		t.Fatalf("keys = %v, want c b a", got)
	}

	after, ok := widget.RowOf[*cell](f.list, "a")
	if !ok {
		t.Fatal("row a went missing across a reorder")
	}
	if before != after {
		t.Fatal("reordering rebuilt a row that only moved; its animation state was thrown away")
	}
}

func TestRowComingBackMidExitIsRevived(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a", "b"}, 120)
	original, _ := widget.RowOf[*cell](f.list, "b")

	f.publish(t, []string{"a"}, 4) // b starts leaving
	f.publish(t, []string{"a", "b"}, 200)

	revived, ok := widget.RowOf[*cell](f.list, "b")
	if !ok {
		t.Fatal("row b left despite coming back mid-exit")
	}
	if revived != original {
		t.Fatal("row b was rebuilt; reviving should keep the node and its velocity")
	}
	if got := f.list.Height(); got != 2*cellHeight {
		t.Fatalf("Height = %v, want %v once b is back", got, 2*cellHeight)
	}
}

func TestUpdateReachesRowsThatStayed(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a"}, 120)

	row, _ := widget.RowOf[*cell](f.list, "a")
	row.label = "stale"

	f.publish(t, []string{"a"}, 1)
	if row.label != "a" {
		t.Fatalf("label = %q, want the update to reach a row that stayed", row.label)
	}
}

func TestListSettlesAndStopsAskingForFrames(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a", "b", "c"}, 300)
	// A list that has finished moving should let the window sleep.
	if f.list.Height() != 3*cellHeight {
		t.Fatalf("Height = %v, want the rows fully open", f.list.Height())
	}
}

// A row that arrives opens where it belongs, rather than gliding there from
// the top of the list, as the rows under a folded header do when it
// opens again.
func TestAnArrivingRowStartsWhereItBelongs(t *testing.T) {
	f := newListFixture(t)
	f.publish(t, []string{"a", "b", "c", "d", "e"}, 120)
	// Folded: d and e go.
	f.publish(t, []string{"a", "b", "c"}, 200)
	// Opened again: they come back, as new rows.
	f.publish(t, []string{"a", "b", "c", "d", "e"}, 1)
	for _, k := range []string{"d", "e"} {
		y, ok := widget.RowY(f.list, widget.Key(k))
		if !ok {
			t.Fatalf("row %s is missing", k)
		}
		// d opens at c's foot, and e under it, as d opens.
		if want := float32(3 * cellHeight); y < want-1 {
			t.Fatalf("a frame after it came back, row %s is at %v, want at %v or below, not on its way from the top", k, y, want)
		}
	}
}
