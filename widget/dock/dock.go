package dock

import (
	"slices"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Drag is what a tab dragged off a bar carries.
type Drag struct {
	// ID is the node the tab shows, and Win the window it left.
	ID  string
	Win int
	// Grab is where the tab is held, from its top left corner, and Size
	// how large a window of its own would open.
	Grab geom.Point
	Size geom.Size
}

// Dock shows one window's part of a [Layout]: a [Bar] over each group,
// what each group's tab in front shows under it, and a divider between
// the two sides of each split, which the pointer drags.
//
// With Content set the dock holds the application's views itself, each
// in its place. Without it the dock draws the bars and the dividers
// alone, and the application draws each view where Rect says, and
// passes the drags over its views to DragEvent.
//
// A tab picked comes to the front at once, its view fading in from the
// side the tab stands on, and a new split slides open. A tab dragged
// over a pane lights where it would land: the pane's half nearest an
// edge splits it, and the middle joins its group. A double click on a
// divider evens the split out.
//
// With Keys, Ctrl+Page Down and Ctrl+Page Up step through the tabs of
// the pane the keys work in, and Ctrl+Tab and Ctrl+Shift+Tab through
// the window's panes, where nothing focused takes those keys.
type Dock struct {
	anim.Group
	// Content gives the node that shows an item, the same node each
	// time. Nil leaves the views to the application.
	Content func(item string) gunim.Node
	// Face says how a node's tab looks. Nil gives an item's tab its ID,
	// and a tab of several panes the first one's, with "+1" for each
	// other.
	Face func(n *Node) Face
	// TitleBar gives the bar of the group at the window's root to the
	// application, to put in the title bar: TopBar returns it.
	TitleBar bool
	// Plus puts a + after each bar's tabs, which asks for a New tab.
	Plus bool
	// Caption lets the room after the tabs of a bar along the dock's top
	// move the window, as a title bar does; Corner is the room a bar in
	// the top right corner leaves free for the window's buttons.
	Caption bool
	Corner  float32
	// Keys steps through the tabs and the panes from the keyboard.
	Keys bool
	// MarkAlone marks the tab in front of the pane the keys work in
	// where the window shows that pane alone too, not only among
	// several. Changed once the dock shows, it shows from the next
	// SetLayout or Refresh.
	MarkAlone bool
	// OnChange, when set, runs on the UI goroutine with what the user
	// asks for; a non-nil result is sent to the application as the
	// dock's intent. Without it the Change is sent itself.
	OnChange func(c Change, u *gunim.UI) gunim.Intent

	win  int
	l    Layout
	laid bool
	size geom.Size
	// groups, items and dividers are where the last layout put each
	// group, each item shown, and each divider.
	groups   []placedGroup
	items    map[string]geom.Rect
	dividers []placedDivider
	// bars are the groups' bars, by group, and top the root group's
	// with TitleBar.
	bars map[string]*Bar
	top  *Bar
	// nodes are the views, by item, as Content gave them; itemOf names
	// each one's item, and mounted says which are in the tree.
	nodes   map[string]gunim.Node
	itemOf  map[gunim.Node]string
	mounted map[gunim.Node]bool
	// showing are the items shown, each with how far it has faded in and
	// the side it comes from, -1 the left, 1 the right; fronts are the
	// groups' tabs in front, to know which side a new one comes from.
	showing map[string]*fadeIn
	fronts  map[string]string
	// shares are the splits' shares as they glide.
	shares map[string]*anim.Float
	// splits are the splits as the last sync saw them, and closing the
	// sides left where a split went with the other side, by the kept
	// side's node: each grows over the room the side gone had.
	splits  map[string]splitWas
	closing map[string]*closingSide
	// active is the item the keys work in.
	active string
	// held is the divider dragged, "" for none, at heldShare; hot the
	// one under the pointer.
	held      string
	heldShare float32
	hot       string
	// drop is where a tab dragged over would land, lit by dropLit.
	drop    *anim.Rect
	dropLit *anim.Float
}

// fadeIn is how far an item's view has come in, and from which side.
type fadeIn struct {
	on   *anim.Float
	from float32
}

// splitWas is a split as the dock last saw it: which way it splits,
// its sides' nodes, and the items each side held.
type splitWas struct {
	kind  Kind
	kids  [2]string
	items [2][]string
}

// closingSide is the side kept, kept 0 the first, of a split that went
// with its other side: its room grows from its share of the split as
// gone, the room the side gone had, shrinks to nothing.
type closingSide struct {
	kind  Kind
	kept  int
	share float32
	gone  *anim.Float
	// parent is the node that holds the kept side as it grows, "" for
	// the window's root.
	parent string
}

// room is where the kept side stands in r, the room it takes once the
// side gone has shrunk away.
func (c *closingSide) room(r geom.Rect, gap float32) geom.Rect {
	along := r.Size().W
	if c.kind == Column {
		along = r.Size().H
	}
	goneShare := 1 - c.share
	if c.kept == 1 {
		goneShare = c.share
	}
	cut := (max(along-gap, 0)*goneShare + gap) * min(max(c.gone.Value(), 0), 1)
	switch {
	case c.kind == Row && c.kept == 0:
		r.Max.X -= cut
	case c.kind == Row:
		r.Min.X += cut
	case c.kept == 0:
		r.Max.Y -= cut
	default:
		r.Min.Y += cut
	}
	return r
}

// placedGroup is a group as the dock laid it out: the room it takes,
// its bar's and what its tab in front shows.
type placedGroup struct {
	id               string
	bar              *Bar
	whole, top, area geom.Rect
}

// placedDivider is a divider: its split, which way it splits, where it
// stands, and the room the split shares.
type placedDivider struct {
	id          string
	kind        Kind
	rect, whole geom.Rect
}

// minPane is the least room a pane keeps while a divider is dragged.
const minPane = 48

// NewDock returns a dock showing window win of the layouts it is given.
func NewDock(win int) *Dock {
	d := &Dock{win: win, items: map[string]geom.Rect{}, bars: map[string]*Bar{}, nodes: map[string]gunim.Node{},
		itemOf: map[gunim.Node]string{}, mounted: map[gunim.Node]bool{}, showing: map[string]*fadeIn{}, fronts: map[string]string{},
		shares: map[string]*anim.Float{}, splits: map[string]splitWas{}, closing: map[string]*closingSide{}, drop: anim.NewRect(geom.Rect{}), dropLit: anim.NewFloat(0)}
	d.Add(d.drop, d.dropLit)
	return d
}

// Win returns the window the dock shows.
func (d *Dock) Win() int { return d.win }

// Arrangement returns the layout as the dock shows it, with what the
// user changed here and the application has not sent back yet.
func (d *Dock) Arrangement() Layout { return d.l.Clone() }

// SetLayout shows l, which the dock keeps a copy of. Once laid out, the
// views and the dividers glide to their new places; before, or with a
// nil u, they jump.
func (d *Dock) SetLayout(l Layout, u *gunim.UI) {
	d.l = l.Clone()
	// Whole, as one read from a file may not be: a split of two nodes,
	// a group with a tab in front.
	d.l.Mend(nil)
	d.sync(u)
}

// Refresh reads the tabs' faces again, as when a title changed.
func (d *Dock) Refresh(u *gunim.UI) { d.sync(u) }

// TopBar returns the bar of the group at the window's root, for the
// application to put in its title bar, with TitleBar set.
func (d *Dock) TopBar() *Bar {
	if d.top == nil {
		d.top = newBar(d, "", d.win)
		d.sync(nil)
	}
	return d.top
}

// Rect returns where item shows, in the dock's space, as the last
// layout put it; false where it does not show here.
func (d *Dock) Rect(item string) (geom.Rect, bool) {
	r, ok := d.items[item]
	return r, ok
}

// TabRect returns where tab id stands on a bar the dock lays out, in
// the dock's space, as the last layout put it and a click finds it: to
// anchor something to it, or for a test to press it. A tab sliding
// there is drawn on its way. False where no bar of the dock's shows
// it; TopBar's tabs are where the application put that bar.
func (d *Dock) TabRect(id string) (geom.Rect, bool) {
	for _, g := range d.groups {
		if g.bar == nil {
			continue
		}
		if i := g.bar.boxOf(id); i >= 0 {
			return g.bar.boxes[i].Add(g.top.Min), true
		}
	}
	return geom.Rect{}, false
}

// DividerRect returns where the divider of split id stands, in the
// dock's space, as the last layout put it; false where it shows none.
func (d *Dock) DividerRect(id string) (geom.Rect, bool) {
	for _, dv := range d.dividers {
		if dv.id == id {
			return dv.rect, true
		}
	}
	return geom.Rect{}, false
}

// Shown returns the items the dock shows, in order.
func (d *Dock) Shown() []string { return d.l.Shown(d.win) }

// Active returns the item the keys work in: the one pressed or picked
// last, or else the first shown.
func (d *Dock) Active() string {
	shown := d.Shown()
	if slices.Contains(shown, d.active) {
		return d.active
	}
	if len(shown) > 0 {
		return shown[0]
	}
	return ""
}

// SetActive has the keys work in item, and marks its tab.
func (d *Dock) SetActive(item string, u *gunim.UI) {
	if d.active == item {
		return
	}
	d.active = item
	d.marks(u)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (d *Dock) Children() []gunim.Node {
	var out []gunim.Node
	for _, it := range d.Shown() {
		if n := d.node(it); n != nil {
			out = append(out, n)
		}
	}
	return append(out, d.wantBars()...)
}

// node returns item's view, from Content, or nil.
func (d *Dock) node(item string) gunim.Node {
	if d.Content == nil {
		return nil
	}
	if n, ok := d.nodes[item]; ok {
		return n
	}
	n := d.Content(item)
	if n != nil {
		d.nodes[item] = n
		d.itemOf[n] = item
	}
	return n
}

// wantBars returns the bars the dock holds: one for each group that
// shows, but the root's with TitleBar.
func (d *Dock) wantBars() []gunim.Node {
	var out []gunim.Node
	w := d.l.Window(d.win)
	if w == nil {
		return nil
	}
	var walk func(n *Node, root bool)
	walk = func(n *Node, root bool) {
		switch n.Kind {
		case Item:
		case Group:
			if !root || !d.TitleBar {
				if b := d.bars[n.ID]; b != nil {
					out = append(out, b)
				}
			}
			if f := n.front(); f != nil {
				walk(f, false)
			}
		case Row, Column:
			for _, k := range n.Kids {
				walk(k, false)
			}
		}
	}
	if w.Root != nil {
		walk(w.Root, true)
	}
	return out
}

// sync brings the bars, the fades and the splits' glides up to date
// with the layout.
func (d *Dock) sync(u *gunim.UI) {
	moving := d.laid && u != nil
	var th *theme.Live
	if u != nil {
		th = u.Theme()
	}
	slide := Slide.Default()
	if th != nil {
		slide = Slide.Get(th)
	}
	w := d.l.Window(d.win)
	var root *Node
	if w != nil {
		root = w.Root
	}
	was := d.showing
	d.showing = map[string]*fadeIn{}
	shown := root.shown(nil)
	keep := map[string]bool{}
	splits := map[string]splitWas{}
	from := map[string]float32{}
	var walk func(n *Node, top bool)
	walk = func(n *Node, top bool) {
		switch n.Kind {
		case Item:
		case Group:
			keep[n.ID] = true
			b := d.bars[n.ID]
			if top && d.TitleBar {
				if d.top == nil {
					d.top = newBar(d, "", d.win)
				}
				b = d.top
				b.group = n.ID
			} else if b == nil {
				b = newBar(d, n.ID, d.win)
				d.bars[n.ID] = b
			}
			tabs := make([]tab, len(n.Kids))
			for i, k := range n.Kids {
				tabs[i] = tab{id: k.ID, Face: d.faceOf(k)}
			}
			b.set(tabs, n.Front, false, u)
			if old := d.fronts[n.ID]; old != "" && old != n.Front {
				i, j := n.index(old), n.index(n.Front)
				for _, it := range n.front().shown(nil) {
					switch {
					case i < 0:
					case j < i:
						from[it] = -1
					case j > i:
						from[it] = 1
					}
				}
			}
			d.fronts[n.ID] = n.Front
			if f := n.front(); f != nil {
				walk(f, false)
			}
		case Row, Column:
			keep[n.ID] = true
			splits[n.ID] = splitWas{kind: n.Kind, kids: [2]string{n.Kids[0].ID, n.Kids[1].ID},
				items: [2][]string{n.Kids[0].Items(), n.Kids[1].Items()}}
			s := d.shares[n.ID]
			target := shareOf(n.Share)
			if s == nil {
				s = anim.NewFloat(target)
				if moving {
					// The side new here slides in.
					switch {
					case !seen(was, n.Kids[1]) && seen(was, n.Kids[0]):
						s.Jump(1)
					case !seen(was, n.Kids[0]) && seen(was, n.Kids[1]):
						s.Jump(0)
					}
				}
				d.shares[n.ID] = s
			}
			switch {
			case n.ID == d.held:
			case moving:
				s.Animate(target, slide)
			default:
				s.Jump(target)
			}
			for _, k := range n.Kids {
				walk(k, false)
			}
		}
	}
	if root != nil {
		walk(root, true)
	}
	if d.top != nil && (root == nil || root.Kind != Group) {
		// No group at the root: the title bar's bar shows no tabs.
		d.top.group = ""
		d.top.set(nil, "", false, u)
	}
	for _, it := range shown {
		f := was[it]
		if f == nil {
			f = &fadeIn{on: anim.NewFloat(1)}
			if moving {
				f.on.Jump(0)
				f.on.Animate(1, slide)
				f.from = from[it]
			}
		}
		d.showing[it] = f
	}
	d.closeSplits(splits, moving, slide)
	for id := range d.bars {
		if !keep[id] {
			delete(d.bars, id)
		}
	}
	for id := range d.shares {
		if !keep[id] {
			delete(d.shares, id)
		}
	}
	for id := range d.fronts {
		if !keep[id] {
			delete(d.fronts, id)
		}
	}
	for it, n := range d.nodes {
		if d.l.Node(it) == nil {
			delete(d.nodes, it)
			delete(d.itemOf, n)
		}
	}
	d.marks(u)
	u.Invalidate()
}

// closeSplits notes the splits gone since the last sync, now splits:
// where one side stays in the window and none of the other's items
// does, the side kept grows over the room of the side gone, while
// moving, as a new side slides in. Not moving, every side jumps to its
// place. A side growing that leaves the window, or the node it grew in,
// stops growing there.
func (d *Dock) closeSplits(splits map[string]splitWas, moving bool, slide anim.Spring) {
	here := func(id string) bool {
		win, ok := d.l.Where(id)
		return ok && win == d.win
	}
	parentOf := func(id string) string {
		if p := d.l.Parent(id); p != nil {
			return p.ID
		}
		return ""
	}
	for kept, c := range d.closing {
		if !moving || !here(kept) || parentOf(kept) != c.parent {
			delete(d.closing, kept)
		}
	}
	for id, sp := range d.splits {
		if _, ok := splits[id]; ok || !moving {
			continue
		}
		for k := range 2 {
			if !here(sp.kids[k]) || slices.ContainsFunc(sp.items[1-k], here) {
				continue
			}
			share := float32(0.5)
			if s := d.shares[id]; s != nil {
				share = min(max(s.Value(), 0), 1)
			}
			if id == d.held {
				share = d.heldShare
			}
			c := &closingSide{kind: sp.kind, kept: k, share: share, gone: anim.NewFloat(1), parent: parentOf(sp.kids[k])}
			c.gone.Animate(0, slide)
			d.closing[sp.kids[k]] = c
		}
	}
	d.splits = splits
}

// seen reports whether any item of n was shown, in was.
func seen(was map[string]*fadeIn, n *Node) bool {
	for _, it := range n.Items() {
		if was[it] != nil {
			return true
		}
	}
	return false
}

// index returns where tab id stands in group n, -1 for nowhere.
func (n *Node) index(id string) int {
	return slices.IndexFunc(n.Kids, func(k *Node) bool { return k.ID == id })
}

// marks underlines the tab in front of the pane the keys work in, where
// the window shows more than one pane, or with MarkAlone, one.
func (d *Dock) marks(u *gunim.UI) {
	active := d.Active()
	bars := 0
	for _, b := range d.allBars() {
		if b.group != "" {
			bars++
		}
	}
	for _, b := range d.allBars() {
		on := false
		if g := d.l.Node(b.group); g != nil && (bars > 1 || d.MarkAlone) {
			if f := g.front(); f != nil && slices.Contains(f.Items(), active) {
				on = true
			}
		}
		b.set(b.tabs, b.front, on, u)
	}
}

// allBars returns every bar of the dock's, the top one too.
func (d *Dock) allBars() []*Bar {
	out := make([]*Bar, 0, len(d.bars)+1)
	for _, b := range d.bars {
		out = append(out, b)
	}
	if d.top != nil && d.TitleBar {
		out = append(out, d.top)
	}
	return out
}

// faceOf is how node n's tab looks.
func (d *Dock) faceOf(n *Node) Face {
	if d.Face != nil {
		return d.Face(n)
	}
	items := n.Items()
	f := Face{Title: n.ID}
	if n.Kind != Item && len(items) > 0 {
		f.Title = items[0]
		if len(items) > 1 {
			f.More = "+" + strconv.Itoa(len(items)-1)
		}
	}
	return f
}

// shareOf is a split's share, 0 read as half.
func shareOf(s float32) float32 {
	if s <= 0 || s >= 1 {
		return 0.5
	}
	return s
}

// change hands c to OnChange, or sends it.
func (d *Dock) change(c Change, from gunim.Node, u *gunim.UI) {
	var in gunim.Intent = c
	if d.OnChange != nil {
		in = d.OnChange(c, u)
	}
	if in != nil {
		u.Send(from, in)
	}
}

// pick brings tab id to the front at once, before the application says
// so, and has the keys work in it.
func (d *Dock) pick(id string, from gunim.Node, u *gunim.UI) {
	n := d.l.Node(id)
	if n == nil {
		return
	}
	if !d.l.Shows(id) {
		d.l = d.l.Clone()
		d.l.Show(id)
		d.change(Show{ID: id}, from, u)
	}
	if items := n.shown(nil); len(items) > 0 && !slices.Contains(items, d.active) {
		d.active = items[0]
	}
	d.sync(u)
	d.focus(u, true)
}

// focus puts the keyboard in the view of the item the keys work in,
// once it is laid out where it is new.
func (d *Dock) focus(u *gunim.UI, laidOut bool) {
	v := d.node(d.Active())
	if v == nil || !d.laid || u == nil {
		return
	}
	if !d.mounted[v] {
		u.Insert(d, v)
		d.mounted[v] = true
	}
	if laidOut {
		u.FocusFirstLaidOut(v)
	} else {
		u.FocusFirst(v)
	}
}

// StepTab brings the next tab of the pane the keys work in to the
// front, or the one before, round from the last to the first.
func (d *Dock) StepTab(next bool, u *gunim.UI) {
	// The tab the pane stands in: the pane itself, or the node round it
	// that is a group's tab.
	child := d.Active()
	g := d.l.GroupOf(child)
	for g == nil {
		p := d.l.Parent(child)
		if p == nil {
			return
		}
		child, g = p.ID, d.l.GroupOf(p.ID)
	}
	if len(g.Kids) < 2 {
		return
	}
	i := g.index(child)
	step := 1
	if !next {
		step = len(g.Kids) - 1
	}
	d.pick(g.Kids[(i+step)%len(g.Kids)].ID, d, u)
}

// StepPane has the keys work in the next pane the window shows, or the
// one before, round from the last to the first.
func (d *Dock) StepPane(next bool, u *gunim.UI) {
	shown := d.Shown()
	if len(shown) < 2 {
		return
	}
	i := slices.Index(shown, d.Active())
	step := 1
	if !next {
		step = len(shown) - 1
	}
	d.SetActive(shown[(i+step)%len(shown)], u)
	d.focus(u, false)
}

// CatchKey implements [gunim.KeyCatcher]: with Keys, the keys that step
// through the tabs and the panes.
func (d *Dock) CatchKey(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !d.Keys || !ok || !k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) {
		return false
	}
	switch k.Key {
	case input.KeyPageUp, input.KeyPageDown:
		d.StepTab(k.Key == input.KeyPageDown, u)
	case input.KeyTab:
		d.StepPane(!k.Mods.Has(input.ModShift), u)
	default:
		return false
	}
	return true
}

// Overhear implements [gunim.Overhearer]: a press in a pane has the
// keys work in it.
func (d *Dock) Overhear(e input.Event, u *gunim.UI) {
	down, ok := e.(input.PointerDown)
	if !ok {
		return
	}
	at, ok := u.Bounds(d)
	if !ok {
		return
	}
	p := down.Pos.Sub(at.Min)
	for it, r := range d.items {
		if r.Contains(p) {
			d.SetActive(it, u)
		}
	}
}

// arrange lays the window's tree out in r.
func (d *Dock) arrange(th *theme.Live, r geom.Rect) {
	d.groups, d.dividers = d.groups[:0], d.dividers[:0]
	clear(d.items)
	w := d.l.Window(d.win)
	if w == nil || w.Root == nil {
		return
	}
	gap, barH, space := Gap.Get(th), BarHeight.Get(th), BarSpace.Get(th)
	var walk func(n *Node, r geom.Rect, root bool)
	walk = func(n *Node, r geom.Rect, root bool) {
		if c := d.closing[n.ID]; c != nil {
			r = c.room(r, gap)
		}
		switch n.Kind {
		case Item:
			d.items[n.ID] = r
		case Group:
			g := placedGroup{id: n.ID, whole: r, area: r}
			if !root || !d.TitleBar {
				g.bar = d.bars[n.ID]
				g.top = geom.Rc(r.Min.X, r.Min.Y, r.Size().W, barH)
				g.area.Min.Y = min(r.Min.Y+barH+space, r.Max.Y)
			}
			d.groups = append(d.groups, g)
			if f := n.front(); f != nil {
				walk(f, g.area, false)
			}
		case Row, Column:
			share := shareOf(n.Share)
			if s := d.shares[n.ID]; s != nil {
				share = min(max(s.Value(), 0), 1)
			}
			if n.ID == d.held {
				share = d.heldShare
			}
			var a, b, dv geom.Rect
			if n.Kind == Row {
				x := r.Min.X + max(r.Size().W-gap, 0)*share
				a = geom.Rect{Min: r.Min, Max: geom.Pt(x, r.Max.Y)}
				dv = geom.Rect{Min: geom.Pt(x, r.Min.Y), Max: geom.Pt(x+gap, r.Max.Y)}
				b = geom.Rect{Min: geom.Pt(x+gap, r.Min.Y), Max: r.Max}
			} else {
				y := r.Min.Y + max(r.Size().H-gap, 0)*share
				a = geom.Rect{Min: r.Min, Max: geom.Pt(r.Max.X, y)}
				dv = geom.Rect{Min: geom.Pt(r.Min.X, y), Max: geom.Pt(r.Max.X, y+gap)}
				b = geom.Rect{Min: geom.Pt(r.Min.X, y+gap), Max: r.Max}
			}
			d.dividers = append(d.dividers, placedDivider{id: n.ID, kind: n.Kind, rect: dv, whole: r})
			walk(n.Kids[0], a, false)
			walk(n.Kids[1], b, false)
		}
	}
	walk(w.Root, r, true)
}

// Layout implements [gunim.Node]: the dock fills its room, each view in
// its place and each bar over its group.
func (d *Dock) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	d.size = size
	d.arrange(f.Theme, geom.Rect{Max: size.Point()})
	type want struct {
		n gunim.Node
		r geom.Rect
	}
	var wants []want
	for _, it := range d.Shown() {
		if n := d.node(it); n != nil {
			wants = append(wants, want{n, d.items[it]})
		}
	}
	for _, g := range d.groups {
		if g.bar != nil {
			g.bar.Caption = d.Caption && g.top.Min.Y <= 0
			g.bar.reserve = 0
			if g.top.Min.Y <= 0 && g.top.Max.X >= size.W {
				g.bar.reserve = d.Corner
			}
			wants = append(wants, want{g.bar, g.top})
		}
	}
	got := map[gunim.Node]bool{}
	clear(d.mounted)
	for k := range kids.All {
		n := k.Node()
		i := slices.IndexFunc(wants, func(w want) bool { return w.n == n })
		if i < 0 {
			if k.Presence() != gunim.Exiting {
				kids.Drop(n)
			}
			k.Layout(gunim.Tight(k.Size()))
			continue
		}
		if k.Presence() == gunim.Exiting {
			continue
		}
		got[n], d.mounted[n] = true, true
		k.Layout(gunim.Tight(wants[i].r.Size()))
		k.Place(wants[i].r.Min)
	}
	for _, w := range wants {
		if got[w.n] {
			continue
		}
		k := kids.Build(w.n)
		d.mounted[w.n] = true
		k.Layout(gunim.Tight(w.r.Size()))
		k.Place(w.r.Min)
	}
	d.laid = true
	return size
}

