package dock

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// draw writes window win's tree on one line: an item as its ID, a
// group as [a *b c] with the tab in front starred, and a split as
// (a | b) side by side or (a / b) one above the other.
func draw(l Layout, win int) string {
	w := l.Window(win)
	if w == nil {
		return "no window"
	}
	if w.Root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *Node)
	walk = func(n *Node) {
		switch {
		case n == nil:
			b.WriteString("-")
		case n.Kind == Item:
			b.WriteString(n.ID)
		case n.Kind == Group:
			b.WriteString("[")
			for i, k := range n.Kids {
				if i > 0 {
					b.WriteString(" ")
				}
				if k.ID == n.Front {
					b.WriteString("*")
				}
				walk(k)
			}
			b.WriteString("]")
		default:
			b.WriteString("(")
			walk(n.Kids[0])
			if n.Kind == Row {
				b.WriteString(" | ")
			} else {
				b.WriteString(" / ")
			}
			walk(n.Kids[1])
			b.WriteString(")")
		}
	}
	walk(w.Root)
	return b.String()
}

func want(t *testing.T, l Layout, win int, w string) {
	t.Helper()
	if got := draw(l, win); got != w {
		t.Errorf("window %d is %s, want %s", win, got, w)
	}
}

// TestATabMovesAlongItsBar: the place counts the tabs with it, so a tab
// dropped just after itself stays put.
func TestATabMovesAlongItsBar(t *testing.T) {
	l := NewLayout(true, "a", "b", "c")
	g := l.Windows[0].Root.ID
	if !l.Move("a", Target{At: g, Index: 2}) {
		t.Fatal("the move failed")
	}
	want(t, l, 0, "[b *a c]")
	l.Move("a", Target{At: g, Index: -1})
	want(t, l, 0, "[b c *a]")
	l.Move("a", Target{At: g, Index: 0})
	want(t, l, 0, "[*a b c]")
	// Just after itself: where it is.
	l.Move("a", Target{At: g, Index: 1})
	want(t, l, 0, "[*a b c]")
}

// TestATabDroppedAtAnEdgeSplitsInAGroupOfItsOwn: grouped, a view put
// beside another gets a bar of its own, and beside a tab means beside
// its group.
func TestATabDroppedAtAnEdgeSplitsInAGroupOfItsOwn(t *testing.T) {
	l := NewLayout(true, "a", "b", "c")
	if !l.Move("b", Target{At: "a", Side: Right}) {
		t.Fatal("the move failed")
	}
	want(t, l, 0, "([*a c] | [*b])")
	l.Move("c", Target{At: "b", Side: Bottom})
	want(t, l, 0, "([*a] | ([*b] / [*c]))")
	// The last tab of a group leaving it: the group goes, and the split
	// gives way to its other side.
	l.Move("b", Target{At: "a", Index: -1})
	want(t, l, 0, "([a *b] | [*c])")
	split := l.Windows[0].Root
	if split.Kind != Row || split.Share != 0.5 {
		t.Errorf("the split is %v at %v", split.Kind, split.Share)
	}
}

// TestPanesSplitInsideATab: ungrouped, as a terminal's tabs, a tab
// dropped beside a pane joins it in a split inside the pane's tab, and
// one pane taken out of a split leaves its neighbour the whole tab.
func TestPanesSplitInsideATab(t *testing.T) {
	l := NewLayout(false, "p1", "p2", "p3")
	if !l.Move("p2", Target{At: "p1", Side: Left}) {
		t.Fatal("the move failed")
	}
	want(t, l, 0, "[*(p2 | p1) p3]")
	l.Move("p3", Target{At: "p1", Side: Bottom})
	want(t, l, 0, "[*(p2 | (p1 / p3))]")
	// A pane out of its split onto a tab of its own, last.
	root := l.Windows[0].Root.ID
	l.Move("p1", Target{At: root, Index: -1})
	want(t, l, 0, "[(p2 | p3) *p1]")
	// A whole tab, its split and all, beside a pane of another tab.
	split := l.Windows[0].Root.Kids[0].ID
	l.Move(split, Target{At: "p1", Side: Right})
	want(t, l, 0, "[*(p1 | (p2 | p3))]")
}

