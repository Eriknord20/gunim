package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// curve is a curve as the graph draws it. It draws itself on from the
// left as it arrives, and morphs from what it was to what it is when it
// changes: the draft curve does, as each key is typed.
type curve struct {
	id   int
	expr string
	hue  int
	f    fn
	// was is the curve it morphs from, and morph how far it has got.
	was    fn
	morph  *anim.Float
	reveal *anim.Float
	alpha  *anim.Float
	// glow flares as the curve is kept, from the draft.
	glow *anim.Float
	gone bool
}

func newCurve(id int, expr string, hueIndex int, f fn) *curve {
	c := &curve{id: id, expr: expr, hue: hueIndex, f: f, morph: anim.NewFloat(1), reveal: anim.NewFloat(0), alpha: anim.NewFloat(1), glow: anim.NewFloat(0)}
	c.reveal.Animate(1, anim.Tween{Duration: 900 * time.Millisecond})
	return c
}

// at is the curve at x, part way from what it was to what it is.
func (c *curve) at(x float64) float64 {
	m := float64(c.morph.Value())
	if c.was == nil || m >= 1 {
		return c.f(x)
	}
	return c.was(x)*(1-m) + c.f(x)*m
}

// become morphs the curve into g.
func (c *curve) become(expr string, g fn) {
	if expr == c.expr {
		return
	}
	// What it shows now, frozen, is what it morphs from.
	was, f, m := c.was, c.f, float64(c.morph.Value())
	if was == nil || m >= 1 {
		c.was = f
	} else {
		c.was = func(x float64) float64 { return was(x)*(1-m) + f(x)*m }
	}
	c.expr, c.f = expr, g
	c.morph.Jump(0)
	c.morph.Animate(1, anim.Spring{Response: 0.45, Damping: 0.75})
}

func (c *curve) step(dt time.Duration) bool {
	moving := false
	for _, a := range []*anim.Float{c.morph, c.reveal, c.alpha, c.glow} {
		if a.Step(dt) {
			moving = true
		}
	}
	return moving
}

// draftID is the draft curve's id: no kept curve has it.
const draftID = -1

// graphBody is the graph: the plot, and beside it the curves kept, the
// line being typed, and a few keys.
type graphBody struct {
	anim.Group
	r      *calcRoot
	canvas *canvas
	keys   *keypad
	expr   *roll
	chips  []*chip
	// side is the panel beside the plot, in the body's space.
	side geom.Rect
	// shake throws the line being typed sideways when a sum has no
	// curve, why says why, and errors counts the sums that had none.
	shake  *anim.Float
	why    *notice
	errors int
	// full grows the plot over the whole graph, from 0 to 1, as the
	// button in its corner asks, and fill is that button.
	full *anim.Float
	fill *widget.IconButton
	// plot is where the plot was laid out, in the body's space.
	plot geom.Rect
}

// notice is a line of text that fades and rises into its place as it is
// set, and fades out as it is cleared, keeping its words while it goes.
type notice struct {
	text string
	a    *anim.Float
}

func newNotice() *notice { return &notice{a: anim.NewFloat(0)} }

// set shows s, or with s empty takes the line away.
func (n *notice) set(s string) {
	if s == "" {
		n.a.Animate(0, anim.Gentle)
		return
	}
	if s != n.text {
		// Words that change come in afresh.
		n.text = s
		n.a.Jump(0)
	}
	n.a.Animate(1, anim.Snappy)
}

// paint draws the line with its top left at at, size high, in col.
func (n *notice) paint(p *paint.Painter, at geom.Point, size float32, col color.NRGBA) {
	a := n.a.Value()
	if a <= 0.01 || n.text == "" {
		return
	}
	run := shaped(n.text, size)
	run.Paint(p, at.Add(geom.Pt(0, 8*(1-a))), paint.Faded(col, min(a, 1)))
}

// errorInk is the colour a sum with no answer is told in.
var errorInk = color.NRGBA{R: 0xff, G: 0x8a, B: 0x7a, A: 0xff}

