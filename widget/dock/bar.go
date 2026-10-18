package dock

import (
	"math"
	"slices"
	"time"

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

// A Face is how a tab looks.
type Face struct {
	// Title is what the tab says, and Short what it says where the
	// title does not fit, "" for nothing shorter. A title cut short
	// fades out at the tab's edge, and shows whole in a tooltip.
	Title, Short string
	// More is said faintly after the title, as "+2" for a tab of three
	// panes.
	More string
	// Icon is drawn before the title; nil draws none.
	Icon *icon.Icon
	// Closable gives the tab a ×, shown on the tab in front and the one
	// under the pointer, and lets the middle button close it.
	Closable bool
}

// tab is a tab on a bar: the node it shows, and how it looks.
type tab struct {
	id string
	Face
}

// Bar is a row of tabs. A [Dock] makes one for each group and lays it
// out over the group, or hands it to the application to put in the
// title bar. NewBar makes one that stands alone, which the application
// fills with SetTabs, as one row of tabs in its title bar over panes
// it lays out itself.
//
// A click shows a tab. The middle button, or its ×, closes it, and the
// + after the tabs asks for a new one. A tab dragged along the bar
// moves along it; onto another bar it joins that group; onto a pane it
// splits it or joins its group; and let go outside every window it asks
// for a window of its own. A drag held over a tab brings it to the
// front, to drop beside a pane in it.
//
// The tabs slide to their places, grow in as they open and shrink away
// as they close, and the tab in front's fill glides to the tab picked.
// A screen reader sees a tab list, and presses a tab to show it.
type Bar struct {
	anim.Group
	// Win is the window the bar is in, which the tabs dragged off it
	// name; a bar on its own whose window learns its number later sets
	// it then.
	Win int
	// Caption lets the room after the tabs move the window, as a title
	// bar does.
	Caption bool
	// Plus puts a + after the tabs, which asks for a New tab. A dock
	// sets it on its bars from its own.
	Plus bool
	// Plain draws no fill behind the tabs, for a bar in a title bar that
	// has its own.
	Plain bool
	// DetachSize is how large a window of its own a tab of a bar on its
	// own asks for; a dock's bars ask for their group's size.
	DetachSize geom.Size
	// OnChange, on a bar on its own, runs on the UI goroutine with what
	// the user asks for: Show, Close, New, Move (onto this bar, To.At
	// naming its group) and Detach; a non-nil result is sent to the
	// application as the bar's intent. Without it the Change is sent
	// itself. A dock's bars ask their dock's OnChange.
	OnChange func(c Change, u *gunim.UI) gunim.Intent
	// OnPlusMenu, when set, runs as the + is clicked with the second
	// button, with where the + is in the bar's space, to open a menu of
	// what a new tab can hold.
	OnPlusMenu func(at geom.Rect, u *gunim.UI) gunim.Intent
	// Takes says the bar takes data other than a tab dragged over it,
	// landing between the tabs; OnDrop runs with it, and where, as it is
	// let go there.
	Takes  func(data any) bool
	OnDrop func(d Dropped, u *gunim.UI) gunim.Intent

	d     *Dock
	group string
	tabs  []tab
	front string
	// titles are the tabs' titles, shaped, shorts their short titles
	// and more what each says after; cut says which tabs showed less
	// than their title when last drawn, and so show it in a tooltip.
	titles, shorts, more []text.Run
	cut                  []bool
	tip                  widget.PartTip
	shapedSize           float32
	shapedFace           *text.Face
	// boxes are where the layout puts the tabs, plus where the + is,
	// and end where the tabs and the + end. reserve is room kept free
	// at the bar's right end, for the window's buttons.
	boxes   []geom.Rect
	plusBox geom.Rect
	end     float32
	reserve float32
	// hot is the tab under the pointer, and pressed the one the pointer
	// went down on, at pressAt, on its × with pressedCross; -1 for none.
	hot, pressed           int
	pressAt                geom.Point
	pressedCross, crossHot bool
	plusPressed            bool
	// carried is the tab dragged off the bar, "" for none, grabbed at
	// grab from its top left corner.
	carried string
	grab    geom.Point
	// landing is where a tab dragged over the bar would land: before
	// the tab at that index, len(tabs) for last, -1 while none is over.
	landing int
	// spring is the tab a drag is held over, which comes to the front
	// after springHold; springRun numbers the wait, so one the drag
	// moved on from does nothing. sprungFrom is the tab in front before
	// a drag brought another forward, to go back to if the drag is let
	// go where nothing takes it.
	spring, sprungFrom string
	springRun          int
	// sprungFor is the drag that brought a tab forward, and dragging the
	// drag of a tab off this bar.
	sprungFor, dragging Drag
	// moves are how the tabs move, by ID: each slides to its box, grows
	// in as it opens and shrinks away in gone as it closes. arriving
	// are the tabs new on the bar, which grow in; plusAt is where the +
	// is drawn, sliding after the tabs.
	moves    map[string]*tabMove
	arriving map[string]bool
	gone     []tabGone
	plusAt   *anim.Rect
	// lit is how far the front tab's fill has come from the tab that
	// was in front, was, where it was drawn, litFrom.
	lit     *anim.Float
	litFrom geom.Rect
	was     string
	// mark is how far the front tab's underline shows: it does in the
	// group the keys work in, while the window shows several panes.
	mark *anim.Float
	laid bool
	// dropped is the tab whose drag ended taken, or let go outside every
	// window, until the tabs change.
	dropped string
}

// A Tab is a tab on a bar on its own: the node it stands for, by its
// ID, and how it looks.
type Tab struct {
	ID string
	Face
}

// Dropped is something other than a tab let go on a bar on its own,
// which its Takes took: the data the drag carried, and the place among
// the tabs it landed at, len of the tabs for last.
type Dropped struct {
	Data  any
	Index int
}

// NewBar returns a bar on its own, for the application to fill with
// SetTabs. group names it in the Moves it sends, and win is the window
// it is in, which the tabs dragged off it name.
func NewBar(group string, win int) *Bar { return makeBar(nil, group, win) }

// SetTabs puts tabs on a bar on its own, front in front. Once laid out,
// tabs new on it grow in, tabs gone shrink away, the rest slide to
// their places, and the front tab's fill glides to the one in front;
// before, they are simply there.
func (b *Bar) SetTabs(tabs []Tab, front string, u *gunim.UI) {
	ts := make([]tab, len(tabs))
	for i, t := range tabs {
		ts[i] = tab{id: t.ID, Face: t.Face}
	}
	b.set(ts, front, false, u)
}

// TabRect returns where tab id stands, in the bar's space, as the last
// layout put it and a click finds it; false where the bar has no such
// tab.
func (b *Bar) TabRect(id string) (geom.Rect, bool) {
	if i := b.boxOf(id); i >= 0 {
		return b.boxes[i], true
	}
	return geom.Rect{}, false
}

// PlusRect returns where the + stands, in the bar's space; empty
// without Plus.
func (b *Bar) PlusRect() geom.Rect { return b.plusBox }

// Carried returns the tab dragged off the bar, "" for none.
func (b *Bar) Carried() string { return b.carried }

// Dropped returns the tab whose drag off the bar ended taken, or let
// go outside every window, "" for none: the next tabs the bar is given
// may show the tab in front changing for it, which an application need
// not show as a switch. SetTabs forgets it once the tabs change.
func (b *Bar) Dropped() string { return b.dropped }

// change hands c to the bar's dock, or else to OnChange, or sends it.
func (b *Bar) change(c Change, u *gunim.UI) {
	if b.d != nil {
		b.d.change(c, b, u)
		return
	}
	var in gunim.Intent = c
	if b.OnChange != nil {
		in = b.OnChange(c, u)
	}
	if in != nil {
		u.Send(b, in)
	}
}

// pick brings tab id to the front at once, before the application says
// so, as the user picked it.
func (b *Bar) pick(id string, u *gunim.UI) {
	if b.d != nil {
		b.d.pick(id, b, u)
		return
	}
	if id == b.front || b.boxOf(id) < 0 {
		return
	}
	b.set(b.tabs, id, b.mark.Target() > 0.5, u)
	b.change(Show{ID: id}, u)
}

// detachSize is how large a window of its own a tab of the bar's asks
// for.
func (b *Bar) detachSize() geom.Size {
	if b.d != nil {
		return b.d.detachSize(b.group)
	}
	return b.DetachSize
}

// tabMove is how a tab moves on the bar: its box, sliding to where the
// layout puts it, and how far it is open, 0 to 1.
type tabMove struct {
	box  *anim.Rect
	open *anim.Float
}

func (m *tabMove) step(dt time.Duration) bool {
	moving := m.box.Step(dt)
	return m.open.Step(dt) || moving
}

// tabGone is a closed tab, drawn as it was while it shrinks away.
type tabGone struct {
	*tabMove
	tab         tab
	title, more text.Run
}

// The bar's fixed measures: a tab's icon, its ×, the + and the room a
// bar in the title bar leaves to move the window by.
const (
	tabCross     = 16
	tabPlus      = 30
	captionLeast = 64
	// slop is how far a tab pressed goes before it lifts off the bar.
	slop = 6
	// springHold is how long a drag is held over a tab before the tab
	// comes to the front.
	springHold = 500 * time.Millisecond
)

func makeBar(d *Dock, group string, win int) *Bar {
	b := &Bar{d: d, group: group, Win: win, hot: -1, pressed: -1, landing: -1,
		moves: map[string]*tabMove{}, arriving: map[string]bool{}, lit: anim.NewFloat(1), mark: anim.NewFloat(0)}
	b.Add(b.lit, b.mark)
	return b
}

// GroupID returns the ID of the group the bar shows.
func (b *Bar) GroupID() string { return b.group }

// Tabs returns the IDs of the bar's tabs, in order.
func (b *Bar) Tabs() []string {
	out := make([]string, len(b.tabs))
	for i, t := range b.tabs {
		out[i] = t.id
	}
	return out
}

// Front returns the ID of the tab in front.
func (b *Bar) Front() string { return b.front }

// set puts tabs on the bar, front in front, and marks the front tab
// where mark says. A change of the tab in front glides the fill there.
func (b *Bar) set(tabs []tab, front string, mark bool, u *gunim.UI) {
	if !b.laid || u == nil {
		b.mark.Jump(onOff(mark))
	} else {
		b.mark.Animate(onOff(mark), widget.Quick.Get(u.Theme()))
	}
	sameTabs := slices.EqualFunc(tabs, b.tabs, func(a, c tab) bool { return a == c })
	if sameTabs && front == b.front {
		return
	}
	if !sameTabs {
		b.dropped = ""
	}
	if len(tabs) != len(b.tabs) {
		b.hot, b.crossHot = -1, false
		if b.carried == "" {
			b.pressed = -1
		}
	}
	if b.laid && u != nil && front != b.front && b.front != "" && front != "" && b.carried == "" {
		b.litFrom, b.was = b.litAt(), b.front
		b.lit.Jump(0)
		b.lit.Animate(1, Glide.Get(u.Theme()))
	}
	if b.laid {
		b.tabsMoved(tabs, u.Theme())
	}
	titlesChanged := len(tabs) != len(b.tabs)
	for i := range tabs {
		if !titlesChanged && (tabs[i].Title != b.tabs[i].Title || tabs[i].Short != b.tabs[i].Short || tabs[i].More != b.tabs[i].More) {
			titlesChanged = true
		}
	}
	b.tabs, b.front = tabs, front
	if titlesChanged {
		b.shapedFace = nil
	}
	u.Invalidate()
}

// shape shapes the titles again where they or the text changed.
func (b *Bar) shape(th *theme.Live) {
	face, size := widget.Font.Get(th), TabText.Get(th)
	if TabBold.Get(th) {
		face = widget.BoldFont.Get(th)
	}
	if b.shapedFace == face && b.shapedSize == size && len(b.titles) == len(b.tabs) {
		return
	}
	b.shapedFace, b.shapedSize = face, size
	b.titles, b.shorts, b.more = b.titles[:0], b.shorts[:0], b.more[:0]
	for _, t := range b.tabs {
		b.titles = append(b.titles, face.Shape(t.Title, size))
		b.shorts = append(b.shorts, face.Shape(t.Short, size))
		b.more = append(b.more, face.Shape(t.More, size-1))
	}
	b.cut = make([]bool, len(b.tabs))
}

// tabsMoved notes what changes on the bar: a tab gone shrinks away
// where it was, and one new grows in where it lands.
func (b *Bar) tabsMoved(tabs []tab, th *theme.Live) {
	slide := Slide.Get(th)
	in := func(tabs []tab, id string) bool {
		return slices.ContainsFunc(tabs, func(t tab) bool { return t.id == id })
	}
	for i, t := range b.tabs {
		m := b.moves[t.id]
		if m == nil || in(tabs, t.id) {
			continue
		}
		delete(b.moves, t.id)
		g := tabGone{tabMove: m, tab: t}
		if i < len(b.titles) {
			g.title, g.more = b.titles[i], b.more[i]
		}
		r := m.box.Value()
		m.box.Animate(geom.Rc(r.Min.X, r.Min.Y, 0, r.Size().H), slide)
		m.open.Animate(0, slide)
		b.gone = append(b.gone, g)
	}
	if len(b.tabs) > 0 {
		for _, t := range tabs {
			if !in(b.tabs, t.id) {
				b.arriving[t.id] = true
			}
		}
	}
}

// litAt is where the front tab's fill is drawn: on its way from where
// it was while it glides, or on the tab in front.
func (b *Bar) litAt() geom.Rect {
	to, ok := b.drawnOf(b.front)
	if !ok {
		return geom.Rect{}
	}
	if t := b.lit.Value(); t < 1 && !b.litFrom.Empty() {
		return anim.Mix(anim.RectCodec, b.litFrom, to, max(t, 0))
	}
	return to
}

// drawnOf is where tab id is drawn, closing or not.
func (b *Bar) drawnOf(id string) (geom.Rect, bool) {
	if i := b.boxOf(id); i >= 0 {
		r, _ := b.drawnAt(i)
		return r, true
	}
	for _, g := range b.gone {
		if g.tab.id == id {
			return g.box.Value(), true
		}
	}
	return geom.Rect{}, false
}

// boxOf returns the place on the bar of tab id, laid out, -1 for none.
func (b *Bar) boxOf(id string) int {
	for i, t := range b.tabs {
		if t.id == id && i < len(b.boxes) {
			return i
		}
	}
	return -1
}

// litOf is how far tab id has the front tab's ink, 0 to 1.
func (b *Bar) litOf(id string) float32 {
	t := min(max(b.lit.Value(), 0), 1)
	switch id {
	case b.front:
		return t
	case b.was:
		return 1 - t
	}
	return 0
}

// Step implements [gunim.Animator].
func (b *Bar) Step(dt time.Duration) bool {
	moving := b.Group.Step(dt)
	for _, m := range b.moves {
		if m.step(dt) {
			moving = true
		}
	}
	if b.plusAt != nil && b.plusAt.Step(dt) {
		moving = true
	}
	kept := b.gone[:0]
	for _, g := range b.gone {
		if g.step(dt) {
			moving = true
			kept = append(kept, g)
		}
	}
	clear(b.gone[len(kept):])
	b.gone = kept
	return moving
}

// moveTabs starts each tab toward its box, a tab new on the bar from
// nothing where it lands, and reports whether any is moving.
func (b *Bar) moveTabs(th *theme.Live) bool {
	slide := Slide.Get(th)
	moving := false
	for i, t := range b.tabs {
		r := b.boxes[i]
		m := b.moves[t.id]
		if m == nil {
			m = &tabMove{box: anim.NewRect(r), open: anim.NewFloat(1)}
			if b.arriving[t.id] {
				m.box.Jump(geom.Rc(r.Min.X, r.Min.Y, 0, r.Size().H))
				m.open.Jump(0)
			}
			b.moves[t.id] = m
		}
		delete(b.arriving, t.id)
		if !b.laid {
			m.box.Jump(r)
			m.open.Jump(1)
		}
		m.box.Animate(r, slide)
		m.open.Animate(1, slide)
		moving = moving || m.box.Active() || m.open.Active()
	}
	if b.plusAt == nil || !b.laid {
		b.plusAt = anim.NewRect(b.plusBox)
	}
	b.plusAt.Animate(b.plusBox, slide)
	return moving || b.plusAt.Active() || len(b.gone) > 0
}

// drawnAt is where tab i is drawn, and how far it is open.
func (b *Bar) drawnAt(i int) (r geom.Rect, open float32) {
	if m := b.moves[b.tabs[i].id]; m != nil {
		return m.box.Value(), m.open.Value()
	}
	return b.boxes[i], 1
}

// fitTitle is what tab i says in room: its title where it fits, or its
// short title where it has one. cut says that is less than the title.
func (b *Bar) fitTitle(i int, room float32) (run text.Run, cut bool) {
	full := b.titles[i]
	switch {
	case full.Advance <= room:
		return full, false
	case b.shorts[i].Advance > 0:
		return b.shorts[i], true
	}
	return full, true
}

// tipOf is the tooltip at pos: the whole title of the tab there where
// the tab shows less of it.
func (b *Bar) tipOf(pos geom.Point) string {
	i := b.tabAt(pos)
	if i < 0 || b.carried != "" || i >= len(b.cut) || !b.cut[i] {
		return ""
	}
	return b.tabs[i].Title
}

// Layout implements [gunim.Node]: the tabs side by side, as wide as
// their titles within bounds, narrower all alike when the room runs
// short, then the +.
func (b *Bar) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	b.shape(th)
	h := BarHeight.Get(th)
	if c.Max.H > 0 {
		h = c.Max.H
	}
	pad, gap, inset, iconW := TabPadding.Get(th), TabGap.Get(th), TabInset.Get(th), TabIcon.Get(th)
	widest, least := TabWidest.Get(th), TabLeast.Get(th)
	b.boxes = b.boxes[:0]
	want := make([]float32, len(b.tabs))
	total := float32(0)
	for i, t := range b.tabs {
		w := 2*pad + b.titles[i].Advance
		if t.Icon != nil {
			w += iconW + 6
		}
		if t.Closable {
			w += tabCross + 2
		}
		if b.more[i].Advance > 0 {
			w += b.more[i].Advance + 6
		}
		want[i] = min(max(w, least), widest)
		total += want[i] + gap
	}
	room := c.Max.W - 2*inset - b.reserve
	if b.Plus {
		room -= tabPlus
	}
	if b.Caption {
		room -= captionLeast
	}
	if c.Max.W > 0 && total > room && len(b.tabs) > 0 {
		each := max(room/float32(len(b.tabs))-gap, 1)
		for i := range want {
			want[i] = min(want[i], each)
		}
	}
	x := inset
	for i := range b.tabs {
		b.boxes = append(b.boxes, geom.Rc(x, inset, want[i], h-2*inset))
		x += want[i] + gap
	}
	b.plusBox, b.end = geom.Rect{}, x
	if b.Plus {
		b.plusBox = geom.Rc(x+2, (h-24)/2, 24, 24)
		b.end = x + tabPlus
	}
	if b.moveTabs(th) {
		f.RedrawAt(f.Now)
	}
	b.laid = true
	w := c.Max.W
	if w <= 0 {
		w = b.end
	}
	return c.Constrain(geom.Sz(w, h))
}

