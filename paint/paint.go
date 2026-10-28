// Package paint records what a frame should look like.
//
// A [Painter] records. Nodes append commands to it during the paint
// pass and the driver replays the finished list on the GPU. Keeping the
// two apart lets painting run while the previous frame is still on the
// wire, and lets a node's paint code be tested on its own.
//
// Every shape here is one the GPU can draw from its signed distance
// field: a rounded rectangle covers panels, buttons, inputs, focus
// rings, dividers and shadows, at any scale, and stays a single shader
// as a widget animates to a fractional size.
package paint

import (
	"image/color"
	"math"
	"slices"

	"github.com/marrasen/gunim/geom"
)

// A Painter records the draw commands for one frame.
//
// The zero Painter is ready to use. The engine hands each frame's
// Painter to the tree and then to the driver, so a node should use it
// and let it go.
type Painter struct {
	ops []Op
	// bounds holds, for each op, the part of the window it can touch.
	// A layer's covers everything drawn inside it.
	bounds []geom.Rect
	// prev and prevBounds are the frame recorded before this one, and
	// hasPrev says there was one. blurs is set when a layer blurs, and
	// prevBlurs when one did in the frame before.
	prev       []Op
	prevBounds []geom.Rect
	hasPrev    bool
	blurs      bool
	prevBlurs  bool
	// open holds the index of each layer open, innermost last.
	open  []int
	stack []Transform
	cur   Transform
	// blend is the blend in force, and blends the ones Blend put aside,
	// innermost last. Only a zero Painter holds BlendInherit, which
	// blendFor takes as BlendNormal.
	blend  Blend
	blends []Blend
	// clip is the innermost clipping layer open, or nil, and proj the
	// projection of the tilted layers open, or nil.
	clip *Clip
	proj *Projection
	// tilts is set when a layer tilts, and prevTilts when one did in the
	// frame before.
	tilts, prevTilts bool
	// ready is false in a zero Painter, whose cur has never been set;
	// at reads it as the identity until then.
	ready bool
	// popFn is pop as a func value, made once, so Push allocates
	// nothing.
	popFn func()
	// unblendFn is unblend as a func value, made once, so Blend
	// allocates nothing.
	unblendFn func()
	// rrects, texts, masks, scenes and ribbons hold the ops themselves, in
	// blocks, so a frame allocates a block now and then, never an op at
	// a time. The blocks of the frame before this one are reused; the
	// driver is done with them by then. An op kept past that, in a
	// Recording, is a copy, never one of these.
	rrects, prevRRects   slab[RRectOp]
	texts, prevTexts     slab[TextOp]
	masks, prevMasks     slab[MaskOp]
	scenes, prevScenes   slab[SceneOp]
	ribbons, prevRibbons slab[RibbonOp]
	// items holds the items of every scene this frame records, back to
	// back, and prevItems those of the frame before, kept from frame to
	// frame as the blocks are. A scene of a thousand items drawn every
	// frame would otherwise copy them to a new slice every frame.
	items, prevItems []SceneItem
	// points holds the points of every ribbon this frame records, back
	// to back, and prevPoints those of the frame before, kept from frame
	// to frame as items are.
	points, prevPoints []RibbonPoint
	// floats holds painting put off until the rest of the frame is
	// done; see Float.
	floats []func(*Painter)
	// carried are the stretches of this frame copied from the frame
	// before with Again, and matched Damage's marks of the ops before.
	carried []carried
	matched []bool
}

// slab hands out ops from blocks it keeps from frame to frame.
type slab[T any] struct {
	blocks [][]T
	// n is how many of the current block are taken, and at which
	// block is current.
	at, n int
}

const slabBlock = 256

// take returns a zeroed T from the slab.
func (s *slab[T]) take() *T {
	if s.at < len(s.blocks) && s.n == slabBlock {
		s.at, s.n = s.at+1, 0
	}
	if s.at == len(s.blocks) {
		s.blocks = append(s.blocks, make([]T, slabBlock))
	}
	v := &s.blocks[s.at][s.n]
	s.n++
	return v
}

// reset makes every block available again. It zeroes what was taken,
// so take hands out zeroed Ts, and an op the next frame leaves untaken
// holds on to nothing: no mesh, item array, shape or glyphs.
func (s *slab[T]) reset() {
	for _, b := range s.blocks[:s.at] {
		clear(b)
	}
	if s.at < len(s.blocks) {
		clear(s.blocks[s.at][:s.n])
	}
	s.at, s.n = 0, 0
}

// Everything is the damage that covers the whole window.
var Everything = geom.Rect{Min: geom.Pt(-1e9, -1e9), Max: geom.Pt(1e9, 1e9)}

