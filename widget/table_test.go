package widget

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

type tableEvents struct {
	activated []Key
	sorts     [][2]int
}

// newTableStage mounts a table of n rows, named row 0 on, with a size
// column, in a 600 by 400 window, with the keyboard.
func newTableStage(t *testing.T, n int) (*gunim.Window, *Table, *tableEvents, func(int)) {
	t.Helper()
	ev := &tableEvents{}
	tbl := NewTable(TableColumn{Title: "Name"}, TableColumn{Title: "Size", Width: 100, End: true})
	tbl.Row = func(k Key) TableRow { return TableRow{Cells: []string{"row " + string(k), string(k) + " KB"}} }
	tbl.OnActivate = func(k Key, _ *gunim.UI) gunim.Intent { ev.activated = append(ev.activated, k); return nil }
	tbl.OnSort = func(col int, desc bool, _ *gunim.UI) gunim.Intent {
		d := 0
		if desc {
			d = 1
		}
		ev.sorts = append(ev.sorts, [2]int{col, d})
		return nil
	}
	keys := make([]Key, n)
	for i := range keys {
		keys[i] = Key(strconv.Itoa(i))
	}
	w := gunimtest.New(t, geom.Sz(600, 400), nil)
	gunim.RegisterView(w, "t", func(struct{}) *Table { return tbl },
		func(tb *Table, _ struct{}, u *gunim.UI) { tb.SetKeys(keys, u) })
	if err := w.Client().Mount(gunim.Root, "t", "t", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Update("t", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("t"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(10)
	return w, tbl, ev, run
}

func TestATableBuildsOnlyTheRowsInView(t *testing.T) {
	_, tbl, _, _ := newTableStage(t, 1000)
	if n := tbl.list.Len(); n != 1000 {
		t.Fatalf("the table holds %d rows, want 1000", n)
	}
	if n := tbl.list.Built(); n == 0 || n > 40 {
		t.Fatalf("the table built %d rows for a view of about 14", n)
	}
}

func TestTheKeysMoveTheCursorAndTheViewFollows(t *testing.T) {
	w, tbl, ev, run := newTableStage(t, 1000)
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	if k, _ := tbl.Cursor(); k != "2" {
		t.Fatalf("after Down twice the cursor is on %q, want 2", k)
	}
	w.Input(input.KeyPress{Key: input.KeyEnd})
	run(60)
	if k, _ := tbl.Cursor(); k != "999" {
		t.Fatalf("after End the cursor is on %q, want 999", k)
	}
	if tbl.list.Offset() <= 0 {
		t.Fatal("after End the view stayed at the top")
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if !slices.Equal(ev.activated, []Key{"999"}) {
		t.Fatalf("activated %v, want 999", ev.activated)
	}
}

func TestSpaceMarksAndMovesOn(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 10)
	w.Input(input.KeyPress{Key: input.KeySpace})
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeySpace})
	run(1)
	if got := tbl.Marked(); !slices.Equal(got, []Key{"0", "2"}) {
		t.Fatalf("marked %v, want 0 and 2", got)
	}
	if k, _ := tbl.Cursor(); k != "3" {
		t.Fatalf("the cursor is on %q, want 3", k)
	}
}

func TestAClickOnATitleSortsAndAgainTheOtherWay(t *testing.T) {
	w, _, ev, run := newTableStage(t, 10)
	for range 2 {
		w.Input(input.PointerDown{Pos: geom.Pt(40, 13), Clicks: 1, Time: time.Now()})
		w.Input(input.PointerUp{Pos: geom.Pt(40, 13), Time: time.Now()})
		run(1)
	}
	if !slices.Equal(ev.sorts, [][2]int{{0, 0}, {0, 1}}) {
		t.Fatalf("sorts %v, want the names up, then down", ev.sorts)
	}
	// A double click sorts once, and a right click not at all.
	ev.sorts = nil
	w.Input(input.PointerDown{Pos: geom.Pt(40, 13), Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(40, 13), Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(40, 13), Clicks: 2, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(40, 13), Time: time.Now()})
	w.Input(input.PointerDown{Pos: geom.Pt(40, 13), Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(40, 13), Button: input.ButtonSecondary, Time: time.Now()})
	run(1)
	if !slices.Equal(ev.sorts, [][2]int{{0, 0}}) {
		t.Fatalf("a double click and a right click sorted %v, want the names up once", ev.sorts)
	}
}

func TestTheCursorStaysOnItsRowAsTheRowsReorder(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 10)
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(1)
	// Reverse the rows; row 1 moves to the second last place.
	rev := make([]Key, 10)
	for i := range rev {
		rev[i] = Key(strconv.Itoa(9 - i))
	}
	gunim.RegisterView(w, "flip", func(struct{}) *Label { return NewLabel("") },
		func(_ *Label, _ struct{}, u *gunim.UI) { tbl.SetKeys(rev, u) })
	if err := w.Client().Mount(gunim.Root, "flip", "flip", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Update("flip", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(2)
	if k, _ := tbl.Cursor(); k != "1" {
		t.Fatalf("after the rows reversed the cursor is on %q, want 1", k)
	}
}

func TestTypingFindsARowByTheStartOfItsName(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 300)
	now := time.Now()
	// "row 2" is row 2; "row 25" comes further on.
	w.Input(input.TextInput{Text: "ROW 2", Time: now})
	w.Input(input.TextInput{Text: "5", Time: now.Add(200 * time.Millisecond)})
	run(1)
	if k, _ := tbl.Cursor(); k != "25" {
		t.Fatalf("typing row 25 put the cursor on %q", k)
	}
	// After a pause, typing starts a new name.
	w.Input(input.TextInput{Text: "row 7", Time: now.Add(3 * time.Second)})
	run(1)
	if k, _ := tbl.Cursor(); k != "7" {
		t.Fatalf("typing row 7 after a pause put the cursor on %q", k)
	}
}

// Typing finds the names the rows had when SetKeys last ran; SetKeys again, with the same keys, finds new ones.
func TestTypingFindsTheNamesOfTheKeysSetLast(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 10)
	now := time.Now()
	w.Input(input.TextInput{Text: "r", Time: now})
	run(1)
	row := tbl.Row
	tbl.Row = func(k Key) TableRow {
		if k == "7" {
			return TableRow{Cells: []string{"zebra"}}
		}
		return row(k)
	}
	w.Input(input.TextInput{Text: "zebra", Time: now.Add(3 * time.Second)})
	run(1)
	if k, _ := tbl.Cursor(); k == "7" {
		t.Fatal("typing found a name the rows took after SetKeys")
	}
	if err := w.Client().Update("t", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	w.Input(input.TextInput{Text: "zebra", Time: now.Add(6 * time.Second)})
	run(1)
	if k, _ := tbl.Cursor(); k != "7" {
		t.Fatalf("after SetKeys again, typing zebra put the cursor on %q, want row 7", k)
	}
}

func TestSpaceTwiceMarksTwoRows(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 10)
	now := time.Now()
	for i := range 2 {
		at := now.Add(time.Duration(i) * 100 * time.Millisecond)
		w.Input(input.KeyPress{Key: input.KeySpace, Typed: true, Time: at})
		w.Input(input.TextInput{Text: " ", Time: at})
	}
	run(1)
	if got := tbl.Marked(); !slices.Equal(got, []Key{"0", "1"}) {
		t.Fatalf("marked %v, want 0 and 1", got)
	}
}

