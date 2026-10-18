package graph

import (
	"image/color"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/theme"
)

// Theme tokens for a graph. Text and the accent come from package
// widget's tokens.
var (
	// GridInk is the dots of the plane's grid, GridStep how far apart
	// they stand on the plane.
	GridInk  = theme.Color("graph.grid", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e})
	GridStep = theme.Length("graph.grid.step", 32)
	// NodeFill is a box's fill, NodeEdge its outline, NodeRadius its
	// corners, and NodeWidth how wide a box is that says no width.
	NodeFill   = theme.Color("graph.node.fill", color.NRGBA{R: 0x22, G: 0x26, B: 0x30, A: 0xff})
	NodeEdge   = theme.Color("graph.node.edge", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x14})
	NodeRadius = theme.Length("graph.node.radius", 10)
	NodeWidth  = theme.Length("graph.node.width", 180)
	// NodeShadow is the shadow under a box.
	NodeShadow = theme.Color("graph.node.shadow", color.NRGBA{A: 0x70})
	// HeadHeight is how tall a box's head is, with its title, and
	// RowHeight each row of its ports.
	HeadHeight = theme.Length("graph.head.height", 34)
	RowHeight  = theme.Length("graph.row.height", 20)
	// TitleInk and TitleSize are a box's title; SubInk its line under
	// the title; LabelInk and LabelSize its ports' names.
	TitleInk  = theme.Foreground("graph.title.ink", color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0xff})
	TitleSize = theme.Length("graph.title.size", 13)
	SubInk    = theme.Foreground("graph.sub.ink", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
	LabelInk  = theme.Foreground("graph.label.ink", color.NRGBA{R: 0x9a, G: 0xa1, B: 0xb0, A: 0xff})
	LabelSize = theme.Length("graph.label.size", 10)
	// PortInk is a port's colour where it says none, and PortHole the
	// fill of a port no wire joins.
	PortInk  = theme.Color("graph.port.ink", color.NRGBA{R: 0x4f, G: 0xd6, B: 0xc0, A: 0xff})
	PortHole = theme.Color("graph.port.hole", color.NRGBA{R: 0x16, G: 0x19, B: 0x20, A: 0xff})
	// WireWidth is how wide a wire's core is, at a zoom of 1, and
	// WireGlow the glow round it; a Thin wire is ThinShare of both.
	WireWidth = theme.Length("graph.wire.width", 2.6)
	WireGlow  = theme.Length("graph.wire.glow", 9)
	ThinShare = theme.Number("graph.wire.thin", 0.62)
	// RefuseInk is a wire drawn to a port it may not join.
	RefuseInk = theme.Color("graph.refuse", color.NRGBA{R: 0xff, G: 0x7a, B: 0x6e, A: 0xff})
	// BandFill and the accent's ring are the band a drag with Shift
	// draws to pick the boxes it covers.
	BandFill = theme.Color("graph.band", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x1c})
	// Camera is how the plane glides as it is fitted or zoomed by a key,
	// Wheel as the wheel moves or zooms it, and Place how a box glides
	// to a place the application gives it, as an undo moves it.
	Camera = theme.Spring("graph.camera", anim.Spring{Response: 0.42, Damping: 1})
	Wheel  = theme.Spring("graph.wheel", anim.Spring{Response: 0.22, Damping: 1})
	Place  = theme.Spring("graph.place", anim.Spring{Response: 0.36, Damping: 1})
)

// Light sets the graph's colours for a light theme, to add to one, as
// widget.Light().With(graph.Light...).
var Light = []theme.Entry{
	theme.Set(GridInk, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x12}),
	theme.Set(NodeFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
	theme.Set(NodeEdge, color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x18}),
	theme.Set(NodeShadow, color.NRGBA{A: 0x30}),
	theme.Set(TitleInk, color.NRGBA{R: 0x16, G: 0x18, B: 0x1e, A: 0xff}),
	theme.Set(SubInk, color.NRGBA{R: 0x5c, G: 0x63, B: 0x70, A: 0xff}),
	theme.Set(LabelInk, color.NRGBA{R: 0x4c, G: 0x53, B: 0x60, A: 0xff}),
	theme.Set(PortInk, color.NRGBA{R: 0x12, G: 0x9e, B: 0x8a, A: 0xff}),
	theme.Set(PortHole, color.NRGBA{R: 0xf4, G: 0xf5, B: 0xf8, A: 0xff}),
	theme.Set(RefuseInk, color.NRGBA{R: 0xd9, G: 0x3d, B: 0x32, A: 0xff}),
	theme.Set(BandFill, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x14}),
}
