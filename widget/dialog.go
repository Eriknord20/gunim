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
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Dialog is a modal panel that fades, scales and blurs its way in and
// out.
//
// It is the worked example for the whole design. The application says
// "remove this dialog" and then stops thinking about it. The dialog
// stays in the tree, watches its own [gunim.Presence], animates itself
// away, and tells the engine when it is finished. How long that takes
// and what it looks like stay inside the dialog, which is what makes
// the animation easy to change later.
type Dialog struct {
	anim.Group

	// Title shows in a title bar across the top of the panel, drawn as a
	// compact window title bar is: [TitleBarCompactHeight] tall, in
	// [MenubarFill], the title centred in the text size, and no line
	// under it. The bar has no buttons; Escape and Cancel close the
	// dialog. A dialog without a title has no bar.
	Title string
	// Icon shows before the title, in the ink, or in [DialogDangerInk] for a danger dialog. A danger dialog
	// without one shows icon.TriangleAlert.
	Icon *icon.Icon
	// NoTitleBar leaves the title bar out, title, icon and all, for a
	// host that names the dialog itself: a window of the dialog's own
	// with a title bar, say. The body then starts at the top of the
	// panel.
	NoTitleBar bool
	// OnAccept runs on the UI goroutine when the user confirms, and
	// OnDismiss when they back out, by Cancel or Escape. Each may read
	// what the dialog holds, such as a form's fields; a non-nil result is
	// sent to the application as the dialog's intent.
	//
	// The dialog closes itself either way, on the frame the button is
	// released. Waiting for the application to say so would put a round
	// trip between the click and the animation, which is the thin
	// client feeling this design exists to avoid.
	OnAccept  func(u *gunim.UI) gunim.Intent
	OnDismiss func(u *gunim.UI) gunim.Intent
	// Danger marks the dialog's OK as an action that destroys, such as
	// Delete, and shows it in red; otherwise it shows as the action the
	// dialog expects. A danger dialog opens with Cancel focused, so
	// Enter cancels and the red button takes a deliberate press.
	Danger bool
	// Careful opens the dialog with Cancel focused, as Danger does, and
	// leaves OK its usual colour: for a question where yes by reflex is
	// the answer that cannot be taken back, such as trusting a server's
	// new key.
	Careful bool
	// DefaultFirst has Tab go from the fields to OK first, then along
	// the buttons to the left, for a dialog whose OK is the safe answer
	// and whose extra buttons are not, such as Leave It beside Replace:
	// Tab then Enter does what Enter alone does. Without it Tab goes
	// along the buttons from the left.
	DefaultFirst bool
	// Check, when set, runs as the user confirms, and says what stands
	// in the way, or nothing. With something in the way the dialog stays
	// open, gives a shake, and says it under the body.
	Check func() string
	// Body is what the dialog shows under its title, such as a [Form].
	// Set it before mounting the dialog, which grows to fit it. Enter
	// in the body confirms, and Tab moves through the body's fields and
	// the buttons. A body taller than the window scrolls: the dialog
	// holds it in a [Scroll] of its own, unless it is one.
	Body gunim.Node
	// Keys, when set, hears the keys pressed and the text typed in the
	// dialog that its body and buttons leave, before the dialog's own
	// keys: a body that works the dialog from the keyboard in its own
	// way, as a list picked from with Up and Down whatever has the
	// keyboard. It reports whether it took the event.
	Keys func(e input.Event, u *gunim.UI) bool
	// Width, when set, is the panel's width in place of the theme's
	// [DialogWidth], for a body that needs the room: a table, or a bank
	// of faders. It is still capped at the window's width.
	Width float32

	// in runs from 0 (gone) to 1 (fully present) and drives every visual
	// property. One value for the whole transition keeps the fade, the
	// scale and the backdrop blur locked together through any retuning
	// of the motion.
	in *anim.Float

	ok        *Button
	cancel    *Button
	extra     []*Button
	titleText laidText
	// width is the panel's width at the last layout.
	width float32
	// height is the panel's height, from the last layout, and focused
	// is set once the keyboard has gone to the body's first field.
	height  float32
	focused bool
	// problem is what Check last said, and shake the shake it set off.
	problem *Label
	shake   *anim.Float
	// scroll holds the body, once mounted, so a long one scrolls.
	scroll *Scroll
}

