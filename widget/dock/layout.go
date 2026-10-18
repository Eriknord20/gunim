// Package dock arranges an application's views in tabs and splits, in
// one window or many, and lets the user move them about. A tab dragged
// along its bar moves along it. Dropped on another bar it joins that
// group, and dropped at the edge of a pane it splits the pane. Dropped
// in another window it moves there, and let go outside every window it
// opens a window of its own.
//
// The package has two halves, as an application has. The [Layout] is
// plain data, which the application keeps, saves and changes. The
// widgets, a [Dock] and the [Bar]s it holds, show one window's part of
// the layout. They do not change it: they say what the user asks for as
// a [Change], and [Layout.Apply] carries it out.
//
// A layout is a tree in each window. Each part of it is a [Node]: an
// item, which is one of the application's views; a split of two nodes,
// side by side or one above the other; or a group of tabs, each tab a
// node, one of them in front. They nest freely. One application puts
// each pane's tabs over the pane, as an editor does; another puts one
// row of tabs in the title bar, each tab a set of panes, as a terminal
// does. Both are layouts.
package dock

import (
	"slices"
	"strconv"
	"strings"

	"github.com/marrasen/gunim/driver"
)

// A Kind is what a node is.
type Kind string

// The kinds of node.
const (
	// Item is one of the application's views.
	Item Kind = "item"
	// Row is a split of two nodes side by side.
	Row Kind = "row"
	// Column is a split of two nodes, one above the other.
	Column Kind = "column"
	// Group is a group of tabs.
	Group Kind = "group"
)

// A Node is one part of a window's layout.
type Node struct {
	// ID names the node. An item's ID is the application's name for the
	// view it shows, which must be unique and must not start with "#".
	// The layout names the splits and groups it makes "#1", "#2" and so
	// on.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Share is a split's first node's share of the room, more than 0 and
	// less than 1. 0 reads as a half.
	Share float32 `json:"share,omitempty"`
	// Front is the ID of the group's tab in front.
	Front string `json:"front,omitempty"`
	// Kids are a split's two nodes, or a group's tabs, in order.
	Kids []*Node `json:"kids,omitempty"`
}

// A Window is one window's part of a layout.
type Window struct {
	// ID numbers the window. The first window made is 0.
	ID int `json:"id"`
	// Root is the window's tree, nil while it holds nothing.
	Root *Node `json:"root,omitempty"`
	// Keep keeps the window open when its last node leaves it, as an
	// application's main window is.
	Keep bool `json:"keep,omitempty"`
	// Place is where the window stood on the screen, nil before it has
	// been shown.
	Place *driver.Placement `json:"place,omitempty"`
}

// A Layout is where an application's views stand, in all its windows.
type Layout struct {
	Windows []Window `json:"windows"`
	// Grouped puts every item in a group of tabs, so each pane has a bar
	// of its own: an item moved beside another node goes in a group of
	// its own, and a node moved beside an item in a group goes beside
	// the group. Without it an item may stand in a split as it is, and a
	// tab may hold several panes.
	Grouped bool `json:"grouped,omitempty"`
	// Next counts the splits and groups made, to name the next one.
	Next int `json:"next,omitempty"`
}

// A Side is where a node moved to a target goes.
type Side uint8

// The sides.
const (
	// Into makes the node a tab of the target group, at the target's
	// Index. Into a tab is into its group. A target that is neither
	// becomes a group of two tabs: itself and the node.
	Into Side = iota
	// Left, Right, Top and Bottom split the target, the node on that
	// side of it.
	Left
	Right
	Top
	Bottom
)

// A Target is where a node goes.
type Target struct {
	// At is the ID of the node it goes to. "" is the window Win: Into
	// its first group, or beside all it holds; in an empty window, the
	// node is all the window holds.
	At  string
	Win int
	// Side is where the node goes, and Index, for Into, is its place
	// among the group's tabs: 0 first, and a negative Index last.
	Side  Side
	Index int
}

// NewLayout makes a layout of one window, kept open, holding a group of
// the items, the first in front. With no items the window is empty.
func NewLayout(grouped bool, items ...string) Layout {
	l := Layout{Grouped: grouped}
	w := Window{Keep: true}
	if len(items) > 0 {
		g := l.made(Group)
		for _, it := range items {
			g.Kids = append(g.Kids, &Node{ID: it, Kind: Item})
		}
		g.Front = items[0]
		w.Root = g
	}
	l.Windows = append(l.Windows, w)
	return l
}