// chip is a curve in the side panel: its colour, its sum, and a cross
// that takes it off.
type chip struct {
	id    int
	expr  string
	hue   int
	y, a  *anim.Float
	going bool
}

const chipHeight = 40

func newGraphBody(r *calcRoot) *graphBody {
	g := &graphBody{r: r, canvas: newCanvas(), expr: newRoll(26), shake: anim.NewFloat(0), why: newNotice(),
		full: anim.NewFloat(0)}
	g.Add(g.shake, g.why.a, g.full)
	g.fill = widget.NewIconButton(icon.Maximize2, "Fill the window with the plot")
	g.fill.OnClick = func(u *gunim.UI) gunim.Intent {
		g.setFull(g.full.Target() == 0, u)
		return nil
	}
	g.keys = newKeypad(8,
		[]string{"x", "^", "(", ")", "⌫"},
		[]string{"sin", "cos", "tan", "√", "C"},
		[]string{"7", "8", "9", "÷", "π"},
		[]string{"4", "5", "6", "×", "e"},
		[]string{"1", "2", "3", "−", "ln"},
		[]string{"0", ".", "%", "+", "="},
	)
	return g
}

// The graph's keypad: graphKeyRows rows graphKeyH high, which take
// graphKeysH with the gaps between them. Its digits let a phone, with no
// keys of its own, type a sum such as 0.5×sin(x).
const (
	graphKeyRows = 6
	graphKeyH    = 40
	graphKeysH   = graphKeyRows*graphKeyH + (graphKeyRows-1)*8
)

// arrive starts the curves drawing on again, as the graph opens.
func (g *graphBody) arrive() {
	for _, c := range g.canvas.curves {
		c.reveal.Jump(0)
		c.reveal.Animate(1, anim.Tween{Duration: 1100 * time.Millisecond})
	}
}

// show takes the application's state.
func (g *graphBody) show(s Calc, u *gunim.UI) {
	g.expr.set(s.Expr)
	g.why.set(s.Error)
	if s.Errors != g.errors {
		g.errors = s.Errors
		if s.Graph {
			g.shake.Jump(1)
			g.shake.Animate(0, anim.Spring{Response: 0.35, Damping: 0.2})
		}
	}
	g.canvas.show(s)
	// The chips: new ones grow in at the end, gone ones fade.
	seen := map[int]*chip{}
	for _, c := range g.chips {
		seen[c.id] = c
	}
	live := map[int]bool{}
	var chips []*chip
	for i, p := range s.Plots {
		live[p.ID] = true
		y := float32(i) * chipHeight
		c, ok := seen[p.ID]
		if !ok {
			c = &chip{id: p.ID, expr: p.Expr, hue: p.Hue, y: anim.NewFloat(y + 20), a: anim.NewFloat(0)}
			c.a.Animate(1, anim.Snappy)
		}
		c.y.Animate(y, anim.Bouncy)
		chips = append(chips, c)
	}
	for _, c := range g.chips {
		if !live[c.id] {
			c.going = true
			c.a.Animate(0, anim.Gentle)
			chips = append(chips, c)
		}
	}
	g.chips = chips
	u.Invalidate()
}

// Step implements [gunim.Animator].
func (g *graphBody) Step(dt time.Duration) bool {
	// The group holds the shake and the line saying why; the roll and
	// the chips step on their own.
	moving := g.Group.Step(dt)
	if g.expr.step(dt) {
		moving = true
	}
	live := g.chips[:0]
	for _, c := range g.chips {
		// Both step every frame: a chip fades as it moves.
		y, a := c.y.Step(dt), c.a.Step(dt)
		if y || a {
			moving = true
		}
		if c.going && !c.a.Active() {
			continue
		}
		live = append(live, c)
	}
	g.chips = live
	return moving
}

// Children implements [gunim.Composite].
func (g *graphBody) Children() []gunim.Node { return []gunim.Node{g.canvas, g.keys, g.fill} }

