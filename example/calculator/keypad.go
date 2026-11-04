package main

import (
	"image/color"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// keyKind is what a key does, which picks its colour.
type keyKind uint8

const (
	kindDigit keyKind = iota
	kindSign
	kindFunc
	kindEquals
	kindClear
)

// kindOf is what a key's label does.
func kindOf(label string) keyKind {
	switch {
	case label == "=":
		return kindEquals
	case label == "C" || label == "⌫":
		return kindClear
	case strings.ContainsAny(label, "+−×÷^%"):
		return kindSign
	case strings.ContainsAny(label, "0123456789.") && len(label) == 1:
		return kindDigit
	}
	return kindFunc
}

// ripple is a ring spreading out from where a key was pressed.
type ripple struct {
	at  geom.Point
	age time.Duration
}

// rippleTime is how long a ripple takes to spread and fade.
const rippleTime = 550 * time.Millisecond

// key is one key: it squashes under the finger and springs back with a
// bounce, lights under the pointer, and a ripple spreads from where it
// was pressed.
type key struct {
	anim.Group
	label   string
	kind    keyKind
	hover   *anim.Float
	squash  *anim.Float
	ripples []ripple
	down    bool
	size    geom.Size
}

func newKey(label string) *key {
	k := &key{label: label, kind: kindOf(label), hover: anim.NewFloat(0), squash: anim.NewFloat(0)}
	k.Add(k.hover, k.squash)
	return k
}

// Step implements [gunim.Animator]: the ripples age as the springs move.
func (k *key) Step(dt time.Duration) bool {
	moving := k.Group.Step(dt)
	live := k.ripples[:0]
	for _, r := range k.ripples {
		r.age += dt
		if r.age < rippleTime {
			live = append(live, r)
		}
	}
	k.ripples = live
	return moving || len(k.ripples) > 0
}

// press squashes the key and starts a ripple at p.
func (k *key) press(p geom.Point) {
	k.squash.Animate(1, anim.Spring{Response: 0.12, Damping: 0.9})
	k.ripples = append(k.ripples, ripple{at: p})
}

// release lets the key spring back, overshooting a little.
func (k *key) release() {
	k.squash.Animate(0, anim.Spring{Response: 0.35, Damping: 0.45})
}

// Layout implements [gunim.Node].
func (k *key) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	k.size = c.Max
	return c.Max
}

// colours are a key's fill and ink.
func (k *key) colours(th gunim.Frame) (fill, ink color.NRGBA) {
	t := th.Theme
	ink = widget.Ink.Get(t)
	switch k.kind {
	case kindEquals:
		return widget.Accent.Get(t), color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	case kindSign:
		a := widget.Accent.Get(t)
		return paint.Faded(a, 0.22), a
	case kindClear:
		return color.NRGBA{R: 0xff, G: 0x6b, B: 0x5e, A: 0x30}, color.NRGBA{R: 0xff, G: 0x9a, B: 0x8e, A: 0xff}
	case kindFunc:
		return paint.Faded(ink, 0.07), paint.Faded(ink, 0.85)
	case kindDigit:
	}
	return paint.Faded(ink, 0.12), ink
}

// Paint implements [gunim.Node].
func (k *key) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	fill, ink := k.colours(f)
	r := geom.Rect{Max: box.Point()}
	s := k.squash.Value()
	defer p.Push(paint.Scale(1-0.08*s, r.Center()))()
	h := k.hover.Value()
	radius := min(box.W, box.H) * 0.28
	p.ShadowRRect(r, radius, paint.Solid(fill), paint.Shadow{
		Offset: geom.Pt(0, 3*(1-s)), Blur: 10 * (1 - s), Color: color.NRGBA{A: uint8(0x50 * (1 - s))},
	})
	if h > 0.01 {
		p.RRect(r, radius, paint.Solid(paint.Faded(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.08*h)))
	}
	// The ripples, clipped to the key.
	if len(k.ripples) > 0 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: radius})()
			reach := geom.Sz(box.W, box.H)
			most := float32(max(reach.W, reach.H)) * 1.2
			for _, rp := range k.ripples {
				t := float32(rp.age) / float32(rippleTime)
				// Fast out, slow to finish.
				e := 1 - (1-t)*(1-t)*(1-t)
				rad := 6 + most*e
				p.RRect(geom.Rc(rp.at.X-rad, rp.at.Y-rad, 2*rad, 2*rad), rad, paint.Solid(paint.Faded(ink, 0.22*(1-t))))
			}
		}()
	}
	size := float32(22)
	if k.kind == kindFunc {
		size = 17
	}
	run := shaped(k.label, size)
	run.Paint(p, geom.Pt((box.W-run.Advance)/2, (box.H-run.Height())/2), ink)
}

// Handle implements [gunim.Handler].
func (k *key) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		k.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		k.hover.Animate(0, anim.Gentle)
		if k.down {
			k.down = false
			k.release()
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		k.down = true
		k.press(e.Pos)
	case input.PointerUp:
		if !k.down {
			return true
		}
		k.down = false
		k.release()
		if (geom.Rect{Max: k.size.Point()}).Contains(e.Pos) {
			u.Send(k, Pressed{Key: k.label})
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// flash plays a press and release, for a key typed on the keyboard.
func (k *key) flash(u *gunim.UI) {
	k.press(geom.Pt(k.size.W/2, k.size.H/2))
	u.After(70*time.Millisecond, func(u *gunim.UI) {
		k.release()
		u.Invalidate()
	})
	u.Invalidate()
}

// keypad is keys in rows and columns, as long as the room.
type keypad struct {
	rows [][]*key
	all  []gunim.Node
	gap  float32
}

func newKeypad(gap float32, rows ...[]string) *keypad {
	kp := &keypad{gap: gap}
	for _, row := range rows {
		keys := make([]*key, 0, len(row))
		for _, label := range row {
			k := newKey(label)
			keys = append(keys, k)
			kp.all = append(kp.all, k)
		}
		kp.rows = append(kp.rows, keys)
	}
	return kp
}

// flash plays the key with label, when the keypad has it.
func (kp *keypad) flash(label string, u *gunim.UI) {
	for _, row := range kp.rows {
		for _, k := range row {
			if k.label == label {
				k.flash(u)
			}
		}
	}
}

// Children implements [gunim.Composite].
func (kp *keypad) Children() []gunim.Node { return kp.all }

// Layout implements [gunim.Node]: every row as tall, every key in a row
// as wide.
func (kp *keypad) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	n := len(kp.rows)
	if n == 0 {
		return size
	}
	h := (size.H - kp.gap*float32(n-1)) / float32(n)
	i := 0
	for r, row := range kp.rows {
		w := (size.W - kp.gap*float32(len(row)-1)) / float32(len(row))
		for c := range row {
			k := kids.At(i)
			k.Layout(gunim.Tight(geom.Sz(w, h)))
			k.Place(geom.Pt(float32(c)*(w+kp.gap), float32(r)*(h+kp.gap)))
			i++
		}
	}
	return size
}

// Paint implements [gunim.Node].
func (kp *keypad) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}
