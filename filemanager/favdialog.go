package filemanager

import (
	"image/color"
	"slices"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// favDialog is the Edit favourite dialog: the favourite's name, a row of
// colours and a grid of icons to pick from.
type favDialog struct {
	*widget.Dialog
	name   *widget.TextField
	colors *pickGrid
	icons  *pickGrid
}

// favEditBody is the form the dialog shows, and what Tab moves through.
type favEditBody struct {
	*widget.Form
	focus []gunim.Node
}

// Focusables is what Tab moves through in the dialog's body.
func (b *favEditBody) Focusables() []gunim.Node { return b.focus }

func newFavouriteDialog(s FavouriteEdit) *favDialog {
	d := &favDialog{Dialog: widget.NewDialog("Edit favourite"), name: widget.NewTextField()}
	d.name.SetText(s.Name, nil)
	d.name.Select(0, utf8.RuneCountInString(s.Name))
	d.name.Placeholder = s.Folder
	look := NewFavouriteLook(s.Color, s.Icon)
	d.colors, d.icons = look.colors, look.icons
	form := widget.NewForm().Add("Name", d.name).Add("Colour", d.colors).Add("Icon", d.icons)
	d.Body = &favEditBody{Form: form, focus: []gunim.Node{d.name, d.colors, d.icons}}
	d.Width = 500
	d.SetButtons("Save", "Cancel")
	d.OnAccept = func(u *gunim.UI) gunim.Intent {
		return FavouriteEdited{Token: s.Token, OK: true, Name: d.name.Text(), Color: d.colors.pick(s.Color),
			Icon: d.icons.pick(s.Icon)}
	}
	d.OnDismiss = widget.Sends(FavouriteEdited{Token: s.Token})
	return d
}

// pickGrid is a grid of choices, such as colours or icons, one of them
// picked. A click picks one, and with the keyboard the arrows, Home and
// End move the pick.
type pickGrid struct {
	anim.Group
	// Names are what a screen reader calls the choices.
	Names []string
	// values are the choices, by name, and sel the one picked, or -1.
	values []string
	sel    int
	// moved is set once the user picks.
	moved bool
	draw  func(p *paint.Painter, th *theme.Live, i int, r geom.Rect)
	// changed runs as the user picks.
	changed func(u *gunim.UI)
	hot     int
	// click follows a click on a choice.
	click widget.Clicker
	ring  *anim.Float
	mark  *anim.Rect
	cols  int
	laid  bool
}

// The size of a choice, and the room between choices.
const pickCell, pickGap = 32, 6

func newPickGrid(values []string, value string, draw func(p *paint.Painter, th *theme.Live, i int, r geom.Rect)) *pickGrid {
	g := &pickGrid{values: values, sel: slices.Index(values, value), draw: draw, hot: -1, ring: anim.NewFloat(0),
		mark: anim.NewRect(geom.Rect{})}
	g.Add(g.ring, g.mark)
	return g
}

// picked is the name of the choice picked, or empty.
func (g *pickGrid) picked() string {
	if g.sel < 0 || g.sel >= len(g.values) {
		return ""
	}
	return g.values[g.sel]
}

// pick is the name of the choice picked, or was where the user picked
// none, so a choice left alone stays as it was.
func (g *pickGrid) pick(was string) string {
	if !g.moved {
		return was
	}
	return g.picked()
}

// cell returns where choice i is.
func (g *pickGrid) cell(i int) geom.Rect {
	cols := max(g.cols, 1)
	return geom.Rc(float32(i%cols)*(pickCell+pickGap), float32(i/cols)*(pickCell+pickGap), pickCell, pickCell)
}

// at returns the choice at p, or -1.
func (g *pickGrid) at(p geom.Point) int {
	for i := range g.values {
		if g.cell(i).Contains(p) {
			return i
		}
	}
	return -1
}

// Focusable implements [gunim.Focusable].
func (g *pickGrid) Focusable() bool { return true }

// Layout implements [gunim.Node].
func (g *pickGrid) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	w := c.Max.W
	g.cols = max(int((w+pickGap)/(pickCell+pickGap)), 1)
	rows := (len(g.values) + g.cols - 1) / g.cols
	if g.sel >= 0 {
		to := g.cell(g.sel)
		if g.laid {
			g.mark.Animate(to, widget.Quick.Get(f.Theme))
		} else {
			g.mark.Jump(to)
		}
	}
	g.laid = true
	return geom.Sz(w, max(float32(rows)*(pickCell+pickGap)-pickGap, 0))
}

