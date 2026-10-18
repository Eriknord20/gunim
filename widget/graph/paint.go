package graph

import (
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// wireShape is the way a wire takes on the plane: a curve from a to b,
// pulled by c1 and c2.
type wireShape struct{ a, c1, c2, b geom.Point }

// points are points along s, fine enough to draw it smooth.
func (s wireShape) points() []geom.Point {
	const n = 36
	out := make([]geom.Point, n+1)
	for i := range n + 1 {
		t := float32(i) / n
		u := 1 - t
		out[i] = s.a.Mul(u * u * u).Add(s.c1.Mul(3 * u * u * t)).Add(s.c2.Mul(3 * u * t * t)).Add(s.b.Mul(t * t * t))
	}
	return out
}

// bounds is the room s stays within.
func (s wireShape) bounds() geom.Rect {
	r := geom.Rect{Min: s.a, Max: s.a}
	for _, p := range []geom.Point{s.b, s.c1, s.c2} {
		r.Min = geom.Pt(min(r.Min.X, p.X), min(r.Min.Y, p.Y))
		r.Max = geom.Pt(max(r.Max.X, p.X), max(r.Max.Y, p.Y))
	}
	return r
}

// curve is the shape of a wire from an output at a to an input at b:
// it leaves to the right and comes in from the left, looping round
// where b stands left of a.
func curve(a, b geom.Point) wireShape {
	dx := max(abs32(b.X-a.X)*0.5, 50)
	if b.X < a.X+40 {
		dx = 90 + abs32(b.Y-a.Y)*0.2 + (a.X-b.X)*0.25
	}
	return wireShape{a: a, c1: a.Add(geom.Pt(dx, 0)), c2: b.Sub(geom.Pt(dx, 0)), b: b}
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

// shapeOf is the shape wire w takes now, and false where an end is
// missing.
func (v *View) shapeOf(w Wire) (wireShape, bool) {
	sh, _, ok := v.wirePts(w)
	return sh, ok
}

// wirePts is the shape wire w takes now, and points along it, worked
// out once for each change to where the boxes stand; false where an
// end is missing.
func (v *View) wirePts(w Wire) (wireShape, []geom.Point, bool) {
	if c, ok := v.shapes[w.ID]; ok && c.seq == v.seq {
		return c.sh, c.pts, c.pts != nil
	}
	a, okA := v.endAt(portKey{w.From, true})
	b, okB := v.endAt(portKey{w.To, false})
	if !okA || !okB {
		v.shapes[w.ID] = wirePoints{seq: v.seq}
		return wireShape{}, nil, false
	}
	sh := curve(a, b)
	pts := sh.points()
	v.shapes[w.ID] = wirePoints{seq: v.seq, sh: sh, pts: pts}
	return sh, pts, true
}

// lengths are the distances along pts to each point.
func lengths(pts []geom.Point) []float32 {
	out := make([]float32, len(pts))
	for i := 1; i < len(pts); i++ {
		out[i] = out[i-1] + dist(pts[i], pts[i-1])
	}
	return out
}

// along is the point distance d along pts.
func along(pts []geom.Point, lens []float32, d float32) geom.Point {
	for i := 1; i < len(pts); i++ {
		if d <= lens[i] || i == len(pts)-1 {
			seg := lens[i] - lens[i-1]
			t := float32(0)
			if seg > 0 {
				t = min(max((d-lens[i-1])/seg, 0), 1)
			}
			return pts[i-1].Add(pts[i].Sub(pts[i-1]).Mul(t))
		}
	}
	return pts[len(pts)-1]
}

// faded is c at a share a of its alpha.
func faded(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(float32(c.A)*min(max(a, 0), 1) + 0.5)
	return c
}

// mix is a share t of the way from a to b.
func mix(a, b color.NRGBA, t float32) color.NRGBA { return anim.Mix(anim.ColorCodec, a, b, t) }

// runCache keeps text shaped, by its words, size and weight, so the
// boxes' names are shaped once.
type runCache struct {
	runs map[runKey]text.Run
	face *text.Face
	bold *text.Face
}

type runKey struct {
	s    string
	size float32
	bold bool
}

// shape returns s shaped at size, in the theme's font or its bold.
func (c *runCache) shape(th *theme.Live, s string, size float32, bold bool) text.Run {
	face, boldFace := widget.Font.Get(th), widget.BoldFont.Get(th)
	if c.runs == nil || c.face != face || c.bold != boldFace || len(c.runs) > 4000 {
		c.runs, c.face, c.bold = map[runKey]text.Run{}, face, boldFace
	}
	k := runKey{s, size, bold}
	if r, ok := c.runs[k]; ok {
		return r
	}
	f := face
	if bold {
		f = boldFace
	}
	r := f.Shape(s, size)
	c.runs[k] = r
	return r
}

// Paint implements [gunim.Node]: the grid; the wires, glowing, with
// dots running along them; the boxes over them with their ports and
// what they hold; the wire held; and the band.
func (v *View) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	whole := geom.Rect{Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: whole, Clip: true, Opacity: 1})()
	v.paintGrid(p, th, box)
	contents := map[string]gunim.Child{}
	for k := range kids.All {
		if id, ok := v.boxOf[k.Node()]; ok && k.Presence() != gunim.Exiting {
			contents[id] = k
		}
	}
	z, pan := v.cam()
	defer p.Push(paint.Translate(pan))()
	defer p.Push(paint.Scale(z, geom.Point{}))()
	view := geom.Rect{Min: v.ToPlane(geom.Point{}), Max: v.ToPlane(box.Point())}.Inset(geom.Uniform(-40))
	ls := max(1, 0.7/z)
	for _, w := range v.g.Wires {
		if v.pr.kind == pressReplug && v.pr.moved && v.pr.wire == w.ID {
			// Its end held, it is drawn where that is.
			continue
		}
		sh, pts, ok := v.wirePts(w)
		if !ok || !meets(sh.bounds(), view) {
			continue
		}
		v.paintWire(p, th, w, pts, ls)
	}
	for _, id := range v.order {
		n := v.node(id)
		if n == nil || !meets(v.rectOf(n), view) {
			continue
		}
		var c *gunim.Child
		if k, ok := contents[id]; ok {
			c = &k
		}
		v.paintNode(p, th, n, c, z)
	}
	v.paintHeld(p, th, ls)
	if v.pr.kind == pressBand && v.pr.moved {
		band := geom.Rect{Min: v.ToPlane(v.pr.from), Max: v.ToPlane(v.pointer)}.Normalized()
		p.RRect(band, 4*ls, paint.Solid(BandFill.Get(th)))
		p.RRectStroke(band, 4*ls, paint.Fill{}, paint.Stroke{Width: 1.2 * ls, Color: faded(widget.Accent.Get(th), 0.8)})
	}
}