// Reset starts a new frame. The frame recorded so far becomes the one
// [Painter.Damage] compares against, and the buffers of the one before
// it are reused.
func (p *Painter) Reset() {
	p.hasPrev = p.ready
	// The ops of the frame before last are let go, all the way to the
	// buffer's end, so a frame of fewer ops keeps none of them alive.
	clear(p.prev[:cap(p.prev)])
	p.prev, p.ops = p.ops, p.prev[:0]
	p.prevBounds, p.bounds = p.bounds, p.prevBounds[:0]
	p.prevBlurs, p.blurs = p.blurs, false
	p.prevTilts, p.tilts = p.tilts, false
	p.prevRRects, p.rrects = p.rrects, p.prevRRects
	p.prevTexts, p.texts = p.texts, p.prevTexts
	p.prevMasks, p.masks = p.masks, p.prevMasks
	p.prevScenes, p.scenes = p.scenes, p.prevScenes
	p.prevRibbons, p.ribbons = p.ribbons, p.prevRibbons
	p.rrects.reset()
	p.texts.reset()
	p.masks.reset()
	p.scenes.reset()
	p.ribbons.reset()
	// The items of the frame before last let go of their meshes, so a
	// mesh no longer drawn is not held on to by a buffer left unused.
	clear(p.prevItems)
	p.prevItems, p.items = p.items, p.prevItems[:0]
	p.prevPoints, p.points = p.points, p.prevPoints[:0]
	p.open = p.open[:0]
	p.stack = p.stack[:0]
	p.cur = Identity
	p.blend, p.blends = BlendNormal, p.blends[:0]
	p.ready = true
	p.clip = nil
	p.proj = nil
	clear(p.floats)
	p.floats = p.floats[:0]
	p.carried = p.carried[:0]
}

// Float puts fn off until the rest of the frame has painted, and runs
// it then in window space, above everything and inside no clip. It is
// for something that must leave its place in the tree for a moment,
// such as an element flying from one screen to the next, which the
// containers around it would otherwise clip. It starts from
// [BlendNormal], whatever blend was in force when it was put off.
func (p *Painter) Float(fn func(*Painter)) { p.floats = append(p.floats, fn) }

// PaintFloats runs the painting put off with Float, including any that
// it puts off in turn. The engine calls it once the tree has painted.
func (p *Painter) PaintFloats() {
	for len(p.floats) > 0 {
		fs := p.floats
		p.floats = nil
		for _, fn := range fs {
			stack, cur, clip, proj, blend := p.stack, p.cur, p.clip, p.proj, p.blend
			p.stack, p.cur, p.clip, p.proj, p.blend = nil, Identity, nil, nil, BlendNormal
			fn(p)
			p.stack, p.cur, p.clip, p.proj, p.blend = stack, cur, clip, proj, blend
		}
	}
}

// Forget drops the frame Damage compares against, so the next frame's
// damage is [Everything]. Use it when the frame recorded before is not
// the one on screen.
func (p *Painter) Forget() {
	p.hasPrev = false
	p.prev = p.prev[:0]
	p.prevBounds = p.prevBounds[:0]
}

// Ops returns the recorded commands in draw order.
func (p *Painter) Ops() []Op { return p.ops }

// lookAhead are how many ops [Painter.Damage] looks past a difference
// for the ops on either side to match again.
var lookAhead = [...]int{1, 2, 3, 4, 5, 6, 7, 8}