// Step implements [gunim.Animator]: the fades, the splits' glides and
// the sides that grow over a side gone.
func (d *Dock) Step(dt time.Duration) bool {
	moving := d.Group.Step(dt)
	for _, f := range d.showing {
		if f.on.Step(dt) {
			moving = true
		}
	}
	for _, s := range d.shares {
		if s.Step(dt) {
			moving = true
		}
	}
	for kept, c := range d.closing {
		if c.gone.Step(dt) {
			moving = true
		} else {
			delete(d.closing, kept)
		}
	}
	return moving
}

// Paint implements [gunim.Node]: the views, each fading in as it comes,
// then the bars, the dividers, and where a tab dragged would land.
func (d *Dock) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	for k := range kids.All {
		if _, bar := k.Node().(*Bar); bar {
			continue
		}
		it := d.itemOf[k.Node()]
		fade := d.showing[it]
		if fade == nil || fade.on.Value() >= 0.999 {
			k.Paint(p)
			continue
		}
		on := min(max(fade.on.Value(), 0), 1)
		r := d.items[it]
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: on, Clip: true})()
			defer p.Push(paint.Translate(geom.Pt(fade.from*24*(1-on), 0)))()
			k.Paint(p)
		}()
	}
	for k := range kids.All {
		if _, bar := k.Node().(*Bar); bar {
			k.Paint(p)
		}
	}
	for _, dv := range d.dividers {
		c := Divider.Get(th)
		if dv.id == d.hot || dv.id == d.held {
			c = DividerHot.Get(th)
		}
		m := dv.rect.Center()
		if dv.kind == Row {
			p.RRect(geom.Rc(m.X-1, m.Y-20, 2, 40), 1, paint.Solid(c))
		} else {
			p.RRect(geom.Rc(m.X-20, m.Y-1, 40, 2), 1, paint.Solid(c))
		}
	}
	if lit := min(d.dropLit.Value(), 1); lit > 0.01 {
		at := d.drop.Value()
		fill, ring := DropFill.Get(th), DropRing.Get(th)
		fill.A = uint8(float32(fill.A) * lit)
		ring.A = uint8(float32(ring.A) * lit)
		radius := BarRadius.Get(th)
		p.RRect(at, radius, paint.Solid(fill))
		p.RRectStroke(at.Inset(geom.Uniform(1)), radius, paint.Fill{}, paint.Stroke{Width: 2, Color: ring})
	}
	if len(d.groups) == 0 && len(d.items) == 0 {
		run := widget.Font.Get(th).Shape(EmptyText.Get(th), widget.TextSize.Get(th))
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, (box.H-run.Height())/2), EmptyInk.Get(th))
	}
}

