package widget

import (
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// minAcross is the fewest samples a live graph's width shows, so the
// first few are not drawn a width apart.
const minAcross = 20

// LiveGraphHeight is a live graph's height.
var LiveGraphHeight = theme.Length("livegraph.height", 56)

// LiveGraphCompactHeight is a compact live graph's height, as in a row
// or beside a switch.
var LiveGraphCompactHeight = theme.Length("livegraph.compact.height", 14)

// LiveGraphHead is the colour of the dot round the newest sample, under the accent at its middle.
var LiveGraphHead = theme.Color("livegraph.head", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})

// LiveGraph draws a value as it runs, such as the speed of a copy: a
// smooth curve over a glowing fill, sliding left every frame as samples
// come in, with a bright head on the newest and what it says beside it.
// The scale glides to the highest value in view rather than jumping.
//
// Samples come with [LiveGraph.Add], about one every Every. The head
// stays at the right edge and runs a little behind the newest sample,
// so the curve slides past it at an even pace however unevenly samples
// come, and a new sample bends the curve into its value over a few
// frames rather than at once. The graph rests while it is not running.
type LiveGraph struct {
	// Every is how often a sample comes.
	Every time.Duration
	// Span is how many samples fit across.
	Span int
	// Label is what the head says for a value, such as "12 MB/s"; nil
	// says nothing.
	Label func(v float64) string
	// Compact draws the graph small, for a row or beside a switch:
	// [LiveGraphCompactHeight] tall, with no room for a label and a
	// smaller head. It takes its room from the start, so what is beside
	// it does not move as the first samples come.
	Compact bool
	// Width, when set, is how wide the graph is; unset, it takes the
	// width it is given.
	Width float32
	// Ink, when set, colours the line, the fill and the head in place
	// of [Accent], as for a state's colour.
	Ink theme.Token[color.NRGBA]
	// Floor is the least the scale reaches up to, so values far below
	// it draw low: a connection's keepalives, beside the floor of a
	// busy one, are not drawn full height.
	Floor float64
	// Sample, when set, is asked for a sample every Every while the
	// graph runs, as the speed a meter shows, in place of calling Add.
	Sample func() float64
	// sinceSample is the time since Sample was last asked.
	sinceSample time.Duration

	samples []float64
	// shown is the value drawn at each sample, which glides to the
	// sample's weighed value when a new sample changes that, and vel
	// how fast it moves.
	shown, vel []float64
	// pos is where the head is, as a sample's index and how far on to
	// the next; speed is how many samples a second it moves, and pace
	// the speed it heads for.
	pos, speed, pace float64
	running          bool
	// stepped is when Step last ran: the graph draws at liveRate while
	// it runs, the window sleeping between, and moves on by the time
	// that passed.
	stepped time.Time
	// u is the window's UI, kept from Layout for its clock.
	u *gunim.UI
	// painted says the graph was painted since the last step: one out of
	// sight asks for no frames.
	painted bool
	// glow is the head's pulse, and the time the graph has run.
	glow float64
	// said is the value the label says, which changes once a second at
	// most so it can be read, and saidAt the glow it last changed at.
	said, saidAt float64
	top          *anim.Float
	// across is how many samples the width shows, gliding from few,
	// which fill it while there are few, to Span.
	across *anim.Float
	text   shapedText
	ell    shapedText
}

// headSlack is how many samples behind the newest the head aims to be
// as the next one comes, so a sample a little late does not stop it.
const headSlack = 0.5

// settle is how a sample's drawn value glides to a new one.
var settle = anim.Spring{Response: 0.3, Damping: 1}

// NewLiveGraph returns a graph of span samples, one every every.
func NewLiveGraph(every time.Duration, span int) *LiveGraph {
	return &LiveGraph{Every: every, Span: span, top: anim.NewFloat(0), across: anim.NewFloat(float32(minAcross))}
}

