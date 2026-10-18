package dock

import (
	"image/color"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/theme"
)

// Theme tokens for the dock. Text, the accent and the motions come from
// package widget's tokens.
var (
	// BarHeight is how tall a bar of tabs is over a pane, and BarSpace
	// the room between the bar and what its tab in front shows.
	BarHeight = theme.Length("dock.bar.height", 30)
	BarSpace  = theme.Length("dock.bar.space", 4)
	// BarFill is a bar's background, and BarRadius rounds it.
	BarFill   = theme.Color("dock.bar.fill", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xe6})
	BarRadius = theme.Length("dock.bar.radius", 10)
	// TabText is the size of a tab's title, and TabBold draws it in
	// package widget's BoldFont, rather than its Font.
	TabText = theme.Length("dock.tab.text", 12.5)
	TabBold = theme.Choice("dock.tab.bold", false)
	// TabIcon is how large a tab's icon is.
	TabIcon = theme.Length("dock.tab.icon", 16)
	// TabPadding is the room each side of a tab's insides, TabGap the
	// room between tabs, and TabInset how far in from the bar's edges
	// the tabs stand.
	TabPadding = theme.Length("dock.tab.padding", 10)
	TabGap     = theme.Length("dock.tab.gap", 3)
	TabInset   = theme.Length("dock.tab.inset", 3)
	// TabWidest and TabLeast are the widest and the narrowest a tab
	// gets: one with a long title cuts it, and many in little room share
	// it alike down to TabLeast.
	TabWidest = theme.Length("dock.tab.widest", 220)
	TabLeast  = theme.Length("dock.tab.least", 72)
	// TabRadius rounds a tab.
	TabRadius = theme.Length("dock.tab.radius", 7)
	// TabFront fills the tab in front, gliding between tabs as the one
	// in front changes, and TabHover the tab under the pointer.
	TabFront = theme.Color("dock.tab.front", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x26})
	TabHover = theme.Color("dock.tab.hover", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10})
	// TabInk is a tab's title and icon, and TabFaint those of a tab
	// behind.
	TabInk   = theme.Foreground("dock.tab.ink", color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0xff})
	TabFaint = theme.Foreground("dock.tab.faint", color.NRGBA{R: 0x9a, G: 0xa1, B: 0xb0, A: 0xff})
	// TabMark underlines the tab in front of the pane the keys work in,
	// where the window shows more than one pane.
	TabMark = theme.Color("dock.tab.mark", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff})
	// Gap is the room between panes, which the pointer grabs to move
	// the divider in it.
	Gap = theme.Length("dock.gap", 8)
	// Divider is the line in the middle of a gap, and DividerHot the
	// line under the pointer or dragged.
	Divider    = theme.Color("dock.divider", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16})
	DividerHot = theme.Color("dock.divider.hot", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xc0})
	// DropFill tints where a tab dragged would land, and DropRing rings
	// it.
	DropFill = theme.Color("dock.drop.fill", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x24})
	DropRing = theme.Color("dock.drop.ring", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xb0})
	// EmptyInk is the words an empty window shows, EmptyText says them.
	EmptyInk  = theme.Foreground("dock.empty.ink", color.NRGBA{R: 0x8a, G: 0x90, B: 0x9c, A: 0xff})
	EmptyText = theme.Choice("dock.empty.text", "Drag a tab here")
	// DockHere, SplitHere and MoveHere are what a drop target answers a
	// tab dragged over it: joining a group, splitting a pane, moving to
	// another window's bar.
	DockHere  = theme.Choice("dock.drop.tab", "Dock here")
	SplitHere = theme.Choice("dock.drop.split", "Split here")
	// ShareLeast is the least share of a split's room either side keeps,
	// as a divider is dragged and as the layout sets it. Dragged, a
	// side keeps this share or the 48 pixels a pane needs, whichever is
	// larger.
	ShareLeast = theme.Number("dock.share.least", 0)
	// EdgeShare is how far into a pane, as a share of its width or
	// height, a tab dragged splits it rather than joining its group.
	EdgeShare = theme.Number("dock.edge", 0.25)
	// Slide is how tabs slide, open and close, and how a split slides
	// open; Glide is how the front tab's fill moves to the tab picked.
	Slide = theme.Spring("dock.slide", anim.Spring{Response: 0.28, Damping: 1})
	Glide = theme.Spring("dock.glide", anim.Spring{Response: 0.16, Damping: 1})
)

// Light sets the dock's colours for a light theme, to add to one, as
// widget.Light().With(dock.Light...).
var Light = []theme.Entry{
	theme.Set(BarFill, color.NRGBA{R: 0xee, G: 0xf0, B: 0xf4, A: 0xf0}),
	theme.Set(TabFront, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x1c}),
	theme.Set(TabHover, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x0a}),
	theme.Set(TabInk, color.NRGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0xff}),
	theme.Set(TabFaint, color.NRGBA{R: 0x5c, G: 0x63, B: 0x70, A: 0xff}),
	theme.Set(TabMark, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
	theme.Set(Divider, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x18}),
	theme.Set(DividerHot, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xc0}),
	theme.Set(DropFill, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x1c}),
	theme.Set(DropRing, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xb0}),
	theme.Set(EmptyInk, color.NRGBA{R: 0x6a, G: 0x70, B: 0x7c, A: 0xff}),
}
