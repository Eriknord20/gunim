package dock

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

// Contrast sets the dock's colours for a contrast theme, to add to widget.Contrast's, as Light is added to
// widget.Light. Bars take the window and titles the window text. The tab in front, a hovered tab and a drop's place
// take a tint of the highlight, and marks, rings and the divider under the pointer the highlight.
func Contrast(c gunim.ContrastColors) []theme.Entry {
	tint := c.Highlight
	tint.A = 0x50
	return []theme.Entry{
		theme.Set(BarFill, c.Window),
		theme.Set(TabFront, tint),
		theme.Set(TabHover, tint),
		theme.Set(TabInk, c.WindowText),
		theme.Set(TabFaint, c.WindowText),
		theme.Set(TabMark, c.Highlight),
		theme.Set(Divider, c.WindowText),
		theme.Set(DividerHot, c.Highlight),
		theme.Set(DropFill, tint),
		theme.Set(DropRing, c.Highlight),
		theme.Set(EmptyInk, c.WindowText),
	}
}
