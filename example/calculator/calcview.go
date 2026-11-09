package main

import (
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// rollChar is one character of a rolling line of text.
type rollChar struct {
	s       string
	x, y, a *anim.Float
	placed  bool
	leaving bool
}

// roll is a line of text whose characters roll in from below as they
// are typed, float up and fade as they go, and slide along to stay
// right-aligned. A line too long for its room shrinks to fit, smoothly.
type roll struct {
	chars []*rollChar
	size  float32
	fit   *anim.Float
	text  string
}

func newRoll(size float32) *roll { return &roll{size: size, fit: anim.NewFloat(1)} }

// set changes the text: what it shares with the text before stays, the
// rest rolls out, and the new rolls in.
func (r *roll) set(s string) {
	if s == r.text {
		return
	}
	r.text = s
	var kept []*rollChar
	for _, c := range r.chars {
		if !c.leaving {
			kept = append(kept, c)
		}
	}
	rs := []rune(s)
	same := 0
	for same < len(kept) && same < len(rs) && kept[same].s == string(rs[same]) {
		same++
	}
	for _, c := range kept[same:] {
		c.leaving = true
		c.y.Animate(-r.size*0.7, anim.Gentle)
		c.a.Animate(0, anim.Spring{Response: 0.25, Damping: 1})
	}
	for i, ch := range rs[same:] {
		c := &rollChar{s: string(ch), x: anim.NewFloat(0), y: anim.NewFloat(r.size * 0.7), a: anim.NewFloat(0)}
		// Each a little after the one before, when many come at once.
		delay := float32(i) * 0.04
		c.y.Jump(r.size * (0.7 + delay*4))
		c.y.Animate(0, anim.Spring{Response: 0.38, Damping: 0.62})
		c.a.Animate(1, anim.Snappy)
		r.chars = append(r.chars, c)
	}
}

// step moves the characters on, and lets go of the ones gone.
func (r *roll) step(dt time.Duration) bool {
	moving := r.fit.Step(dt)
	live := r.chars[:0]
	for _, c := range r.chars {
		for _, f := range []*anim.Float{c.x, c.y, c.a} {
			if f.Step(dt) {
				moving = true
			}
		}
		if c.leaving && c.a.Value() < 0.02 && !c.a.Active() {
			continue
		}
		live = append(live, c)
	}
	r.chars = live
	return moving
}

// place aims each character at its place, right-aligned at right, and
// shrinks the line to fit width.
func (r *roll) place(right, width float32) {
	total := float32(0)
	for _, c := range r.chars {
		if !c.leaving {
			total += shaped(c.s, r.size).Advance
		}
	}
	fit := float32(1)
	if total > width && total > 0 {
		fit = width / total
	}
	if fit != r.fit.Target() {
		r.fit.Animate(fit, anim.Snappy)
	}
	x := right - total
	for _, c := range r.chars {
		if c.leaving {
			continue
		}
		if !c.placed {
			c.placed = true
			c.x.Jump(x)
		} else if c.x.Target() != x {
			c.x.Animate(x, anim.Spring{Response: 0.3, Damping: 0.78})
		}
		x += shaped(c.s, r.size).Advance
	}
}

// paint draws the line with its baseline box's top at top, shrunk
// about its right edge.
func (r *roll) paint(p *paint.Painter, top, right float32, ink color.NRGBA) {
	defer p.Push(paint.Scale(r.fit.Value(), geom.Pt(right, top+r.size)))()
	for _, c := range r.chars {
		run := shaped(c.s, r.size)
		run.Paint(p, geom.Pt(c.x.Value(), top+c.y.Value()), paint.Faded(ink, c.a.Value()))
	}
}

// tapeRow is a sum on the tape: it springs to its place as sums arrive
// above it, and fades in once the sum flying to it has landed.
type tapeRow struct {
	line Line
	y, a *anim.Float
}

// flight is a sum flying from the display to the top of the tape.
type flight struct {
	id   int
	text string
	t    *anim.Float
}

// calcBody is the calculator: the display, the keypad under it, and
// the tape of sums worked out beside them.
type calcBody struct {
	anim.Group
	r      *calcRoot
	keypad *keypad
	expr   *roll
	// shake throws the display sideways when a sum has no answer.
	shake  *anim.Float
	errors int
	rows   []*tapeRow
	flying []*flight
	// display, pad and tape are where the display, the keypad and the
	// tape were laid out, in the body's space; exprAt is the line
	// being typed.
	display, pad, tape geom.Rect
	exprAt             geom.Rect
}

// tapeRowHeight is a sum's height on the tape.
const tapeRowHeight = 58

// narrowWidth is the width below which the calculator stacks the
// display, the keypad and the tape, as on a phone, where it sets the
// tape beside the other two on a wider window.
const narrowWidth = 640

func newCalcBody(r *calcRoot) *calcBody {
	b := &calcBody{r: r, shake: anim.NewFloat(0), expr: newRoll(40)}
	b.keypad = newKeypad(10,
		[]string{"C", "(", ")", "⌫", "÷"},
		[]string{"sin", "cos", "tan", "√", "×"},
		[]string{"7", "8", "9", "^", "−"},
		[]string{"4", "5", "6", "π", "+"},
		[]string{"1", "2", "3", "x", "ln"},
		[]string{"0", ".", "e", "%", "="},
	)
	b.Add(b.shake)
	return b
}

// Step implements [gunim.Animator].
func (b *calcBody) Step(dt time.Duration) bool {
	moving := b.Group.Step(dt)
	if b.expr.step(dt) {
		moving = true
	}
	for _, row := range b.rows {
		if row.y.Step(dt) || row.a.Step(dt) {
			moving = true
		}
	}
	live := b.flying[:0]
	for _, f := range b.flying {
		if f.t.Step(dt) {
			moving = true
		}
		if f.t.Value() > 0.97 && !f.t.Active() {
			b.landed(f.id)
			continue
		}
		live = append(live, f)
	}
	b.flying = live
	return moving
}

// landed shows the row a sum flew to.
func (b *calcBody) landed(id int) {
	for _, row := range b.rows {
		if row.line.ID == id {
			row.a.Animate(1, anim.Snappy)
		}
	}
}

// show takes the application's state.
func (b *calcBody) show(was, s Calc, u *gunim.UI) {
	b.expr.set(s.Expr)
	if s.Errors != b.errors {
		b.errors = s.Errors
		b.shake.Jump(1)
		b.shake.Animate(0, anim.Spring{Response: 0.35, Damping: 0.2})
	}
	// The tape: new sums arrive at the top, flying there from the
	// display; the rest spring down to make room.
	seen := map[int]*tapeRow{}
	for _, row := range b.rows {
		seen[row.line.ID] = row
	}
	rows := make([]*tapeRow, 0, len(s.Tape))
	for i, l := range s.Tape {
		y := float32(i) * tapeRowHeight
		row, ok := seen[l.ID]
		if !ok {
			row = &tapeRow{line: l, y: anim.NewFloat(y), a: anim.NewFloat(0)}
			if i == 0 && len(was.Tape) > 0 || i == 0 && was.Expr != "" {
				b.flying = append(b.flying, &flight{id: l.ID, text: l.Expr + " = " + l.Result, t: flyFrom()})
			} else {
				row.a.Animate(1, anim.Snappy)
			}
		}
		row.y.Animate(y, anim.Spring{Response: 0.45, Damping: 0.72})
		rows = append(rows, row)
	}
	b.rows = rows
	u.Invalidate()
}

// flyFrom is a flight's progress, starting.
func flyFrom() *anim.Float {
	t := anim.NewFloat(0)
	t.Animate(1, anim.Spring{Response: 0.55, Damping: 0.85})
	return t
}

// Children implements [gunim.Composite].
func (b *calcBody) Children() []gunim.Node { return []gunim.Node{b.keypad} }

// Covers implements [gunim.Shaped]: the calculator takes the pointer
// while it is the one showing.
func (b *calcBody) Covers(geom.Point) bool { return b.r.mode.Target() == 0 }

// Layout implements [gunim.Node].
func (b *calcBody) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	const pad, gap = 18, 16
	if size.W < narrowWidth {
		// A phone: the display across the top, the keypad under it,
		// and the tape a strip along the bottom.
		lw := size.W - 2*pad
		tapeH := min(max(size.H*0.2, 110), 220)
		b.display = geom.Rc(pad, pad, lw, 150)
		b.tape = geom.Rc(pad, size.H-pad-tapeH, lw, tapeH)
		b.pad = geom.Rc(pad, b.display.Max.Y+gap, lw, max(0, b.tape.Min.Y-gap-b.display.Max.Y-gap))
	} else {
		lw := min(430, size.W*0.55)
		b.display = geom.Rc(pad, pad, lw, 160)
		b.pad = geom.Rc(pad, b.display.Max.Y+gap, lw, max(0, size.H-b.display.Max.Y-gap-pad))
		b.tape = geom.Rc(b.display.Max.X+gap, pad, max(0, size.W-b.display.Max.X-gap-pad), max(0, size.H-2*pad))
	}
	k := kids.At(0)
	k.Layout(gunim.Tight(b.pad.Size()))
	k.Place(b.pad.Min)
	b.exprAt = geom.Rect{Min: geom.Pt(b.display.Min.X+20, b.display.Max.Y-86), Max: geom.Pt(b.display.Max.X-20, b.display.Max.Y-40)}
	b.expr.place(b.exprAt.Max.X, b.exprAt.Size().W)
	return size
}