// NewDialog returns a dialog with an OK and a Cancel button. Mount it
// from a view and it animates itself in.
func NewDialog(title string) *Dialog {
	d := &Dialog{Title: title, in: anim.NewFloat(0), shake: anim.NewFloat(0)}
	d.problem = NewLabel("")
	d.problem.Color = DialogProblem
	d.Add(d.in, d.shake)

	d.ok = NewButton("OK")
	d.ok.OnClick = func(u *gunim.UI) gunim.Intent {
		d.accept(u)
		return nil
	}
	d.cancel = NewButton("Cancel")
	d.cancel.OnClick = func(u *gunim.UI) gunim.Intent {
		d.dismiss(u)
		return nil
	}
	return d
}

// dismiss closes the dialog, sending what OnDismiss makes.
func (d *Dialog) dismiss(u *gunim.UI) {
	var what gunim.Intent
	if d.OnDismiss != nil {
		what = d.OnDismiss(u)
	}
	d.finish(u, what)
}

// SetButtons names the dialog's buttons, such as "Connect" and
// "Cancel". An empty cancel leaves the dialog with OK alone, for one
// that only tells; Escape still closes it.
func (d *Dialog) SetButtons(ok, cancel string) {
	d.ok.Label, d.cancel.Label = ok, cancel
}

// AddButton adds a button left of Cancel, which closes the dialog and
// sends the intent onClick makes, as a third answer to a question: Leave
// It beside Replace and Stop. Add buttons before mounting the dialog.
func (d *Dialog) AddButton(label string, onClick func(u *gunim.UI) gunim.Intent) {
	b := NewButton(label)
	b.OnClick = func(u *gunim.UI) gunim.Intent {
		d.finish(u, onClick(u))
		return nil
	}
	d.extra = append(d.extra, b)
}

// AddAction adds a button left of Cancel that runs onClick and leaves the
// dialog open, for help with the form, such as generating a password. A
// non-nil result is sent as the dialog's intent. Add buttons before
// mounting the dialog.
func (d *Dialog) AddAction(label string, onClick func(u *gunim.UI) gunim.Intent) {
	b := NewButton(label)
	b.OnClick = func(u *gunim.UI) gunim.Intent {
		send(u, d, onClick(u))
		return nil
	}
	d.extra = append(d.extra, b)
}

// Close closes the dialog and sends nothing, for an action that leads
// on to another dialog, such as a Remove button in a form that asks
// first.
func (d *Dialog) Close(u *gunim.UI) { d.finish(u, nil) }

func (d *Dialog) accept(u *gunim.UI) {
	if d.Check != nil {
		if msg := d.Check(); msg != "" {
			d.problem.Text = msg
			// A kick sideways that a springy motion rings down to rest.
			d.shake.Jump(1)
			d.shake.Animate(0, anim.Spring{Response: 0.18, Damping: 0.12})
			u.Invalidate()
			return
		}
	}
	d.problem.Text = ""
	var what gunim.Intent
	if d.OnAccept != nil {
		what = d.OnAccept(u)
	}
	d.finish(u, what)
}

// focusables returns what Tab moves through: the body's fields, then
// the buttons, left to right. With DefaultFirst, the buttons from the
// right instead, OK first.
func (d *Dialog) focusables() []gunim.Node {
	var out []gunim.Node
	if b, ok := d.Body.(interface{ Focusables() []gunim.Node }); ok {
		out = append(out, b.Focusables()...)
	}
	if d.DefaultFirst {
		buttons := d.buttons()
		for i := len(buttons) - 1; i >= 0; i-- {
			out = append(out, buttons[i])
		}
		for i := len(d.extra) - 1; i >= 0; i-- {
			out = append(out, d.extra[i])
		}
		return out
	}
	for _, b := range d.extra {
		out = append(out, b)
	}
	return append(out, d.buttons()...)
}

