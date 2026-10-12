package widget

import (
	"image/color"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// The tone curve's tokens.
var (
	CurveFill  = theme.Color("curve.fill", color.NRGBA{R: 0x12, G: 0x14, B: 0x19, A: 0xff})
	CurveGrid  = theme.Color("curve.grid", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x14})
	CurvePoint = theme.Length("curve.point", 9)
	// CurveMotion carries the curve to a new shape set from outside, or
	// as a point goes.
	CurveMotion = theme.Spring("motion.curve", anim.Spring{Response: 0.3, Damping: 0.9})
)

// curveSamples is how many places along the curve are drawn.
const curveSamples = 97

// CurveGuide is another curve drawn faintly behind the one edited, such
// as the other channels' curves.
type CurveGuide struct {
	Points []geom.Point
	Color  color.NRGBA
}

// ToneCurve edits a curve through points, from 0 to 1 across and up, as
// a photo editor's tone curve: a monotone cubic through them, flat past
// the ends, as [MonotoneCurve] computes it.
//
// A press on the curve's box takes the point nearest across, or adds one
// there and takes that; a drag moves it, held clear of its neighbours.
// The two end points stay at the ends and move up and down. A double
// click on a point between them removes it. Each change animates: a new
// point pops in, a removed one shrinks away as the curve settles, the
// point under the pointer grows, and a curve set from outside morphs into
// its new shape.
//
// The curve takes the keyboard by Tab, its first point chosen, with a
// ring round it. Tab and Shift+Tab choose the next point and the one
// before, and leave the curve past its ends. The arrow keys glide the
// chosen point a hundredth of the way, a tenth with Shift; Delete or
// Backspace removes a point between the ends.
type ToneCurve struct {
	anim.Group
	// Color is the curve's colour; the zero value takes the theme's
	// [Accent].
	Color color.NRGBA
	// Guides are drawn faintly behind the curve.
	Guides []CurveGuide
	// MaxPoints caps the points, ends included; zero is 16.
	MaxPoints int
	// OnChange runs on the UI goroutine with the points at every step of a
	// drag or press of a key, and OnCommit as the drag ends, a point goes,
	// or the keys stop moving a point for half a second. A non-nil result
	// is sent to the application as the curve's intent.
	OnChange func(pts []geom.Point, u *gunim.UI) gunim.Intent
	OnCommit func(pts []geom.Point, u *gunim.UI) gunim.Intent

	pts []geom.Point
	// size is how large each point shows, 0 to 1, as it pops in.
	size []*anim.Float
	// gone are points removed, shrinking away.
	gone []goneDot
	// from and to are the curve's samples before and after a change, and
	// morph how far it has gone from one to the other.
	from, to [curveSamples]float32
	morph    *anim.Float
	held     int
	hover    int
	// chosen is the point the keys act on, ring how far its ring has grown, and nudge how far the point shows from
	// where it is, in the box, as it glides there after a key moved it. settle stops the wait for a commit after the
	// keys moved a point.
	chosen int
	ring   *anim.Float
	nudge  *anim.Point
	settle func()
	box    geom.Size
	// laid is set by the first layout. Points set before it show at
	// once, with no morph from the straight line.
	laid bool
}

type goneDot struct {
	at   geom.Point
	size *anim.Float
}

// NewToneCurve returns a straight curve, from black to white.
func NewToneCurve() *ToneCurve {
	c := &ToneCurve{morph: anim.NewFloat(1), held: -1, hover: -1, ring: anim.NewFloat(0), nudge: anim.NewPoint(geom.Point{})}
	c.Add(c.morph, c.ring, c.nudge)
	c.replacePoints(nil)
	c.from = c.to
	return c
}

// Points returns the curve's points.
func (c *ToneCurve) Points() []geom.Point { return slices.Clone(c.pts) }

