package graph

import (
	"maps"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
)

// View shows a [Graph] on a plane the user pans and zooms.
//
// A drag on empty room pans the plane; the wheel pans it too, gliding
// a notch at a time, and with Ctrl, or a pinch, zooms it about the
// pointer. A drag on a box moves it, with the other boxes picked; a
// box or a wire held near an edge scrolls the plane toward it. A drag
// from a port draws a wire: the ports it may join glow, the others
// dim, and it snaps to the nearest within reach, turning RefuseInk
// where Accepts refuses. A drag from an input a wire joins takes the
// wire's end, to another input or, let go on nothing, out. A click
// picks a box, or a wire; with Shift it adds a box to those picked or
// takes it out, and a drag with Shift on empty room draws a band that
// picks the boxes it covers. A double click opens a box.
//
// Delete takes what is picked out, Escape lets go of what is held or
// picked, and Home fits every box in view. A box the application moves
// glides to its place, as an undo moves it.
type View struct {
	anim.Group
	// Content gives the node that fills a box's Body, the same node
	// each time; nil leaves it empty.
	Content func(node string) gunim.Node
	// Accepts says whether a wire may join output from to input to,
	// and, where not, why. Nil lets a wire join ports of one Kind, on
	// two boxes, that no wire joins already.
	Accepts func(from, to End) (ok bool, why string)
	// OnChange, when set, runs on the UI goroutine with what the user
	// asks for; a non-nil result is sent to the application as the
	// view's intent. Without it the Change is sent itself.
	OnChange func(c Change, u *gunim.UI) gunim.Intent

	g    Graph
	laid bool
	size geom.Size
	// zoom, panX and panY are the camera: the plane drawn zoom times
	// life size, its origin at pan. wheel keeps the plane's point under
	// the pointer still while a wheel's zoom glides.
	zoom, panX, panY *anim.Float
	wheel            struct {
		on    bool
		at, w geom.Point
	}
	fitted bool
	// places are where each box stands as it glides, and order the
	// order they are drawn in, the last on top.
	places map[string]*place
	order  []string
	// picked are the boxes picked, and wire the wire picked.
	picked map[string]bool
	wire   string
	// hover is what is under the pointer, and pointer where it is.
	hover   target
	pointer geom.Point
	pr      press
	// phase is how far each wire's dots have run.
	phase map[string]float32
	// joined says which ports a wire joins, by end and side.
	joined map[portKey]bool
	// nodes are the boxes' contents, by box, and boxOf each one's box.
	nodes map[string]gunim.Node
	boxOf map[gunim.Node]string
	// runs are text shaped, kept from frame to frame.
	runs runCache
	// m are the boxes' measures, from the theme the view last laid out
	// in.
	m measures
	// idx is each box's place in the graph, by its ID.
	idx map[string]int
	// shapes are the wires' curves, worked out once for each seq: seq
	// counts the changes to where the boxes stand.
	shapes map[string]wirePoints
	seq    int
	// replay is how far the plane scrolled under a held drag, which the
	// engine's replay of the pointer takes off.
	replay geom.Point
}

// measures are the boxes' sizes, from the theme.
type measures struct{ width, head, row float32 }

// measuresOf are the measures th gives.
func measuresOf(th *theme.Live) measures {
	return measures{NodeWidth.Get(th), HeadHeight.Get(th), RowHeight.Get(th)}
}

// wirePoints are a wire's curve, as at seq.
type wirePoints struct {
	seq int
	sh  wireShape
	pts []geom.Point
}

// place is where a box stands as it glides to its place.
type place struct{ x, y *anim.Float }

// portKey names a port: its end, and whether it is an output.
type portKey struct {
	End
	out bool
}

// target is what stands under a point: a box, a port of one, or a
// wire.
type target struct {
	node string
	port portKey
	wire string
}

func (t target) isPort() bool { return t.port.Node != "" }

// pressKind is what a press holds.
type pressKind uint8

const (
	pressNone pressKind = iota
	// pressPan moves the plane.
	pressPan
	// pressMove moves boxes.
	pressMove
	// pressLink draws a wire from a port.
	pressLink
	// pressReplug takes a wire's input end elsewhere.
	pressReplug
	// pressBand draws a band to pick boxes by.
	pressBand
)

// press is a press under way.
type press struct {
	kind   pressKind
	moved  bool
	from   geom.Point
	clicks int
	shift  bool
	pan    geom.Point
	// node is the box pressed, grab where on the plane it was taken,
	// and start where each box moving stood.
	node  string
	grab  geom.Point
	start map[string]geom.Point
	// port is the port a link starts from; wire the wire whose input
	// end a replug carries.
	port portKey
	wire string
	// to is the loose end, on the plane; snap the port it would join,
	// where snapped, refused with why where Accepts says no.
	to      geom.Point
	snap    portKey
	snapped bool
	why     string
	// was are the boxes picked as a band began.
	was map[string]bool
}

