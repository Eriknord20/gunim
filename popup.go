package gunim

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// A Popup is a window a node opens beside itself: the list of a
// drop-down, a menu, a tooltip. It is a real window in the display
// server, so it can reach past the edge of the window that opened it.
//
// Its content is an ordinary node in the opener's tree of nodes, run
// by the same UI goroutine. The content enters and leaves with its own
// transitions, and the window closes once the content has left.
//
// A popup never takes the keyboard. Keys keep going to the focused node
// in the window, so the node that opened a menu moves through it with
// the arrow keys.
type Popup struct {
	u *UI
	s *surface
}

// PopupOptions describes a popup to open.
type PopupOptions struct {
	// Anchor is the rectangle the popup attaches to, in the opener's
	// own space. The popup opens just below it, starting at its left
	// edge. Where the screen runs out below and there is more room
	// above, it opens above. It slides sideways to stay on the screen.
	Anchor geom.Rect
	// Max bounds the content's size. Zero allows up to 4096 each way.
	Max geom.Size
	// Passthrough lets the pointer through the popup to whatever is
	// under it, as for the picture a drag carries.
	Passthrough bool
	// Over puts the popup's top-left corner at the anchor's, exactly,
	// for a popup laid over the window, such as a glow reaching past
	// its edges. It stays there even where the screen runs out.
	Over bool
	// Above opens the popup above the anchor, or below it where the screen runs out above and there is more room
	// below, as for suggestions at a message box along the bottom of a window.
	Above bool
	// Beside opens the popup beside the anchor, as a submenu opens beside its menu: right of the anchor with the
	// content's top at the anchor's top, or left of it where the screen runs out on the right and there is more room
	// on the left. Where the screen runs out below, the popup moves up as far as keeps it on the screen. A
	// [PopupFitter] is told the room from the anchor's top down and up, and from its right edge right and its left
	// edge left.
	Beside bool
	// Owned keeps the popup just above its window, under any window in front of it, as for a glow round the window.
	// Without it a popup stays above every window, as a menu does. Only Windows tells the two apart.
	Owned bool
	// Dismiss runs when the pointer is pressed outside the popup and
	// its anchor, or when the window loses the keyboard. It usually
	// closes the popup. A press on the anchor is left to the opener, so
	// a menu's title or a drop-down's box can close what it opened
	// rather than see it closed and open it again. A popup with no
	// Dismiss, such as a tooltip, stays until its opener closes it.
	Dismiss func(u *UI)
}

// OpenPopup opens a popup holding content, attached to opener. The
// window opens with the next frame, at the size content lays out to,
// and follows the content's size and the anchor's place from then on.
//
// Closing or removing the opener closes the popup.
func (u *UI) OpenPopup(opener, content Node, o PopupOptions) *Popup {
	from, ok := u.index[opener]
	if !ok {
		panic("gunim: OpenPopup from a node that is not in the tree")
	}
	if o.Max == (geom.Size{}) {
		o.Max = geom.Sz(4096, 4096)
	}
	root := &state{node: &popupRoot{}, presence: Present, opener: from}
	u.index[root.node] = root
	s := &surface{root: root, opts: o, opened: time.Now()}
	u.popups = append(u.popups, s)
	u.Insert(root.node, content)
	return &Popup{u: u, s: s}
}

// Close starts the content's exit. The window closes once it has left.
func (p *Popup) Close() { p.u.closePopup(p.s) }

// Open reports whether the popup is open and staying so: false once it
// has started to close.
func (p *Popup) Open() bool { return !p.s.closing }

// Offscreen returns the driver window behind a popup of a window from [NewOffscreen], for a test to send it input,
// and nil before the popup's window opens or for a window on a display.
func (p *Popup) Offscreen() *driver.OffscreenWindow {
	d, _ := p.s.dw.(*driver.OffscreenWindow)
	return d
}

// Input hands the popup a platform event, as its window would, for a window driven with [Window.Frame]. It must be
// called from the goroutine calling Frame, and positions are in the popup's window's space.
func (p *Popup) Input(ev any) { p.u.popupEvent(popupEvent{s: p.s, ev: ev}) }

