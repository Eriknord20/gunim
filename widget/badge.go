package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Badge tokens.
var (
	// BadgeHeight is a badge's height, and BadgePadding the room at
	// its ends.
	BadgeHeight  = theme.Length("badge.height", 18)
	BadgePadding = theme.Length("badge.padding", 7)
	// BadgeSize is its words' size.
	BadgeSize = theme.Length("badge.size", 12)
	// BadgeTint is how much of a badge's colour fills it, from 0 to 1.
	BadgeTint = theme.Length("badge.tint", 0.17)
)

// Badge is a small rounded label in a colour of its own, as a count of
// problems beside a row, or "new". It grows and fades in as it is
// shown, and shrinks and fades out as it is hidden, keeping its words
// while it goes. It takes no keys.
type Badge struct {
	anim.Group
	// Text is what it says.
	Text string
	// Ink colours its words and, faintly, its fill; unset, it is the
	// theme's [Accent].
	Ink theme.Token[color.NRGBA]

	// hidden says it is hidden, or on its way out; shown is how far it
	// has grown in.
	hidden bool
	shown  *anim.Float
	said   string
	shaped shapedText
	laid   bool
}

// NewBadge returns a badge saying text, shown.
func NewBadge(text string) *Badge {
	b := &Badge{Text: text, shown: anim.NewFloat(0)}
	b.Add(b.shown)
	return b
}

// Hidden reports whether the badge is hidden.
func (b *Badge) Hidden() bool { return b.hidden }

// SetHidden hides the badge, or shows it again, and sends no intent.
// Once laid out, it shrinks and fades out, or grows and fades in; before,
// it is gone or there at once. u may be nil.
func (b *Badge) SetHidden(hidden bool, u *gunim.UI) {
	if b.hidden == hidden {
		return
	}
	b.hidden = hidden
	u.Invalidate()
}

// ink is the badge's colour.
func (b *Badge) ink(th *theme.Live) color.NRGBA {
	if b.Ink.Key() != "" {
		return b.Ink.Get(th)
	}
	return Accent.Get(th)
}

// Layout implements [gunim.Node]: as wide as its words, times how far
// it has grown in.
func (b *Badge) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	on := !b.hidden && b.Text != ""
	if on {
		b.said = b.Text
	}
	to := float32(0)
	if on {
		to = 1
	}
	switch {
	case !b.laid:
		// There at once, the first time, as what is beside it is.
		b.shown.Jump(to)
	case on:
		b.shown.Animate(1, Quick.Get(th))
	default:
		b.shown.Animate(0, Settle.Get(th))
	}
	b.laid = true
	t := min(max(b.shown.Value(), 0), 1)
	if t < 0.001 || b.said == "" {
		return geom.Size{}
	}
	run := b.shaped.shape(faceIn(Font, th), b.said, BadgeSize.Get(th))
	w := run.Advance + 2*BadgePadding.Get(th)
	return c.Constrain(geom.Sz(w*t, BadgeHeight.Get(th)))
}

// Paint implements [gunim.Node].
func (b *Badge) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	t := min(max(b.shown.Value(), 0), 1)
	if t < 0.001 || box.W <= 0 {
		return
	}
	th := f.Theme
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t})()
	ink := b.ink(th)
	fill := ink
	fill.A = uint8(float32(fill.A) * min(max(BadgeTint.Get(th), 0), 1))
	p.RRect(geom.Rect{Max: box.Point()}, box.H/2, paint.Solid(fill))
	pad := BadgePadding.Get(th)
	run := b.shaped.shape(faceIn(Font, th), b.said, BadgeSize.Get(th))
	run.PaintFaded(p, geom.Pt(pad, box.H/2-run.Height()/2), ink, max(box.W-pad, 0), TextFade.Get(th))
}

// Access implements [gunim.Accessible]: a badge reads as its words.
func (b *Badge) Access() access.Info {
	if b.hidden {
		return access.Info{Role: access.RoleLabel}
	}
	return access.Info{Role: access.RoleLabel, Name: b.Text}
}
