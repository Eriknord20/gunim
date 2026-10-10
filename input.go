package gunim

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// handlePlatform turns one raw platform event from the window into
// tree traffic.
func (u *UI) handlePlatform(ev any) { u.handleOn(u.root, ev) }

// handleOn turns one raw platform event into tree traffic, for the
// window whose tree starts at root: the main window's, or a popup's.
//
// Pointer events are routed by position, keyboard events by focus. The
// engine works out hover changes itself, on every widget's behalf. A
// press in a popup leaves focus where it is, so the node that opened a
// menu keeps the keyboard while the menu is clicked.
func (u *UI) handleOn(root *state, ev any) {
	if u.touchEvent(root, ev) {
		return
	}
	u.handleRaw(root, ev)
}

// handleRaw is handleOn past the reading of a finger's moves in
// touch.go, which sends what it makes of them here.
func (u *UI) handleRaw(root *state, ev any) {
	u.invalid = true
	u.heldMods(ev)
	if u.goingAway {
		// A window on its way out takes nothing more.
		return
	}
	if u.fadeOut {
		// Nor does one fading out, or out, save word of the keyboard.
		switch ev.(type) {
		case driver.WindowFocus, driver.WindowShown, driver.WindowCovered:
		default:
			return
		}
	}
	if root == u.root {
		switch e := ev.(type) {
		case input.PointerMove, input.PointerDown, input.Scroll, input.KeyPress:
			u.overhear(e.(input.Event))
		}
	}
	switch e := ev.(type) {
	case access.Request:
		u.accessRequest(e)
	case driver.DragOutEnded:
		if len(u.dragOuts) > 0 {
			id := u.dragOuts[0]
			u.dragOuts = u.dragOuts[1:]
			if e.Back {
				u.dragBack(id, e.At)
			} else {
				u.endDrop(id, e.Taken)
			}
		}
	case driver.WindowMaximized:
		if u.chrome != nil {
			u.chrome.maximized = e.Maximized
		}
	case driver.MoveStarted:
		u.altAlone = false
		// The press that started it went to the system, and would have closed these
		if root == u.root {
			u.dismissFor(nil, nil)
		}
		return
	case input.Heading:
		u.headingEvent(e)
		return
	case input.MediaSeek:
		target := u.focus
		if target == nil {
			target = u.appRoot()
		}
		u.bubble(target, e)
		return
	case driver.WindowCovered:
		if root != u.root {
			return
		}
		// A window out of sight draws nothing, and the application
		// carries on unaware: music plays on under other windows.
		u.w.covered = e.Covered
		if !e.Covered {
			u.w.resumed, u.invalid = true, true
		}
		return
	case driver.WindowShown:
		if root != u.root {
			return
		}
		// A window no one sees draws nothing; shown again, it goes on
		// from where its animations held.
		u.w.hidden = !e.Shown
		if e.Shown {
			u.w.resumed, u.invalid = true, true
		} else {
			// Out of sight, the compass rests; the first frame shown runs
			// it again.
			u.watchHeading()
		}
		var ev input.Event = input.WindowShown{Time: time.Now()}
		if !e.Shown {
			ev = input.WindowHidden{Time: time.Now()}
		}
		target := u.focus
		if target == nil {
			target = u.appRoot()
		}
		u.bubble(target, ev)
		// The whole window went out of sight, not one view: a view under
		// a dialog or an intro that holds the keyboard hears it too, so a
		// game's music stops with it.
		u.tellViews(target, ev)
		return
	case driver.FilesOver:
		if root != u.root {
			return
		}
		// Files from another program are offered to the nodes under
		// them as a drag inside the application is.
		u.invalid = true
		u.dragOverMods = e.Mods
		u.dragHover(e.Pos, input.Files{Paths: e.Paths})
		return
	case driver.FilesLeft:
		if root == u.root && u.dragAt != nil {
			u.deliver(u.dragAt, input.DragLeave{Time: time.Now()})
			u.dragAt = nil
		}
		return
	case driver.WindowFocus:
		if root != u.root {
			return
		}
		u.altAlone = false
		u.keyboardAway = !e.Focused
		if e.Focused {
			// A press from behind has nothing left to bring to the front.
			u.behind = false
		}
		if !e.Focused {
			u.dismissFor(nil, nil)
		} else if u.w.app != nil {
			u.w.app.windows.focus(u.w)
		}
		var ev input.Event = input.WindowFocusGained{Time: time.Now()}
		if !e.Focused {
			ev = input.WindowFocusLost{Time: time.Now()}
		}
		target := u.focus
		if target == nil {
			target = u.appRoot()
		}
		if !u.bubble(target, ev) {
			u.catchKey(ev)
		}
	case input.PointerMove:
		if root == u.root {
			// A finger has no hover: it moves the pointer for a drag, and
			// lights nothing as it passes, nor as what is under it
			// scrolls.
			u.pointer, u.pointerIn = e.Pos, !e.Touch
			if u.drag != nil {
				u.drag.mods = e.Mods
				u.dragTo(e.Pos)
				return
			}
		}
		if !e.Touch {
			u.updateHover(root, e.Pos, e.Time)
		}
		mk := func(local geom.Point) input.Event {
			return input.PointerMove{Pos: local, Mods: e.Mods, Touch: e.Touch, Time: e.Time}
		}
		if u.capture != nil {
			if !u.capture.within(root) {
				// The press was in another window, which holds the pointer
				// until the release and hears its moves itself. A move from
				// this one was made up, as when a popup opens under the
				// pointer, and its point is in this window's space, not the
				// capture's.
				pointerf("move to %.1f,%.1f in another window than the press, left alone", e.Pos.X, e.Pos.Y)
				return
			}
			u.deliver(u.capture, mk(u.localHeld(u.capture, e.Pos)))
			u.shapePointer(root, e.Pos)
			return
		}
		u.dispatchAt(root, e.Pos, mk)
		u.shapePointer(root, e.Pos)
		if root == u.root {
			u.movedAt(e.Mods)
		}
	case input.PointerDown:
		u.altAlone = false
		u.showRings(false)
		// A side button presses nothing: it closes what a press closes, and goes back or forward as it is let go
		if sideButton(e.Button) {
			u.dismissFor(u.hit(root, e.Pos), u.onAnchorOf(root, e.Pos))
			return
		}
		if root == u.root {
			u.pointer = e.Pos
			u.behind, u.dragged = e.Behind, false
		}
		// A press outside a popup dismisses it; the press still goes
		// where it lands.
		u.dismissFor(u.hit(root, e.Pos), u.onAnchorOf(root, e.Pos))
		// On a chromeless window's edge or caption, the press moves or
		// sizes the window, where the system leaves that to the engine.
		if root == u.root && u.framePress(e.Pos, e) {
			return
		}
		// A press moves focus before it is delivered, so a text field
		// that is clicked is already focused when it sees the press. A
		// finger's primary press leaves it until the finger lifts from a
		// tap, as a press that goes on to scroll moves no focus; see
		// touch.go.
		was := u.focus
		if root == u.root && (!e.Touch || e.Button != input.ButtonPrimary) {
			u.focusAt(e.Pos, e.Touch)
		}
		focusing := u.focus != was
		held := u.capture
		// Whoever takes the press keeps the pointer until the release.
		u.capture = u.dispatchAt(root, e.Pos, func(local geom.Point) input.Event {
			return input.PointerDown{Pos: local, Button: e.Button, Mods: e.Mods, Clicks: e.Clicks, Focusing: focusing, Behind: e.Behind, Touch: e.Touch, Time: e.Time}
		})
		if pointerDebug {
			pointerf("press %d at %.1f,%.1f in the %s: under it %s, taken by %s; held before %s, %d popups, drag %v, focus %s",
				e.Button, e.Pos.X, e.Pos.Y, u.rootName(root), u.chainAt(root, e.Pos), nodeName(u.capture), nodeName(held),
				len(u.popups), u.drag != nil, nodeName(u.focus))
		}
		u.shapePointer(root, e.Pos)
	case input.PointerUp:
		if sideButton(e.Button) {
			from := u.hit(root, e.Pos)
			if from == nil {
				from = u.appRoot()
			}
			u.bubble(from, input.HistoryStep{Forward: e.Button == input.ButtonForward, Time: e.Time})
			return
		}
		if root == u.root && u.behind {
			// A press from behind that started no drag was a click, which
			// brings the window to the front, as it would have on the
			// press; a drag leaves it where it is.
			u.behind = false
			if !u.dragged {
				u.ToFront()
			}
		}
		if root == u.root && u.drag != nil {
			u.drag.mods = e.Mods
			u.capture = nil
			u.dragDrop(e.Pos)
			u.updateHover(root, e.Pos, e.Time)
			return
		}
		mk := func(local geom.Point) input.Event {
			return input.PointerUp{Pos: local, Button: e.Button, Mods: e.Mods, Touch: e.Touch, Time: e.Time}
		}
		if root == u.root {
			u.askKeyboard(u.hit(root, e.Pos))
		}
		if pointerDebug {
			pointerf("release %d at %.1f,%.1f in the %s, to %s", e.Button, e.Pos.X, e.Pos.Y, u.rootName(root), nodeName(u.capture))
		}
		if c := u.capture; c != nil {
			u.capture = nil
			u.deliver(c, mk(u.localHeld(c, e.Pos)))
			u.updateHover(root, e.Pos, e.Time)
			u.shapePointer(root, e.Pos)
			return
		}
		u.dispatchAt(root, e.Pos, mk)
	case input.Scroll:
		u.altAlone = false
		if !u.wheelZoomerAt(root, e.Pos) && u.zoomKey(e) {
			return
		}
		u.dispatchAt(root, e.Pos, func(local geom.Point) input.Event {
			return input.Scroll{Pos: local, Delta: e.Delta, Notches: e.Notches, Mods: e.Mods, Touch: e.Touch, Time: e.Time}
		})
	case input.Pinch:
		u.pinchEvent(root, e)
	case input.PointerLeave:
		if root == u.root {
			u.pointerIn = false
		}
		u.updateHover(root, geom.Pt(-1, -1), e.Time)
	case input.Drop:
		data := e.Data
		if data == nil && e.Paths != nil {
			data = input.Files{Paths: e.Paths}
		}
		took := u.dispatchAt(root, e.Pos, func(local geom.Point) input.Event {
			return input.Drop{Pos: local, Data: data, Paths: e.Paths, Mods: e.Mods, Time: e.Time}
		})
		// The node the files hovered over hears they left, if another
		// took them.
		if u.dragAt != nil && u.dragAt != took {
			u.deliver(u.dragAt, input.DragLeave{Time: e.Time})
		}
		u.dragAt = nil
	case input.KeyPress, input.KeyRelease:
		if k, ok := ev.(input.KeyPress); ok && k.Key == input.KeyTab {
			u.showRings(true)
		}
		tapped := u.altTap(ev)
		if u.drag != nil {
			// Keys speak to the drag while it lasts.
			u.dragKey(ev)
			return
		}
		if u.behind {
			// A window pressed from behind has no keyboard; what comes is
			// Escape, as the system says it is held, for a drag alone.
			return
		}
		u.keyEvent(ev)
		if tapped {
			u.keyEvent(input.AltTapped{Time: time.Now()})
		}
	default:
		u.keyEvent(ev)
	}
}