// Add adds a sample, which slides in from the right.
func (g *LiveGraph) Add(v float64) {
	v = max(0, v)
	from := v
	if n := len(g.shown); n > 0 {
		// The newest drawn value stands in for the samples after it, so
		// starting the new one there changes nothing on screen yet.
		from = g.shown[n-1]
	}
	g.samples = append(g.samples, v)
	g.shown = append(g.shown, from)
	g.vel = append(g.vel, 0)
	if n := len(g.samples) - g.Span - 2; n > 0 {
		g.samples, g.shown, g.vel = g.samples[n:], g.shown[n:], g.vel[n:]
		g.pos = max(0, g.pos-float64(n))
	}
	// Head for the newest sample, to be headSlack short of it by the
	// time the next one should come.
	lag := float64(len(g.samples)-1) - g.pos
	g.pace = min(max(lag-headSlack, 0), 4) / g.every()
	g.across.Animate(float32(min(max(len(g.samples), minAcross), g.Span)), anim.Gentle)
	most := 0.0
	for _, s := range g.samples {
		most = max(most, s)
	}
	if top := max(most*1.15, g.Floor); top > 0 {
		g.top.Animate(float32(top), anim.Gentle)
	}
	if len(g.samples) == 1 || g.glow-g.saidAt >= 1 {
		g.said, g.saidAt = g.recent(), g.glow
	}
}

// SetRunning starts the graph sliding, or rests it.
func (g *LiveGraph) SetRunning(on bool) { g.running = on }

// every is Every in seconds.
func (g *LiveGraph) every() float64 {
	if g.Every <= 0 {
		return 0.2
	}
	return g.Every.Seconds()
}

// weighed is sample k weighed with its neighbours, so a rate taken ten
// times a second reads as a line rather than as noise.
func (g *LiveGraph) weighed(k int) float64 {
	n := len(g.samples)
	raw := func(k int) float64 { return g.samples[min(max(k, 0), n-1)] }
	return (raw(k-1) + 2*raw(k) + raw(k+1)) / 4
}

// Step implements [gunim.Animator]: while running, the graph moves on
// by the time that passed, drawn at liveRate as WakeIn asks, and once
// stopped, every frame until the head reaches the newest sample and
// every value has settled. A graph out of sight, on a page not shown,
// lets the window rest, and catches up once painted again. The engine's
// step of no time, after a frame is painted, leaves the mark for the
// next frame's.
func (g *LiveGraph) Step(dt time.Duration) bool {
	moving := g.advance(dt) && g.painted
	if dt > 0 {
		g.painted = false
	}
	return moving
}

// advance moves the graph on by dt, and reports whether it moves on.
func (g *LiveGraph) advance(dt time.Duration) bool {
	now := g.u.Clock()
	if !g.stepped.IsZero() {
		// The window slept between the graph's frames: the time that
		// passed, rather than a refresh's.
		if slept := now.Sub(g.stepped); slept > dt && slept < time.Second {
			dt = slept
		}
	}
	g.stepped = now
	moving := g.top.Step(dt)
	if g.across.Step(dt) {
		moving = true
	}
	for k := range g.shown {
		to := g.weighed(k)
		if g.shown[k] == to && g.vel[k] == 0 {
			continue
		}
		g.shown[k], g.vel[k] = settle.Follow(g.shown[k], g.vel[k], to, dt)
		if tol := 1e-4 * (1 + math.Abs(to)); math.Abs(g.shown[k]-to) < tol && math.Abs(g.vel[k]) < 10*tol {
			g.shown[k], g.vel[k] = to, 0
		} else {
			moving = true
		}
	}
	if g.running && g.Sample != nil {
		// Asked for its own samples, as often as they come.
		every := time.Duration(g.every() * float64(time.Second))
		for g.sinceSample += dt; g.sinceSample >= every; g.sinceSample -= every {
			g.Add(g.Sample())
			moving = true
		}
	}
	newest := float64(len(g.samples) - 1)
	if g.running {
		// The speed eases to the pace, so the slide never lurches.
		g.speed += (g.pace - g.speed) * (1 - math.Exp(-dt.Seconds()/0.15))
		g.pos = min(g.pos+g.speed*dt.Seconds(), max(newest, 0))
		g.glow += dt.Seconds()
		// Not moving: WakeIn draws it again at liveRate, rather than
		// every refresh, as a rate sampled a few times a second asks
		// for no more.
	} else if newest > 0 && (g.pos != newest || g.speed != 0) {
		// Stopped, the head coasts onto the newest sample.
		g.pos, g.speed = anim.Gentle.Follow(g.pos, g.speed, newest, dt)
		g.pos = min(g.pos, newest)
		if math.Abs(newest-g.pos) < 1e-3 && math.Abs(g.speed) < 1e-2 {
			g.pos, g.speed = newest, 0
		} else {
			moving = true
		}
	}
	return moving
}

