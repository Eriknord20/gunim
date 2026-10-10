package filemanager

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

func registerSidebar(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, s Places, u *gunim.UI) { b.side.set(s, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, v PlaceMenuItems, u *gunim.UI) { b.dnd.sideMenu.give(v, u) })
}

// sidebar lists the places to go in sections: one for each group of
// places and one for the favourites, each under a heading that opens
// and closes it, and that a drag puts in another order. A drag puts the
// favourites in order too.
type sidebar struct {
	// sections holds a section for each heading, in the order shown.
	sections *widget.List
	// byID holds the sections by their IDs, and favSec is the one of the
	// favourites, whose rows are favs.
	byID   map[string]*section
	favSec *section
	favs   *widget.List
	hint   *widget.Label
	scroll *widget.Scroll
	// group makes the headings and the places one stop for Tab, walked with Up and Down.
	group *widget.Group
	// fs is the ID of the window's file system, and ps how it writes
	// paths.
	fs string
	ps PathStyle
	// items holds the rows of every section by their keys.
	items map[widget.Key]placeItem
	// order is the IDs of the sections as shown, and collapsed those
	// closed.
	order     []string
	collapsed map[string]bool
	// menuAt opens the context menu of a heading, as the Menu key asks.
	menuAt func(h *sectionHead, u *gunim.UI)
}

// placeItem is a place and whether it is the folder showing.
type placeItem struct {
	Place
	current bool
	// away is set for a place on another file system than the window's.
	away bool
	// key is the row's key in its list.
	key widget.Key
}

// placeKey is the key of the row of p: its path, for a place on the
// computer's own file system, and its file system and path for one
// elsewhere, so a place on one is not taken for one on another. The key
// does not depend on the window's file system, so a row stays as it is
// when the window turns to another.
func placeKey(p Place) widget.Key {
	if p.FS != "" {
		return widget.Key("\x00" + p.FS + "\x00" + p.Path)
	}
	return widget.Key(p.Path)
}

// goes says where a click on the row with key k goes: a place on the
// window's file system, or one on another.
func (s *sidebar) goes(k widget.Key) gunim.Intent {
	i, ok := s.items[k]
	switch {
	case !ok:
		return nil
	case i.away:
		return Visit{FS: i.FS, Path: i.Path}
	}
	return Navigate{Path: i.Path}
}

// favAt names the favourite of the row with key k.
func (s *sidebar) favAt(k widget.Key) FavouriteAt {
	if i, ok := s.items[k]; ok {
		return FavouriteAt{FS: i.FS, Path: i.Path}
	}
	return FavouriteAt{FS: s.fs, Path: string(k)}
}

func newSidebar() *sidebar {
	s := &sidebar{sections: widget.NewList(), byID: map[string]*section{}, items: map[widget.Key]placeItem{},
		collapsed: map[string]bool{}}
	// A click on a heading opens or closes its section, and a drag of one
	// puts the sections in another order. The headings take the keyboard
	// themselves.
	s.sections.OnActivate = func(k widget.Key, u *gunim.UI) gunim.Intent { return s.toggle(string(k)) }
	s.sections.ClickOnce, s.sections.SkipFocus = true, true
	s.sections.OnReorder = func(keys []widget.Key, u *gunim.UI) gunim.Intent {
		order := make([]string, len(keys))
		for i, k := range keys {
			order[i] = string(k)
		}
		s.order = order
		return SectionsArranged{Order: order}
	}
	s.hint = widget.NewLabel("Pin a folder here with Ctrl+D.")
	s.hint.Color = Caption
	s.hint.Size = SmallText
	s.favSec = s.newSection(sectionItem{id: FavouritesSection, title: "FAVOURITES"})
	s.favs = s.favSec.list
	s.favs.OnReorder = func(keys []widget.Key, u *gunim.UI) gunim.Intent {
		favs := make([]FavouriteAt, len(keys))
		for i, k := range keys {
			favs[i] = s.favAt(k)
		}
		return FavouritesReordered{Favourites: favs}
	}
	s.scroll = widget.NewScroll(&sidePad{child: widget.NewThemed(s.sections, sideTheme())})
	s.group = widget.NewGroup(widget.Vertical, s.scroll)
	return s
}

// sectionItem is a section as the sidebar lists it: its ID and heading.
type sectionItem struct {
	id, title string
}