// The view's measures: how near a press must come to a port and to a
// wire, in pixels, how far a loose end reaches to snap to a port, and
// how far a press goes before it moves anything.
const (
	portHit  = 11
	wireHit  = 7
	snapNear = 28
	slop     = 4
	// zoomMin and zoomMax bound the zoom, and labelsFrom is the zoom
	// below which the ports' names and the boxes' lines go.
	zoomMin    = 0.2
	zoomMax    = 3
	labelsFrom = 0.45
	// keep is how much of the boxes, in pixels, the plane keeps in view
	// as it is panned.
	keep = 80
	// dotGap is how far apart a wire's dots run, at a zoom of 1.
	dotGap = 30
)

// NewView returns a view of no boxes.
func NewView() *View {
	v := &View{zoom: anim.NewFloat(1), panX: anim.NewFloat(0), panY: anim.NewFloat(0), places: map[string]*place{},
		picked: map[string]bool{}, phase: map[string]float32{}, joined: map[portKey]bool{}, nodes: map[string]gunim.Node{},
		boxOf: map[gunim.Node]string{}, idx: map[string]int{}, shapes: map[string]wirePoints{},
		m: measures{NodeWidth.Default(), HeadHeight.Default(), RowHeight.Default()}}
	v.Add(v.zoom, v.panX, v.panY)
	return v
}

// Graph returns the graph the view shows, the boxes where the
// application last put them.
func (v *View) Graph() Graph { return v.g.Clone() }

// SetGraph shows g, of which the view keeps a copy. Once laid out, a box
// the application moves glides to its place; before, or with a nil u,
// it is there. The first graph shown is fitted in view.
func (v *View) SetGraph(g Graph, u *gunim.UI) {
	v.g = g.Clone()
	v.seq++
	clear(v.idx)
	for i, n := range v.g.Nodes {
		v.idx[n.ID] = i
	}
	if u != nil {
		v.m = measuresOf(u.Theme())
	}
	var spring anim.Spring
	if u != nil {
		spring = Place.Get(u.Theme())
	}
	seen := map[string]bool{}
	for _, n := range v.g.Nodes {
		seen[n.ID] = true
		pl := v.places[n.ID]
		if pl == nil {
			pl = &place{x: anim.NewFloat(n.At.X), y: anim.NewFloat(n.At.Y)}
			v.places[n.ID] = pl
			v.order = append(v.order, n.ID)
			continue
		}
		if v.pr.kind == pressMove && v.moving(n.ID) {
			if v.pr.moved {
				// Held: it follows the pointer, not the graph.
				continue
			}
			// Pressed and not moved yet: it moves from here.
			v.pr.start[n.ID] = n.At
		}
		if v.laid && u != nil {
			pl.x.Animate(n.At.X, spring)
			pl.y.Animate(n.At.Y, spring)
		} else {
			pl.x.Jump(n.At.X)
			pl.y.Jump(n.At.Y)
		}
	}
	for id := range v.places {
		if !seen[id] {
			delete(v.places, id)
			delete(v.picked, id)
		}
	}
	v.order = slices.DeleteFunc(v.order, func(id string) bool { return !seen[id] })
	clear(v.joined)
	wires := map[string]bool{}
	for _, w := range v.g.Wires {
		wires[w.ID] = true
		v.joined[portKey{w.From, true}] = true
		v.joined[portKey{w.To, false}] = true
	}
	if !wires[v.wire] {
		v.wire = ""
	}
	for id := range v.phase {
		if !wires[id] {
			delete(v.phase, id)
		}
	}
	for id := range v.shapes {
		if !wires[id] {
			delete(v.shapes, id)
		}
	}
	for id, n := range v.nodes {
		if !seen[id] {
			delete(v.nodes, id)
			delete(v.boxOf, n)
		}
	}
	switch v.pr.kind {
	case pressLink:
		if _, ok := v.g.Port(v.pr.port.End, v.pr.port.out); !ok {
			v.pr = press{}
		}
	case pressReplug:
		if v.g.Wire(v.pr.wire) == nil {
			v.pr = press{}
		}
	case pressNone, pressPan, pressMove, pressBand:
	}
	u.Invalidate()
}

// node returns box id, by the index, or nil.
func (v *View) node(id string) *Node {
	if i, ok := v.idx[id]; ok && i < len(v.g.Nodes) && v.g.Nodes[i].ID == id {
		return &v.g.Nodes[i]
	}
	return nil
}

// moving reports whether box id moves with the press under way.
func (v *View) moving(id string) bool {
	_, ok := v.pr.start[id]
	return ok
}

// Picked returns the boxes picked, in the order they are drawn, and
// the wire picked, "" for none.
func (v *View) Picked() (nodes []string, wire string) {
	for _, id := range v.order {
		if v.picked[id] {
			nodes = append(nodes, id)
		}
	}
	return nodes, v.wire
}

// SetPicked picks boxes nodes, or else wire, and sends nothing.
func (v *View) SetPicked(nodes []string, wire string, u *gunim.UI) {
	clear(v.picked)
	for _, id := range nodes {
		if v.places[id] != nil {
			v.picked[id] = true
		}
	}
	v.wire = ""
	if len(nodes) == 0 && v.g.Wire(wire) != nil {
		v.wire = wire
	}
	u.Invalidate()
}