// Damage returns the part of the window where this frame differs from
// the one recorded before it: the old and new bounds of every op that
// changed, came or went. A driver redraws just that part, so a button
// easing into its hover colour costs the button and not the window.
//
// It is [Everything] for the first frame, and for a frame that blurs
// or tilts or followed one that did, since a blur spreads a change past
// its bounds, and a tilt moves it. Ops are matched in order, so a node
// that adds ops early in the frame damages what is painted after it, up
// to the next stretch carried with [Painter.Again]: a carried stretch
// is the same by making, wherever it now stands among the ops, and the
// ops after it are matched to those after it the frame before. That
// costs time and never correctness. Past a difference, it looks a few
// ops ahead on either side for the two to match again, so a few ops that
// came or went, as a meter lighting more of its lights draws, damage
// where they stand, and not every op after them.
func (p *Painter) Damage() geom.Rect {
	if !p.hasPrev || p.blurs || p.prevBlurs || p.tilts || p.prevTilts {
		return Everything
	}
	var d geom.Rect
	// matched marks the ops of the frame before that this frame's match;
	// the rest went, and damage where they stood.
	p.matched = slices.Grow(p.matched[:0], len(p.prev))[:len(p.prev)]
	clear(p.matched)
	same := func(i, j int) bool { return p.bounds[i] == p.prevBounds[j] && sameOp(p.ops[i], p.prev[j]) }
	// in reports whether ops i and i+1 match ops j and j+1 before, within
	// i1 and j1: where they do after a difference, the ops between came
	// or went, and those after are matched from there.
	in := func(i, i1, j, j1 int) bool { return i+1 < i1 && j+1 < j1 && same(i, j) && same(i+1, j+1) }
	// compare matches the ops of this frame from i to i1 in order with
	// those of the frame before from j to j1.
	// Where looking ahead failed a few times running, as everything
	// moved in a scroll, it stops until ops match again.
	compare := func(i, i1, j, j1 int) {
		misses := 0
		for i < i1 && j < j1 {
			if same(i, j) {
				p.matched[j] = true
				i, j = i+1, j+1
				misses = 0
				continue
			}
			if misses < 4 {
				if k := slices.IndexFunc(lookAhead[:], func(k int) bool { return in(i, i1, j+k, j1) }); k >= 0 {
					j += lookAhead[k]
					continue
				}
				if k := slices.IndexFunc(lookAhead[:], func(k int) bool { return in(i+k, i1, j, j1) }); k >= 0 {
					for range lookAhead[k] {
						d = d.Union(p.bounds[i])
						i++
					}
					continue
				}
				misses++
			}
			p.matched[j] = true
			d = d.Union(p.bounds[i]).Union(p.prevBounds[j])
			i, j = i+1, j+1
		}
		for ; i < i1; i++ {
			d = d.Union(p.bounds[i])
		}
	}
	i, j := 0, 0
	for _, c := range p.carried {
		if c.at < i || c.from < j || c.from+c.n > len(p.prev) {
			// Out of the order of the frame before: compared as drawn.
			continue
		}
		compare(i, c.at, j, c.from)
		for k := range c.n {
			p.matched[c.from+k] = true
		}
		i, j = c.at+c.n, c.from+c.n
	}
	compare(i, len(p.ops), j, len(p.prev))

	for k, ok := range p.matched {
		if !ok {
			d = d.Union(p.prevBounds[k])
		}
	}
	return d
}

// sameOp reports whether two ops draw the same thing.
func sameOp(a, b Op) bool {
	switch a := a.(type) {
	case *RRectOp:
		b, ok := b.(*RRectOp)
		if !ok {
			return false
		}
		if !a.Fill.Gradient.Equal(b.Fill.Gradient) {
			return false
		}
		a2, b2 := *a, *b
		a2.Fill.Gradient, b2.Fill.Gradient = nil, nil
		return a2 == b2
	case *TextOp:
		b, ok := b.(*TextOp)
		return ok && a.Size == b.Size && a.Color == b.Color && a.Transform == b.Transform && a.Blend == b.Blend &&
			a.FadeFrom == b.FadeFrom && a.FadeTo == b.FadeTo && slices.Equal(a.Glyphs, b.Glyphs)
	case *ImageOp:
		b, ok := b.(*ImageOp)
		return ok && *a == *b
	case *MaskOp:
		b, ok := b.(*MaskOp)
		if !ok || !a.Gradient.Equal(b.Gradient) {
			return false
		}
		a2, b2 := *a, *b
		a2.Gradient, b2.Gradient = nil, nil
		return a2 == b2
	case *LayerOp:
		b, ok := b.(*LayerOp)
		return ok && *a == *b
	case *LayerEndOp:
		_, ok := b.(*LayerEndOp)
		return ok
	case *CellsOp:
		b, ok := b.(*CellsOp)
		return ok && sameCells(a, b)
	case *SceneOp:
		b, ok := b.(*SceneOp)
		return ok && sameScene(a, b)
	case *RibbonOp:
		b, ok := b.(*RibbonOp)
		return ok && sameRibbon(a, b)
	}
	return false
}

// Transform is an affine transform, stored as the two rows of a 2x3
// matrix. Scale and translate cover almost everything a widget does;
// rotation is there for spinners and the odd flourish.
type Transform struct {
	A, B, C float32 // x' = A*x + B*y + C
	D, E, F float32 // y' = D*x + E*y + F
}

// Identity is the transform that leaves a point where it is.
var Identity = Transform{1, 0, 0, 0, 1, 0}

// Translate moves by p.
func Translate(p geom.Point) Transform { return Transform{1, 0, p.X, 0, 1, p.Y} }

// Scale scales by s about the point at.
//
// A centre is what you want nine times out of ten: a dialog growing
// into place should swell from its middle and stay where it is.
func Scale(s float32, at geom.Point) Transform {
	return Transform{s, 0, at.X * (1 - s), 0, s, at.Y * (1 - s)}
}