// AddWindow adds an empty window and returns its ID: one more than any
// window's, or 0 for the first. keep keeps it open when it empties.
func (l *Layout) AddWindow(keep bool) int {
	id := l.nextWindow()
	l.Windows = append(l.Windows, Window{ID: id, Keep: keep})
	return id
}

func (l *Layout) nextWindow() int {
	id := 0
	for _, w := range l.Windows {
		id = max(id, w.ID+1)
	}
	return id
}

// Window returns window win, or nil when there is none.
func (l *Layout) Window(win int) *Window {
	for i := range l.Windows {
		if l.Windows[i].ID == win {
			return &l.Windows[i]
		}
	}
	return nil
}

// Node returns the node id, or nil when there is none.
func (l *Layout) Node(id string) *Node {
	if s, ok := l.find(id); ok {
		return s.node
	}
	return nil
}

// Where returns the window node id stands in, and false where it stands
// nowhere.
func (l *Layout) Where(id string) (win int, ok bool) {
	s, ok := l.find(id)
	return s.win, ok
}

// Parent returns the node holding node id, nil for a window's root or a
// node there is none of.
func (l *Layout) Parent(id string) *Node {
	if s, ok := l.find(id); ok {
		return s.parent
	}
	return nil
}

// GroupOf returns the group node id is a tab of, or nil.
func (l *Layout) GroupOf(id string) *Node {
	if p := l.Parent(id); p != nil && p.Kind == Group {
		return p
	}
	return nil
}

// Items returns every item, window by window, in order.
func (l *Layout) Items() []string {
	var out []string
	for _, w := range l.Windows {
		out = w.Root.appendItems(out)
	}
	return out
}

// Items returns the items below n, n itself for an item, in order.
func (n *Node) Items() []string { return n.appendItems(nil) }

func (n *Node) appendItems(out []string) []string {
	if n == nil {
		return out
	}
	if n.Kind == Item {
		return append(out, n.ID)
	}
	for _, k := range n.Kids {
		out = k.appendItems(out)
	}
	return out
}

// Shown returns the items window win shows: those in front of their
// groups, and in front of every group round them.
func (l *Layout) Shown(win int) []string {
	w := l.Window(win)
	if w == nil {
		return nil
	}
	return w.Root.shown(nil)
}

func (n *Node) shown(out []string) []string {
	switch {
	case n == nil:
		return out
	case n.Kind == Item:
		return append(out, n.ID)
	case n.Kind == Group:
		if f := n.front(); f != nil {
			return f.shown(out)
		}
		return out
	}
	for _, k := range n.Kids {
		out = k.shown(out)
	}
	return out
}

// front returns the group's tab in front, nil for none.
func (n *Node) front() *Node {
	for _, k := range n.Kids {
		if k.ID == n.Front {
			return k
		}
	}
	return nil
}

// Shows reports whether node id shows: it stands in front of every
// group round it.
func (l *Layout) Shows(id string) bool {
	for _, w := range l.Windows {
		if path := w.Root.path(id, nil); path != nil {
			for i := 0; i+1 < len(path); i++ {
				if path[i].Kind == Group && path[i].Front != path[i+1].ID {
					return false
				}
			}
			return true
		}
	}
	return false
}

// path returns the nodes from n down to node id, or nil.
func (n *Node) path(id string, out []*Node) []*Node {
	if n == nil {
		return nil
	}
	out = append(out, n)
	if n.ID == id {
		return out
	}
	for _, k := range n.Kids {
		if p := k.path(id, out); p != nil {
			return p
		}
	}
	return nil
}

// Show brings node id to the front of every group round it, and
// returns the window it stands in; false where it stands nowhere.
func (l *Layout) Show(id string) (win int, ok bool) {
	for _, w := range l.Windows {
		if path := w.Root.path(id, nil); path != nil {
			for i := 0; i+1 < len(path); i++ {
				if path[i].Kind == Group {
					path[i].Front = path[i+1].ID
				}
			}
			return w.ID, true
		}
	}
	return 0, false
}

// SetShare sets split id's share of the room, kept between 0.02 and
// 0.98, and reports whether there is such a split.
func (l *Layout) SetShare(id string, share float32) bool {
	n := l.Node(id)
	if n == nil || n.Kind != Row && n.Kind != Column {
		return false
	}
	n.Share = min(max(share, 0.02), 0.98)
	return true
}

