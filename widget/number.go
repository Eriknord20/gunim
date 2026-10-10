package widget

import (
	"math"
	"strconv"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/text"
)

// NumberField is a [TextField] that holds a number.
//
// It takes typing as a text field does, and adds what a number wants:
// the up and down arrows step the value, Page Up and Page Down step it
// ten times as far, the wheel turns it while the field has the
// keyboard and the pointer is over it, and anything typed is held to Min and Max when the field is left or
// Enter is pressed.
//
// Half-typed text is left alone while the field has focus, so a value
// can be cleared and retyped without the field fighting back. The
// application hears a value only when one can be read, through
// OnChange.
//
// The number sits against the field's right edge, and the field can be
// dragged, as in a design tool: press it and move the pointer up or
// right to raise the value, down or left to lower it, Shift held for a
// tenth of the speed and Ctrl for ten times. The value keeps to Min
// and Max and to steps of Increment, OnChange hears it as it goes, and
// OnCommit when the button is let go. Escape puts the value from
// before the drag back. A click that does not move puts the caret in
// to type, and from then on presses select text, until the field is
// left. Over the field the pointer is an up and down arrow while a
// drag would change the value.
type NumberField struct {
	*TextField

	// Min and Max bound the value. Leave Max at zero for no ceiling.
	Min, Max float64
	// Increment is how far one arrow press moves the value; zero is one.
	Increment float64
	// Decimals is how many places the value is written with. Zero
	// writes whole numbers.
	Decimals int
	// Suffix is written after the number, such as " ms". It is not part
	// of what the user types.
	Suffix string
	// OnChange runs on the UI goroutine with a new value: when the text
	// can be read as a number, and again when the field is left or Enter
	// is pressed and the value had to be held to the bounds. It may act
	// in the window through u, such as a slider beside the field that
	// follows it; a non-nil result is sent to the application as the
	// field's intent. It shadows the text field's own OnChange, which
	// takes a string and which the number field uses itself.
	OnChange func(v float64, u *gunim.UI) gunim.Intent
	// OnCommit runs on the UI goroutine with the value when an edit is
	// done: Enter is pressed, or a drag that changed the value is let
	// go. It shadows the text field's own OnCommit, which takes a
	// string. A non-nil result is sent to the application as the
	// field's intent.
	OnCommit func(v float64, u *gunim.UI) gunim.Intent
	// NoDrag keeps the field to typing, the arrows and the wheel, for a
	// number such as a port that has no sense of near and far.
	NoDrag bool
	// DragRate is how far the value moves for each pixel the pointer is
	// dragged. Zero picks a rate from Increment and the range: a step
	// every few pixels, quicker where that would make a short range a
	// long drag.
	DragRate float64

	value float64
	// typing says a click put the caret in, or keys were typed, since
	// the field took the keyboard: presses then select text rather than
	// drag.
	typing bool
	drag   numberDrag
	// pressed runs from 0 to 1 as the field is pressed to drag it.
	pressed *anim.Float
}

// numberDrag is a press held on a number field, which becomes a drag
// once it moves far enough.
type numberDrag struct {
	// held says a primary press is down, moving says it has moved past
	// dragSlop and changes the value, and called says Escape called the
	// drag off, so the rest of the press does nothing.
	held, moving, called bool
	// down is the press, handed to the text field if it turns out a
	// click. last is where the pointer was at the last move.
	down input.PointerDown
	last geom.Point
	// vertical says the drag follows the pointer up and down, chosen by
	// which way it first moved further.
	vertical bool
	// was is the value before the press, and raw the value the pointer
	// has dragged to before it is held to a step.
	was, raw float64
}

// How a drag turns pixels into a value.
const (
	// dragSlop is how far, in pixels, a press moves before it is a
	// drag, not a click.
	dragSlop = 3
	// dragPixelsPerStep is how many pixels a drag takes to move one
	// Increment.
	dragPixelsPerStep = 4
	// dragAcross is how many pixels a drag takes, at most, to cross a
	// short range end to end. A range of more than dragLongRange steps
	// is not a range to cross, as a port's or a field with no real
	// bounds, and keeps to a step every dragPixelsPerStep.
	dragAcross    = 400
	dragLongRange = 10000
)

// NewNumberField returns a field holding lo, bounded by lo and hi.
func NewNumberField(lo, hi float64) *NumberField {
	n := &NumberField{TextField: NewTextField(), Min: lo, Max: hi, value: lo, pressed: anim.NewFloat(0)}
	n.Align = text.AlignEnd
	n.TextField.pressed = n.pressed
	n.Add(n.pressed)
	n.SetText(n.format(lo), nil)
	n.TextField.OnChange = func(s string, u *gunim.UI) gunim.Intent {
		v, ok := parseNumber(s)
		if !ok {
			return nil
		}
		// While typing, the value is taken as it reads. Holding it to
		// the bounds mid-word would fight the fingers: typing 1 on the
		// way to 12 would jump to the minimum.
		n.value = v
		if n.OnChange != nil {
			send(u, n, n.OnChange(v, u))
		}
		return nil
	}
	// The text field's own OnChange returns nothing to send, and its
	// OnCommit stays unset: they would send from a node that is not in
	// the tree. Everything this field reports goes out from the field
	// itself.
	return n
}