// SetPoints sets the points, a straight curve for none, and the curve
// morphs into its new shape. Call it from a view's update function.
func (c *ToneCurve) SetPoints(pts []geom.Point, u *gunim.UI) {
	if c.held >= 0 || slices.Equal(pts, c.pts) || len(pts) == 0 && isStraight(c.pts) {
		return
	}
	c.from = c.shown()
	c.replacePoints(pts)
	// Before the first layout, or with no UI, the curve takes its shape at once.
	if !c.laid || u == nil {
		c.from = c.to
		c.morph.Jump(1)
		return
	}
	c.morph.Jump(0)
	c.morph.Animate(1, CurveMotion.Get(u.Theme()))
	u.Invalidate()
}

func isStraight(pts []geom.Point) bool {
	return len(pts) == 2 && pts[0] == geom.Pt(0, 0) && pts[1] == geom.Pt(1, 1)
}

// replacePoints takes pts, or the straight line, and its samples.
func (c *ToneCurve) replacePoints(pts []geom.Point) {
	if len(pts) < 2 {
		pts = []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 1}}
	}
	c.pts = slices.Clone(pts)
	c.Remove(stepperList(c.size)...)
	c.size = c.size[:0]
	for range c.pts {
		f := anim.NewFloat(1)
		c.size = append(c.size, f)
		c.Add(f)
	}
	c.sample()
}

func stepperList(fs []*anim.Float) []anim.Stepper {
	out := make([]anim.Stepper, len(fs))
	for i, f := range fs {
		out[i] = f
	}
	return out
}

// sample computes the curve's samples from its points.
func (c *ToneCurve) sample() {
	for i := range c.to {
		c.to[i] = MonotoneCurve(c.pts, float32(i)/(curveSamples-1))
	}
}

// shown returns the samples drawn now, on the way from one shape to the
// next.
func (c *ToneCurve) shown() [curveSamples]float32 {
	t := c.morph.Value()
	var out [curveSamples]float32
	for i := range out {
		out[i] = c.from[i] + (c.to[i]-c.from[i])*t
	}
	return out
}

// Step implements [gunim.Animator]: points that went are let go once
// they have shrunk away.
func (c *ToneCurve) Step(dt time.Duration) bool {
	busy := c.Group.Step(dt)
	c.gone = slices.DeleteFunc(c.gone, func(g goneDot) bool {
		g.size.Step(dt)
		return !g.size.Active()
	})
	return busy || len(c.gone) > 0
}

// plot returns where the curve is drawn in the box.
func (c *ToneCurve) plot() geom.Rect {
	pad := float32(6)
	return geom.Rect{Min: geom.Pt(pad, pad), Max: geom.Pt(c.box.W-pad, c.box.H-pad)}
}

// toBox and fromBox turn a point on the curve into the box and back.
func (c *ToneCurve) toBox(p geom.Point) geom.Point {
	r := c.plot()
	return geom.Pt(r.Min.X+p.X*r.Size().W, r.Max.Y-p.Y*r.Size().H)
}

func (c *ToneCurve) fromBox(p geom.Point) geom.Point {
	r := c.plot()
	return geom.Pt(max(0, min((p.X-r.Min.X)/max(r.Size().W, 1), 1)), max(0, min((r.Max.Y-p.Y)/max(r.Size().H, 1), 1)))
}