// Backspace takes back a letter of a name being found, Escape gives up
// finding, and after that both are left to whatever holds the table.
func TestBackspaceAndEscapeWorkOnANameBeingFound(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 300)
	now := time.Now()
	w.Input(input.TextInput{Text: "row 25", Time: now})
	run(1)
	w.Input(input.KeyPress{Key: input.KeyBackspace, Time: now.Add(100 * time.Millisecond)})
	run(1)
	if k, _ := tbl.Cursor(); k != "2" {
		t.Fatalf("taking back the 5 left the cursor on %q, want row 2", k)
	}
	w.Input(input.KeyPress{Key: input.KeyEscape, Time: now.Add(200 * time.Millisecond)})
	run(1)
	if tbl.finding(now.Add(300 * time.Millisecond)) {
		t.Fatal("Escape left the name being found")
	}
}

func TestInsertMarksAndMovesOn(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 10)
	w.Input(input.KeyPress{Key: input.KeyInsert})
	run(1)
	if !slices.Equal(tbl.Marked(), []Key{"0"}) {
		t.Fatalf("Insert marked %v", tbl.Marked())
	}
	if k, _ := tbl.Cursor(); k != "1" {
		t.Fatalf("Insert left the cursor on %q", k)
	}
}

// Up past the top of the view scrolls it back, as Down past the bottom
// does.
func TestUpPastTheTopOfTheViewScrollsBack(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 1000)
	w.Input(input.KeyPress{Key: input.KeyEnd})
	run(60)
	for range 40 {
		w.Input(input.KeyPress{Key: input.KeyUp})
		run(1)
	}
	run(60)
	k, _ := tbl.Cursor()
	h := TableRowHeight.Get(nil)
	top := float32(tbl.cursor) * h
	if off := tbl.list.Offset(); top < off || top+h > off+tbl.list.viewport {
		t.Fatalf("the cursor, on %s at %v, is out of the view from %v to %v", k, top, off, off+tbl.list.viewport)
	}
}

