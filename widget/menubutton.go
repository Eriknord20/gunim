package widget

import (
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// MenuButton is a button that opens a menu below it, or above it where
// the screen runs out. Its fill warms under the pointer, focus grows a
// ring round it, and its chevron turns over as the menu opens and back
// as it shuts.
//
// A click opens the menu and another closes it. With focus, Space, Enter
// and Down open it; Up, Down, Home and End move through it; Enter or
// Space picks; Escape and Tab close it.
//
// With an Icon and no Title it is a square button showing the icon
// alone, clear until the pointer comes over it, as an [IconButton] is:
// for a menu of more actions, such as "⋯". Its Tooltip names it.
//
// With StayOpen the menu stays open after a pick, and a pick ticks or
// unticks the item, for a set of choices where several can be on, such
// as the files a filter lets through.
type MenuButton struct {
	Control

	Title string
	// Icon shows before the title.
	Icon *icon.Icon
	// StayOpen keeps the menu open after a pick, and flips the item's tick.
	StayOpen bool
	// Active draws the button's border in the accent colour, as when its
	// choices narrow something down.
	Active bool
	// OnPick runs on the UI goroutine as an item is picked. It may act in
	// the window through u, such as opening a dialog; a non-nil result is
	// sent to the application as the button's intent.
	OnPick func(i int, u *gunim.UI) gunim.Intent

	turn *anim.Float

	popup *gunim.Popup
	menu  *Menu
	list  *menuList
	size  geom.Size
	shown shapedText
	ell   shapedText
}

// NewMenuButton returns a button titled title that opens a menu of items.
func NewMenuButton(title string, items []MenuItem) *MenuButton {
	b := &MenuButton{Control: newControl(), Title: title, list: newMenuList(items), turn: anim.NewFloat(0)}
	b.Add(b.turn)
	return b
}

// Items returns the menu's items. With StayOpen, a pick flips its item's Checked here.
func (b *MenuButton) Items() []MenuItem { return b.list.items }

// SetItems makes items the menu's items. The button keeps the slice, and with StayOpen a pick flips an item's
// Checked in it: a change to it goes through SetItems again. The menu open shows them at once.
func (b *MenuButton) SetItems(items []MenuItem) { b.list = newMenuList(items) }

// IsOpen reports whether the menu is open.
func (b *MenuButton) IsOpen() bool { return b.popup != nil && b.popup.Open() }

// Handle implements [gunim.Handler].
func (b *MenuButton) Handle(e input.Event, u *gunim.UI) bool {
	b.showTip(e, u, b)
	th := u.Theme()
	if b.Disabled {
		return b.handleDisabled(e, u, b.close)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover.Animate(1, Quick.Get(th))
	case input.PointerLeave:
		b.hover.Animate(0, Settle.Get(th))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		b.toggle(u)
	case input.PointerUp:
	case input.FocusRing:
		b.ring.Animate(ringTo(e), RingFade.Get(th))
	case input.FocusLost:
		b.ring.Animate(0, RingFade.Get(th))
		b.close(u)
	case input.KeyPress:
		return b.key(e, u)
	default:
		return false
	}
	return true
}

func (b *MenuButton) toggle(u *gunim.UI) {
	if b.IsOpen() {
		b.close(u)
	} else {
		b.open(u)
	}
}

func (b *MenuButton) key(k input.KeyPress, u *gunim.UI) bool {
	if !b.IsOpen() {
		switch k.Key {
		case input.KeySpace, input.KeyEnter, input.KeyKPEnter, input.KeyDown:
			b.open(u)
			return true
		default:
			return false
		}
	}
	switch k.Key {
	case input.KeyEscape:
		u.Cue(gunim.CueClose, b)
		b.close(u)
		return true
	case input.KeyTab:
		b.close(u)
		return false
	default:
		return b.menu.Key(k, u)
	}
}

func (b *MenuButton) open(u *gunim.UI) {
	m := NewMenu(nil)
	b.sync(m)
	m.MinWidth = b.size.W
	m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if b.StayOpen {
			items := b.list.items
			items[i].Checked = !items[i].Checked
			b.SetItems(items)
			b.sync(m)
		} else {
			b.close(u)
		}
		if b.OnPick != nil {
			send(u, b, b.OnPick(i, u))
		}
		u.Invalidate()
		return nil
	}
	b.menu = m
	u.Cue(gunim.CueOpen, b)
	b.popup = u.OpenPopup(b, m, gunim.PopupOptions{
		Anchor:  geom.Rect{Max: b.size.Point()},
		Max:     geom.Sz(600, 560),
		Dismiss: dismissed(b, b.close),
	})
	b.turn.Animate(1, Quick.Get(u.Theme()))
}