// fullMotion is the spring the plot grows and shrinks on.
var fullMotion = anim.Spring{Response: 0.45, Damping: 0.82}

// setFull grows the plot over the whole graph, or back to its place
// beside the panel, which slides away and back as it does.
func (g *graphBody) setFull(on bool, u *gunim.UI) {
	to := float32(0)
	g.fill.Icon, g.fill.Tooltip = icon.Maximize2, "Fill the window with the plot"
	if on {
		to = 1
		g.fill.Icon, g.fill.Tooltip = icon.Minimize2, "Bring the curves and the keys back"
	}
	g.full.Animate(to, fullMotion)
	u.Invalidate()
}

// isFull reports whether the plot fills the graph, or is on its way.
func (g *graphBody) isFull() bool { return g.full.Target() == 1 }

// Covers implements [gunim.Shaped]: the graph takes the pointer while
// it is the one showing.
func (g *graphBody) Covers(geom.Point) bool { return g.r.mode.Target() == 1 }

// sideWidth is the panel's width beside the plot, on a window wider than
// narrowWidth; on a narrower one the panel goes under the plot, as wide
// as the window.
const sideWidth = 280

// Layout implements [gunim.Node].
func (g *graphBody) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	const pad = 14
	var plot geom.Rect
	if size.W < narrowWidth {
		// A phone: the plot across the top, and the panel under it.
		plot = geom.Rc(pad, pad, max(0, size.W-2*pad), max(0, size.H*0.35))
		g.side = geom.Rc(pad, plot.Max.Y+pad, plot.Size().W, max(0, size.H-plot.Max.Y-2*pad))
	} else {
		plot = geom.Rc(pad, pad, max(0, size.W-sideWidth-3*pad), max(0, size.H-2*pad))
		g.side = geom.Rc(plot.Max.X+pad, pad, sideWidth, plot.Size().H)
	}
	// Full, the plot fills the graph, and the panel slides away the way
	// it sits from the plot: down on a phone, right on a wider window.
	t := g.full.Value()
	plot = mixRect(plot, geom.Rc(pad, pad, max(0, size.W-2*pad), max(0, size.H-2*pad)), t)
	if size.W < narrowWidth {
		g.side = g.side.Add(geom.Pt(0, t*(size.H-g.side.Min.Y+pad)))
	} else {
		g.side = g.side.Add(geom.Pt(t*(size.W-g.side.Min.X+pad), 0))
	}
	g.plot = plot
	canvas := kids.At(0)
	canvas.Layout(gunim.Tight(plot.Size()))
	canvas.Place(plot.Min)
	fill := kids.At(2)
	fs := fill.Layout(gunim.Loose(plot.Size()))
	fill.Place(geom.Pt(plot.Max.X-fs.W-8, plot.Min.Y+8))
	sideW := g.side.Size().W
	keysH := float32(graphKeysH)
	keys := kids.At(1)
	keys.Layout(gunim.Tight(geom.Sz(sideW-24, keysH)))
	keys.Place(geom.Pt(g.side.Min.X+12, g.side.Max.Y-12-keysH))
	g.expr.place(g.side.Max.X-18, sideW-36)
	return size
}

