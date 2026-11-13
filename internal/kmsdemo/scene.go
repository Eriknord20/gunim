// Package kmsdemo is the test pattern that example/kmsdemo shows and
// tools/kmsbench measures: a slow colour sweep, a spinning ring and a
// bouncing ball, a grid with 1-pixel lines at the edges, a live graph of
// frame times, the figures once a second, and a dot under each finger
// with a fading trail and its slot number.
package kmsdemo

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// View is the name the scene registers under.
const View = "kmsdemo"

// State is the scene's state. Still stops everything that moves, for a
// measure of the screen at rest; fingers still draw.
type State struct {
	Still bool
}

// Options are what the scene reads from outside it.
type Options struct {
	// Scale is device pixels per logical pixel, for lines one device
	// pixel wide.
	Scale float32
	// Figures returns the line of figures shown at the top, asked once a
	// second. FrameMS returns the latest frame time in milliseconds, for
	// the graph.
	Figures func() string
	FrameMS func() float64
	// Software shows a small notice that the CPU draws, as where the GPU
	// failed.
	Software bool
	// Exit is called once three fingers have been held down for three
	// seconds.
	Exit func()
}

// Register registers the scene on w.
func Register(w *gunim.Window, o Options) {
	gunim.RegisterView(w, View, func(s State) *Scene { return New(o, s) }, func(n *Scene, s State, _ *gunim.UI) { n.setStill(s.Still) })
}

// maxFingers is how many fingers the scene draws: more than a Touch
// Display 2's five.
const maxFingers = 10

// trailLen is how many places a finger's trail keeps.
const trailLen = 24

// holdToExit is how long three fingers held down end the demo.
const holdToExit = 3 * time.Second

// Scene is the test pattern.
type Scene struct {
	o     Options
	still bool
	// t is the time the scene has moved.
	t time.Duration
	// ball is where the bouncing ball is across, 0 to 1.
	ball *anim.Float
	// sinceFigures is the time since the figures were asked.
	sinceFigures time.Duration
	figures      *widget.Label
	graph        *widget.LiveGraph
	nums         [maxFingers]*widget.Label
	fingers      [maxFingers]finger
	held         time.Duration
	exited       bool
}

// finger is one finger's dot and trail.
type finger struct {
	down  bool
	at    geom.Point
	trail [trailLen]geom.Point
	n     int
	// fade runs from 1 as the finger lifts to 0.
	fade float32
}

// New returns the scene.
func New(o Options, s State) *Scene {
	if o.Scale <= 0 {
		o.Scale = 1
	}
	sc := &Scene{o: o, still: s.Still, ball: anim.NewFloat(0)}
	sc.figures = widget.NewLabel("Waiting for the first figures…")
	sc.figures.Align = text.AlignCenter
	sc.graph = widget.NewLiveGraph(100*time.Millisecond, 120)
	sc.graph.Label = func(v float64) string { return fmt.Sprintf("%.1f ms", v) }
	if o.FrameMS != nil {
		sc.graph.Sample = o.FrameMS
	}
	sc.setStill(s.Still)
	for i := range sc.nums {
		sc.nums[i] = widget.NewLabel(strconv.Itoa(i))
		sc.nums[i].Align = text.AlignCenter
	}
	return sc
}

// Children implements [gunim.Composite].
func (s *Scene) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 2+len(s.nums))
	kids = append(kids, s.figures, s.graph)
	for _, n := range s.nums {
		kids = append(kids, n)
	}
	return kids
}

// Handle implements [gunim.Handler]: each finger moves its dot.
func (s *Scene) Handle(e input.Event, _ *gunim.UI) bool {
	f, ok := e.(input.Finger)
	if !ok || f.ID < 0 || f.ID >= maxFingers {
		return false
	}
	fg := &s.fingers[f.ID]
	switch f.Phase {
	case input.FingerDown:
		*fg = finger{down: true, at: f.Pos, fade: 1}
		fg.push(f.Pos)
	case input.FingerMove:
		fg.at = f.Pos
		fg.push(f.Pos)
	case input.FingerUp:
		fg.down = false
		if f.Pos != input.Away {
			fg.at = f.Pos
		}
	}
	return true
}

// setStill stops or starts what moves. The graph stops too, as it draws
// a few frames a second while it runs.
func (s *Scene) setStill(on bool) {
	s.still = on
	s.graph.SetRunning(!on)
}

func (f *finger) push(p geom.Point) {
	copy(f.trail[1:], f.trail[:trailLen-1])
	f.trail[0] = p
	f.n = min(f.n+1, trailLen)
}

// Step implements [gunim.Animator].
func (s *Scene) Step(dt time.Duration) bool {
	moving := false
	down := 0
	for i := range s.fingers {
		f := &s.fingers[i]
		switch {
		case f.down:
			down++
			// The trail shrinks toward the dot while the finger rests.
			if f.n > 1 {
				f.push(f.at)
				moving = true
			}
		case f.fade > 0:
			f.fade = max(0, f.fade-float32(dt.Seconds()*3))
			moving = true
		}
	}
	if down >= 3 {
		s.held += dt
		moving = true
		if s.held >= holdToExit && !s.exited && s.o.Exit != nil {
			s.exited = true
			go s.o.Exit()
		}
	} else {
		s.held = 0
	}
	s.sinceFigures += dt
	if s.sinceFigures >= time.Second && s.o.Figures != nil {
		s.sinceFigures = 0
		s.figures.Text = s.o.Figures()
		if s.o.Software {
			s.figures.Text = "Drawn without the GPU · " + s.figures.Text
		}
	}
	if s.still {
		return moving
	}
	s.t += dt
	if !s.ball.Active() {
		s.ball.Animate(1-s.ball.Target(), anim.Bouncy)
	}
	s.ball.Step(dt)
	return true
}

