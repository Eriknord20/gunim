package widget

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// TextField is a single line of editable text.
//
// It takes typing, deleting, the arrow, Home and End keys with Shift to
// select and Ctrl to move by word, clicks and drags, double and triple
// clicks, and Ctrl+A, C, X and V. The arrow keys follow the screen, so
// in text that mixes directions the caret moves the way the key points.
// The caret and the selection glide to where they go with the theme's
// [Caret] motion, the text slides sideways to keep the caret in view,
// and focus grows a ring around the field.
//
// While the field has focus, keys pressed without Ctrl, Alt or Super
// stop at it, so typing never sets off a window's shortcuts. The
// function keys go on.
//
// An input method composes in place: the composition shows at the caret,
// underlined, with the input method's own caret or highlight inside it,
// until it commits.
type TextField struct {
	Control
	Placeholder string
	// Icon shows at the start of the field in the placeholder's colour, such as icon.Search.
	Icon *icon.Icon
	// Clearable shows an X at the end while the field has text. Clicking it or pressing Escape empties the field.
	Clearable bool
	// Face is the face the text is set in, and the theme's [Font] when unset.
	Face theme.Token[*text.Face]
	// Height, when set, is the field's height in place of the theme's [FieldHeight], for a compact field in a dense
	// list.
	Height theme.Token[float32]
	// Secret shows each character as a dot, as a password field does,
	// keeps the text off the clipboard, and gives a screen reader no
	// value to read.
	Secret bool
	// OnChange runs on the UI goroutine each time the user changes the
	// text, and OnCommit when they press Enter. Each may act in the window
	// through u, as a palette filters its list as the query changes; a
	// non-nil result is sent to the application as the field's intent.
	OnChange func(text string, u *gunim.UI) gunim.Intent
	OnCommit func(text string, u *gunim.UI) gunim.Intent
	// Keys, when set, hears each key pressed in the field before the
	// field uses it, and takes it from the field by reporting true: for
	// a field that lends some of its keys to what is around it, as Left
	// and Right to a row of buttons.
	Keys func(e input.KeyPress, u *gunim.UI) bool
	// Ghost is a suggestion for the rest of the text, such as the rest
	// of a folder's name, shown faintly after the caret while the caret
	// is at the end. Tab or Right takes it. Set it from OnChange.
	Ghost string
	// Align puts text shorter than the field against its left edge,
	// AlignStart, its right edge, AlignEnd, or in its middle, as a
	// column of numbers lines up on the right. Text longer than the
	// field scrolls as it always does.
	Align text.Align

	editor

	focus   *anim.Float
	caretAt *anim.Float
	selA    *anim.Float
	selB    *anim.Float
	scroll  *anim.Float
	// flash runs from 1 down to 0 after Flash, tinting the field.
	flash *anim.Float
	// clearOn runs from 0 to 1 as the X shows, and clearHot as the pointer comes over it.
	clearOn  *anim.Float
	clearHot *anim.Float
	// clearing says a primary press on the X is held.
	clearing bool
	// pressed, when set, runs from 0 to 1 as a widget built on the
	// field is pressed, and tints it [FieldPressed]; caretless hides
	// the caret meanwhile. A number field's drag sets them.
	pressed   *anim.Float
	caretless bool

	// size is the field's size at its last layout, lead and trail the room before and after the text, and shift
	// how far Align moves text shorter than the field from its start.
	size               geom.Size
	lead, trail, shift float32
	// line is the text as shaped, kept up as it changes; dots are the
	// dots a secret shows in its place, and wasSecret whether the line
	// holds them.
	line      longLine
	dots      []rune
	wasSecret bool
}