// sectionOrder returns the IDs of the sections of groups and the
// favourites in the order saved, which the user left them in: those
// saved first, and then the rest, the first group before the favourites
// and the others after them.
func sectionOrder(groups, saved []string) []string {
	var defaults []string
	for i, g := range groups {
		if i == 1 {
			defaults = append(defaults, FavouritesSection)
		}
		defaults = append(defaults, GroupSection(g))
	}
	if len(groups) < 2 {
		defaults = append(defaults, FavouritesSection)
	}
	out := make([]string, 0, len(defaults))
	for _, id := range slices.Concat(saved, defaults) {
		if slices.Contains(defaults, id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// groupTitle is the heading of the section of group.
func groupTitle(group string) string {
	if group == "" {
		return "PLACES"
	}
	return strings.ToUpper(group)
}

func (s *sidebar) set(p Places, u *gunim.UI) {
	clear(s.items)
	// The places of a group are one section, in the order the groups
	// first come.
	var groups []string
	byGroup := map[string][]Place{}
	for _, pl := range p.Places {
		if _, ok := byGroup[pl.Group]; !ok {
			groups = append(groups, pl.Group)
		}
		byGroup[pl.Group] = append(byGroup[pl.Group], pl)
	}
	items := func(ps []Place) []placeItem {
		out := make([]placeItem, 0, len(ps))
		for _, pl := range ps {
			away := pl.FS != s.fs
			it := placeItem{Place: pl, away: away, current: !away && s.ps.Same(pl.Path, p.Current), key: placeKey(pl)}
			// A place listed twice, as in two groups, takes a key of its own.
			for n := 2; ; n++ {
				if _, taken := s.items[it.key]; !taken {
					break
				}
				it.key = placeKey(pl) + widget.Key(fmt.Sprintf("\x00%d", n))
			}
			s.items[it.key] = it
			out = append(out, it)
		}
		return out
	}
	// Keys are given in one order, whatever the sections', so a row keeps
	// its key as the sections move.
	rows := map[string][]placeItem{}
	for _, g := range groups {
		rows[GroupSection(g)] = items(byGroup[g])
	}
	rows[FavouritesSection] = items(p.Favourites)

	s.order = sectionOrder(groups, p.Order)
	clear(s.collapsed)
	for _, id := range p.Collapsed {
		s.collapsed[id] = true
	}
	titles := map[string]string{FavouritesSection: "FAVOURITES"}
	for _, g := range groups {
		titles[GroupSection(g)] = groupTitle(g)
	}
	secs := make([]sectionItem, len(s.order))
	for i, id := range s.order {
		secs[i] = sectionItem{id: id, title: titles[id]}
	}
	widget.Sync(s.sections, u, secs, func(i sectionItem) widget.Key { return widget.Key(i.id) }, s.newSection,
		(*section).set)
	key := func(i placeItem) widget.Key { return i.key }
	for _, id := range s.order {
		if sec := s.byID[id]; sec != nil {
			widget.Sync(sec.list, u, rows[id], key, newPlaceRow, (*placeRow).set)
		}
	}
	if len(p.Favourites) == 0 {
		s.hint.Text = "Pin a folder here with Ctrl+D."
	} else {
		s.hint.Text = ""
	}
	u.Invalidate()
}

// toggle opens the section of ID id if it is closed, and else closes it,
// and returns the intent that keeps it so.
func (s *sidebar) toggle(id string) gunim.Intent {
	closed := !s.collapsed[id]
	s.collapsed[id] = closed
	return SectionCollapsed{ID: id, Collapsed: closed}
}

// moved returns the intent that moves the section of ID id by step
// places, up for a step below zero, or nil where it cannot go.
func (s *sidebar) moved(id string, step int) gunim.Intent {
	i := slices.Index(s.order, id)
	j := i + step
	if i < 0 || j < 0 || j >= len(s.order) {
		return nil
	}
	order := slices.Clone(s.order)
	order[i], order[j] = order[j], order[i]
	s.order = order
	return SectionsArranged{Order: order}
}

// editFocused returns the intent to edit the favourite the keyboard is
// on, or nil where it is on none.
func (s *sidebar) editFocused(u *gunim.UI) gunim.Intent {
	if u.Focused() != s.favs {
		return nil
	}
	k, ok := s.favs.Cursor()
	if !ok {
		return nil
	}
	return EditFavourite(s.favAt(k))
}

// lists returns the lists of places of the sections, in the order shown,
// those of the sections closed too.
func (s *sidebar) lists() []*widget.List {
	out := make([]*widget.List, 0, len(s.order))
	for _, id := range s.order {
		if sec := s.byID[id]; sec != nil {
			out = append(out, sec.list)
		}
	}
	return out
}

// newSection makes the section of it: the favourites' is made once, and
// a group's each time it comes.
func (s *sidebar) newSection(it sectionItem) *section {
	if it.id == FavouritesSection && s.favSec != nil {
		return s.favSec
	}
	c := &section{s: s, id: it.id, list: widget.NewList(), open: anim.NewFloat(1)}
	c.Add(c.open)
	c.list.OnActivate = func(k widget.Key, _ *gunim.UI) gunim.Intent { return s.goes(k) }
	// A double click on a place goes there once, and with Ctrl opens
	// one window, as Explorer's do.
	c.list.ClickOnce = true
	c.head = newSectionHead(c, it.title)
	c.body = c.list
	if it.id == FavouritesSection {
		c.body = &favBody{list: c.list, hint: s.hint}
	}
	s.byID[it.id] = c
	return c
}

// section is one heading of the sidebar and its places, which close
// under the heading.
type section struct {
	anim.Group
	s    *sidebar
	id   string
	head *sectionHead
	list *widget.List
	// body is the list, and for the favourites the hint under it.
	body gunim.Node
	// open runs from 0, closed, to 1, open.
	open *anim.Float
	laid bool
	// headH is the heading's height at the last layout.
	headH float32
}

func (c *section) set(it sectionItem, u *gunim.UI) {
	c.head.setTitle(it.title)
	u.Invalidate()
}

// closed reports whether the user closed the section.
func (c *section) closed() bool { return c.s.collapsed[c.id] }

// The room between a heading and its places, and below the places of a
// section open.
const sectionGap, sectionBelow = 2, 10

// Children implements [gunim.Composite].
func (c *section) Children() []gunim.Node { return []gunim.Node{c.head, c.body} }

// Layout implements [gunim.Node]: the heading, and the places below it
// as far as the section is open. While a heading is dragged, every
// section folds to its heading, so the sections move past each other a
// heading at a time.
func (c *section) Layout(cs gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w := cs.Max.W
	target := float32(1)
	if c.closed() || c.s.sections.DragHeld() {
		target = 0
	}
	if !c.laid {
		c.open.Jump(target)
		c.laid = true
	}
	c.open.Animate(target, widget.Quick.Get(f.Theme))
	head, body := kids.At(0), kids.At(1)
	hs := head.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, cs.Max.H)})
	head.Place(geom.Point{})
	c.headH = hs.H
	bs := body.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, cs.Max.H)})
	body.Place(geom.Pt(0, hs.H+sectionGap))
	open := min(max(c.open.Value(), 0), 1)
	return geom.Sz(w, hs.H+(sectionGap+bs.H+sectionBelow)*open)
}

