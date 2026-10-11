package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// LinkInk is the colour of a link's text.
var LinkInk = theme.Foreground("link.ink", color.NRGBA{R: 0x7c, G: 0x9c, B: 0xff, A: 0xff})

// Link is text that does something when clicked, underlined under the
// pointer: a small action among other text, where a button would be
// too big, such as "show 12 more". It takes focus, shows a ring as a
// [Button] does, and Enter or Space activates it. Disabled, it fades
// faint and takes no clicks, keys or focus.
type Link struct {
	Control
	Text string
	// Icon shows before the text, as tall as the text and in its colour.
	Icon *icon.Icon
	// Size and Face default to the theme's [TextSize] and [Font].
	Size theme.Token[float32]
	Face theme.Token[*text.Face]
	// OnClick runs on the UI goroutine when the link is clicked, or
	// pressed by Enter or Space. It may act in the window through u; a
	// non-nil result is sent to the application as the link's intent.
	OnClick func(u *gunim.UI) gunim.Intent

	shaped shapedText
	// laid is the text cut to its box, ending in an ellipsis, for a link
	// given less room than its text takes.
	laid laidText
	// box is the link's size at its last layout, for telling a release
	// over it from one outside.
	box   geom.Size
	click Clicker
}

// NewLink returns a link showing s.
func NewLink(s string) *Link {
	l := &Link{Control: newControl(), Text: s, Size: TextSize}
	return l
}

func (l *Link) run(th *theme.Live) text.Run {
	return l.shaped.shape(faceIn(l.Face, th), l.Text, l.textSize(th))
}

// textSize is the link's text size in th.
func (l *Link) textSize(th *theme.Live) float32 {
	if l.Size.Key() != "" {
		return l.Size.Get(th)
	}
	return TextSize.Get(th)
}

// Layout implements [gunim.Node].
func (l *Link) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	s := l.run(f.Theme).Box()
	s.W += l.iconWidth(f.Theme)
	l.box = c.Constrain(s)
	l.follow(f.Theme)
	return l.box
}

// iconWidth is the room the link's icon takes before its text, with the gap.
func (l *Link) iconWidth(th *theme.Live) float32 {
	if l.Icon == nil {
		return 0
	}
	return l.textSize(th) + IconGap.Get(th)
}

// Paint implements [gunim.Node].
// Given less room than its text takes, it ends the text in an ellipsis
// within its box.
func (l *Link) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer l.faint(p, box)()
	l.paintRing(p, geom.Rect{Max: box.Point()}, FocusRadius.Get(f.Theme), f.Theme)
	run := l.run(f.Theme)
	ink := LinkInk.Get(f.Theme)
	x := l.iconWidth(f.Theme)
	if l.Icon != nil {
		s := l.textSize(f.Theme)
		drawIcon(p, f.Theme, l.Icon, geom.Rc(0, (run.Height()-s)/2, s, s), ink, 1)
	}
	wide := run.Advance
	if run.Advance > box.W-x+0.5 {
		para := l.laid.layout(faceIn(l.Face, f.Theme), l.Text, text.Style{Size: l.textSize(f.Theme), MaxLines: 1},
			max(box.W-x, 0))
		para.Paint(p, geom.Pt(x, 0), ink)
		wide = para.Size.W
	} else {
		run.Paint(p, geom.Pt(x, 0), ink)
	}
	if t := l.hover.Value(); t > 0.01 {
		ink.A = uint8(float32(ink.A) * min(t, 1))
		p.RRect(geom.Rc(x, run.Ascent+1.5, wide, 1), 0, paint.Solid(ink))
	}
}

// Cursor implements [gunim.CursorShaper].
func (l *Link) Cursor(geom.Point) input.Cursor {
	if l.Disabled {
		return input.CursorArrow
	}
	return input.CursorHand
}

// Handle implements [gunim.Handler].
func (l *Link) Handle(e input.Event, u *gunim.UI) bool {
	l.showTip(e, u, l)
	if l.Disabled {
		return l.handleDisabled(e, u, nil)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		l.hover.Animate(1, Quick.Get(u.Theme()))
	case input.PointerLeave:
		l.hover.Animate(0, Settle.Get(u.Theme()))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		l.click.Press(e, 0)
		return true
	case input.PointerUp:
		if l.click.Release(e, over(e.Pos, l.box)) {
			l.fire(u)
		}
		return true
	case input.KeyPress:
		if e.Key != input.KeyEnter && e.Key != input.KeyKPEnter && e.Key != input.KeySpace {
			return false
		}
		l.fire(u)
		return true
	}
	return l.ringFollows(e, u.Theme())
}

func (l *Link) fire(u *gunim.UI) {
	act0(u, l, gunim.CuePress, l.OnClick)
}

// Access implements [gunim.Accessible].
func (l *Link) Access() access.Info {
	return access.Info{Role: access.RoleLink, Name: l.accessName(l.Text), State: l.accessState(), Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor].
func (l *Link) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || l.Disabled {
		return false
	}
	l.fire(u)
	return true
}
