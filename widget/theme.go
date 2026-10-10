package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Tokens shared by every widget. Their defaults make the dark theme.
var (
	// Ink is the colour of text.
	Ink = theme.Foreground("ink", color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0xff})
	// Accent marks focus and selection.
	Accent = theme.Color("accent", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff})
	// TextSize is the body text size.
	TextSize = theme.Length("text.size", 14)
	// Font is the face text is set in, BoldFont the face for strong text, ItalicFont and BoldItalicFont the faces
	// for emphasis, and MonoFont the face for code and logs.
	Font           = theme.Choice("text.font", text.Default())
	BoldFont       = theme.Choice("text.font.bold", text.GoSans(true, false))
	ItalicFont     = theme.Choice("text.font.italic", text.GoSans(false, true))
	BoldItalicFont = theme.Choice("text.font.bold.italic", text.GoSans(true, true))
	MonoFont       = theme.Choice("text.font.mono", text.GoMono(false, false))
	// CodeFill is the fill behind code in text, and QuoteBar the bar beside a quote.
	CodeFill = theme.Color("text.code.fill", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12})
	QuoteBar = theme.Color("text.quote.bar", color.NRGBA{R: 0x4a, G: 0x50, B: 0x60, A: 0xff})

	// Quick is the motion for direct feedback: hover, press, focus.
	Quick = theme.Spring("motion.quick", anim.Snappy)
	// Settle is the motion for things coming to rest or going away.
	Settle = theme.Spring("motion.settle", anim.Gentle)
	// Bounce is the motion for things arriving, with a visible
	// overshoot.
	Bounce = theme.Spring("motion.bounce", anim.Bouncy)
	// RingFade is the motion of a focus ring fading in and out: short,
	// so a Tab shows one ring at a time.
	RingFade = theme.Spring("motion.ring", anim.Spring{Response: 0.12, Damping: 1})
)