// Rotate turns by rad radians about the point at, clockwise on screen.
func Rotate(rad float32, at geom.Point) Transform {
	s, c := float32(math.Sin(float64(rad))), float32(math.Cos(float64(rad)))
	return Transform{
		c, -s, at.X - c*at.X + s*at.Y,
		s, c, at.Y - s*at.X - c*at.Y,
	}
}

// Mul returns t applied after u.
func (t Transform) Mul(u Transform) Transform {
	return Transform{
		A: t.A*u.A + t.B*u.D,
		B: t.A*u.B + t.B*u.E,
		C: t.A*u.C + t.B*u.F + t.C,
		D: t.D*u.A + t.E*u.D,
		E: t.D*u.B + t.E*u.E,
		F: t.D*u.C + t.E*u.F + t.F,
	}
}

// Invert returns the transform that undoes t. It reports false when t
// has no inverse, as when it scales to zero and folds the plane onto a
// line.
func (t Transform) Invert() (Transform, bool) {
	det := t.A*t.E - t.B*t.D
	if det == 0 {
		return Transform{}, false
	}
	a, b := t.E/det, -t.B/det
	d, e := -t.D/det, t.A/det
	return Transform{
		A: a, B: b, C: -(a*t.C + b*t.F),
		D: d, E: e, F: -(d*t.C + e*t.F),
	}, true
}

// Apply maps a point through t.
func (t Transform) Apply(p geom.Point) geom.Point {
	return geom.Point{X: t.A*p.X + t.B*p.Y + t.C, Y: t.D*p.X + t.E*p.Y + t.F}
}

// at returns the transform in force. A zero Painter starts from the
// identity.
func (p *Painter) at() Transform {
	if !p.ready {
		return Identity
	}
	return p.cur
}

// Transform returns the transform in force, which maps the current
// drawing space to window space.
func (p *Painter) Transform() Transform { return p.at() }

// Push applies t on top of the current transform and returns the
// function that pops it, so a node can write:
//
//	defer p.Push(paint.Translate(origin))()
func (p *Painter) Push(t Transform) func() {
	p.stack = append(p.stack, p.at())
	p.cur = p.at().Mul(t)
	p.ready = true
	if p.popFn == nil {
		p.popFn = p.pop
	}
	return p.popFn
}

func (p *Painter) pop() {
	n := len(p.stack) - 1
	p.cur = p.stack[n]
	p.stack = p.stack[:n]
}

// LayerOpts describes an offscreen group.
type LayerOpts struct {
	// Bounds is the area the layer covers.
	Bounds geom.Rect
	// Opacity multiplies the whole group at once, so overlapping shapes
	// inside it stay opaque to one another as the group fades.
	Opacity float32
	// Blur blurs the layer's own contents. It is the standard deviation
	// of a Gaussian in logical pixels, as in CSS's blur().
	Blur float32
	// Backdrop blurs whatever is already behind the layer, within its
	// bounds, rounded by Radius when the layer clips. It is a standard
	// deviation like Blur. This is the frosted glass behind a modal,
	// and it is why layers are a first-class idea here: it needs the
	// frame so far as a texture.
	Backdrop float32
	// Tilt turns the layer in depth, in perspective. A tilted layer
	// draws offscreen, and its Backdrop blurs behind its flat Bounds.
	Tilt Tilt
	// Clip confines drawing to Bounds, rounded by Radius.
	Clip   bool
	Radius float32
	// Ellipse makes Clip confine drawing to the ellipse that fits
	// Bounds, and Radius goes unused. An opaque layer, unblurred and
	// unturned, clips to it in place, with no offscreen pass, as it
	// does to a rectangle.
	Ellipse bool
	// Fade fades the layer out toward each edge of Bounds, over that
	// many logical pixels in from the edge, as a list fades where it
	// scrolls on past its edge. A layer that fades shows only within
	// Bounds, clipping or not. A tilted layer does not fade.
	Fade geom.Insets
	// Blend is how the whole layer meets what is beneath it as it
	// composites, after its contents have drawn into it with their own
	// blends; [BlendInherit] takes the blend in force, as an op does.
	// A layer that adds with [BlendAdd] adds its colours, times
	// Opacity, to what is beneath, and hides nothing: the layer's
	// alpha goes unused. Its Backdrop, if any, still draws normally
	// beneath it. An added layer always draws offscreen.
	Blend Blend
}