// Covers implements [gunim.Shaped]: holding the views, the dock takes
// the pointer everywhere; drawing the bars alone, only on its dividers
// and in a window that holds nothing, so the views under it take it.
func (d *Dock) Covers(p geom.Point) bool {
	if d.Content != nil || len(d.groups) == 0 && len(d.items) == 0 {
		return true
	}
	for _, g := range d.groups {
		if g.bar != nil && g.top.Contains(p) {
			return true
		}
	}
	return d.dividerAt(p) >= 0
}

// dividerAt is the divider at p, -1 for none.
func (d *Dock) dividerAt(p geom.Point) int {
	for i, dv := range d.dividers {
		if dv.rect.Inset(geom.Uniform(-2)).Contains(p) {
			return i
		}
	}
	return -1
}

// Cursor implements [gunim.CursorShaper].
func (d *Dock) Cursor(p geom.Point) input.Cursor {
	i := d.dividerAt(p)
	if d.held != "" {
		i = slices.IndexFunc(d.dividers, func(dv placedDivider) bool { return dv.id == d.held })
	}
	switch {
	case i < 0:
		return input.CursorArrow
	case d.dividers[i].kind == Row:
		return input.CursorResizeH
	}
	return input.CursorResizeV
}

// Focusable implements [gunim.Focusable]: the keys go to the views.
func (d *Dock) Focusable() bool { return false }

