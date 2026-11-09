package filemanager

import (
	"image/color"

	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/theme"
)

// favColors are the colours of the favourites, by the names
// FavouriteColors gives them. Their defaults belong to the dark theme;
// the light theme has darker ones, which read on its lighter sidebar.
var favColors = map[string]theme.Token[color.NRGBA]{
	"red":    theme.Color("files.fav.red", color.NRGBA{R: 0xff, G: 0x6b, B: 0x6b, A: 0xff}),
	"orange": theme.Color("files.fav.orange", color.NRGBA{R: 0xff, G: 0x9f, B: 0x43, A: 0xff}),
	"yellow": theme.Color("files.fav.yellow", color.NRGBA{R: 0xf5, G: 0xc5, B: 0x42, A: 0xff}),
	"green":  theme.Color("files.fav.green", color.NRGBA{R: 0x4c, G: 0xd0, B: 0x7d, A: 0xff}),
	"teal":   theme.Color("files.fav.teal", color.NRGBA{R: 0x2e, G: 0xc4, B: 0xb6, A: 0xff}),
	"blue":   theme.Color("files.fav.blue", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff}),
	"indigo": theme.Color("files.fav.indigo", color.NRGBA{R: 0x8b, G: 0x8f, B: 0xff, A: 0xff}),
	"purple": theme.Color("files.fav.purple", color.NRGBA{R: 0xc4, G: 0x8b, B: 0xff, A: 0xff}),
	"pink":   theme.Color("files.fav.pink", color.NRGBA{R: 0xff, G: 0x7a, B: 0xb6, A: 0xff}),
	"gray":   theme.Color("files.fav.gray", color.NRGBA{R: 0x9a, G: 0xa3, B: 0xb5, A: 0xff}),
}

// lightFavColors are the favourites' colours in the light theme.
var lightFavColors = map[string]color.NRGBA{
	"red":    {R: 0xd3, G: 0x3a, B: 0x3a, A: 0xff},
	"orange": {R: 0xd2, G: 0x6a, B: 0x10, A: 0xff},
	"yellow": {R: 0xb0, G: 0x84, B: 0x00, A: 0xff},
	"green":  {R: 0x1f, G: 0x9d, B: 0x55, A: 0xff},
	"teal":   {R: 0x0e, G: 0x8c, B: 0x83, A: 0xff},
	"blue":   {R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff},
	"indigo": {R: 0x4f, G: 0x55, B: 0xd6, A: 0xff},
	"purple": {R: 0x8e, G: 0x4f, B: 0xd6, A: 0xff},
	"pink":   {R: 0xcc, G: 0x3f, B: 0x86, A: 0xff},
	"gray":   {R: 0x66, G: 0x6e, B: 0x7f, A: 0xff},
}

// FavTile is how strongly the tile behind a favourite's icon takes its
// colour, from 0 to 1.
var FavTile = theme.Number("files.fav.tile", 0.2)

// favColor is the colour of name, one of FavouriteColors, and the colour
// of folders for another.
func favColor(name string) theme.Token[color.NRGBA] {
	if tok, ok := favColors[name]; ok {
		return tok
	}
	return TintToken(TintFolder)
}

// favIcons are the icons of the favourites, by the names FavouriteIcons
// gives them.
var favIcons = map[string]*icon.Icon{
	"folder": icon.Folder, "house": icon.House, "monitor": icon.Monitor, "file-text": icon.FileText,
	"download": icon.Download, "image": icon.Image, "music": icon.Music, "video": icon.Video,
	"code": icon.Code, "briefcase": icon.Briefcase, "book": icon.Book, "star": icon.Star,
	"heart": icon.Heart, "cloud": icon.Cloud, "server": icon.Server, "database": icon.Database,
	"archive": icon.Archive, "camera": icon.Camera, "gamepad": icon.Gamepad, "globe": icon.Globe,
	"graduation-cap": icon.GraduationCap, "wrench": icon.Wrench, "flask-conical": icon.FlaskConical,
	"terminal": icon.Terminal,
}

// favIcon is the icon of name, one of FavouriteIcons, and a folder for
// another.
func favIcon(name string) *icon.Icon {
	if ic, ok := favIcons[name]; ok {
		return ic
	}
	return icon.Folder
}

// FavouriteIcon is the icon of name, one of FavouriteIcons, and a folder
// for another, as the sidebar draws a favourite's.
func FavouriteIcon(name string) *icon.Icon { return favIcon(name) }

// FavouriteColor is the colour of name, one of FavouriteColors, and the
// colour of folders for another, as the sidebar draws a favourite's.
func FavouriteColor(name string) theme.Token[color.NRGBA] { return favColor(name) }
