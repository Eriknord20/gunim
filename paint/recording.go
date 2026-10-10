package paint

import (
	"slices"

	"github.com/marrasen/gunim/geom"
)

// A Recording is a stretch of a frame's commands kept past the frame,
// to be drawn again somewhere else: what a node drew, shown small in an
// overview, still there once the node has left the screen.
//
// It keeps the commands in the space that was in force as it began, so
// drawing it again under another transform moves and scales the whole
// of it, and it reuses its buffers from one keeping to the next.
type Recording struct {
	ops []keptOp
	// glyphs holds every text command's glyphs, back to back, and items
	// every scene's items. A kept command never points into the
	// painter's own buffers, which later frames reuse.
	glyphs []Glyph
	items  []SceneItem
}

// keptOp is one command, its transform taken back into the recording's
// space, and its bounds in its own space.
type keptOp struct {
	op     Op
	rel    Transform
	bounds geom.Rect
	// from and to are where a text command's glyphs lie in the glyph
	// buffer, or a scene's items in the item buffer.
	from, to int
}

// Mark returns where the next command recorded will go, for Keep.
func (p *Painter) Mark() int { return len(p.ops) }

// Keep copies the commands recorded since mark into r, in the space in
// force now, which should be the one in force at mark. Layers opened in
// the stretch must have closed in it.
func (p *Painter) Keep(mark int, r *Recording) {
	inv, ok := p.at().Invert()
	r.ops, r.glyphs = r.ops[:0], r.glyphs[:0]
	clear(r.items)
	r.items = r.items[:0]
	if !ok {
		return
	}
	for i := mark; i < len(p.ops); i++ {
		k := keptOp{}
		var own Transform
		switch op := p.ops[i].(type) {
		case *RRectOp:
			c := *op
			k.op, own = &c, op.Transform
		case *TextOp:
			c := *op
			k.from = len(r.glyphs)
			r.glyphs = append(r.glyphs, op.Glyphs...)
			k.to = len(r.glyphs)
			c.Glyphs = nil
			k.op, own = &c, op.Transform
		case *ImageOp:
			c := *op
			k.op, own = &c, op.Transform
		case *MaskOp:
			c := *op
			k.op, own = &c, op.Transform
		case *SceneOp:
			c := *op
			k.from = len(r.items)
			r.items = append(r.items, op.Scene.Items...)
			k.to = len(r.items)
			c.Scene.Items = nil
			k.op, own = &c, op.Transform
		case *LayerOp:
			c := *op
			k.op, own = &c, op.Transform
		case *LayerEndOp:
			k.op, own = &LayerEndOp{}, p.at()
		default:
			continue
		}
		k.rel = inv.Mul(own)
		// The bounds were kept in window space; back into the command's
		// own space, as record takes them.
		if back, ok := own.Invert(); ok {
			k.bounds = box(back, p.bounds[i])
		}
		r.ops = append(r.ops, k)
	}
}

// Empty reports whether the recording holds nothing to draw.
func (r *Recording) Empty() bool { return r == nil || len(r.ops) == 0 }

// Replay draws the recording again, in the current space. Each command
// keeps the blend it was recorded with, as [Painter.Again] keeps it,
// whatever the blend in force.
func (p *Painter) Replay(r *Recording) {
	if r.Empty() {
		return
	}
	var closers []func()
	for _, k := range r.ops {
		pop := p.Push(k.rel)
		switch op := k.op.(type) {
		case *RRectOp:
			c := *op
			c.Transform = p.at()
			p.record(p.takeRRect(c), k.bounds)
		case *TextOp:
			t := p.texts.take()
			*t = *op
			t.Glyphs = slices.Clone(r.glyphs[k.from:k.to])
			t.Transform = p.at()
			p.record(t, k.bounds)
		case *ImageOp:
			c := *op
			c.Transform = p.at()
			p.record(&c, k.bounds)
		case *MaskOp:
			c := *op
			c.Transform = p.at()
			p.record(p.takeMask(c), k.bounds)
		case *SceneOp:
			s := op.Scene
			s.Items = r.items[k.from:k.to]
			p.Scene(op.Rect, s)
		case *LayerOp:
			closers = append(closers, p.Layer(op.Opts))
		case *LayerEndOp:
			if n := len(closers); n > 0 {
				closers[n-1]()
				closers = closers[:n-1]
			}
		}
		pop()
	}
	// A layer left open closes here, so the frame stays balanced.
	for i := len(closers) - 1; i >= 0; i-- {
		closers[i]()
	}
}

// box is the rectangle t maps r's corners into, squared to the axes.
func box(t Transform, r geom.Rect) geom.Rect {
	first := t.Apply(r.Min)
	out := geom.Rect{Min: first, Max: first}
	for _, c := range [...]geom.Point{{X: r.Max.X, Y: r.Min.Y}, r.Max, {X: r.Min.X, Y: r.Max.Y}} {
		q := t.Apply(c)
		out.Min = geom.Pt(min(out.Min.X, q.X), min(out.Min.Y, q.Y))
		out.Max = geom.Pt(max(out.Max.X, q.X), max(out.Max.Y, q.Y))
	}
	return out
}
