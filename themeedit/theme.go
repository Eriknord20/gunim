package themeedit

import (
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/theme"
)

// The editor's tokens. Its colours are the widgets' own, so it takes the
// light theme from [widget.Light] with no more values. The room it
// leaves is a settings page's: a heading over a card of rows.
var (
	// Width and Height are the editor's size where it is given no
	// bound, as in a dialog that grows to fit it.
	Width  = theme.Length("themeedit.width", 640)
	Height = theme.Length("themeedit.height", 520)
	// PagePadding is the room round the controls, and PageWidth the
	// widest they grow, so their lines stay short enough to read.
	PagePadding = theme.Insets("themeedit.page.padding", geom.Insets{Top: 20, Right: 28, Bottom: 28, Left: 28})
	PageWidth   = theme.Length("themeedit.page.width", 600)
	// HeaderPadding is the room round the header: the theme's name, the
	// count of changes, and Reset all.
	HeaderPadding = theme.Insets("themeedit.header.padding", geom.Insets{Top: 16, Right: 28, Bottom: 10, Left: 28})
	// ToolsPadding is the room round All values' search and filters.
	ToolsPadding = theme.Insets("themeedit.tools.padding", geom.Insets{Top: 20, Right: 28, Bottom: 16, Left: 28})
	// SectionGap is the room between sections, HeadingGap between a
	// section's heading and its card, and RowGap between the rows on a
	// card. DenseRowGap is RowGap in All values, where rows are many.
	SectionGap  = theme.Length("themeedit.section.gap", 22)
	HeadingGap  = theme.Length("themeedit.heading.gap", 8)
	RowGap      = theme.Length("themeedit.row.gap", 16)
	DenseRowGap = theme.Length("themeedit.row.gap.dense", 12)
	// NamesGap is the room between a row's name and the words under it,
	// and ControlGap between those words and the row's control.
	NamesGap   = theme.Length("themeedit.names.gap", 3)
	ControlGap = theme.Length("themeedit.control.gap", 24)
	// TextRoom is the least room a row keeps for its words beside its
	// control. With less, the control goes under the words.
	TextRoom = theme.Length("themeedit.text.room", 200)
	// TitleSize is the size of the header's title, and KeySize of a
	// token's key under its name in All values.
	TitleSize = theme.Length("themeedit.title.size", 16)
	KeySize   = theme.Length("themeedit.key.size", 11.5)
	// DotSize is the size of the dot after a changed row's name, and
	// DotGap the room between the name and the dot.
	DotSize = theme.Length("themeedit.dot.size", 6)
	DotGap  = theme.Length("themeedit.dot.gap", 6)
	// IconSize is the size of a row's Play and Reset icons.
	IconSize = theme.Length("themeedit.icon.size", 16)
	// NumberWidth is the width of a field for a length or a number,
	// SideWidth of each of the four for insets, and SliderWidth of the
	// slider for a value with a range.
	NumberWidth = theme.Length("themeedit.number.width", 96)
	SideWidth   = theme.Length("themeedit.side.width", 56)
	SliderWidth = theme.Length("themeedit.slider.width", 160)
	// CompactHeight and CompactWidth are the size of a field for a
	// number in All values, as tall as a swatch's button, so every row
	// there is as tall as its words.
	CompactHeight = theme.Length("themeedit.compact.height", 28)
	CompactWidth  = theme.Length("themeedit.compact.width", 80)
	// TabInset is how far in the tabs' titles start and their line
	// ends: the page's own padding, so they line up with the cards.
	TabInset = theme.Length("themeedit.tab.inset", 28)
	// SliderPadding is the room round a spring's Speed and Bounce
	// sliders, under its row.
	SliderPadding = theme.Insets("themeedit.sliders.padding", geom.Insets{Top: 10})
	// ValueWidth is the width of the field for the value beside a
	// slider.
	ValueWidth = theme.Length("themeedit.value.width", 52)
	// PreviewWidth is the width of the preview beside the controls, and
	// Narrow the editor's width below which the preview goes above them.
	PreviewWidth = theme.Length("themeedit.preview.width", 400)
	Narrow       = theme.Length("themeedit.narrow", 900)
	// Short is the height of a narrow editor below which the preview
	// starts folded away, to its heading, and opens as a motion plays.
	Short = theme.Length("themeedit.short", 760)
	// PreviewRadius rounds the window the preview draws, and
	// PreviewTitleHeight is the height of its title bar.
	PreviewRadius      = theme.Length("themeedit.preview.radius", 10)
	PreviewTitleHeight = theme.Length("themeedit.preview.title.height", 30)
	// TerminalSize is the size of the preview terminal's text.
	TerminalSize = theme.Length("themeedit.terminal.size", 12)
)