// meets reports whether a and b share any room.
func meets(a, b geom.Rect) bool {
	return a.Min.X < b.Max.X && b.Min.X < a.Max.X && a.Min.Y < b.Max.Y && b.Min.Y < a.Max.Y
}

// paintGrid draws the plane's grid of dots, in the view's space, at
// least 24 pixels apart however far out it is zoomed.
func (v *View) paintGrid(p *paint.Painter, th *theme.Live, box geom.Size) {
	z, pan := v.cam()
	step := GridStep.Get(th) * z
	if step <= 0 {
		return
	}
	for step < 24 {
		step *= 2
	}
	x0 := float32(math.Mod(float64(pan.X), float64(step)))
	y0 := float32(math.Mod(float64(pan.Y), float64(step)))
	if x0 < 0 {
		x0 += step
	}
	if y0 < 0 {
		y0 += step
	}
	c := GridInk.Get(th)
	for y := y0; y < box.H; y += step {
		for x := x0; x < box.W; x += step {
			p.RRect(geom.Rc(x-1, y-1, 2, 2), 1, paint.Solid(c))
		}
	}
}

// wireColor is wire w's colour: its own, or its output's, or its
// output's box's, or the theme's.
func (v *View) wireColor(th *theme.Live, w Wire) color.NRGBA {
	if w.Color.A > 0 {
		return w.Color
	}
	return v.portColor(th, portKey{w.From, true})
}

// portColor is port k's colour: its own, or its box's, or the theme's.
func (v *View) portColor(th *theme.Live, k portKey) color.NRGBA {
	if pt, ok := v.g.Port(k.End, k.out); ok && pt.Color.A > 0 {
		return pt.Color
	}
	if n := v.g.Node(k.Node); n != nil && n.Color.A > 0 {
		return n.Color
	}
	return PortInk.Get(th)
}