// Add puts a new item where to says, in front of its group, and reports
// whether it could. An item of that name already in the layout moves
// there.
func (l *Layout) Add(item string, to Target) bool {
	if item == "" || strings.HasPrefix(item, "#") {
		return false
	}
	if l.Node(item) != nil {
		return l.Move(item, to)
	}
	c := l.Clone()
	if !c.put(&Node{ID: item, Kind: Item}, to) {
		return false
	}
	c.tidy()
	c.Show(item)
	*l = c
	return true
}

// Move moves node id, with all it holds, where to says, and brings it
// to the front there. It reports whether it could, and leaves l as it
// was where it could not: where there is no such node or target, or the
// target is in the node itself. A window the node leaves empty closes,
// unless it is kept.
func (l *Layout) Move(id string, to Target) bool {
	s, ok := l.find(id)
	if !ok {
		return false
	}
	if to.At != "" {
		if to.At == id || s.node.path(to.At, nil) != nil {
			return false
		}
		if l.Node(to.At) == nil {
			return false
		}
	} else if l.Window(to.Win) == nil {
		return false
	}
	c := l.Clone()
	cs, _ := c.find(id)
	if to.Side == Into && cs.parent != nil && c.intoGroup(to) == cs.parent.ID && to.Index > cs.index {
		// Along its own bar: the place counts the tabs with it.
		to.Index--
	}
	cs.cut()
	if !c.put(cs.node, to) {
		return false
	}
	c.tidy()
	c.Show(id)
	*l = c
	return true
}

// Remove takes node id, with all it holds, out of the layout, and
// reports whether it was there. A group's tab beside it comes to the
// front in its place: the one after it, or the one before where it was
// last. A window it leaves empty closes, unless it is kept.
func (l *Layout) Remove(id string) bool {
	s, ok := l.find(id)
	if !ok {
		return false
	}
	s.cut()
	l.tidy()
	return true
}

// ToWindow moves node id into a new window, at place, as a group's only
// tab, and returns the window's ID; false where there is no such node.
func (l *Layout) ToWindow(id string, place *driver.Placement) (win int, ok bool) {
	s, ok := l.find(id)
	if !ok {
		return 0, false
	}
	win = l.nextWindow()
	s.cut()
	root := s.node
	if root.Kind != Group {
		root = l.made(Group)
		root.Kids = []*Node{s.node}
		root.Front = s.node.ID
	}
	l.Windows = append(l.Windows, Window{ID: win, Root: root, Place: place})
	l.tidy()
	return win, true
}

// CloseWindow takes window win out of the layout, and returns the items
// it held, for the application to close or to put elsewhere.
func (l *Layout) CloseWindow(win int) []string {
	i := slices.IndexFunc(l.Windows, func(w Window) bool { return w.ID == win })
	if i < 0 {
		return nil
	}
	items := l.Windows[i].Root.Items()
	l.Windows = slices.Delete(l.Windows, i, i+1)
	return items
}

// Clone returns a deep copy of l, to change while l stays as it is.
func (l *Layout) Clone() Layout {
	c := *l
	c.Windows = slices.Clone(l.Windows)
	for i := range c.Windows {
		c.Windows[i].Root = c.Windows[i].Root.Clone()
		if p := c.Windows[i].Place; p != nil {
			q := *p
			c.Windows[i].Place = &q
		}
	}
	return c
}

// Clone returns a deep copy of n.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Kids = make([]*Node, len(n.Kids))
	for i, k := range n.Kids {
		c.Kids[i] = k.Clone()
	}
	if len(c.Kids) == 0 {
		c.Kids = nil
	}
	return &c
}

