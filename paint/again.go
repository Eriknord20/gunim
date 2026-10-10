package paint

// A Run is a stretch of one frame's commands, which a node that draws
// the same again next frame can hand on with [Painter.Again] rather
// than drawing it anew: a terminal of thousands of cells, still while
// something beside it moves.
//
// It names the commands by where they lie in the frame and by the first
// and the last of them, so a run from another painter, or from another
// frame than the one before, is told apart and refused.
type Run struct {
	p           *Painter
	from, to    int
	first, last Op
}

// RunFrom returns the commands recorded since mark as a run.
func (p *Painter) RunFrom(mark int) Run {
	r := Run{p: p, from: mark, to: len(p.ops)}
	if r.to > r.from {
		r.first, r.last = p.ops[r.from], p.ops[r.to-1]
	}
	return r
}

// carried is a stretch of this frame's commands copied from the frame
// before: n of them, at at, from from.
type carried struct{ at, from, n int }

// Again records run, from the frame before, into this frame unchanged,
// and reports whether it could. It can when run is from this painter's
// frame before this one and holds only shapes, text, images and masks; with
// false nothing is recorded, and the node draws as usual.
//
// The commands keep the transforms and blends they were drawn with, so
// the space in force should be the one they were drawn in, and the
// blend in force does not change them. [Painter.Damage] takes
// commands carried to the place they held as unchanged, without
// comparing them.
func (p *Painter) Again(run Run) bool {
	if run.p != p || !p.hasPrev || run.from < 0 || run.to > len(p.prev) || run.from > run.to {
		return false
	}
	if run.to > run.from && (p.prev[run.from] != run.first || p.prev[run.to-1] != run.last) {
		return false
	}
	for _, op := range p.prev[run.from:run.to] {
		switch op.(type) {
		case *RRectOp, *TextOp, *ImageOp, *MaskOp, *CellsOp:
		default:
			return false
		}
	}
	at := len(p.ops)
	for i, op := range p.prev[run.from:run.to] {
		switch op := op.(type) {
		case *RRectOp:
			p.ops = append(p.ops, p.takeRRect(*op))
		case *TextOp:
			t := p.texts.take()
			*t = *op
			p.ops = append(p.ops, t)
		case *ImageOp:
			c := *op
			p.ops = append(p.ops, &c)
		case *MaskOp:
			p.ops = append(p.ops, p.takeMask(*op))
		case *CellsOp:
			c := *op
			p.ops = append(p.ops, &c)
		}
		b := p.prevBounds[run.from+i]
		p.bounds = append(p.bounds, b)
		for _, j := range p.open {
			p.bounds[j] = p.bounds[j].Union(b)
		}
	}
	if n := run.to - run.from; n > 0 {
		p.carried = append(p.carried, carried{at: at, from: run.from, n: n})
	}
	return true
}