// altTap follows Alt going down and up, and reports whether ev lets it go with nothing pressed while it was down.
// Alt pressed with another modifier held, as AltGr is, does not count.
func (u *UI) altTap(ev any) bool {
	switch e := ev.(type) {
	case input.KeyPress:
		if isAlt(e.Key) {
			if !e.Repeat {
				u.altAlone = e.Mods&^input.ModAlt == 0
			}
			return false
		}
		u.altAlone = false
	case input.KeyRelease:
		if isAlt(e.Key) && u.altAlone {
			u.altAlone = false
			return true
		}
	}
	return false
}

// heldMods follows the modifier keys held from the input ev: what a press, a move or a key says, and a modifier's
// own key going down or up.
func (u *UI) heldMods(ev any) {
	switch e := ev.(type) {
	case input.PointerDown:
		u.mods = e.Mods
	case input.PointerUp:
		u.mods = e.Mods
	case input.PointerMove:
		u.mods = e.Mods
	case input.Scroll:
		u.mods = e.Mods
	case input.KeyPress:
		u.mods = e.Mods | modKeys[e.Key]
	case input.KeyRelease:
		u.mods = e.Mods &^ modKeys[e.Key]
	case input.WindowFocusLost:
		u.mods = 0
		u.keyboardAway = true
	case input.WindowFocusGained:
		u.keyboardAway = false
	}
}