// NewTextField returns an empty field.
func NewTextField() *TextField {
	t := &TextField{
		Control:  newControl(),
		focus:    anim.NewFloat(0),
		caretAt:  anim.NewFloat(0),
		selA:     anim.NewFloat(0),
		selB:     anim.NewFloat(0),
		scroll:   anim.NewFloat(0),
		flash:    anim.NewFloat(0),
		clearOn:  anim.NewFloat(0),
		clearHot: anim.NewFloat(0),
	}
	t.changed = func(u *gunim.UI) {
		if t.OnChange != nil {
			send(u, t, t.OnChange(t.Text(), u))
		}
	}
	return t
}

// TakesText implements [gunim.TextTaker], so an input method composes
// into the field.
func (t *TextField) TakesText() bool { return true }

// TextCaret implements [gunim.CaretReporter].
func (t *TextField) TextCaret() geom.Rect {
	h := t.line.Height()
	y := (t.size.H - h) / 2
	return geom.Rc(t.origin()+t.caretAt.Value(), y, 1.5, h)
}

// caretRect returns where a caret before rune i would stand, in the
// field's space.
func (t *TextField) caretRect(i int) geom.Rect {
	h := t.line.Height()
	return geom.Rc(t.origin()+t.line.CaretX(i), (t.size.H-h)/2, 1.5, h)
}

// origin returns where the text starts, in the field's space: after
// the room before it, moved by Align and by the scroll.
func (t *TextField) origin() float32 { return t.lead + t.shift - t.scroll.Value() }

// hostIndex returns the rune a press at p in the field's space is before.
func (t *TextField) hostIndex(p geom.Point, u *gunim.UI) int { return t.indexAt(p, u) }

// Text returns the field's text.
func (t *TextField) Text() string { return t.textString() }

// SetText replaces the text, puts the caret at its end, and sends no
// intent. The field draws it on the next frame; u may be nil, as before
// the field is laid out. An application that sets the text on every
// change it hears about should skip the ones the field sent, or it will
// move the caret under the user's fingers.
func (t *TextField) SetText(s string, u *gunim.UI) {
	t.setText([]rune(s))
	t.set(len(t.text), false)
	t.forget()
	if u != nil {
		u.Invalidate()
	}
}

// Select selects the text from rune start to rune end, with the caret at
// end, such as a file's name without its extension. Call it from a
// view's update function.
func (t *TextField) Select(start, end int) {
	t.set(start, false)
	t.set(end, true)
}

// Flash tints the field in the accent colour and fades it back, to
// show that something other than typing changed it, such as a button
// that fills it in. A hidden field changes only its dots, which are
// easy to miss.
func (t *TextField) Flash() {
	t.flash.Jump(1)
	t.flash.Animate(0, anim.Tween{Duration: flashTime, Ease: anim.EaseInOut})
}

// flashTime is how long a flash takes to fade.
const flashTime = 700 * time.Millisecond

// Step implements [gunim.Animator].
func (t *TextField) Step(dt time.Duration) bool {
	moving := false
	for _, a := range []*anim.Float{t.focus, t.caretAt, t.selA, t.selB, t.scroll, t.flash, t.clearOn, t.clearHot} {
		if a.Step(dt) {
			moving = true
		}
	}
	if t.Control.Step(dt) {
		moving = true
	}
	return t.blink.step(dt) || moving
}