// Layout implements [gunim.Node]: the figures along the top, the graph
// along the bottom, and each finger's number above its dot.
func (s *Scene) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	i := 0
	for kid := range kids.All {
		switch i {
		case 0:
			kid.Layout(gunim.Loose(geom.Sz(box.W-32, 80)))
			kid.Place(geom.Pt(16, 16))
		case 1:
			kid.Layout(gunim.Tight(geom.Sz(box.W-32, widget.LiveGraphHeight.Default())))
			kid.Place(geom.Pt(16, box.H-16-widget.LiveGraphHeight.Default()))
		default:
			f := s.fingers[i-2]
			kid.Layout(gunim.Tight(geom.Sz(40, 24)))
			kid.Place(f.at.Add(geom.Pt(-20, -64)))
		}
		i++
	}
	return box
}

// hue returns a colour round the colour wheel, h from 0 to 1.
func hue(h float64, a uint8) color.NRGBA {
	h -= math.Floor(h)
	c := func(o float64) uint8 {
		v := math.Abs(math.Mod(h*6+o, 6)-3) - 1
		return uint8(40 + 120*min(max(v, 0), 1))
	}
	return color.NRGBA{R: c(0), G: c(4), B: c(2), A: a}
}

// Paint implements [gunim.Node].
func (s *Scene) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	secs := s.t.Seconds()
	// A slow colour sweep, to show banding.
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(0, 0), To: geom.Pt(box.W, box.H),
		Start: hue(secs/40, 0xff), End: hue(secs/40+0.33, 0xff),
	}})
	// A grid every 80 logical pixels, and 1-pixel lines at the edges, to
	// show the scale and the turn are right: red at the left, green at
	// the top, blue at the right, yellow at the bottom.
	px := 1 / s.o.Scale
	grid := paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x30})
	for x := float32(80); x < box.W; x += 80 {
		p.RRect(geom.Rc(x, 0, px, box.H), 0, grid)
	}
	for y := float32(80); y < box.H; y += 80 {
		p.RRect(geom.Rc(0, y, box.W, px), 0, grid)
	}
	p.RRect(geom.Rc(0, 0, px, box.H), 0, paint.Solid(color.NRGBA{R: 0xff, A: 0xff}))
	p.RRect(geom.Rc(0, 0, box.W, px), 0, paint.Solid(color.NRGBA{G: 0xff, A: 0xff}))
	p.RRect(geom.Rc(box.W-px, 0, px, box.H), 0, paint.Solid(color.NRGBA{B: 0xff, A: 0xff}))
	p.RRect(geom.Rc(0, box.H-px, box.W, px), 0, paint.Solid(color.NRGBA{R: 0xff, G: 0xff, A: 0xff}))
	// A spinning ring, once every two seconds.
	r := min(box.W, box.H) * 0.18
	center := geom.Pt(box.W*0.3, box.H*0.48)
	ring := geom.Rc(center.X-r, center.Y-r, 2*r, 2*r)
	p.RRectStroke(ring, r, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 6, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xc0}})
	a := secs * math.Pi
	dot := geom.Pt(center.X+r*float32(math.Cos(a)), center.Y+r*float32(math.Sin(a)))
	p.RRect(geom.Rc(dot.X-12, dot.Y-12, 24, 24), 12, paint.Solid(color.NRGBA{R: 0xff, G: 0xc0, B: 0x40, A: 0xff}))
	// A ball bouncing across on a spring.
	br := r * 0.35
	bx := box.W*0.55 + s.ball.Value()*(box.W*0.4-2*br)
	p.RRect(geom.Rc(bx, center.Y-br, 2*br, 2*br), br, paint.Solid(color.NRGBA{R: 0x40, G: 0xd0, B: 0xff, A: 0xff}))
	// A dot under each finger, with a trail that fades.
	for i := range s.fingers {
		f := &s.fingers[i]
		if !f.down && f.fade <= 0 {
			continue
		}
		c := hue(float64(i)/maxFingers, uint8(255*f.fade))
		if f.n > 1 {
			pts := make([]paint.RibbonPoint, f.n)
			for j := range f.n {
				k := float32(f.n-j) / float32(f.n)
				pts[j] = paint.RibbonPoint{At: f.trail[j], Width: 20 * k, Color: color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(float32(c.A) * k)}}
			}
			p.Ribbon(pts)
		}
		p.RRect(geom.Rc(f.at.X-28, f.at.Y-28, 56, 56), 28, paint.Solid(c))
		p.RRectStroke(geom.Rc(f.at.X-28, f.at.Y-28, 56, 56), 28, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 2, Color: color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: uint8(255 * f.fade)}})
	}
	i := 0
	for kid := range kids.All {
		if i < 2 || s.fingers[i-2].down {
			kid.Paint(p)
		}
		i++
	}
}