// Handle implements [gunim.Handler]: a divider dragged, and a tab
// dragged over the views.
func (d *Dock) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if d.held != "" {
			d.dragDivider(e.Pos, u)
			return true
		}
		hot := ""
		if i := d.dividerAt(e.Pos); i >= 0 {
			hot = d.dividers[i].id
		}
		if hot != d.hot {
			d.hot = hot
			u.Invalidate()
		}
		return hot != ""
	case input.PointerLeave:
		if d.hot != "" {
			d.hot = ""
			u.Invalidate()
		}
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			if d.held != "" {
				// A second button puts the divider back.
				if s, n := d.shares[d.held], d.l.Node(d.held); s != nil && n != nil {
					s.Jump(shareOf(n.Share))
				}
				d.held = ""
				u.Invalidate()
				return true
			}
			return false
		}
		i := d.dividerAt(e.Pos)
		if i < 0 {
			return false
		}
		dv := d.dividers[i]
		n := d.l.Node(dv.id)
		if n == nil {
			// A split the layout has let go since the last frame.
			return true
		}
		if e.Clicks == 2 {
			d.resize(dv.id, 0.5, u)
			return true
		}
		d.held, d.heldShare = dv.id, shareOf(n.Share)
		if s := d.shares[dv.id]; s != nil {
			d.heldShare = s.Value()
		}
		return true
	case input.PointerUp:
		if d.held == "" {
			return false
		}
		id, share := d.held, d.heldShare
		d.held = ""
		d.resize(id, share, u)
		return true
	case input.DragOver, input.DragLeave, input.Drop:
		return d.DragEvent(e, u)
	}
	return false
}