// Handle implements [gunim.Handler].
func (t *TextField) Handle(e input.Event, u *gunim.UI) bool {
	t.showTip(e, u, t)
	if _, lost := e.(input.FocusLost); t.Disabled && !lost {
		return t.handleDisabled(e, u, nil)
	}
	if k, ok := e.(input.KeyPress); ok && t.Keys != nil && t.Keys(k, u) {
		return true
	}
	if t.blink.windowFocus(e, u) {
		return false
	}
	switch e := e.(type) {
	case input.FocusGained:
		t.focus.Animate(1, Quick.Get(u.Theme()))
		t.blink.restart(u)
	case input.FocusLost:
		t.focus.Animate(0, Settle.Get(u.Theme()))
		t.blink.halt()
		t.anchor = t.caret
		t.preedit = nil // the driver ends the composition too
		t.closeMenu()
		t.wording = false
		t.closeHandles()
	case input.PointerDown:
		if e.Button == input.ButtonSecondary {
			t.contextPress(t, t.indexAt(e.Pos, u), e.Pos, e.Touch, u)
			break
		}
		// The X clears on a primary click let go over it, so a press
		// dragged off it clears nothing.
		if t.overClear(e.Pos) {
			t.clearing = e.Button == input.ButtonPrimary
			break
		}
		t.closeHandles()
		t.press(t.indexAt(e.Pos, u), e.Clicks, e.Mods.Has(input.ModShift))
		t.held = true
	case input.PointerMove:
		t.clearHot.Animate(value(t.overClear(e.Pos)), Quick.Get(u.Theme()))
		if t.wording {
			t.dragWords(t.indexAt(e.Pos, u))
			break
		}
		if !t.held {
			return false
		}
		t.set(t.indexAt(e.Pos, u), true)
	case input.PointerLeave:
		t.clearHot.Animate(0, Settle.Get(u.Theme()))
		return false
	case input.PointerUp:
		t.held = false
		if t.endWords(t, e.Pos, u) {
			break
		}
		if t.clearing && e.Button == input.ButtonPrimary {
			t.clearing = false
			if t.overClear(e.Pos) {
				t.clear(u)
			}
		}
	case input.TextInput:
		t.commit(e.Text, u)
	case input.Composing:
		t.compose(e)
	case input.TextEdit:
		t.edit(e, u)
	case input.KeyPress:
		if e.Key == input.KeyEnter || e.Key == input.KeyKPEnter {
			// With nothing to commit to, Enter is for the nodes around
			// the field, such as a dialog's default button.
			if t.OnCommit == nil {
				return false
			}
			send(u, t, t.OnCommit(t.Text(), u))
			return true
		}
		if e.Key == input.KeyEscape && e.Mods == 0 && t.clearable() {
			t.clear(u)
			break
		}
		if t.Ghost != "" && e.Mods == 0 && (e.Key == input.KeyTab || e.Key == input.KeyRight) && t.atEnd() {
			ghost := t.Ghost
			t.Ghost = ""
			t.insert(ghost, u)
			break
		}
		if !t.key(e, u, fieldNav{t}) {
			return false
		}
	default:
		return false
	}
	if _, lost := e.(input.FocusLost); !lost {
		t.blink.restart(u)
	}
	u.Invalidate()
	return true
}

// atEnd reports whether the caret is at the end of the text, with
// nothing selected and nothing being composed.
func (t *TextField) atEnd() bool {
	return t.caret == len(t.text) && t.anchor == t.caret && len(t.preedit) == 0
}

// indexAt returns the rune index a pointer at p, in the field's space,
// puts the caret at.
func (t *TextField) indexAt(p geom.Point, u *gunim.UI) int {
	return t.run(u.Theme()).Index(p.X - t.origin())
}

// clearable reports whether the X shows: the field is Clearable, has text, and is not composing.
func (t *TextField) clearable() bool {
	return t.Clearable && len(t.text) > 0 && len(t.preedit) == 0
}

// overClear reports whether p, in the field's space, is on the X.
func (t *TextField) overClear(p geom.Point) bool {
	return t.clearable() && p.X >= t.size.W-t.trail
}

// clear empties the field as typing would, sending OnChange.
func (t *TextField) clear(u *gunim.UI) {
	t.anchor = 0
	t.replace(0, len(t.text), nil, u)
}

// room returns the room before the text, for the icon, and after it, for the X.
func (t *TextField) room(th *theme.Live) (lead, trail float32) {
	pad := FieldPadding.Get(th)
	lead, trail = pad, pad
	if t.Icon != nil {
		lead += IconSize.Get(th) + IconGap.Get(th)
	}
	if t.Clearable {
		trail += IconSize.Get(th) + IconGap.Get(th)
	}
	return lead, trail
}

// fieldNav navigates a field's one line.
type fieldNav struct{ t *TextField }

