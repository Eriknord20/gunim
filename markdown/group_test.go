package markdown

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// grouped shows views of texts in a column, in one group keyed a, b, c and so on. keys lists more keys than there
// are views, standing for views that are not built, whose text is "key's text".
func grouped(t *testing.T, keys []string, texts ...string) (*gunim.Window, []*View, func(int)) {
	t.Helper()
	g := NewGroup(func(from, to string) []string {
		i, j := slices.Index(keys, from), slices.Index(keys, to)
		if i < 0 || j < 0 {
			return nil
		}
		if i > j {
			return slices.Clone(keys[j : i+1])
		}
		return slices.Clone(keys[i : j+1])
	}, func(key string) string { return key + "'s text" })
	views := make([]*View, len(texts))
	nodes := make([]gunim.Node, len(texts))
	for i, s := range texts {
		views[i] = New(s)
		views[i].Group, views[i].Key = g, keys[i]
		nodes[i] = views[i]
	}
	col := widget.Column(nodes...)
	col.Cross = widget.CrossStretch
	w := gunimtest.New(t, geom.Sz(400, 600), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "g", func(struct{}) gunim.Node { return col }, nil)
	if err := w.Client().Mount(gunim.Root, "g", "g", nil); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(3)
	return w, views, run
}

func TestADragSelectsAcrossTheViewsOfAGroup(t *testing.T) {
	w, views, run := grouped(t, []string{"a", "b", "c"}, "first message", "second one", "third")
	from := views[0].origin.Add(geom.Pt(0.5, 4))
	to := views[2].origin.Add(geom.Pt(views[2].size.W-2, 4))
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: to})
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(1)
	if s, e := views[1].Selection(); s != 0 || e != len([]rune("second one")) {
		t.Fatalf("the middle view selects %d..%d, want all of it", s, e)
	}
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	want := "first message\nsecond one\nthird"
	if c, _ := w.Offscreen().Clipboard(); c != want {
		t.Fatalf("clipboard %q, want %q", c, want)
	}
	// A new press starts over.
	w.Input(input.PointerDown{Pos: to, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(1)
	if s, e := views[0].Selection(); s != e {
		t.Fatalf("the first view still selects %d..%d after a press elsewhere", s, e)
	}
}

func TestADragUpwardsSelectsTheSameWay(t *testing.T) {
	w, views, run := grouped(t, []string{"a", "b"}, "top", "bottom")
	from := views[1].origin.Add(geom.Pt(views[1].size.W-2, 4))
	to := views[0].origin.Add(geom.Pt(0.5, 4))
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: to})
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(1)
	if got := views[1].SelectedText(); got != "top\nbottom" {
		t.Fatalf("selected %q, want both", got)
	}
}

func TestACopyTakesTheViewsBetweenThatAreNotBuilt(t *testing.T) {
	g := NewGroup(func(from, to string) []string { return []string{"a", "gone", "b"} },
		func(key string) string { return key + "'s text" })
	a, b := New("one"), New("two")
	a.Key, b.Key = "a", "b"
	a.plain, b.plain = []rune("one"), []rune("two")
	g.views["a"], g.views["b"] = a, b
	g.anchor, g.caret, g.on = point{"a", 0}, point{"b", 3}, true
	if got, want := g.text(), "one\ngone's text\ntwo"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
}
