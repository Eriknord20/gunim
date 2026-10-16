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
// The ends are smoothed as the edges are, a pixel's width apart: a short
// quad at each end fades the colour in, from nothing half a pixel
// outside the end to the whole of it half a pixel inside, the share of
// each pixel the cut end covers. It shares its corners with the quad
// next to it, so no seam shows.
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
	// The capped corners go after the points' own, in the same buffer.
	all := capRibbon(cs, cs)
	r.ribbonCorners = all[:0]
	cs = all[len(cs):]
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

// capRibbon appends to dst the corners of cs with a fading quad at each
// end, and returns the longer dst: each end moves half a pixel in along
// the ribbon, or halfway along a shorter one, the corners it passes
// left out, and a corner of no colour a pixel further out ends it.
func capRibbon(dst, cs []ribbonCorner) []ribbonCorner {
	n := len(cs)
	var length float32
	for i := range n - 1 {
		length += dist(cs[i].at, cs[i+1].at)
	}
	in := min(0.5, length/2)
	start, first, startOut := capEnd(cs, in, 1)
	end, last, endOut := capEnd(cs, in, -1)
	dst = append(dst, startOut, start)
	if first <= last {
		dst = append(dst, cs[first:last+1]...)
	}
	return append(dst, end, endOut)
}

// capEnd returns the point at distance in along cs from its first
// corner, for way 1, or from its last, for way -1; the index of the next
// corner on from there; and the corner of no colour a pixel back out
// from the point.
func capEnd(cs []ribbonCorner, in float32, way int) (at ribbonCorner, next int, beyond ribbonCorner) {
	i, j := 0, 1
	if way < 0 {
		i, j = len(cs)-1, len(cs)-2
	}
	for ; j >= 0 && j < len(cs); i, j = j, j+way {
		l := dist(cs[i].at, cs[j].at)
		if l < in || l == 0 {
			in -= l
			continue
		}
		c, d := &cs[i], &cs[j]
		t := in / l
		lerp := func(a, b float32) float32 { return a + (b-a)*t }
		at = *c
		at.at = geom.Pt(lerp(c.at.X, d.at.X), lerp(c.at.Y, d.at.Y))
		at.out = geom.Pt(lerp(c.out.X, d.out.X), lerp(c.out.Y, d.out.Y))
		at.across, at.half = lerp(c.across, d.across), lerp(c.half, d.half)
		for k := range at.color {
			at.color[k] = lerp(c.color[k], d.color[k])
		}
		beyond = at
		beyond.at = at.at.Add(c.at.Sub(d.at).Mul(1 / l))
		beyond.color = [4]float32{}
		return at, j, beyond
	}
	// Rounding left in a hair past the ribbon's length: its far end.
	at = cs[i]
	return at, i + way, at
}

// dist returns how far apart a and b lie.
func dist(a, b geom.Point) float32 {
	d := b.Sub(a)
	return float32(math.Hypot(float64(d.X), float64(d.Y)))
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