// Paint implements [gunim.Node].
func (b *Bar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	if !b.Plain {
		// A bar in a title bar draws on the title bar's own fill.
		p.RRect(geom.Rect{Max: box.Point()}, BarRadius.Get(th), paint.Solid(BarFill.Get(th)))
	}
	for _, g := range b.gone {
		b.paintTab(p, th, g.tab, g.title, g.more, g.box.Value(), g.open.Value(), tabLook{at: -1})
	}
	radius := TabRadius.Get(th)
	if r := b.litAt(); r.Size().W > 0 {
		fill := TabFront.Get(th)
		if i := slices.IndexFunc(b.tabs, func(t tab) bool { return t.id == b.front }); i >= 0 {
			_, open := b.drawnAt(i)
			fill.A = uint8(float32(fill.A) * min(max(open, 0), 1))
		}
		p.RRect(r, radius, paint.Solid(fill))
		if m := b.mark.Value(); m > 0.01 {
			c := TabMark.Get(th)
			c.A = uint8(float32(c.A) * min(m, 1))
			p.RRect(geom.Rc(r.Min.X+radius, r.Max.Y-2, r.Size().W-2*radius, 2), 1, paint.Solid(c))
		}
	}
	for i, t := range b.tabs {
		r, open := b.drawnAt(i)
		b.paintTab(p, th, t, b.titles[i], b.more[i], r, open, tabLook{
			at:       i,
			lit:      b.litOf(t.id),
			hot:      i == b.hot && b.carried == "",
			front:    t.id == b.front,
			lifted:   t.id == b.carried,
			cross:    b.crossShown(i),
			crossLit: i == b.hot && b.crossHot,
		})
	}
	if b.landing >= 0 && len(b.boxes) > 0 && b.landing <= len(b.boxes) {
		gap := TabGap.Get(th)
		x := b.boxes[len(b.boxes)-1].Max.X + gap/2
		if b.landing < len(b.boxes) {
			x = b.boxes[b.landing].Min.X - gap/2
		}
		p.RRect(geom.Rc(x-1.5, 6, 3, box.H-12), 1.5, paint.Solid(DropRing.Get(th)))
	}
	if b.Plus && b.plusAt != nil {
		c := TabFaint.Get(th)
		if b.hot == len(b.tabs) {
			c = TabInk.Get(th)
		}
		widget.PaintIcon(p, th, icon.Plus, b.plusAt.Value().Inset(geom.Uniform(5)), c)
	}
}