// buttons are Cancel, when it has a label, and OK.
func (d *Dialog) buttons() []gunim.Node {
	if d.cancel.Label == "" {
		return []gunim.Node{d.ok}
	}
	return []gunim.Node{d.cancel, d.ok}
}

// Children implements [gunim.Composite], so mounting the dialog brings
// its buttons with it.
//
// Layout places them from the right, so the last one, OK, lands
// outermost.
func (d *Dialog) Children() []gunim.Node {
	var out []gunim.Node
	if d.Body != nil {
		out = append(out, d.held(), d.problem)
	}
	for _, b := range d.extra {
		out = append(out, b)
	}
	return append(out, d.buttons()...)
}

// held is the body as the dialog holds it: in a scroll, so a body too
// tall for the window scrolls within the room there is, rather than run
// over the buttons and off the panel.
func (d *Dialog) held() gunim.Node {
	if s, ok := d.Body.(*Scroll); ok {
		return s
	}
	if d.scroll == nil || d.scroll.child != d.Body {
		d.scroll = NewScroll(d.Body)
	}
	return d.scroll
}

// finish closes the dialog and tells the application what happened.
//
// The close lands first and locally, so the fade starts on this frame.
// The intent goes out behind it, and the application reads it whenever
// it is ready.
func (d *Dialog) finish(u *gunim.UI, what gunim.Intent) {
	u.Remove(d)
	if what != nil {
		u.Send(d, what)
	}
}

// Handle implements [gunim.Handler]. Escape dismisses.
func (d *Dialog) Handle(e input.Event, u *gunim.UI) bool {
	if d.Keys != nil {
		switch e.(type) {
		case input.KeyPress, input.TextInput:
			if d.Keys(e, u) {
				return true
			}
		}
	}
	if k, ok := e.(input.KeyPress); ok {
		switch k.Key {
		case input.KeyEscape:
			d.dismiss(u)
			return true
		case input.KeyEnter, input.KeyKPEnter:
			d.accept(u)
			return true
		case input.KeyTab:
			// A modal keeps focus among its own buttons.
			d.cycle(u, !k.Mods.Has(input.ModShift))
			return true
		case input.KeyLeft, input.KeyRight, input.KeyUp, input.KeyDown:
			// From a button, the arrows go along the row of buttons, as
			// in the system's own dialogs. A field keeps its arrows.
			if d.step(u, k.Key == input.KeyRight || k.Key == input.KeyDown) {
				return true
			}
		default:
		}
	}
	// Given the keyboard as it opens, the dialog hands it to its first
	// field, or to Cancel when it is a danger dialog.
	if _, ok := e.(input.FocusGained); ok && !d.focused {
		d.focused = true
		if d.Danger || d.Careful {
			// Cancel has the keyboard, and Enter presses it rather than
			// the action the dialog stands out with: its ring says so,
			// however the dialog was opened.
			u.Focus(d.buttons()[0])
			u.ShowFocusRing()
			return true
		}
		if b, ok := d.Body.(interface{ Focusables() []gunim.Node }); ok {
			if f := b.Focusables(); len(f) > 0 {
				u.Focus(f[0])
			}
		}
		return true
	}
	// A modal swallows the pointer events that reach it, keeping clicks
	// off whatever lies behind, and keeps the window's menus shut.
	switch e := e.(type) {
	case input.PointerDown, input.PointerUp, input.Scroll, input.AltTapped:
		return true
	case input.KeyPress:
		return e.Key == input.KeyF10 || e.Mods.Has(input.ModAlt)
	}
	return false
}