// near returns the point within reach of p, in the box, or -1.
func (c *ToneCurve) near(p geom.Point) int {
	best, bestD := -1, float32(12*12)
	for i, q := range c.pts {
		b := c.toBox(q)
		if d := (b.X-p.X)*(b.X-p.X) + (b.Y-p.Y)*(b.Y-p.Y); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// gapX is how close across two points may come.
const gapX = 0.02

func (c *ToneCurve) maxPoints() int {
	if c.MaxPoints > 0 {
		return c.MaxPoints
	}
	return 16
}

// Focusable implements [gunim.Focusable].
func (c *ToneCurve) Focusable() bool { return true }

// keyStep is how far an arrow key moves a point, and keySettle how long the keys rest before the move commits.
const (
	keyStep   = 0.01
	keySettle = 500 * time.Millisecond
)

// key acts on a key press, and reports whether it was one of the curve's keys.
func (c *ToneCurve) key(k input.KeyPress, u *gunim.UI) bool {
	if k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) || c.held >= 0 {
		return false
	}
	th := u.Theme()
	c.chosen = min(max(c.chosen, 0), len(c.pts)-1)
	step := float32(keyStep)
	if k.Mods.Has(input.ModShift) {
		step *= 10
	}
	var d geom.Point
	switch k.Key {
	case input.KeyTab:
		next := c.chosen + 1
		if k.Mods.Has(input.ModShift) {
			next = c.chosen - 1
		}
		if next < 0 || next >= len(c.pts) {
			return false // the keyboard moves on
		}
		c.chosen = next
		u.Invalidate()
		return true
	case input.KeyDelete, input.KeyBackspace:
		i := c.chosen
		if i <= 0 || i >= len(c.pts)-1 {
			return false
		}
		c.removePoint(i, th)
		c.chosen = min(i, len(c.pts)-1)
		c.commitNow(u)
		u.Invalidate()
		return true
	case input.KeyLeft:
		d.X = -step
	case input.KeyRight:
		d.X = step
	case input.KeyUp:
		d.Y = step
	case input.KeyDown:
		d.Y = -step
	default:
		return false
	}
	was := c.toBox(c.pts[c.chosen])
	from := c.shown()
	c.held = c.chosen
	q := c.pts[c.chosen].Add(d)
	c.moveHeld(geom.Pt(clamp01(q.X), clamp01(q.Y)))
	c.held = -1
	// The curve and the point glide to their new place.
	c.from = from
	c.morph.Jump(0)
	c.morph.Animate(1, Quick.Get(th))
	c.nudge.Jump(c.nudge.Value().Add(was.Sub(c.toBox(c.pts[c.chosen]))))
	c.nudge.Animate(geom.Point{}, Quick.Get(th))
	if c.OnChange != nil {
		send(u, c, c.OnChange(c.Points(), u))
	}
	if c.settle != nil {
		c.settle()
	}
	c.settle = u.After(keySettle, c.commitNow)
	u.Invalidate()
	return true
}

// commitNow sends OnCommit, and ends the wait for one.
func (c *ToneCurve) commitNow(u *gunim.UI) {
	if c.settle != nil {
		c.settle()
		c.settle = nil
	}
	if c.OnCommit != nil {
		send(u, c, c.OnCommit(c.Points(), u))
	}
}

// Handle implements [gunim.Handler].
func (c *ToneCurve) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.KeyPress:
		return c.key(e, u)
	case input.FocusGained:
		c.chosen = 0
		return false
	case input.FocusRing:
		c.ring.Animate(ringTo(e), RingFade.Get(th))
		return true
	case input.FocusLost:
		c.ring.Animate(0, RingFade.Get(th))
		// A move the keys made and that has yet to commit commits as the keyboard leaves.
		if c.settle != nil {
			c.commitNow(u)
		}
		return true
	case input.PointerMove:
		if c.held >= 0 {
			c.moveHeld(c.fromBox(e.Pos))
			if c.OnChange != nil {
				send(u, c, c.OnChange(c.Points(), u))
			}
			u.Invalidate()
			return true
		}
		if h := c.near(e.Pos); h != c.hover {
			c.hover = h
			u.Invalidate()
		}
		return false
	case input.PointerLeave:
		if c.held < 0 && c.hover >= 0 {
			c.hover = -1
			u.Invalidate()
		}
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		i := c.near(e.Pos)
		if e.Clicks == 2 && i > 0 && i < len(c.pts)-1 {
			c.removePoint(i, th)
			if c.OnCommit != nil {
				send(u, c, c.OnCommit(c.Points(), u))
			}
			u.Invalidate()
			return true
		}
		if i < 0 {
			i = c.addPoint(c.fromBox(e.Pos), th)
			if i < 0 {
				return true
			}
		}
		c.held, c.hover = i, i
		c.moveHeld(c.fromBox(e.Pos))
		if c.OnChange != nil {
			send(u, c, c.OnChange(c.Points(), u))
		}
		u.Invalidate()
		return true
	case input.PointerUp:
		if c.held < 0 {
			return false
		}
		c.held = -1
		if c.OnCommit != nil {
			send(u, c, c.OnCommit(c.Points(), u))
		}
		u.Invalidate()
		return true
	}
	return false
}

