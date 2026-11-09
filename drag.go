package gunim

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A drag carries a value from the node it started at to a node that
// takes it, in the same window or another window of the application.
//
// The source window owns the drag: it keeps the pointer while the
// button is down, wherever the pointer goes, so it follows the pointer
// across the screen, finds the window under it, and tells that window's
// UI goroutine. The picture under the pointer rides in a popup window
// that lets the pointer through. Everything crosses between windows as
// messages, since each window's nodes belong to its own goroutine.

// A FileExporter is drag data that can leave the application as files,
// to be dropped on a file manager or another program. Once a drag of
// one leaves every window of the application, ExportFiles writes the
// files, where they need writing, and returns their paths, and the
// platform's own drag and drop carries them on.
type FileExporter interface {
	ExportFiles() ([]string, error)
}

// ExportError is an error ExportFiles can return to say why the files
// cannot leave the application yet, or at all. While the drag is over
// no window, the picture it carries hears Hint as an
// [input.DragAnswer], such as a widget.DropHint saying so.
//
// With Wait set, the files are on their way, as files fetched from
// elsewhere are: the drag stays the application's, and ExportFiles is
// asked again every exportRetry, and with each move, for as long as the
// button stays down and the drag over no window. ExportFiles is called
// on the window's UI goroutine, so it must not wait for the files
// itself.
type ExportError struct {
	Hint any
	Wait bool
	Err  error
}

func (e *ExportError) Error() string {
	switch {
	case e.Err != nil:
		return e.Err.Error()
	case e.Wait:
		return "gunim: the files are not ready to leave yet"
	}
	return "gunim: the files cannot leave the application"
}

func (e *ExportError) Unwrap() error { return e.Err }

// exportRetry is how often a drag over no window asks again for files
// that are on their way.
const exportRetry = 100 * time.Millisecond

// windows is the application's open windows, which drags look through
// for the one under the pointer.
type windows struct {
	mu   sync.Mutex
	list []*Window
	// focused counts the times a window took the keyboard, and
	// focusedAt is the count when each window last took it, so that
	// where the system cannot say which window is in front, the one
	// focused last is taken to be.
	focused   uint64
	focusedAt map[*Window]uint64
	// stacker, when not nil, says how the system stacks the windows,
	// and coverer whether another program's window is in front of them.
	stacker driver.Stacker
	coverer driver.Coverer
}

func (ws *windows) add(w *Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.list = append(ws.list, w)
}

func (ws *windows) remove(w *Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.list = slices.DeleteFunc(ws.list, func(o *Window) bool { return o == w })
	delete(ws.focusedAt, w)
}

// focus records that w took the keyboard.
func (ws *windows) focus(w *Window) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.focusedAt == nil {
		ws.focusedAt = map[*Window]uint64{}
	}
	ws.focused++
	ws.focusedAt[w] = ws.focused
}

// dragDebug is set by GUNIM_DEBUG_DRAG=1, which logs to standard error
// each time a drag goes over another window, or off them all, where the
// pointer is on the screen and where each window is, and each time it
// starts or stops leaving the windows and what it hands out as it does.
var dragDebug = os.Getenv("GUNIM_DEBUG_DRAG") == "1"

// debugDrag logs a drag at the screen point at going over window over,
// nil for none. Each window's depth is its place in the system's stack,
// 0 at the front and -1 where the system cannot say, and focused is
// when it last took the keyboard, larger for later.
func (a *App) debugDrag(at geom.Point, over *Window) {
	if a == nil {
		return
	}
	a.windows.mu.Lock()
	list := slices.Clone(a.windows.list)
	focusedAt := maps.Clone(a.windows.focusedAt)
	a.windows.mu.Unlock()
	depths := a.windows.depths(list)
	var line strings.Builder
	fmt.Fprintf(&line, "gunim drag at %.1f,%.1f over %p;", at.X, at.Y, over)
	for i, w := range list {
		sc, ok := w.dw.(driver.Screener)
		if !ok {
			continue
		}
		o, size := sc.ToScreen(geom.Point{}), w.dw.Size()
		far := sc.ToScreen(size.Point())
		fmt.Fprintf(&line, " window %p at %.1f,%.1f to %.1f,%.1f (size %.1fx%.1f, holds %v, depth %d, focused %d);", w, o.X, o.Y, far.X, far.Y, size.W, size.H, holds(w, at), depths[i], focusedAt[w])
	}
	fmt.Fprintln(os.Stderr, line.String())
}

