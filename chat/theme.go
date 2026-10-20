package chat

import (
	"hash/fnv"
	"image/color"

	"github.com/marrasen/gunim/theme"
)

// The chat's own tokens. Their defaults belong to the dark theme. The rail and the sidebar are for a host that
// lists projects and conversations beside the chat, in its colours.
var (
	RailFill    = theme.Color("chat.rail", color.NRGBA{R: 0x14, G: 0x16, B: 0x1c, A: 0xff})
	SidebarFill = theme.Color("chat.sidebar", color.NRGBA{R: 0x1a, G: 0x1d, B: 0x24, A: 0xff})
	SidebarHot  = theme.Color("chat.sidebar.hot", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10})
	SidebarOn   = theme.Color("chat.sidebar.on", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30})
	PaneFill    = theme.Color("chat.pane", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	RowHot      = theme.Color("chat.row.hot", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x08})
	QuoteFill   = theme.Color("chat.quote", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0a})
	Faint       = theme.Foreground("chat.faint", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
	ErrorInk    = theme.Foreground("chat.error", color.NRGBA{R: 0xff, G: 0x8a, B: 0x80, A: 0xff})
	WarnFill    = theme.Color("chat.warn", color.NRGBA{R: 0x5a, G: 0x45, B: 0x1c, A: 0xff})
	ActiveInk   = theme.Color("chat.active", color.NRGBA{R: 0x3f, G: 0xb9, B: 0x6f, A: 0xff})
	BadgeFill   = theme.Color("chat.badge", color.NRGBA{R: 0xe0, G: 0x4f, B: 0x4f, A: 0xff})
	SmallText   = theme.Length("chat.small", 12)
	// Backdrop is how far the view blurs what is behind it, as a standard deviation in logical pixels. Above zero,
	// with a PaneFill that lets some of it through, the chat sits on glass.
	Backdrop = theme.Length("chat.backdrop", 0)
	// TimelineSpacing is the room between the timeline's rows.
	TimelineSpacing = theme.Length("chat.timeline.spacing", 0)
)

// avatarTints colour the avatars, one per author, picked by the author's name.
var avatarTints = []color.NRGBA{
	{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff},
	{R: 0xe8, G: 0x8b, B: 0x4a, A: 0xff},
	{R: 0x4f, G: 0xb6, B: 0x8c, A: 0xff},
	{R: 0xc4, G: 0x7b, B: 0xe8, A: 0xff},
	{R: 0xe0, G: 0x5f, B: 0x7d, A: 0xff},
	{R: 0x3f, G: 0xb0, B: 0xc8, A: 0xff},
}

// AvatarTint returns the colour of a name's avatar, the same each time for the same name.
func AvatarTint(name string) color.NRGBA {
	h := fnv.New32a()
	h.Write([]byte(name))
	return avatarTints[h.Sum32()%uint32(len(avatarTints))]
}

// fade returns c with its alpha scaled by t.
func fade(c color.NRGBA, t float32) color.NRGBA {
	c.A = uint8(float32(c.A) * max(0, min(t, 1)))
	return c
}