// jumpState is what the jump test's view shows: rows, and the row to
// put the cursor on at once, if any.
type jumpState struct {
	Keys []Key
	Jump Key
}

// A table given new rows, and put on one at once, shows them in place
// rather than gliding them in from where the rows before were scrolled
// to.
func TestJumpToShowsNewRowsInPlace(t *testing.T) {
	tbl := NewTable(TableColumn{Title: "Name"})
	tbl.Row = func(k Key) TableRow { return TableRow{Cells: []string{string(k)}} }
	w := gunimtest.New(t, geom.Sz(600, 400), nil)
	gunim.RegisterView(w, "t", func(jumpState) *Table { return tbl }, func(tb *Table, st jumpState, u *gunim.UI) {
		tb.SetKeys(st.Keys, u)
		if st.Jump != "" {
			tb.JumpTo(st.Jump, u)
		}
	})
	many := make([]Key, 1000)
	for i := range many {
		many[i] = Key(strconv.Itoa(i))
	}
	c := w.Client()
	if err := c.Mount(gunim.Root, "t", "t", jumpState{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Update("t", jumpState{Keys: many}); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("t"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	w.Input(input.KeyPress{Key: input.KeyEnd})
	run(60)
	if tbl.list.Offset() <= 0 {
		t.Fatal("after End the view stayed at the top")
	}
	if err := c.Update("t", jumpState{Keys: []Key{"a", "b", "c"}, Jump: "b"}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if off := tbl.list.Offset(); off != 0 || tbl.list.offset.Active() {
		t.Fatalf("the new rows are shown from %v, moving %v; want at once at the top", off, tbl.list.offset.Active())
	}
	if k, _ := tbl.Cursor(); k != "b" {
		t.Fatalf("the cursor is on %q, want b", k)
	}
}

// A table keeps a Space from the window's keys while a name is being found, so it goes into the name; alone, or after
// a pause, the Space is the window's.
func TestATableKeepsASpaceWhileFinding(t *testing.T) {
	w, tbl, _, run := newTableStage(t, 300)
	now := time.Now()
	space := func(at time.Time) input.KeyPress { return input.KeyPress{Key: input.KeySpace, Typed: true, Time: at} }
	if tbl.KeepsKey(space(now), nil) {
		t.Fatal("a table finding nothing keeps a Space")
	}
	w.Input(input.TextInput{Text: "row", Time: now})
	run(1)
	if !tbl.KeepsKey(space(now.Add(200*time.Millisecond)), nil) {
		t.Fatal("a table finding \"row\" leaves a Space to the window")
	}
	if tbl.KeepsKey(space(now.Add(3*time.Second)), nil) {
		t.Fatal("a table keeps a Space after a pause in typing")
	}
}
