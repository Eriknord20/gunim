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
	// Overlaps with them and with the parts inside them are allowed, texts' too.
	Over []gunim.Node
	// Backdrops are parts that take keys and lie under the rest on purpose, as a graph that fills the view. They count
	// as no control, and texts are still checked over them.
	Backdrops []gunim.Node
	// Hidden are parts kept out of sight on purpose, as a knob the mode set does not use. They and the parts inside
	// them are not checked.
	Hidden []gunim.Node
	// Frames is how many frames of a sixtieth of a second run before the view is checked. Zero runs 30.
	Frames int
}

// Fits lays view out and paints it at size in an offscreen window, and fails t for each thing wrong, naming the parts:
//
//   - two controls' boxes overlap;
//   - a control lies partly outside the view;
//   - a text runs partly outside the view, out of a control or over another text;
//   - a text is cut short: clipped, faded where it is cut, or ended with an ellipsis;
//   - a shape, a ribbon, a picture or another drawing lies more than 3 px outside the view;
//   - a part is smaller than its least size, or squeezed to nothing: a [plugui.Knob] under its natural size, a
//     [widget.Shrinker] under its least width, or a part in f.Least under the size given there;
//   - a control or a part in f.Least is in the tree and never painted, outside a view that scrolls.
//
// A control is a node that can take the keyboard ([gunim.Focusable]), or a part in f.Least. One that holds another
// control under it in the tree is a container, and only its parts are checked. A part inside a view that scrolls
// ([gunim.Revealer]) counts only where it shows, and a text may cross the scrolling view's edge. A text may reach 2 px
// past a control's box, as a glyph's room does. A text's box is worked out from its glyphs' places and its size, so
// it may be off by part of a glyph at its end.
//
// Fits cannot see:
//
//   - a part that takes no keys and is not in f.Least lying outside the view or over another part;
//   - a curve or a meter squeezed out of shape, or a part drawn over another without text;
//   - text drawn in a picture;
//   - any state but the one the test sets up, so set the longest values and labels by hand to check they fit;
//   - much of a view that embeds a [plug.Room] and is shown under its least size, as its parts count only where they
//     show, so check such a size with [UnderLeast].
func Fits(t testing.TB, view gunim.Node, size geom.Size, f Fit) {
	t.Helper()
	for _, m := range Misfits(t, view, size, f) {
		t.Errorf("at %v×%v: %s", size.W, size.H, m)
	}
}

// UnderLeast lays out and paints a view made by view at size, under its least size least, and fails t where it lays
// its parts out other than at least, draws anything outside its box, or does not scroll: where the wheel does not
// move it, gliding, a drag on empty ground does not move it, or Tab to a control out of sight leaves it out of sight.
func UnderLeast(t testing.TB, view func() gunim.Node, size, least geom.Size) {
	t.Helper()
	defer scrollsUnderLeast(t, view, size)
	want := map[string]placed{}
	for _, p := range check(t, view(), least, Fit{}).laid {
		want[p.path] = p
	}
	c := check(t, view(), size, Fit{})
	for _, m := range c.out {
		if m.Kind == Spills {
			t.Errorf("at %v×%v: %s", size.W, size.H, m)
		}
	}
	wrong, both := 0, 0
	for _, p := range c.laid {
		w, ok := want[p.path]
		if !ok {
			continue
		}
		both++
		if p.name == w.name && within(p.at, w.at, 0.5) && within(w.at, p.at, 0.5) {
			continue
		}
		wrong++
		if wrong <= 5 {
			t.Errorf("at %v×%v %s lies at %v; at the least size %v×%v, %s lies at %v", size.W, size.H, p.name, p.at,
				least.W, least.H, w.name, w.at)
		}
	}
	if wrong > 5 {
		t.Errorf("at %v×%v, %d parts in all lie other than at the least size", size.W, size.H, wrong)
	}
	if both == 0 && len(want) > 0 {
		t.Errorf("at %v×%v the view draws none of the parts it draws at its least size %v×%v", size.W, size.H,
			least.W, least.H)
	}
}

// Misfit is one thing wrong with a view, as [Fits] finds it.
type Misfit struct {
	Kind MisfitKind
	// Text says what is wrong, naming the parts.
	Text string
}

// String returns m's Text.
func (m Misfit) String() string { return m.Text }