// Paint implements [gunim.Node]. The places of a section closed are not
// drawn, so neither the pointer nor the keyboard finds them.
func (c *section) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	open := c.open.Value()
	switch {
	case open <= 0.01:
		return
	case open >= 0.999:
		kids.At(1).Paint(p)
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, c.headH, box.W, max(box.H-c.headH, 0)), Opacity: open,
		Clip: true})()
	kids.At(1).Paint(p)
}

// Handle implements [gunim.Handler]: a press below the heading that no
// place takes is kept from the sections' list, so only a heading drags
// its section.
func (c *section) Handle(e input.Event, _ *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	return ok && d.Button == input.ButtonPrimary && d.Pos.Y >= c.headH
}

// favBody is the favourites and, while there are none, the hint under
// them.
type favBody struct {
	list *widget.List
	hint *widget.Label
}

// Children implements [gunim.Composite].
func (b *favBody) Children() []gunim.Node { return []gunim.Node{b.list, b.hint} }

// Layout implements [gunim.Node].
func (b *favBody) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	ls := kids.At(0).Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, c.Max.H)})
	kids.At(0).Place(geom.Point{})
	h := ls.H
	if b.hint.Text != "" {
		hs := kids.At(1).Layout(gunim.Constraints{Max: geom.Sz(w, c.Max.H)})
		if h > 0 {
			h += 4
		}
		kids.At(1).Place(geom.Pt(4, h))
		h += hs.H
	} else {
		kids.At(1).Layout(gunim.Constraints{Max: geom.Sz(w, 0)})
	}
	return geom.Sz(w, h)
}