// Paint implements [gunim.Node].
func (g *graphBody) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	kids.At(0).Paint(p)
	defer kids.At(2).Paint(p)
	t := g.full.Value()
	if t >= 0.999 {
		return
	}
	// The panel fades as it slides away.
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1 - t})()
	s := g.side
	p.RRect(s, 18, paint.Solid(widget.CardFill.Get(th)))
	head := shaped("Curves", 13)
	head.Paint(p, geom.Pt(s.Min.X+18, s.Min.Y+14), paint.Faded(ink, 0.5))
	if len(g.chips) == 0 {
		hint := shaped("Type a sum of x, then =", 14)
		hint.Paint(p, geom.Pt(s.Min.X+18, s.Min.Y+42), paint.Faded(ink, 0.35))
	}
	for _, c := range g.chips {
		y := s.Min.Y + 40 + c.y.Value()
		a := c.a.Value()
		col := hue(c.hue)
		p.RRect(geom.Rc(s.Min.X+18, y+10, 12, 12), 6, paint.Solid(paint.Faded(col, a)))
		run := shaped("y = "+c.expr, 16)
		run.Paint(p, geom.Pt(s.Min.X+40, y+(chipHeight-run.Height())/2-4), paint.Faded(ink, a))
		cross := shaped("×", 16)
		cross.Paint(p, geom.Pt(s.Max.X-30, y+(chipHeight-cross.Height())/2-4), paint.Faded(ink, 0.4*a))
	}
	// The line being typed, over the keys, shaken when a sum has no
	// curve, with why over it.
	keysTop := s.Max.Y - 12 - float32(graphKeysH)
	line := geom.Rc(s.Min.X+12, keysTop-62, s.Size().W-24, 50)
	g.why.paint(p, geom.Pt(line.Min.X+6, line.Min.Y-24), 14, errorInk)
	func() {
		defer p.Push(paint.Translate(geom.Pt(14*g.shake.Value(), 0)))()
		p.RRect(line, 12, paint.Solid(paint.Faded(ink, 0.06)))
		g.expr.paint(p, line.Min.Y+10, g.expr.rightEdge(s), ink)
	}()
	kids.At(1).Paint(p)
}

// rightEdge is where the typed line ends.
func (r *roll) rightEdge(side geom.Rect) float32 { return side.Max.X - 24 }

// Handle implements [gunim.Handler]: a chip's cross takes its curve off.
func (g *graphBody) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || d.Button != input.ButtonPrimary || !g.side.Contains(d.Pos) {
		return false
	}
	for _, c := range g.chips {
		y := g.side.Min.Y + 40 + c.y.Value()
		if !c.going && d.Pos.Y >= y && d.Pos.Y < y+chipHeight && d.Pos.X > g.side.Max.X-44 {
			u.Send(g, RemovePlot{ID: c.id})
			return true
		}
	}
	return false
}

// canvas is the plot: a grid that thins and thickens smoothly as it
// zooms, the curves, and a dot tracing the curve under the pointer. A
// drag pans it and a flick coasts; the wheel zooms about the pointer,
// and two fingers about the point between them; a double click springs
// it home.
type canvas struct {
	anim.Group
	curves []*curve
	// cx, cy are the world point in the middle, and zoom log2 of the
	// pixels a unit takes.
	cx, cy, zoom *anim.Float
	// anchor is the world point the pointer was on as a zoom began, and
	// anchorAt where on the canvas, which the zoom holds still.
	anchor, anchorAt geom.Point
	zooming          bool
	size             geom.Size
	// The pointer: where it is, whether it is over, and a drag's last
	// place, time and speed.
	pointer        geom.Point
	over, dragging bool
	lastAt         geom.Point
	lastTime       time.Time
	vx, vy         float32
	// trace is the dot on the curve under the pointer, following it on
	// a spring, and traceOn how much it shows; pulse its ring's age.
	trace   *anim.Point
	traceOn *anim.Float
	traceOf int
	pulse   float64
}

// homeZoom is how close the graph starts: 40 pixels a unit.
var homeZoom = float32(math.Log2(40))

func newCanvas() *canvas {
	c := &canvas{cx: anim.NewFloat(0), cy: anim.NewFloat(0), zoom: anim.NewFloat(homeZoom),
		trace: anim.NewPoint(geom.Point{}), traceOn: anim.NewFloat(0), traceOf: math.MinInt}
	c.Add(c.cx, c.cy, c.zoom, c.trace, c.traceOn)
	return c
}