// Layer opens an offscreen group and returns the function that closes
// and composites it:
//
//	defer p.Layer(paint.LayerOpts{Opacity: t, Backdrop: 12 * t})()
func (p *Painter) Layer(o LayerOpts) func() {
	o.Blend = p.blendFor(o.Blend)
	if o.Blur > 0 || o.Backdrop > 0 {
		p.blurs = true
	}
	p.record(&LayerOp{Opts: o, Transform: p.at()}, o.Bounds)
	at := len(p.ops) - 1
	p.open = append(p.open, at)
	outer, outerProj := p.clip, p.proj
	if o.Tilt.tilted() {
		p.tilts = true
		at, b := p.at(), o.Bounds
		centre := at.Apply(geom.Pt((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2))
		var flat [4]geom.Point
		for i, c := range []geom.Point{{X: b.Min.X - 1, Y: b.Min.Y - 1}, {X: b.Max.X + 1, Y: b.Min.Y - 1},
			{X: b.Max.X + 1, Y: b.Max.Y + 1}, {X: b.Min.X - 1, Y: b.Max.Y + 1}} {
			flat[i] = at.Apply(c)
		}
		p.proj = newProjection(o.Tilt, centre, outerProj, flat)
		if !o.Clip {
			// A tilted layer shows only what lies in its Bounds, clipping
			// or not, and takes input there alone.
			c := &Clip{outer: outer, proj: p.proj, rect: b}
			c.inv, c.ok = at.Invert()
			p.clip = c
		}
	}
	if o.Clip {
		c := &Clip{outer: outer, proj: p.proj, rect: o.Bounds, radius: o.Radius, ellipse: o.Ellipse}
		c.inv, c.ok = p.at().Invert()
		p.clip = c
	}
	return func() {
		if i := slices.Index(p.open, at); i >= 0 {
			p.open = slices.Delete(p.open, i, i+1)
		}
		// The end covers what the layer does, since compositing it
		// draws there.
		p.ops = append(p.ops, &LayerEndOp{})
		p.bounds = append(p.bounds, p.bounds[at])
		p.clip, p.proj = outer, outerProj
	}
}

// Clip returns the clipping in force: every open layer that clips,
// innermost first. It is nil when nothing clips.
func (p *Painter) Clip() *Clip { return p.clip }

// Visible is the part of the painter's space that the clips around it
// let through, for a node with much to draw to leave out what can't
// show, as a long document in a scroll does its thousands of lines. It
// reports false where that can't be told, under a perspective or with
// no clip at all, and everything should be drawn.
func (p *Painter) Visible() (geom.Rect, bool) {
	if p.Projection() != nil {
		return geom.Rect{}, false
	}
	b, clipped := p.Clip().Bounds()
	if !clipped {
		return geom.Rect{}, false
	}
	back, ok := p.Transform().Invert()
	if !ok {
		return geom.Rect{}, false
	}
	var out geom.Rect
	for i, q := range []geom.Point{b.Min, {X: b.Max.X, Y: b.Min.Y}, b.Max, {X: b.Min.X, Y: b.Max.Y}} {
		at := back.Apply(q)
		if i == 0 {
			out = geom.Rect{Min: at, Max: at}
			continue
		}
		out.Min.X, out.Min.Y = min(out.Min.X, at.X), min(out.Min.Y, at.Y)
		out.Max.X, out.Max.Y = max(out.Max.X, at.X), max(out.Max.Y, at.Y)
	}
	return out, true
}

// Projection returns the perspective in force: every open layer that
// tilts. It is nil when nothing tilts.
func (p *Painter) Projection() *Projection { return p.proj }

// A Clip is the area a clipping layer lets drawing through, together
// with the clips around it. It stays valid after the frame that made it,
// which lets input be tested against what the frame showed.
type Clip struct {
	outer *Clip
	// proj is the perspective the clip was made under, which a point on
	// the screen is taken back through to test it.
	proj    *Projection
	inv     Transform
	ok      bool
	rect    geom.Rect
	radius  float32
	ellipse bool
}

// Same reports whether c and o clip alike: the same shapes, in the same
// spaces, inside the same clips, none of them tilted.
func (c *Clip) Same(o *Clip) bool {
	for ; c != nil || o != nil; c, o = c.outer, o.outer {
		if c == nil || o == nil || c.proj != nil || o.proj != nil || c.inv != o.inv || c.ok != o.ok ||
			c.rect != o.rect || c.radius != o.radius || c.ellipse != o.ellipse {
			return false
		}
	}
	return true
}

// Contains reports whether p, a point on the screen, in window space,
// lies inside c and every clip around it. A nil Clip contains every
// point.
func (c *Clip) Contains(p geom.Point) bool {
	for ; c != nil; c = c.outer {
		flat, shown := c.proj.Unapply(p)
		if !c.ok || !shown {
			return false
		}
		q := c.inv.Apply(flat)
		if c.ellipse && !insideEllipse(q, c.rect) || !c.ellipse && !insideRounded(q, c.rect, c.radius) {
			return false
		}
	}
	return true
}

// Bounds returns the rectangle around what c and every clip around it
// let through, in window space, and false for a nil Clip, where nothing
// clips. A clip under a perspective is left out of it, as its edges are
// not straight on the screen.
func (c *Clip) Bounds() (geom.Rect, bool) {
	if c == nil {
		return geom.Rect{}, false
	}
	var out geom.Rect
	first := true
	for ; c != nil; c = c.outer {
		if c.proj != nil {
			continue
		}
		fwd, ok := c.inv.Invert()
		if !c.ok || !ok {
			return geom.Rect{}, true
		}
		r := c.rect
		var b geom.Rect
		for i, q := range []geom.Point{r.Min, {X: r.Max.X, Y: r.Min.Y}, r.Max, {X: r.Min.X, Y: r.Max.Y}} {
			w := fwd.Apply(q)
			if i == 0 {
				b = geom.Rect{Min: w, Max: w}
				continue
			}
			b.Min.X, b.Min.Y = min(b.Min.X, w.X), min(b.Min.Y, w.Y)
			b.Max.X, b.Max.Y = max(b.Max.X, w.X), max(b.Max.Y, w.Y)
		}
		if first {
			out, first = b, false
			continue
		}
		out.Min.X, out.Min.Y = max(out.Min.X, b.Min.X), max(out.Min.Y, b.Min.Y)
		out.Max.X, out.Max.Y = min(out.Max.X, b.Max.X), min(out.Max.Y, b.Max.Y)
		if out.Empty() {
			return geom.Rect{}, true
		}
	}
	if first {
		return geom.Rect{}, false
	}
	return out, true
}

// insideRounded reports whether p lies inside r with its corners
// rounded by radius.
func insideRounded(p geom.Point, r geom.Rect, radius float32) bool {
	if !r.Contains(p) {
		return false
	}
	radius = min(radius, (r.Max.X-r.Min.X)/2, (r.Max.Y-r.Min.Y)/2)
	if radius <= 0 {
		return true
	}
	// Distance into the corner square, from the centre of its rounding.
	dx := max(r.Min.X+radius-p.X, p.X-(r.Max.X-radius), 0)
	dy := max(r.Min.Y+radius-p.Y, p.Y-(r.Max.Y-radius), 0)
	return dx*dx+dy*dy <= radius*radius
}

// insideEllipse reports whether p lies inside the ellipse that fits r.
func insideEllipse(p geom.Point, r geom.Rect) bool {
	rx, ry := (r.Max.X-r.Min.X)/2, (r.Max.Y-r.Min.Y)/2
	if rx <= 0 || ry <= 0 {
		return false
	}
	dx, dy := (p.X-r.Min.X-rx)/rx, (p.Y-r.Min.Y-ry)/ry
	return dx*dx+dy*dy <= 1
}

// Fill describes how a shape is coloured. Exactly one of Solid or
// Gradient applies; a zero Gradient means Solid.
type Fill struct {
	Solid    color.NRGBA
	Gradient *Gradient
}

// Solid is shorthand for a flat fill.
func Solid(c color.NRGBA) Fill { return Fill{Solid: c} }

// Gradient is a gradient from Start at From to End at To. Past either
// end it keeps the colour there.
//
// A linear gradient changes along the line from From to To, and stays
// the same across it. A radial one changes in circles out from From,
// and reaches End at the distance to To; Aspect, or a transform that
// scales one way more than the other, makes the circles ellipses.
type Gradient struct {
	From, To   geom.Point
	Start, End color.NRGBA
	// Radial makes the gradient run out from From in circles.
	Radial bool
	// Aspect makes a radial gradient's circles ellipses: it reaches End
	// at the distance to To along the line to To, and at Aspect times
	// that distance at right angles to it. Zero keeps them circles, as 1
	// does.
	Aspect float32
	// Stops are colours on the way from Start to End, in order, each at
	// its place between 0, at From, and 1, at To. A shape draws a
	// gradient of two colours a little quicker than one with stops.
	Stops []Stop
}

// Stop is a colour on a gradient's way, At from 0 to 1.
type Stop struct {
	At    float32
	Color color.NRGBA
}

// Equal reports whether g and h draw the same, nil being no gradient.
func (g *Gradient) Equal(h *Gradient) bool {
	if g == nil || h == nil {
		return g == h
	}
	return g.From == h.From && g.To == h.To && g.Start == h.Start && g.End == h.End &&
		g.Radial == h.Radial && g.Aspect == h.Aspect && slices.Equal(g.Stops, h.Stops)
}

// At returns the gradient's colour at t, from 0 at From to 1 at To:
// what it blends between its stops, straight, before any alpha
// multiplies the colour in.
func (g *Gradient) At(t float32) color.NRGBA {
	t = min(max(t, 0), 1)
	prev := Stop{At: 0, Color: g.Start}
	for i := 0; i <= len(g.Stops); i++ {
		s := Stop{At: 1, Color: g.End}
		if i < len(g.Stops) {
			s = g.Stops[i]
		}
		// A stop out of order sits at the one before it.
		at := min(max(s.At, prev.At), 1)
		if t <= at {
			if at <= prev.At {
				return s.Color
			}
			return mix(prev.Color, s.Color, (t-prev.At)/(at-prev.At))
		}
		prev = Stop{At: at, Color: s.Color}
	}
	return g.End
}

// mix returns a blended to b by t, channel by channel.
func mix(a, b color.NRGBA, t float32) color.NRGBA {
	m := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), m(a.A, b.A)}
}

