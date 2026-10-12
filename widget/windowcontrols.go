package widget

import (
	"image/color"
	"math"
	"slices"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Theme tokens for a chromeless window's buttons.
var (
	// WindowButtonHot lights the minimize and maximize buttons under
	// the pointer, and WindowCloseHot the close button, red as Windows
	// makes it.
	WindowButtonHot = theme.Color("window.button.hot", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16})
	WindowCloseHot  = theme.Color("window.close.hot", color.NRGBA{R: 0xc4, G: 0x2b, B: 0x1c, A: 0xff})
)

// windowButtonWidth is each button's width, as Windows 11 has it, and compactButtonWidth a compact title bar's.
const (
	windowButtonWidth  = 46
	compactButtonWidth = 36
)

// TitleBarCompactHeight is the height of a compact title bar, such as a small window's kept beside others.
var TitleBarCompactHeight = theme.Length("titlebar.compact.height", 26)

// windowButton is one of the buttons at the end of a title bar.
type windowButton int

const (
	pinButton windowButton = iota
	minimizeButton
	maximizeButton
	closeButton
	noButton windowButton = -1
)

// WindowControls are a chromeless window's minimize, maximize and close
// buttons, for the end of its title bar, drawn as Windows 11 draws them.
// Maximize shows restore while the window is maximized. It is the
// window's [gunim.MaximizeButton], for the snap layouts Windows 11 hangs
// on it. Close does what the system's does: see [gunim.UI.AskToClose].
//
// NoMinimize, NoMaximize and NoClose leave buttons out, for a small
// window such as one that asks a question. With all three left out, and
// no Pin, the controls take no room. Without maximize, the window tells
// the system it has no maximize button, so Windows 11 shows no snap
// layouts, and a double click on the title bar leaves the window as it
// is.
type WindowControls struct {
	anim.Group
	// Pin adds a button before minimize that keeps the window above other windows; see [gunim.UI.SetPinned].
	Pin bool
	// NoMinimize, NoMaximize and NoClose leave out the minimize, maximize and close buttons.
	NoMinimize, NoMaximize, NoClose bool
	// Compact makes the buttons narrower, and as tall as [TitleBarCompactHeight] when nothing sets their height.
	Compact bool
	hot     [4]*anim.Float
	over    windowButton
	down    windowButton
	click   Clicker
	// size, maxed and pinned are the buttons' size and whether the window was maximized and pinned, at the last
	// layout.
	size   geom.Size
	maxed  bool
	pinned bool
}

// NewWindowControls returns the three buttons. Set NoMinimize,
// NoMaximize or NoClose to leave some out.
func NewWindowControls() *WindowControls {
	c := &WindowControls{over: noButton, down: noButton}
	for i := range c.hot {
		c.hot[i] = anim.NewFloat(0)
		c.Add(c.hot[i])
	}
	return c
}

// buttons returns the buttons shown, in order.
func (c *WindowControls) buttons() []windowButton {
	out := make([]windowButton, 0, 4)
	for _, b := range []struct {
		b  windowButton
		on bool
	}{{pinButton, c.Pin}, {minimizeButton, !c.NoMinimize}, {maximizeButton, !c.NoMaximize}, {closeButton, !c.NoClose}} {
		if b.on {
			out = append(out, b.b)
		}
	}
	return out
}

// width returns each button's width.
func (c *WindowControls) width() float32 {
	if c.Compact {
		return compactButtonWidth
	}
	return windowButtonWidth
}

// height returns the title bar's height, for buttons not told it.
func (c *WindowControls) height(th *theme.Live) float32 {
	if c.Compact {
		return TitleBarCompactHeight.Get(th)
	}
	return MenubarHeight.Get(th)
}

// Layout implements [gunim.Node]: the buttons as tall as the title
// bar.
func (c *WindowControls) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	c.size, c.maxed, c.pinned = geom.Size{}, f.Maximized(), f.Pinned()
	if !f.Chromeless() {
		// The system's title bar has its own.
		return cs.Constrain(geom.Size{})
	}
	h := cs.Max.H
	if h <= 0 {
		h = c.height(f.Theme)
	}
	c.size = cs.Constrain(geom.Sz(float32(len(c.buttons()))*c.width(), h))
	return c.size
}

