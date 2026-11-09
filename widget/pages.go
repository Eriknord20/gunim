package widget

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// PageSlide is how far a page shown by [Pages] slides as it comes in.
var PageSlide = theme.Length("pages.slide", 24)

// Pages shows one page at a time, as the right side of a settings
// sidebar does, and animates each change: the page coming slides in
// from the side it was asked to come from as it fades in, and the page
// going fades out where it stood, taking none of the room.
//
// Show changes the page. A page asked for again while it is shown stays
// as it is, with what was typed in it.
type Pages struct {
	anim.Group
	cur *pageFrame
	// gone are the pages shown before, fading out, which the engine
	// holds until they have.
	gone []*pageFrame
}

// NewPages returns pages showing first, which arrives with no motion.
func NewPages(first gunim.Node) *Pages {
	p := &Pages{}
	if first != nil {
		p.cur = newPageFrame(first, geom.Point{})
		p.cur.shown.Jump(1)
	}
	return p
}

// Page returns the page shown, or nil.
func (p *Pages) Page() gunim.Node {
	if p.cur == nil {
		return nil
	}
	return p.cur.child
}

// Show shows page in place of the one shown, coming from the side from
// points to: (0, 1) from below, as for a row picked further down a
// sidebar; (0, -1) from above; (1, 0) from the right. A zero from fades
// it in where it stands. Before the pages are mounted, u may be nil.
func (p *Pages) Show(page gunim.Node, from geom.Point, u *gunim.UI) {
	if p.cur != nil && p.cur.child == page {
		return
	}
	old := p.cur
	p.cur = newPageFrame(page, from)
	if u == nil {
		p.cur.shown.Jump(1)
		return
	}
	if old != nil {
		p.gone = append(p.gone, old)
		u.Remove(old)
	}
	u.Insert(p, p.cur)
	u.Invalidate()
}

// Children implements [gunim.Composite]: the page shown.
func (p *Pages) Children() []gunim.Node {
	if p.cur == nil {
		return nil
	}
	return []gunim.Node{p.cur}
}

// Layout implements [gunim.Node]: every page laid out the same, the
// one shown taking the room, those going drawn where they stood.
func (p *Pages) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	var size geom.Size
	live := map[*pageFrame]bool{}
	for k := range kids.All {
		frame, ok := k.Node().(*pageFrame)
		if !ok {
			continue
		}
		live[frame] = true
		s := k.Layout(c)
		k.Place(geom.Point{})
		if frame == p.cur {
			size = s
		}
	}
	// Those the engine has let go of are gone for good.
	kept := p.gone[:0]
	for _, g := range p.gone {
		if live[g] {
			kept = append(kept, g)
		}
	}
	clear(p.gone[len(kept):])
	p.gone = kept
	return c.Constrain(size)
}

// Paint implements [gunim.Node]: those going first, under the one
// coming.
func (p *Pages) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	var cur gunim.Child
	found := false
	for k := range kids.All {
		if k.Node() == gunim.Node(p.cur) {
			cur, found = k, true
			continue
		}
		k.Paint(pt)
	}
	if found {
		cur.Paint(pt)
	}
}

// pageFrame holds one page, sliding and fading it in and out.
type pageFrame struct {
	anim.Group
	child gunim.Node
	from  geom.Point
	shown *anim.Float
	size  geom.Size
	// leaving says it is on its way out, and fades where it stands.
	leaving bool
}

func newPageFrame(child gunim.Node, from geom.Point) *pageFrame {
	f := &pageFrame{child: child, from: from, shown: anim.NewFloat(0)}
	f.Add(f.shown)
	return f
}

// Transition implements [gunim.Transitioner]: a page comes in and goes
// quickly, so the one going is out of the way of the one coming.
func (f *pageFrame) Transition(p gunim.Presence, fr gunim.Frame) bool {
	switch p {
	case gunim.Entering, gunim.Present:
		f.shown.Animate(1, Quick.Get(fr.Theme))
	case gunim.Exiting:
		f.leaving = true
		f.shown.Animate(0, Quick.Get(fr.Theme))
	}
	return !f.shown.Active()
}

// Children implements [gunim.Composite].
func (f *pageFrame) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node]: the page, offset towards where it
// comes from while it arrives.
func (f *pageFrame) Layout(c gunim.Constraints, fr gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	f.size = k.Layout(c)
	away := (1 - min(max(f.shown.Value(), 0), 1)) * PageSlide.Get(fr.Theme)
	if f.leaving {
		// Going, it fades where it stands.
		away = 0
	}
	k.Place(geom.Pt(f.from.X*away, f.from.Y*away))
	return f.size
}

// Paint implements [gunim.Node].
func (f *pageFrame) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	if t := min(max(f.shown.Value(), 0), 1); t < 0.999 {
		if f.leaving {
			// Going, it fades sooner than it moves, so its words are
			// mostly gone before the next page's settle over them.
			t *= t
		}
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t})()
	}
	kids.At(0).Paint(p)
}