// liveRate is how often a running graph draws.
const liveRate = time.Second / 30

// WakeIn implements [gunim.Waker]: a running graph draws at liveRate,
// while it is painted.
func (g *LiveGraph) WakeIn() time.Duration {
	if !g.running || !g.painted {
		return 0
	}
	return liveRate
}

// Layout implements [gunim.Node]: nothing until there are two samples.
func (g *LiveGraph) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	g.u = f.UI()
	w := c.Max.W
	if g.Width > 0 {
		w = g.Width
	}
	if g.Compact {
		return c.Constrain(geom.Sz(w, LiveGraphCompactHeight.Get(f.Theme)))
	}
	if len(g.samples) < 2 {
		return c.Constrain(geom.Size{})
	}
	return c.Constrain(geom.Sz(w, LiveGraphHeight.Get(f.Theme)))
}

// Paint implements [gunim.Node].
func (g *LiveGraph) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	g.painted = true
	n := len(g.samples)
	top := float64(g.top.Value())
	if box.H <= 0 || n < 2 || top <= 0 || g.Span < 2 {
		return
	}
	th := f.Theme
	accent := Accent.Get(th)
	if g.Ink.Key() != "" {
		accent = g.Ink.Get(th)
	}
	// The plot sits below room for the head's label; a compact one has
	// none.
	room := TextSize.Get(th) + 4
	if g.Compact {
		room = 2
	}
	plot := g.plot(box, room)
	xOf, yOf, valueAt, step := plot.xOf, plot.yOf, plot.valueAt, plot.step
	plotTop := plot.top
	pos := g.pos
	first := max(0, pos-float64(box.W/step)-1)
	var pts []geom.Point
	for x := max(0, xOf(first)); ; x += 2 {
		head := xOf(pos)
		if x > head {
			x = head
		}
		t := pos - float64((head-x)/step)
		pts = append(pts, geom.Pt(x, yOf(valueAt(t))))
		if x >= head {
			break
		}
	}
	// The fill, fading to nothing at the foot: columns two device
	// pixels wide, whose edges fall on device pixels, so they meet
	// without a seam or an overlap either would show as a stripe.
	fill := accent
	fill.A = 0x60
	faded := accent
	faded.A = 0
	grad := &paint.Gradient{From: geom.Pt(0, plotTop), To: geom.Pt(0, box.H), Start: fill, End: faded}
	// t maps the graph's x to the window's, in logical pixels, and dev
	// is device pixels per logical one.
	t := p.Transform()
	sx, dev := max(t.A, 1e-3), max(f.Scale, 1)
	snap := func(x float32) float32 {
		wx := sx*x + t.C
		return (float32(math.Round(float64(wx*dev)))/dev - t.C) / sx
	}
	head := xOf(pos)
	colW := 2 / (dev * sx)
	for x := snap(max(0, xOf(first))); x < head; {
		next := min(snap(x+colW), head)
		if next <= x {
			next = x + colW
		}
		mid := (x + next) / 2
		y := yOf(valueAt(pos - float64((head-mid)/step)))
		p.RRect(geom.Rect{Min: geom.Pt(x, y), Max: geom.Pt(next, box.H)}, 0, paint.Fill{Gradient: grad})
		x = next
	}
	// The line, a short stroke between each point and the next.
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		d := b.Sub(a)
		length := float32(math.Hypot(float64(d.X), float64(d.Y)))
		if length <= 0 {
			continue
		}
		angle := float32(math.Atan2(float64(d.Y), float64(d.X)))
		func() {
			defer p.Push(paint.Rotate(angle, a))()
			p.RRect(geom.Rc(a.X, a.Y-0.75, length+0.5, 1.5), 0.75, paint.Solid(accent))
		}()
	}
	// The foot.
	foot := Ink.Get(th)
	foot.A = 0x20
	p.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(foot))
	// The head: a dot on the newest, and a ring breathing out of it
	// while the graph runs.
	headAt := pts[len(pts)-1]
	dot, reach := float32(3), float32(7)
	if g.Compact {
		dot, reach = 2, 3
	}
	if g.running {
		pulse := float32(math.Mod(g.glow, 1.2) / 1.2)
		ring := accent
		ring.A = uint8(0x90 * (1 - pulse))
		r := dot + reach*pulse
		p.RRect(geom.Rc(headAt.X-r, headAt.Y-r, 2*r, 2*r), r, paint.Solid(ring))
	}
	p.RRect(geom.Rc(headAt.X-dot, headAt.Y-dot, 2*dot, 2*dot), dot, paint.Solid(LiveGraphHead.Get(th)))
	p.RRect(geom.Rc(headAt.X-dot+1, headAt.Y-dot+1, 2*dot-2, 2*dot-2), dot-1, paint.Solid(accent))
	if g.Label != nil {
		// Over the plot, at the end the samples come in at, and cut to the box.
		run := fitRun(g.text.shape(faceIn(Font, th), g.Label(g.said), TextSize.Get(th)*0.85), &g.ell, box.W)
		ink := Ink.Get(th)
		ink.A = 0xc0
		run.Paint(p, geom.Pt(max(0, box.W-run.Advance), 0), ink)
	}
}