// Stroke describes an outline.
type Stroke struct {
	Width float32
	Color color.NRGBA
}

// RRectOp draws a rounded rectangle, optionally stroked, optionally
// with a drop shadow, and shaded inside. One command covers most of a
// widget set.
type RRectOp struct {
	Rect   geom.Rect
	Radius float32
	Fill   Fill
	Stroke Stroke
	Shadow Shadow
	// Inset are up to two shadows inside the shape, over its fill and
	// under its stroke, as a dark core shadow and a light rim give it
	// depth. An inset shadow lies where the shape, moved by Offset and
	// shrunk by Spread, leaves it, softened by Blur, and stays within the
	// shape. A zero Shadow is skipped.
	Inset     [2]Shadow
	Transform Transform
	// Blend is how the shape, its shadows and its stroke meet what is
	// beneath them. The painter sets it from the blend in force as the
	// op is recorded, unless the op asks for one of its own in
	// DrawRRect; see [Painter.Blend].
	Blend Blend
}

// Shadow is a soft drop shadow behind a shape. A zero Shadow is
// skipped.
type Shadow struct {
	Offset geom.Point
	Blur   float32
	Spread float32
	Color  color.NRGBA
	// Outside keeps the shadow out from under the shape, as CSS's
	// box-shadow is: a see-through shape, such as frosted glass, then
	// shows what lies behind it undarkened. A drop shadow, Outside
	// applies to it alone, not to an inset shadow.
	Outside bool
}