// Move attaches the popup to a new anchor, in the opener's space.
func (p *Popup) Move(anchor geom.Rect) {
	p.s.opts.Anchor = anchor
	p.u.invalid = true
}

// A PopupPadder is popup content that leaves room around what it
// shows, for a shadow. The popup lines up the part inside the padding
// with its anchor, so the menu itself sits against the drop-down that
// opened it and its shadow reaches past.
//
// The padding takes no pointer, unless the content is [Shaped], which
// then says where it does: the pointer goes to whatever lies under it,
// as the menu bar a menu's shadow reaches over.
type PopupPadder interface {
	Node
	PopupPadding() geom.Insets
}

// A PopupFitter is popup content that fits itself to the room the screen leaves: before each layout it is told how
// many logical pixels there are from its anchor to each edge of the screen. The window opens below the anchor while
// content no taller than the room below fits there, and slides sideways only for content wider than the room right
// of the anchor, so content that keeps to that room stays where it is attached.
type PopupFitter interface {
	Node
	FitPopup(room driver.Room)
}

// surface is a popup window and the tree of nodes it shows.
type surface struct {
	root *state
	opts PopupOptions
	// dw is nil until the first frame has laid the content out.
	dw driver.Window
	// zoom is the zoom dw was last given.
	zoom float32
	// size and anchor are where the window was last put, in logical
	// pixels, anchor in the parent window's space.
	size   geom.Size
	anchor geom.Rect
	// painter records the frames the window is given, one at a time:
	// the next only once the window has reported the last shown, as the
	// main window's painter does. By then the window draws nothing of
	// the frame before, whose buffers painter reuses. A frame recorded
	// while one is in flight, to lay the popup out and place its nodes
	// for input, goes to scratch, and the window is never given it.
	painter, scratch paint.Painter
	inFlight         bool
	// stale is set when a frame was recorded while one was in flight,
	// and so never shown.
	stale   bool
	closing bool
	// stop ends the goroutine passing the window's events on.
	stop chan struct{}
	// room is the room the screen leaves round roomAt, the anchor it was asked for, on the screen where the parent
	// window can say; see PopupFitter.
	room      driver.Room
	roomAt    geom.Rect
	roomKnown bool
	// opened is when the popup was opened, window how it got its window and in how long, and shown whether its first
	// frame has been shown, for GUNIM_DEBUG_POPUP.
	opened time.Time
	window string
	shown  bool
	// region is where the window was last told it takes the pointer, and regionSet says it was told at all; see
	// pointerRegion.
	region    []geom.Rect
	regionSet bool
}

// popupDebug is set by GUNIM_DEBUG_POPUP=1, which logs to standard error how each popup got its window and how long
// it took to show.
var popupDebug = os.Getenv("GUNIM_DEBUG_POPUP") == "1"

// popupEvent is one event from a popup's window, passed on to the UI
// goroutine: input, or a frame shown.
type popupEvent struct {
	s     *surface
	ev    any
	shown bool
}

// popupRoot is the node at the top of a popup's tree. It is as big as
// its content.
type popupRoot struct{ _ byte }

// Layout implements [Node]: the content, laid out loose, at the top left.
func (r *popupRoot) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	var size geom.Size
	for k := range kids.All {
		ks := k.Layout(Loose(c.Max))
		k.Place(geom.Point{})
		size = geom.Sz(max(size.W, ks.W), max(size.H, ks.H))
	}
	return size
}