// dragDivider moves the divider held to follow the pointer at p,
// leaving each side minPane at least.
func (d *Dock) dragDivider(p geom.Point, u *gunim.UI) {
	i := slices.IndexFunc(d.dividers, func(dv placedDivider) bool { return dv.id == d.held })
	if i < 0 {
		return
	}
	dv := d.dividers[i]
	gap := dv.rect.Size().W
	at, length := p.X-dv.whole.Min.X, dv.whole.Size().W
	if dv.kind == Column {
		gap = dv.rect.Size().H
		at, length = p.Y-dv.whole.Min.Y, dv.whole.Size().H
	}
	room := max(length-gap, 1)
	least := min(minPane/room, 0.5)
	d.heldShare = min(max((at-gap/2)/room, least), 1-least)
	if s := d.shares[dv.id]; s != nil {
		s.Jump(d.heldShare)
	}
	u.Invalidate()
}

// resize sets split id's share at once, before the application says
// so, and asks it to keep it.
func (d *Dock) resize(id string, share float32, u *gunim.UI) {
	d.l = d.l.Clone()
	if !d.l.SetShare(id, share) {
		return
	}
	d.sync(u)
	d.change(Resize{ID: id, Share: d.l.Node(id).Share}, d, u)
}

// DragEvent takes a tab dragged over the window: where it would land
// lights, and a drop there moves it. An application that draws the
// views itself passes it the drags over them. It reports whether it
// took e.
func (d *Dock) DragEvent(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.DragOver:
		dr, ok := e.Data.(Drag)
		if !ok {
			return false
		}
		_, at, split, ok := d.dropAt(e.Pos, dr, u.Theme())
		d.light(at, ok, u)
		if !ok {
			return false
		}
		hint := DockHere.Get(u.Theme())
		if split {
			hint = SplitHere.Get(u.Theme())
		}
		u.AnswerDrag(widget.DropHint{Text: hint, Effect: widget.DropMove})
		return true
	case input.DragLeave:
		d.light(geom.Rect{}, false, u)
		return false
	case input.Drop:
		dr, ok := e.Data.(Drag)
		if !ok {
			return false
		}
		to, _, _, ok := d.dropAt(e.Pos, dr, u.Theme())
		d.light(geom.Rect{}, false, u)
		if !ok {
			return false
		}
		d.change(Move{ID: dr.ID, To: to}, d, u)
		return true
	}
	return false
}