// sideButton reports whether b is a mouse's back or forward button.
func sideButton(b input.Button) bool { return b == input.ButtonBack || b == input.ButtonForward }

// isAlt reports whether k is either Alt key.
func isAlt(k input.Key) bool { return k == input.KeyLeftAlt || k == input.KeyRightAlt }

// keyEvent delivers a keyboard or focus event. It goes to the focused
// node and bubbles from there, which is how a shortcut a text field
// ignores ends up at the window. With nothing focused it goes to the
// root, so a window-wide shortcut works before anything has been
// clicked. What nothing takes goes to the [KeyCatcher]s.
func (u *UI) keyEvent(ev any) {
	if u.zoomKey(ev) {
		return
	}
	if ev, ok := ev.(input.Event); ok {
		target := u.focus
		if target == nil {
			target = u.appRoot()
		}
		if te, ok := ev.(input.TextEdit); ok && te.Seq != 0 {
			u.textSeq = te.Seq
		}
		// Focus a key press moves is keyed
		if _, ok := ev.(input.KeyPress); ok {
			u.keyed = true
			defer func() { u.keyed = false }()
		}
		if m := u.stopsAtModal(ev); m != nil {
			// A modal holds the keys: what it and its contents leave
			// goes no further, to the window's shortcuts or the
			// catchers behind it. Tab still walks it.
			if !u.bubbleTo(target, ev, m) && !u.catchKeyIn(ev, m) {
				u.tab(ev)
			}
			return
		}
		if !u.bubble(target, ev) && !u.catchKey(ev) {
			u.tab(ev)
		}
	}
}

