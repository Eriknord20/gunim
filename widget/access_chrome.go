package widget

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
)

// Access implements [gunim.Accessible]: a menu bar with an item for each
// menu's title, the open one expanded.
func (b *Menubar) Access() access.Info {
	info := access.Info{Role: access.RoleMenuBar}
	for i, m := range b.Menus {
		shown, _, _ := accessKey(m.Title)
		part := access.Info{Role: access.RoleMenuItem, Name: shown, Actions: []string{access.ActionPress},
			State: access.StateExpandable | access.StateHasPopup}
		if i == b.open {
			part.State |= access.StateExpanded
			info.Active = i + 1
		}
		if i < len(b.spans) {
			part.Bounds = geom.Rc(b.spans[i][0], 0, b.spans[i][1]-b.spans[i][0], b.height)
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a title opens its
// menu, or closes it when it is open.
func (b *Menubar) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(b.Menus) {
		return false
	}
	if b.open == r.Part {
		b.Close(u)
	} else {
		b.Open(r.Part, u)
	}
	return true
}

// Access implements [gunim.Accessible]: the buttons.
func (c *WindowControls) Access() access.Info {
	info := access.Info{Role: access.RoleGroup}
	if c.size.W <= 0 {
		return info
	}
	bw := c.width()
	for i, b := range c.buttons() {
		var name string
		switch b {
		case pinButton:
			name = "Keep on top"
			if c.pinned {
				name = "Stop keeping on top"
			}
		case minimizeButton:
			name = "Minimize"
		case maximizeButton:
			name = "Maximize"
			if c.maxed {
				name = "Restore"
			}
		case closeButton:
			name = "Close"
		default:
			// No button has no name to read out.
		}
		info.Parts = append(info.Parts, access.Info{Role: access.RoleButton, Name: name,
			Actions: []string{access.ActionPress}, Bounds: geom.Rc(float32(i)*bw, 0, bw, c.size.H)})
	}
	return info
}

// AccessAct implements [gunim.AccessActor].
func (c *WindowControls) AccessAct(r access.Request, u *gunim.UI) bool {
	bs := c.buttons()
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(bs) {
		return false
	}
	c.act(bs[r.Part], u)
	return true
}

// Access implements [gunim.Accessible]: how far the work has got, from 0
// to 1, or no range while that is unknown.
func (b *ProgressBar) Access() access.Info {
	if b.Indeterminate {
		return access.Info{Role: access.RoleProgressBar, Value: "working"}
	}
	return access.Info{Role: access.RoleProgressBar, Range: &access.Range{Max: 1, Value: float64(b.value.Target())}}
}

// Access implements [gunim.Accessible]: a scroll bar over the rows the
// target can scroll to.
func (o *Overview) Access() access.Info {
	info := access.Info{Role: access.RoleScrollBar, Name: "Overview"}
	if o.Target != nil {
		info.Range = &access.Range{Max: max(0, float64(o.Target.Rows())-o.Target.Visible()), Value: o.Target.Top(), Step: 1}
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: a new value scrolls there.
func (o *Overview) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue || o.Target == nil {
		return false
	}
	o.Target.JumpTo(r.Value, false, u)
	return true
}

// Access implements [gunim.Accessible]: a group of the two panes, with the divider between them.
func (s *Split) Access() access.Info { return access.Info{Role: access.RoleGroup} }

// Access implements [gunim.Accessible]: the divider is a slider of the first pane's share, or its length with Fixed.
func (b *splitBar) Access() access.Info {
	s := b.s
	top := float64(1)
	if s.Fixed {
		top = float64(s.length)
	}
	return access.Info{Role: access.RoleSlider, Name: "Divider", Range: &access.Range{Max: top, Value: float64(s.share.Target())}}
}

// AccessAct implements [gunim.AccessActor]: a new value for the divider moves it there, and runs OnCommit.
func (b *splitBar) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue {
		return false
	}
	b.s.SetShare(float32(r.Value), u)
	b.s.moved(u)
	u.Invalidate()
	return true
}

// Access implements [gunim.Accessible]: a picture named by what its head
// says.
func (g *LiveGraph) Access() access.Info {
	info := access.Info{Role: access.RoleImage}
	if g.Label != nil && len(g.samples) > 0 {
		info.Name = g.Label(g.said)
	}
	return info
}

// Access implements [gunim.Accessible]: the item's title, what it says
// beside it, and whether it is highlighted.
func (r *paletteRow) Access() access.Info {
	info := access.Info{Role: access.RoleLabel, Name: r.item.Title, Description: r.item.Detail}
	if r.item.Hint != "" {
		info.Description += " " + r.item.Hint
	}
	if r.on {
		info.State = access.StateSelected
	}
	return info
}