// depths returns each of list's depth in the system's stack of
// windows, 0 at the front, or -1 where the system cannot say.
func (ws *windows) depths(list []*Window) []int {
	if ws.stacker == nil {
		out := make([]int, len(list))
		for i := range out {
			out[i] = -1
		}
		return out
	}
	dws := make([]driver.Window, len(list))
	for i, w := range list {
		dws[i] = w.dw
	}
	return ws.stacker.Depths(dws)
}

// at returns the window holding p, a screen point, or nil. Where
// windows overlap at p, it returns the one in front. Where the system
// cannot say which that is, the window asking, first, wins, as a drag
// inside a window stays there, and then the window that last took the
// keyboard.
func (ws *windows) at(p geom.Point, first *Window) *Window {
	w, _ := ws.under(p, first)
	return w
}

// under returns the window at p, as at does, and reports whether p is
// over another program's window in front of the application's windows
// there, where it returns nil.
func (ws *windows) under(p geom.Point, first *Window) (*Window, bool) {
	w := ws.front(p, first)
	if w == nil || ws.coverer == nil {
		return w, false
	}
	ws.mu.Lock()
	list := slices.Clone(ws.list)
	ws.mu.Unlock()
	var in []driver.Window
	for _, o := range list {
		if holds(o, p) {
			in = append(in, o.dw)
		}
	}
	if covered, ok := ws.coverer.Covered(in, p); ok && covered {
		return nil, true
	}
	return w, false
}

