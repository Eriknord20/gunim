package main

import (
	"image/color"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// registerViews is the window half: the calculator's view.
func registerViews(w *gunim.Window) {
	gunim.RegisterView(w, "calc",
		func(Calc) *calcRoot { return newCalcRoot() },
		func(r *calcRoot, s Calc, u *gunim.UI) { r.show(s, u) })
}

// runs shapes text once for each string and size it is asked for, for
// nodes that paint text every frame. It is reached from the UI
// goroutine alone.
var runs = map[runKey]text.Run{}

type runKey struct {
	s    string
	size float32
}

// shaped is s shaped at size, from the cache.
func shaped(s string, size float32) text.Run {
	k := runKey{s, size}
	if r, ok := runs[k]; ok {
		return r
	}
	if len(runs) > 2000 {
		// Axis labels at every zoom add up; start again.
		clear(runs)
	}
	r := text.Default().Shape(s, size)
	runs[k] = r
	return r
}

// hues are the colours curves take, in turn.
var hues = []color.NRGBA{
	{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff},
	{R: 0xff, G: 0x7a, B: 0x6b, A: 0xff},
	{R: 0x4f, G: 0xd6, B: 0x9c, A: 0xff},
	{R: 0xf5, G: 0xc2, B: 0x42, A: 0xff},
	{R: 0xc4, G: 0x8b, B: 0xff, A: 0xff},
	{R: 0x3f, G: 0xd0, B: 0xe0, A: 0xff},
}

func hue(i int) color.NRGBA { return hues[i%len(hues)] }

// titleHeight is the height of the window's own title bar.
const titleHeight = 40

// calcRoot is the whole window: the title bar, and under it the
// calculator and the graph, one of which shows. Switching, the keypad
// slides away as the graph grows out of the display into the window,
// or the other way.
type calcRoot struct {
	anim.Group
	bar   *titleBar
	calc  *calcBody
	graph *graphBody
	// mode is 0 showing the calculator, 1 the graph.
	mode  *anim.Float
	state Calc
	// body is where the calculator and the graph go, and from the rect
	// the graph grows out of: the display's.
	body, from geom.Rect
	// echo pings past the window's edges: red for a sum with no answer,
	// green for a curve kept on the graph.
	echo widget.Echo
}

func newCalcRoot() *calcRoot {
	r := &calcRoot{mode: anim.NewFloat(0)}
	r.bar = newTitleBar(r)
	r.calc = newCalcBody(r)
	r.graph = newGraphBody(r)
	r.Add(r.mode)
	return r
}

// show takes the application's state.
func (r *calcRoot) show(s Calc, u *gunim.UI) {
	was := r.state
	r.state = s
	to := float32(0)
	if s.Graph {
		to = 1
	}
	if s.Graph != was.Graph || r.mode.Target() != to {
		r.mode.Animate(to, anim.Spring{Response: 0.55, Damping: 0.82})
		if s.Graph {
			r.graph.arrive()
		}
	}
	if s.Errors > was.Errors {
		r.echo.Ping(u, widget.EchoProblem)
	}
	if len(s.Plots) > 0 && !slices.ContainsFunc(was.Plots, func(p Plot) bool { return p.ID == s.Plots[len(s.Plots)-1].ID }) {
		r.echo.Ping(u, widget.EchoDone)
	}
	r.bar.show(s.Graph, u)
	r.calc.show(was, s, u)
	r.graph.show(s, u)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *calcRoot) Children() []gunim.Node { return []gunim.Node{r.bar, r.calc, r.graph} }

// Focusable implements [gunim.Focusable]: the window's keys come here.
func (r *calcRoot) Focusable() bool { return true }

// Layout implements [gunim.Node].
func (r *calcRoot) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	// The background runs under a phone's bars; the bar and the body
	// keep clear of them.
	area := geom.Rect{Max: size.Point()}.Inset(f.Safe)
	bar := kids.At(0)
	bar.Layout(gunim.Tight(geom.Sz(area.Size().W, titleHeight)))
	bar.Place(area.Min)
	r.body = geom.Rect{Min: area.Min.Add(geom.Pt(0, titleHeight)), Max: area.Max}
	for i := 1; i < 3; i++ {
		k := kids.At(i)
		k.Layout(gunim.Tight(r.body.Size()))
		k.Place(r.body.Min)
	}
	r.from = r.calc.display.Add(r.body.Min)
	return size
}

// Paint implements [gunim.Node]: the calculator slides off to the left
// and fades as the graph grows out of the display's card, until it
// fills the window.
func (r *calcRoot) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.Background.Get(th)))
	m := min(max(r.mode.Value(), -0.1), 1.1)
	if m < 1 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: r.body, Opacity: min(max(1-m*1.4, 0), 1)})()
			defer p.Push(paint.Translate(geom.Pt(-60*m, 0)))()
			defer p.Push(paint.Scale(1-0.06*m, r.body.Center()))()
			kids.At(1).Paint(p)
		}()
	}
	if m > 0 {
		// The rect the graph fills, from the display's to the body's.
		at := mixRect(r.from, r.body, m)
		sx := at.Size().W / max(r.body.Size().W, 1)
		sy := at.Size().H / max(r.body.Size().H, 1)
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: at, Opacity: min(max(m*1.6, 0), 1), Clip: true, Radius: 18 * (1 - min(m, 1))})()
			defer p.Push(paint.Transform{A: sx, C: at.Min.X - r.body.Min.X*sx, E: sy, F: at.Min.Y - r.body.Min.Y*sy})()
			kids.At(2).Paint(p)
		}()
	}
	kids.At(0).Paint(p)
}

