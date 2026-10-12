package install

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// page is one step of the installer under the icon: a column of items
// from the top, and a row of buttons along the bottom. Its items arrive
// one after another, each rising into place as it fades in, and the
// page as a whole sinks and fades as it leaves.
type page struct {
	anim.Group
	// name is which page this is, as the scene says.
	name string
	// items are stacked from the top, gaps the space over each, and foot
	// is pinned to the bottom, or nil.
	items []gunim.Node
	gaps  []float32
	foot  gunim.Node
	// width is the widest the items get.
	width float32
	// step is the line that says what the work does now, on the page
	// that shows the work.
	step gunim.Node
	// fill, the last of the items when set, takes the height the others
	// and the foot leave, and notes is what an update changed, when the
	// page shows it.
	fill  gunim.Node
	notes *notesBox
	// status says how a check for updates went, and check is the button
	// that starts one, on the page about the program.
	status  *widget.Label
	check   *widget.Button
	trouble bool

	// in runs from 0 to 1 as the page arrives, and back as it leaves.
	in *anim.Float
	// since is when the page first painted, for the items' stagger;
	// staggering says the stagger still runs.
	since      time.Time
	staggering bool
	// at is where each item was placed.
	at map[gunim.Node]geom.Point
	// short is how far the items ran into the room the foot keeps, at
	// the last layout: what the stage takes off the icon to fit them.
	short float32
}

// stagger is how far apart the items start arriving, and arrival how
// long each takes.
const (
	stagger = 55 * time.Millisecond
	arrival = 420 * time.Millisecond
)

func newPage(name string, width float32) *page {
	p := &page{name: name, width: width, in: anim.NewFloat(0), at: map[gunim.Node]geom.Point{}, staggering: true}
	p.Add(p.in)
	return p
}

// add puts n under the items, gap below the one before.
func (p *page) add(gap float32, n gunim.Node) {
	p.items = append(p.items, n)
	p.gaps = append(p.gaps, gap)
}

// Children implements [gunim.Composite].
func (p *page) Children() []gunim.Node {
	kids := append([]gunim.Node(nil), p.items...)
	if p.foot != nil {
		kids = append(kids, p.foot)
	}
	return kids
}

// Transition implements [gunim.Transitioner].
func (p *page) Transition(pr gunim.Presence, f gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		p.in.Animate(1, widget.Settle.Get(f.Theme))
	case gunim.Exiting:
		p.in.Animate(0, anim.Tween{Duration: 220 * time.Millisecond, Ease: anim.EaseOut})
	default:
		// A page that is simply there, or simply gone, animates nothing.
	}
	return !p.in.Active()
}

// Step implements [gunim.Animator]: the springs, and the stagger while
// it runs.
func (p *page) Step(dt time.Duration) bool {
	moving := p.Group.Step(dt)
	return moving || p.staggering
}

// margin is the space left at the page's sides and under its foot,
// fillGap the space between the item that fills and the foot, and
// footGap the least space between the other items and the foot.
const (
	margin  = 28
	fillGap = 18
	footGap = 12
)

// Layout implements [gunim.Node].
func (p *page) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	w := min(box.W-2*margin, p.width)
	x := (box.W - w) / 2
	wide := gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, box.H)}
	y := float32(0)
	footTop := box.H - margin
	for i := range kids.Len() {
		k := kids.At(i)
		if n := k.Node(); n == p.foot {
			size := k.Layout(wide)
			at := geom.Pt(x, box.H-size.H-margin+6)
			k.Place(at)
			p.at[n] = at
			footTop = at.Y
		}
	}
	for i := range kids.Len() {
		k := kids.At(i)
		n := k.Node()
		if n == p.foot {
			continue
		}
		gap := float32(0)
		for j, it := range p.items {
			if it == n {
				gap = p.gaps[j]
			}
		}
		y += gap
		if n == p.fill {
			h := max(footTop-fillGap-y, 0)
			k.Layout(gunim.Tight(geom.Sz(w, h)))
			at := geom.Pt(x, y)
			k.Place(at)
			p.at[n] = at
			y += h
			continue
		}
		size := k.Layout(wide)
		at := geom.Pt(x, y)
		k.Place(at)
		p.at[n] = at
		y += size.H
	}
	p.short = 0
	if p.foot != nil {
		p.short = max(y+footGap-footTop, 0)
	}
	return box
}

// Paint implements [gunim.Node].
func (p *page) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if p.since.IsZero() {
		p.since = f.Now
	}
	in := min(max(p.in.Value(), 0), 1)
	if in <= 0.001 {
		return
	}
	elapsed := f.Now.Sub(p.since)
	defer pt.Push(paint.Translate(geom.Pt(0, 14*(1-in))))()
	staggering := false
	for i := range kids.Len() {
		k := kids.At(i)
		t := float32(1)
		if k.Node() != p.foot {
			t = float32(elapsed-time.Duration(i)*stagger) / float32(arrival)
		} else {
			t = float32(elapsed-time.Duration(len(p.items))*stagger) / float32(arrival)
		}
		if t < 1 {
			staggering = true
		}
		t = anim.EaseOut(min(max(t, 0), 1))
		alpha := in * t
		if alpha <= 0.001 {
			continue
		}
		at := p.at[k.Node()]
		r := geom.Rect{Min: at, Max: at.Add(k.Size().Point())}.Inset(geom.Uniform(-12))
		func() {
			if alpha < 0.999 {
				defer pt.Layer(paint.LayerOpts{Bounds: r, Opacity: alpha})()
			}
			defer pt.Push(paint.Translate(geom.Pt(0, 18*(1-t))))()
			k.Paint(pt)
		}()
	}
	p.staggering = staggering
}