// overhear tells the [Overhearer]s the last frame drew of ev, in paint order.
func (u *UI) overhear(ev input.Event) {
	var walk func(s *state)
	walk = func(s *state) {
		if s.presence == Exiting || (s != u.root && s.drawn != u.seq) {
			return
		}
		if o, ok := s.node.(Overhearer); ok {
			u.on(s, func() { o.Overhear(ev, u) })
		}
		for _, k := range s.kids {
			walk(k)
		}
	}
	walk(u.root)
}

// catchKey offers ev to the [KeyCatcher]s the last frame drew, in paint order, and reports whether one took it.
func (u *UI) catchKey(ev input.Event) bool { return u.catchKeyIn(ev, nil) }

// catchKeyIn is catchKey keeping to the catchers inside m, popups
// opened from it included; a nil m offers ev to them all.
func (u *UI) catchKeyIn(ev input.Event, m *state) bool {
	var took bool
	var walk func(s *state)
	walk = func(s *state) {
		if took || s.presence == Exiting || (s != u.root && s.drawn != u.seq) {
			return
		}
		if c, ok := s.node.(KeyCatcher); ok && (m == nil || inside(s, m)) {
			u.on(s, func() { took = c.CatchKey(ev, u) })
		}
		for _, k := range s.kids {
			walk(k)
		}
	}
	walk(u.root)
	return took
}

// focusAt moves focus for a press at p: to the nearest [Focusable] at
// or above the node hit. A press inside the focused node, or on a
// [FocusKeeper], leaves focus where it is, which keeps a focused dialog
// focused when its panel is clicked, and so does a press on a
// [PressFocuser] that declines it. A press anywhere else drops it.
//
// A finger's press, with touch set, moves focus only to a node that
// takes text, and otherwise leaves it where it is, as a phone does: a
// tap on a button, a row or a message keeps the keyboard in the text
// being written.
func (u *UI) focusAt(p geom.Point, touch bool) {
	target := u.hit(u.root, p)
	for s := target; s != nil; s = s.parent {
		if f, ok := s.node.(Focusable); ok && f.Focusable() {
			if touch && !takingText(s) {
				return
			}
			if pf, ok := s.node.(PressFocuser); !ok || pf.FocusOnPress() {
				u.clicking = true
				u.Focus(s.node)
				u.clicking = false
			}
			return
		}
		if _, ok := s.node.(FocusKeeper); ok {
			return
		}
	}
	if touch || target != nil && u.focus != nil && target.within(u.focus) {
		return
	}
	u.Focus(nil)
}