// show takes the curves kept and the draft.
func (c *canvas) show(s Calc) {
	byID := map[int]*curve{}
	for _, cv := range c.curves {
		byID[cv.id] = cv
	}
	var draft *curve
	if d, ok := byID[draftID]; ok && !d.gone {
		draft = d
	}
	keep := map[*curve]bool{}
	var curves []*curve
	for _, p := range s.Plots {
		cv, ok := byID[p.ID]
		if !ok {
			f, err := parse(p.Expr)
			if err != nil {
				continue
			}
			if draft != nil && draft.expr == p.Expr {
				// The draft is kept: it becomes the curve, and flares.
				cv, draft = draft, nil
				cv.id, cv.hue = p.ID, p.Hue
				cv.glow.Jump(1)
				cv.glow.Animate(0, anim.Tween{Duration: 900 * time.Millisecond})
			} else {
				cv = newCurve(p.ID, p.Expr, p.Hue, f)
			}
		}
		keep[cv] = true
		curves = append(curves, cv)
	}
	if s.Draft != "" {
		if f, err := parse(s.Draft); err == nil {
			if draft == nil {
				draft = newCurve(draftID, s.Draft, len(s.Plots), f)
			} else {
				draft.become(s.Draft, f)
			}
			draft.hue = len(s.Plots)
			keep[draft] = true
			curves = append(curves, draft)
		}
	}
	// Curves taken off fade away.
	for _, cv := range c.curves {
		if !keep[cv] {
			if !cv.gone {
				cv.gone = true
				cv.alpha.Animate(0, anim.Gentle)
			}
			if cv.alpha.Value() > 0.01 || cv.alpha.Active() {
				curves = append(curves, cv)
			}
		}
	}
	c.curves = curves
}

// Step implements [gunim.Animator].
func (c *canvas) Step(dt time.Duration) bool {
	moving := c.Group.Step(dt)
	for _, cv := range c.curves {
		if cv.step(dt) {
			moving = true
		}
	}
	if c.traceOn.Value() > 0.01 {
		c.pulse += dt.Seconds()
		moving = true
	}
	return moving
}

// scale is the pixels a unit takes now.
func (c *canvas) scale() float64 { return math.Exp2(float64(c.zoom.Value())) }

// toScreen and toWorld convert between the canvas and the world.
func (c *canvas) toScreen(x, y float64) geom.Point {
	s := c.scale()
	return geom.Pt(float32((x-float64(c.cx.Value()))*s)+c.size.W/2, c.size.H/2-float32((y-float64(c.cy.Value()))*s))
}

func (c *canvas) toWorld(p geom.Point) (x, y float64) {
	s := c.scale()
	return float64(c.cx.Value()) + float64(p.X-c.size.W/2)/s, float64(c.cy.Value()) - float64(p.Y-c.size.H/2)/s
}

// Layout implements [gunim.Node]. While a zoom runs, the middle moves
// with it, so the point under the pointer stays under it.
func (c *canvas) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.size = cs.Max
	if c.zooming {
		s := c.scale()
		c.cx.Jump(float32(float64(c.anchor.X) - float64(c.anchorAt.X-c.size.W/2)/s))
		c.cy.Jump(float32(float64(c.anchor.Y) + float64(c.anchorAt.Y-c.size.H/2)/s))
		c.zooming = c.zoom.Active()
	}
	// The dot stays on its curve as the curve morphs and the view moves.
	if c.over {
		c.retrace()
	}
	return c.size
}

// gridStep is the step between grid lines that puts them about target
// pixels apart: 1, 2 or 5 times a power of ten.
func gridStep(scale, target float64) float64 {
	raw := target / scale
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*mag >= raw {
			return m * mag
		}
	}
	return 10 * mag
}

