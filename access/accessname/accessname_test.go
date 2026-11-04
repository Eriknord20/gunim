package accessname

import (
	"slices"
	"testing"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
)

// A name finds what shows: whole names before parts of names, a path through the names above it, and a popup past
// the window's edge, but nothing laid out off the window.
func TestFindsWhatShowsByName(t *testing.T) {
	node := func(name string, r geom.Rect, kids ...*access.Node) *access.Node {
		return &access.Node{Info: access.Info{Name: name}, Bounds: r, Children: kids}
	}
	solo := node("Solo", geom.Rc(10, 10, 10, 10))
	other := node("Solo", geom.Rc(40, 10, 10, 10))
	hidden := node("Solo", geom.Rc(10, 900, 10, 10))
	menu := node("Standard kit", geom.Rc(10, 700, 50, 20))
	popup := node("", geom.Rc(0, 650, 100, 100), menu)
	popup.Role = access.RoleWindow
	root := node("Window", geom.Rc(0, 0, 100, 100),
		node("Snare top", geom.Rc(0, 0, 30, 30), solo),
		node("Kick", geom.Rc(30, 0, 30, 30), other, node("Solo stage", geom.Rc(40, 18, 10, 10))),
		node("Kit", geom.Rc(0, 800, 100, 200), hidden), popup)
	if got := Matches(root, []string{"solo"}, true); !slices.Equal(got, []*access.Node{solo, other}) {
		t.Fatalf("Solo finds %v", got)
	}
	if got := Matches(root, []string{"kick", "solo"}, true); !slices.Equal(got, []*access.Node{other}) {
		t.Fatalf("Kick > Solo finds %v", got)
	}
	if got := Matches(root, []string{"stage"}, false); len(got) != 1 {
		t.Fatalf("a part of a name finds %v", got)
	}
	if got := Matches(root, []string{"standard kit"}, true); !slices.Equal(got, []*access.Node{menu}) {
		t.Fatalf("an item of a popup past the window's edge finds %v", got)
	}
}

// A query names its nth match, whole names before names that hold it.
func TestAQueryFindsItsNthMatchWholeNamesFirst(t *testing.T) {
	node := func(name string, x float32) *access.Node {
		return &access.Node{Info: access.Info{Name: name}, Bounds: geom.Rc(x, 0, 10, 10)}
	}
	solo, soloed, solo2 := node("Solo", 0), node("Soloed", 20), node("Solo", 40)
	tree := access.NewTree(&access.Node{Bounds: geom.Rc(0, 0, 100, 100), Children: []*access.Node{soloed, solo, solo2}})
	if q := Parse("Track > Solo#2"); !slices.Equal(q.Path, []string{"track", "solo"}) || q.Nth != 2 {
		t.Fatalf("Track > Solo#2 parses as %+v", q)
	}
	if n, i := Find([]*access.Tree{tree}, Parse("solo#2")); n != solo2 || i != 0 {
		t.Fatalf("Solo#2 finds %v in tree %d", n, i)
	}
	if n, _ := Find([]*access.Tree{nil, tree}, Parse("soloe")); n != soloed {
		t.Fatalf("a part of a name finds %v", n)
	}
	if n, i := Find([]*access.Tree{tree}, Parse("Mute")); n != nil || i != -1 {
		t.Fatalf("a name nothing has finds %v in tree %d", n, i)
	}
}

// An exact query matches whole names, or names' starts by a "*", and a
// sharp in a name is a sharp, not a count.
func TestAnExactQueryMatchesWholeNamesOrStarts(t *testing.T) {
	node := func(name string, x float32) *access.Node {
		return &access.Node{Info: access.Info{Name: name}, Bounds: geom.Rc(x, 0, 10, 10)}
	}
	humanize, sharp := node("humanize 10%", 0), node("C#4", 20)
	tree := access.NewTree(&access.Node{Bounds: geom.Rc(0, 0, 100, 100), Children: []*access.Node{humanize, sharp}})
	if n, _ := Find([]*access.Tree{tree}, Exact("humanize")); n != nil {
		t.Fatalf("a part of a name finds %v", n.Name)
	}
	if n, _ := Find([]*access.Tree{tree}, Exact("Humanize*")); n != humanize {
		t.Fatalf("a name's start finds %v", n)
	}
	if n, _ := Find([]*access.Tree{tree}, Exact("C#4")); n != sharp {
		t.Fatalf("C#4 finds %v", n)
	}
}

// A node shows only where its middle lies inside every node round it: a
// row scrolled out of its list is hidden, though its window holds it.
func TestANodeOutsideTheNodesRoundItIsHidden(t *testing.T) {
	node := func(name string, r geom.Rect, kids ...*access.Node) *access.Node {
		return &access.Node{Info: access.Info{Name: name}, Bounds: r, Children: kids}
	}
	shown := node("Clip", geom.Rc(10, 20, 10, 10))
	scrolled := node("Clip", geom.Rc(10, 70, 10, 10))
	list := node("Lanes", geom.Rc(0, 0, 100, 50), node("Arp", geom.Rc(0, 10, 100, 30), shown), node("Bass", geom.Rc(0, 60, 100, 30), scrolled))
	root := node("Window", geom.Rc(0, 0, 100, 100), list)
	if got := Matches(root, []string{"clip"}, true); len(got) != 1 || got[0] != shown {
		t.Fatalf("Clip finds %v", got)
	}
}

// A list taller than its scroll shows where part of it does, though its middle is past the scroll's edge. Its rows,
// and a node to act on, keep the middle rule.
func TestAListTallerThanItsScrollShows(t *testing.T) {
	node := func(name string, r geom.Rect, kids ...*access.Node) *access.Node {
		return &access.Node{Info: access.Info{Name: name}, Bounds: r, Children: kids}
	}
	shown := node("Drums", geom.Rc(0, 10, 100, 20))
	cut := node("Bass", geom.Rc(0, 45, 100, 20))
	groups := node("Groups", geom.Rc(0, 0, 100, 200), shown, cut, node("Pads", geom.Rc(0, 150, 100, 20)))
	scroll := node("", geom.Rc(0, 0, 100, 50), groups)
	gone := node("Presets", geom.Rc(0, 60, 100, 100), node("Lead", geom.Rc(0, 60, 100, 20)))
	root := node("Window", geom.Rc(0, 0, 100, 100), scroll, node("", geom.Rc(0, 0, 100, 50), gone))
	if got := Matches(root, []string{"groups"}, true); !slices.Equal(got, []*access.Node{groups}) {
		t.Fatalf("Groups finds %v", got)
	}
	if got := Matches(root, []string{"groups", "*"}, true); !slices.Equal(got, []*access.Node{shown}) {
		t.Fatalf("the rows of Groups that show are %v", got)
	}
	if got := Matches(root, []string{"presets"}, true); len(got) != 0 {
		t.Fatalf("a list scrolled wholly out of sight finds %v", got)
	}
	// A row to act on, its middle past the scroll's edge, keeps the middle rule.
	lane := node("Lane", geom.Rc(0, 30, 100, 40), node("Mute", geom.Rc(0, 30, 10, 10)))
	lane.Actions = []string{access.ActionPress}
	root = node("Window", geom.Rc(0, 0, 100, 100), node("", geom.Rc(0, 0, 100, 40), lane))
	if got := Matches(root, []string{"lane"}, true); len(got) != 0 {
		t.Fatalf("a row to act on, its middle out of sight, finds %v", got)
	}
}