// Zoom returns how many times life size the plane is drawn, as the
// zoom heads.
func (v *View) Zoom() float32 { return v.zoom.Target() }

// ToPlane returns where view point p is on the plane, and ToView where
// plane point p is in the view, as the camera stands now.
func (v *View) ToPlane(p geom.Point) geom.Point {
	z, pan := v.cam()
	return p.Sub(pan).Mul(1 / z)
}

// ToView is the reverse of ToPlane.
func (v *View) ToView(p geom.Point) geom.Point {
	z, pan := v.cam()
	return p.Mul(z).Add(pan)
}

func (v *View) cam() (float32, geom.Point) {
	return v.zoom.Value(), geom.Pt(v.panX.Value(), v.panY.Value())
}

// FitAll glides the plane to show every box; with a nil u, or before
// the view is laid out, it is there at once.
func (v *View) FitAll(u *gunim.UI) {
	b, ok := v.bounds()
	if !ok || v.size.W <= 0 {
		v.fitted = false
		return
	}
	const margin = 48
	room := geom.Sz(max(v.size.W-2*margin, 1), max(v.size.H-2*margin, 1))
	z := min(room.W/max(b.Size().W, 1), room.H/max(b.Size().H, 1), 1.1)
	z = min(max(z, zoomMin), zoomMax)
	mid := b.Center()
	v.setCam(z, v.size.Point().Mul(0.5).Sub(mid.Mul(z)), u)
}

// setCam moves the camera to zoom z with the plane's origin at pan,
// gliding where u says the view shows.
func (v *View) setCam(z float32, pan geom.Point, u *gunim.UI) {
	v.wheel.on = false
	if v.laid && u != nil {
		c := Camera.Get(u.Theme())
		v.zoom.Animate(z, c)
		v.panX.Animate(pan.X, c)
		v.panY.Animate(pan.Y, c)
		return
	}
	v.zoom.Jump(z)
	v.panX.Jump(pan.X)
	v.panY.Jump(pan.Y)
}

// bounds is the room on the plane every box stands in.
func (v *View) bounds() (geom.Rect, bool) {
	var out geom.Rect
	for i, n := range v.g.Nodes {
		r := v.rectOf(&n)
		if i == 0 {
			out = r
			continue
		}
		out = out.Union(r)
	}
	return out, len(v.g.Nodes) > 0
}

// panTarget is where the plane's origin heads: where a wheel's zoom
// takes it, while one glides.
func (v *View) panTarget() geom.Point {
	if v.wheel.on {
		return v.wheel.at.Sub(v.wheel.w.Mul(v.zoom.Target()))
	}
	return geom.Pt(v.panX.Target(), v.panY.Target())
}

// keepInView is pan moved as little as keeps some of the boxes in
// view at zoom z.
func (v *View) keepInView(pan geom.Point, z float32) geom.Point {
	b, ok := v.bounds()
	if !ok || v.size.W <= 0 {
		return pan
	}
	lo, hi := b.Min.Mul(z), b.Max.Mul(z)
	kx, ky := min(keep, (hi.X-lo.X)/2), min(keep, (hi.Y-lo.Y)/2)
	pan.X = min(max(pan.X, kx-hi.X), v.size.W-kx-lo.X)
	pan.Y = min(max(pan.Y, ky-hi.Y), v.size.H-ky-lo.Y)
	return pan
}

// panBy moves the plane by d, at once, as far as keepInView lets it.
func (v *View) panBy(d geom.Point) {
	v.wheel.on = false
	pan := v.keepInView(geom.Pt(v.panX.Target(), v.panY.Target()).Add(d), v.zoom.Target())
	v.panX.Jump(pan.X)
	v.panY.Jump(pan.Y)
}

// wheelPan glides the plane by d, a wheel's notch; a notch more while
// it glides goes on from where the last was heading.
func (v *View) wheelPan(d geom.Point, th *theme.Live) {
	if v.wheel.on {
		v.wheel.at = v.wheel.at.Add(d)
		v.followWheel()
		return
	}
	pan := v.keepInView(geom.Pt(v.panX.Target(), v.panY.Target()).Add(d), v.zoom.Target())
	s := Wheel.Get(th)
	v.panX.Retarget(pan.X, s)
	v.panY.Retarget(pan.Y, s)
}

// zoomAbout zooms by k at once, keeping view point at where it is.
func (v *View) zoomAbout(at geom.Point, k float32) {
	z0, pan0 := v.zoom.Target(), v.panTarget()
	z := min(max(z0*k, zoomMin), zoomMax)
	w := at.Sub(pan0).Mul(1 / z0)
	v.wheel.on = false
	v.zoom.Jump(z)
	pan := at.Sub(w.Mul(z))
	v.panX.Jump(pan.X)
	v.panY.Jump(pan.Y)
}