// Paint implements [gunim.Node].
func (b *favBody) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	if b.hint.Text != "" {
		kids.At(1).Paint(p)
	}
}

// headHeight is the height of a section's heading.
const headHeight = 26

// sectionHead is the heading of a section: its title, and a chevron
// that turns as the section closes. A click or Enter opens or closes the
// section, and a drag moves it.
type sectionHead struct {
	anim.Group
	sec   *section
	title string
	run   text.Run
	shown string
	size  float32
	hover *anim.Float
	// lit shows the heading has the keyboard in the sidebar's group, and
	// ring that it has it alone.
	lit  *anim.Float
	ring *anim.Float
}

func newSectionHead(c *section, title string) *sectionHead {
	h := &sectionHead{sec: c, title: title, hover: anim.NewFloat(0), lit: anim.NewFloat(0), ring: anim.NewFloat(0)}
	h.Add(h.hover, h.lit, h.ring)
	return h
}

func (h *sectionHead) setTitle(t string) { h.title = t }

// Focusable implements [gunim.Focusable].
func (h *sectionHead) Focusable() bool { return true }

// Layout implements [gunim.Node].
func (h *sectionHead) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	size := SmallText.Get(f.Theme)
	if h.shown != h.title || h.size != size {
		face := text.Default()
		if bold := widget.BoldFont.Get(f.Theme); bold != nil {
			face = bold
		}
		h.run, h.shown, h.size = face.Shape(h.title, size), h.title, size
	}
	return geom.Sz(c.Max.W, headHeight)
}

// Paint implements [gunim.Node].
func (h *sectionHead) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	all := geom.Rect{Max: box.Point()}
	// The heading hides what it passes over as it is dragged.
	p.RRect(all, 6, paint.Solid(SidebarFill.Get(th)))
	if t := max(h.hover.Value(), h.lit.Value()); t > 0.01 {
		c := SidebarHot.Get(th)
		c.A = uint8(float32(c.A) * min(t, 1))
		p.RRect(all, 6, paint.Solid(c))
	}
	if t := h.ring.Value(); t > 0.01 {
		c := widget.Accent.Get(th)
		c.A = uint8(float32(c.A) * 0.56 * min(t, 1))
		p.RRectStroke(all.Inset(geom.Uniform(1)), 5, paint.Fill{}, paint.Stroke{Width: 2, Color: c})
	}
	ink := Caption.Get(th)
	h.run.Paint(p, geom.Pt(6, (box.H-h.run.Height())/2), ink)
	// The chevron points down while the section is open, and turns to
	// point at the heading as it closes.
	const s = 14
	mid := geom.Pt(box.W-6-s/2, box.H/2)
	turn := float32(1 - min(max(h.sec.open.Value(), 0), 1))
	defer p.Push(paint.Rotate(-math.Pi/2*turn, mid))()
	widget.PaintIcon(p, th, icon.ChevronDown, geom.Rc(mid.X-s/2, mid.Y-s/2, s, s), ink)
}

// Handle implements [gunim.Handler]. The pointer's presses go by to the
// sections' list, which tells a click from a drag.
func (h *sectionHead) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerEnter:
		h.hover.Animate(1, widget.Quick.Get(th))
	case input.PointerLeave:
		h.hover.Animate(0, widget.Settle.Get(th))
	case input.KeyPress:
		shift := e.Mods.Has(input.ModShift)
		switch {
		case e.Key == input.KeyMenu, shift && e.Key == input.KeyF10:
			if h.sec.s.menuAt != nil {
				h.sec.s.menuAt(h, u)
			}
		case e.Mods != 0:
			return false
		case e.Key == input.KeyEnter, e.Key == input.KeyKPEnter, e.Key == input.KeySpace:
			u.Send(h, h.sec.s.toggle(h.sec.id))
		default:
			return false
		}
	case input.FocusGained:
		if e.Step != 0 || e.Grouped {
			h.lit.Animate(1, widget.Quick.Get(th))
		}
		return false
	case input.FocusRing:
		if e.On && e.Grouped {
			h.ring.Animate(0, widget.RingFade.Get(th))
			h.lit.Animate(1, widget.Quick.Get(th))
			break
		}
		if e.On {
			h.ring.Animate(1, widget.RingFade.Get(th))
		} else {
			h.ring.Animate(0, widget.RingFade.Get(th))
		}
	case input.FocusLost:
		h.ring.Animate(0, widget.RingFade.Get(th))
		h.lit.Animate(0, widget.Settle.Get(th))
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Cursor implements [gunim.CursorShaper].
func (h *sectionHead) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// Access implements [gunim.Accessible]: a heading is a button that
// opens and closes its section.
func (h *sectionHead) Access() access.Info {
	state := access.StateExpandable
	if !h.sec.closed() {
		state |= access.StateExpanded
	}
	return access.Info{Role: access.RoleButton, Name: h.title, State: state, Actions: []string{access.ActionPress}}
}