// MisfitKind is what sort of thing is wrong with a view.
type MisfitKind uint8

// The kinds of misfit.
const (
	// Overlaps is two controls, two texts, or a text and a control over each other.
	Overlaps MisfitKind = iota
	// Outside is a control lying partly outside the view.
	Outside
	// Spills is a text or a drawing outside the view.
	Spills
	// CutShort is a text clipped, faded where it is cut, or ended with an ellipsis.
	CutShort
	// Squeezed is a part under its least size.
	Squeezed
	// Unpainted is a control or a part in [Fit.Least] that is in the tree and never painted.
	Unpainted
)

// Misfits returns what [Fits] fails t for, without failing it.
func Misfits(t testing.TB, view gunim.Node, size geom.Size, f Fit) []Misfit {
	t.Helper()
	return check(t, view, size, f).out
}

// check lays view out and paints it at size, and returns what it found.
func check(t testing.TB, view gunim.Node, size geom.Size, f Fit) *checker {
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
	c := &checker{fit: f, box: geom.Rect{Max: size.Point()}}
	if pr.u == nil {
		c.fail(Outside, "the view was never laid out")
		return c
	}
	c.u, c.frame = pr.u, pr.f
	if _, ok := c.u.Bounds(view); !ok {
		c.fail(Unpainted, "the view was never painted")
		return c
	}
	c.walk(view, nil, "", false)
	c.checkParts()
	c.checkOps(w.Offscreen().Ops())
	return c
}

// mount returns an offscreen window of size with n mounted in it, painting every frame, as the checks read where
// each node was drawn and the pointer hits what was painted.
func mount(t testing.TB, size geom.Size, n gunim.Node) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, size, nil)
	w.PaintWhenLooked(false)
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
	n  gunim.Node
	at geom.Rect
	// whole is where the part is laid out, at where it shows.
	whole   geom.Rect
	name    string
	control bool
	over    bool
	up      []gunim.Node
}

// placed is a node as it was drawn: its place in the tree, its name and its box.
type placed struct {
	path string
	name string
	at   geom.Rect
}

// checker gathers what is wrong with a view.
type checker struct {
	u     *gunim.UI
	frame gunim.Frame
	fit   Fit
	box   geom.Rect
	parts []part
	// laid is every node under the view that was drawn, in the tree's order.
	laid []placed
	out  []Misfit
	// scrolls are where views that scroll show, which cut what they hold at their edges.
	scrolls []geom.Rect
}

func (c *checker) fail(k MisfitKind, format string, args ...any) {
	c.out = append(c.out, Misfit{Kind: k, Text: fmt.Sprintf(format, args...)})
}

// roomer is a view that says the size it laid itself out at, as one that embeds a [plug.Room] does.
type roomer interface {
	Laid() geom.Size
}

// scrolls reports whether a, a view that scrolls, at bounds, holds more than shows: a [roomer] laid out at its box
// holds no more.
func scrolls(a gunim.Node, bounds geom.Rect) bool {
	r, ok := a.(roomer)
	if !ok {
		return true
	}
	laid := r.Laid()
	return laid.W > bounds.Size().W+0.5 || laid.H > bounds.Size().H+0.5
}

// walk notes n and the nodes under it, each with the nodes above it; path is n's place in the tree.
func (c *checker) walk(n gunim.Node, up []gunim.Node, path string, over bool) {
	if slices.Contains(c.fit.Hidden, n) {
		return
	}
	at, ok := c.u.Bounds(n)
	if !ok {
		c.checkUnpainted(n, up)
		return
	}
	if at.Empty() {
		if least, ok := c.leastOf(n); ok && least != (geom.Size{}) {
			c.fail(Squeezed, "%s is squeezed to %v×%v, under its least size %v×%v", nameOf(n), at.Size().W,
				at.Size().H, least.W, least.H)
		}
		return
	}
	if len(up) > 0 {
		c.laid = append(c.laid, placed{path: path, name: nameOf(n), at: at})
	}
	over = over || slices.Contains(c.fit.Over, n)
	whole := at
	// A part in a view that scrolls shows only within it; one scrolled out of sight is left out.
	for _, a := range up {
		if _, ok := a.(gunim.Revealer); !ok {
			continue
		}
		if sa, ok := c.u.Bounds(a); ok && scrolls(a, sa) {
			at = intersect(at, sa)
			c.scrolls = append(c.scrolls, sa)
		}
	}
	if !at.Empty() {
		control := len(up) > 0 && !slices.Contains(c.fit.Backdrops, n) && c.isControl(n)
		c.parts = append(c.parts, part{n: n, at: at, whole: whole, name: nameOf(n), control: control, over: over,
			up: up})
	}
	if co, ok := n.(gunim.Composite); ok {
		up = append(slices.Clip(up), n)
		for i, k := range co.Children() {
			c.walk(k, up, fmt.Sprintf("%s/%d", path, i), over)
		}
	}
}