// wheelZoom glides the zoom by k, keeping what is at view point at
// under it all the way.
func (v *View) wheelZoom(at geom.Point, k float32, th *theme.Live) {
	z0, pan0 := v.zoom.Target(), v.panTarget()
	z := min(max(z0*k, zoomMin), zoomMax)
	v.wheel.on, v.wheel.at, v.wheel.w = true, at, at.Sub(pan0).Mul(1/z0)
	v.zoom.Retarget(z, Wheel.Get(th))
	v.followWheel()
}

// followWheel keeps the point a wheel zooms about where it is, and
// lets it go once the zoom stands still.
func (v *View) followWheel() {
	if !v.wheel.on {
		return
	}
	pan := v.wheel.at.Sub(v.wheel.w.Mul(v.zoom.Value()))
	v.panX.Jump(pan.X)
	v.panY.Jump(pan.Y)
	if !v.zoom.Active() {
		v.wheel.on = false
	}
}

// at is where box n's top left stands now, on its way to its place.
func (v *View) at(n *Node) geom.Point {
	if pl := v.places[n.ID]; pl != nil {
		return geom.Pt(pl.x.Value(), pl.y.Value())
	}
	return n.At
}

// sizeOf is how large box n is.
func (v *View) sizeOf(n *Node) geom.Size {
	w := n.Width
	if w <= 0 {
		w = v.m.width
	}
	rows := max(len(n.Ins), len(n.Outs))
	h := v.m.head + n.Body + float32(rows)*v.m.row
	if rows > 0 {
		h += 8
	}
	return geom.Sz(w, max(h, v.m.head+6))
}

// rectOf is where box n stands on the plane now.
func (v *View) rectOf(n *Node) geom.Rect {
	at := v.at(n)
	return geom.Rect{Min: at, Max: at.Add(v.sizeOf(n).Point())}
}

// bodyOf is the room under box n's head for its content, on the plane.
func (v *View) bodyOf(n *Node) geom.Rect {
	r := v.rectOf(n)
	return geom.Rc(r.Min.X+1, r.Min.Y+v.m.head, r.Size().W-2, n.Body)
}

// portAt is where port i of box n stands on the plane: down its left
// edge for an input, its right for an output.
func (v *View) portAt(n *Node, out bool, i int) geom.Point {
	r := v.rectOf(n)
	x := r.Min.X
	if out {
		x = r.Max.X
	}
	return geom.Pt(x, r.Min.Y+v.m.head+n.Body+4+float32(i)*v.m.row+v.m.row/2)
}

// endAt is where the port k stands on the plane, and false where there
// is no such port.
func (v *View) endAt(k portKey) (geom.Point, bool) {
	n := v.node(k.Node)
	if n == nil {
		return geom.Point{}, false
	}
	ps := n.Ins
	if k.out {
		ps = n.Outs
	}
	for i, p := range ps {
		if p.ID == k.Port {
			return v.portAt(n, k.out, i), true
		}
	}
	return geom.Point{}, false
}

// nodeAt returns the top box at view point p, or "".
func (v *View) nodeAt(p geom.Point) string {
	w := v.ToPlane(p)
	for _, id := range slices.Backward(v.order) {
		if n := v.node(id); n != nil && v.rectOf(n).Contains(w) {
			return id
		}
	}
	return ""
}

// portUnder returns the port nearest view point p, within portHit,
// where the ports show.
func (v *View) portUnder(p geom.Point) (portKey, bool) {
	best, found, ok := float32(portHit), portKey{}, false
	for _, id := range slices.Backward(v.order) {
		n := v.node(id)
		if n == nil {
			continue
		}
		for _, out := range []bool{false, true} {
			ps := n.Ins
			if out {
				ps = n.Outs
			}
			for i, port := range ps {
				if d := dist(v.ToView(v.portAt(n, out, i)), p); d < best {
					best, found, ok = d, portKey{End{n.ID, port.ID}, out}, true
				}
			}
		}
	}
	return found, ok
}

// wireUnder returns the wire nearest view point p, within wireHit.
func (v *View) wireUnder(p geom.Point) (string, bool) {
	best, found, ok := float32(wireHit), "", false
	for _, w := range v.g.Wires {
		_, pts, okShape := v.wirePts(w)
		if !okShape {
			continue
		}
		for i := 1; i < len(pts); i++ {
			if d := segDist(p, v.ToView(pts[i-1]), v.ToView(pts[i])); d < best {
				best, found, ok = d, w.ID, true
			}
		}
	}
	return found, ok
}

// targetAt is what stands at view point p: a port first, then a box,
// then a wire.
func (v *View) targetAt(p geom.Point) target {
	if k, ok := v.portUnder(p); ok {
		return target{port: k}
	}
	if id := v.nodeAt(p); id != "" {
		return target{node: id}
	}
	if id, ok := v.wireUnder(p); ok {
		return target{wire: id}
	}
	return target{}
}

