package plugtest

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// Fit names what [Fits] cannot learn from a view by itself.
type Fit struct {
	// Least gives parts the helper cannot size, as a meter or a graph, a least size each must have. A part named
	// here counts as a control in the checks for overlaps and room.
	Least map[gunim.Node]geom.Size
	// Over are parts that lie over others on purpose, as a card that opens over the panel or a pot set in a meter.
	// Overlaps with them and with the parts inside them are allowed.
	Over []gunim.Node
	// Frames is how many frames of a sixtieth of a second run before the view is checked. Zero runs 30.
	Frames int
}

// Fits lays view out and paints it at size in an offscreen window, and fails t for each thing wrong, naming the parts:
//
//   - two controls' boxes overlap;
//   - a control lies partly outside the view;
//   - a text runs partly outside the view, out of a control or over another text;
//   - a text is cut short: clipped, faded where it is cut, or ended with an ellipsis;
//   - a shape is drawn more than 3 px outside the view;
//   - a part is smaller than its least size: a [plugui.Knob] under its natural size, a [widget.Shrinker] under its
//     least width, or a part in f.Least under the size given there.
//
// A control is a node that can take the keyboard ([gunim.Focusable]). A text's box is worked out from its glyphs'
// places and its size, so it may be off by part of a glyph at its end. Fits cannot see a curve or a meter squeezed
// out of shape, a part drawn over another without text, or text drawn in a picture.
func Fits(t testing.TB, view gunim.Node, size geom.Size, f Fit) {
	t.Helper()
	for _, m := range Misfits(t, view, size, f) {
		t.Errorf("at %v×%v: %s", size.W, size.H, m)
	}
}

// Misfits returns what [Fits] fails t for, without failing it.
func Misfits(t testing.TB, view gunim.Node, size geom.Size, f Fit) []string {
	t.Helper()
	pr := &probe{kid: view}
	w := mount(t, size, pr)
	frames := f.Frames
	if frames <= 0 {
		frames = 30
	}
	for range frames {
		w.Frame(time.Second / 60)
	}
	if pr.u == nil {
		return []string{"the view was never laid out"}
	}
	c := checker{u: pr.u, frame: pr.f, fit: f, box: geom.Rect{Max: size.Point()}}
	c.walk(view, nil, false)
	c.checkParts()
	c.checkOps(w.Offscreen().Ops())
	return c.out
}

// mount returns an offscreen window of size with n mounted in it.
func mount(t testing.TB, size geom.Size, n gunim.Node) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "plugtest", func(struct{}) gunim.Node { return n }, nil)
	if err := w.Client().Mount(gunim.Root, "plugtest", "plugtest", nil); err != nil {
		t.Fatal(err)
	}
	return w
}

// probe holds the view under test, and keeps the UI and the frame it is laid out in.
type probe struct {
	kid gunim.Node
	u   *gunim.UI
	f   gunim.Frame
}

// Children implements [gunim.Composite].
func (p *probe) Children() []gunim.Node { return []gunim.Node{p.kid} }

// Layout implements [gunim.Node]: the view fills the window.
func (p *probe) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	p.u, p.f = f.UI(), f
	kids.At(0).Layout(gunim.Tight(c.Max))
	kids.At(0).Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (p *probe) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(pt)
}

// part is a node drawn in the view: where, its name, the parts it lies in, and whether it lies over others on purpose.
type part struct {
	n       gunim.Node
	at      geom.Rect
	name    string
	control bool
	over    bool
	up      []gunim.Node
}

// checker gathers what is wrong with a view.
type checker struct {
	u     *gunim.UI
	frame gunim.Frame
	fit   Fit
	box   geom.Rect
	parts []part
	out   []string
}

func (c *checker) fail(format string, args ...any) {
	c.out = append(c.out, fmt.Sprintf(format, args...))
}

// walk notes n and the nodes under it that were drawn, each with the nodes above it.
func (c *checker) walk(n gunim.Node, up []gunim.Node, over bool) {
	at, ok := c.u.Bounds(n)
	if !ok || at.Empty() {
		return
	}
	over = over || slices.Contains(c.fit.Over, n)
	_, least := c.fit.Least[n]
	fo, focusable := n.(gunim.Focusable)
	control := len(up) > 0 && (least || focusable && fo != nil)
	c.parts = append(c.parts, part{n: n, at: at, name: nameOf(n), control: control, over: over, up: up})
	if co, ok := n.(gunim.Composite); ok {
		up = append(slices.Clip(up), n)
		for _, k := range co.Children() {
			c.walk(k, up, over)
		}
	}
}

