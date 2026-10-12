package main

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// activeFor is how long after someone last wrote they still count as active.
const activeFor = 10 * time.Minute

// membersOf returns the people of c as the panel lists them: the user first, then those active, then the rest,
// each part by name.
func (a *app) membersOf(c *conv, now time.Time) []Member {
	seen := c.lastWrote
	out := make([]Member, 0, len(c.people)+1)
	out = append(out, Member{Name: me, Active: a.link == Online, Status: "You", You: true})
	var active, away []Member
	for _, p := range c.people {
		m := Member{Name: p, Status: "Away"}
		if t, ok := seen[p]; ok {
			if now.Sub(t) < activeFor {
				m.Active, m.Status = true, "Active now"
			} else {
				m.Status = "Last wrote " + ago(now.Sub(t))
			}
		}
		if m.Active {
			active = append(active, m)
		} else {
			away = append(away, m)
		}
	}
	byName := func(a, b Member) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(active, byName)
	slices.SortFunc(away, byName)
	return append(append(out, active...), away...)
}

// ago says how long ago something was, roughly.
func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h ago"
	}
	return strconv.Itoa(int(d.Hours()/24)) + " days ago"
}

// membersPanel lists the people of the open conversation, beside the timeline.
type membersPanel struct {
	root  gunim.Node
	title *widget.Label
	list  *widget.List
}

func newMembersPanel(shut func(*gunim.UI)) *membersPanel {
	m := &membersPanel{title: widget.NewLabel("Members"), list: widget.NewList()}
	m.title.Face = widget.BoldFont
	closer := widget.NewIconButton(icon.X, "Close")
	closer.OnClick = func(u *gunim.UI) gunim.Intent {
		shut(u)
		return nil
	}
	spacer := widget.NewSpacer()
	head := widget.Row(m.title, spacer, closer).Grow(spacer, 1)
	head.Cross = widget.CrossCenter
	col := widget.Column(head, m.list)
	col.Cross = widget.CrossStretch
	m.root = &panel{child: widget.NewPad(col), fill: SidebarFill}
	return m
}

// set shows members.
func (m *membersPanel) set(members []Member, u *gunim.UI) {
	m.title.Text = "Members · " + strconv.Itoa(len(members))
	widget.Sync(m.list, u, members,
		memberKey,
		newMemberRow,
		func(r *memberRow, p Member, u *gunim.UI) { r.set(p, u) })
}

// memberKey keys a member's row: the user's apart from anyone else of the same name.
func memberKey(m Member) widget.Key {
	if m.You {
		return "you"
	}
	return widget.Key("p:" + m.Name)
}

// memberRow is a person in the panel: their avatar with a dot that is green while they are active, their name,
// and how they stand.
type memberRow struct {
	anim.Group
	m                      Member
	active, hover          *anim.Float
	name, status, initials text.Run
}

func newMemberRow(m Member) *memberRow {
	r := &memberRow{m: m, active: anim.NewFloat(0), hover: anim.NewFloat(0)}
	r.Add(r.active, r.hover)
	if m.Active {
		r.active.Jump(1)
	}
	return r
}

func (r *memberRow) set(m Member, u *gunim.UI) {
	r.m = m
	r.active.Animate(map[bool]float32{false: 0, true: 1}[m.Active], widget.Settle.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (r *memberRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	textW := c.Max.W - 58
	r.name = shapeFit(widget.Font.Get(th), r.m.Name, widget.TextSize.Get(th), textW)
	r.status = shapeFit(widget.Font.Get(th), r.m.Status, SmallText.Get(th), textW)
	r.initials = widget.BoldFont.Get(th).Shape(initials(r.m.Name), 12)
	return geom.Sz(c.Max.W, 44)
}

// Paint implements [gunim.Node].
func (r *memberRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	if h := min(r.hover.Value(), 1); h > 0.01 {
		p.RRect(geom.Rect{Max: box.Point()}, 7, paint.Solid(fade(SidebarHot.Get(th), h)))
	}
	av := geom.Rc(8, (box.H-30)/2, 30, 30)
	p.RRect(av, 15, paint.Solid(avatarTint(r.m.Name)))
	c := av.Center()
	r.initials.Paint(p, geom.Pt(c.X-r.initials.Advance/2, c.Y-r.initials.Height()/2), widget.ButtonStrongInk.Get(th))
	// The dot turns green as they become active, and grey as they go.
	a := min(max(r.active.Value(), 0), 1)
	grey, green := widget.PaletteHint.Get(th), ActiveInk.Get(th)
	dot := anim.Mix(anim.ColorCodec, grey, green, a)
	d := geom.Rc(av.Max.X-10, av.Max.Y-10, 11, 11)
	p.RRectStroke(d, 5.5, paint.Solid(dot), paint.Stroke{Width: 2, Color: SidebarFill.Get(th)})
	x := av.Max.X + 12
	top := (box.H - r.name.Height() - r.status.Height()) / 2
	r.name.Paint(p, geom.Pt(x, top), widget.Ink.Get(th))
	r.status.Paint(p, geom.Pt(x, top+r.name.Height()), Faint.Get(th))
}

// Handle implements [gunim.Handler]: the row lights under the pointer.
func (r *memberRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e.(type) {
	case input.PointerEnter:
		r.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		r.hover.Animate(0, widget.Settle.Get(u.Theme()))
	}
	return false
}