// Paint implements [gunim.Node].
func (c *canvas) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	bounds := geom.Rect{Max: box.Point()}
	p.RRect(bounds, 18, paint.Solid(widget.CardFill.Get(th)))
	defer p.Layer(paint.LayerOpts{Bounds: bounds, Opacity: 1, Clip: true, Radius: 18})()
	s := c.scale()
	x0, y1 := c.toWorld(geom.Point{})
	x1, y0 := c.toWorld(box.Point())

	// The grid: the minor lines fade in as there is room for them, so
	// it thickens and thins smoothly as the zoom goes.
	major := gridStep(s, 90)
	minor := major / 5
	if minor*s > 6 {
		a := float32(min(1, (minor*s-6)/14)) * 0.05
		c.gridLines(p, minor, x0, x1, y0, y1, paint.Faded(ink, a))
	}
	c.gridLines(p, major, x0, x1, y0, y1, paint.Faded(ink, 0.11))
	// The axes, and their numbers along them.
	origin := c.toScreen(0, 0)
	axis := paint.Faded(ink, 0.35)
	if origin.X >= 0 && origin.X <= box.W {
		p.RRect(geom.Rc(origin.X-0.75, 0, 1.5, box.H), 0, paint.Solid(axis))
	}
	if origin.Y >= 0 && origin.Y <= box.H {
		p.RRect(geom.Rc(0, origin.Y-0.75, box.W, 1.5), 0, paint.Solid(axis))
	}
	labelY := min(max(origin.Y+6, 6), box.H-20)
	for x := math.Ceil(x0/major) * major; x <= x1; x += major {
		if math.Abs(x) < major/2 {
			continue
		}
		at := c.toScreen(x, 0)
		run := shaped(trimNumber(x), 11)
		run.Paint(p, geom.Pt(at.X-run.Advance/2, labelY), paint.Faded(ink, 0.45))
	}
	labelX := min(max(origin.X+6, 6), box.W-40)
	for y := math.Ceil(y0/major) * major; y <= y1; y += major {
		if math.Abs(y) < major/2 {
			continue
		}
		at := c.toScreen(0, y)
		run := shaped(trimNumber(y), 11)
		run.Paint(p, geom.Pt(labelX, at.Y-run.Height()/2), paint.Faded(ink, 0.45))
	}

	for _, cv := range c.curves {
		c.paintCurve(p, cv, box)
	}
	c.paintTrace(p, f, box)
}

// gridLines draws lines step apart across the view.
func (c *canvas) gridLines(p *paint.Painter, step, x0, x1, y0, y1 float64, col color.NRGBA) {
	for x := math.Ceil(x0/step) * step; x <= x1; x += step {
		at := c.toScreen(x, 0)
		p.RRect(geom.Rc(at.X-0.5, 0, 1, c.size.H), 0, paint.Solid(col))
	}
	for y := math.Ceil(y0/step) * step; y <= y1; y += step {
		at := c.toScreen(0, y)
		p.RRect(geom.Rc(0, at.Y-0.5, c.size.W, 1), 0, paint.Solid(col))
	}
}

// trimNumber writes an axis number short.
func trimNumber(v float64) string {
	if math.Abs(v) >= 1e5 || (math.Abs(v) < 1e-3 && v != 0) {
		return fmt.Sprintf("%.0e", v)
	}
	return format(math.Round(v*1e6) / 1e6)
}

// paintCurve draws a curve as far as it has drawn itself on, with a soft
// glow under its line.
func (c *canvas) paintCurve(p *paint.Painter, cv *curve, box geom.Size) {
	alpha := cv.alpha.Value()
	if alpha <= 0.01 {
		return
	}
	col := hue(cv.hue)
	if cv.id == draftID {
		alpha *= 0.75
	}
	upTo := box.W * min(max(cv.reveal.Value(), 0), 1)
	const step = 2
	var prev geom.Point
	have := false
	glow := cv.glow.Value()
	width := float32(2.5) + 3*glow
	for sx := float32(0); sx <= upTo; sx += step {
		x, _ := c.toWorld(geom.Pt(sx, 0))
		y := cv.at(x)
		if math.IsNaN(y) || math.IsInf(y, 0) {
			have = false
			continue
		}
		at := c.toScreen(x, y)
		// A jump off the view, as tan's, breaks the line rather than
		// drawing a wall.
		if have && abs32(at.Y-prev.Y) > box.H*1.5 {
			have = false
		}
		if have && (inY(prev.Y, box.H) || inY(at.Y, box.H)) {
			segment(p, prev, at, width+6, paint.Faded(col, 0.12*alpha))
			segment(p, prev, at, width, paint.Faded(col, alpha))
		}
		prev, have = at, true
	}
	// The pen: a bright point where the curve is drawing itself on.
	if cv.reveal.Active() && have {
		p.RRect(geom.Rc(prev.X-5, prev.Y-5, 10, 10), 5, paint.Solid(paint.Faded(col, alpha)))
		p.RRect(geom.Rc(prev.X-12, prev.Y-12, 24, 24), 12, paint.Solid(paint.Faded(col, 0.2*alpha)))
	}
}