// tab moves focus when Tab went unhandled: forward, or back with Shift.
func (u *UI) tab(ev input.Event) {
	k, ok := ev.(input.KeyPress)
	if !ok || k.Key != input.KeyTab || k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) {
		return
	}
	u.FocusNext(!k.Mods.Has(input.ModShift))
}

// FocusNext moves keyboard focus to the next [Focusable] node the last
// frame drew, in the order they were painted, or to the previous one
// when forward is false. It wraps around at either end, and scrolls the
// node into view through every [Revealer] around it.
func (u *UI) FocusNext(forward bool) {
	var order []*state
	var walk func(s *state)
	walk = func(s *state) {
		if s.presence == Exiting || (s != u.root && s.drawn != u.seq) {
			return
		}
		// A scope with a modal of its own offers the modal in place of what is under it.
		if _, ok := s.node.(ModalScope); ok {
			if m := u.scopedModal(s); m != nil && m != s {
				walk(m)
				return
			}
		}
		if _, ok := s.node.(TabGroup); ok && s != u.root {
			if stop := u.tabStop(s); stop != nil {
				order = append(order, stop)
			}
			return
		}
		if f, ok := s.node.(Focusable); ok && f.Focusable() {
			if _, skip := s.node.(TabSkipper); !skip {
				order = append(order, s)
			}
		}
		for _, k := range s.kids {
			walk(k)
		}
	}
	// Tab keeps to a modal while there is one.
	if m := u.modal(u.focus); m != nil {
		walk(m)
	} else {
		walk(u.root)
	}
	if len(order) == 0 {
		return
	}
	at := -1
	for i, s := range order {
		if s == u.focus {
			at = i
		}
	}
	var next int
	switch {
	case at < 0 && forward:
		next = 0
	case at < 0:
		next = len(order) - 1
	case forward:
		next = (at + 1) % len(order)
	default:
		next = (at - 1 + len(order)) % len(order)
	}
	u.Focus(order[next].node)
	u.revealState(order[next])
}

// tabStop is where Tab stops in group g: the node in it with the focus, or the one that last had it, or the first.
func (u *UI) tabStop(g *state) *state {
	stops := u.focusables(g)
	for _, want := range []*state{u.focus, g.tabStop} {
		for _, s := range stops {
			if s == want {
				return s
			}
		}
	}
	if len(stops) == 0 {
		return nil
	}
	return stops[0]
}

// focusables are the nodes under s, s left out, that Tab could visit, in paint order.
func (u *UI) focusables(s *state) []*state {
	var out []*state
	var walk func(s *state)
	walk = func(s *state) {
		if s.presence == Exiting || s.drawn != u.seq {
			return
		}
		if f, ok := s.node.(Focusable); ok && f.Focusable() {
			if _, skip := s.node.(TabSkipper); !skip {
				out = append(out, s)
			}
		}
		for _, k := range s.kids {
			walk(k)
		}
	}
	for _, k := range s.kids {
		walk(k)
	}
	return out
}

// FocusWithin moves the focus to the next node group holds that Tab could visit, or the previous one when forward is
// false, as a [TabGroup]'s arrow keys do. It stops at either end, and reports whether the focus moved.
func (u *UI) FocusWithin(group Node, forward bool) bool {
	g, ok := u.index[group]
	if !ok {
		return false
	}
	stops := u.focusables(g)
	at := slices.Index(stops, u.focus)
	next := at + 1
	if !forward {
		next = at - 1
	}
	if at < 0 || next < 0 || next >= len(stops) {
		return false
	}
	u.focusStep = map[bool]int{true: 1, false: -1}[forward]
	u.Focus(stops[next].node)
	u.focusStep = 0
	u.revealState(stops[next])
	return true
}

// FocusFirst moves the focus to n, or to the first node inside it that Tab could visit, or takes it away when there is none,
// and reports whether a node took it.
func (u *UI) FocusFirst(n Node) bool {
	g, ok := u.index[n]
	if !ok {
		return false
	}
	if f, ok := n.(Focusable); ok && f.Focusable() {
		u.Focus(n)
		u.revealState(g)
		return true
	}
	if stops := u.focusables(g); len(stops) > 0 {
		u.Focus(stops[0].node)
		u.revealState(stops[0])
		return true
	}
	u.Focus(nil)
	return false
}