// Modal implements [gunim.Modal]: the dialog holds the keyboard while it is open.
func (d *Dialog) Modal() bool { return true }

// Focusable implements [gunim.Focusable]. A click on the dialog's
// empty space focuses the dialog itself, so Escape and Tab still reach
// it.
func (d *Dialog) Focusable() bool { return true }

// Buttons are the dialog's buttons, left to right as they stand: its
// extra ones, then Cancel, then OK.
func (d *Dialog) Buttons() []gunim.Node { return d.row() }

// row is the dialog's buttons, left to right.
func (d *Dialog) row() []gunim.Node {
	var out []gunim.Node
	for _, b := range d.extra {
		out = append(out, b)
	}
	return append(out, d.buttons()...)
}

// step moves focus from the button that has it to the next along the
// row, or the one before, wrapping around, and shows the focus ring. It
// reports false while no button has the keyboard.
func (d *Dialog) step(u *gunim.UI, forward bool) bool {
	row := d.row()
	at := slices.Index(row, u.Focused())
	if at < 0 {
		return false
	}
	next := (at + 1) % len(row)
	if !forward {
		next = (at - 1 + len(row)) % len(row)
	}
	u.Focus(row[next])
	u.ShowFocusRing()
	return true
}

// cycle moves focus to the next of the dialog's buttons, or the
// previous, wrapping around.
func (d *Dialog) cycle(u *gunim.UI, forward bool) {
	kids := d.focusables()
	at := -1
	for i, k := range kids {
		if k == u.Focused() {
			at = i
		}
	}
	next := 0
	switch {
	case at < 0 && !forward:
		next = len(kids) - 1
	case at >= 0 && forward:
		next = (at + 1) % len(kids)
	case at >= 0:
		next = (at - 1 + len(kids)) % len(kids)
	}
	u.Focus(kids[next])
	// A field out of sight in a body that scrolls, such as a long form,
	// is brought into view, as Tab does outside a dialog.
	u.Reveal(kids[next])
}

// Layout implements [gunim.Node]. The dialog fills the space it is
// given and centres a panel inside it, so the scrim covers the window
// while the panel keeps its own size.
func (d *Dialog) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	th := f.Theme
	pad := DialogPadding.Get(th)
	d.ok.Kind = ButtonPrimary
	if d.Danger {
		d.ok.Kind = ButtonDanger
	}
	// As wide as the theme says, or as the buttons need in a row,
	// whichever is wider, and no wider than the window.
	first := 0
	if d.Body != nil {
		first = 2
	}
	gap := DialogGap.Get(th)
	row := -gap
	for i := first; i < kids.Len(); i++ {
		row += kids.At(i).Layout(gunim.Loose(size)).W + gap
	}
	want := DialogWidth.Get(th)
	if d.Width > 0 {
		want = d.Width
	}
	width := min(max(want, row+2*pad), size.W)
	d.width = width
	// The panel grows to fit the title bar, the body and the buttons.
	bar := d.barHeight(th)
	var body, problem gunim.Child
	hasBody := d.Body != nil
	var bs, ps geom.Size
	around := bar + pad + ButtonHeight.Get(th) + pad
	if hasBody {
		body, problem = kids.At(0), kids.At(1)
		bs = body.Layout(gunim.Constraints{Max: geom.Sz(width-2*pad, 0)})
		ps = problem.Layout(gunim.Constraints{Max: geom.Sz(width-2*pad, 0)})
		around += pad
		if d.problem.Text != "" {
			around += pad/2 + ps.H
		}
		// A body too tall to leave the dialog its margin is measured
		// again with the room that is left, so a form that scrolls
		// fills it instead of running off both ends of the screen, or
		// up to the window's very edge, under a title bar drawn there.
		if room := size.H - DialogMargin.Get(th) - around; around+bs.H > size.H-DialogMargin.Get(th) && room > 0 {
			bs = body.Layout(gunim.Tight(geom.Sz(width-2*pad, room)))
		}
	}
	d.height = around + bs.H
	panel := d.panel(size, f)
	if hasBody {
		at := panel.Min.Add(geom.Pt(pad, bar+pad))
		body.Place(at)
		problem.Place(at.Add(geom.Pt(0, bs.H+pad/2)))
	}

	// Buttons sit along the bottom right of the panel, laid out from the
	// right so the primary action lands outermost.
	x := panel.Max.X - pad
	y := panel.Max.Y - pad
	for i := kids.Len() - 1; i >= first; i-- {
		kid := kids.At(i)
		s := kid.Layout(gunim.Loose(panel.Size()))
		x -= s.W
		kid.Place(geom.Pt(x, y-s.H))
		x -= DialogGap.Get(f.Theme)
	}
	return size
}