// paintWire draws wire w along sh: a glow, its core, solid or in
// dashes, and the dots its Flow runs along it.
func (v *View) paintWire(p *paint.Painter, th *theme.Live, w Wire, pts []geom.Point, ls float32) {
	c := v.wireColor(th, w)
	lit := float32(0)
	switch {
	case v.wire == w.ID:
		lit = 1
	case v.hover.wire == w.ID:
		lit = 0.5
	}
	on := float32(1)
	if w.Dim {
		on = 0.35
	}
	core, glow := WireWidth.Get(th)*ls, WireGlow.Get(th)*ls
	if w.Thin {
		k := ThinShare.Get(th)
		core, glow = core*k, glow*k
	}
	core += 1.2 * lit * ls
	ribbon(p, pts, glow, faded(c, (0.07+0.22*w.Flow+0.18*lit)*on))
	coreC := faded(mix(c, TitleInk.Get(th), 0.15+0.4*lit), (0.6+0.3*w.Flow+0.1*lit)*on)
	if w.Dashed {
		for i := 0; i+2 < len(pts); i += 4 {
			ribbon(p, pts[i:i+3], core, coreC)
		}
	} else {
		ribbon(p, pts, core, coreC)
	}
	lens := lengths(pts)
	total := lens[len(lens)-1]
	if w.Flow > 0.02 && total > 1 && !w.Dim {
		gap := dotGap * ls
		dot := mix(c, TitleInk.Get(th), 0.55)
		for d := float32(math.Mod(float64(v.phase[w.ID]*ls), float64(gap))); d < total; d += gap {
			at := along(pts, lens, d)
			// The dots fade in and out at the ends.
			a := min(min(d, total-d)/(12*ls), 1) * (0.2 + 0.8*w.Flow)
			r := (1.6 + 1.4*w.Flow) * ls
			p.RRect(geom.Rc(at.X-r, at.Y-r, 2*r, 2*r), r, paint.Solid(faded(dot, a)))
		}
	}
}

// ribbon draws a band width wide, c, through pts.
func ribbon(p *paint.Painter, pts []geom.Point, width float32, c color.NRGBA) {
	rp := make([]paint.RibbonPoint, len(pts))
	for i, pt := range pts {
		rp[i] = paint.RibbonPoint{At: pt, Width: width, Color: c}
	}
	p.Ribbon(rp)
}

// paintNode draws box n: its shadow and glow, its card, its head, what
// it holds, and its ports with their names, which go zoomed far out.
func (v *View) paintNode(p *paint.Painter, th *theme.Live, n *Node, content *gunim.Child, z float32) {
	r := v.rectOf(n)
	if n.Dim {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-20)), Opacity: 0.45})()
	}
	radius := NodeRadius.Get(th)
	c := n.Color
	if c.A == 0 {
		c = PortInk.Get(th)
	}
	if n.Glow > 0.01 {
		p.ShadowRRect(r, radius, paint.Fill{}, paint.Shadow{Blur: 18 + 18*n.Glow, Color: faded(c, 0.55*n.Glow)})
	}
	p.ShadowRRect(r, radius, paint.Solid(NodeFill.Get(th)), paint.Shadow{Blur: 14, Offset: geom.Pt(0, 4), Color: NodeShadow.Get(th)})
	// The head: a bar of the box's colour across its top.
	headH := v.m.head
	p.RRect(geom.Rc(r.Min.X+radius, r.Min.Y+1, r.Size().W-2*radius, 3), 1.5, paint.Solid(faded(c, 0.9)))
	edge := NodeEdge.Get(th)
	switch {
	case v.picked[n.ID]:
		edge = widget.Accent.Get(th)
	case v.hover.node == n.ID:
		edge = mix(edge, TitleInk.Get(th), 0.18)
	}
	width := float32(1)
	if v.picked[n.ID] {
		width = 2
	}
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: width, Color: edge})
	labels := z >= labelsFrom
	pad := float32(12)
	title := v.runs.shape(th, n.Title, TitleSize.Get(th), true)
	room := r.Size().W - 2*pad
	ty := r.Min.Y + 9
	if n.Sub == "" || !labels {
		ty = r.Min.Y + (headH-title.Height())/2 + 1
	}
	title.PaintFaded(p, geom.Pt(r.Min.X+pad, ty), TitleInk.Get(th), room, widget.TextFade.Get(th))
	if n.Sub != "" && labels {
		sub := v.runs.shape(th, n.Sub, LabelSize.Get(th), false)
		sub.PaintFaded(p, geom.Pt(r.Min.X+pad, ty+title.Height()), SubInk.Get(th), room, widget.TextFade.Get(th))
	}
	if content != nil {
		func() {
			body := v.bodyOf(n)
			defer p.Layer(paint.LayerOpts{Bounds: body, Clip: true, Opacity: 1})()
			content.Paint(p)
		}()
	}
	v.paintPorts(p, th, n, labels)
}