// MaximizeRect implements [gunim.MaximizeButton]. It is empty without a
// maximize button, which tells the system there is none.
func (c *WindowControls) MaximizeRect(size geom.Size) geom.Rect {
	i := slices.Index(c.buttons(), maximizeButton)
	if i < 0 {
		return geom.Rect{}
	}
	return geom.Rc(float32(i)*c.width(), 0, c.width(), size.H)
}

// buttonAt is the button under p, or noButton.
func (c *WindowControls) buttonAt(p geom.Point, size geom.Size) windowButton {
	if p.Y < 0 || p.Y >= size.H || p.X < 0 {
		return noButton
	}
	if i, bs := int(p.X/c.width()), c.buttons(); i < len(bs) {
		return bs[i]
	}
	return noButton
}

// act does what button b does.
func (c *WindowControls) act(b windowButton, u *gunim.UI) {
	switch b {
	case pinButton:
		// A window the system cannot pin stays as it is, and the button shows so.
		_ = u.SetPinned(!u.Pinned())
	case minimizeButton:
		u.Minimize()
	case maximizeButton:
		u.ToggleMaximize()
	case closeButton:
		u.AskToClose()
	default:
		// No button is no button.
	}
}

// Handle implements [gunim.Handler]: a button lights under the pointer,
// and acts when the button pressed on it is let go on it.
func (c *WindowControls) Handle(e input.Event, u *gunim.UI) bool {
	size := geom.Sz(float32(len(c.buttons()))*c.width(), c.height(u.Theme()))
	if b, ok := u.Bounds(c); ok {
		size = b.Size()
	}
	switch e := e.(type) {
	case input.PointerEnter:
		c.light(c.buttonAt(e.Pos, size), u)
	case input.PointerMove:
		c.light(c.buttonAt(e.Pos, size), u)
	case input.PointerLeave:
		c.light(noButton, u)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		c.down = c.buttonAt(e.Pos, size)
		c.click.Press(e, int(c.down))
	case input.PointerUp:
		at := c.buttonAt(e.Pos, size)
		c.down = noButton
		if at != noButton && c.click.Release(e, int(at)) {
			c.act(at, u)
		}
	default:
		return false
	}
	return true
}

// light lights button b, and dims the rest.
func (c *WindowControls) light(b windowButton, u *gunim.UI) {
	if b == c.over {
		return
	}
	c.over = b
	for k, h := range c.hot {
		to := float32(0)
		if windowButton(k) == b {
			to = 1
		}
		h.Animate(to, Quick.Get(u.Theme()))
	}
	u.Invalidate()
}

// Paint implements [gunim.Node].
func (c *WindowControls) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	// The title bar's colour, for the row they end to read as one, or the application's own drawing under them.
	if !f.UnderTitleBar() {
		p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(MenubarFill.Get(th)))
	}
	ink := Ink.Get(th)
	bw := c.width()
	for i, b := range c.buttons() {
		r := geom.Rc(float32(i)*bw, 0, bw, box.H)
		t := min(max(c.hot[b].Value(), 0), 1)
		glyph := ink
		if t > 0.01 {
			hot := WindowButtonHot.Get(th)
			if b == closeButton {
				hot = WindowCloseHot.Get(th)
				glyph = anim.Mix(anim.ColorCodec, ink, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, t)
			}
			hot.A = uint8(float32(hot.A) * t)
			p.RRect(r, 0, paint.Solid(hot))
		}
		mid := geom.Pt(float32(math.Round(float64(r.Center().X))), float32(math.Round(float64(r.Center().Y))))
		const g, w = 10, 1
		switch b {
		case pinButton:
			// The pin stands upright in the accent while pinned, and leans over in the ink while not.
			const s = 14
			at := geom.Rc(mid.X-s/2, mid.Y-s/2, s, s)
			if c.pinned {
				drawIcon(p, th, icon.Pin, at, Accent.Get(th), 1)
			} else {
				func() {
					defer p.Push(paint.Rotate(math.Pi/4, mid))()
					drawIcon(p, th, icon.Pin, at, glyph, 1)
				}()
			}
		case minimizeButton:
			p.RRect(geom.Rc(mid.X-g/2, mid.Y, g, w), 0, paint.Solid(glyph))
		case maximizeButton:
			if f.Maximized() {
				// Restore: a square with another behind it.
				p.RRect(geom.Rc(mid.X-g/2+2, mid.Y-g/2, g-2, w), 0, paint.Solid(glyph))
				p.RRect(geom.Rc(mid.X+g/2-w, mid.Y-g/2, w, g-2), 0, paint.Solid(glyph))
				p.RRectStroke(geom.Rc(mid.X-g/2, mid.Y-g/2+2, g-2, g-2), 1, paint.Fill{}, paint.Stroke{Width: w, Color: glyph})
			} else {
				p.RRectStroke(geom.Rc(mid.X-g/2, mid.Y-g/2, g, g), 1, paint.Fill{}, paint.Stroke{Width: w, Color: glyph})
			}
		case closeButton:
			// Two strokes crossed.
			for _, a := range []float32{math.Pi / 4, -math.Pi / 4} {
				func() {
					defer p.Push(paint.Rotate(a, mid))()
					p.RRect(geom.Rc(mid.X-g*0.7, mid.Y-w/2, g*1.4, w), 0, paint.Solid(glyph))
				}()
			}
		default:
			// No button under the pointer is nothing to draw.
		}
	}
}