// TextOp draws a run of already-shaped text.
//
// Shaping happens outside paint, in the text package, because it
// depends on the font and the script, which outlive any one frame.
// Glyphs carry fractional positions, so a label animating across the
// screen stays steady as it crosses pixel boundaries.
type TextOp struct {
	Glyphs []Glyph
	// Size is the font size in logical pixels.
	Size      float32
	Color     color.NRGBA
	Transform Transform
	// Blend is how the glyphs meet what is beneath them, set from the
	// blend in force as the op is recorded; see [Painter.Blend]. Text
	// added with [BlendAdd] glows over what it crosses.
	Blend Blend
	// FadeFrom and FadeTo fade the text out along x, in the op's own
	// space: whole up to FadeFrom, gone by FadeTo and past it, as text
	// cut short fades where it is cut. FadeTo no further than FadeFrom
	// fades nothing.
	FadeFrom, FadeTo float32
}

// Glyph is one positioned glyph from a shaped run.
type Glyph struct {
	// ID identifies the glyph within its face, after shaping. Shaping
	// breaks the link with runes: one rune can become several glyphs,
	// and several runes can become one.
	ID uint32
	// At is the glyph origin, with subpixel precision.
	At geom.Point
	// Face indexes the font face the glyph was shaped with.
	Face uint32
}

// An Op is one recorded draw command.
type Op interface{ isOp() }

// LayerOp opens an offscreen group; LayerEndOp composites it.
type LayerOp struct {
	Opts      LayerOpts
	Transform Transform
}

// LayerEndOp closes the most recently opened layer.
type LayerEndOp struct{}

func (*RRectOp) isOp()    {}
func (*TextOp) isOp()     {}
func (*LayerOp) isOp()    {}
func (*LayerEndOp) isOp() {}

// takeRRect takes an RRectOp from the painter's blocks.
func (p *Painter) takeRRect(op RRectOp) *RRectOp {
	v := p.rrects.take()
	*v = op
	return v
}

// RRect records a rounded rectangle.
func (p *Painter) RRect(r geom.Rect, radius float32, f Fill) {
	p.record(p.takeRRect(RRectOp{Rect: r, Radius: radius, Fill: f, Transform: p.at(), Blend: p.blendNow()}), r)
}

// RRectStroke records a rounded rectangle with an outline, which is
// centred on the rectangle's edge.
func (p *Painter) RRectStroke(r geom.Rect, radius float32, f Fill, s Stroke) {
	half := s.Width / 2
	p.record(p.takeRRect(RRectOp{Rect: r, Radius: radius, Fill: f, Stroke: s, Transform: p.at(), Blend: p.blendNow()}),
		geom.Rect{Min: geom.Pt(r.Min.X-half, r.Min.Y-half), Max: geom.Pt(r.Max.X+half, r.Max.Y+half)})
}