// inY reports whether y is near enough the view to draw.
func inY(y, h float32) bool { return y > -h && y < 2*h }

// segment draws a stroke from a to b.
func segment(p *paint.Painter, a, b geom.Point, width float32, col color.NRGBA) {
	d := b.Sub(a)
	length := float32(math.Hypot(float64(d.X), float64(d.Y)))
	if length <= 0 {
		return
	}
	angle := float32(math.Atan2(float64(d.Y), float64(d.X)))
	defer p.Push(paint.Rotate(angle, a))()
	p.RRect(geom.Rc(a.X-width/2, a.Y-width/2, length+width, width), width/2, paint.Solid(col))
}

// paintTrace draws the dot on the curve under the pointer, a ring
// breathing out of it, and where it is.
func (c *canvas) paintTrace(p *paint.Painter, f gunim.Frame, box geom.Size) {
	on := c.traceOn.Value()
	if on <= 0.01 {
		return
	}
	var col color.NRGBA
	for _, cv := range c.curves {
		if cv.id == c.traceOf {
			col = hue(cv.hue)
		}
	}
	if col.A == 0 {
		return
	}
	at := c.trace.Value()
	ink := widget.Ink.Get(f.Theme)
	p.RRect(geom.Rc(at.X-0.5, 0, 1, box.H), 0, paint.Solid(paint.Faded(ink, 0.1*on)))
	pulse := float32(math.Mod(c.pulse, 1.4) / 1.4)
	ring := 6 + 16*pulse
	p.RRect(geom.Rc(at.X-ring, at.Y-ring, 2*ring, 2*ring), ring, paint.Solid(paint.Faded(col, 0.35*(1-pulse)*on)))
	p.RRect(geom.Rc(at.X-7, at.Y-7, 14, 14), 7, paint.Solid(paint.Faded(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, on)))
	p.RRect(geom.Rc(at.X-5, at.Y-5, 10, 10), 5, paint.Solid(paint.Faded(col, on)))
	x, y := c.toWorld(at)
	label := shaped(fmt.Sprintf("x = %s   y = %s", trimNumber(x), trimNumber(y)), 13)
	lx := min(max(at.X+14, 8), box.W-label.Advance-24)
	ly := min(max(at.Y-38, 8), box.H-32)
	card := geom.Rc(lx, ly, label.Advance+16, label.Height()+10)
	p.ShadowRRect(card, 8, paint.Solid(paint.Faded(widget.CardFill.Get(f.Theme), on)), paint.Shadow{Offset: geom.Pt(0, 3), Blur: 12, Color: color.NRGBA{A: uint8(0x70 * on)}})
	label.Paint(p, geom.Pt(lx+8, ly+5), paint.Faded(ink, on))
}

// retrace puts the dot on the curve nearest the pointer, at its x.
func (c *canvas) retrace() {
	if !c.over || c.dragging {
		c.traceOn.Animate(0, anim.Gentle)
		return
	}
	x, _ := c.toWorld(c.pointer)
	best, bestD := -1, float32(48)
	var at geom.Point
	for i, cv := range c.curves {
		if cv.gone {
			continue
		}
		y := cv.at(x)
		if math.IsNaN(y) || math.IsInf(y, 0) {
			continue
		}
		p := c.toScreen(x, y)
		if d := abs32(p.Y - c.pointer.Y); d < bestD {
			best, bestD, at = i, d, p
		}
	}
	if best < 0 {
		c.traceOn.Animate(0, anim.Gentle)
		return
	}
	cv := c.curves[best]
	if cv.id != c.traceOf || c.traceOn.Value() < 0.05 {
		c.trace.Jump(at)
	}
	c.traceOf = cv.id
	c.trace.Animate(at, anim.Spring{Response: 0.12, Damping: 0.8})
	c.traceOn.Animate(1, anim.Snappy)
}