// Mend makes l whole, as after reading it from a file another build
// wrote. Each item known says to keep, nil for all, stands once; a
// split holds two nodes, a group at least one, and every group has a
// tab in front; splits and groups have IDs of their own; window IDs
// are unique; and a window left empty goes, unless it is kept. Items
// known to the application but missing from the layout are the
// application's to Add.
func (l *Layout) Mend(known func(item string) bool) {
	seen := map[string]bool{}
	var walk func(n *Node) *Node
	walk = func(n *Node) *Node {
		if n == nil {
			return nil
		}
		switch n.Kind {
		case Item:
			if n.ID == "" || strings.HasPrefix(n.ID, "#") || seen[n.ID] || known != nil && !known(n.ID) {
				return nil
			}
			seen[n.ID] = true
			n.Kids, n.Front, n.Share = nil, "", 0
			return n
		case Row, Column, Group:
		default:
			return nil
		}
		if n.ID == "" || !strings.HasPrefix(n.ID, "#") || seen[n.ID] {
			n.ID = ""
		} else {
			seen[n.ID] = true
		}
		kids := n.Kids[:0]
		for _, k := range n.Kids {
			if k = walk(k); k != nil {
				kids = append(kids, k)
			}
		}
		clear(n.Kids[len(kids):])
		n.Kids = kids
		if n.Kind != Group && len(n.Kids) > 2 {
			// A split of more: the rest split the same way, in the
			// second place.
			rest := &Node{Kind: n.Kind, Kids: slices.Clone(n.Kids[1:])}
			n.Kids = []*Node{n.Kids[0], rest}
		}
		return n
	}
	ids := map[int]bool{}
	l.Windows = slices.DeleteFunc(l.Windows, func(w Window) bool {
		drop := ids[w.ID]
		ids[w.ID] = true
		return drop
	})
	for i := range l.Windows {
		l.Windows[i].Root = walk(l.Windows[i].Root)
	}
	// Names for the splits and groups that lack one, past every name
	// in use.
	for id := range seen {
		if n, err := strconv.Atoi(strings.TrimPrefix(id, "#")); err == nil && strings.HasPrefix(id, "#") {
			l.Next = max(l.Next, n)
		}
	}
	var name func(n *Node)
	name = func(n *Node) {
		if n == nil {
			return
		}
		if n.ID == "" {
			l.Next++
			n.ID = "#" + strconv.Itoa(l.Next)
		}
		for _, k := range n.Kids {
			name(k)
		}
	}
	for _, w := range l.Windows {
		name(w.Root)
	}
	l.tidy()
}

// made returns a new node of kind k, named.
func (l *Layout) made(k Kind) *Node {
	l.Next++
	return &Node{ID: "#" + strconv.Itoa(l.Next), Kind: k}
}

// spot is where a node stands: its window, the node holding it, nil at
// the root, and its place there.
type spot struct {
	w      *Window
	win    int
	node   *Node
	parent *Node
	index  int
}

// find returns where node id stands.
func (l *Layout) find(id string) (spot, bool) {
	if id == "" {
		return spot{}, false
	}
	for i := range l.Windows {
		w := &l.Windows[i]
		if w.Root == nil {
			continue
		}
		if w.Root.ID == id {
			return spot{w: w, win: w.ID, node: w.Root, index: -1}, true
		}
		if s, ok := w.Root.findIn(id); ok {
			s.w, s.win = w, w.ID
			return s, true
		}
	}
	return spot{}, false
}

func (n *Node) findIn(id string) (spot, bool) {
	for i, k := range n.Kids {
		if k == nil {
			// The hole a cut leaves in a split, until the tidy.
			continue
		}
		if k.ID == id {
			return spot{node: k, parent: n, index: i}, true
		}
		if s, ok := k.findIn(id); ok {
			return s, true
		}
	}
	return spot{}, false
}

// cut takes the node out of where it stands. A group's tab beside it
// comes to the front in its place. What it leaves empty or half is
// tidied by tidy.
func (s spot) cut() {
	if s.parent == nil {
		s.w.Root = nil
		return
	}
	p := s.parent
	p.Kids = slices.Delete(p.Kids, s.index, s.index+1)
	if p.Kind == Group && p.Front == s.node.ID {
		p.Front = ""
		if len(p.Kids) > 0 {
			p.Front = p.Kids[min(s.index, len(p.Kids)-1)].ID
		}
	}
	if p.Kind != Group {
		// A split with one node left: a hole the tidy fills.
		p.Kids = slices.Insert(p.Kids, s.index, (*Node)(nil))
	}
}