// paintPorts draws box n's ports, each filled where a wire joins it,
// with its name inside the box beside it. While a wire is held, the
// ports it could join glow and the rest dim.
func (v *View) paintPorts(p *paint.Painter, th *theme.Live, n *Node, labels bool) {
	held, holding := v.heldFrom()
	half := v.sizeOf(n).W/2 - 14
	for _, out := range []bool{false, true} {
		ps := n.Ins
		if out {
			ps = n.Outs
		}
		for i, port := range ps {
			k := portKey{End{n.ID, port.ID}, out}
			at := v.portAt(n, out, i)
			c := v.portColor(th, k)
			lit, dim := float32(0), float32(1)
			if v.hover.port == k {
				lit = 0.6
			}
			if holding {
				switch {
				case k == held:
					lit = 1
				case v.joinable(held, k):
					lit = 0.45
					if v.pr.snapped && v.pr.snap == k {
						lit = 1
						if v.pr.why != "" {
							c = RefuseInk.Get(th)
						}
					}
				default:
					dim = 0.35
				}
			}
			if lit > 0 {
				p.ShadowRRect(geom.Rc(at.X-6, at.Y-6, 12, 12), 6, paint.Solid(faded(c, 0.25*lit)), paint.Shadow{Blur: 12 * lit, Color: faded(c, 0.8*lit)})
			}
			paintGlyph(p, th, at, port.Shape, faded(c, dim), v.joined[k], lit)
			if !labels || port.Name == "" {
				continue
			}
			name := v.runs.shape(th, port.Name, LabelSize.Get(th), false)
			ink := faded(mix(LabelInk.Get(th), TitleInk.Get(th), lit), dim)
			room := half
			if len(n.Outs) == 0 || len(n.Ins) == 0 {
				room = 2 * half
			}
			x := at.X + 10
			if out {
				x = at.X - 10 - min(name.Advance, room)
			}
			name.PaintFaded(p, geom.Pt(x, at.Y-name.Height()/2), ink, room, widget.TextFade.Get(th))
		}
	}
}

// heldFrom is the port the wire held runs from, the end that stays,
// and whether a wire is held.
func (v *View) heldFrom() (portKey, bool) {
	switch v.pr.kind {
	case pressLink:
		return v.pr.port, v.pr.moved
	case pressReplug:
		if w := v.g.Wire(v.pr.wire); w != nil {
			return portKey{w.From, true}, v.pr.moved
		}
	case pressNone, pressPan, pressMove, pressBand:
	}
	return portKey{}, false
}

// paintGlyph draws a port at at: a ring, a diamond or a bolt, filled
// where a wire joins it.
func paintGlyph(p *paint.Painter, th *theme.Live, at geom.Point, s Shape, c color.NRGBA, joined bool, lit float32) {
	r := 4.5 + 1.5*lit
	fill := PortHole.Get(th)
	if joined {
		fill = mix(c, TitleInk.Get(th), 0.15)
	}
	fill.A = c.A
	switch s {
	case Diamond:
		end := p.Push(paint.Rotate(math.Pi/4, at))
		box := geom.Rc(at.X-r+0.5, at.Y-r+0.5, 2*r-1, 2*r-1)
		p.RRect(box, 1, paint.Solid(fill))
		p.RRectStroke(box, 1, paint.Fill{}, paint.Stroke{Width: 1.6, Color: c})
		end()
	case Bolt:
		pts := []geom.Point{at.Add(geom.Pt(1.5, -r-1)), at.Add(geom.Pt(-2.5, 0.5)), at.Add(geom.Pt(1, 0.5)), at.Add(geom.Pt(-1.5, r+1))}
		ribbon(p, pts, 2.2, c)
		if joined {
			ribbon(p, pts, 1, fill)
		}
	default:
		box := geom.Rc(at.X-r, at.Y-r, 2*r, 2*r)
		p.RRect(box, r, paint.Solid(fill))
		p.RRectStroke(box, r, paint.Fill{}, paint.Stroke{Width: 1.8, Color: c})
	}
}

// paintHeld draws the wire a press holds: from its port to the port it
// would join, or to the pointer, in RefuseInk where Accepts says no.
func (v *View) paintHeld(p *paint.Painter, th *theme.Live, ls float32) {
	from, holding := v.heldFrom()
	if !holding {
		return
	}
	a, ok := v.endAt(from)
	if !ok {
		return
	}
	b := v.pr.to
	if v.pr.snapped {
		if at, ok := v.endAt(v.pr.snap); ok {
			b = at
		}
	}
	c := v.portColor(th, from)
	if v.pr.kind == pressReplug {
		if w := v.g.Wire(v.pr.wire); w != nil {
			c = v.wireColor(th, *w)
		}
	}
	if v.pr.snapped && v.pr.why != "" {
		c = RefuseInk.Get(th)
	}
	sh := curve(a, b)
	if !from.out {
		sh = curve(b, a)
	}
	pts := sh.points()
	ribbon(p, pts, 7*ls, faded(c, 0.15))
	if v.pr.snapped {
		ribbon(p, pts, 2.2*ls, faded(c, 0.9))
	} else {
		for i := 0; i+2 < len(pts); i += 4 {
			ribbon(p, pts[i:i+3], 2.2*ls, faded(c, 0.9))
		}
	}
	r := 5 * ls
	p.ShadowRRect(geom.Rc(b.X-r, b.Y-r, 2*r, 2*r), r, paint.Solid(c), paint.Shadow{Blur: 12, Color: faded(c, 0.7)})
}