// Button tokens.
var (
	ButtonFill  = theme.Color("button.fill", color.NRGBA{R: 0x2b, G: 0x2f, B: 0x3a, A: 0xff})
	ButtonHover = theme.Color("button.hover", color.NRGBA{R: 0x3d, G: 0x45, B: 0x58, A: 0xff})
	// ButtonPrimaryFill and ButtonDangerFill fill a primary button and a
	// dangerous one, lighter under the pointer, with ButtonStrongInk on
	// them.
	ButtonPrimaryFill  = theme.Color("button.primary", color.NRGBA{R: 0x3a, G: 0x6f, B: 0xd8, A: 0xff})
	ButtonPrimaryHover = theme.Color("button.primary.hover", color.NRGBA{R: 0x4d, G: 0x82, B: 0xe8, A: 0xff})
	ButtonDangerFill   = theme.Color("button.danger", color.NRGBA{R: 0xc2, G: 0x3f, B: 0x38, A: 0xff})
	ButtonDangerHover  = theme.Color("button.danger.hover", color.NRGBA{R: 0xd6, G: 0x53, B: 0x4b, A: 0xff})
	ButtonStrongInk    = theme.Foreground("button.strong.ink", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	// ButtonPrimaryInk is the ink on a primary button, for a theme that
	// wants it apart from a dangerous one's.
	ButtonPrimaryInk = theme.Foreground("button.primary.ink", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	// ButtonShadow is the shadow a button casts down and to the right,
	// as a theme drawn in the old boxes of a text screen has. Clear, as
	// it is unless a theme sets it, casts none.
	ButtonShadow  = theme.Color("button.shadow", color.NRGBA{})
	ButtonRadius  = theme.Length("button.radius", 8)
	ButtonPadding = theme.Length("button.padding", 16)
	ButtonHeight  = theme.Length("button.height", 36)
	// ButtonSquash is how far a press shrinks the button, as a fraction
	// of its size.
	ButtonSquash = theme.Length("button.squash", 0.035)
)

// Dialog tokens.
var (
	DialogFill   = theme.Color("dialog.fill", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	DialogBorder = theme.Color("dialog.border", color.NRGBA{R: 0x3a, G: 0x40, B: 0x50, A: 0xff})
	// DialogBorderLines is how many lines the rule round a dialog has:
	// 2 draws a double rule, as a text screen's double-line box does.
	DialogBorderLines = theme.Number("dialog.border.lines", 1)
	DialogShadow      = theme.Color("dialog.shadow", color.NRGBA{A: 0x80})
	Scrim             = theme.Color("dialog.scrim", color.NRGBA{A: 0x66})
	// DialogProblem colours what stands in the way of confirming a dialog.
	DialogProblem = theme.Color("dialog.problem", color.NRGBA{R: 0xff, G: 0x8a, B: 0x80, A: 0xff})
	// DialogDangerInk colours the icon of a danger dialog.
	DialogDangerInk = theme.Color("dialog.danger", color.NRGBA{R: 0xe5, G: 0x5a, B: 0x52, A: 0xff})
	DialogRadius    = theme.Length("dialog.radius", 14)
	DialogPadding   = theme.Length("dialog.padding", 20)
	// DialogTitleSize was the size of the heading a dialog drew its
	// title in. A dialog now shows its title in a title bar, in
	// [TextSize], and nothing in gunim reads this.
	DialogTitleSize = theme.Length("dialog.title.size", 17)
	DialogWidth     = theme.Length("dialog.width", 420)
	// DialogHeight is a dialog's height until its first layout, which
	// sizes it to its title bar, body and buttons.
	DialogHeight = theme.Length("dialog.height", 200)
	DialogGap    = theme.Length("dialog.gap", 10)
	// DialogMargin is the room a dialog leaves around itself when its
	// body is taller than the window.
	DialogMargin = theme.Length("dialog.margin", 48)
	// DialogBackdrop is how far the scrim blurs what is behind it, as a
	// blur's standard deviation: a touch, so the window behind stays
	// readable.
	DialogBackdrop = theme.Length("dialog.backdrop", 2)
)

// List tokens.
var (
	// ListSpacing is the gap between rows.
	ListSpacing = theme.Length("list.spacing", 6)
	// RowRadius rounds a row's clip.
	RowRadius = theme.Length("list.row.radius", 4)
)

// Light is a light theme. It changes more than colour: padding and
// radii grow and motion softens, so switching to it shows every kind of
// token animating.
func Light() theme.Theme {
	return theme.Make("light",
		theme.Set(Ink, color.NRGBA{R: 0x1c, G: 0x20, B: 0x28, A: 0xff}),
		theme.Set(Accent, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
		theme.Set(TextSize, 15),
		theme.Set(Quick, anim.Spring{Response: 0.35, Damping: 0.9}),
		theme.Set(Bounce, anim.Spring{Response: 0.5, Damping: 0.7}),
		theme.Set(ButtonFill, color.NRGBA{R: 0xe4, G: 0xe8, B: 0xf0, A: 0xff}),
		theme.Set(ButtonHover, color.NRGBA{R: 0xd2, G: 0xda, B: 0xe8, A: 0xff}),
		theme.Set(ButtonRadius, 18),
		theme.Set(ButtonPadding, 22),
		theme.Set(ButtonHeight, 40),
		theme.Set(DialogFill, color.NRGBA{R: 0xfa, G: 0xfb, B: 0xfd, A: 0xff}),
		theme.Set(DialogBorder, color.NRGBA{R: 0xd5, G: 0xdb, B: 0xe5, A: 0xff}),
		theme.Set(DialogShadow, color.NRGBA{A: 0x40}),
		theme.Set(Scrim, color.NRGBA{R: 0xf0, G: 0xf2, B: 0xf6, A: 0x60}),
		theme.Set(DialogRadius, 22),
		theme.Set(DialogPadding, 28),
		theme.Set(DialogWidth, 460),
		theme.Set(DialogHeight, 220),
		theme.Set(ListSpacing, 10),
		theme.Set(RowRadius, 10),
		theme.Set(Background, color.NRGBA{R: 0xf3, G: 0xf5, B: 0xf9, A: 0xff}),
		theme.Set(Margin, geom.Uniform(24)),
		theme.Set(CardFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(CardRadius, 16),
		theme.Set(CardPadding, geom.Insets{Top: 14, Right: 16, Bottom: 14, Left: 20}),
		theme.Set(ScrollbarColor, color.NRGBA{A: 0x40}),
		theme.Set(Gap, 12),
		theme.Set(HeadingSize, 24),
		theme.Set(FieldFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(FieldPressed, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x1c}),
		theme.Set(SegmentedTrack, color.NRGBA{R: 0xe4, G: 0xe8, B: 0xf0, A: 0xff}),
		theme.Set(FieldBorder, color.NRGBA{R: 0xc8, G: 0xd0, B: 0xdc, A: 0xff}),
		theme.Set(Selection, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x40}),
		theme.Set(Placeholder, color.NRGBA{R: 0x80, G: 0x88, B: 0x96, A: 0xff}),
		theme.Set(FieldRadius, 12),
		theme.Set(FieldHeight, 40),
		theme.Set(MenuFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(MenuBorder, color.NRGBA{R: 0xd5, G: 0xdb, B: 0xe5, A: 0xff}),
		theme.Set(MenuShadow, color.NRGBA{A: 0x40}),
		theme.Set(MenuHot, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x30}),
		theme.Set(MenuRadius, 12),
		theme.Set(TooltipFill, color.NRGBA{R: 0x1c, G: 0x20, B: 0x28, A: 0xf0}),
		theme.Set(TooltipInk, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(SwitchOff, color.NRGBA{R: 0xc8, G: 0xd0, B: 0xdc, A: 0xff}),
		theme.Set(SliderRestMark, color.NRGBA{R: 0x6a, G: 0x72, B: 0x80, A: 0xc0}),
		theme.Set(SliderRowInk, color.NRGBA{R: 0x5b, G: 0x63, B: 0x72, A: 0xff}),
		theme.Set(SliderRowActive, color.NRGBA{R: 0x3a, G: 0x6f, B: 0xd8, A: 0x1c}),
		theme.Set(CurveFill, color.NRGBA{R: 0xee, G: 0xf1, B: 0xf5, A: 0xff}),
		theme.Set(CurveGrid, color.NRGBA{A: 0x18}),
		theme.Set(HistogramFill, color.NRGBA{R: 0x2a, G: 0x2e, B: 0x36, A: 0xff}),
		theme.Set(Knob, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(KnobShadow, color.NRGBA{A: 0x40}),
		theme.Set(FocusRadius, 8),
		theme.Set(CheckRadius, 6),
		theme.Set(ControlHeight, 32),
		theme.Set(ToastInfoInk, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
		theme.Set(ToastSuccessInk, color.NRGBA{R: 0x1f, G: 0x8a, B: 0x4c, A: 0xff}),
		theme.Set(ToastWarningInk, color.NRGBA{R: 0xb7, G: 0x79, B: 0x1f, A: 0xff}),
		theme.Set(ToastErrorInk, color.NRGBA{R: 0xd0, G: 0x3a, B: 0x35, A: 0xff}),
		theme.Set(ToolbarFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(ToolbarBorder, color.NRGBA{R: 0xd5, G: 0xdb, B: 0xe5, A: 0xff}),
		theme.Set(ToolbarShadow, color.NRGBA{A: 0x28}),
		theme.Set(CodeFill, color.NRGBA{A: 0x0b}),
		theme.Set(QuoteBar, color.NRGBA{R: 0xc8, G: 0xd0, B: 0xdc, A: 0xff}),
		// The window's chrome.
		theme.Set(MenubarFill, color.NRGBA{R: 0xe1, G: 0xe5, B: 0xec, A: 0xff}),
		theme.Set(MenubarHot, color.NRGBA{A: 0x10}),
		theme.Set(WindowButtonHot, color.NRGBA{A: 0x14}),
		// Quiet text: hints, headers, crumbs.
		theme.Set(MenuHint, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(PaletteHint, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(TableHeader, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(ChipLead, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(AddressCrumbInk, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(AddressChevron, color.NRGBA{R: 0x80, G: 0x88, B: 0x96, A: 0xff}),
		// Text and marks that say something is wrong.
		theme.Set(DialogProblem, color.NRGBA{R: 0xb4, G: 0x2a, B: 0x22, A: 0xff}),
		theme.Set(DialogDangerInk, color.NRGBA{R: 0xc8, G: 0x34, B: 0x2c, A: 0xff}),
		theme.Set(DropRefusedInk, color.NRGBA{R: 0xc8, G: 0x34, B: 0x2c, A: 0xff}),
		// Fills, lines and tracks.
		theme.Set(ChipFill, color.NRGBA{R: 0xe4, G: 0xe8, B: 0xf0, A: 0xff}),
		theme.Set(ChipHover, color.NRGBA{R: 0xd2, G: 0xda, B: 0xe8, A: 0xff}),
		theme.Set(SplitLine, color.NRGBA{R: 0xd5, G: 0xdb, B: 0xe5, A: 0xff}),
		theme.Set(ProgressTrack, color.NRGBA{R: 0xd5, G: 0xdb, B: 0xe5, A: 0xff}),
		theme.Set(OverviewFill, color.NRGBA{R: 0xe9, G: 0xec, B: 0xf2, A: 0xff}),
		theme.Set(OverviewHover, color.NRGBA{A: 0x40}),
		theme.Set(GridPending, color.NRGBA{A: 0x10}),
		// The accent's tints, from the light accent.
		theme.Set(LinkInk, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
		theme.Set(TableStrong, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
		theme.Set(SplitHot, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
		theme.Set(GridRule, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x90}),
		theme.Set(GridCursor, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x30}),
		theme.Set(TableCursor, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x30}),
		theme.Set(PaletteMark, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x40}),
		theme.Set(OverviewMark, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xe6}),
		theme.Set(OverviewBox, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x22}),
		theme.Set(OverviewBoxEdge, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x80}),
		theme.Set(BandFill, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x2a}),
		theme.Set(BandEdge, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xd0}),
		theme.Set(TileCursor, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xc0}),
		theme.Set(TileHover, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x14}),
		theme.Set(TileSelected, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x38}),
		theme.Set(TreeHover, color.NRGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0x0a}),
		theme.Set(TreeSelected, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x26}),
		theme.Set(TreeGuide, color.NRGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0x16}),
		theme.Set(TreeBranch, color.NRGBA{R: 0xc9, G: 0x8f, B: 0x1c, A: 0xff}),
		// Code, in the colours of a light editor.
		theme.Set(CodeEditorFill, color.NRGBA{R: 0xfa, G: 0xfb, B: 0xfc, A: 0xff}),
		theme.Set(CodeGutterFill, color.NRGBA{R: 0xf0, G: 0xf2, B: 0xf6, A: 0xff}),
		theme.Set(CodeGutterInk, color.NRGBA{R: 0x9d, G: 0xa5, B: 0xb4, A: 0xff}),
		theme.Set(CodeLineFill, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x0c}),
		theme.Set(CodeProblem, color.NRGBA{R: 0xc8, G: 0x34, B: 0x2c, A: 0xff}),
		theme.Set(SyntaxKeyword, color.NRGBA{R: 0xa6, G: 0x26, B: 0xa4, A: 0xff}),
		theme.Set(SyntaxBuiltin, color.NRGBA{R: 0x01, G: 0x84, B: 0xbc, A: 0xff}),
		theme.Set(SyntaxType, color.NRGBA{R: 0xa0, G: 0x6c, B: 0x00, A: 0xff}),
		theme.Set(SyntaxFunction, color.NRGBA{R: 0x40, G: 0x78, B: 0xf2, A: 0xff}),
		theme.Set(SyntaxString, color.NRGBA{R: 0x50, G: 0xa1, B: 0x4f, A: 0xff}),
		theme.Set(SyntaxNumber, color.NRGBA{R: 0x98, G: 0x68, B: 0x01, A: 0xff}),
		theme.Set(SyntaxComment, color.NRGBA{R: 0x8a, G: 0x8f, B: 0x98, A: 0xff}),
		theme.Set(SyntaxOperator, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(SyntaxVariable, color.NRGBA{R: 0xe4, G: 0x56, B: 0x49, A: 0xff}),
		theme.Set(ColorPickerCheckLight, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		theme.Set(ColorPickerCheckDark, color.NRGBA{R: 0xd5, G: 0xd9, B: 0xe1, A: 0xff}),
	)
}

// Dark is the default theme, with every token at its default.
func Dark() theme.Theme { return theme.Make("dark") }

// Layout tokens.
var (
	// Gap is the space between the children of a row or column.
	Gap = theme.Length("layout.gap", 8)
	// Reflow is the motion children spring to new places with when a
	// row or column gains or loses one.
	Reflow = theme.Spring("motion.reflow", anim.Snappy)
)

// Scroll tokens.
var (
	ScrollbarColor = theme.Color("scroll.bar", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x50})
	ScrollbarWidth = theme.Length("scroll.bar.width", 6)
	// ScrollbarGrabWidth is how wide the bar grows while the pointer is
	// on it.
	ScrollbarGrabWidth = theme.Length("scroll.bar.grab.width", 10)
	// ScrollLine is how far an arrow key scrolls.
	ScrollLine = theme.Length("scroll.line", 40)
	// ScrollFade is how far in from its edge what scrolls fades where more of it lies past that edge: a scroll view,
	// a list, a menu, a row of tabs.
	ScrollFade = theme.Length("scroll.fade", 24)
)

// Surface tokens.
var (
	// Background is the window's own colour, behind everything. It is
	// the token application code reads and themes set: the engine's
	// [gunim.WindowBackground], which lays it under a window's tree.
	Background = gunim.WindowBackground
	// Margin is the space Pad leaves around its child by default.
	Margin      = theme.Insets("layout.margin", geom.Uniform(16))
	CardFill    = theme.Color("card.fill", color.NRGBA{R: 0x22, G: 0x26, B: 0x30, A: 0xff})
	CardRadius  = theme.Length("card.radius", 10)
	CardPadding = theme.Insets("card.padding", geom.Insets{Top: 10, Right: 12, Bottom: 10, Left: 14})
	// HeadingSize is the size of a heading's text.
	HeadingSize = theme.Length("text.heading.size", 22)
)

// Text field tokens.
var (
	FieldFill    = theme.Color("field.fill", color.NRGBA{R: 0x12, G: 0x14, B: 0x19, A: 0xff})
	FieldBorder  = theme.Color("field.border", color.NRGBA{R: 0x3a, G: 0x40, B: 0x50, A: 0xff})
	Selection    = theme.Color("field.selection", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x55})
	Placeholder  = theme.Foreground("field.placeholder", color.NRGBA{R: 0x8a, G: 0x90, B: 0x9c, A: 0xff})
	FieldHeight  = theme.Length("field.height", 36)
	FieldWidth   = theme.Length("field.width", 240)
	FieldPadding = theme.Length("field.padding", 10)
	FieldRadius  = theme.Length("field.radius", 8)
	// FieldPressed tints a number field while it is pressed or dragged
	// to change its value. It is laid over the field's fill, so a
	// see-through colour keeps the fill showing through.
	FieldPressed = theme.Color("field.pressed", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30})
	// Caret is the motion the caret and the selection glide with.
	// Make it very stiff for a caret that jumps.
	Caret = theme.Spring("motion.caret", anim.Spring{Response: 0.09, Damping: 1})
)

