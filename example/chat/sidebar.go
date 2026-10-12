package main

import (
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Sizes of the rail's project buttons.
const (
	railButton = 44
	railStep   = 56
	railTop    = 14
)

// rail is the column of projects at the window's left edge. The chosen project's button is a rounded square with
// a bar beside it, and a project with unread messages wears a count.
type rail struct {
	anim.Group
	projects []Project
	chosen   int
	hover    int
	// round runs from 1, a circle, to 0, a rounded square, for each project; lit is how far each is under the
	// pointer or chosen.
	round, lit []*anim.Float
	shorts     []text.Run
	counts     []text.Run
}

func (r *rail) set(ps []Project, chosen int, u *gunim.UI) {
	for len(r.round) < len(ps) {
		round, lit := anim.NewFloat(1), anim.NewFloat(0)
		r.round, r.lit = append(r.round, round), append(r.lit, lit)
		r.Add(round, lit)
	}
	r.projects, r.chosen = ps, chosen
	r.aim(u.Theme())
	u.Invalidate()
}

// aim sends each button's shape toward where the choice and the pointer leave it.
func (r *rail) aim(th *theme.Live) {
	for i := range r.projects {
		on := i == r.chosen
		r.round[i].Animate(map[bool]float32{false: 1, true: 0}[on || i == r.hover], widget.Bounce.Get(th))
		r.lit[i].Animate(map[bool]float32{false: 0, true: 1}[on], widget.Quick.Get(th))
	}
}

// Layout implements [gunim.Node].
func (r *rail) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	bold := widget.BoldFont.Get(f.Theme)
	r.shorts, r.counts = r.shorts[:0], r.counts[:0]
	for _, p := range r.projects {
		r.shorts = append(r.shorts, bold.Shape(p.Short, 15))
		r.counts = append(r.counts, bold.Shape(strconv.Itoa(p.Unread), 11))
	}
	return geom.Sz(railW, c.Max.H)
}

// button returns project i's button.
func (r *rail) button(i int) geom.Rect {
	return geom.Rc((railW-railButton)/2, railTop+float32(i)*railStep, railButton, railButton)
}

// Paint implements [gunim.Node].
func (r *rail) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(RailFill.Get(th)))
	white := widget.ButtonStrongInk.Get(th)
	for i, pr := range r.projects {
		b := r.button(i)
		round, lit := r.round[i].Value(), min(max(r.lit[i].Value(), 0), 1)
		if lit > 0.01 {
			bar := geom.Rc(0, b.Center().Y-18*lit, 4, 36*lit)
			p.RRect(bar, 2, paint.Solid(widget.Ink.Get(th)))
		}
		radius := 12 + (railButton/2-12)*min(max(round, 0), 1)
		p.RRect(b, radius, paint.Solid(avatarTint(pr.Name)))
		s := r.shorts[i]
		c := b.Center()
		s.Paint(p, geom.Pt(c.X-s.Advance/2, c.Y-s.Height()/2), white)
		if pr.Unread > 0 && i != r.chosen {
			n := r.counts[i]
			w := max(18, n.Advance+10)
			badge := geom.Rc(b.Max.X-w+4, b.Max.Y-14, w, 18)
			p.RRectStroke(badge, 9, paint.Solid(BadgeFill.Get(th)), paint.Stroke{Width: 2, Color: RailFill.Get(th)})
			n.Paint(p, geom.Pt(badge.Center().X-n.Advance/2, badge.Center().Y-n.Height()/2), white)
		}
	}
}

// at returns the project whose button holds pt, or -1.
func (r *rail) at(pt geom.Point) int {
	for i := range r.projects {
		if r.button(i).Contains(pt) {
			return i
		}
	}
	return -1
}