// Paint implements [Node].
func (r *popupRoot) Paint(p *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// closePopup starts s's content leaving.
func (u *UI) closePopup(s *surface) {
	if s.closing {
		return
	}
	s.closing = true
	for _, k := range s.root.kids {
		u.Remove(k.node)
	}
	u.invalid = true
}

// closePopupNow closes s and its window at once, with no exit.
func (u *UI) closePopupNow(s *surface) {
	s.closing = true
	for _, k := range s.root.kids {
		u.forget(k)
	}
	s.root.kids = nil
	u.dropPopup(s)
	u.invalid = true
}

// closePopupsOf closes every popup opened from s or beneath it.
func (u *UI) closePopupsOf(s *state) {
	for _, p := range u.popups {
		if p.root.opener != nil && p.root.opener.below(s) {
			u.closePopup(p)
		}
	}
}

// dismissFor runs Dismiss on each popup a press at target, in the tree
// under root, lands outside of. A press inside a popup opened from
// within another counts as inside both. A nil target dismisses them
// all.
func (u *UI) dismissFor(target *state, onAnchor func(p *surface) bool) {
	for _, p := range slices.Clone(u.popups) {
		if p.closing || p.opts.Dismiss == nil {
			continue
		}
		if target != nil && (target.below(p.root) || onAnchor(p)) {
			continue
		}
		p.opts.Dismiss(u)
	}
}

// onAnchorOf reports whether a press at pos, in the window whose tree
// starts at root, lands on a popup's anchor.
func (u *UI) onAnchorOf(root *state, pos geom.Point) func(p *surface) bool {
	return func(p *surface) bool {
		o := p.root.opener
		if o == nil || o.leaving() || u.surfaceOf(o) != u.surfaceOf(root) {
			return false
		}
		return p.opts.Anchor.Contains(u.local(o, pos))
	}
}

// surfaceOf returns the popup holding s, or nil for the main window.
func (u *UI) surfaceOf(s *state) *surface {
	for s.parent != nil {
		s = s.parent
	}
	for _, p := range u.popups {
		if p.root == s {
			return p
		}
	}
	return nil
}

// windowOf returns the driver window showing s.
func (u *UI) windowOf(s *state) driver.Window {
	if p := u.surfaceOf(s); p != nil {
		return p.dw
	}
	return u.w.dw
}

// framePopups lays out, paints and presents every popup, after the
// main window has been painted, so each opener's place is known. A
// popup opened from within another comes after it, so its parent
// window is open by the time it opens.
func (u *UI) framePopups(f Frame) {
	for _, s := range slices.Clone(u.popups) {
		if s.closing && len(s.root.kids) == 0 {
			u.dropPopup(s)
			continue
		}
		u.framePopup(s, f)
	}
}

func (u *UI) framePopup(s *surface, f Frame) {
	opener := s.root.opener
	f.Theme = u.themeOf(opener)
	// A popup's window keeps clear of the system's bars itself.
	f.Safe = geom.Insets{}
	f.Keyboard = 0
	// Until the window opens, guess it blends as the last one did.
	f.Transparent = u.w.blends
	if s.dw != nil && s.zoom != u.zoom {
		setZoom(s.dw, u.zoom)
		// Place the window again at the new zoom
		s.zoom, s.size = u.zoom, geom.Size{}
	}
	if s.dw != nil {
		f.Scale = s.dw.Scale()
		f.Transparent = transparent(s.dw)
	}
	parent := u.windowOf(opener)
	u.fitPopup(s, parent)
	size := u.layoutPopup(s, f)
	anchor := u.popupAnchor(s)

	switch {
	case s.dw == nil && parent == nil:
		// The opener's own popup has yet to open.
		u.invalid = true
		return
	case s.dw == nil:
		start := time.Now()
		dw, err := u.reuse(parent, s.opts, anchor, size), error(nil)
		s.window = "a spare window"
		if dw == nil {
			dw, err = u.w.open(driver.Options{Kind: driver.KindPopup, Parent: parent, Anchor: anchor, Size: size, Passthrough: s.opts.Passthrough, Over: s.opts.Over, Above: s.opts.Above, Owned: s.opts.Owned})
			s.window = "a new window"
		}
		s.window += fmt.Sprintf(", got in %.1f ms", float64(time.Since(start).Microseconds())/1000)
		if err != nil {
			u.w.err = fmt.Errorf("gunim: open popup: %w", err)
			u.closePopup(s)
			return
		}
		if s.zoom = u.zoom; setZoom(dw, u.zoom) {
			if pl, ok := dw.(driver.Placer); ok {
				_ = pl.Place(anchor, size)
			}
		}
		s.dw, s.size, s.anchor = dw, size, anchor
		s.stop = make(chan struct{})
		go u.w.forward(s)
		if tr := transparent(dw); tr != f.Transparent {
			// The guess was wrong: lay out again before the first frame.
			u.w.blends, f.Transparent = tr, tr
			size, anchor = u.layoutPopup(s, f), u.popupAnchor(s)
			if pl, ok := dw.(driver.Placer); ok {
				_ = pl.Place(anchor, size)
			}
			s.size, s.anchor = size, anchor
		}
	case size != s.size || anchor != s.anchor:
		if pl, ok := s.dw.(driver.Placer); ok {
			_ = pl.Place(anchor, size)
		}
		s.size, s.anchor = size, anchor
	}

	pp := &s.painter
	if s.inFlight {
		pp = &s.scratch
	}
	pp.Reset()
	s.root.toWindow, s.root.drawn = paint.Identity, u.seq
	s.root.node.Paint(pp, f, s.root.size, Children{ns: s.root.kids, f: f, s: s.root})
	pp.PaintFloats()
	u.setPointerRegion(s)
	if s.inFlight {
		s.stale = true
		return
	}
	if err := s.dw.Present(pp.Ops(), paint.Everything); err != nil {
		u.w.err = fmt.Errorf("gunim: present popup: %w", err)
		u.closePopup(s)
		return
	}
	s.inFlight, s.stale = true, false
}

// fitPopup tells content that fits itself to the screen how much room there is round its anchor, asking the parent
// window again only when the anchor has moved. A popup opened beside its anchor keeps the room to place itself by,
// fitter or not.
func (u *UI) fitPopup(s *surface, parent driver.Window) {
	r, ok := parent.(driver.PopupRoomer)
	if !ok || s.closing {
		return
	}
	var fits []PopupFitter
	for _, k := range s.root.kids {
		if fit, ok := k.node.(PopupFitter); ok {
			fits = append(fits, fit)
		}
	}
	if len(fits) == 0 && !s.opts.Beside {
		return
	}
	a := u.popupAnchor(s)
	if s.opts.Beside {
		// The room round the anchor itself, which the popup is put beside
		a = u.openerRect(s)
	}
	at := a
	if sc, ok := parent.(driver.Screener); ok {
		// Where the anchor is on the screen, which changes as the window moves
		at = geom.Rect{Min: sc.ToScreen(a.Min), Max: sc.ToScreen(a.Max)}
	}
	if !s.roomKnown || at != s.roomAt {
		s.room = r.PopupRoom(a)
		if s.opts.Beside {
			s.room = besideRoom(a, s.room)
		}
		s.roomAt, s.roomKnown = at, true
	}
	for _, fit := range fits {
		fit.FitPopup(s.room)
	}
}

// besideRoom turns room, round anchor a as [driver.PopupRoomer] tells it, into the room for a popup beside a: from
// a's top down and up, from its right edge right, and from its left edge left.
func besideRoom(a geom.Rect, room driver.Room) driver.Room {
	room.Below += a.Size().H
	room.Right -= a.Size().W
	return room
}

// layoutPopup lays out a popup's content and returns the size its
// window takes.
func (u *UI) layoutPopup(s *surface, f Frame) geom.Size {
	s.root.size = s.root.node.Layout(Loose(s.opts.Max), f, Children{ns: s.root.kids, f: f, s: s.root})
	return geom.Sz(max(1, s.root.size.W), max(1, s.root.size.H))
}

// popupAnchor returns s's anchor in the space of the window its opener
// is in, moved by the content's padding. It is left as it is, top above
// bottom, even where the padding turns it inside out: the popup lines
// its top up with the bottom and its bottom with the top.
//
// A popup opened beside its anchor gets an anchor of no size, at where its window's top-left corner goes.
func (u *UI) popupAnchor(s *surface) geom.Rect {
	anchor := u.openerRect(s)
	if s.opts.Beside {
		room := driver.NoRoomLimit
		if s.roomKnown {
			room = s.room
		}
		var in geom.Insets
		for _, k := range s.root.kids {
			if pp, ok := k.node.(PopupPadder); ok {
				in = pp.PopupPadding()
				break
			}
		}
		at := besideAt(anchor, s.root.size, in, room)
		return geom.Rect{Min: at, Max: at}
	}
	for _, k := range s.root.kids {
		pp, ok := k.node.(PopupPadder)
		if !ok {
			continue
		}
		in := pp.PopupPadding()
		if s.opts.Over {
			// The content, inside its padding, sits on the anchor's corner
			anchor.Min = anchor.Min.Sub(geom.Pt(in.Left, in.Top))
			anchor.Max = anchor.Max.Sub(geom.Pt(in.Left, in.Top))
			break
		}
		anchor.Min.X -= in.Left
		anchor.Min.Y += in.Bottom
		anchor.Max.Y -= in.Top
		break
	}
	return anchor
}

// openerRect returns s's anchor in the space of the window its opener is in.
func (u *UI) openerRect(s *surface) geom.Rect {
	o := s.root.opener
	a := s.opts.Anchor
	return geom.Rect{Min: o.screenAt(a.Min), Max: o.screenAt(a.Max)}
}

// besideAt returns where the window of a popup of size, padded by in, goes beside anchor a, given the room round a
// as [besideRoom] tells it: its top-left corner. The content's card meets a's right edge, or its left edge where the
// card does not fit on the right and there is more room on the left. Its top is level with a's top, or as much higher
// as keeps the window a pixel above the screen's bottom, so rounding keeps it on the screen, though never above the
// screen's top.
func besideAt(a geom.Rect, size geom.Size, in geom.Insets, room driver.Room) geom.Point {
	x := a.Max.X - in.Left
	if right, left := size.W-in.Left, size.W-in.Right; right > room.Right && room.Left > room.Right {
		x = a.Min.X - left
	}
	y := a.Min.Y - in.Top
	if over := size.H - in.Top - (room.Below - 1); over > 0 {
		y = max(y-over, a.Min.Y-room.Above)
	}
	return geom.Pt(x, y)
}

// popupCard is the part of a popup's content k that takes the pointer by its padding alone: its box less its
// padding, which holds only its shadow. It reports false for content that is not a popup's, or is [Shaped] and says
// itself where it takes the pointer, or has no padding.
func popupCard(k *state) (geom.Rect, bool) {
	if k.parent == nil {
		return geom.Rect{}, false
	}
	if _, ok := k.parent.node.(*popupRoot); !ok {
		return geom.Rect{}, false
	}
	if _, ok := k.node.(Shaped); ok {
		return geom.Rect{}, false
	}
	pp, ok := k.node.(PopupPadder)
	if !ok {
		return geom.Rect{}, false
	}
	return geom.Rect{Max: k.size.Point()}.Inset(pp.PopupPadding()), true
}

// pointerRegion is where s's window takes the pointer, in its logical pixels, from the frame just painted: where its
// content draws something to point at, as [UI.hit] finds it. Content on its way out takes none, so a menu fading
// out over the bar leaves the bar the pointer. It is nil for a window that takes the pointer all over.
func (u *UI) pointerRegion(s *surface) []geom.Rect {
	box := geom.Rect{Max: s.root.size.Point()}
	rects := []geom.Rect{}
	whole := false
	for _, k := range s.root.kids {
		if k.presence == Exiting || k.drawn != u.seq {
			continue
		}
		var parts []geom.Rect
		switch n := k.node.(type) {
		case RegionShaped:
			if parts = n.CoverRects(); parts == nil {
				parts = []geom.Rect{{Max: k.size.Point()}}
			}
		case Shaped:
			whole = true
		default:
			if card, ok := popupCard(k); ok {
				parts = []geom.Rect{card}
			} else {
				parts = []geom.Rect{{Max: k.size.Point()}}
			}
		}
		for _, r := range parts {
			if r = clipRect(k.screenRect(r), box); !r.Empty() {
				rects = append(rects, r)
			}
		}
	}
	if whole {
		return nil
	}
	return rects
}

// setPointerRegion tells s's window where it takes the pointer, when that has changed. A popup the pointer passes
// through whole is left as it is.
func (u *UI) setPointerRegion(s *surface) {
	pr, ok := s.dw.(driver.PointerRegioner)
	if !ok || s.opts.Passthrough {
		return
	}
	r := u.pointerRegion(s)
	if s.regionSet && (r == nil) == (s.region == nil) && slices.Equal(r, s.region) {
		return
	}
	s.region, s.regionSet = r, true
	if r == nil {
		pointerf("popup %p takes the pointer all over", s)
	} else {
		pointerf("popup %p takes the pointer in %v", s, r)
	}
	pr.SetPointerRegion(r)
}

// clipRect is the part of r inside c, empty where they do not meet.
func clipRect(r, c geom.Rect) geom.Rect {
	return geom.Rect{
		Min: geom.Pt(max(r.Min.X, c.Min.X), max(r.Min.Y, c.Min.Y)),
		Max: geom.Pt(min(r.Max.X, c.Max.X), min(r.Max.Y, c.Max.Y)),
	}
}

// transparent reports whether dw blends with what is behind it.
func transparent(dw driver.Window) bool {
	t, ok := dw.(driver.Transparent)
	return ok && t.Transparent()
}

// dropPopup closes a popup's window once its content has left.
func (u *UI) dropPopup(s *surface) {
	u.popups = slices.DeleteFunc(u.popups, func(p *surface) bool { return p == s })
	delete(u.index, s.root.node)
	if s.dw == nil {
		return
	}
	close(s.stop)
	// Kept, hidden, for the next popup, unless a frame is still on its
	// way to it, whose report would reach that popup instead. Each kind
	// of window keeps its own few.
	sp := spareWindow{dw: s.dw, parent: u.windowOf(s.root.opener), passthrough: s.opts.Passthrough, over: s.opts.Over,
		above: s.opts.Above, owned: s.opts.Owned}
	alike := 0
	for _, o := range u.spare {
		if o.fits(sp.parent, s.opts) {
			alike++
		}
	}
	if r, ok := s.dw.(driver.Recycler); ok && !s.inFlight && alike < mostSpare && hideSpare(s.dw, r) {
		u.spare = append(u.spare, sp)
		return
	}
	_ = s.dw.Close()
	// A spare kept for a popup of this one's has lost its parent.
	u.spare = slices.DeleteFunc(u.spare, func(sp spareWindow) bool {
		if sp.parent == s.dw {
			_ = sp.dw.Close()
			return true
		}
		return false
	})
}

// mostSpare is how many hidden popup windows are kept to open again,
// and warmSpare how many are made ahead of time. Running along a menu
// bar opens a menu while the ones before are still fading out in their
// windows, so a few are wanted at once.
const (
	mostSpare = 4
	warmSpare = 4
)

// warmSize is the size a window made ahead of time is made at: a
// menu's, near enough, since growing a window's surface to fit the
// popup is the larger part of opening one in it.
var warmSize = geom.Sz(320, 360)

// spareWindow is a popup's window, hidden, kept for the next popup
// with the same parent and the same Passthrough, Over, Above and Owned.
type spareWindow struct {
	dw          driver.Window
	parent      driver.Window
	passthrough bool
	over        bool
	above       bool
	owned       bool
}

// fits reports whether sp serves a popup with parent and options o.
func (sp spareWindow) fits(parent driver.Window, o PopupOptions) bool {
	return sp.parent == parent && sp.passthrough == o.Passthrough && sp.over == o.Over && sp.above == o.Above &&
		sp.owned == o.Owned
}

// reuse puts a spare window at anchor, at size, and shows it. It
// returns nil when none fits, or the one that did would not show, and
// a new window is to be opened.
func (u *UI) reuse(parent driver.Window, o PopupOptions, anchor geom.Rect, size geom.Size) driver.Window {
	for i, sp := range u.spare {
		if !sp.fits(parent, o) {
			continue
		}
		u.spare = slices.Delete(u.spare, i, i+1)
		// What came in while it was hidden was for the popup before.
		drain(sp.dw)
		r, ok := sp.dw.(driver.Recycler)
		if pl, placer := sp.dw.(driver.Placer); !ok || (placer && pl.Place(anchor, size) != nil) || r.Show() != nil {
			_ = sp.dw.Close()
			return nil
		}
		return sp.dw
	}
	return nil
}

// makeSpare makes a popup's window ahead of time, hidden, once the
// window is on screen, so the first menu or palette opens as fast as
// the ones after it: making a window and its surface is the larger
// part of opening a popup, and on Windows a slow one.
//
// One is made a frame, so the window's first frames are not held up.
func (u *UI) makeSpare() {
	if u.spared >= warmSpare {
		return
	}
	u.spared++
	dw, err := u.w.open(driver.Options{Kind: driver.KindPopup, Parent: u.w.dw, Size: warmSize, Hidden: true})
	if err != nil {
		return
	}
	u.w.blends = transparent(dw)
	if _, ok := dw.(driver.Recycler); !ok || len(u.spare) >= mostSpare {
		_ = dw.Close()
		return
	}
	noPointer(dw)
	u.spare = append(u.spare, spareWindow{dw: dw, parent: u.w.dw})
}

// hideSpare hides a popup's window to keep for the next popup, and reports whether it did. The window takes the
// pointer nowhere until the next popup's first frame says where, so as it shows again under the pointer it does not
// take it from the window under it on the way.
func hideSpare(dw driver.Window, r driver.Recycler) bool {
	noPointer(dw)
	return r.Hide() == nil
}

// noPointer has a spare popup window take the pointer nowhere; see hideSpare.
func noPointer(dw driver.Window) {
	if pr, ok := dw.(driver.PointerRegioner); ok {
		pr.SetPointerRegion([]geom.Rect{})
	}
}

// drain empties a window's input and frame reports without waiting.
func drain(dw driver.Window) {
	for {
		select {
		case _, ok := <-dw.Input():
			if !ok {
				return
			}
		case _, ok := <-dw.Presented():
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// closeAllPopups closes every popup window at once, as the window
// closes, and the spare ones.
func (u *UI) closeAllPopups() {
	for _, s := range u.popups {
		if s.dw != nil {
			close(s.stop)
			_ = s.dw.Close()
		}
	}
	u.popups = nil
	for _, sp := range u.spare {
		_ = sp.dw.Close()
	}
	u.spare = nil
}

// popupEvent handles one event from a popup's window.
func (u *UI) popupEvent(e popupEvent) {
	if !slices.Contains(u.popups, e.s) {
		return
	}
	if e.shown {
		if popupDebug && !e.s.shown {
			fmt.Fprintf(os.Stderr, "gunim popup: shown %.1f ms after it opened, in %s\n",
				float64(time.Since(e.s.opened).Microseconds())/1000, e.s.window)
		}
		e.s.shown = true
		e.s.inFlight = false
		if e.s.stale {
			u.invalid = true
		}
		return
	}
	u.handleOn(e.s.root, e.ev)
}

// forward passes a popup window's events to the UI goroutine until the
// popup closes.
func (w *Window) forward(s *surface) {
	send := func(e popupEvent) bool {
		select {
		case w.popupIn <- e:
			return true
		case <-s.stop:
			return false
		}
	}
	for {
		select {
		case <-s.stop:
			return
		case ev, ok := <-s.dw.Input():
			if !ok || !send(popupEvent{s: s, ev: ev}) {
				return
			}
		case _, ok := <-s.dw.Presented():
			if !ok || !send(popupEvent{s: s, shown: true}) {
				return
			}
		}
	}
}