func (n fieldNav) caretX(i int) float32 { return n.t.line.CaretX(i) }

func (n fieldNav) beside(i int, x float32, right bool) (next int, nextX float32) {
	return n.t.line.Beside(i, x, right)
}

func (n fieldNav) lineStart(int) int { return 0 }

func (n fieldNav) lineEnd(int) int { return len(n.t.text) }

func (n fieldNav) vertical(i int, _ float32, _ int) (int, bool) { return i, false }

func (n fieldNav) page() int { return 1 }

// run returns the text as shaped, shaping again only what changed
// since it was last asked.
func (t *TextField) run(th *theme.Live) *longLine {
	shown, _ := t.shown()
	t.secret = t.Secret
	head, tail, was, changed := t.shownChange()
	if t.Secret != t.wasSecret {
		t.wasSecret = t.Secret
		head, tail, was, changed = 0, 0, t.line.end(), true
	}
	if t.Secret {
		for len(t.dots) < len(shown) {
			t.dots = append(t.dots, '•')
		}
		shown = t.dots[:len(shown)]
	}
	t.line.update(faceIn(t.Face, th), TextSize.Get(th), shown, head, tail, was, changed)
	return &t.line
}

// Layout implements [gunim.Node]. The field fills the width it is given,
// or the theme's [FieldWidth] when the width is unbounded.
func (t *TextField) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	w := c.Max.W
	if w <= 0 {
		w = FieldWidth.Get(th)
	}
	h := FieldHeight.Get(th)
	if t.Height.Key() != "" {
		h = t.Height.Get(th)
	}
	own := c.Constrain(geom.Sz(w, h))
	t.size = own
	t.follow(th)

	// Aim the caret, the selection and the scroll. Each glides there.
	run := t.run(th)
	t.lead, t.trail = t.room(th)
	inner := own.W - t.lead - t.trail
	if on := value(t.clearable()); on != t.clearOn.Target() {
		if on == 1 {
			t.clearOn.Animate(1, Bounce.Get(th))
		} else {
			t.clearOn.Animate(0, Quick.Get(th))
		}
	}
	motion := Caret.Get(th)
	caret, anchor := t.drawnCaret()
	cx := run.CaretX(caret)
	if t.hinted && len(t.preedit) == 0 && run.Places(caret, t.hintX) {
		cx = t.hintX
	}
	t.aim(t.caretAt, cx, motion)
	a, b := run.CaretX(anchor), cx
	t.aim(t.selA, min(a, b), motion)
	t.aim(t.selB, max(a, b), motion)

	scroll := t.scroll.Target()
	switch {
	case cx < scroll:
		scroll = cx
	case cx > scroll+inner:
		scroll = cx - inner
	}
	scroll = max(0, min(scroll, run.Advance-inner))
	t.aim(t.scroll, scroll, motion)
	t.shift = alignShift(t.Align, inner-run.Advance)
	t.edited = false
	t.placeHandles(t)
	return own
}