// AccessAct implements [gunim.AccessActor].
func (h *sectionHead) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress {
		return false
	}
	u.Send(h, h.sec.s.toggle(h.sec.id))
	u.Invalidate()
	return true
}

// Children implements [gunim.Composite].
func (s *sidebar) Children() []gunim.Node { return []gunim.Node{s.group} }

// Layout implements [gunim.Node].
func (s *sidebar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (s *sidebar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(SidebarFill.Get(f.Theme)))
	kids.At(0).Paint(p)
}

// sidePad leaves room around the sidebar's rows.
type sidePad struct {
	child gunim.Node
}

// Children implements [gunim.Composite].
func (s *sidePad) Children() []gunim.Node { return []gunim.Node{s.child} }

// Layout implements [gunim.Node].
func (s *sidePad) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	size := kid.Layout(gunim.Constraints{Min: geom.Sz(c.Max.W-16, 0), Max: geom.Sz(c.Max.W-16, 0)})
	kid.Place(geom.Pt(8, 12))
	return geom.Sz(c.Max.W, size.H+24)
}

// Paint implements [gunim.Node].
func (s *sidePad) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// placeRow is one place in the sidebar: a mark in its colour, its name,
// and for a volume a bar of how full it is.
type placeRow struct {
	anim.Group
	item  placeItem
	hover *anim.Float
	on    *anim.Float
	used  *anim.Float
	name  text.Run
	note  text.Run
	shown string
	size  float32
	// cutName and cutNote are name and note cut short to the room the
	// row had at width cutW.
	cutName, cutNote text.Run
	cutW             float32
	// tip shows the name and the note whole while either is cut short.
	tip widget.PartTip
}

func newPlaceRow(i placeItem) *placeRow {
	r := &placeRow{item: i, hover: anim.NewFloat(0), on: anim.NewFloat(0), used: anim.NewFloat(0), cutW: -1}
	r.Add(r.hover, r.on, r.used)
	if i.current {
		r.on.Jump(1)
	}
	return r
}

func (r *placeRow) set(i placeItem, u *gunim.UI) {
	r.item = i
	r.on.Animate(map[bool]float32{false: 0, true: 1}[i.current], widget.Quick.Get(u.Theme()))
	r.shown = ""
	u.Invalidate()
}

// note is the small line under a volume's name, or a place's note.
func (r *placeRow) noteText() string {
	switch {
	case r.item.Err != "":
		return r.item.Err
	case r.item.Total > 0:
		return humanBytes(int64(r.item.Free)) + " free of " + humanBytes(int64(r.item.Total))
	}
	return r.item.Note
}

func (r *placeRow) shape(th *theme.Live) {
	size := widget.TextSize.Get(th)
	key := r.item.Name + "\x00" + r.noteText()
	if key == r.shown && size == r.size {
		return
	}
	r.shown, r.size = key, size
	r.name = text.Default().Shape(r.item.Name, size)
	r.note = text.Default().Shape(r.noteText(), SmallText.Get(th))
	r.cutW = -1
}

// textLeft is where a row's name and note start, and textRight the room
// kept after them.
const textLeft, textRight = 34, 10

// cut cuts the name and the note short, with an ellipsis, to fit a row
// width wide.
func (r *placeRow) cut(width float32) {
	if width == r.cutW {
		return
	}
	r.cutW = width
	room := max(0, width-textLeft-textRight)
	r.cutName, r.cutNote = widget.CutRun(r.name, room), widget.CutRun(r.note, room)
}

// tall reports whether the row has a second line.
func (r *placeRow) tall() bool { return r.noteText() != "" }

// Layout implements [gunim.Node].
func (r *placeRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	r.shape(f.Theme)
	h := float32(30)
	if r.tall() {
		h += 18
	}
	if r.item.Total > 0 {
		r.used.Animate(1-float32(float64(r.item.Free)/float64(r.item.Total)), widget.Settle.Get(f.Theme))
	}
	return geom.Sz(c.Max.W, h)
}