// panel returns the dialog's own rectangle, centred in size.
func (d *Dialog) panel(size geom.Size, f gunim.Frame) geom.Rect {
	w, h := d.width, d.height
	if w <= 0 {
		w = DialogWidth.Get(f.Theme)
	}
	if h <= 0 {
		h = DialogHeight.Get(f.Theme)
	}
	return geom.Rc((size.W-w)/2, (size.H-h)/2, w, h)
}

// Transition implements [gunim.Transitioner]. The dialog springs in
// with a slight overshoot, and settles out without one.
func (d *Dialog) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		d.in.Animate(1, Bounce.Get(f.Theme))
	case gunim.Exiting:
		d.in.Animate(0, Settle.Get(f.Theme))
	case gunim.Present:
		// Settled in.
	}
	return !d.in.Active()
}

// Paint implements [gunim.Node].
func (d *Dialog) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	t := d.in.Value()
	if t <= 0 {
		return
	}
	// The spring overshoots on the way in. The scale takes the
	// overshoot, and everything that fades stops at fully opaque.
	fade := min(t, 1)

	// The scrim darkens whatever is behind the dialog and blurs it. The
	// blur radius is tied to the same value as the fade, so the
	// background comes back into focus as the dialog leaves.
	dim := Scrim.Get(th)
	dim.A = uint8(float32(dim.A) * fade)
	full := geom.Rect{Max: box.Point()}
	closeScrim := p.Layer(paint.LayerOpts{Bounds: full, Opacity: fade, Backdrop: DialogBackdrop.Get(th) * fade})
	p.RRect(full, 0, paint.Solid(dim))
	closeScrim()

	panel := d.panel(box, f)
	radius := DialogRadius.Get(th)
	// A shake swings the panel from side to side.
	defer p.Push(paint.Translate(geom.Pt(14*d.shake.Value(), 0)))()

	// The panel fades and grows into place together. Starting at 0.94
	// makes it read as arriving; starting nearer 0 would make it read
	// as being inflated.
	defer p.Layer(paint.LayerOpts{Bounds: panel, Opacity: fade})()
	defer p.Push(paint.Scale(0.94+0.06*t, panel.Center()))()

	shadow := DialogShadow.Get(th)
	shadow.A = uint8(float32(shadow.A) * fade)
	p.ShadowRRect(panel, radius, paint.Solid(DialogFill.Get(th)), paint.Shadow{
		Offset: geom.Pt(0, 8*fade),
		Blur:   32 * fade,
		Color:  shadow,
	})
	d.paintBar(p, th, panel, radius)
	p.RRectStroke(panel, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: DialogBorder.Get(th)})
	if DialogBorderLines.Get(th) >= 2 {
		// A second rule just inside the first, as a double-line box.
		const gap = 3
		inner := geom.Rect{Min: geom.Pt(panel.Min.X+gap, panel.Min.Y+gap), Max: geom.Pt(panel.Max.X-gap, panel.Max.Y-gap)}
		p.RRectStroke(inner, max(radius-gap, 0), paint.Fill{}, paint.Stroke{Width: 1, Color: DialogBorder.Get(th)})
	}

	for kid := range kids.All {
		kid.Paint(p)
	}
}