// livePlot maps a graph's samples to where they are drawn.
type livePlot struct {
	// top is the plot's top, and step the width between samples.
	top, step float32
	xOf       func(i float64) float32
	yOf       func(v float64) float32
	// valueAt is the curve's value at a sample's index, with a fraction.
	valueAt func(t float64) float64
}

// plot returns where g draws in box, its plot starting at top.
func (g *LiveGraph) plot(box geom.Size, top float32) livePlot {
	n := len(g.samples)
	most := float64(g.top.Value())
	plotH := box.H - top - 1
	step := box.W / max(float32(g.across.Value())-1, 1)
	// The head stays at the right edge, the dot on it wholly inside.
	headX := box.W - 3
	return livePlot{
		top:  top,
		step: step,
		xOf:  func(i float64) float32 { return headX - float32(g.pos-i)*step },
		yOf:  func(v float64) float32 { return top + plotH*(1-float32(min(v/most, 1))) },
		// A Catmull-Rom curve through the drawn values, so the line bends
		// smoothly between them.
		valueAt: func(t float64) float64 {
			i := int(math.Floor(t))
			u := t - float64(i)
			at := func(k int) float64 { return g.shown[min(max(k, 0), n-1)] }
			p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
			v := 0.5 * (2*p1 + (-p0+p2)*u + (2*p0-5*p1+4*p2-p3)*u*u + (-p0+3*p1-3*p2+p3)*u*u*u)
			return max(0, v)
		},
	}
}

// head returns where g's head is drawn in box, its plot starting at top.
func (g *LiveGraph) head(box geom.Size, top float32) geom.Point {
	p := g.plot(box, top)
	return geom.Pt(p.xOf(g.pos), p.yOf(p.valueAt(g.pos)))
}

// recent returns the mean of the last second's samples, which holds
// still enough to read.
func (g *LiveGraph) recent() float64 {
	n := len(g.samples)
	if n == 0 {
		return 0
	}
	k := n
	if g.Every > 0 {
		k = min(n, max(1, int(time.Second/g.Every)))
	}
	sum := 0.0
	for _, v := range g.samples[n-k:] {
		sum += v
	}
	return sum / float64(k)
}