// tabLook is how a tab is drawn. lit is how far it has the front tab's
// ink, and at its place on the bar, -1 for one going.
type tabLook struct {
	at                                  int
	lit                                 float32
	hot, front, lifted, cross, crossLit bool
}

// paintTab draws tab t in r, open as far as open says.
func (b *Bar) paintTab(p *paint.Painter, th *theme.Live, t tab, title, more text.Run, r geom.Rect, open float32, l tabLook) {
	if r.Size().W <= 0 || open <= 0 {
		return
	}
	if open < 1 {
		defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: min(open, 1)})()
	}
	ink, faint := TabInk.Get(th), TabFaint.Get(th)
	if l.hot && !l.front {
		p.RRect(r, TabRadius.Get(th), paint.Solid(TabHover.Get(th)))
	}
	c := faint
	switch {
	case l.lit >= 1:
		c = ink
	case l.lit > 0:
		c = anim.Mix(anim.ColorCodec, faint, ink, l.lit)
	}
	if l.lifted {
		c.A /= 3
	}
	pad, tabIcon := TabPadding.Get(th), TabIcon.Get(th)
	mid := r.Min.Y + r.Size().H/2
	x := r.Min.X + pad
	if t.Icon != nil {
		if r.Size().W < pad+tabIcon+6 {
			// Too narrow for its title: its icon alone, in the middle.
			if r.Size().W >= tabIcon+4 {
				widget.PaintIcon(p, th, t.Icon, geom.Rc(r.Center().X-tabIcon/2, mid-tabIcon/2, tabIcon, tabIcon), c)
			}
			return
		}
		widget.PaintIcon(p, th, t.Icon, geom.Rc(x, mid-tabIcon/2, tabIcon, tabIcon), c)
		x += tabIcon + 6
	}
	stop := r.Max.X - pad
	if l.cross {
		stop -= tabCross
	}
	if more.Advance > 0 {
		mc := faint
		mc.A = uint8(float32(mc.A) * 0.8)
		more.Paint(p, geom.Pt(stop-more.Advance, mid-more.Height()/2), mc)
		stop -= more.Advance + 6
	}
	if room := stop - x; room > 0 {
		if l.at >= 0 && l.at < len(b.titles) {
			var cut bool
			title, cut = b.fitTitle(l.at, room)
			b.cut[l.at] = cut
		}
		title.PaintFaded(p, geom.Pt(x, mid-title.Height()/2), c, room, widget.TextFade.Get(th))
	}
	if l.cross {
		cc := faint
		if l.crossLit {
			cc = ink
		}
		widget.PaintIcon(p, th, icon.X, crossIn(r, pad), cc)
	}
}