// Value returns the number the field holds.
func (n *NumberField) Value() float64 { return n.value }

// Dragging reports whether the field is being dragged, so an OnChange
// can tell a value on the way from one the user has settled on.
func (n *NumberField) Dragging() bool { return n.drag.moving }

// SetValue sets the value, held to Min and Max, rewrites the text, and
// sends no intent. u may be nil, as before the field is laid out.
func (n *NumberField) SetValue(v float64, u *gunim.UI) {
	n.value = n.clamp(v)
	n.SetText(n.format(n.value), u)
}

func (n *NumberField) clamp(v float64) float64 {
	if math.IsNaN(v) {
		return n.Min
	}
	v = max(n.Min, v)
	if n.Max > n.Min {
		v = min(n.Max, v)
	}
	return v
}

func (n *NumberField) format(v float64) string {
	return strconv.FormatFloat(v, 'f', n.Decimals, 64) + n.Suffix
}

func (n *NumberField) step() float64 {
	if n.Increment > 0 {
		return n.Increment
	}
	return 1
}

// nudge moves the value by delta steps and rewrites the text.
func (n *NumberField) nudge(delta float64, u *gunim.UI) {
	if n.clamp(n.value+delta) != n.value {
		u.Cue(gunim.CueTick, n)
	}
	n.commit(n.value+delta, u)
}

// settle holds the value to the bounds and rewrites the text, which is
// what leaving the field or pressing Enter does.
func (n *NumberField) settle(u *gunim.UI) {
	v, ok := parseNumber(n.TextField.Text())
	if !ok {
		v = n.value
	}
	n.commit(v, u)
}

// snap returns v held to a whole number of steps from Min, and to the
// places the field writes, so a drag lands on values the arrows reach.
func (n *NumberField) snap(v float64) float64 {
	s := n.step()
	v = n.Min + math.Round((v-n.Min)/s)*s
	p := math.Pow(10, float64(max(n.Decimals, 0)))
	return n.clamp(math.Round(v*p) / p)
}

// dragRate returns how far the value moves for a pixel dragged with
// mods held: Shift a tenth as far, Ctrl ten times.
func (n *NumberField) dragRate(mods input.Mods) float64 {
	r := n.DragRate
	if r <= 0 {
		s := n.step()
		r = s / dragPixelsPerStep
		if span := n.Max - n.Min; span > 0 && span/s <= dragLongRange {
			r = max(r, span/dragAcross)
		}
	}
	if mods.Has(input.ModShift) {
		r /= 10
	}
	if mods.Has(input.ModControl) {
		r *= 10
	}
	return r
}

func (n *NumberField) commit(v float64, u *gunim.UI) {
	v = n.clamp(v)
	changed := v != n.value
	n.value = v
	n.SetText(n.format(v), u)
	if !changed {
		return
	}
	if n.OnChange != nil {
		send(u, n, n.OnChange(v, u))
	}
}

// Handle implements [gunim.Handler]: the keys, the wheel and the drag
// a number wants, with everything else left to the text field
// underneath.
func (n *NumberField) Handle(e input.Event, u *gunim.UI) bool {
	if n.handleDrag(e, u) {
		return true
	}
	switch e.(type) {
	case input.TextInput, input.TextEdit, input.Composing:
		n.typing = true
	case input.FocusLost:
		n.typing = false
	}
	switch e := e.(type) {
	case input.KeyPress:
		if n.Disabled || e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) {
			break
		}
		switch e.Key {
		case input.KeyUp:
			n.nudge(n.step(), u)
			return true
		case input.KeyDown:
			n.nudge(-n.step(), u)
			return true
		case input.KeyPageUp:
			n.nudge(10*n.step(), u)
			return true
		case input.KeyPageDown:
			n.nudge(-10*n.step(), u)
			return true
		case input.KeyEnter:
			n.settle(u)
			// The field's own Handle never sees Enter, so the
			// submission is sent from here, where the node is the one
			// the tree knows.
			if n.OnCommit != nil {
				send(u, n, n.OnCommit(n.value, u))
			}
			return true
		}
	case input.Scroll:
		// Only while the field has the keyboard, so scrolling a panel
		// leaves every number the pointer passes over as it is. The
		// wheel turned up gives a positive Delta.Y, and turns the value
		// up.
		if n.Disabled || e.Delta.Y == 0 || !u.HasFocus(n) {
			break
		}
		n.nudge(math.Copysign(n.step(), float64(e.Delta.Y)), u)
		return true
	case input.FocusLost:
		n.settle(u)
	}
	return n.TextField.Handle(e, u)
}