// addPoint adds a point at p, between the points either side of it, and
// returns its index, or -1 where there is no room.
func (c *ToneCurve) addPoint(p geom.Point, th *theme.Live) int {
	if len(c.pts) >= c.maxPoints() {
		return -1
	}
	i, _ := slices.BinarySearchFunc(c.pts, p.X, func(q geom.Point, x float32) int {
		switch {
		case q.X < x:
			return -1
		case q.X > x:
			return 1
		}
		return 0
	})
	if i <= 0 || i >= len(c.pts) || p.X-c.pts[i-1].X < gapX || c.pts[i].X-p.X < gapX {
		return -1
	}
	c.pts = slices.Insert(c.pts, i, p)
	f := anim.NewFloat(0)
	f.Animate(1, Bounce.Get(th))
	c.size = slices.Insert(c.size, i, f)
	c.Add(f)
	return i
}

// removePoint removes point i, which shrinks away as the curve settles
// without it.
func (c *ToneCurve) removePoint(i int, th *theme.Live) {
	g := goneDot{at: c.pts[i], size: anim.NewFloat(1)}
	g.size.Animate(0, Quick.Get(th))
	c.gone = append(c.gone, g)
	c.Remove(c.size[i])
	c.from = c.shown()
	c.pts = slices.Delete(c.pts, i, i+1)
	c.size = slices.Delete(c.size, i, i+1)
	c.hover = -1
	c.sample()
	c.morph.Jump(0)
	c.morph.Animate(1, CurveMotion.Get(th))
}

// moveHeld moves the point held to p: an end only up and down, one
// between them no nearer its neighbours across than gapX.
func (c *ToneCurve) moveHeld(p geom.Point) {
	i := c.held
	switch {
	case i == 0:
		p.X = c.pts[0].X
	case i == len(c.pts)-1:
		p.X = c.pts[i].X
	default:
		p.X = max(c.pts[i-1].X+gapX, min(p.X, c.pts[i+1].X-gapX))
	}
	c.pts[i] = p
	// A drag shows the curve as it is, at once.
	c.sample()
	c.from = c.to
	c.morph.Jump(1)
}

// Layout implements [gunim.Node]: square, as wide as it is given.
func (c *ToneCurve) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	w := cs.Max.W
	if w <= 0 {
		w = FieldWidth.Get(f.Theme)
	}
	c.box = cs.Constrain(geom.Sz(w, w))
	c.laid = true
	return c.box
}

// Paint implements [gunim.Node].
func (c *ToneCurve) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	p.RRect(geom.Rect{Max: box.Point()}, 6, paint.Solid(CurveFill.Get(th)))
	r := c.plot()
	grid := CurveGrid.Get(th)
	for _, q := range []float32{0.25, 0.5, 0.75} {
		x, y := r.Min.X+q*r.Size().W, r.Min.Y+q*r.Size().H
		p.RRect(geom.Rc(x-0.5, r.Min.Y, 1, r.Size().H), 0, paint.Solid(grid))
		p.RRect(geom.Rc(r.Min.X, y-0.5, r.Size().W, 1), 0, paint.Solid(grid))
	}
	// The straight line, dotted, for reference.
	for i := 0; i < 40; i += 2 {
		a := c.toBox(geom.Pt(float32(i)/40, float32(i)/40))
		polyline(p, []geom.Point{a, c.toBox(geom.Pt(float32(i+1)/40, float32(i+1)/40))}, 1, grid)
	}
	for _, g := range c.Guides {
		ink := g.Color
		ink.A = uint8(float32(ink.A) * 0.35)
		polyline(p, c.line(sampleCurve(g.Points)), 1.25, ink)
	}
	ink := c.Color
	if ink.A == 0 {
		ink = Accent.Get(th)
	}
	polyline(p, c.line(c.shown()), 2, ink)
	dot := CurvePoint.Get(th)
	for _, g := range c.gone {
		paintDot(p, c.toBox(g.at), dot*g.size.Value(), ink, CurveFill.Get(th), false)
	}
	ring := c.ring.Value()
	for i, q := range c.pts {
		grow := float32(1)
		at := c.toBox(q)
		if i == c.chosen {
			at = at.Add(c.nudge.Value())
			grow += 0.25 * min(max(ring, 0), 1)
		}
		switch i {
		case c.held:
			grow = 1.45
		case c.hover:
			grow = max(grow, 1.25)
		}
		d := dot * grow * c.size[i].Value()
		paintDot(p, at, d, ink, CurveFill.Get(th), i == c.held)
		if i == c.chosen {
			FocusRing(p, geom.Rc(at.X-d/2, at.Y-d/2, d, d), d/2, ring, th)
		}
	}
}