// FocusFirstLaidOut puts the keyboard on n, or on the first node inside it that Tab could visit, once a frame has laid n
// out: for a page about to show, which no frame has drawn yet, and so [UI.FocusFirst] cannot find. The node takes the
// keyboard before that frame paints, so the page shows with it. Where the keyboard moves before then, or nothing inside
// n takes it, it stays where it is.
func (u *UI) FocusFirstLaidOut(n Node) {
	s, ok := u.index[n]
	if !ok {
		u.stray("FocusFirstLaidOut", n)
		return
	}
	u.focusLaid = &laidFocus{in: s, from: u.focus}
	u.invalid = true
}

// laidFocus is a [UI.FocusFirstLaidOut] waiting for a layout: in is the node to focus inside, and from where the
// keyboard was when it was asked for.
type laidFocus struct{ in, from *state }

// focusLaidOut ends a [UI.FocusFirstLaidOut] once the node it names has been laid out.
func (u *UI) focusLaidOut() {
	lf := u.focusLaid
	if lf == nil {
		return
	}
	if u.focus != lf.from || lf.in.leaving() || u.index[lf.in.node] != lf.in {
		u.focusLaid = nil
		return
	}
	if lf.in.laid != u.seq {
		// Not laid out yet, as in a popup still to open.
		return
	}
	u.focusLaid = nil
	if f, ok := lf.in.node.(Focusable); ok && f.Focusable() {
		u.Focus(lf.in.node)
		return
	}
	var walk func(s *state) *state
	walk = func(s *state) *state {
		if s.presence == Exiting || s.laid != u.seq {
			return nil
		}
		if f, ok := s.node.(Focusable); ok && f.Focusable() {
			if _, skip := s.node.(TabSkipper); !skip {
				return s
			}
		}
		for _, k := range s.kids {
			if found := walk(k); found != nil {
				return found
			}
		}
		return nil
	}
	for _, k := range lf.in.kids {
		if first := walk(k); first != nil {
			u.Focus(first.node)
			return
		}
	}
}

// Reveal scrolls n into view through every [Revealer] around it, as
// focusing it does, and leaves the keyboard where it is: for a list
// that follows what the user works in, such as a row for the pane in
// front.
func (u *UI) Reveal(n Node) {
	if s, ok := u.index[n]; ok {
		u.revealState(s)
	}
}

// revealState asks each [Revealer] above s to bring s into view.
func (u *UI) revealState(s *state) {
	for a := s.parent; a != nil; a = a.parent {
		if r, ok := a.node.(Revealer); ok {
			r.Reveal(u.rectIn(s, a), u)
		}
	}
}

// rectIn returns s's box, as the last frame drew it, in a's space.
func (u *UI) rectIn(s, a *state) geom.Rect {
	inv, ok := a.toWindow.Invert()
	if !ok {
		return geom.Rect{}
	}
	out := geom.Rect{Min: geom.Pt(float32(math.Inf(1)), float32(math.Inf(1))), Max: geom.Pt(float32(math.Inf(-1)), float32(math.Inf(-1)))}
	for _, c := range []geom.Point{{}, {X: s.size.W}, {Y: s.size.H}, s.size.Point()} {
		flat, ok := a.proj.Unapply(s.screenAt(c))
		if !ok {
			continue
		}
		p := inv.Apply(flat)
		out.Min = geom.Pt(min(out.Min.X, p.X), min(out.Min.Y, p.Y))
		out.Max = geom.Pt(max(out.Max.X, p.X), max(out.Max.Y, p.Y))
	}
	return out
}