// nameOf names n for a message: its accessible name, and its type.
func nameOf(n gunim.Node) string {
	typ := reflect.TypeOf(n).String()
	if a, ok := n.(gunim.Accessible); ok {
		if name := a.Access().Name; name != "" {
			return fmt.Sprintf("%q (%s)", name, typ)
		}
	}
	return typ
}

// checkParts checks the parts' room, sizes and overlaps.
func (c *checker) checkParts() {
	const slack = 0.5
	for i, p := range c.parts {
		if p.control && !within(p.at, c.box, slack) {
			c.fail("%s at %v lies partly outside the view", p.name, p.at)
		}
		if least, ok := c.leastOf(p.n); ok && (p.at.Size().W < least.W-slack || p.at.Size().H < least.H-slack) {
			c.fail("%s is %v×%v, under its least size %v×%v", p.name, p.at.Size().W, p.at.Size().H, least.W, least.H)
		}
		if !p.control || p.over {
			continue
		}
		for _, q := range c.parts[i+1:] {
			if !q.control || q.over || slices.Contains(q.up, p.n) || slices.Contains(p.up, q.n) {
				continue
			}
			if o := intersect(p.at, q.at); o.Size().W > 1 && o.Size().H > 1 {
				c.fail("%s at %v and %s at %v overlap", p.name, p.at, q.name, q.at)
			}
		}
	}
}

// leastOf returns n's least size, where it has one.
func (c *checker) leastOf(n gunim.Node) (geom.Size, bool) {
	if s, ok := c.fit.Least[n]; ok {
		return s, true
	}
	switch v := n.(type) {
	case *plugui.Knob:
		if v.Compact {
			return geom.Sz(plugui.CompactW, plugui.CompactH), true
		}
		return geom.Sz(plugui.KnobW, plugui.KnobH), true
	case widget.Shrinker:
		return geom.Sz(v.MinWidth(c.frame), 0), true
	}
	return geom.Size{}, false
}

// layer is an open layer as the ops are read: where it clips, if it does, and how opaque it leaves what it holds.
type layer struct {
	clip    geom.Rect
	clips   bool
	opacity float32
}

// run is a text drawn, in the window's space.
type run struct {
	text string
	at   geom.Rect
}

// checkOps checks what the view drew: its texts and its shapes.
func (c *checker) checkOps(ops []paint.Op) {
	stack := []layer{{opacity: 1}}
	var runs []run
	var spilt geom.Rect
	spills := 0
	for _, op := range ops {
		top := stack[len(stack)-1]
		switch o := op.(type) {
		case *paint.LayerOp:
			l := layer{clip: top.clip, clips: top.clips, opacity: top.opacity * o.Opts.Opacity}
			if o.Opts.Clip {
				r := transformed(o.Transform, o.Opts.Bounds)
				if l.clips {
					r = intersect(r, l.clip)
				}
				l.clip, l.clips = r, true
			}
			stack = append(stack, l)
		case *paint.LayerEndOp:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case *paint.TextOp:
			if top.opacity < 0.05 || o.Color.A < 13 || len(o.Glyphs) == 0 {
				continue
			}
			r := run{text: spell(o), at: transformed(o.Transform, glyphBox(o))}
			if top.clips {
				r.at = intersect(r.at, top.clip)
			}
			if r.at.Empty() {
				continue
			}
			c.checkRun(o, r, top)
			runs = append(runs, r)
		case *paint.RRectOp:
			if top.opacity < 0.05 {
				continue
			}
			r := transformed(o.Transform, o.Rect)
			if top.clips {
				r = intersect(r, top.clip)
			}
			if !r.Empty() && !within(r, c.box, 3) {
				if spills == 0 {
					spilt = r
				}
				spilt = spilt.Union(r)
				spills++
			}
		}
	}
	if spills > 0 {
		c.fail("%d shapes are drawn outside the view, within %v", spills, spilt)
	}
	for i, a := range runs {
		if c.underOver(a.at) {
			continue
		}
		for _, b := range runs[i+1:] {
			if c.underOver(b.at) {
				continue
			}
			if o := intersect(a.at, b.at); o.Size().W > 1 && o.Size().H > 1 {
				c.fail("text %q at %v and text %q at %v overlap", a.text, a.at, b.text, b.at)
			}
		}
	}
}