// ZoomsWithWheel implements [gunim.WheelZoomer]: the wheel zooms the
// plot, with Ctrl as without.
func (c *canvas) ZoomsWithWheel() bool { return true }

// ZoomsWithPinch implements [gunim.PinchZoomer]: two fingers zoom the
// plot and pan it, smoothly.
func (c *canvas) ZoomsWithPinch() bool { return true }

// DragsTouch implements [gunim.TouchDragger]: a finger pans the plot,
// as the mouse does.
func (c *canvas) DragsTouch() bool { return c.dragging }

// Handle implements [gunim.Handler].
func (c *canvas) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		c.over, c.pointer = true, e.Pos
		c.retrace()
	case input.PointerLeave:
		c.over = false
		c.retrace()
	case input.PointerMove:
		c.pointer = e.Pos
		if c.dragging {
			dt := float32(e.Time.Sub(c.lastTime).Seconds())
			d := e.Pos.Sub(c.lastAt)
			s := float32(c.scale())
			c.cx.Jump(c.cx.Value() - d.X/s)
			c.cy.Jump(c.cy.Value() + d.Y/s)
			if dt > 0 {
				// Smoothed, so the last jerk of the hand is not the fling.
				c.vx = 0.6*c.vx + 0.4*(-d.X/s/dt)
				c.vy = 0.6*c.vy + 0.4*(d.Y/s/dt)
			}
			c.lastAt, c.lastTime = e.Pos, e.Time
		}
		c.retrace()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if e.Clicks == 2 {
			// Home, on a bounce.
			c.zooming = false
			c.zoom.Animate(homeZoom, anim.Bouncy)
			c.cx.Animate(0, anim.Bouncy)
			c.cy.Animate(0, anim.Bouncy)
			break
		}
		c.dragging, c.lastAt, c.lastTime, c.vx, c.vy = true, e.Pos, e.Time, 0, 0
		c.cx.Jump(c.cx.Value())
		c.cy.Jump(c.cy.Value())
		c.retrace()
	case input.PointerUp:
		if !c.dragging {
			return true
		}
		c.dragging = false
		// A flick coasts on.
		if time.Since(c.lastTime) < 80*time.Millisecond || e.Time.Sub(c.lastTime) < 80*time.Millisecond {
			anim.Fling(c.cx, c.vx, anim.Decay{Tau: 0.35})
			anim.Fling(c.cy, c.vy, anim.Decay{Tau: 0.35})
		}
		c.retrace()
	case input.Pinch:
		// The world point that was under the fingers goes where they are
		// now, at the new zoom.
		x, y := c.toWorld(e.Pos.Sub(e.Delta))
		c.zooming = false
		c.zoom.Jump(min(max(c.zoom.Value()+float32(math.Log2(float64(e.Scale))), 2), 14))
		s := c.scale()
		c.cx.Jump(float32(x - float64(e.Pos.X-c.size.W/2)/s))
		c.cy.Jump(float32(y + float64(e.Pos.Y-c.size.H/2)/s))
	case input.Scroll:
		n := e.Notches.Y
		if n == 0 {
			n = e.Delta.Y / 40
		}
		x, y := c.toWorld(e.Pos)
		c.anchor, c.anchorAt = geom.Pt(float32(x), float32(y)), e.Pos
		c.zooming = true
		to := min(max(c.zoom.Target()+n*0.35, 2), 14)
		c.zoom.Animate(to, anim.Spring{Response: 0.3, Damping: 0.9})
	default:
		return false
	}
	u.Invalidate()
	return true
}