// updateHover sends [input.PointerLeave] and [input.PointerEnter] when
// the node under the pointer changes. The pointer is over a node and
// every node around it, so a card lights up while the pointer is over
// a button inside it. Each node the pointer leaves hears so, innermost
// first, and each node it enters hears so, outermost first. While a
// node holds the pointer,
// only it and what is inside it can be hovered, so a drag lights up
// nothing else, and the node learns when the pointer leaves it and
// comes back.
//
// root is the tree of the window the pointer is in. The pointer leaving
// one window clears the hover only when it was in that window, so it
// keeps what it found in the popup it moved into.
func (u *UI) updateHover(root *state, p geom.Point, t time.Time) {
	next := u.hit(root, p)
	if u.capture != nil && (next == nil || !next.within(u.capture)) {
		next = nil
	}
	if next == nil && u.hover != nil && !u.hover.within(root) {
		return
	}
	if next == u.hover {
		return
	}
	prev := u.hover
	u.hover = next
	for s := prev; s != nil && s.parent != nil && (next == nil || !next.within(s)); s = s.parent {
		u.deliver(s, input.PointerLeave{Time: t})
	}
	var entered []*state
	for s := next; s != nil && s.parent != nil && (prev == nil || !prev.within(s)); s = s.parent {
		entered = append(entered, s)
	}
	for _, s := range slices.Backward(entered) {
		u.deliver(s, input.PointerEnter{Pos: u.local(s, p), Time: t})
	}
	// A claimer the pointer crosses between its claimed part and its
	// children stays hovered, and hears where the pointer went by a
	// move, even when a child takes the moves.
	for _, s := range []*state{prev, next} {
		if prev == nil || next == nil || !prev.within(s) || !next.within(s) {
			continue
		}
		if _, ok := s.node.(PointerClaimer); ok {
			u.deliver(s, input.PointerMove{Pos: u.local(s, p), Time: t})
		}
	}
}

// dispatchAt finds the topmost node under p and offers it the event
// built by mk, with the position translated into that node's space. A
// node that declines it passes the event on to its ancestors. It
// returns the node that took the event, or nil.
func (u *UI) dispatchAt(root *state, p geom.Point, mk func(local geom.Point) input.Event) *state {
	for s := u.hit(root, p); s != nil; s = s.parent {
		if h, ok := s.node.(Handler); ok {
			var took bool
			u.on(s, func() { took = h.Handle(mk(u.local(s, p)), u) })
			if took {
				return s
			}
		}
	}
	return nil
}

// bubble offers e to s and then to each of its ancestors, and reports
// whether one took it.
func (u *UI) bubble(s *state, e input.Event) bool { return u.bubbleTo(s, e, nil) }

// bubbleTo is bubble stopping at top, which is the last node offered e;
// a nil top goes all the way up.
func (u *UI) bubbleTo(s *state, e input.Event, top *state) bool {
	for ; s != nil; s = s.parent {
		if top != nil && s == top.parent {
			return false
		}
		if h, ok := s.node.(Handler); ok {
			var took bool
			u.on(s, func() { took = h.Handle(e, u) })
			if took {
				return true
			}
		}
	}
	return false
}

// tellViews hands e to each view mounted at the root that bubbling it
// up from start did not pass, so every view in the window hears it
// once. A view on its way out hears nothing.
func (u *UI) tellViews(start *state, e input.Event) {
	passed := map[*state]bool{}
	for s := start; s != nil; s = s.parent {
		passed[s] = true
	}
	for _, k := range slices.Clone(u.appRoot().kids) {
		if !passed[k] && k.presence != Exiting {
			u.deliver(k, e)
		}
	}
}

// deliver hands e to exactly one node and stops there. Enter, leave and
// focus changes concern a specific node, so they skip the bubbling that
// region events get.
func (u *UI) deliver(s *state, e input.Event) {
	if h, ok := s.node.(Handler); ok {
		u.on(s, func() { h.Handle(e, u) })
	}
}

// hit finds the topmost node under p, a point in window space.
//
// Each node is tested where the last frame drew it, through the
// transform it was painted under, so a scaled dialog or a panel sliding
// in takes clicks where it appears. The part of a node a clipping layer
// hides takes none, and neither does a node that frame left unpainted.
//
// Children are tested last-first because the last child painted is the
// one on top. A [PointerClaimer] keeps the points it claims from its
// children. Exiting nodes are skipped, so a click aimed at what lies
// behind a fading dialog reaches it.
func (u *UI) hit(s *state, p geom.Point) *state {
	for _, k := range slices.Backward(s.kids) {
		if k.presence == Exiting || k.drawn != u.seq {
			continue
		}
		local := u.local(k, p)
		if !(geom.Rect{Max: k.size.Point()}).Contains(local) || !k.clip.Contains(p) {
			continue
		}
		if sh, ok := k.node.(Shaped); ok && !sh.Covers(local) {
			continue
		}
		if card, ok := popupCard(k); ok && !card.Contains(local) {
			// A popup's shadow, which its window does not take the pointer on
			continue
		}
		if c, ok := k.node.(PointerClaimer); ok && c.ClaimsPointer(local) {
			return k
		}
		if deep := u.hit(k, p); deep != nil {
			return deep
		}
		return k
	}
	if s.parent == nil {
		return nil
	}
	return s
}