// parseNumber reads what was typed, ignoring any suffix and taking a
// comma for a decimal point, as a European keyboard gives.
func parseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	cut := len(s)
	for i, r := range s {
		if !strings.ContainsRune("0123456789+-.,eE", r) {
			cut = i
			break
		}
	}
	s = strings.TrimSpace(strings.Replace(s[:cut], ",", ".", 1))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// handleDrag takes the presses and moves of a drag, and reports whether
// it took e. A press is held back from the text field until it either
// moves past dragSlop, and drags, or is let go where it was, and is
// handed on as the click that puts the caret in.
func (n *NumberField) handleDrag(e input.Event, u *gunim.UI) bool {
	d := &n.drag
	switch e := e.(type) {
	case input.PointerDown:
		if !n.drags() || e.Button != input.ButtonPrimary || e.Touch || n.overClear(e.Pos) {
			return false
		}
		n.showTip(e, u, n.TextField)
		*d = numberDrag{held: true, down: e, last: e.Pos, was: n.value, raw: n.value}
		n.pressed.Animate(1, Quick.Get(u.Theme()))
		n.caretless = true
		u.Invalidate()
		return true
	case input.PointerMove:
		if !d.held {
			return false
		}
		if d.called {
			return true
		}
		if !d.moving {
			dx, dy := e.Pos.X-d.down.Pos.X, e.Pos.Y-d.down.Pos.Y
			if max(abs32(dx), abs32(dy)) < dragSlop {
				return true
			}
			d.moving, d.vertical = true, abs32(dy) > abs32(dx)
		}
		// Up and right raise the value; the screen's y runs down.
		px := e.Pos.X - d.last.X
		if d.vertical {
			px = d.last.Y - e.Pos.Y
		}
		d.last = e.Pos
		d.raw = n.clamp(d.raw + float64(px)*n.dragRate(e.Mods))
		if v := n.snap(d.raw); v != n.value {
			u.Cue(gunim.CueTick, n)
			n.commit(v, u)
		}
		return true
	case input.KeyPress:
		// Escape calls a drag off whatever else is held, as Shift or
		// Ctrl for its speed.
		if e.Key != input.KeyEscape || !d.moving {
			return false
		}
		n.callOff(u)
		return true
	case input.PointerUp:
		if !d.held || e.Button != input.ButtonPrimary {
			return false
		}
		moved, called, was, down := d.moving, d.called, d.was, d.down
		n.letGo(u)
		if called {
			return true
		}
		if moved {
			if n.value != was && n.OnCommit != nil {
				send(u, n, n.OnCommit(n.value, u))
			}
			return true
		}
		// A click: the caret goes in where it landed, to type.
		n.typing = true
		n.TextField.Handle(down, u)
		n.TextField.Handle(e, u)
		return true
	case input.FocusLost:
		// The keyboard went elsewhere mid-drag, as when the window lost
		// it: the drag ends where it got to.
		if d.held {
			moved, was := d.moving, d.was
			n.letGo(u)
			if moved && n.value != was && n.OnCommit != nil {
				send(u, n, n.OnCommit(n.value, u))
			}
		}
	}
	return false
}

// drags reports whether a press on the field would start a drag.
func (n *NumberField) drags() bool { return !n.NoDrag && !n.Disabled && !n.typing }

// callOff puts the value from before the drag back, telling OnChange,
// and leaves the rest of the press doing nothing.
func (n *NumberField) callOff(u *gunim.UI) {
	n.commit(n.drag.was, u)
	n.drag.called = true
	n.drag.moving = false
	n.pressed.Animate(0, Settle.Get(u.Theme()))
	u.Invalidate()
}

// letGo ends a press: the pressed look fades and the caret comes back.
func (n *NumberField) letGo(u *gunim.UI) {
	n.drag = numberDrag{}
	n.caretless = false
	n.pressed.Animate(0, Settle.Get(u.Theme()))
	u.Invalidate()
}

// Cursor implements [gunim.CursorShaper]: an up and down arrow while a
// press would drag the value, and the text field's I-beam once a click
// has put the caret in.
func (n *NumberField) Cursor(p geom.Point) input.Cursor {
	if n.drag.held || (n.drags() && !n.overClear(p)) {
		return input.CursorResizeV
	}
	return n.TextField.Cursor(p)
}

// Access implements [gunim.Accessible]: a text field that also holds a
// number in a range, so a screen reader can read and set the value.
func (n *NumberField) Access() access.Info {
	info := n.TextField.Access()
	top := n.Max
	if top <= n.Min {
		top = math.MaxFloat64
	}
	info.Range = &access.Range{Min: n.Min, Max: top, Value: n.value, Step: n.step()}
	return info
}

// AccessAct implements [gunim.AccessActor]: a screen reader setting the
// value sets it as Enter would, held to Min and Max.
func (n *NumberField) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue || n.Disabled {
		return false
	}
	n.commit(r.Value, u)
	if n.OnCommit != nil {
		send(u, n, n.OnCommit(n.value, u))
	}
	return true
}