// WindowTitle is a chromeless window's title, centred in the room it is
// given, or at its start with AtStart. All of the room is title bar: a
// press on it moves the window, and a double click maximizes it.
type WindowTitle struct {
	// Text is the title.
	Text string
	// AtStart puts the title at the start of its room, left in a
	// left-to-right language, [WindowTitleInset] in from the edge.
	AtStart bool

	label *Label
	// shown says the window is chromeless, so the title is the
	// window's to show.
	shown bool
	// bar is the width of the whole title bar the title's room starts
	// at, which a [TitleBar] gives it, so the title is centred on the
	// bar and not on the room the buttons leave; 0 centres it on its
	// room.
	bar float32
}

// WindowTitleInset is how far in from the start of its room a title
// with [WindowTitle.AtStart] begins, in logical pixels.
const WindowTitleInset = 14

// NewWindowTitle returns a title showing text.
func NewWindowTitle(text string) *WindowTitle {
	l := NewLabel(text)
	l.MaxLines = 1
	return &WindowTitle{Text: text, label: l}
}

// Label is the label showing the title, to style.
func (t *WindowTitle) Label() *Label { return t.label }

// Children implements [gunim.Composite].
func (t *WindowTitle) Children() []gunim.Node { return []gunim.Node{t.label} }

// Layout implements [gunim.Node]: the title is centred, or at the
// start with AtStart, and cut short when the room runs out.
func (t *WindowTitle) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	own := cs.Max
	k := kids.At(0)
	t.label.Text = t.Text
	t.shown = f.Chromeless()
	if !t.shown {
		// The system's title bar says it, or on a phone the system's
		// own bar names the application.
		k.Layout(gunim.Tight(geom.Size{}))
		return cs.Constrain(geom.Sz(own.W, 0))
	}
	if t.AtStart {
		size := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, own.W-WindowTitleInset), own.H)})
		k.Place(geom.Pt(WindowTitleInset, max(0, (own.H-size.H)/2)))
		return own
	}
	size := k.Layout(gunim.Constraints{Max: own})
	x := (own.W - size.W) / 2
	if t.bar > 0 {
		// Centred on the whole bar, as the eye measures it, and moved
		// towards the start only as far as the buttons ask.
		x = min((t.bar-size.W)/2, own.W-size.W)
	}
	k.Place(geom.Pt(max(0, x), max(0, (own.H-size.H)/2)))
	return own
}

// Paint implements [gunim.Node].
func (t *WindowTitle) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if t.shown {
		kids.At(0).Paint(p)
	}
}

// CaptionRects implements [gunim.Caption]: all of it.
//
// It is room left over, so a row holding it should let it grow.
func (t *WindowTitle) CaptionRects(size geom.Size) []geom.Rect {
	return []geom.Rect{{Max: size.Point()}}
}
