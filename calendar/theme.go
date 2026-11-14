package calendar

import (
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
)

// Theme tokens for the calendar's views. Lines, text and the accent come from package widget's tokens.
var (
	// HourHeight is how tall an hour is in a [Days] grid.
	HourHeight = theme.Length("calendar.hour", 48)
	// NowInk is the line across today at the time it is now.
	NowInk = theme.Color("calendar.now", color.NRGBA{R: 0xf0, G: 0x5a, B: 0x4f, A: 0xff})
	// TodayFill tints today's column, and WeekendFill the columns of Saturdays and Sundays.
	TodayFill   = theme.Color("calendar.today", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x16})
	WeekendFill = theme.Color("calendar.weekend", color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x10})
	// OffHoursFill shades the hours outside a [Days] grid's working day.
	OffHoursFill = theme.Color("calendar.offhours", color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x0e})
	// OtherMonthFill tints the days of a [Month] outside the month it shows.
	OtherMonthFill = theme.Color("calendar.othermonth", color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x30})
	// EventBase is the solid colour under an event's tint, the colour of what the grid sits on.
	EventBase = theme.Color("calendar.event.base", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	// EventText is the size of the text on an event.
	EventText = theme.FontSize("calendar.event.text", 12.5)
	// EventRadius rounds an event's corners in a [Days] grid, and MonthEventRadius in a [Month], where events are a
	// line tall. A chosen event's ring rounds a little wider, round it.
	EventRadius      = theme.Length("calendar.event.radius", 6)
	MonthEventRadius = theme.Length("calendar.month.event.radius", 5)
)

// pickDay plays the select cue from n and runs onDay with day, when there is one, sending what it returns.
func pickDay(u *gunim.UI, n gunim.Node, onDay func(day time.Time, u *gunim.UI) gunim.Intent, day time.Time) {
	u.Cue(gunim.CueSelect, n)
	if onDay != nil {
		send(u, n, onDay(day, u))
	}
}

// send sends in from n, when there is one.
func send(u *gunim.UI, n gunim.Node, in gunim.Intent) {
	if in != nil {
		u.Send(n, in)
	}
}

// ringShown reports whether a view shows that it has the keyboard, for e: when e turns the rings on, and no group
// round the view draws its own.
func ringShown(e input.FocusRing) bool { return e.On && !e.Grouped }

// Light sets the calendar's tokens for a light theme, to add to one.
var Light = []theme.Entry{
	theme.Set(NowInk, color.NRGBA{R: 0xd9, G: 0x3d, B: 0x32, A: 0xff}),
	theme.Set(TodayFill, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x10}),
	theme.Set(WeekendFill, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x06}),
	theme.Set(OffHoursFill, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x05}),
	theme.Set(OtherMonthFill, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x07}),
	theme.Set(EventBase, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
}