// isControl reports whether n takes the keyboard or is a part in f.Least.
func (c *checker) isControl(n gunim.Node) bool {
	_, least := c.fit.Least[n]
	_, focusable := n.(gunim.Focusable)
	return least || focusable
}

// checkUnpainted names n, a node that was not drawn, where it is a control or holds one, unless a view above it
// scrolls and may have scrolled it out of sight.
func (c *checker) checkUnpainted(n gunim.Node, up []gunim.Node) {
	if len(up) == 0 {
		return
	}
	for _, a := range up {
		if _, ok := a.(gunim.Revealer); !ok {
			continue
		}
		if sa, ok := c.u.Bounds(a); ok && scrolls(a, sa) {
			return
		}
	}
	held := c.controlsIn(n)
	switch {
	case held == 0 && !c.isControl(n):
	case held == 0:
		c.fail(Unpainted, "%s is never painted", nameOf(n))
	default:
		c.fail(Unpainted, "%s, which holds %d controls, is never painted", nameOf(n), held)
	}
}

// controlsIn counts the controls under n, leaving out the parts hidden on purpose.
func (c *checker) controlsIn(n gunim.Node) int {
	co, ok := n.(gunim.Composite)
	if !ok {
		return 0
	}
	count := 0
	for _, k := range co.Children() {
		if slices.Contains(c.fit.Hidden, k) {
			continue
		}
		if c.isControl(k) {
			count++
		}
		count += c.controlsIn(k)
	}
	return count
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
	// A control with another under it, as a pane that takes keys, is a container: its parts are checked, not it.
	for i, p := range c.parts {
		for _, q := range c.parts {
			if p.control && q.control && slices.Contains(q.up, p.n) {
				c.parts[i].control = false
				break
			}
		}
	}
	for i, p := range c.parts {
		if p.control && !within(p.at, c.box, slack) {
			c.fail(Outside, "%s at %v lies partly outside the view", p.name, p.at)
		}
		if least, ok := c.leastOf(p.n); ok && (p.whole.Size().W < least.W-slack || p.whole.Size().H < least.H-slack) {
			c.fail(Squeezed, "%s is %v×%v, under its least size %v×%v", p.name, p.whole.Size().W, p.whole.Size().H,
				least.W, least.H)
		}
		if !p.control || p.over {
			continue
		}
		for _, q := range c.parts[i+1:] {
			if !q.control || q.over || slices.Contains(q.up, p.n) || slices.Contains(p.up, q.n) {
				continue
			}
			if o := intersect(p.at, q.at); o.Size().W > 1 && o.Size().H > 1 {
				c.fail(Overlaps, "%s at %v and %s at %v overlap", p.name, p.at, q.name, q.at)
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
		default:
			r, ok := drawn(op)
			if !ok || top.opacity < 0.05 {
				continue
			}
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
		c.fail(Spills, "%d drawings are drawn outside the view, within %v", spills, spilt)
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
				c.fail(Overlaps, "text %q at %v and text %q at %v overlap", a.text, a.at, b.text, b.at)
			}
		}
	}
}