// mixRect is the rect t of the way from a to b.
func mixRect(a, b geom.Rect, t float32) geom.Rect {
	mix := func(x, y float32) float32 { return x + (y-x)*t }
	return geom.Rect{
		Min: geom.Pt(mix(a.Min.X, b.Min.X), mix(a.Min.Y, b.Min.Y)),
		Max: geom.Pt(mix(a.Max.X, b.Max.X), mix(a.Max.Y, b.Max.Y)),
	}
}

// Handle implements [gunim.Handler]: the keyboard types, as the keypad
// does. Tab switches between the calculator and the graph.
func (r *calcRoot) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.KeyPress:
		var key string
		switch e.Key {
		case input.KeyEnter, input.KeyKPEnter:
			key = "="
		case input.KeyBackspace:
			key = "⌫"
		case input.KeyEscape:
			// Escape, as a phone's back gesture sends it, first brings a
			// plot that fills the graph back to its place.
			if r.state.Graph && r.graph.isFull() {
				r.graph.setFull(false, u)
				return true
			}
			key = "C"
		case input.KeyDelete:
			key = "C"
		case input.KeyTab:
			u.Send(r, ShowGraph{On: !r.state.Graph})
			return true
		default:
			return false
		}
		r.press(key, u)
		return true
	case input.TextInput:
		for _, c := range e.Text {
			if key, ok := typedKey(c); ok {
				r.press(key, u)
			}
		}
		return true
	}
	return false
}

// press sends a key, and lights it on whichever keypad shows.
func (r *calcRoot) press(key string, u *gunim.UI) {
	if r.state.Graph {
		r.graph.keys.flash(key, u)
	} else {
		r.calc.keypad.flash(key, u)
	}
	u.Send(r, Pressed{Key: key})
}

// typedKey is the key a character typed stands for.
func typedKey(c rune) (string, bool) {
	switch c {
	case '*':
		return "×", true
	case '/':
		return "÷", true
	case '-':
		return "−", true
	case 'p':
		return "π", true
	case 's':
		return "sin", true
	case 'c':
		return "cos", true
	case 't':
		return "tan", true
	case 'r':
		return "√", true
	case 'l':
		return "ln", true
	case '=':
		return "=", true
	}
	if strings.ContainsRune("0123456789.+^%()xeπ×÷−", c) {
		return string(c), true
	}
	return "", false
}

// titleBar is the window's own title bar: the switch between the
// calculator and the graph, the title, which moves the window, and the
// window's buttons.
type titleBar struct {
	r        *calcRoot
	sw       *widget.Segmented
	title    *widget.WindowTitle
	controls *widget.WindowControls
}

func newTitleBar(r *calcRoot) *titleBar {
	t := &titleBar{r: r, sw: newModeSwitch(), title: widget.NewWindowTitle("Calculator"), controls: widget.NewWindowControls()}
	t.title.Label().Color = widget.Ink
	return t
}

func (t *titleBar) show(graph bool, u *gunim.UI) {
	mode := 0
	if graph {
		mode = 1
	}
	t.sw.SetSelected(mode, u)
}

// Children implements [gunim.Composite].
func (t *titleBar) Children() []gunim.Node { return []gunim.Node{t.sw, t.title, t.controls} }

// Layout implements [gunim.Node].
func (t *titleBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	sw, title, controls := kids.At(0), kids.At(1), kids.At(2)
	cs := controls.Layout(gunim.Constraints{Max: size})
	controls.Place(geom.Pt(size.W-cs.W, 0))
	ss := sw.Layout(gunim.Constraints{Max: geom.Sz(size.W, size.H)})
	sw.Place(geom.Pt(10, (size.H-ss.H)/2))
	left := 10 + ss.W + 10
	title.Layout(gunim.Tight(geom.Sz(max(0, size.W-cs.W-left), size.H)))
	title.Place(geom.Pt(left, 0))
	return size
}

// Paint implements [gunim.Node].
func (t *titleBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.MenubarFill.Get(f.Theme)))
	for k := range kids.All {
		k.Paint(p)
	}
}

// switchTrack fills the track of the switch between the calculator and the graph.
var switchTrack = theme.Color("calc.switch.track", color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0x14})

// newModeSwitch returns the switch between the calculator and the graph, which leaves the keyboard with the
// calculator when clicked.
func newModeSwitch() *widget.Segmented {
	s := widget.NewSegmented("Calc", "Graph")
	s.Track, s.KeepFocus = switchTrack, true
	s.OnChange = func(i int, u *gunim.UI) gunim.Intent { return ShowGraph{On: i == 1} }
	return s
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