// line returns samples as points in the box.
func (c *ToneCurve) line(s [curveSamples]float32) []geom.Point {
	out := make([]geom.Point, len(s))
	for i, y := range s {
		out[i] = c.toBox(geom.Pt(float32(i)/(curveSamples-1), max(0, min(y, 1))))
	}
	return out
}

func sampleCurve(pts []geom.Point) [curveSamples]float32 {
	if len(pts) < 2 {
		pts = []geom.Point{{X: 0, Y: 0}, {X: 1, Y: 1}}
	}
	var out [curveSamples]float32
	for i := range out {
		out[i] = MonotoneCurve(pts, float32(i)/(curveSamples-1))
	}
	return out
}

// paintDot draws a curve's point: a ring, filled while it is held.
func paintDot(p *paint.Painter, at geom.Point, d float32, ink, fill color.NRGBA, held bool) {
	if d < 0.5 {
		return
	}
	r := geom.Rc(at.X-d/2, at.Y-d/2, d, d)
	if held {
		fill = ink
	}
	p.RRectStroke(r, d/2, paint.Solid(fill), paint.Stroke{Width: 1.75, Color: ink})
}

// polyline draws a line through pts, as short bars from point to point.
func polyline(p *paint.Painter, pts []geom.Point, width float32, ink color.NRGBA) {
	for i := 1; i < len(pts); i++ {
		bar(p, pts[i-1], pts[i], width, ink)
	}
}

// MonotoneCurve returns the curve through pts at x: a monotone cubic
// Hermite spline with Fritsch–Carlson tangents, which never overshoots
// between points, and flat past the first and the last. The points are
// sorted across, from 0 to 1.
func MonotoneCurve(pts []geom.Point, x float32) float32 {
	n := len(pts)
	switch {
	case n == 0:
		return x
	case n == 1 || x <= pts[0].X:
		return clamp01(pts[0].Y)
	case x >= pts[n-1].X:
		return clamp01(pts[n-1].Y)
	}
	d := make([]float64, n-1)
	for i := range d {
		if dx := float64(pts[i+1].X - pts[i].X); dx > 0 {
			d[i] = float64(pts[i+1].Y-pts[i].Y) / dx
		}
	}
	m := make([]float64, n)
	m[0], m[n-1] = d[0], d[n-2]
	for i := 1; i < n-1; i++ {
		if d[i-1]*d[i] > 0 {
			m[i] = (d[i-1] + d[i]) / 2
		}
	}
	for i := range d {
		if d[i] == 0 {
			m[i], m[i+1] = 0, 0
			continue
		}
		a, b := m[i]/d[i], m[i+1]/d[i]
		if h := a*a + b*b; h > 9 {
			t := 3 / math.Sqrt(h)
			m[i], m[i+1] = t*a*d[i], t*b*d[i]
		}
	}
	seg := 0
	for seg < n-2 && x > pts[seg+1].X {
		seg++
	}
	h := float64(pts[seg+1].X - pts[seg].X)
	t := (float64(x) - float64(pts[seg].X)) / h
	t2, t3 := t*t, t*t*t
	y := (2*t3-3*t2+1)*float64(pts[seg].Y) + (t3-2*t2+t)*h*m[seg] + (-2*t3+3*t2)*float64(pts[seg+1].Y) + (t3-t2)*h*m[seg+1]
	return clamp01(float32(y))
}

func clamp01(v float32) float32 { return max(0, min(v, 1)) }