// Paint implements [gunim.Node].
func (b *calcBody) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	card := widget.CardFill.Get(th)
	st := b.r.state

	// The display, shaken when a sum has no answer.
	func() {
		sh := b.shake.Value()
		defer p.Push(paint.Translate(geom.Pt(14*sh, 0)))()
		d := b.display
		p.ShadowRRect(d, 18, paint.Solid(card), paint.Shadow{Offset: geom.Pt(0, 6), Blur: 22, Color: color.NRGBA{A: 0x60}})
		// The last few sums, faint, over the line being typed.
		for i, row := range b.rows {
			if i >= 2 {
				break
			}
			run := shaped(row.line.Expr+" = "+row.line.Result, 14)
			y := d.Max.Y - 110 - float32(i)*20
			if y < d.Min.Y+8 {
				break
			}
			run.Paint(p, geom.Pt(d.Max.X-20-run.Advance, y), paint.Faded(ink, 0.35*row.a.Value()/float32(i+1)))
		}
		b.expr.paint(p, b.exprAt.Min.Y, b.exprAt.Max.X, ink)
		below := st.Preview
		col := paint.Faded(ink, 0.5)
		if st.Error != "" {
			below, col = st.Error, color.NRGBA{R: 0xff, G: 0x8a, B: 0x7a, A: 0xff}
		} else if below != "" {
			below = "= " + below
		}
		if below != "" {
			run := shaped(below, 18)
			run.Paint(p, geom.Pt(d.Max.X-20-run.Advance, d.Max.Y-32), col)
		}
	}()

	kids.At(0).Paint(p)
	b.paintTape(p, f)
	// Sums on their way to the tape, over everything.
	for _, fl := range b.flying {
		t := fl.t.Value()
		from := geom.Pt(b.exprAt.Max.X, b.exprAt.Min.Y)
		to := geom.Pt(b.tape.Min.X+18, b.tape.Min.Y+46)
		run := shaped(fl.text, 22)
		fromX := from.X - run.Advance
		x := fromX + (to.X-fromX)*t
		// Up in an arc on the way.
		y := from.Y + (to.Y-from.Y)*t - 60*t*(1-t)
		scale := 1.5 + (1-1.5)*t
		func() {
			defer p.Push(paint.Scale(scale, geom.Pt(x, y)))()
			run.Paint(p, geom.Pt(x, y), widget.Accent.Get(th))
		}()
	}
}