// light lights at, where a tab would land, or puts the light out.
func (d *Dock) light(at geom.Rect, on bool, u *gunim.UI) {
	th := u.Theme()
	if on {
		if d.dropLit.Target() < 0.5 {
			d.drop.Jump(at)
		} else {
			d.drop.Animate(at, widget.Quick.Get(th))
		}
		d.dropLit.Animate(1, widget.Quick.Get(th))
	} else {
		d.dropLit.Animate(0, widget.Settle.Get(th))
	}
	u.Invalidate()
}

// dropAt is where tab dr, dragged to p, would go, the room that lights
// to say so, and whether it splits a pane; false for nowhere new.
func (d *Dock) dropAt(p geom.Point, dr Drag, th *theme.Live) (to Target, at geom.Rect, split, ok bool) {
	moved := d.l.Node(dr.ID)
	if moved == nil {
		return Target{}, geom.Rect{}, false, false
	}
	if len(d.groups) == 0 && len(d.items) == 0 {
		return Target{Win: d.win, Index: -1}, geom.Rect{Max: d.size.Point()}, false, true
	}
	edge := EdgeShare.Get(th)
	// The innermost group first: groups are laid out outermost first.
	for _, g := range slices.Backward(d.groups) {
		if !g.area.Contains(p) || g.top.Contains(p) {
			continue
		}
		// The pane under p: the group, grouped, or the item there.
		pane, room := g.id, g.area
		if !d.l.Grouped {
			for it, r := range d.items {
				if r.Contains(p) {
					pane, room = it, r
				}
			}
		}
		s := room.Size()
		if s.W <= 0 || s.H <= 0 {
			return Target{}, geom.Rect{}, false, false
		}
		fx, fy := (p.X-room.Min.X)/s.W, (p.Y-room.Min.Y)/s.H
		side, lit, near := Into, g.area, edge
		for _, c := range []struct {
			side Side
			dist float32
			at   geom.Rect
		}{
			{Left, fx, geom.Rc(room.Min.X, room.Min.Y, s.W/2, s.H)},
			{Right, 1 - fx, geom.Rc(room.Min.X+s.W/2, room.Min.Y, s.W/2, s.H)},
			{Top, fy, geom.Rc(room.Min.X, room.Min.Y, s.W, s.H/2)},
			{Bottom, 1 - fy, geom.Rc(room.Min.X, room.Min.Y+s.H/2, s.W, s.H/2)},
		} {
			if c.dist < near {
				side, lit, near = c.side, c.at, c.dist
			}
		}
		group := d.l.Node(g.id)
		if pane == dr.ID || moved.path(pane, nil) != nil {
			// Onto itself.
			return Target{}, geom.Rect{}, false, false
		}
		if side == Into {
			if group.index(dr.ID) >= 0 {
				return Target{}, geom.Rect{}, false, false
			}
			return Target{At: g.id, Win: d.win, Index: -1}, lit, false, true
		}
		if d.l.Grouped && len(group.Kids) == 1 && group.Kids[0].ID == dr.ID {
			// Beside its own group, alone in it: where it is.
			return Target{}, geom.Rect{}, false, false
		}
		return Target{At: pane, Win: d.win, Side: side}, lit, true, true
	}
	return Target{}, geom.Rect{}, false, false
}

// detachSize is how large a window of its own a tab of group g opens:
// as large as the group.
func (d *Dock) detachSize(g string) geom.Size {
	for _, pg := range d.groups {
		if pg.id == g {
			return pg.whole.Size()
		}
	}
	return d.size
}