// crossIn is where the × of a tab in r is.
func crossIn(r geom.Rect, pad float32) geom.Rect {
	const s = 12
	return geom.Rc(r.Max.X-pad-s+2, r.Min.Y+(r.Size().H-s)/2, s, s)
}

// crossShown reports whether tab i shows its ×: a closable tab in front
// or under the pointer, while no tab is dragged, wide enough for it.
func (b *Bar) crossShown(i int) bool {
	if i < 0 || i >= len(b.tabs) || i >= len(b.boxes) || !b.tabs[i].Closable || b.carried != "" ||
		b.boxes[i].Size().W < TabLeast.Default() {
		return false
	}
	return b.tabs[i].id == b.front || i == b.hot
}

// onCross reports whether p is on tab i's ×, where it shows.
func (b *Bar) onCross(i int, p geom.Point) bool {
	return b.crossShown(i) && crossIn(b.boxes[i], TabPadding.Default()).Inset(geom.Uniform(-4)).Contains(p)
}

// tabAt returns the tab at p; len(tabs) for the +; or -1.
func (b *Bar) tabAt(p geom.Point) int {
	for i, r := range b.boxes {
		if r.Contains(p) {
			return i
		}
	}
	if b.Plus && b.plusBox.Inset(geom.Uniform(-2)).Contains(p) {
		return len(b.tabs)
	}
	return -1
}