// local converts a point in window space into s's own space, through
// the transform s was last painted under and any tilt round it. A node
// drawn with no area, scaled to zero, or tilted to show its back where
// it is one-sided, maps every point far outside itself.
func (u *UI) local(s *state, p geom.Point) geom.Point { return toLocal(s, p, s.proj.Unapply) }

// localHeld is local for a node that holds the pointer, as through a
// drag. Past the horizon of a layer tilted round it, the point stays on
// the layer, far out the way the pointer went, so what follows the
// pointer runs to its end rather than jumping off to no place.
func (u *UI) localHeld(s *state, p geom.Point) geom.Point {
	return toLocal(s, p, s.proj.UnapplyNear)
}

// toLocal is p in s's own space, taken back through the tilts round s
// by unapply.
func toLocal(s *state, p geom.Point, unapply func(geom.Point) (geom.Point, bool)) geom.Point {
	inv, ok := s.toWindow.Invert()
	flat, shown := unapply(p)
	if !ok || !shown {
		inf := float32(math.Inf(1))
		return geom.Pt(inf, inf)
	}
	return inv.Apply(flat)
}

// hoverAgain finds what the pointer rests over in the frame just drawn.
// What is under a still pointer moves as the window zooms, a list
// scrolls or tiles spring to new places, and hover and the pointer's
// shape follow it, as they would a move.
//
// A node that tracks the part under the pointer itself, such as a grid
// of tiles, hears of it by a move: when the point under the pointer is
// another in the node's space than the last move gave it, the move is
// given again, at the point it now is.
func (u *UI) hoverAgain(now time.Time) {
	if !u.pointerIn || u.drag != nil {
		return
	}
	u.updateHover(u.root, u.pointer, now)
	u.shapePointer(u.root, u.pointer)
	if u.capture != nil || u.hover == nil {
		return
	}
	if u.hover == u.movedOn && u.local(u.hover, u.pointer) == u.movedTo {
		return
	}
	mods := u.movedMods
	pointerf("move to %.1f,%.1f again, as what is under the pointer moved", u.pointer.X, u.pointer.Y)
	u.dispatchAt(u.root, u.pointer, func(local geom.Point) input.Event {
		return input.PointerMove{Pos: local, Mods: mods, Time: now}
	})
	u.movedAt(mods)
}

// pointerDebug is set by GUNIM_DEBUG_POINTER=1, which logs to standard error the moves the engine makes up or
// leaves alone, beside the ones the driver logs, to find where the pointer goes astray.
var pointerDebug = os.Getenv("GUNIM_DEBUG_POINTER") == "1"

// pointerf logs one line about the pointer, under GUNIM_DEBUG_POINTER=1.
// nodeName names the node of s for a log, by its type: "none" for no
// node.
func nodeName(s *state) string {
	if s == nil || s.node == nil {
		return "none"
	}
	return fmt.Sprintf("%T", s.node)
}

// chainAt names the nodes under p, from the topmost up, for a log.
func (u *UI) chainAt(root *state, p geom.Point) string {
	var names []string
	for s := u.hit(root, p); s != nil && len(names) < 8; s = s.parent {
		names = append(names, nodeName(s))
	}
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, " < ")
}

// rootName says which of the window's trees root is, for a log.
func (u *UI) rootName(root *state) string {
	if root == u.root {
		return "window"
	}
	return "popup"
}

func pointerf(format string, args ...any) {
	if pointerDebug {
		fmt.Fprintf(os.Stderr, "gunim pointer engine %s: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	}
}

// movedAt notes where the pointer's last move landed, for hoverAgain.
func (u *UI) movedAt(mods input.Mods) {
	u.movedOn, u.movedMods = u.hover, mods
	if u.hover != nil {
		u.movedTo = u.local(u.hover, u.pointer)
	}
}