// TestATabMovesToAnotherWindowAndBack: out into a window of its own,
// over to the first window's bar, and the window it left closes.
func TestATabMovesToAnotherWindowAndBack(t *testing.T) {
	l := NewLayout(true, "a", "b")
	win, ok := l.ToWindow("b", nil)
	if !ok || win != 1 {
		t.Fatalf("window %d, %v", win, ok)
	}
	want(t, l, 0, "[*a]")
	want(t, l, 1, "[*b]")
	l.Move("a", Target{Win: 1, Index: -1})
	want(t, l, 0, "")
	want(t, l, 1, "[b *a]")
	if len(l.Windows) != 2 {
		t.Error("the kept window closed")
	}
	l.Move("a", Target{Win: 0})
	l.Move("b", Target{Win: 0, Index: 0})
	want(t, l, 0, "[*b a]")
	if l.Window(1) != nil {
		t.Error("the emptied window stayed")
	}
}

// TestATabCannotGoIntoItself: a split's tab dropped on one of its own
// panes, or a node beside itself, leaves the layout as it was.
func TestATabCannotGoIntoItself(t *testing.T) {
	l := NewLayout(false, "p1", "p2")
	l.Move("p2", Target{At: "p1", Side: Right})
	before := draw(l, 0)
	split := l.Windows[0].Root.Kids[0].ID
	if l.Move(split, Target{At: "p1", Side: Left}) || l.Move("p1", Target{At: "p1", Side: Left}) ||
		l.Move("p1", Target{At: "nothing"}) || l.Move("nothing", Target{}) {
		t.Error("a move that cannot be was made")
	}
	want(t, l, 0, before)
}

// TestClosingATabBringsItsNeighbour: the tab after it, or the one
// before where it was last; and Apply carries Close out.
func TestClosingATabBringsItsNeighbour(t *testing.T) {
	l := NewLayout(true, "a", "b", "c")
	l.Show("b")
	if !l.Apply(Close{ID: "b"}) {
		t.Fatal("the close failed")
	}
	want(t, l, 0, "[a *c]")
	l.Apply(Close{ID: "c"})
	want(t, l, 0, "[*a]")
	l.Apply(Close{ID: "a"})
	want(t, l, 0, "")
	if len(l.Windows) != 1 {
		t.Error("the kept window closed")
	}
	if !l.Add("d", Target{}) {
		t.Fatal("adding to the empty window failed")
	}
	want(t, l, 0, "[*d]")
}

// TestShowingANestedTabBringsEveryGroupRoundIt: a pane in a tab not in
// front shows by bringing that tab forward too.
func TestShowingANestedTabBringsEveryGroupRoundIt(t *testing.T) {
	l := NewLayout(false, "p1", "p2")
	l.Move("p2", Target{At: "p1", Side: Right})
	l.Add("p3", Target{Index: -1})
	want(t, l, 0, "[(p1 | p2) *p3]")
	if l.Shows("p1") {
		t.Error("p1 shows behind p3")
	}
	if !slices.Equal(l.Shown(0), []string{"p3"}) {
		t.Errorf("shown %v", l.Shown(0))
	}
	l.Apply(Show{ID: "p2"})
	want(t, l, 0, "[*(p1 | p2) p3]")
	if !slices.Equal(l.Shown(0), []string{"p1", "p2"}) {
		t.Errorf("shown %v", l.Shown(0))
	}
}

// TestAGroupDroppedIntoAGroupJoinsAsItsTabs: the groups do not nest,
// and the tab in front stays in front.
func TestAGroupDroppedIntoAGroupJoinsAsItsTabs(t *testing.T) {
	l := NewLayout(true, "a", "b", "c")
	l.Move("b", Target{At: "a", Side: Right})
	l.Move("c", Target{At: "b", Index: -1})
	l.Show("b")
	want(t, l, 0, "([*a] | [*b c])")
	g := l.GroupOf("b").ID
	l.Move(g, Target{At: l.GroupOf("a").ID, Index: -1})
	want(t, l, 0, "[a *b c]")
}