// drawn returns the box op draws in, in the window's space, for an op other than a text or a layer.
func drawn(op paint.Op) (geom.Rect, bool) {
	switch o := op.(type) {
	case *paint.RRectOp:
		return transformed(o.Transform, o.Rect), true
	case *paint.ImageOp:
		return transformed(o.Transform, o.Rect), true
	case *paint.MaskOp:
		return transformed(o.Transform, o.Rect), true
	case *paint.SceneOp:
		return transformed(o.Transform, o.Rect), true
	case *paint.CellsOp:
		return transformed(o.Transform, geom.Rect{Min: o.At, Max: o.At.Add(o.Size.Point())}), true
	case *paint.RibbonOp:
		if len(o.Points) == 0 {
			return geom.Rect{}, false
		}
		var r geom.Rect
		for i, pt := range o.Points {
			w := pt.Width / 2
			b := geom.Rect{Min: geom.Pt(pt.At.X-w, pt.At.Y-w), Max: geom.Pt(pt.At.X+w, pt.At.Y+w)}
			if i == 0 {
				r = b
			} else {
				r = r.Union(b)
			}
		}
		return transformed(o.Transform, r), true
	}
	return geom.Rect{}, false
}

// checkRun checks one text: that it is whole, inside the view, and inside or clear of each control.
func (c *checker) checkRun(o *paint.TextOp, r run, l layer) {
	whole := transformed(o.Transform, glyphBox(o))
	switch {
	case l.clips && !within(whole, l.clip, 1) && !c.atScrollEdge(whole):
		c.fail(CutShort, "text %q at %v is cut short by a clip at %v", r.text, whole, l.clip)
	case o.FadeTo > o.FadeFrom:
		c.fail(CutShort, "text %q at %v is cut short, fading out", r.text, whole)
	case strings.Contains(r.text, "…"):
		c.fail(CutShort, "text %q at %v is cut short with an ellipsis", r.text, whole)
	}
	if !within(r.at, c.box, 1) {
		c.fail(Spills, "text %q at %v runs outside the view", r.text, r.at)
	}
	if c.underOver(r.at) {
		return
	}
	for _, p := range c.parts {
		if !p.control || p.over {
			continue
		}
		if o := intersect(r.at, p.at); o.Size().W > 2 && o.Size().H > 2 && !within(r.at, p.at, 2) {
			c.fail(Overlaps, "text %q at %v runs into %s at %v", r.text, r.at, p.name, p.at)
		}
	}
}

// atScrollEdge reports whether r crosses the edge of a view that scrolls, which cuts what it holds there.
func (c *checker) atScrollEdge(r geom.Rect) bool {
	for _, sa := range c.scrolls {
		if !intersect(r, sa).Empty() && !within(r, sa, 1) {
			return true
		}
	}
	return false
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
	last := o.Glyphs[len(o.Glyphs)-1]
	r.Max.X += advance(last, o.Size)
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

// spelled maps each face's glyphs back to the characters a view's labels are written in, and advances to how far
// each glyph moves the pen at a size of 1.
var (
	spelledMu sync.Mutex
	spelled   = map[uint32]map[uint32]rune{}
	advances  = map[uint32]map[uint32]float32{}
)

// advance returns how far g moves the pen at size: as measured where [spell] knows it, and a broad guess where not.
func advance(g paint.Glyph, size float32) float32 {
	spelledMu.Lock()
	defer spelledMu.Unlock()
	learn(g.Face)
	if a, ok := advances[g.Face][g.ID]; ok {
		return a * size
	}
	return 0.6 * size
}

// learn maps face's glyphs back to the characters that make them, once. It runs with spelledMu held.
func learn(id uint32) {
	if _, ok := spelled[id]; ok {
		return
	}
	m, adv := map[uint32]rune{}, map[uint32]float32{}
	if face, ok := text.Lookup(id); ok {
		for _, ch := range spellable {
			if r := face.Shape(string(ch), 100); len(r.Glyphs) == 1 && r.Glyphs[0].Face == id {
				if _, taken := m[r.Glyphs[0].ID]; !taken {
					m[r.Glyphs[0].ID], adv[r.Glyphs[0].ID] = ch, r.Advance/100
				}
			}
		}
	}
	spelled[id], advances[id] = m, adv
}

// spellable are the characters [spell] reads back.
const spellable = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~" +
	"°±µ×÷–—−…·•→←↑↓♯♭åäöÅÄÖéü"

// spell returns the text o draws, as near as its glyphs tell: "?" for a glyph none of the spellable characters makes.
func spell(o *paint.TextOp) string {
	spelledMu.Lock()
	defer spelledMu.Unlock()
	var b strings.Builder
	for _, g := range o.Glyphs {
		learn(g.Face)
		if ch, ok := spelled[g.Face][g.ID]; ok {
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