// CaptionRects implements [gunim.Caption]: with Caption, the room after
// the tabs and the + moves the window.
func (b *Bar) CaptionRects(size geom.Size) []geom.Rect {
	to := size.W - b.reserve
	if !b.Caption || b.end >= to {
		return nil
	}
	return []geom.Rect{geom.Rc(b.end, 0, to-b.end, size.H)}
}

// Cursor implements [gunim.CursorShaper].
func (b *Bar) Cursor(p geom.Point) input.Cursor {
	if b.tabAt(p) >= 0 {
		return input.CursorHand
	}
	return input.CursorArrow
}

// Focusable implements [gunim.Focusable]: the keys stay where they are.
// A screen reader reaches the tabs through Access, and the keys of the
// dock step through them.
func (b *Bar) Focusable() bool { return false }

// Handle implements [gunim.Handler]. The ends of what began on the bar,
// a press let go and a drag ended, are heard however the bar changed.
func (b *Bar) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove, input.PointerEnter, input.PointerDown:
		b.tip.Handle(e, u, b, b.tipOf(pointerAt(e)))
	case input.PointerLeave, input.Scroll:
		b.tip.Handle(e, u, b, "")
	}
	switch e := e.(type) {
	case input.PointerMove:
		if b.pressed >= 0 && b.pressed < len(b.tabs) && b.carried == "" && !b.pressedCross {
			if d := e.Pos.Sub(b.pressAt); math.Hypot(float64(d.X), float64(d.Y)) > slop {
				b.lift(u)
				return true
			}
		}
		hot := b.tabAt(e.Pos)
		if hot != b.hot {
			b.hot = hot
			u.Invalidate()
		}
		if cross := hot < len(b.tabs) && b.onCross(hot, e.Pos); cross != b.crossHot {
			b.crossHot = cross
			u.Invalidate()
		}
		return hot >= 0
	case input.PointerLeave:
		if b.hot >= 0 {
			b.hot, b.crossHot = -1, false
			u.Invalidate()
		}
		return false
	case input.PointerDown:
		i := b.tabAt(e.Pos)
		switch {
		case e.Button == input.ButtonMiddle && i >= 0 && i < len(b.tabs):
			if b.tabs[i].Closable {
				b.change(Close{ID: b.tabs[i].id}, u)
			}
			return true
		case e.Button == input.ButtonSecondary && i == len(b.tabs) && b.OnPlusMenu != nil:
			if in := b.OnPlusMenu(b.plusBox, u); in != nil {
				u.Send(b, in)
			}
			return true
		case e.Button != input.ButtonPrimary:
			if b.pressed >= 0 && b.carried == "" {
				// A second button ends the press.
				b.pressed = -1
				return true
			}
			return false
		case i == len(b.tabs):
			b.plusPressed = true
			return true
		case i >= 0:
			b.pressed, b.pressAt = i, e.Pos
			b.pressedCross = b.onCross(i, e.Pos)
			return true
		}
		return b.covers(e.Pos)
	case input.PointerUp:
		if b.plusPressed {
			b.plusPressed = false
			if b.tabAt(e.Pos) == len(b.tabs) {
				u.Cue(gunim.CuePress, b)
				b.change(New{Group: b.group, Win: b.Win}, u)
			}
			return true
		}
		i := b.pressed
		b.pressed = -1
		if i < 0 || i >= len(b.tabs) || b.carried != "" || b.tabAt(e.Pos) != i {
			return i >= 0
		}
		if b.pressedCross {
			if b.onCross(i, e.Pos) {
				u.Cue(gunim.CueClose, b)
				b.change(Close{ID: b.tabs[i].id}, u)
			}
			return true
		}
		if b.tabs[i].id != b.front {
			u.Cue(gunim.CueSelect, b)
		}
		b.pick(b.tabs[i].id, u)
		return true
	case input.DragEnd:
		b.dragEnded(e, u)
		return true
	case input.DragOver:
		d, ok := e.Data.(Drag)
		if !ok {
			if b.Takes == nil || !b.Takes(e.Data) {
				return false
			}
			b.landing = b.landingAt(e.Pos)
			b.holdOver(b.tabAt(e.Pos), Drag{}, u)
			u.AnswerDrag(widget.DropHint{Text: DockHere.Get(u.Theme()), Effect: widget.DropMove})
			u.Invalidate()
			return true
		}
		b.landing = b.landingAt(e.Pos)
		b.holdOver(b.tabAt(e.Pos), d, u)
		if b.lands(d, b.landing) {
			u.AnswerDrag(widget.DropHint{Text: DockHere.Get(u.Theme()), Effect: widget.DropMove})
		}
		u.Invalidate()
		return true
	case input.DragLeave:
		b.landing = -1
		b.holdOver(-1, Drag{}, u)
		u.Invalidate()
		return false
	case input.Drop:
		d, ok := e.Data.(Drag)
		if !ok {
			if b.Takes == nil || !b.Takes(e.Data) {
				return false
			}
			at := b.landingAt(e.Pos)
			b.landing = -1
			b.holdOver(-1, Drag{}, u)
			b.sprungFrom = ""
			u.Invalidate()
			if b.OnDrop != nil {
				if in := b.OnDrop(Dropped{Data: e.Data, Index: at}, u); in != nil {
					u.Send(b, in)
				}
			}
			return true
		}
		at := b.landingAt(e.Pos)
		b.landing = -1
		b.holdOver(-1, Drag{}, u)
		b.sprungFrom = ""
		u.Invalidate()
		if !b.lands(d, at) {
			// Where it was: in front, as a tab dropped is.
			b.pick(d.ID, u)
			return true
		}
		b.change(Move{ID: d.ID, To: Target{At: b.group, Win: b.Win, Index: at}}, u)
		return true
	}
	return false
}