// paintTape draws the tape of sums worked out, newest at the top.
func (b *calcBody) paintTape(p *paint.Painter, f gunim.Frame) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	t := b.tape
	if t.Size().W < 60 {
		return
	}
	p.RRect(t, 18, paint.Solid(paint.Faded(widget.CardFill.Get(th), 0.6)))
	head := shaped("Tape", 13)
	head.Paint(p, geom.Pt(t.Min.X+18, t.Min.Y+14), paint.Faded(ink, 0.5))
	if len(b.rows) == 0 {
		hint := shaped("Sums worked out land here", 14)
		hint.Paint(p, geom.Pt(t.Min.X+18, t.Min.Y+44), paint.Faded(ink, 0.3))
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: t, Opacity: 1, Clip: true, Radius: 18})()
	for _, row := range b.rows {
		y := t.Min.Y + 40 + row.y.Value()
		if y > t.Max.Y {
			continue
		}
		a := row.a.Value()
		sum := shaped(row.line.Expr, 14)
		sum.Paint(p, geom.Pt(t.Min.X+18, y), paint.Faded(ink, 0.5*a))
		res := shaped("= "+row.line.Result, 22)
		res.Paint(p, geom.Pt(t.Min.X+18, y+18), paint.Faded(ink, a))
	}
}

// Handle implements [gunim.Handler]: a sum on the tape, clicked, brings
// its answer back to work on.
func (b *calcBody) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || d.Button != input.ButtonPrimary || !b.tape.Contains(d.Pos) {
		return false
	}
	i := int((d.Pos.Y - b.tape.Min.Y - 40) / tapeRowHeight)
	if i >= 0 && i < len(b.rows) {
		u.Send(b, Recall{ID: b.rows[i].line.ID})
		return true
	}
	return false
}
