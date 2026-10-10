package widget

import (
	"strconv"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
)

// What each widget tells a screen reader, and what it does when a
// screen reader asks. See [gunim.Accessible].

// Access implements [gunim.Accessible].
func (l *Label) Access() access.Info {
	return access.Info{Role: access.RoleLabel, Name: l.Text}
}

// Access implements [gunim.Accessible]. A button showing an icon alone is named by the icon; an active one reads as
// checked, as a toggle that is on.
func (b *Button) Access() access.Info {
	name := b.Label
	if name == "" {
		name = iconName(b.Icon)
	}
	return access.Info{Role: access.RoleButton, Name: name, State: b.state(), Actions: []string{access.ActionPress}}
}

// state is the button's state: active, disabled, both or neither.
func (b *Button) state() access.State { return activeState(b.Active) | b.accessState() }

// activeState is the state of a button that is active, as a toggle that is on, or none.
func activeState(active bool) access.State {
	if active {
		return access.StateCheckable | access.StateChecked
	}
	return 0
}

// iconName is what a screen reader calls an icon shown without words: its name in Lucide, in words.
func iconName(ic *icon.Icon) string {
	if ic == nil {
		return ""
	}
	return strings.ReplaceAll(ic.Name, "-", " ")
}

// AccessAct implements [gunim.AccessActor].
func (b *Button) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || b.Disabled {
		return false
	}
	b.fire(u)
	return true
}

// Access implements [gunim.Accessible].
func (t *TextField) Access() access.Info {
	value := ""
	if !t.Secret {
		value = t.Text()
	}
	return access.Info{Role: access.RoleTextField, Name: t.accessName(t.Placeholder), Value: value,
		State: access.StateEditable | t.accessState()}
}

// Access implements [gunim.Accessible].
func (a *TextArea) Access() access.Info {
	return access.Info{
		Role: access.RoleTextField, Name: a.accessName(a.Placeholder), Value: a.textString(),
		State: access.StateEditable | access.StateMultiline | a.accessState(),
	}
}

// Access implements [gunim.Accessible].
func (c *CodeEditor) Access() access.Info {
	state := access.StateMultiline
	if c.readOnly {
		state |= access.StateReadOnly
	} else {
		state |= access.StateEditable
	}
	return access.Info{Role: access.RoleTextField, Name: c.Label, Value: c.textString(), State: state}
}

// access is what a checkbox or a switch says.
func (t *toggle) access(role access.Role) access.Info {
	s := access.StateCheckable | t.accessState()
	if t.checked {
		s |= access.StateChecked
	}
	return access.Info{Role: role, Name: t.accessName(t.Label), State: s, Actions: []string{access.ActionPress}}
}

// Access implements [gunim.Accessible].
func (c *Checkbox) Access() access.Info { return c.access(access.RoleCheckbox) }

// AccessAct implements [gunim.AccessActor].
func (c *Checkbox) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || c.Disabled {
		return false
	}
	c.flip(c, u)
	return true
}

// Access implements [gunim.Accessible].
func (s *Switch) Access() access.Info { return s.access(access.RoleSwitch) }

// AccessAct implements [gunim.AccessActor].
func (s *Switch) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || s.Disabled {
		return false
	}
	s.flip(s, u)
	return true
}

// Access implements [gunim.Accessible]. The slider is named by its Label, the row it is in, or its tooltip.
func (s *Slider) Access() access.Info {
	name := s.Label
	if name == "" {
		name = s.row
	}
	return access.Info{
		Role:  access.RoleSlider,
		Name:  s.accessName(name),
		State: s.accessState(),
		Value: strconv.FormatFloat(float64(s.value), 'g', 4, 32),
		Range: &access.Range{Min: float64(s.Min), Max: float64(s.Max), Value: float64(s.value), Step: float64(s.Snap)},
	}
}

// AccessAct implements [gunim.AccessActor]: a new value.
func (s *Slider) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue || s.Disabled {
		return false
	}
	s.set(float32(r.Value), Quick.Get(u.Theme()), u)
	return true
}