// Paint implements [gunim.Node].
func (r *placeRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	const t = 0
	full := geom.Rect{Max: box.Point()}
	if h := r.hover.Value(); h > 0.01 {
		c := SidebarHot.Get(th)
		c.A = uint8(float32(c.A) * min(h, 1))
		p.RRect(full, 7, paint.Solid(c))
	}
	if on := r.on.Value(); on > 0.01 {
		c := SidebarOn.Get(th)
		c.A = uint8(float32(c.A) * min(on, 1))
		p.RRect(full, 7, paint.Solid(c))
		// The accent edge grows from the middle.
		h := box.H - t
		bar := geom.Rc(0, t+h/2*(1-on)+4*on, 3, (h-8)*on)
		p.RRect(bar, 1.5, paint.Solid(widget.Accent.Get(th)))
	}
	line := float32(30)
	if r.item.Kind == "favourite" {
		paintFavMark(p, th, r.item.Color, r.item.Icon, geom.Pt(18, t+line/2))
	} else {
		mark := placeMark(r.item.Place).Get(th)
		p.RRect(geom.Rc(12, t+(line-12)/2, 12, 12), 3.5, paint.Solid(mark))
	}
	ink := widget.Ink.Get(th)
	r.cut(box.W)
	r.cutName.Paint(p, geom.Pt(textLeft, t+(line-r.name.Height())/2), ink)
	if !r.tall() {
		return
	}
	noteInk := Faint.Get(th)
	if r.item.Err != "" {
		noteInk = ErrorInk.Get(th)
	}
	y := t + line - 2
	if r.item.Total > 0 {
		track := geom.Rc(34, y, box.W-46, 4)
		p.RRect(track, 2, paint.Solid(widget.ProgressTrack.Get(th)))
		used := min(max(r.used.Value(), 0), 1)
		fill := widget.Accent.Get(th)
		if used > 0.9 {
			fill = ErrorInk.Get(th)
		}
		p.RRect(geom.Rc(track.Min.X, y, track.Size().W*used, 4), 2, paint.Solid(fill))
		y += 6
	}
	r.cutNote.Paint(p, geom.Pt(textLeft, y), noteInk)
}

// placeMark is the colour of the mark of p: green while it is lit, and
// else that of its kind.
func placeMark(p Place) theme.Token[color.NRGBA] {
	if p.Lit {
		return PlaceLit
	}
	return placeTint(p.Kind)
}

// placeTint is the colour of a place's mark.
func placeTint(kind string) theme.Token[color.NRGBA] {
	switch kind {
	case "drive":
		return TintToken(TintOther)
	case "favourite":
		return widget.Accent
	case "pictures":
		return TintToken(TintImage)
	case "documents":
		return TintToken(TintDocument)
	case "downloads":
		return TintToken(TintCode)
	}
	return TintToken(TintFolder)
}

// Handle implements [gunim.Handler]: the row lights under the pointer,
// and leaves presses to the list, which tells a click from a drag.
func (r *placeRow) Handle(e input.Event, u *gunim.UI) bool {
	r.tip.Handle(e, u, r, r.tipText())
	switch e.(type) {
	case input.PointerEnter:
		r.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		r.hover.Animate(0, widget.Settle.Get(u.Theme()))
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (r *placeRow) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// tipText is what the last paint cut short of the row's name and note,
// whole, and else nothing.
func (r *placeRow) tipText() string {
	if r.cutW < 0 {
		return ""
	}
	var parts []string
	if len(r.cutName.Glyphs) != len(r.name.Glyphs) {
		parts = append(parts, r.item.Name)
	}
	if len(r.cutNote.Glyphs) != len(r.note.Glyphs) {
		parts = append(parts, r.noteText())
	}
	return strings.Join(parts, " · ")
}

// paintFavMark draws a favourite's mark centred on c: its icon in its
// colour, on a tile of the colour.
func paintFavMark(p *paint.Painter, th *theme.Live, colour, ic string, c geom.Point) {
	ink := favColor(colour).Get(th)
	tile := ink
	tile.A = uint8(255 * min(max(FavTile.Get(th), 0), 1))
	p.RRect(geom.Rc(c.X-10, c.Y-10, 20, 20), 6, paint.Solid(tile))
	widget.PaintIcon(p, th, favIcon(ic), geom.Rc(c.X-7, c.Y-7, 14, 14), ink)
}