// Text area tokens. The frame, colours and padding are the text
// field's.
var (
	// AreaWidth is a text area's width when it is given none.
	AreaWidth = theme.Length("area.width", 320)
)

// Menu tokens, for menus, drop-down lists and tooltips.
var (
	MenuFill   = theme.Color("menu.fill", color.NRGBA{R: 0x24, G: 0x28, B: 0x33, A: 0xff})
	MenuBorder = theme.Color("menu.border", color.NRGBA{R: 0x3a, G: 0x40, B: 0x50, A: 0xff})
	MenuShadow = theme.Color("menu.shadow", color.NRGBA{A: 0x90})
	// MenuHot is the highlight behind the item under the pointer.
	MenuHot = theme.Color("menu.hot", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x48})
	// MenuHint colours an item's hint, such as its shortcut.
	MenuHint   = theme.Color("menu.hint", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
	MenuRadius = theme.Length("menu.radius", 8)
	// MenuPadding is the space between a menu's edge and its items.
	MenuPadding    = theme.Length("menu.padding", 4)
	MenuRowHeight  = theme.Length("menu.row.height", 28)
	MenuRowPadding = theme.Length("menu.row.padding", 12)
	// MenuMargin is the room a popup leaves around itself for its
	// shadow.
	MenuMargin = theme.Length("menu.margin", 14)

	TooltipFill    = theme.Color("tooltip.fill", color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0xf4})
	TooltipInk     = theme.Foreground("tooltip.ink", color.NRGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0xff})
	TooltipSize    = theme.Length("tooltip.size", 12.5)
	TooltipRadius  = theme.Length("tooltip.radius", 6)
	TooltipPadding = theme.Insets("tooltip.padding", geom.Insets{Top: 4, Right: 8, Bottom: 5, Left: 8})
	// TooltipMaxWidth is as wide as a tooltip's words run before they wrap.
	TooltipMaxWidth = theme.Length("tooltip.max.width", 480)
)