// ShadowRRect records a rounded rectangle with a drop shadow.
func (p *Painter) ShadowRRect(r geom.Rect, radius float32, f Fill, sh Shadow) {
	grown := geom.Rect{
		Min: geom.Pt(r.Min.X-sh.Blur-sh.Spread+sh.Offset.X, r.Min.Y-sh.Blur-sh.Spread+sh.Offset.Y),
		Max: geom.Pt(r.Max.X+sh.Blur+sh.Spread+sh.Offset.X, r.Max.Y+sh.Blur+sh.Spread+sh.Offset.Y),
	}
	p.record(p.takeRRect(RRectOp{Rect: r, Radius: radius, Fill: f, Shadow: sh, Transform: p.at(), Blend: p.blendNow()}),
		grown.Union(r))
}

// DrawRRect records op as it is, in the transform in force, with any
// of a rounded rectangle's parts: a fill, a stroke, a drop shadow and
// inset shadows. It draws with op's Blend, or with the blend in force
// where that is [BlendInherit].
func (p *Painter) DrawRRect(op RRectOp) {
	b := op.Rect
	half := op.Stroke.Width / 2
	b = geom.Rect{Min: geom.Pt(b.Min.X-half, b.Min.Y-half), Max: geom.Pt(b.Max.X+half, b.Max.Y+half)}
	if sh := op.Shadow; sh.Color.A > 0 {
		grow := sh.Blur + sh.Spread
		b = b.Union(geom.Rect{
			Min: geom.Pt(op.Rect.Min.X-grow+sh.Offset.X, op.Rect.Min.Y-grow+sh.Offset.Y),
			Max: geom.Pt(op.Rect.Max.X+grow+sh.Offset.X, op.Rect.Max.Y+grow+sh.Offset.Y),
		})
	}
	op.Transform = p.at()
	op.Blend = p.blendFor(op.Blend)
	p.record(p.takeRRect(op), b)
}

// Text records a shaped run at size logical pixels. bounds is the
// area the run covers, for damage tracking. The text package's Run.Paint
// is the usual way to call it.
func (p *Painter) Text(g []Glyph, size float32, c color.NRGBA, bounds geom.Rect) {
	op := p.texts.take()
	*op = TextOp{Glyphs: g, Size: size, Color: c, Transform: p.at(), Blend: p.blendNow()}
	p.record(op, bounds)
}

// TextFaded is [Painter.Text] fading out along x, whole up to from and
// gone by to, in the current space: text cut short at to that fades
// where it is cut, rather than stopping at an edge or ending in an
// ellipsis that takes the room of letters. Nothing past to shows, so
// bounds need reach no further.
func (p *Painter) TextFaded(g []Glyph, size float32, c color.NRGBA, bounds geom.Rect, from, to float32) {
	op := p.texts.take()
	*op = TextOp{Glyphs: g, Size: size, Color: c, Transform: p.at(), Blend: p.blendNow(), FadeFrom: from, FadeTo: to}
	bounds.Max.X = min(bounds.Max.X, to)
	p.record(op, bounds)
}

// record adds op, which draws within bounds in the current space. The
// bounds are kept in window space, whatever space the node happened to
// be painting in, grown by a pixel and a half for antialiasing.
func (p *Painter) record(op Op, bounds geom.Rect) {
	t := p.at()
	a, b := t.Apply(bounds.Min), t.Apply(bounds.Max)
	w := geom.Rect{Min: geom.Pt(min(a.X, b.X), min(a.Y, b.Y)), Max: geom.Pt(max(a.X, b.X), max(a.Y, b.Y))}
	// Turned, the other two corners can reach further; scaled and moved,
	// as nearly everything is drawn, the two opposite ones say it all.
	if t.B != 0 || t.D != 0 {
		for _, c := range [...]geom.Point{{X: bounds.Max.X, Y: bounds.Min.Y}, {X: bounds.Min.X, Y: bounds.Max.Y}} {
			q := t.Apply(c)
			w.Min = geom.Pt(min(w.Min.X, q.X), min(w.Min.Y, q.Y))
			w.Max = geom.Pt(max(w.Max.X, q.X), max(w.Max.Y, q.Y))
		}
	}
	const aa = 1.5
	w = geom.Rect{Min: geom.Pt(w.Min.X-aa, w.Min.Y-aa), Max: geom.Pt(w.Max.X+aa, w.Max.Y+aa)}
	p.ops = append(p.ops, op)
	p.bounds = append(p.bounds, w)
	// Every layer open around the op draws where it does.
	for _, i := range p.open {
		p.bounds[i] = p.bounds[i].Union(w)
	}
}