// accepts says whether a wire may join output from to input to.
func (v *View) accepts(from, to End) (ok bool, why string) {
	if v.Accepts != nil {
		return v.Accepts(from, to)
	}
	a, okA := v.g.Port(from, true)
	b, okB := v.g.Port(to, false)
	switch {
	case !okA || !okB:
		return false, "There is no such port."
	case from.Node == to.Node:
		return false, "A box joins no port of its own."
	case a.Kind != b.Kind:
		return false, "Those ports carry different things."
	case v.g.Joins(from, to):
		return false, "Those ports are joined already."
	}
	return true, ""
}

// joinable reports whether a wire from port k could join port to at
// all: the other side, the same kind. Accepts says whether it may.
func (v *View) joinable(k, to portKey) bool {
	if k.out == to.out || k.Node == to.Node {
		return false
	}
	a, _ := v.g.Port(k.End, k.out)
	b, _ := v.g.Port(to.End, to.out)
	return a.Kind == b.Kind
}

// snapFor is the port the loose end at view point p would join: the
// nearest that could take it, within snapNear.
func (v *View) snapFor(from portKey, p geom.Point) (portKey, bool) {
	best, found, ok := float32(snapNear), portKey{}, false
	for _, n := range v.g.Nodes {
		ps := n.Ins
		if !from.out {
			ps = n.Outs
		}
		for i, port := range ps {
			to := portKey{End{n.ID, port.ID}, !from.out}
			if !v.joinable(from, to) {
				continue
			}
			if d := dist(v.ToView(v.portAt(&n, !from.out, i)), p); d < best {
				best, found, ok = d, to, true
			}
		}
	}
	return found, ok
}

// ends are the output and the input a wire between ports a and b joins.
func ends(a, b portKey) (from, to End) {
	if a.out {
		return a.End, b.End
	}
	return b.End, a.End
}

// change hands c to OnChange, or sends it.
func (v *View) change(c Change, u *gunim.UI) {
	var in gunim.Intent = c
	if v.OnChange != nil {
		in = v.OnChange(c, u)
	}
	if in != nil {
		u.Send(v, in)
	}
}

// selectChange sends what is picked now.
func (v *View) selectChange(u *gunim.UI) {
	nodes, wire := v.Picked()
	v.change(Select{Nodes: nodes, Wire: wire}, u)
}

// raise draws box id over the others.
func (v *View) raise(id string) {
	if i := slices.Index(v.order, id); i >= 0 {
		v.order = append(slices.Delete(v.order, i, i+1), id)
	}
}

