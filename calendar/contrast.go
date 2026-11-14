package calendar

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// HotFill is the band under the days a [MiniMonth] marks and the fill of a [Month]'s event under the pointer, where a
// theme sets it, as a contrast theme does, and else [widget.MenuHot].
var HotFill = theme.Color("calendar.hot", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x48})

// hotFill is [HotFill] in th where th sets it, and else [widget.MenuHot].
func hotFill(th *theme.Live) color.NRGBA {
	if th.Sets(HotFill.Key()) {
		return HotFill.Get(th)
	}
	return widget.MenuHot.Get(th)
}

// Contrast sets the calendar's colours for a contrast theme, to add to [widget.Contrast]'s, as Light is added to
// [widget.Light]. Fills under the days take a tint of the highlight, and the line at the time it is now takes the
// window text.
func Contrast(c gunim.ContrastColors) []theme.Entry {
	tint := c.Highlight
	tint.A = 0x50
	none := color.NRGBA{}
	return []theme.Entry{
		theme.Set(NowInk, c.WindowText),
		theme.Set(TodayFill, tint),
		theme.Set(WeekendFill, none),
		theme.Set(OffHoursFill, none),
		theme.Set(OtherMonthFill, none),
		theme.Set(EventBase, c.Window),
		theme.Set(HotFill, tint),
	}
}
