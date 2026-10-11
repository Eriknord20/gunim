package main

import (
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/calendar"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// cardW is how wide an event's card is.
const cardW = 340

// cardIndent lines a row without an icon up with the text of the rows that have one.
var cardIndent = theme.Insets("cal.card.indent", geom.Insets{Left: 32})

// eventCard is the card about an event, beside it: a square of its calendar's colour, its title, when it is, how it
// repeats, where, its calendar, who invited the user and its notes. Buttons at the top edit it, delete it and close
// the card; an invitation has buttons to answer it at the foot. Escape closes the card, Delete deletes the event,
// and Enter or E edits it. The card grows out of the side it opens on.
type eventCard struct {
	popCard
	id    string
	fixed bool
	done  func(*gunim.UI)
}

// newEventCard makes the card about e. The buttons run done as they act.
func newEventCard(e calendar.Event, d Details, left bool, done func(*gunim.UI)) *eventCard {
	c := &eventCard{id: e.ID, fixed: e.Fixed, done: done}
	act := func(ic *icon.Icon, tip string, v gunim.Intent) *widget.IconButton {
		b := widget.NewIconButton(ic, tip)
		b.OnClick = func(u *gunim.UI) gunim.Intent {
			if v != nil {
				u.Send(c, v)
			}
			done(u)
			return nil
		}
		return b
	}
	var tools []gunim.Node
	if !e.Fixed {
		tools = append(tools, act(icon.Pencil, "Edit (E)", EditAsked{ID: e.ID}),
			act(icon.Trash2, "Delete (Delete)", DeleteAsked{ID: e.ID}))
	}
	tools = append(tools, act(icon.X, "Close (Esc)", nil))
	spacer := widget.NewSpacer()
	top := widget.Row(append([]gunim.Node{spacer}, tools...)...).Grow(spacer, 1)
	top.Cross = widget.CrossCenter

	title := widget.NewLabel(e.Title)
	title.Face, title.Size, title.MaxLines = widget.BoldFont, CardTitle, 3
	day, hours := when2(e)
	rows := []gunim.Node{top, &titleRow{color: e, title: title}, line(icon.Clock, widget.NewLabel(day))}
	if hours != "" {
		h := widget.NewLabel(hours)
		h.Color = widget.PaletteHint
		rows = append(rows, indent(h))
	}
	if d.Repeat != Never {
		rows = append(rows, line(icon.Repeat, widget.NewLabel(repeatNames[d.Repeat])))
	}
	if e.Location != "" {
		rows = append(rows, line(icon.MapPin, widget.NewLabel(e.Location)))
	}
	rows = append(rows, &calLine{name: d.Calendar, color: e})
	if d.From != "" {
		answer := map[Answer]string{NoAnswer: "waiting for your answer", Going: "you are going", NotGoing: "you are not going"}
		rows = append(rows, line(icon.Mail, widget.NewLabel("Invited by "+d.From+", "+answer[d.Answer])))
	}
	if d.Notes != "" {
		n := widget.NewLabel(d.Notes)
		n.MaxLines, n.Color = 6, widget.PaletteHint
		rows = append(rows, line(icon.AlignLeft, n))
	}
	if d.Answer != NotInvited {
		yes := widget.NewButton("Going")
		yes.Icon = icon.Check
		if d.Answer != Going {
			yes.Kind = widget.ButtonPrimary
		}
		yes.OnClick = func(u *gunim.UI) gunim.Intent {
			u.Send(c, Answered{ID: e.ID, Answer: Going})
			done(u)
			return nil
		}
		no := widget.NewButton("Not going")
		no.Icon = icon.X
		no.OnClick = func(u *gunim.UI) gunim.Intent {
			u.Send(c, Answered{ID: e.ID, Answer: NotGoing})
			done(u)
			return nil
		}
		rows = append(rows, indent(widget.Row(yes, no)))
	}
	col := widget.Column(rows...)
	col.Cross = widget.CrossStretch
	c.popCard = newPopCard(widget.NewPad(col), cardW, left)
	return c
}

// when2 says when e is: its day or days, and its hours, or "All day" for an event of whole days.
func when2(e calendar.Event) (day, hours string) {
	const layout = "Monday 2 January"
	last := calendar.Day(e.End.Add(-time.Nanosecond))
	switch {
	case e.AllDay && calendar.SameDay(e.Start, last):
		return e.Start.Format(layout), "All day"
	case e.AllDay:
		return e.Start.Format(layout) + " – " + last.Format(layout), "All day"
	case calendar.SameDay(e.Start, e.End) || e.End.Equal(calendar.AddDays(calendar.Day(e.Start), 1)):
		return e.Start.Format(layout), e.Start.Format("15:04") + " – " + e.End.Format("15:04") + " · " + lasting(e.End.Sub(e.Start))
	}
	return e.Start.Format(day+", 15:04") + " –", e.End.Format(day + ", 15:04")
}

// when says when e is on one line, such as "Tuesday 29 September · 09:15 – 09:30 · 15 min".
func when(e calendar.Event) string {
	d, h := when2(e)
	if h == "All day" {
		return d
	}
	return d + " · " + h
}

// lasting says how long d is, such as "1 h 30 min".
func lasting(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return strconv.Itoa(m) + " min"
	case m == 0:
		return strconv.Itoa(h) + " h"
	}
	return strconv.Itoa(h) + " h " + strconv.Itoa(m) + " min"
}