// Children implements [gunim.Composite]: the boxes' contents.
func (v *View) Children() []gunim.Node {
	var out []gunim.Node
	for _, n := range v.g.Nodes {
		if n.Body <= 0 {
			continue
		}
		if c := v.content(n.ID); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// content returns box id's content, from Content, or nil.
func (v *View) content(id string) gunim.Node {
	if v.Content == nil {
		return nil
	}
	if c, ok := v.nodes[id]; ok {
		return c
	}
	c := v.Content(id)
	if c != nil {
		v.nodes[id] = c
		v.boxOf[c] = id
	}
	return c
}

// Layout implements [gunim.Node]: the view fills its room, and each
// box's content its body, on the plane.
func (v *View) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	v.size = c.Max
	if m := measuresOf(f.Theme); m != v.m {
		v.m = m
		v.seq++
	}
	type want struct {
		c gunim.Node
		r geom.Rect
	}
	var wants []want
	for i := range v.g.Nodes {
		n := &v.g.Nodes[i]
		if n.Body <= 0 {
			continue
		}
		if c := v.content(n.ID); c != nil {
			wants = append(wants, want{c, v.bodyOf(n)})
		}
	}
	got := map[gunim.Node]bool{}
	for k := range kids.All {
		i := slices.IndexFunc(wants, func(w want) bool { return w.c == k.Node() })
		if i < 0 {
			if k.Presence() != gunim.Exiting {
				kids.Drop(k.Node())
			}
			k.Layout(gunim.Tight(k.Size()))
			continue
		}
		if k.Presence() == gunim.Exiting {
			continue
		}
		got[k.Node()] = true
		k.Layout(gunim.Tight(wants[i].r.Size()))
		k.Place(wants[i].r.Min)
	}
	for _, w := range wants {
		if got[w.c] {
			continue
		}
		k := kids.Build(w.c)
		k.Layout(gunim.Tight(w.r.Size()))
		k.Place(w.r.Min)
	}
	if !v.fitted && len(v.g.Nodes) > 0 && v.size.W > 0 {
		v.fitted = true
		v.FitAll(nil)
	}
	v.laid = true
	return v.size
}

// Step implements [gunim.Animator]: the camera, the boxes gliding, a
// box or a wire held near an edge scrolling the plane, and the dots
// running along the wires.
func (v *View) Step(dt time.Duration) bool {
	moving := v.Group.Step(dt)
	v.followWheel()
	glided := false
	for _, pl := range v.places {
		x := pl.x.Step(dt)
		if pl.y.Step(dt) || x {
			glided = true
		}
	}
	if glided {
		v.seq++
		moving = true
	}
	for _, w := range v.g.Wires {
		if w.Flow > 0.02 {
			// Round the dots' gap, so it never grows past what a float
			// keeps exactly.
			v.phase[w.ID] = float32(math.Mod(float64(v.phase[w.ID]+float32(dt.Seconds())*(24+90*w.Flow)), dotGap))
			moving = true
		}
	}
	return moving
}

// DragHeld implements [gunim.DragHolder]: a box or a wire held near
// an edge has the plane scroll toward it.
func (v *View) DragHeld() bool {
	switch v.pr.kind {
	case pressMove, pressLink, pressReplug, pressBand:
		return v.pr.moved
	case pressNone, pressPan:
	}
	return false
}

// EdgeScroll implements [gunim.EdgeScroller]: the plane scrolls toward
// an edge the pointer holding a box or a wire nears, as fast as
// gunim's EdgeSpeed says, as far as some of the boxes stay in view.
func (v *View) EdgeScroll(p geom.Point, dt time.Duration, _ *gunim.UI) geom.Point {
	s := geom.Pt(gunim.EdgeSpeed(p.X, 0, v.size.W), gunim.EdgeSpeed(p.Y, 0, v.size.H))
	if s == (geom.Point{}) {
		return geom.Point{}
	}
	was := geom.Pt(v.panX.Target(), v.panY.Target())
	v.panBy(s.Mul(-float32(dt.Seconds())))
	moved := geom.Pt(v.panX.Target(), v.panY.Target()).Sub(was)
	// The engine replays the pointer where the content was before it
	// moved, as a scroll view's children are drawn where they were. The
	// plane is drawn by the camera, which moved already: the replay
	// comes back that far.
	v.replay = moved
	return moved
}

// DragsTouch implements [gunim.TouchDragger]: a finger that presses a
// box or a port drags it, and one on empty room pans, as it scrolls.
func (v *View) DragsTouch() bool {
	switch v.pr.kind {
	case pressMove, pressLink, pressReplug, pressBand:
		return true
	case pressNone, pressPan:
	}
	return false
}

// follow brings what the press holds to view point p.
func (v *View) follow(p geom.Point) {
	pr := &v.pr
	switch pr.kind {
	case pressMove:
		d := v.ToPlane(p).Sub(pr.grab)
		for id, at := range pr.start {
			if pl := v.places[id]; pl != nil {
				pl.x.Jump(at.X + d.X)
				pl.y.Jump(at.Y + d.Y)
			}
		}
		v.seq++
	case pressLink:
		pr.to = v.ToPlane(p)
		pr.snap, pr.snapped = v.snapFor(pr.port, p)
		pr.why = ""
		if pr.snapped {
			from, to := ends(pr.port, pr.snap)
			if ok, why := v.accepts(from, to); !ok {
				pr.why = why
				if why == "" {
					pr.why = "Those ports cannot be joined."
				}
			}
		}
	case pressReplug:
		w := v.g.Wire(pr.wire)
		if w == nil {
			return
		}
		pr.to = v.ToPlane(p)
		pr.snap, pr.snapped = v.snapFor(portKey{w.From, true}, p)
		pr.why = ""
		if pr.snapped && pr.snap.End != w.To {
			if ok, why := v.accepts(w.From, pr.snap.End); !ok {
				pr.why = why
				if why == "" {
					pr.why = "Those ports cannot be joined."
				}
			}
		}
	case pressNone, pressPan, pressBand:
	}
}

// dist is how far apart a and b are.
func dist(a, b geom.Point) float32 {
	d := a.Sub(b)
	return float32(math.Hypot(float64(d.X), float64(d.Y)))
}

// segDist is how far p is from the segment from a to b.
func segDist(p, a, b geom.Point) float32 {
	ab := b.Sub(a)
	l2 := ab.X*ab.X + ab.Y*ab.Y
	t := float32(0)
	if l2 > 0 {
		t = min(max(((p.X-a.X)*ab.X+(p.Y-a.Y)*ab.Y)/l2, 0), 1)
	}
	return dist(p, a.Add(ab.Mul(t)))
}

// Focusable implements [gunim.Focusable]: a press gives the view the
// keys, for Delete, Escape and Home.
func (v *View) Focusable() bool { return true }

// ZoomsWithWheel implements [gunim.WheelZoomer]: Ctrl and the wheel
// zoom the plane, not the window.
func (v *View) ZoomsWithWheel() bool { return true }

// ZoomsWithPinch implements [gunim.PinchZoomer].
func (v *View) ZoomsWithPinch() bool { return true }

// Cursor implements [gunim.CursorShaper].
func (v *View) Cursor(p geom.Point) input.Cursor {
	switch v.pr.kind {
	case pressPan, pressMove:
		if v.pr.moved {
			return input.CursorMove
		}
	case pressLink, pressReplug:
		return input.CursorCrosshair
	case pressNone, pressBand:
	}
	t := v.hover
	if p != v.pointer {
		t = v.targetAt(p)
	}
	switch {
	case t.isPort():
		return input.CursorCrosshair
	case t.node != "":
		return input.CursorMove
	case t.wire != "":
		return input.CursorHand
	}
	return input.CursorArrow
}

// Handle implements [gunim.Handler].
func (v *View) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if v.replay != (geom.Point{}) {
			e.Pos, v.replay = e.Pos.Add(v.replay), geom.Point{}
		}
		v.pointer = e.Pos
		v.drag(e.Pos)
		if v.pr.kind == pressNone {
			v.hover = v.targetAt(e.Pos)
		}
	case input.PointerLeave:
		v.hover = target{}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			if v.pr.kind != pressNone {
				// A second button lets go of what is held.
				v.cancel(u)
				return true
			}
			return false
		}
		u.Focus(v)
		v.down(e, u)
	case input.PointerUp:
		if v.pr.kind == pressNone {
			return false
		}
		v.up(e.Pos, u)
	case input.Scroll:
		v.scroll(e, u.Theme())
	case input.Pinch:
		k := e.Scale
		if k <= 0 {
			k = 1
		}
		v.zoomAbout(e.Pos.Sub(e.Delta), k)
		v.panBy(e.Delta)
	case input.KeyPress:
		if !v.key(e, u) {
			return false
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// key does what a key does: Delete takes what is picked out, Escape
// lets go of what is held or picked, and Home fits every box in view.
func (v *View) key(k input.KeyPress, u *gunim.UI) bool {
	switch k.Key {
	case input.KeyDelete, input.KeyBackspace:
		nodes, wire := v.Picked()
		if len(nodes) == 0 && wire == "" {
			return false
		}
		var wires []string
		if wire != "" {
			wires = []string{wire}
		}
		u.Cue(gunim.CueClose, v)
		v.change(Delete{Nodes: nodes, Wires: wires}, u)
	case input.KeyEscape:
		if v.pr.kind != pressNone {
			v.cancel(u)
			return true
		}
		if len(v.picked) == 0 && v.wire == "" {
			return false
		}
		v.SetPicked(nil, "", u)
		v.selectChange(u)
	case input.KeyHome:
		v.FitAll(u)
	default:
		return false
	}
	return true
}

// cancel lets go of what the press holds: boxes moved go back.
func (v *View) cancel(u *gunim.UI) {
	if v.pr.kind == pressMove {
		for id, at := range v.pr.start {
			if pl := v.places[id]; pl != nil {
				pl.x.Animate(at.X, Place.Get(u.Theme()))
				pl.y.Animate(at.Y, Place.Get(u.Theme()))
			}
		}
	}
	v.pr = press{}
	u.Invalidate()
}

// scroll moves the plane by the wheel, or zooms it with Ctrl.
func (v *View) scroll(e input.Scroll, th *theme.Live) {
	// A wheel turns in whole notches; a touchpad's fractions move the
	// plane as smoothly as the fingers go.
	wheel := e.Notches != (geom.Point{}) && e.Notches.X == float32(math.Round(float64(e.Notches.X))) &&
		e.Notches.Y == float32(math.Round(float64(e.Notches.Y)))
	if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModSuper) {
		if !wheel {
			// A touchpad zooms as smoothly as it moves.
			v.zoomAbout(e.Pos, float32(math.Exp(float64(e.Delta.Y)/240)))
			return
		}
		v.wheelZoom(e.Pos, float32(math.Pow(1.15, float64(e.Notches.Y))), th)
		return
	}
	d := e.Delta
	if wheel {
		d = e.Notches.Mul(60)
	}
	if e.Mods.Has(input.ModShift) && d.X == 0 {
		d = geom.Pt(d.Y, 0)
	}
	if !wheel {
		v.panBy(d)
		return
	}
	v.wheelPan(d, th)
}

