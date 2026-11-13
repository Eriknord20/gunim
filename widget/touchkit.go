package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Tokens of the touch kit that every widget reads.
var (
	// Tooltips shows a control's tooltip once the pointer rests on it. [Touch] turns it off: nothing rests on a
	// touch screen to ask for one, so a control's Tooltip is only its name for a screen reader there.
	Tooltips = theme.Choice("tooltip.on", true)
	// PressGlow is the colour of the soft glow that spreads from a finger as it presses a control, brightest where
	// the finger landed.
	PressGlow = theme.Color("press.glow", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x2c})
	// FigureSize is the size of one big figure on a screen, such as a moisture level, and CaptionSize the size of
	// a caption under it.
	FigureSize  = theme.FontSize("text.figure.size", 32)
	CaptionSize = theme.FontSize("text.caption.size", 12)
	// CaptionInk is the quiet colour of a caption, which still reads at 4.5:1 on a card and on the window.
	CaptionInk = theme.Foreground("text.caption.ink", color.NRGBA{R: 0x8a, G: 0x90, B: 0x9c, A: 0xff})
)

// bounded reports whether a maximum in constraints holds a node to a size: a zero or a huge one leaves it free, as
// a row or a scroll view hands its children.
func bounded(v float32) bool { return v > 0 && v < 1e9 }

// fillOr is the size of a node that fills the room c gives it, and takes want along an axis c leaves free.
func fillOr(c gunim.Constraints, want geom.Size) geom.Size {
	size := c.Max
	if !bounded(size.W) {
		size.W = want.W
	}
	if !bounded(size.H) {
		size.H = want.H
	}
	return c.Constrain(size)
}

// Touch is the touch kit's theme: what a screen worked by a finger needs, laid over a theme as
// widget.Dark().With(widget.Touch()...). It follows the design language in WIDGETS.md.
//
//   - Every target is at least 48 logical pixels both ways, and spacing is on a grid of 8.
//   - Text is set in Inter, built into the program: 17 for body text, 24 for titles, 40 for one big figure, and 14
//     for captions. Titles and figures are semibold.
//   - Cards are rounded by 16 and buttons by 12.
//   - Tooltips are off. A finger never hovers, so nothing lights as it passes.
//
// It is a function, so a program that never calls it carries no Inter: the font is built in only where it is used.
func Touch() []theme.Entry {
	return []theme.Entry{
		theme.Set(Font, text.Inter(false)),
		theme.Set(BoldFont, text.Inter(true)),
		theme.Set(TextSize, 17),
		theme.Set(HeadingSize, 24),
		theme.Set(FigureSize, 40),
		theme.Set(CaptionSize, 14),
		theme.Set(TooltipSize, 14),
		theme.Set(Tooltips, false),

		theme.Set(ButtonHeight, 48),
		theme.Set(ButtonRadius, 12),
		theme.Set(ButtonPadding, 24),
		theme.Set(ButtonSquash, 0.05),
		theme.Set(IconSize, 24),
		theme.Set(IconGap, 8),
		theme.Set(IconPadding, 12),

		theme.Set(FieldHeight, 48),
		// A grey between the dark theme's and the light's, which edges a field at 3:1 or more on either.
		theme.Set(FieldBorder, color.NRGBA{R: 0x80, G: 0x88, B: 0x96, A: 0xff}),
		theme.Set(FieldRadius, 12),
		theme.Set(FieldPadding, 16),

		theme.Set(ControlHeight, 48),
		theme.Set(ControlGap, 16),
		theme.Set(CheckSize, 28),
		theme.Set(CheckRadius, 8),
		theme.Set(RadioSize, 28),
		theme.Set(SwitchWidth, 56),
		theme.Set(SwitchHeight, 32),
		theme.Set(KnobSize, 28),
		theme.Set(SliderTrack, 6),
		theme.Set(StatusSwitchHeight, 48),
		theme.Set(SegmentedHeight, 48),
		theme.Set(ChipHeight, 48),
		theme.Set(TabHeight, 48),

		theme.Set(MenuRowHeight, 48),
		theme.Set(MenuRowPadding, 16),
		theme.Set(MenuRadius, 12),
		theme.Set(NavRowHeight, 56),
		theme.Set(NavRadius, 12),
		theme.Set(NavPadding, 16),
		theme.Set(TableRowHeight, 48),
		theme.Set(TreeRowHeight, 48),
		theme.Set(PaletteRowHeight, 48),
		theme.Set(ListSpacing, 8),
		theme.Set(KeyboardKey, 48),

		theme.Set(Gap, 8),
		theme.Set(Margin, geom.Uniform(16)),
		theme.Set(CardRadius, 16),
		theme.Set(CardPadding, geom.Uniform(16)),
		theme.Set(DialogRadius, 16),
		theme.Set(DialogPadding, 24),
		theme.Set(PageSlide, 32),
		// A finger scrolls the content itself: the bar only shows where it is.
		theme.Set(ScrollbarWidth, 4),
		theme.Set(ScrollbarGrabWidth, 4),
	}
}
