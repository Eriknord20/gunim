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

// PageTurn is the motion of each half of a change of [Pages] with
// InTurn set: the page going out, and then the page coming in. Each
// half takes about half the time of a cross-fade on [Quick].
var PageTurn = theme.Spring("motion.pages.turn", anim.Spring{Response: 0.1, Damping: 1})

// pageGone is how faint a page going out must be before the next comes
// in, with InTurn set: 1% of its ink, under three steps of 255, which
// no eye picks out from the page coming over it.
const pageGone = 0.01

// Pages shows one page at a time, as the right side of a settings
// sidebar does, and animates each change: the page coming slides in
// from the side it was asked to come from as it fades in, and the page
// going fades out where it stood, taking none of the room.
//
// With InTurn set, a change goes in two steps instead: the page going
// fades and slides out first, away from the side the next comes from,
// and the next slides and fades in once it has gone. The two are never
// seen over each other.
//
// Show changes the page. A page asked for again while it is shown stays
// as it is, with what was typed in it. A page asked for again while it
// is going out turns and comes back from where it is.
type Pages struct {
	anim.Group
	// InTurn has the page going out fade and slide out before the next
	// comes in, so their words never show over each other. Set it for
	// pages laid out alike, as a caption and an editor in the same places
	// on each, which a cross-fade would show as one garbled page. A
	// change takes about as long as the cross-fade.
	InTurn bool

	cur *pageFrame
	// gone are the pages shown before, fading out, which the engine
	// holds until they have.
	gone []*pageFrame
}

// NewPages returns pages showing first, which arrives with no motion.
func NewPages(first gunim.Node) *Pages {
	p := &Pages{}
	if first != nil {
		p.cur = newPageFrame(p, first, geom.Point{})
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
	if u == nil {
		p.cur = newPageFrame(p, page, from)
		p.cur.shown.Jump(1)
		return
	}
	if old != nil {
		// Going, it moves on the way it was moving: on, away from the
		// next page, once it has come to rest, and back the way it came
		// while it is still coming in.
		old.out = geom.Point{}
		if p.InTurn {
			old.out = old.from
			if !old.shown.Active() {
				old.out = from.Mul(-1)
			}
		}
		p.gone = append(p.gone, old)
		u.Remove(old)
	}
	p.cur = p.back(page)
	if p.cur == nil {
		p.cur = newPageFrame(p, page, from)
	}
	u.Insert(p, p.cur)
	u.Invalidate()
}

// back takes the frame of page off those going, to turn it round, or
// returns nil when page is not among them. It comes back the way it
// was going, from where it stands.
func (p *Pages) back(page gunim.Node) *pageFrame {
	for i, g := range p.gone {
		if g.child != page {
			continue
		}
		p.gone = append(p.gone[:i], p.gone[i+1:]...)
		g.leaving = false
		g.from = g.out
		return g
	}
	return nil
}

// clear reports whether every page going out has faded far enough for
// the next to come in over it.
func (p *Pages) clear() bool {
	for _, g := range p.gone {
		if g.shown.Value() > pageGone {
			return false
		}
	}
	return true
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
	pages *Pages
	child gunim.Node
	// from is the side it comes in from, and out the side it goes out
	// to; a zero one stays where it stands.
	from, out geom.Point
	shown     *anim.Float
	size      geom.Size
	// leaving says it is on its way out.
	leaving bool
}

func newPageFrame(pages *Pages, child gunim.Node, from geom.Point) *pageFrame {
	f := &pageFrame{pages: pages, child: child, from: from, shown: anim.NewFloat(0)}
	f.Add(f.shown)
	return f
}

// motion is how the frame comes and goes: quickly, so the one going is
// out of the way of the one coming, or in turn, each in half the time.
func (f *pageFrame) motion(th *theme.Live) anim.Spring {
	if f.pages.InTurn {
		return PageTurn.Get(th)
	}
	return Quick.Get(th)
}

// Transition implements [gunim.Transitioner]. With InTurn set, a page
// coming waits until those going have faded, and it is not settled
// while it waits.
func (f *pageFrame) Transition(p gunim.Presence, fr gunim.Frame) bool {
	switch p {
	case gunim.Entering, gunim.Present:
		if f.pages.InTurn && f.shown.Target() < 1 && !f.pages.clear() {
			return false
		}
		f.shown.Animate(1, f.motion(fr.Theme))
	case gunim.Exiting:
		f.leaving = true
		f.shown.Animate(0, f.motion(fr.Theme))
	}
	return !f.shown.Active()
}

// Children implements [gunim.Composite].
func (f *pageFrame) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node]: the page, offset towards where it
// comes from while it arrives, and towards where it goes as it leaves.
func (f *pageFrame) Layout(c gunim.Constraints, fr gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	f.size = k.Layout(c)
	away := (1 - min(max(f.shown.Value(), 0), 1)) * PageSlide.Get(fr.Theme)
	side := f.from
	if f.leaving {
		side = f.out
	}
	k.Place(geom.Pt(side.X*away, side.Y*away))
	return f.size
}

// Paint implements [gunim.Node].
func (f *pageFrame) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	if t := min(max(f.shown.Value(), 0), 1); t < 0.999 {
		if f.leaving && !f.pages.InTurn {
			// Going, it fades sooner than it moves, so its words are
			// mostly gone before the next page's settle over them.
			t *= t
		}
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t})()
	}
	kids.At(0).Paint(p)
}
