package graph

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

// Contrast sets the graph's colours for a contrast theme, to add to widget.Contrast's, as Light is added to
// widget.Light. Boxes take the window with an edge of the window text, ports and the band the highlight, and shadows
// go.
func Contrast(c gunim.ContrastColors) []theme.Entry {
	tint := c.Highlight
	tint.A = 0x50
	return []theme.Entry{
		theme.Set(GridInk, c.GrayText),
		theme.Set(NodeFill, c.Window),
		theme.Set(NodeEdge, c.WindowText),
		theme.Set(NodeShadow, color.NRGBA{}),
		theme.Set(TitleInk, c.WindowText),
		theme.Set(SubInk, c.WindowText),
		theme.Set(LabelInk, c.WindowText),
		theme.Set(PortInk, c.Highlight),
		theme.Set(PortHole, c.Window),
		theme.Set(RefuseInk, c.WindowText),
		theme.Set(BandFill, tint),
	}
}
