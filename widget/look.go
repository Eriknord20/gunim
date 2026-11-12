package widget

import (
	"errors"
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

// ForLook returns th changed to follow the user's system settings: text and the rows that hold it scaled by
// look.TextScale, animation off under look.LessMotion, and the contrast theme's colours under look.Contrast. Pick th by
// look.Dark first:
//
//	base := widget.Light()
//	if look.Dark {
//	    base = widget.Dark()
//	}
//	th := widget.ForLook(base, look)
//
// The contrast colours cover package widget. Add each other package's own, as calendar.Contrast, to the result with
// [theme.Theme.With].
func ForLook(th theme.Theme, look gunim.SystemLook) theme.Theme {
	if look.TextScale > 0 && look.TextScale != 1 {
		th = theme.ScaleText(th, look.TextScale)
	}
	if look.LessMotion {
		th = th.With(theme.Set(theme.Scale, 0))
	}
	if look.Contrast != nil {
		th = th.With(Contrast(*look.Contrast)...)
		for key, v := range contrastByKey(*look.Contrast) {
			// A token another file of this package declares, where it is in this build.
			if next, err := th.WithValue(key, v); err == nil {
				th = next
			} else if !errors.Is(err, theme.ErrUnknownKey) {
				panic(err)
			}
		}
	}
	return th
}

// tintAlpha is how much of the highlight a contrast theme's tints take: enough to see, little enough that the window's
// text on them keeps its contrast.
const tintAlpha = 0x50

// tint is c at the strength of a contrast theme's tints.
func tint(c color.NRGBA) color.NRGBA {
	c.A = tintAlpha
	return c
}

// contrastGroups sorts the colours a contrast theme gives package widget's tokens by how they are drawn.
type contrastGroups struct {
	// solid are fills of the highlight that text or marks are drawn on, each with the ink for them.
	solid []contrastPair
	// tints are fills under the window's own text, in a tint of the highlight.
	tints []theme.Token[color.NRGBA]
	// marks are lines, edges and marks in the highlight, with nothing drawn on them.
	marks []theme.Token[color.NRGBA]
	// entries are the rest.
	entries []theme.Entry
}

// contrastPair is a fill and the ink drawn on it.
type contrastPair struct {
	fill, ink theme.Token[color.NRGBA]
}

// contrastSorted returns the contrast theme's colours for package widget, sorted by how they are drawn.
func contrastSorted(c gunim.ContrastColors) contrastGroups {
	var none color.NRGBA
	set := func(v color.NRGBA, tokens ...theme.Token[color.NRGBA]) []theme.Entry {
		out := make([]theme.Entry, len(tokens))
		for i, t := range tokens {
			out[i] = theme.Set(t, v)
		}
		return out
	}
	scrim := c.Window
	scrim.A = 0xa0
	var entries []theme.Entry
	for _, group := range [][]theme.Entry{
		// Grounds.
		set(c.Window, Background, CardFill, DialogFill, PanelFill, MenuFill, FieldFill, ToolbarFill, MenubarFill,
			TooltipFill, CurveFill, HistogramFill, OverviewFill, CodeEditorFill, CodeGutterFill, ChordInputFill, Knob,
			ColorPickerCheckLight),
		set(c.ButtonFace, ButtonFill, ChipFill, ChipCap, SegmentedTrack),
		set(c.ButtonText, ButtonInk),
		// Text, lines and status.
		set(c.WindowText, Ink, TooltipInk, MenuHint, PaletteHint, TableHeader, ChipLead, AddressCrumbInk,
			AddressChevron, LogViewLead, SliderRowInk, ChordInputPrompt, CodeGutterInk,
			DialogBorder, PanelBorder, MenuBorder, FieldBorder, ToolbarBorder, ChipCapEdge, SplitLine, ProgressTrack,
			SwitchOff, ScrollbarColor, QuoteBar, SliderRestMark, CurveGrid, TreeGuide, TreeBranch, GridPending,
			ColorPickerCheckDark,
			ToastInfoInk, ToastSuccessInk, ToastWarningInk, ToastErrorInk, DialogProblem, DialogDangerInk,
			DropRefusedInk, ChordInputRefused, CodeProblem,
			SyntaxKeyword, SyntaxBuiltin, SyntaxType, SyntaxFunction, SyntaxString, SyntaxNumber, SyntaxComment,
			SyntaxOperator, SyntaxVariable),
		set(c.GrayText, Placeholder),
		set(c.HotLight, LinkInk),
		// No shadows.
		set(none, DialogShadow, PanelShadow, MenuShadow, ToolbarShadow, KnobShadow, ButtonShadow, CodeFill),
		set(scrim, Scrim),
	} {
		entries = append(entries, group...)
	}
	return contrastGroups{
		solid: []contrastPair{
			{MenuHot, MenuHotInk},
			{Accent, ButtonStrongInk}, {Accent, CheckMark},
			{ButtonPrimaryFill, ButtonPrimaryInk}, {ButtonPrimaryHover, ButtonPrimaryInk},
			{ButtonDangerFill, ButtonStrongInk}, {ButtonDangerHover, ButtonStrongInk},
		},
		tints: []theme.Token[color.NRGBA]{ListSelected, Selection, FieldPressed, SliderRowActive, ButtonHover,
			ChipHover, MenubarHot, WindowButtonHot, OverviewHover, OverviewBox, GridCursor, TableCursor, PaletteMark,
			BandFill, TileHover, TileSelected, TreeHover, TreeSelected, CodeLineFill},
		marks: []theme.Token[color.NRGBA]{OverviewMark, OverviewBoxEdge, TableStrong, SplitHot, GridRule, BandEdge,
			TileCursor},
		entries: entries,
	}
}

// Contrast gives every colour of package widget's tokens one of a contrast theme's colours. Text and lines take the
// window text, and grounds the window. A fill of the highlight that holds text, as a menu's hot row, takes the
// highlight, with the highlight text on it. Hover, selection and cursor tints take a tint of the highlight, under
// the window text. Shadows go, and status shows in its icon and words.
func Contrast(c gunim.ContrastColors) []theme.Entry {
	g := contrastSorted(c)
	out := g.entries
	for _, p := range g.solid {
		out = append(out, theme.Set(p.fill, c.Highlight), theme.Set(p.ink, c.HighlightText))
	}
	for _, t := range g.tints {
		out = append(out, theme.Set(t, tint(c.Highlight)))
	}
	for _, t := range g.marks {
		out = append(out, theme.Set(t, c.Highlight))
	}
	return out
}

// contrastByKey gives a contrast theme's colours to tokens of this package that may be declared apart, by key.
func contrastByKey(c gunim.ContrastColors) map[string]color.NRGBA {
	return map[string]color.NRGBA{
		"nav.hover":        tint(c.Highlight),
		"nav.selected":     c.Highlight,
		"nav.selected.ink": c.HighlightText,
	}
}