// Paint implements [gunim.Node].
func (g *pickGrid) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, _ gunim.Children) {
	th := f.Theme
	for i := range g.values {
		r := g.cell(i)
		if i == g.hot {
			p.RRect(r, 8, paint.Solid(SidebarHot.Get(th)))
		}
		g.draw(p, th, i, r)
	}
	if g.sel < 0 {
		return
	}
	m := g.mark.Value()
	ink := widget.Ink.Get(th)
	if t := g.ring.Value(); t > 0.01 {
		ink = blend(ink, widget.Accent.Get(th), min(t, 1))
	}
	p.RRectStroke(m, 9, paint.Fill{}, paint.Stroke{Width: 2, Color: ink})
}

// blend mixes a toward b, t of the way.
func blend(a, b color.NRGBA, t float32) color.NRGBA {
	return anim.Mix(anim.ColorCodec, a, b, t)
}

// choose picks choice i.
func (g *pickGrid) choose(i int, u *gunim.UI) {
	i = min(max(i, 0), len(g.values)-1)
	g.sel, g.moved = i, true
	if g.changed != nil {
		g.changed(u)
	}
	u.Invalidate()
}

// Handle implements [gunim.Handler].
func (g *pickGrid) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		// A click picks the choice it lets go on, the one it pressed, as widget's clicks do: a finger that lands on
		// one to scroll the dialog picks nothing, and a double click's second press starts no click.
		if e.Button != input.ButtonPrimary {
			return false
		}
		g.click.Press(e, g.at(e.Pos))
	case input.PointerUp:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if i := g.at(e.Pos); g.click.Release(e, i) {
			g.choose(i, u)
		}
	case input.PointerMove:
		if i := g.at(e.Pos); i != g.hot {
			g.hot = i
			u.Invalidate()
		}
	case input.PointerLeave:
		g.hot = -1
		u.Invalidate()
	case input.KeyPress:
		if e.Mods != 0 {
			return false
		}
		at := max(g.sel, 0)
		switch e.Key {
		case input.KeyLeft:
			g.choose(at-1, u)
		case input.KeyRight:
			g.choose(at+1, u)
		case input.KeyUp:
			if at < g.cols {
				return false
			}
			g.choose(at-g.cols, u)
		case input.KeyDown:
			if at+g.cols >= len(g.values) {
				return false
			}
			g.choose(at+g.cols, u)
		case input.KeyHome:
			g.choose(0, u)
		case input.KeyEnd:
			g.choose(len(g.values)-1, u)
		default:
			return false
		}
	case input.FocusRing:
		to := float32(0)
		if e.On {
			to = 1
		}
		g.ring.Animate(to, widget.RingFade.Get(u.Theme()))
		u.Invalidate()
	case input.FocusLost:
		g.ring.Animate(0, widget.RingFade.Get(u.Theme()))
		u.Invalidate()
	default:
		return false
	}
	return true
}

// Cursor implements [gunim.CursorShaper].
func (g *pickGrid) Cursor(at geom.Point) input.Cursor {
	if g.at(at) >= 0 {
		return input.CursorHand
	}
	return input.CursorArrow
}

// Access implements [gunim.Accessible]: a group of buttons, the one
// picked checked.
func (g *pickGrid) Access() access.Info {
	info := access.Info{Role: access.RoleGroup, Active: g.sel + 1}
	for i := range g.values {
		name := g.values[i]
		if i < len(g.Names) {
			name = g.Names[i]
		}
		state := access.StateCheckable
		if i == g.sel {
			state |= access.StateChecked
		}
		info.Parts = append(info.Parts, access.Info{Role: access.RoleButton, Name: name, State: state,
			Actions: []string{access.ActionPress}, Bounds: g.cell(i)})
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a choice picks it.
func (g *pickGrid) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(g.values) {
		return false
	}
	g.choose(r.Part, u)
	return true
}