// TestASplitsShareStaysInsideTheRoom: and Apply reports no change for
// the share it has.
func TestASplitsShareStaysInsideTheRoom(t *testing.T) {
	l := NewLayout(true, "a", "b")
	l.Move("b", Target{At: "a", Side: Bottom})
	id := l.Windows[0].Root.ID
	if !l.Apply(Resize{ID: id, Share: 1.4}) {
		t.Fatal("the resize failed")
	}
	if s := l.Node(id).Share; s != 0.98 {
		t.Errorf("share %v, want 0.98", s)
	}
	if l.Apply(Resize{ID: id, Share: 0.98}) {
		t.Error("the same share again was a change")
	}
	if l.Apply(Resize{ID: "a", Share: 0.3}) {
		t.Error("an item took a share")
	}
}

// TestALayoutComesBackWhole: written out and read back, then mended
// from a file with a view twice, a view no longer known, a split of one
// node and a window twice.
func TestALayoutComesBackWhole(t *testing.T) {
	l := NewLayout(true, "a", "b", "c")
	l.Move("c", Target{At: "a", Side: Right})
	l.ToWindow("b", nil)
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var back Layout
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if draw(back, 0) != draw(l, 0) || draw(back, 1) != draw(l, 1) || back.Next != l.Next {
		t.Errorf("came back as %s and %s", draw(back, 0), draw(back, 1))
	}

	const broken = `{"grouped":true,"windows":[
		{"id":0,"keep":true,"root":{"id":"#9","kind":"row","kids":[
			{"kind":"group","front":"gone","kids":[{"id":"a","kind":"item"},{"id":"old","kind":"item"}]},
			{"id":"#9","kind":"row","kids":[{"id":"a","kind":"item"}]}]}},
		{"id":0,"root":{"id":"b","kind":"item"}},
		{"id":3,"root":{"id":"#2","kind":"column","share":7,"kids":[{"id":"b","kind":"item"},{"id":"c","kind":"item"}]}}]}`
	var m Layout
	if err := json.Unmarshal([]byte(broken), &m); err != nil {
		t.Fatal(err)
	}
	m.Mend(func(item string) bool { return item != "old" })
	want(t, m, 0, "[*a]")
	want(t, m, 3, "([*b] / [*c])")
	if len(m.Windows) != 2 {
		t.Errorf("%d windows, want 2", len(m.Windows))
	}
	ids := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if ids[n.ID] || n.ID == "" {
			t.Errorf("node %q named twice, or not at all", n.ID)
		}
		ids[n.ID] = true
		for _, k := range n.Kids {
			walk(k)
		}
	}
	for _, w := range m.Windows {
		walk(w.Root)
	}
	if s := m.Windows[1].Root.Share; s != 0.5 {
		t.Errorf("a share of 7 mended to %v", s)
	}
	// A new group is named past every name read.
	m.Move("c", Target{At: "a", Side: Left})
	if g := m.GroupOf("c").ID; ids[g] {
		t.Errorf("the new group took a name in use, %s", g)
	}
}

// TestCloseWindowGivesItsItems: for the application to put back or
// close; Apply leaves Detach and New alone.
func TestCloseWindowGivesItsItems(t *testing.T) {
	l := NewLayout(true, "a", "b", "c")
	win, _ := l.ToWindow("b", nil)
	l.Move("c", Target{At: "b", Side: Right})
	items := l.CloseWindow(win)
	if !slices.Equal(items, []string{"b", "c"}) {
		t.Errorf("items %v", items)
	}
	for _, it := range items {
		l.Add(it, Target{Index: -1})
	}
	want(t, l, 0, "[a b *c]")
	if l.Apply(Detach{ID: "a"}) || l.Apply(New{}) {
		t.Error("Apply carried out the application's change")
	}
}
