package main

import (
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/calendar"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// moreW is how wide the list of a day's events is.
const moreW = 280

// moreCard lists every event of a day in the month, for a day with more than it has room for: each a line with a
// dot of its calendar's colour, its time and its title, which opens the event's card when clicked, and a link to
// the day by the hour. Escape closes it.
type moreCard struct {
	popCard
	done func(*gunim.UI)
}

// newMoreCard lists evs, the events of day. Picking one runs pick with its ID.
func newMoreCard(day time.Time, evs []calendar.Event, left bool, pick func(id string, u *gunim.UI),
	done func(*gunim.UI),
) *moreCard {
	c := &moreCard{done: done}
	title := widget.NewLabel(day.Format("Monday 2 January"))
	title.Face = widget.BoldFont
	closer := widget.NewIconButton(icon.X, "Close (Esc)")
	closer.OnClick = func(u *gunim.UI) gunim.Intent {
		done(u)
		return nil
	}
	spacer := widget.NewSpacer()
	head := widget.Row(title, spacer, closer).Grow(spacer, 1)
	head.Cross = widget.CrossCenter
	slices.SortStableFunc(evs, func(a, b calendar.Event) int {
		if a.AllDay != b.AllDay {
			return map[bool]int{true: -1, false: 1}[a.AllDay]
		}
		return a.Start.Compare(b.Start)
	})
	rows := make([]gunim.Node, 0, len(evs)+2)
	rows = append(rows, head)
	for _, e := range evs {
		label := e.Start.Format("15:04") + "  " + e.Title
		if e.AllDay {
			label = e.Title
		}
		l := widget.NewLink(label)
		l.OnClick = func(u *gunim.UI) gunim.Intent {
			pick(e.ID, u)
			return nil
		}
		rows = append(rows, &dotRow{color: e, child: l})
	}
	whole := widget.NewLink("Show the day by the hour")
	whole.Icon = icon.CalendarDays
	whole.OnClick = widget.Sends(DayOpened{Day: day})
	rows = append(rows, whole)
	col := widget.Column(rows...)
	col.Cross = widget.CrossStretch
	c.popCard = newPopCard(widget.NewPad(col), moreW, left)
	return c
}

// Focusable implements [gunim.Focusable]: the list takes the keyboard as it opens, for Escape.
func (c *moreCard) Focusable() bool { return true }

// Handle implements [gunim.Handler].
func (c *moreCard) Handle(e input.Event, u *gunim.UI) bool {
	if k, ok := e.(input.KeyPress); ok && k.Key == input.KeyEscape {
		c.done(u)
		return true
	}
	return false
}

// dotRow is a line of the list: a dot of the event's colour before what it says.
type dotRow struct {
	color calendar.Event
	child gunim.Node
}

// Children implements [gunim.Composite].
func (r *dotRow) Children() []gunim.Node { return []gunim.Node{r.child} }

// Layout implements [gunim.Node].
func (r *dotRow) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	s := kid.Layout(gunim.Constraints{Max: geom.Sz(cs.Max.W-20, cs.Max.H)})
	h := max(s.H, 24)
	kid.Place(geom.Pt(20, (h-s.H)/2))
	return geom.Sz(cs.Max.W, h)
}

// Paint implements [gunim.Node].
func (r *dotRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	dot := geom.Rc(3, box.H/2-4, 8, 8)
	c := r.color.Color
	if r.color.Faint {
		p.RRectStroke(dot, 4, paint.Fill{}, paint.Stroke{Width: 1.5, Color: c})
	} else {
		p.RRect(dot, 4, paint.Solid(c))
	}
	kids.At(0).Paint(p)
}