// sync gives the open menu the button's items as they are now.
func (b *MenuButton) sync(m *Menu) { m.setList(b.list) }

func (b *MenuButton) close(u *gunim.UI) { b.shut(u.Theme()) }

// shut closes the menu.
func (b *MenuButton) shut(th *theme.Live) {
	if b.popup != nil {
		b.popup.Close()
		b.popup = nil
	}
	b.turn.Animate(0, Quick.Get(th))
}

// Layout implements [gunim.Node]. The button is as wide as its title.
func (b *MenuButton) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	if b.menu != nil && b.IsOpen() {
		b.sync(b.menu)
	}
	run := b.shown.shape(faceIn(Font, th), b.Title, TextSize.Get(th))
	pad := FieldPadding.Get(th)
	b.size = c.Constrain(geom.Sz(b.iconRoom(th)+run.Advance+2*pad+chevron+pad, FieldHeight.Get(th)))
	if b.iconOnly() {
		h := ButtonHeight.Get(th)
		b.size = c.Constrain(geom.Sz(h, h))
	}
	b.follow(th)
	if b.Disabled && b.popup != nil {
		b.shut(th)
	}
	return b.size
}

// Paint implements [gunim.Node].
func (b *MenuButton) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	defer b.faint(p, box)()
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	if b.iconOnly() {
		b.paintIconOnly(p, th, box)
		return
	}
	radius := FieldRadius.Get(th)
	b.paintRing(p, r, radius, th)
	fill := anim.Mix(anim.ColorCodec, ButtonFill.Get(th), ButtonHover.Get(th), b.hover.Value())
	border := FieldBorder.Get(th)
	if b.Active {
		border = Accent.Get(th)
	}
	p.RRectStroke(r, radius, paint.Solid(fill), paint.Stroke{Width: 1, Color: border})
	pad := FieldPadding.Get(th)
	if b.Icon != nil {
		s := IconSize.Get(th)
		paintIcon(p, th, b.Icon, geom.Rc(pad, (box.H-s)/2, s, s), Ink.Get(th), 1)
	}
	// A title wider than the box is cut short before the chevron.
	run := b.shown.run
	x := pad + b.iconRoom(th)
	if room := box.W - x - pad - chevron - pad; run.Advance > room {
		run = cutRunWith(run, b.ell.shape(faceIn(Font, th), "…", TextSize.Get(th)), room)
	}
	run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), Ink.Get(th))
	c := geom.Pt(box.W-pad-chevron/2, box.H/2)
	defer p.Push(paint.Rotate(math.Pi*b.turn.Value(), c))()
	paintChevron(p, th, c, Ink.Get(th))
}

// iconOnly reports whether the button shows its icon alone.
func (b *MenuButton) iconOnly() bool { return b.Title == "" && b.Icon != nil }

// paintIconOnly draws a button that shows its icon alone: clear, but
// for a fill under the pointer and while the menu is open.
func (b *MenuButton) paintIconOnly(p *paint.Painter, th *theme.Live, box geom.Size) {
	r := geom.Rect{Max: box.Point()}
	radius := ButtonRadius.Get(th)
	b.paintRing(p, r, radius, th)
	if on := max(b.hover.Value(), b.turn.Value()); on > 0.001 {
		fill := ButtonHover.Get(th)
		fill.A = uint8(float32(fill.A) * min(on, 1))
		p.RRect(r, radius, paint.Solid(fill))
	}
	s := IconSize.Get(th)
	paintIcon(p, th, b.Icon, geom.Rc((box.W-s)/2, (box.H-s)/2, s, s), Ink.Get(th), 1)
}

// iconRoom is the room the icon takes before the title, with its gap.
func (b *MenuButton) iconRoom(th *theme.Live) float32 {
	if b.Icon == nil {
		return 0
	}
	return IconSize.Get(th) + IconGap.Get(th)
}

// Access implements [gunim.Accessible].
func (b *MenuButton) Access() access.Info {
	info := access.Info{
		Role:    access.RoleButton,
		Name:    b.accessName(b.Title),
		State:   access.StateExpandable | access.StateHasPopup | b.accessState(),
		Actions: []string{access.ActionPress},
	}
	if b.IsOpen() {
		info.State |= access.StateExpanded
	}
	if info.Name == "" {
		info.Name = b.Tooltip
	}
	if info.Name == "" {
		info.Name = iconName(b.Icon)
	}
	return info
}

// AccessAct implements [gunim.AccessActor].
func (b *MenuButton) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || b.Disabled {
		return false
	}
	b.toggle(u)
	return true
}