// checkRun checks one text: that it is whole, inside the view, and inside or clear of each control.
func (c *checker) checkRun(o *paint.TextOp, r run, l layer) {
	whole := transformed(o.Transform, glyphBox(o))
	switch {
	case l.clips && !within(whole, l.clip, 1):
		c.fail("text %q at %v is cut short by a clip at %v", r.text, whole, l.clip)
	case o.FadeTo > o.FadeFrom:
		c.fail("text %q at %v is cut short, fading out", r.text, whole)
	case strings.Contains(r.text, "…"):
		c.fail("text %q at %v is cut short with an ellipsis", r.text, whole)
	}
	if !within(r.at, c.box, 1) {
		c.fail("text %q at %v runs outside the view", r.text, r.at)
	}
	if c.underOver(r.at) {
		return
	}
	for _, p := range c.parts {
		if !p.control || p.over {
			continue
		}
		if o := intersect(r.at, p.at); o.Size().W > 1 && o.Size().H > 1 && !within(r.at, p.at, 1) {
			c.fail("text %q at %v runs into %s at %v", r.text, r.at, p.name, p.at)
		}
	}
}

// underOver reports whether a text at r touches a part that lies over others on purpose: the part's own text, or
// text it covers.
func (c *checker) underOver(r geom.Rect) bool {
	for _, p := range c.parts {
		if p.over && !intersect(r, p.at).Empty() {
			return true
		}
	}
	return false
}

// glyphBox is the box a text's glyphs cover in its own space, from the first glyph's start to an end a glyph past
// the last, and from its ascent to its descent.
func glyphBox(o *paint.TextOp) geom.Rect {
	g0 := o.Glyphs[0].At
	r := geom.Rect{Min: g0, Max: g0}
	for _, g := range o.Glyphs {
		r.Min.X, r.Max.X = min(r.Min.X, g.At.X), max(r.Max.X, g.At.X)
		r.Min.Y, r.Max.Y = min(r.Min.Y, g.At.Y), max(r.Max.Y, g.At.Y)
	}
	r.Max.X += 0.55 * o.Size
	r.Min.Y -= 0.72 * o.Size
	r.Max.Y += 0.2 * o.Size
	return r
}

// transformed returns r through t, as the box round its corners.
func transformed(t paint.Transform, r geom.Rect) geom.Rect {
	var out geom.Rect
	for i, q := range []geom.Point{r.Min, {X: r.Max.X, Y: r.Min.Y}, r.Max, {X: r.Min.X, Y: r.Max.Y}} {
		p := t.Apply(q)
		if i == 0 {
			out = geom.Rect{Min: p, Max: p}
			continue
		}
		out.Min.X, out.Min.Y = min(out.Min.X, p.X), min(out.Min.Y, p.Y)
		out.Max.X, out.Max.Y = max(out.Max.X, p.X), max(out.Max.Y, p.Y)
	}
	return out
}

// within reports whether r lies inside in, allowing slack on each side.
func within(r, in geom.Rect, slack float32) bool {
	return r.Min.X >= in.Min.X-slack && r.Min.Y >= in.Min.Y-slack && r.Max.X <= in.Max.X+slack &&
		r.Max.Y <= in.Max.Y+slack
}

// spelled maps each face's glyphs back to the characters a view's labels are written in.
var (
	spelledMu sync.Mutex
	spelled   = map[uint32]map[uint32]rune{}
)

// spellable are the characters [spell] reads back.
const spellable = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~" +
	"°±µ×÷–—−…·•→←↑↓♯♭åäöÅÄÖéü"

// spell returns the text o draws, as near as its glyphs tell: "?" for a glyph none of the spellable characters makes.
func spell(o *paint.TextOp) string {
	spelledMu.Lock()
	defer spelledMu.Unlock()
	var b strings.Builder
	for _, g := range o.Glyphs {
		m, ok := spelled[g.Face]
		if !ok {
			m = map[uint32]rune{}
			if face, ok := text.Lookup(g.Face); ok {
				for _, ch := range spellable {
					if r := face.Shape(string(ch), 12); len(r.Glyphs) == 1 && r.Glyphs[0].Face == g.Face {
						if _, taken := m[r.Glyphs[0].ID]; !taken {
							m[r.Glyphs[0].ID] = ch
						}
					}
				}
			}
			spelled[g.Face] = m
		}
		if ch, ok := m[g.ID]; ok {
			b.WriteRune(ch)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}

// intersect returns the part of a and b both cover, empty where they miss.
func intersect(a, b geom.Rect) geom.Rect {
	r := geom.Rect{Min: geom.Pt(max(a.Min.X, b.Min.X), max(a.Min.Y, b.Min.Y)),
		Max: geom.Pt(min(a.Max.X, b.Max.X), min(a.Max.Y, b.Max.Y))}
	if r.Empty() {
		return geom.Rect{}
	}
	return r
}
