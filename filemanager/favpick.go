package filemanager

import (
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// FavouriteLook picks a favourite's colour and icon, as the Edit
// favourite dialog does, for a program's own forms: a row of the colours
// FavouriteColors names, and a grid of the icons FavouriteIcons names,
// drawn in the colour picked. Colors and Icons are its two parts, a form
// row each. A click picks, and with the keyboard the arrows, Home and
// End move the pick, its ring gliding to the choice.
type FavouriteLook struct {
	// OnChange hears the colour and icon as the user picks one.
	OnChange func(look FavouriteEdit, u *gunim.UI) gunim.Intent

	colors, icons *pickGrid
}

// colourNames are what a screen reader calls FavouriteColors.
var colourNames = []string{"Red", "Orange", "Yellow", "Green", "Teal", "Blue", "Indigo", "Purple", "Pink", "Gray"}

// NewFavouriteLook returns a FavouriteLook with color and icon picked: no
// colour for "", and a folder for an icon of "".
func NewFavouriteLook(color, icon string) *FavouriteLook {
	l := &FavouriteLook{}
	l.colors = newPickGrid(FavouriteColors, color, func(p *paint.Painter, th *theme.Live, i int, r geom.Rect) {
		c := r.Center()
		p.RRect(geom.Rc(c.X-10, c.Y-10, 20, 20), 10, paint.Solid(favColor(FavouriteColors[i]).Get(th)))
	})
	l.colors.Names = colourNames
	l.icons = newPickGrid(FavouriteIcons, iconOrFolder(icon), func(p *paint.Painter, th *theme.Live, i int, r geom.Rect) {
		// The icons show in the colour picked.
		paintFavMark(p, th, l.colors.picked(), FavouriteIcons[i], r.Center())
	})
	l.icons.Names = make([]string, len(FavouriteIcons))
	for i, n := range FavouriteIcons {
		l.icons.Names[i] = strings.ReplaceAll(n, "-", " ")
	}
	l.colors.changed = func(u *gunim.UI) { l.changed(l.colors, u) }
	l.icons.changed = func(u *gunim.UI) { l.changed(l.icons, u) }
	return l
}

// iconOrFolder is icon, or the folder a favourite without one draws.
func iconOrFolder(icon string) string {
	if icon == "" {
		return "folder"
	}
	return icon
}

// changed tells OnChange of a pick in g.
func (l *FavouriteLook) changed(g *pickGrid, u *gunim.UI) {
	if u == nil {
		return
	}
	// The icons follow the colour.
	u.Invalidate()
	if l.OnChange != nil {
		if in := l.OnChange(FavouriteEdit{Color: l.Color(), Icon: l.Icon()}, u); in != nil {
			u.Send(g, in)
		}
	}
}

// Colors is the row of colours, to put in a form.
func (l *FavouriteLook) Colors() gunim.Node { return l.colors }

// Icons is the grid of icons, to put in a form.
func (l *FavouriteLook) Icons() gunim.Node { return l.icons }

// Color is the colour picked, by name, or "" for none.
func (l *FavouriteLook) Color() string { return l.colors.picked() }

// Icon is the icon picked, by name.
func (l *FavouriteLook) Icon() string { return l.icons.picked() }

// SetLook picks color and icon, the ring gliding to each once laid out.
// It does not call OnChange. u may be nil.
func (l *FavouriteLook) SetLook(color, icon string, u *gunim.UI) {
	l.colors.sel = slices.Index(FavouriteColors, color)
	l.icons.sel = slices.Index(FavouriteIcons, iconOrFolder(icon))
	if u != nil {
		u.Invalidate()
	}
}