// covers reports whether p is on the bar's tabs or its +.
func (b *Bar) covers(p geom.Point) bool { return b.tabAt(p) >= 0 }

// pointerAt is where a pointer event happened.
func pointerAt(e input.Event) geom.Point {
	switch e := e.(type) {
	case input.PointerMove:
		return e.Pos
	case input.PointerEnter:
		return e.Pos
	case input.PointerDown:
		return e.Pos
	}
	return geom.Point{}
}

// lands reports whether tab d dropped at landing moves: not where it
// stands already, and not into a group of its own.
func (b *Bar) lands(d Drag, landing int) bool {
	if d.ID == b.group {
		return false
	}
	if b.d == nil {
		return true
	}
	if n := b.d.l.Node(d.ID); n != nil && n.path(b.group, nil) != nil {
		// The bar is in the tab dragged.
		return false
	}
	i := slices.IndexFunc(b.tabs, func(t tab) bool { return t.id == d.ID })
	if i < 0 {
		return true
	}
	return landing != i && landing != i+1
}

// holdOver notes that a drag is over the tab at i, -1 for none: held
// there for springHold, the tab comes to the front.
func (b *Bar) holdOver(i int, dr Drag, u *gunim.UI) {
	id := ""
	if i >= 0 && i < len(b.tabs) {
		id = b.tabs[i].id
	}
	if id == b.spring {
		return
	}
	b.spring = id
	b.springRun++
	if id == "" || id == b.front {
		return
	}
	run := b.springRun
	u.After(springHold, func(u *gunim.UI) {
		if b.springRun != run || b.spring != id || id == b.front {
			return
		}
		if b.sprungFrom == "" || b.sprungFor != dr {
			b.sprungFrom, b.sprungFor = b.front, dr
		}
		b.pick(id, u)
	})
}