// front returns the application's window holding p in front, as at
// describes, minding no other program's windows.
func (ws *windows) front(p geom.Point, first *Window) *Window {
	ws.mu.Lock()
	list := slices.Clone(ws.list)
	ws.mu.Unlock()
	var in []*Window
	for _, w := range list {
		if holds(w, p) {
			in = append(in, w)
		}
	}
	switch len(in) {
	case 0:
		return nil
	case 1:
		return in[0]
	}
	// Only now, with windows overlapping, is the system asked, as this
	// runs on every move of a drag.
	depths := ws.depths(in)
	var front *Window
	best := -1
	for i, w := range in {
		d := depths[i]
		if d >= 0 && (front == nil || d < best || d == best && w == first) {
			front, best = w, d
		}
	}
	if front != nil {
		return front
	}
	if slices.Contains(in, first) {
		return first
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	front = in[0]
	for _, w := range in[1:] {
		if ws.focusedAt[w] > ws.focusedAt[front] {
			front = w
		}
	}
	return front
}

// farFrom reports whether p, a screen point, is farther than reach
// from every window.
func (ws *windows) farFrom(p geom.Point, reach float32) bool {
	ws.mu.Lock()
	list := slices.Clone(ws.list)
	ws.mu.Unlock()
	for _, w := range list {
		sc, ok := w.dw.(driver.Screener)
		if !ok {
			continue
		}
		q := sc.FromScreen(p)
		s := w.dw.Size()
		dx := max(0, -q.X, q.X-s.W)
		dy := max(0, -q.Y, q.Y-s.H)
		if dx*dx+dy*dy <= reach*reach {
			return false
		}
	}
	return true
}

// holds reports whether w covers p, a screen point.
func holds(w *Window, p geom.Point) bool {
	sc, ok := w.dw.(driver.Screener)
	if !ok {
		return false
	}
	return (geom.Rect{Max: w.dw.Size().Point()}).Contains(sc.FromScreen(p))
}

// toScreen turns p, in w's space, into a screen point.
func toScreen(w *Window, p geom.Point) geom.Point {
	if sc, ok := w.dw.(driver.Screener); ok {
		return sc.ToScreen(p)
	}
	return p
}

// fromScreen turns a screen point into w's space.
func fromScreen(w *Window, p geom.Point) geom.Point {
	if sc, ok := w.dw.(driver.Screener); ok {
		return sc.FromScreen(p)
	}
	return p
}

// dragKind is what a drag message says.
type dragKind uint8

const (
	// dragOver: the drag is over the window, at a screen point.
	dragOver dragKind = iota
	// dragLeave: the drag has left the window.
	dragLeave
	// dragDrop: the drag was let go over the window.
	dragDrop
	// dragEnded: the drop was taken, or was not, back to the source.
	dragEnded
	// dragAnswer: what a drop over the window would do, back to the
	// source.
	dragAnswer
)

// dragMsg is one step of a drag, sent from one window to another. drop
// numbers a drop, and its end carries the number back, so the end
// reaches the drag it is for.
type dragMsg struct {
	kind  dragKind
	at    geom.Point
	data  any
	mods  input.Mods
	from  *Window
	taken bool
	drop  uint64
}

// pendingDrop is a drag let go whose end is still to be heard: the node
// it started from, and the picture it carried, until it closes.
type pendingDrop struct {
	from  *state
	ghost *Popup
}

// drag is the drag a window's pointer is carrying.
type drag struct {
	source *state
	data   any
	ghost  *Popup
	// picture is what the ghost shows.
	picture Node
	grab    geom.Point
	// over is the window the drag is over, or nil.
	over *Window
	// mods are the modifier keys held.
	mods input.Mods
	// retrying is set while a call to ask again for files on their way
	// waits.
	retrying bool
	// hinted is set while the picture shows why the files cannot leave.
	hinted bool
	// leavingLogged is whether GUNIM_DEBUG_DRAG last logged the drag as
	// leaving the windows.
	leavingLogged bool
}

// leaveReach is how far a drag of files must go from every window of
// the application before it leaves it. Crossing a gap between two of
// its windows, however slowly, stays a drag inside it. Once the
// platform's drag has it, the pointer may come back as close as it
// likes to drop.
const leaveReach = 64

// ghostWait is the longest the picture a drag carries waits, once let
// go, to hear whether the drop was taken, and dropWait the longest the
// node it came from waits: a window that closed with the drop still
// unread never answers, and the drop ends untaken.
const (
	ghostWait = 2 * time.Second
	dropWait  = 10 * time.Second
)

// StartDrag starts dragging data from n, which has the pointer pressed
// on it: call it from n's Handle, for a press or a move. ghost, when
// not nil, is shown under the pointer for the length of the drag, in a
// window of its own, held grab from its top left corner.
//
// The drag goes where the pointer does, over this window and the
// application's others. Nodes under it hear [input.DragOver], and the
// one that takes it hears [input.Drop] when the button is let go. n
// hears [input.DragEnd] at the end, saying whether a node took it.
// Escape gives the drag up.
//
// The ghost hears [input.DragMove] as the pointer moves it,
// [input.DragAnswer] when the node under the pointer says what a drop
// would do, and [input.DragEnd] before it leaves.
func (u *UI) StartDrag(n Node, data any, ghost Node, grab geom.Point) {
	s, ok := u.index[n]
	if !ok {
		panic("gunim: StartDrag from a node that is not in the tree")
	}
	d := &drag{source: s, data: data, grab: grab, picture: ghost}
	// A drag from a press on the window as it lay behind another leaves
	// it there.
	u.dragged = u.behind
	u.openGhost(d)
	u.drag = d
	u.dragTo(u.pointer)
}

// openGhost opens the popup that shows d's picture under the pointer.
func (u *UI) openGhost(d *drag) {
	if d.picture == nil {
		return
	}
	// The root opens the picture, so it stays while the source leaves
	at := u.local(u.root, u.pointer).Sub(d.grab)
	d.ghost = u.OpenPopup(u.root.node, d.picture, PopupOptions{
		Anchor:      geom.Rect{Min: at, Max: at},
		Passthrough: true,
		Over:        true,
	})
}

// dragBack carries on drag id, which went out to other programs, as the pointer comes back over a window of the
// application with the button still down.
func (u *UI) dragBack(id uint64, at geom.Point) {
	d, ok := u.outDrags[id]
	if !ok {
		u.endDrop(id, false)
		return
	}
	delete(u.outDrags, id)
	delete(u.drops, id)
	if u.drag != nil {
		return
	}
	d.over = nil
	u.pointer = at
	u.openGhost(d)
	u.drag = d
	u.dragTo(u.pointer)
}

// AnswerDrag says what a drop here would do, from a node's Handle for
// [input.DragOver]. The picture the drag carries hears it as
// [input.DragAnswer], in whichever window the drag started. answer
// must be comparable.
func (u *UI) AnswerDrag(answer any) { u.dragAnswer = answer }

// toGhost hands e to the picture p shows.
func (u *UI) toGhost(p *Popup, e input.Event) {
	if p == nil {
		return
	}
	for _, k := range p.s.root.kids {
		u.deliver(k, e)
	}
}

// dragTo carries the drag to p, in the window's space.
func (u *UI) dragTo(p geom.Point) {
	d := u.drag
	at := toScreen(u.w, p)
	var over *Window
	covered := false
	if u.w.app != nil {
		over, covered = u.w.app.windows.under(at, u.w)
	} else if holds(u.w, at) {
		over = u.w
	}
	if d.over != over {
		if d.over != nil {
			u.w.sendDrag(d.over, dragMsg{kind: dragLeave})
		}
		u.toGhost(d.ghost, input.DragAnswer{Time: u.Clock()})
	}
	if dragDebug && d.over != over {
		u.w.app.debugDrag(at, over)
	}
	d.over = over
	// Over another program's window, the drag leaves at once, however
	// near the application's windows behind it.
	leaving := over == nil && (covered || u.leaving(at))
	if dragDebug && leaving != d.leavingLogged {
		d.leavingLogged = leaving
		log.Printf("gunim drag at %.1f,%.1f leaving the windows: %v, over another program's window: %v", at.X, at.Y, leaving, covered)
	}
	if leaving {
		if u.dragOut(d) {
			return
		}
	} else if d.hinted {
		// Back near a window, the hint of files that cannot leave goes.
		d.hinted = false
		u.toGhost(d.ghost, input.DragAnswer{Time: u.Clock()})
	}
	if d.ghost != nil {
		g := u.local(u.root, p).Sub(d.grab)
		d.ghost.Move(geom.Rect{Min: g, Max: g})
		u.toGhost(d.ghost, input.DragMove{At: p, Time: u.Clock()})
	}
	if over != nil {
		u.w.sendDrag(over, dragMsg{kind: dragOver, at: at, data: d.data, mods: d.mods, from: u.w})
	}
	u.invalid = true
}

// modKeys are the modifier keys and what each holds.
var modKeys = map[input.Key]input.Mods{
	input.KeyLeftShift: input.ModShift, input.KeyRightShift: input.ModShift,
	input.KeyLeftControl: input.ModControl, input.KeyRightControl: input.ModControl,
	input.KeyLeftAlt: input.ModAlt, input.KeyRightAlt: input.ModAlt,
}

// dragKey takes a key pressed or let go while the pointer carries a
// drag: Escape gives the drag up, and a modifier key tells the node
// under the drag.
func (u *UI) dragKey(ev any) {
	var mods input.Mods
	var key input.Key
	down := false
	switch e := ev.(type) {
	case input.KeyPress:
		mods, key, down = e.Mods, e.Key, true
	case input.KeyRelease:
		mods, key = e.Mods, e.Key
	default:
		return
	}
	if down && key == input.KeyEscape {
		u.cancelDrag()
		return
	}
	// A modifier's own key may or may not count itself as held.
	if m, ok := modKeys[key]; ok {
		if down {
			mods |= m
		} else {
			mods &^= m
		}
	}
	if mods != u.drag.mods {
		u.drag.mods = mods
		u.dragTo(u.pointer)
	}
}

// cancelDrag gives up the drag the pointer carries: nothing takes it.
func (u *UI) cancelDrag() {
	d := u.drag
	u.drag = nil
	if d.over != nil {
		u.w.sendDrag(d.over, dragMsg{kind: dragLeave})
	}
	u.endDrop(u.holdDrop(d.source, d.ghost), false)
}

// leaving reports whether a drag at the screen point at, outside every
// window of the application, has gone far enough to leave it.
func (u *UI) leaving(at geom.Point) bool {
	if u.w.app == nil {
		return !holds(u.w, at)
	}
	return u.w.app.windows.farFrom(at, leaveReach)
}

// dragOut hands d to other programs, when it can go as files, and
// reports whether it did. The picture under the pointer goes at once:
// the platform's drag shows its own, and on Windows it holds the main
// thread, which moving a window needs.
func (u *UI) dragOut(d *drag) bool {
	fe, ok := d.data.(FileExporter)
	if !ok {
		return false
	}
	do, ok := u.w.dw.(driver.DragOuter)
	if !ok {
		return false
	}
	paths, err := fe.ExportFiles()
	if dragDebug {
		var ee *ExportError
		if errors.As(err, &ee) {
			log.Printf("gunim drag out waits: %v, hint %+v, wait %v", ee.Err, ee.Hint, ee.Wait)
		} else {
			log.Printf("gunim drag out: %d paths, %v", len(paths), err)
		}
	}
	if err != nil || len(paths) == 0 {
		u.exportWaits(d, err)
		return false
	}
	if d.ghost != nil {
		u.closePopupNow(d.ghost.s)
	}
	u.drag = nil
	id := u.holdDrop(d.source, nil)
	if err := do.DragOut(paths); err != nil {
		u.endDrop(id, false)
		return true
	}
	u.dragOuts = append(u.dragOuts, id)
	if u.outDrags == nil {
		u.outDrags = map[uint64]*drag{}
	}
	u.outDrags[id] = d
	return true
}

// exportWaits shows the hint of err, an ExportError, under the pointer,
// and where the files are on their way, asks for them again shortly,
// unless the button is let go, or the drag goes over a window, first.
func (u *UI) exportWaits(d *drag, err error) {
	var ee *ExportError
	if !errors.As(err, &ee) {
		return
	}
	if ee.Hint != nil {
		d.hinted = true
		u.toGhost(d.ghost, input.DragAnswer{Answer: ee.Hint, Time: u.Clock()})
	}
	if !ee.Wait || d.retrying {
		return
	}
	d.retrying = true
	u.After(exportRetry, func(u *UI) {
		d.retrying = false
		if u.drag == d && d.over == nil {
			u.dragTo(u.pointer)
		}
	})
}

// dragDrop lets the drag go at p, in the window's space. The picture
// under the pointer stays until the window under it says whether it
// took the drop.
func (u *UI) dragDrop(p geom.Point) {
	u.dragTo(p)
	d := u.drag
	if d == nil {
		// The drag went out to other programs on the way.
		return
	}
	u.drag = nil
	id := u.holdDrop(d.source, d.ghost)
	if d.over == nil {
		u.endDropOut(id, p)
		return
	}
	u.After(dropWait, func(u *UI) { u.endDrop(id, false) })
	if d.ghost != nil {
		// The picture waits so long for the answer, and then goes; the
		// node still hears the end when it comes.
		u.After(ghostWait, func(u *UI) {
			if p, ok := u.drops[id]; ok && p.ghost != nil {
				u.dropGhost(p.ghost, input.DragEnd{Time: u.Clock()})
				p.ghost = nil
				u.drops[id] = p
			}
		})
	}
	u.w.sendDrag(d.over, dragMsg{kind: dragDrop, at: toScreen(u.w, p), data: d.data, mods: d.mods, from: u.w, drop: id})
}

// holdDrop keeps a drag let go until its end is heard, and returns its
// number.
func (u *UI) holdDrop(from *state, ghost *Popup) uint64 {
	if u.drops == nil {
		u.drops = map[uint64]pendingDrop{}
	}
	u.dropSeq++
	u.drops[u.dropSeq] = pendingDrop{from: from, ghost: ghost}
	return u.dropSeq
}

// endDrop tells the node drop id started from how it ended, and the
// picture it carried, which then leaves. An end heard twice, or for a
// drop unknown here, does nothing.
func (u *UI) endDrop(id uint64, taken bool) {
	u.endDropAs(id, input.DragEnd{Taken: taken})
}

// endDropOut ends drop id untaken, let go at p, in the window's space,
// over no window of the application.
func (u *UI) endDropOut(id uint64, p geom.Point) {
	u.endDropAs(id, input.DragEnd{Out: true, At: p})
}

// endDropAs ends drop id as e says.
func (u *UI) endDropAs(id uint64, e input.DragEnd) {
	delete(u.outDrags, id)
	p, ok := u.drops[id]
	if !ok {
		return
	}
	delete(u.drops, id)
	e.Time = u.Clock()
	if p.from != nil && p.from.parent != nil {
		u.deliver(p.from, e)
	}
	u.dropGhost(p.ghost, e)
	u.invalid = true
}

// dropGhost tells a picture a drag let go of how the drag ended, and
// closes it.
func (u *UI) dropGhost(g *Popup, e input.DragEnd) {
	if g == nil || !g.Open() {
		return
	}
	u.toGhost(g, e)
	g.Close()
}

// sendDrag hands m to window to, at once when it is this window.
func (w *Window) sendDrag(to *Window, m dragMsg) {
	if to == w {
		w.ui.dragMsg(m)
		return
	}
	select {
	case to.dragIn <- m:
	default:
		switch m.kind {
		case dragDrop:
			w.ui.endDrop(m.drop, false)
		case dragEnded:
			// A drop's end must arrive, or its node never hears it:
			// it waits for the window to take it, or to close.
			go func() {
				select {
				case to.dragIn <- m:
				case <-to.done:
				}
			}()
		default:
			// A window too busy to take a drag step misses it; the
			// next move sends another.
		}
	}
}

// dragHover offers a drag of data at p, in the window's space, to the
// node under it, and tells the node it left, if another took it. What
// the node answers goes back to the window the drag came from.
func (u *UI) dragHover(p geom.Point, data any) {
	now := u.Clock()
	u.dragAnswer = nil
	took := u.dispatchAt(u.root, p, func(local geom.Point) input.Event {
		return input.DragOver{Pos: local, Data: data, Mods: u.dragOverMods, Time: now}
	})
	answer := u.dragAnswer
	u.dragAnswer = nil
	if took == nil {
		answer = nil
	}
	if took != u.dragAt {
		if u.dragAt != nil {
			u.deliver(u.dragAt, input.DragLeave{Time: now})
		}
		u.dragAt = took
	}
	if answer != u.dragAnswered && u.dragOverFrom != nil {
		u.dragAnswered = answer
		u.w.sendDrag(u.dragOverFrom, dragMsg{kind: dragAnswer, data: answer, from: u.w})
	}
}

// dragMsg handles one step of a drag over this window.
func (u *UI) dragMsg(m dragMsg) {
	u.invalid = true
	now := u.Clock()
	switch m.kind {
	case dragOver:
		p := fromScreen(u.w, m.at)
		u.dragOver, u.dragOverAt, u.dragOverData = true, p, m.data
		u.dragOverMods, u.dragOverFrom = m.mods, m.from
		u.dragHover(p, m.data)
	case dragLeave:
		u.dragOver, u.dragOverData, u.dragOverFrom, u.dragAnswered = false, nil, nil, nil
		if u.dragAt != nil {
			u.deliver(u.dragAt, input.DragLeave{Time: now})
			u.dragAt = nil
		}
	case dragDrop:
		u.dragOver, u.dragOverData, u.dragOverFrom, u.dragAnswered = false, nil, nil, nil
		p := fromScreen(u.w, m.at)
		took := u.dispatchAt(u.root, p, func(local geom.Point) input.Event {
			return input.Drop{Pos: local, Data: m.data, Mods: m.mods, Time: now}
		})
		if u.dragAt != nil && u.dragAt != took {
			u.deliver(u.dragAt, input.DragLeave{Time: now})
		}
		u.dragAt = nil
		reply := dragMsg{kind: dragEnded, taken: took != nil, drop: m.drop}
		if m.from != nil {
			u.w.sendDrag(m.from, reply)
		}
	case dragEnded:
		u.endDrop(m.drop, m.taken)
	case dragAnswer:
		if d := u.drag; d != nil && d.over == m.from {
			u.toGhost(d.ghost, input.DragAnswer{Answer: m.data, Time: now})
		}
	}
}