// down starts what a press at e does: a port draws a wire, or an
// input a wire joins carries the wire's end; a box is held; a wire is
// picked; empty room pans, or with Shift draws a band.
func (v *View) down(e input.PointerDown, u *gunim.UI) {
	v.pointer = e.Pos
	pr := press{from: e.Pos, clicks: e.Clicks, shift: e.Mods.Has(input.ModShift)}
	t := v.targetAt(e.Pos)
	switch {
	case t.isPort():
		if !t.port.out {
			if i := slices.IndexFunc(v.g.Wires, func(w Wire) bool { return w.To == t.port.End }); i >= 0 {
				pr.kind, pr.wire, pr.to = pressReplug, v.g.Wires[i].ID, v.ToPlane(e.Pos)
				break
			}
		}
		pr.kind, pr.port, pr.to = pressLink, t.port, v.ToPlane(e.Pos)
	case t.node != "":
		if e.Clicks == 2 {
			u.Cue(gunim.CuePress, v)
			v.change(Open{Node: t.node}, u)
			return
		}
		pr.kind, pr.node, pr.grab = pressMove, t.node, v.ToPlane(e.Pos)
		pr.start = map[string]geom.Point{}
		group := []string{t.node}
		if v.picked[t.node] {
			group, _ = v.Picked()
		}
		for _, id := range group {
			if pl := v.places[id]; pl != nil {
				pr.start[id] = geom.Pt(pl.x.Value(), pl.y.Value())
			}
		}
		v.raise(t.node)
	case t.wire != "":
		v.SetPicked(nil, t.wire, u)
		u.Cue(gunim.CueSelect, v)
		v.selectChange(u)
		return
	case pr.shift:
		pr.kind, pr.was = pressBand, maps.Clone(v.picked)
	default:
		pr.kind, pr.pan = pressPan, v.panTarget()
		v.wheel.on = false
	}
	v.pr = pr
}

