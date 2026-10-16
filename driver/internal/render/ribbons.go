//go:build linux || windows || darwin

package render

import (
	"math"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// A ribbon draws in the batch with everything else, through the draw
// program, as one quad for each stretch between two of its points. The
// quads are cut on the CPU, in device pixels: each point has two
// corners, one either side of it, along the line halfway between the
// ways the stretches before and after it run. Each corner carries how
// far across the ribbon it lies and the ribbon's half width there, and
// the shader works out from them how much of each pixel the ribbon
// covers. So the edges stay smooth, and a ribbon thinner than a pixel
// fades out evenly, with no mask drawn on the CPU.
//
// Where the ribbon bends more sharply than its width allows, the quads
// either side of a bend overlap on its inside, and a see-through ribbon
// is darker there.

// ribbonFeather is how far each quad reaches past the ribbon's edge, in
// device pixels, for the edge's smoothing: the half pixel it needs, and
// a little more.
const ribbonFeather = 1

// ribbonCorner is a ribbon's point made ready for its quads: where it
// lies, in device pixels; the way to its corners, as far as they lie
// from it; how far across the ribbon they are; its half width; and its
// colour, premultiplied.
type ribbonCorner struct {
	at, out      geom.Point
	across, half float32
	color        [4]float32
}

// ribbon queues a ribbon's quads.
func (r *Renderer) ribbon(op *paint.RibbonOp) {
	pts := op.Points
	if len(pts) < 2 {
		return
	}
	t := op.Transform
	// A width is in the op's own space; scaled one way more than the
	// other, it takes the mean of the two.
	widthScale := float32(math.Sqrt(math.Abs(float64(t.A*t.E-t.B*t.D)))) * r.scale
	cs := r.ribbonCorners[:0]
	for _, q := range pts {
		c := rgba(q.Color)
		cs = append(cs, ribbonCorner{
			at:    t.Apply(q.At).Mul(r.scale),
			half:  max(q.Width, 0) / 2 * widthScale,
			color: [4]float32{c[0] * c[3], c[1] * c[3], c[2] * c[3], c[3]},
		})
	}
	r.ribbonCorners = cs
	if !ribbonNormals(cs) {
		return
	}
	add := op.Blend == paint.BlendAdd
	for i := range len(cs) - 1 {
		a, b := &cs[i], &cs[i+1]
		if a.color[3] == 0 && b.color[3] == 0 || a.half == 0 && b.half == 0 {
			continue
		}
		quad := [4]geom.Point{a.at.Add(a.out), a.at.Sub(a.out), b.at.Sub(b.out), b.at.Add(b.out)}
		if !r.cull.Empty() {
			lo, hi := quad[0], quad[0]
			for _, p := range quad[1:] {
				lo = geom.Pt(min(lo.X, p.X), min(lo.Y, p.Y))
				hi = geom.Pt(max(hi.X, p.X), max(hi.Y, p.Y))
			}
			if r.outside(geom.Rect{Min: lo, Max: hi}) {
				continue
			}
		}
		r.uses(0)
		r.ribbonVert(quad[0], a, 1, add)
		r.ribbonVert(quad[1], a, -1, add)
		r.ribbonVert(quad[2], b, -1, add)
		r.ribbonVert(quad[3], b, 1, add)
	}
}

// ribbonNormals sets each corner's way out and how far across it lies,
// and reports false where the points all lie in one place, which draws
// nothing. A stretch of no length takes the way of the one before it,
// or of the first one with a length.
func ribbonNormals(cs []ribbonCorner) bool {
	// Each point's out holds, for now, the normal of the stretch from
	// it to the next one.
	var last geom.Point
	found := false
	for i := range len(cs) - 1 {
		d := cs[i+1].at.Sub(cs[i].at)
		if l := float32(math.Hypot(float64(d.X), float64(d.Y))); l > 1e-6 {
			n := geom.Pt(-d.Y/l, d.X/l)
			if !found {
				// The stretches of no length at the start take this one's.
				for j := range i {
					cs[j].out = n
				}
			}
			last, found = n, true
		}
		cs[i].out = last
	}
	if !found {
		return false
	}
	prev := cs[0].out
	for i := range cs {
		n := prev
		if i < len(cs)-1 {
			n = cs[i].out
		}
		// The corners lie along the line halfway between the two
		// stretches' normals, far enough out that each stretch's edge
		// lies the same way from its middle line: the miter.
		m := prev.Add(n)
		ml := float32(math.Hypot(float64(m.X), float64(m.Y)))
		if ml < 1e-3 {
			// The ribbon turns right back on itself.
			m, ml = n, 1
		}
		m = m.Mul(1 / ml)
		dot := m.X*n.X + m.Y*n.Y
		reach, across := 1/dot, float32(1)
		if reach > paint.RibbonMiter {
			// Too sharp a bend: cut short, and so thinner across.
			reach, across = paint.RibbonMiter, paint.RibbonMiter*dot
		}
		c := &cs[i]
		w := c.half + ribbonFeather
		c.out = m.Mul(w * reach)
		c.across = w * across
		prev = n
	}
	return true
}

// ribbonVert queues one corner of a ribbon's quad, at p in device
// pixels, on the side of c that side gives, 1 or -1.
func (r *Renderer) ribbonVert(p geom.Point, c *ribbonCorner, side float32, add bool) {
	kind := float32(kindRibbon)
	if add {
		kind += kindAdd
	}
	clip := [4]float32{0, 0, 0, 1}
	if e := r.ellipse; e.on {
		clip = [4]float32{(p.X - e.centre.X) / e.rad.X, (p.Y - e.centre.Y) / e.rad.Y, 1, 1}
	}
	col := c.color
	r.verts = append(r.verts,
		p.X*2/float32(r.fbW)-1, 1-p.Y*2/float32(r.fbH),
		side*c.across, c.half,
		0, 0, 0, 0,
		0, 0, kind, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		col[0], col[1], col[2], col[3],
		0, 0, 0, 0,
		clip[0], clip[1], clip[2], clip[3],
	)
}