// line is a row of the card: an icon and what it says.
func line(ic *icon.Icon, l *widget.Label) gunim.Node {
	if l.MaxLines == 0 {
		l.MaxLines = 2
	}
	r := widget.Row(widget.NewIcon(ic, ""), l).Grow(l, 1)
	r.Cross = widget.CrossStart
	return r
}

// indent lines n up with the text of the rows that have an icon.
func indent(n gunim.Node) gunim.Node {
	p := widget.NewPad(n)
	p.Padding = cardIndent
	return p
}

// Focusable implements [gunim.Focusable]: the card takes the keyboard as it opens, for its keys.
func (c *eventCard) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (c *eventCard) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	switch {
	case k.Key == input.KeyEscape:
		c.done(u)
	case (k.Key == input.KeyDelete || k.Key == input.KeyBackspace) && !c.fixed:
		u.Send(c, DeleteAsked{ID: c.id})
		c.done(u)
	case (k.Key == input.KeyEnter || k.Key == input.KeyE) && !c.fixed:
		u.Send(c, EditAsked{ID: c.id})
		c.done(u)
	default:
		return false
	}
	return true
}

// titleRow is the card's title beside a square of the event's calendar's colour.
type titleRow struct {
	color calendar.Event
	title *widget.Label
}

// Children implements [gunim.Composite].
func (r *titleRow) Children() []gunim.Node { return []gunim.Node{r.title} }

// Layout implements [gunim.Node].
func (r *titleRow) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	lead := widget.IconSize.Get(f.Theme) + 8
	kid := kids.At(0)
	s := kid.Layout(gunim.Constraints{Max: geom.Sz(cs.Max.W-lead, cs.Max.H)})
	kid.Place(geom.Pt(lead, 0))
	return geom.Sz(cs.Max.W, s.H)
}

// Paint implements [gunim.Node].
func (r *titleRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	s := widget.IconSize.Get(f.Theme)
	p.RRect(geom.Rc(2, 5, s-4, s-4), 4, paint.Solid(r.color.Color))
	kids.At(0).Paint(p)
}

// calLine is the card's row naming the event's calendar, with a dot of its colour.
type calLine struct {
	name  string
	color calendar.Event
}

// Layout implements [gunim.Node].
func (c *calLine) Layout(cs gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	return geom.Sz(cs.Max.W, widget.ControlHeight.Get(f.Theme))
}

// Paint implements [gunim.Node].
func (c *calLine) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	s := widget.IconSize.Get(th)
	p.RRect(geom.Rc(s/2-5, box.H/2-5, 10, 10), 5, paint.Solid(c.color.Color))
	t := widget.Font.Get(th).Shape(c.name, widget.TextSize.Get(th))
	t.Paint(p, geom.Pt(s+8, (box.H-t.Height())/2), widget.Ink.Get(th))
}