// Paint implements [gunim.Node].
func (t *TextField) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer t.faint(p, box)()
	th := f.Theme
	focus := t.focus.Value()
	r := geom.Rect{Max: box.Point()}
	radius := FieldRadius.Get(th)
	border := anim.Mix(anim.ColorCodec, FieldBorder.Get(th), Accent.Get(th), min(focus, 1))
	fill := FieldFill.Get(th)
	if t.pressed != nil {
		if lit := min(max(t.pressed.Value(), 0), 1); lit > 0 {
			tint := FieldPressed.Get(th)
			a := float32(tint.A) / 0xff
			tint.A = 0xff
			fill = anim.Mix(anim.ColorCodec, fill, tint, a*lit)
			border = anim.Mix(anim.ColorCodec, border, Accent.Get(th), lit)
		}
	}
	if lit := min(max(t.flash.Value(), 0), 1); lit > 0 {
		fill = anim.Mix(anim.ColorCodec, fill, Accent.Get(th), 0.35*lit)
		border = anim.Mix(anim.ColorCodec, border, Accent.Get(th), lit)
	}
	p.RRectStroke(r, radius, paint.Solid(fill), paint.Stroke{Width: 1 + focus, Color: border})

	pad := FieldPadding.Get(th)
	lead, trail := t.room(th)
	s := IconSize.Get(th)
	if t.Icon != nil {
		paintIcon(p, th, t.Icon, geom.Rc(pad, (box.H-s)/2, s, s), Placeholder.Get(th), 1)
	}
	t.paintClear(p, th, box)
	inner := geom.Rect{Min: geom.Pt(lead-pad/2, 0), Max: geom.Pt(box.W-trail+pad/2, box.H)}
	defer p.Layer(paint.LayerOpts{Bounds: inner, Opacity: 1, Clip: true})()

	run := t.run(th)
	x := lead + t.shift - t.scroll.Value()
	y := (box.H - run.Height()) / 2
	if len(t.text) == 0 && len(t.preedit) == 0 && t.Placeholder != "" {
		ph := faceIn(t.Face, th).Shape(t.Placeholder, TextSize.Get(th))
		ph.Paint(p, geom.Pt(lead+alignShift(t.Align, box.W-lead-trail-ph.Advance), y), Placeholder.Get(th))
	}

	if a, b := t.selA.Value(), t.selB.Value(); b-a > 0.5 && focus > 0 {
		sel := Selection.Get(th)
		sel.A = uint8(float32(sel.A) * min(focus, 1))
		p.RRect(geom.Rc(x+a, y, b-a, run.Height()), 3, paint.Solid(sel))
	}
	run.Paint(p, geom.Pt(x, y), Ink.Get(th), inner.Min.X-x, inner.Max.X-x)
	if t.Ghost != "" && focus > 0.01 && t.atEnd() && !t.Secret {
		ghost := faceIn(t.Face, th).Shape(t.Ghost, TextSize.Get(th))
		ghost.Paint(p, geom.Pt(x+run.Advance, y), Placeholder.Get(th))
	}
	if len(t.preedit) > 0 {
		// Underline the composition, as input methods expect.
		_, at := t.shown()
		x0, x1 := run.CaretX(at), run.CaretX(at+len(t.preedit))
		p.RRect(geom.Rc(x+min(x0, x1), y+run.Ascent()+2, abs32(x1-x0), 1), 0, paint.Solid(Ink.Get(th)))
	}

	if focus > 0.01 && !t.caretless {
		c := Accent.Get(th)
		c.A = uint8(float32(c.A) * min(focus, 1) * t.blink.value())
		p.RRect(geom.Rc(x+t.caretAt.Value()-0.75, y, 1.5, run.Height()), 0.75, paint.Solid(c))
	}
}

// paintClear draws the X at the end of the field, growing and fading in with clearOn.
func (t *TextField) paintClear(p *paint.Painter, th *theme.Live, box geom.Size) {
	on := t.clearOn.Value()
	if !t.Clearable || on <= 0.01 {
		return
	}
	s := IconSize.Get(th)
	r := geom.Rc(box.W-FieldPadding.Get(th)-s, (box.H-s)/2, s, s)
	ink := anim.Mix(anim.ColorCodec, Placeholder.Get(th), Ink.Get(th), min(max(t.clearHot.Value(), 0), 1))
	ink.A = uint8(float32(ink.A) * min(on, 1))
	defer p.Push(paint.Scale(0.5+0.5*on, r.Center()))()
	paintIcon(p, th, icon.X, r, ink, 1)
}

// alignShift returns how far align moves text that leaves room spare
// in its field: none for AlignStart, or text that fills the field, all
// of it for AlignEnd, and half for AlignCenter.
func alignShift(align text.Align, room float32) float32 {
	if room <= 0 {
		return 0
	}
	switch align {
	case text.AlignEnd:
		return room
	case text.AlignCenter:
		return room / 2
	default:
		return 0
	}
}