// Handle implements [gunim.Handler]: a button squares its corners under the pointer, and a click chooses its
// project.
func (r *rail) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if i := r.at(e.Pos); i != r.hover {
			r.hover = i
			r.aim(u.Theme())
		}
	case input.PointerLeave:
		r.hover = -1
		r.aim(u.Theme())
	case input.PointerDown:
		if i := r.at(e.Pos); i >= 0 && e.Button == input.ButtonPrimary {
			u.Send(r, ProjectChosen{Index: i})
			return true
		}
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (r *rail) Cursor(pt geom.Point) input.Cursor {
	if r.at(pt) >= 0 {
		return input.CursorHand
	}
	return input.CursorArrow
}

// convRow is a conversation in the sidebar: a # or a person's mark, its name, bold while it has unread messages,
// and their count.
type convRow struct {
	anim.Group
	conv        Conversation
	hover, on   *anim.Float
	name, count text.Run
}

func newConvRow(c Conversation, current bool) *convRow {
	r := &convRow{conv: c, hover: anim.NewFloat(0), on: anim.NewFloat(0)}
	r.Add(r.hover, r.on)
	if current {
		r.on.Jump(1)
	}
	return r
}

func (r *convRow) set(c Conversation, current bool, u *gunim.UI) {
	r.conv = c
	r.on.Animate(map[bool]float32{false: 0, true: 1}[current], widget.Quick.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (r *convRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	face := widget.Font.Get(th)
	if r.conv.Unread > 0 {
		face = widget.BoldFont.Get(th)
	}
	r.name = face.Shape(r.conv.Name, widget.TextSize.Get(th))
	r.count = widget.BoldFont.Get(th).Shape(strconv.Itoa(r.conv.Unread), 11)
	return geom.Sz(c.Max.W, 32)
}

// Paint implements [gunim.Node].
func (r *convRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	full := geom.Rect{Max: box.Point()}
	if h := min(r.hover.Value(), 1); h > 0.01 {
		p.RRect(full, 7, paint.Solid(fade(SidebarHot.Get(th), h)))
	}
	if on := min(r.on.Value(), 1); on > 0.01 {
		p.RRect(full, 7, paint.Solid(fade(SidebarOn.Get(th), on)))
	}
	ink := Faint.Get(th)
	if r.conv.Unread > 0 || r.on.Target() > 0 {
		ink = widget.Ink.Get(th)
	}
	mid := box.H / 2
	if ic := areaIcon(r.conv.Area); ic != nil {
		widget.PaintIcon(p, th, ic, geom.Rc(11, mid-8, 16, 16), Faint.Get(th))
	} else if r.conv.Direct {
		p.RRect(geom.Rc(14, mid-5, 10, 10), 5, paint.Solid(avatarTint(r.conv.Name)))
	} else {
		hash := widget.Font.Get(th).Shape("#", widget.TextSize.Get(th))
		hash.Paint(p, geom.Pt(19-hash.Advance/2, mid-hash.Height()/2), Faint.Get(th))
	}
	r.name.Paint(p, geom.Pt(34, mid-r.name.Height()/2), ink)
	if r.conv.Unread > 0 {
		w := max(20, r.count.Advance+12)
		badge := geom.Rc(box.W-w-8, mid-9, w, 18)
		p.RRect(badge, 9, paint.Solid(BadgeFill.Get(th)))
		r.count.Paint(p, geom.Pt(badge.Center().X-r.count.Advance/2, mid-r.count.Height()/2), widget.ButtonStrongInk.Get(th))
	}
}

// areaIcon returns the icon of a row that opens an area of the project, or nil for a conversation.
func areaIcon(area string) *icon.Icon {
	if area == "files" {
		return icon.Folder
	}
	return nil
}

// Handle implements [gunim.Handler]: the row lights under the pointer, and leaves clicks to the list.
func (r *convRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e.(type) {
	case input.PointerEnter:
		r.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		r.hover.Animate(0, widget.Settle.Get(u.Theme()))
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (r *convRow) Cursor(geom.Point) input.Cursor { return input.CursorHand }
