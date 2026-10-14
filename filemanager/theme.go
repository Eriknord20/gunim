package filemanager

import (
	"image/color"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The file manager's own tokens. Their defaults belong to the dark theme.
var (
	SidebarFill = theme.Color("files.sidebar", color.NRGBA{R: 0x1a, G: 0x1d, B: 0x24, A: 0xff})
	SidebarHot  = theme.Color("files.sidebar.hot", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10})
	SidebarOn   = theme.Color("files.sidebar.on", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30})
	PaneFill    = theme.Color("files.pane", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	Faint       = theme.Foreground("files.faint", color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff})
	Caption     = theme.Foreground("files.caption", color.NRGBA{R: 0x70, G: 0x79, B: 0x8c, A: 0xff})
	ErrorInk    = theme.Foreground("files.error", color.NRGBA{R: 0xff, G: 0x8a, B: 0x80, A: 0xff})
	ErrorFill   = theme.Color("files.error.fill", color.NRGBA{R: 0x5a, G: 0x22, B: 0x22, A: 0xff})
	// PlaceLit is the mark of a place that is lit, as a machine connected is.
	PlaceLit = theme.Color("files.place.lit", color.NRGBA{R: 0x4c, G: 0xd0, B: 0x7d, A: 0xff})
	// CloudLocalInk marks an item a cloud provider has on this device,
	// CloudPinnedInk one it always keeps there, and CloudBlank holds the
	// place of a mark where none can be told.
	CloudLocalInk  = theme.Color("files.cloud.local", color.NRGBA{R: 0x4c, G: 0xd0, B: 0x7d, A: 0xff})
	CloudPinnedInk = theme.Color("files.cloud.pinned", color.NRGBA{R: 0x2f, G: 0xb8, B: 0x63, A: 0xff})
	CloudBlank     = theme.Color("files.cloud.blank", color.NRGBA{})
	SmallText      = theme.Length("files.small", 12)
	TitleText      = theme.Length("files.title", 16)

	// Page is the motion a folder's listing slides in with.
	Page = theme.Spring("files.motion.page", anim.Spring{Response: 0.34, Damping: 0.88})
)

// tintTokens colour the mark of each group of files.
var tintTokens = map[Tint]theme.Token[color.NRGBA]{
	TintOther:    theme.Color("files.tint.other", color.NRGBA{R: 0x7d, G: 0x86, B: 0x99, A: 0xff}),
	TintFolder:   theme.Color("files.tint.folder", color.NRGBA{R: 0xe8, G: 0xb3, B: 0x4a, A: 0xff}),
	TintImage:    theme.Color("files.tint.image", color.NRGBA{R: 0xc4, G: 0x8b, B: 0xff, A: 0xff}),
	TintVideo:    theme.Color("files.tint.video", color.NRGBA{R: 0xff, G: 0x7a, B: 0x9c, A: 0xff}),
	TintAudio:    theme.Color("files.tint.audio", color.NRGBA{R: 0x3f, G: 0xd0, B: 0xe0, A: 0xff}),
	TintArchive:  theme.Color("files.tint.archive", color.NRGBA{R: 0xff, G: 0x9e, B: 0x5a, A: 0xff}),
	TintDocument: theme.Color("files.tint.document", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff}),
	TintCode:     theme.Color("files.tint.code", color.NRGBA{R: 0x4f, G: 0xd6, B: 0x9c, A: 0xff}),
	TintProgram:  theme.Color("files.tint.program", color.NRGBA{R: 0xf5, G: 0x6c, B: 0x5c, A: 0xff}),
}

// TintToken is the token that colours the mark of group t, for a program
// whose themes set the file manager's colours.
func TintToken(t Tint) theme.Token[color.NRGBA] {
	if tok, ok := tintTokens[t]; ok {
		return tok
	}
	return tintTokens[TintOther]
}

// darkTheme and lightTheme extend the widget library's themes with the
// file manager's tokens.
func darkTheme() theme.Theme { return widget.Dark() }

func lightTheme() theme.Theme {
	entries := make([]theme.Entry, 0, 1+len(lightFavColors))
	entries = append(entries, theme.Set(FavTile, 0.16))
	for name, c := range lightFavColors {
		entries = append(entries, theme.Set(favColors[name], c))
	}
	entries = append(entries, usageLight...)
	return widget.Light().With(entries...).With(
		theme.Set(SidebarFill, color.NRGBA{R: 0xe9, G: 0xec, B: 0xf2, A: 0xff}),
		theme.Set(SidebarHot, color.NRGBA{A: 0x0c}),
		theme.Set(SidebarOn, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0x26}),
		theme.Set(PaneFill, color.NRGBA{R: 0xfa, G: 0xfb, B: 0xfd, A: 0xff}),
		theme.Set(Faint, color.NRGBA{R: 0x5f, G: 0x67, B: 0x75, A: 0xff}),
		theme.Set(Caption, color.NRGBA{R: 0x80, G: 0x88, B: 0x96, A: 0xff}),
		theme.Set(ErrorInk, color.NRGBA{R: 0xb4, G: 0x2a, B: 0x22, A: 0xff}),
		theme.Set(ErrorFill, color.NRGBA{R: 0xfd, G: 0xe4, B: 0xe1, A: 0xff}),
		theme.Set(Success, color.NRGBA{R: 0x1f, G: 0x9d, B: 0x55, A: 0xff}),
		theme.Set(PlaceLit, color.NRGBA{R: 0x1f, G: 0xa8, B: 0x52, A: 0xff}),
		theme.Set(widget.TextSize, 14),
		theme.Set(widget.ButtonHeight, 34),
		theme.Set(widget.ButtonPadding, 16),
		theme.Set(widget.ButtonRadius, 8),
		theme.Set(widget.FieldHeight, 34),
		theme.Set(widget.FieldRadius, 8),
	)
}