// mark returns the icon before the title and its colour, or nil.
func (d *Dialog) mark() (*icon.Icon, theme.Token[color.NRGBA]) {
	ic, ink := d.Icon, Ink
	if d.Danger {
		ink = DialogDangerInk
		if ic == nil {
			ic = icon.TriangleAlert
		}
	}
	return ic, ink
}

// iconRoom is the room the icon takes before the title, with its gap.
func (d *Dialog) iconRoom(th *theme.Live) float32 {
	if ic, _ := d.mark(); ic == nil {
		return 0
	}
	return IconSize.Get(th) + IconGap.Get(th)
}

// hasBar reports whether the dialog shows a title bar: it has a title,
// and the host has not left the bar out.
func (d *Dialog) hasBar() bool { return d.Title != "" && !d.NoTitleBar }

// barHeight is the title bar's height, or 0 without one: a compact
// window title bar's, or four fifths of a button's where the theme's
// buttons stand taller, so the bar holds its own beside them.
func (d *Dialog) barHeight(th *theme.Live) float32 {
	if !d.hasBar() {
		return 0
	}
	return max(TitleBarCompactHeight.Get(th), 0.8*ButtonHeight.Get(th))
}

// title lays the title out on one line, for a panel width wide, after
// the icon: the text a window's title bar shows, in its size.
func (d *Dialog) title(th *theme.Live, width float32) text.Paragraph {
	style := text.Style{Size: TextSize.Get(th), MaxLines: 1}
	return d.titleText.layout(faceIn(Font, th), d.Title, style, max(0, width-2*DialogPadding.Get(th)-d.iconRoom(th)))
}

// paintBar paints the title bar across the top of panel, whose corners
// are rounded by radius: the bar's fill, in the colour of a window's
// title bar, cut to the panel's round top corners, and the icon and
// title centred in it. No line sets it off: its fill, darker than the
// panel's, does.
func (d *Dialog) paintBar(p *paint.Painter, th *theme.Live, panel geom.Rect, radius float32) {
	if !d.hasBar() {
		return
	}
	h := d.barHeight(th)
	fill := DialogFill.Get(th)
	strip := geom.Rect{Min: panel.Min, Max: geom.Pt(panel.Max.X, panel.Min.Y+h)}
	if radius <= h && fill.A == 0xff {
		// The bar with every corner round, as tall again as the
		// corners, and the panel's fill painted back over the part
		// below the bar: the bottom corners go, and the top ones stay
		// round with the panel's.
		p.RRect(geom.Rect{Min: strip.Min, Max: geom.Pt(strip.Max.X, strip.Max.Y+radius)}, radius, paint.Solid(MenubarFill.Get(th)))
		p.RRect(geom.Rect{Min: geom.Pt(strip.Min.X, strip.Max.Y), Max: geom.Pt(strip.Max.X, strip.Max.Y+radius)}, 0, paint.Solid(fill))
	} else {
		// Corners rounder than the bar is tall: the bar cut to the
		// panel's shape.
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: panel, Opacity: 1, Clip: true, Radius: radius})()
			p.RRect(strip, 0, paint.Solid(MenubarFill.Get(th)))
		}()
	}
	title := d.title(th, panel.Size().W)
	room := d.iconRoom(th)
	x := float32(math.Round(float64(strip.Min.X + (strip.Size().W-room-title.Size.W)/2)))
	y := float32(math.Round(float64(strip.Min.Y + (h-title.Size.H)/2)))
	if ic, ink := d.mark(); ic != nil {
		s := IconSize.Get(th)
		paintIcon(p, th, ic, geom.Rc(x, strip.Min.Y+(h-s)/2, s, s), ink.Get(th), 1)
	}
	title.Paint(p, geom.Pt(x+room, y), Ink.Get(th))
}