// drag follows the press under way to view point p.
func (v *View) drag(p geom.Point) {
	pr := &v.pr
	if pr.kind == pressNone {
		return
	}
	if !pr.moved {
		if dist(p, pr.from) < slop {
			return
		}
		pr.moved = true
	}
	switch pr.kind {
	case pressPan:
		pan := v.keepInView(pr.pan.Add(p.Sub(pr.from)), v.zoom.Target())
		v.panX.Jump(pan.X)
		v.panY.Jump(pan.Y)
	case pressBand:
		band := geom.Rect{Min: v.ToPlane(pr.from), Max: v.ToPlane(p)}.Normalized()
		v.picked = maps.Clone(pr.was)
		if v.picked == nil {
			v.picked = map[string]bool{}
		}
		for i := range v.g.Nodes {
			if r := v.rectOf(&v.g.Nodes[i]); band.Contains(r.Min) && band.Contains(r.Max) {
				v.picked[v.g.Nodes[i].ID] = true
			}
		}
		v.wire = ""
	case pressMove, pressLink, pressReplug:
		v.follow(p)
	case pressNone:
	}
}

// up ends the press under way at view point p.
func (v *View) up(p geom.Point, u *gunim.UI) {
	pr := v.pr
	v.pr = press{}
	switch pr.kind {
	case pressMove:
		if !pr.moved {
			// A click picks the box, or with Shift adds it.
			switch {
			case pr.shift && v.picked[pr.node]:
				delete(v.picked, pr.node)
			case pr.shift:
				v.picked[pr.node] = true
				v.wire = ""
			default:
				v.SetPicked([]string{pr.node}, "", u)
			}
			u.Cue(gunim.CueSelect, v)
			v.selectChange(u)
			break
		}
		to := map[string]geom.Point{}
		for id := range pr.start {
			if pl := v.places[id]; pl != nil {
				to[id] = geom.Pt(pl.x.Value(), pl.y.Value())
				if n := v.g.Node(id); n != nil {
					n.At = to[id]
				}
			}
		}
		v.change(Move{To: to}, u)
	case pressLink:
		if !pr.snapped {
			if id := v.nodeAt(p); id != "" && id != pr.port.Node {
				v.change(LinkOnNode{From: pr.port.End, Out: pr.port.out, Node: id, At: v.ToPlane(p)}, u)
			}
			break
		}
		from, to := ends(pr.port, pr.snap)
		if pr.why != "" {
			v.change(Refused{From: from, To: to, Why: pr.why}, u)
			break
		}
		u.Cue(gunim.CueTick, v)
		v.change(Connect{From: from, To: to}, u)
	case pressReplug:
		w := v.g.Wire(pr.wire)
		switch {
		case w == nil || !pr.moved:
			if w != nil {
				v.SetPicked(nil, w.ID, u)
				v.selectChange(u)
			}
		case !pr.snapped:
			// Pulled out and let go on nothing: out.
			u.Cue(gunim.CueClose, v)
			v.change(Delete{Wires: []string{w.ID}}, u)
		case pr.snap.End == w.To:
		case pr.why != "":
			v.change(Refused{From: w.From, To: pr.snap.End, Why: pr.why}, u)
		default:
			u.Cue(gunim.CueTick, v)
			v.change(Replug{Wire: w.ID, From: w.From, To: pr.snap.End}, u)
		}
	case pressBand:
		if pr.moved {
			v.selectChange(u)
		}
	case pressPan:
		if !pr.moved && (len(v.picked) > 0 || v.wire != "") {
			v.SetPicked(nil, "", u)
			v.selectChange(u)
		}
	case pressNone:
	}
}

// Access implements [gunim.Accessible]: a list of the boxes, the ones
// picked selected.
func (v *View) Access() access.Info {
	info := access.Info{Role: access.RoleList, Name: "Graph"}
	for i, id := range v.order {
		n := v.g.Node(id)
		if n == nil {
			continue
		}
		part := access.Info{Role: access.RoleListItem, Name: n.Title, Actions: []string{access.ActionPress}}
		if v.picked[id] {
			part.State = access.StateSelected
			info.Active = i + 1
		}
		r := v.rectOf(n)
		part.Bounds = geom.Rect{Min: v.ToView(r.Min), Max: v.ToView(r.Max)}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a box picks it.
func (v *View) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(v.order) {
		return false
	}
	v.SetPicked([]string{v.order[r.Part]}, "", u)
	v.selectChange(u)
	return true
}