// Access implements [gunim.Accessible]: a tab list with a tab for each
// title.
func (b *tabBar) Access() access.Info {
	t := b.t
	info := access.Info{Role: access.RoleTabList}
	for i, title := range t.Titles {
		part := access.Info{Role: access.RoleTab, Name: title, Actions: []string{access.ActionPress}}
		if i == t.selected {
			part.State = access.StateSelected
		}
		if i < len(t.spans) {
			part.Bounds = geom.Rc(t.spans[i][0], 0, t.spans[i][1]-t.spans[i][0], t.head)
		}
		info.Parts = append(info.Parts, part)
	}
	// The chosen tab is where the titles' focus is.
	info.Active = t.selected + 1
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a tab chooses it.
func (b *tabBar) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 {
		return false
	}
	b.t.choose(r.Part, u)
	return true
}

// Access implements [gunim.Accessible].
func (d *Dropdown) Access() access.Info {
	info := access.Info{
		Role:    access.RoleComboBox,
		Name:    d.accessName(d.Label),
		State:   access.StateExpandable | access.StateHasPopup | d.accessState(),
		Actions: []string{access.ActionPress},
	}
	items := d.list.items
	if it := d.chosen(); it != nil {
		info.Value = it.Label
	}
	if d.IsOpen() {
		info.State |= access.StateExpanded
		// While the list is open, its highlighted item is what the
		// keys move through.
		if h := d.menu.Highlighted(); h >= 0 && h < len(items) {
			info.Value = items[h].Label
		}
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: press opens the list, or
// closes it.
func (d *Dropdown) AccessAct(r access.Request, u *gunim.UI) bool {
	switch r.Action {
	case access.ActionPress, access.ActionOpen, access.ActionClose:
	default:
		return false
	}
	if d.Disabled {
		return false
	}
	if d.IsOpen() {
		d.close(u)
	} else if r.Action != access.ActionClose {
		d.open(u)
	}
	return true
}

// Access implements [gunim.Accessible]: a menu with an item for each
// entry, the highlighted one active.
func (m *Menu) Access() access.Info {
	info := access.Info{Role: access.RoleMenu, Active: m.hot + 1}
	for i := range m.len() {
		part := access.Info{Role: access.RoleMenuItem, Name: m.label(i), Actions: []string{access.ActionPress}}
		if i == m.hot {
			part.State = access.StateSelected
		}
		if len(m.list.items[i].subItems()) > 0 {
			part.State |= access.StateExpandable | access.StateHasPopup
			if i == m.subOpen() {
				part.State |= access.StateExpanded
			}
		}
		part.Bounds = geom.Rc(m.card.Min.X, m.rowY(i), m.card.Size().W, m.row)
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing an item picks it, or opens its submenu.
func (m *Menu) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action == access.ActionPress && m.hasSub(r.Part) {
		m.openSub(r.Part, true, u)
		return true
	}
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= m.len() || m.OnPick == nil {
		return false
	}
	send(u, m, m.OnPick(r.Part, u))
	return true
}

// Access implements [gunim.Accessible].
func (t *tip) Access() access.Info { return access.Info{Role: access.RoleTooltip, Name: t.text} }

// Access implements [gunim.Accessible].
func (i *Image) Access() access.Info { return access.Info{Role: access.RoleImage, Name: i.Alt} }

// Access implements [gunim.Accessible].
func (d *Dialog) Access() access.Info {
	return access.Info{Role: access.RoleDialog, Name: d.Title, State: access.StateModal}
}

// Access implements [gunim.Accessible].
func (l *List) Access() access.Info { return access.Info{Role: access.RoleList} }

// Access implements [gunim.Accessible].
func (l *VirtualList) Access() access.Info { return access.Info{Role: access.RoleList} }

// Access implements [gunim.Accessible]: each of a list's rows is an
// item, holding what its node says.
func (r *row) Access() access.Info { return access.Info{Role: access.RoleListItem} }

// Access implements [gunim.Accessible].
func (s *Scroll) Access() access.Info { return access.Info{Role: access.RoleScrollArea} }