// put puts node n where to says, and reports whether there is such a
// place.
func (l *Layout) put(n *Node, to Target) bool {
	if to.At == "" {
		w := l.Window(to.Win)
		if w == nil {
			return false
		}
		if w.Root == nil {
			w.Root = n
			if l.Grouped && n.Kind == Item || to.Side == Into && n.Kind != Group {
				g := l.made(Group)
				g.Kids, g.Front = []*Node{n}, n.ID
				w.Root = g
			}
			return true
		}
		to.At = w.Root.ID
		if g := w.Root.firstGroup(); g != nil && to.Side == Into {
			to.At = g.ID
		}
	}
	s, ok := l.find(to.At)
	if !ok {
		return false
	}
	t := s.node
	if s.parent != nil && s.parent.Kind == Group && (to.Side == Into || l.Grouped) {
		// Into a tab is into its group; grouped, beside a tab is beside
		// its group.
		gs, _ := l.find(s.parent.ID)
		s, t = gs, gs.node
	}
	if to.Side == Into {
		if t.Kind != Group {
			g := l.made(Group)
			g.Kids, g.Front = []*Node{t}, t.ID
			s.replace(g)
			t = g
		}
		kids := []*Node{n}
		if n.Kind == Group {
			// A group joins as its tabs.
			kids = n.Kids
		}
		i := to.Index
		if i < 0 || i > len(t.Kids) {
			i = len(t.Kids)
		}
		t.Kids = slices.Insert(t.Kids, i, kids...)
		t.Front = n.ID
		if n.Kind == Group {
			t.Front = n.Front
		}
		return true
	}
	if l.Grouped && n.Kind == Item {
		g := l.made(Group)
		g.Kids, g.Front = []*Node{n}, n.ID
		n = g
	}
	split := l.made(Row)
	if to.Side == Top || to.Side == Bottom {
		split.Kind = Column
	}
	split.Share = 0.5
	if to.Side == Left || to.Side == Top {
		split.Kids = []*Node{n, t}
	} else {
		split.Kids = []*Node{t, n}
	}
	s.replace(split)
	return true
}

// intoGroup returns the ID of the group a node moved Into to joins,
// "" for none yet.
func (l *Layout) intoGroup(to Target) string {
	at := to.At
	if at == "" {
		w := l.Window(to.Win)
		if w == nil || w.Root == nil {
			return ""
		}
		at = w.Root.ID
		if g := w.Root.firstGroup(); g != nil {
			at = g.ID
		}
	}
	s, ok := l.find(at)
	switch {
	case !ok:
		return ""
	case s.node.Kind == Group:
		return s.node.ID
	case s.parent != nil && s.parent.Kind == Group:
		return s.parent.ID
	}
	return ""
}

// replace puts n where the spot's node stands.
func (s spot) replace(n *Node) {
	if s.parent == nil {
		s.w.Root = n
		return
	}
	s.parent.Kids[s.index] = n
}

// firstGroup returns the first group at or below n, or nil.
func (n *Node) firstGroup() *Node {
	if n == nil || n.Kind == Item {
		return nil
	}
	if n.Kind == Group {
		return n
	}
	for _, k := range n.Kids {
		if g := k.firstGroup(); g != nil {
			return g
		}
	}
	return nil
}

// tidy fills what cuts left: a group of no tabs goes, a split of one
// node gives way to it, a group in a group joins it as its tabs, every
// group has a tab in front, and with Grouped every item is a tab. A
// window left empty goes, unless it is kept.
func (l *Layout) tidy() {
	var walk func(n, parent *Node) *Node
	walk = func(n, parent *Node) *Node {
		if n == nil {
			return nil
		}
		if n.Kind == Item {
			if l.Grouped && (parent == nil || parent.Kind != Group) {
				g := l.made(Group)
				g.Kids, g.Front = []*Node{n}, n.ID
				return g
			}
			return n
		}
		kids := make([]*Node, 0, len(n.Kids))
		for _, k := range n.Kids {
			if k = walk(k, n); k == nil {
				continue
			}
			if n.Kind == Group && k.Kind == Group {
				kids = append(kids, k.Kids...)
				if n.Front == k.ID {
					n.Front = k.Front
				}
				continue
			}
			kids = append(kids, k)
		}
		n.Kids = kids
		switch {
		case len(kids) == 0:
			return nil
		case n.Kind == Group:
			if n.front() == nil {
				n.Front = kids[0].ID
			}
			return n
		case len(kids) == 1:
			// The split gives way to its one node, which in a group
			// must be no group itself.
			return walk(kids[0], parent)
		}
		if n.Share <= 0 || n.Share >= 1 {
			n.Share = 0.5
		}
		return n
	}
	for i := range l.Windows {
		l.Windows[i].Root = walk(l.Windows[i].Root, nil)
	}
	l.Windows = slices.DeleteFunc(l.Windows, func(w Window) bool { return w.Root == nil && !w.Keep })
}