// Crossfade is the motion an [Image] fades with.
var Crossfade = theme.Spring("motion.crossfade", anim.Gentle)

// Control tokens, for checkboxes, switches, sliders and tabs.
var (
	// ControlHeight is the least height a control takes, so a row of
	// them lines up with buttons and fields.
	ControlHeight = theme.Length("control.height", 28)
	// ControlGap is the space between a control and its label.
	ControlGap  = theme.Length("control.gap", 8)
	CheckSize   = theme.Length("check.size", 18)
	CheckRadius = theme.Length("check.radius", 5)
	// CheckMark is the colour of a checkbox's tick.
	CheckMark    = theme.Color("check.mark", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	SwitchWidth  = theme.Length("switch.width", 40)
	SwitchHeight = theme.Length("switch.height", 22)
	// SwitchOff is the track of a switch that is off, and of a slider
	// past its knob.
	SwitchOff = theme.Color("switch.off", color.NRGBA{R: 0x3a, G: 0x40, B: 0x50, A: 0xff})
	// Knob is the colour of a switch's and a slider's knob, and KnobShadow of the shadow it casts.
	Knob        = theme.Color("knob", color.NRGBA{R: 0xf4, G: 0xf6, B: 0xfa, A: 0xff})
	KnobShadow  = theme.Color("knob.shadow", color.NRGBA{A: 0x58})
	KnobSize    = theme.Length("knob.size", 18)
	SliderTrack = theme.Length("slider.track", 4)
	// SliderRestMark marks a slider's resting value on its track.
	SliderRestMark = theme.Color("slider.rest", color.NRGBA{R: 0xa4, G: 0xab, B: 0xbb, A: 0xc0})
	TabHeight      = theme.Length("tab.height", 38)
	TabPadding     = theme.Length("tab.padding", 14)
	// TabGap is the room between the line under a [Tabs]' titles and its
	// page.
	TabGap = theme.Length("tab.gap", 14)
	// FocusRadius rounds the focus ring round a control with no corners of its own, such as a link or a tab's
	// title.
	FocusRadius = theme.Length("focus.radius", 5)
)

// HeroMotion is the motion a [Hero] flies with.
var HeroMotion = theme.Spring("motion.hero", anim.Spring{Response: 0.42, Damping: 0.86})
