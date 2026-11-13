package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

type treeActivated struct{ Key Key }

// treeModel is a small hierarchy: folders a and b, a holding a/1 and a/2, and the file c.
type treeModel struct{ open map[Key]bool }

var treeKids = map[Key][]Key{"": {"a", "b", "c"}, "a": {"a/1", "a/2"}, "b": {}}

func (m treeModel) keys(dir Key, out []Key) []Key {
	for _, k := range treeKids[dir] {
		out = append(out, k)
		if m.open[k] {
			out = m.keys(k, out)
		}
	}
	return out
}

func (m treeModel) item(k Key) TreeItem {
	_, branch := treeKids[k]
	return TreeItem{Text: string(k), Depth: strings.Count(string(k), "/"), Branch: branch, Open: m.open[k]}
}

// newTreeStage mounts the model's tree in a 300 by 200 window, with the keyboard. apply feeds the intents the
// tree sent back to it, opening and shutting the branches activated.
func newTreeStage(t *testing.T) (w *gunim.Window, tr *Tree, run func(int), apply func() []Key) {
	t.Helper()
	m := treeModel{open: map[Key]bool{}}
	tr = NewTree()
	tr.Item = m.item
	tr.OnActivate = func(k Key, u *gunim.UI) gunim.Intent { return treeActivated{k} }
	w = gunimtest.New(t, geom.Sz(300, 200), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "t", func(struct{}) *Tree { return tr },
		func(tr *Tree, _ struct{}, u *gunim.UI) { tr.SetKeys(m.keys("", nil), u) })
	if err := w.Client().Mount(gunim.Root, "t", "t", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Update("t", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("t"); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(10)
	apply = func() []Key {
		var got []Key
		for _, in := range sent(w) {
			if a, ok := in.(treeActivated); ok {
				got = append(got, a.Key)
				if _, branch := treeKids[a.Key]; branch {
					m.open[a.Key] = !m.open[a.Key]
				}
			}
		}
		if err := w.Client().Update("t", struct{}{}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	return w, tr, run, apply
}

func TestATreeOpensABranchOnAClickAndItsRowsGrowIn(t *testing.T) {
	w, tr, run, apply := newTreeStage(t)
	h := TreeRowHeight.Default()
	click(w, 50, h/2)
	run(1)
	if got := apply(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("a click on a sent %v, want [a]", got)
	}
	run(3)
	r, ok := tr.list.live["a/1"]
	if !ok {
		t.Fatal("opening a built no row for a/1")
	}
	if v := r.height.Value(); v <= 0 || v >= h {
		t.Fatalf("three frames in, a/1 is %v tall, want it growing toward %v", v, h)
	}
	run(60)
	if v := r.height.Value(); v < h-0.5 {
		t.Fatalf("settled, a/1 is %v tall, want %v", v, h)
	}
	if k, _ := tr.Cursor(); k != "a" {
		t.Fatalf("the cursor is on %q after clicking a", k)
	}
}

func TestATreeChevronTurnsAsItsBranchOpens(t *testing.T) {
	w, tr, run, apply := newTreeStage(t)
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyRight})
	run(1)
	apply()
	run(3)
	r, ok := tr.list.live["a"].child.(*treeRow)
	if !ok {
		t.Fatal("a's row is not a tree row")
	}
	if v := r.turn.Value(); v <= 0 || v >= 1 {
		t.Fatalf("three frames in, the chevron has turned %v, want between 0 and 1", v)
	}
	run(60)
	if v := r.turn.Value(); v < 0.99 {
		t.Fatalf("settled, the chevron has turned %v, want 1", v)
	}
}

func TestTheArrowKeysWalkATree(t *testing.T) {
	w, tr, run, apply := newTreeStage(t)
	press := func(k input.Key) {
		w.Input(input.KeyPress{Key: k})
		run(1)
		apply()
		run(30)
	}
	press(input.KeyDown)
	if k, _ := tr.Cursor(); k != "a" {
		t.Fatalf("Down put the cursor on %q, want a", k)
	}
	press(input.KeyRight)
	if k, _ := tr.Cursor(); k != "a" || !tr.Item("a").Open {
		t.Fatalf("Right left the cursor on %q with a open %v, want on a and open", k, tr.Item("a").Open)
	}
	press(input.KeyRight)
	if k, _ := tr.Cursor(); k != "a/1" {
		t.Fatalf("Right on an open branch put the cursor on %q, want a/1", k)
	}
	press(input.KeyLeft)
	if k, _ := tr.Cursor(); k != "a" {
		t.Fatalf("Left on a/1 put the cursor on %q, want its branch a", k)
	}
	press(input.KeyLeft)
	if tr.Item("a").Open {
		t.Fatal("Left on an open branch left it open")
	}
	press(input.KeyEnd)
	press(input.KeyEnter)
	if k, _ := tr.Cursor(); k != "c" {
		t.Fatalf("End put the cursor on %q, want c", k)
	}
}

func TestATreePillSlidesToTheCursor(t *testing.T) {
	w, tr, run, _ := newTreeStage(t)
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(30)
	w.Input(input.KeyPress{Key: input.KeyDown})
	run(2)
	y, _ := tr.pillY()
	to, _ := tr.rowY("b")
	from, _ := tr.rowY("a")
	if y <= from || y >= to {
		t.Fatalf("two frames after Down the pill is at %v, want between a at %v and b at %v", y, from, to)
	}
	run(60)
	if y, _ := tr.pillY(); y != to {
		t.Fatalf("settled, the pill is at %v, want b's row at %v", y, to)
	}
}

func TestATreeTipsARowCutShort(t *testing.T) {
	tr := NewTree()
	long := strings.Repeat("a long name ", 20)
	tr.Item = func(Key) TreeItem { return TreeItem{Text: long} }
	w := gunimtest.New(t, geom.Sz(200, 100), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "t", func(struct{}) *Tree { return tr },
		func(tr *Tree, _ struct{}, u *gunim.UI) { tr.SetKeys([]Key{"x"}, u) })
	if err := w.Client().Mount(gunim.Root, "t", "t", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Update("t", struct{}{}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		w.Frame(time.Second / 60)
	}
	r, ok := tr.list.live["x"].child.(*treeRow)
	if !ok {
		t.Fatal("x's row is not a tree row")
	}
	if r.tipText() != long {
		t.Fatalf("a row cut short tips %q, want its whole text", r.tipText())
	}
}