// landingAt is where a tab dragged to p would land: before the first
// tab whose middle is past p.
func (b *Bar) landingAt(p geom.Point) int {
	for i, r := range b.boxes {
		if p.X < r.Min.X+r.Size().W/2 {
			return i
		}
	}
	return len(b.boxes)
}

// lift picks the tab pressed up, to follow the pointer.
func (b *Bar) lift(u *gunim.UI) {
	i := b.pressed
	t := b.tabs[i]
	r := b.boxes[i]
	b.carried, b.sprungFrom = t.id, ""
	b.grab = b.pressAt.Sub(r.Min)
	b.tip.Hide(u)
	d := Drag{ID: t.id, Win: b.Win, Grab: b.grab, Size: b.detachSize()}
	b.dragging = d
	pic := &tabPicture{face: t.Face, title: b.titles[i], size: r.Size()}
	u.StartDrag(b, d, widget.NewDragGhost(pic, b.grab), b.grab)
	u.Invalidate()
}

// dragEnded hears how the drag of a tab ended: taken, which the next
// layout shows; let go outside every window, which asks for one; or
// neither, and the tab stays where it was, the tab in front before a
// held drag brought another forward back in front.
func (b *Bar) dragEnded(e input.DragEnd, u *gunim.UI) {
	id, dr := b.carried, b.dragging
	b.carried, b.pressed, b.landing, b.dragging = "", -1, -1, Drag{}
	b.holdOver(-1, Drag{}, u)
	if id == "" {
		return
	}
	// Every bar of the window this drag brought a tab forward on, this
	// one or another, puts back the tab it had in front.
	bars := []*Bar{b}
	if b.d != nil {
		bars = b.d.allBars()
	}
	for _, o := range bars {
		if o.sprungFrom == "" || o.sprungFor != dr {
			continue
		}
		from := o.sprungFrom
		o.sprungFrom, o.sprungFor = "", Drag{}
		if !e.Taken && !e.Out {
			o.pick(from, u)
		}
	}
	if e.Out || e.Taken {
		b.dropped = id
	}
	if e.Out && !e.Taken {
		b.change(Detach{ID: id, From: b.Win, At: e.At.Sub(b.grab), Size: b.detachSize()}, u)
	}
	u.Invalidate()
}

// Access implements [gunim.Accessible]: a tab list, a tab for each tab.
func (b *Bar) Access() access.Info {
	info := access.Info{Role: access.RoleTabList}
	for i, t := range b.tabs {
		name := t.Title
		if t.More != "" {
			name += ", " + t.More
		}
		part := access.Info{Role: access.RoleTab, Name: name, Actions: []string{access.ActionPress}}
		if t.id == b.front {
			part.State = access.StateSelected
			info.Active = i + 1
		}
		if i < len(b.boxes) {
			part.Bounds = b.boxes[i]
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a tab shows it.
func (b *Bar) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(b.tabs) {
		return false
	}
	b.pick(b.tabs[r.Part].id, u)
	return true
}

// tabPicture is a tab as a drag carries it.
type tabPicture struct {
	face  Face
	title text.Run
	size  geom.Size
}

// Layout implements [gunim.Node].
func (t *tabPicture) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(t.size)
}

// Paint implements [gunim.Node].
func (t *tabPicture) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	radius := TabRadius.Get(th)
	p.RRect(r, radius, paint.Solid(BarFill.Get(th)))
	p.RRect(r, radius, paint.Solid(TabFront.Get(th)))
	ink := TabInk.Get(th)
	pad, tabIcon := TabPadding.Get(th), TabIcon.Get(th)
	mid := box.H / 2
	x := pad
	if t.face.Icon != nil {
		widget.PaintIcon(p, th, t.face.Icon, geom.Rc(x, mid-tabIcon/2, tabIcon, tabIcon), ink)
		x += tabIcon + 6
	}
	t.title.PaintFaded(p, geom.Pt(x, mid-t.title.Height()/2), ink, box.W-x-pad, widget.TextFade.Get(th))
}

func onOff(on bool) float32 {
	if on {
		return 1
	}
	return 0
}
