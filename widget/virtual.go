package widget

import (
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// VirtualList shows a long list of items and scrolls through it,
// building only the rows in view and a little beyond. A list of a
// hundred thousand items costs about what the few dozen on screen do.
//
// Rows that scroll into view appear at once, and rows that scroll out
// are dropped. Changes to the items animate as a [List]'s do: a new
// item in view grows into place, a removed one collapses while its
// neighbours close the gap, and moved ones spring to their new places.
//
// A row's height is known once it has been laid out. Until then the
// list assumes Estimate. When a row above the view turns out taller or
// shorter than assumed, the list moves its offset by the difference,
// so what is on screen stays still.
//
// The list scrolls itself, as a [Scroll] does: the wheel and the keys
// glide it with a spring, and a thin bar shows while it moves.
type VirtualList struct {
	scrolling

	// Estimate is the height assumed for a row not yet laid out. Zero
	// means 40.
	Estimate float32
	// still says the rows built at the next layout come at once, for a
	// view put back as it was.
	still bool
	// Spacing is the room between rows. It defaults to the theme's
	// [ListSpacing]; a table's rows sit against each other.
	Spacing theme.Token[float32]
	// StickToEnd starts the view at the end of the list and keeps it there while rows arrive and grow, as a chat's
	// timeline does. A view scrolled away from the end stays where it is.
	StickToEnd bool
	// HoldOnPrepend keeps what is in view still when items arrive before the first one, even with the view at the
	// top of the list, as older messages loaded above a timeline should. Such items come at once, without growing in.
	HoldOnPrepend bool
	// OnReachStart, when set, runs from the list's layout, with the window's UI, as the view comes within a screen
	// of the start of the list; a non-nil result is sent as the list's intent. It is for loading the items before
	// the first. It runs once, and again after items arrive before the first or the view has gone well away from
	// the start.
	OnReachStart func(u *gunim.UI) gunim.Intent
	// reached says OnReachStart was sent and waits for older items.
	reached bool
	// openAt is the row the view opens at, in place of the end; see OpenAt.
	openAt Key
	// stuck says the view is held at the end, and laidOut that the list has been laid out once. again asks for
	// another frame, to build the rows a move to the end brought into view.
	stuck, laidOut, again bool

	build func(Key) gunim.Node

	// order is the keys in display order, including rows leaving;
	// gone marks those leaving.
	order []Key
	gone  map[Key]bool
	// heights holds each row's height from the last time it was laid
	// out, spacing left out.
	heights map[Key]float32
	// live holds the rows in the tree.
	live map[Key]*row
	// animate marks the keys that arrived through SetKeys, whose rows
	// grow in when built.
	animate map[Key]bool

	// entry holds each row's height and the spacing after it, in
	// order, and tops sums them, so finding the row at an offset and
	// the offset of a row are quick however long the list. dirty says
	// the order changed and both need building again.
	entry []float32
	tops  fenwick
	dirty bool
	gap   float32
}

// anchor returns the first row at or below the top of the view, and
// where it sits in the content, when the list is scrolled from its top.
func (l *VirtualList) anchor() (Key, float32, bool) {
	offset := l.offset.Value()
	if offset <= 0 && !l.HoldOnPrepend {
		return "", 0, false
	}
	var best Key
	at, found := float32(0), false
	for k, r := range l.live {
		if l.gone[k] {
			continue
		}
		if y := r.y.Target(); y >= offset && (!found || y < at) {
			best, at, found = k, y, true
		}
	}
	return best, at, found
}

// entryOf is the room a row of height h takes: its height and the
// spacing after it. The spacing closes with a row closing, r, so the
// row takes no room once it is shut.
func (l *VirtualList) entryOf(h float32, r *row) float32 {
	return h + closing(l.gap, h, r)
}

// closing returns the spacing after a row of height h: all of it,
// shrinking with a row that is opening or closing, r, in proportion to
// how open it is. r may be nil.
func closing(spacing, h float32, r *row) float32 {
	if h <= 0 {
		return 0
	}
	if r != nil && r.inner > 0 && h < r.inner {
		return spacing * h / r.inner
	}
	return spacing
}

// index builds entry and tops from the order and the known heights.
func (l *VirtualList) index() {
	l.entry = l.entry[:0]
	for _, k := range l.order {
		l.entry = append(l.entry, l.entryFor(k, l.gap))
	}
	l.tops.reset(l.entry)
	l.dirty = false
}

// entryFor is the room row k takes with spacing after it, from its
// known height, or as much as is left of it while it leaves.
func (l *VirtualList) entryFor(k Key, spacing float32) float32 {
	h := l.height(k)
	r, ok := l.live[k]
	if ok && l.gone[k] {
		h = r.height.Value()
	}
	return h + closing(spacing, h, r)
}

// fenwick sums a list of numbers by prefix, and finds where a running
// sum passes a value, each in time logarithmic in the list's length.
type fenwick struct{ t []float64 }

func (f *fenwick) reset(vals []float32) {
	f.t = f.t[:0]
	for _, v := range vals {
		f.t = append(f.t, float64(v))
	}
	for i := range f.t {
		if j := i | (i + 1); j < len(f.t) {
			f.t[j] += f.t[i]
		}
	}
}

// add adds d to value i.
func (f *fenwick) add(i int, d float64) {
	for ; i < len(f.t); i |= i + 1 {
		f.t[i] += d
	}
}

// sum returns the sum of the first n values.
func (f *fenwick) sum(n int) float64 {
	s := 0.0
	for i := n - 1; i >= 0; i = (i & (i + 1)) - 1 {
		s += f.t[i]
	}
	return s
}

// find returns the first i whose running sum through i passes y: the
// row that holds offset y. Past the end it returns the length.
func (f *fenwick) find(y float64) int {
	pos := -1
	step := 1
	for step*2 <= len(f.t) {
		step *= 2
	}
	for ; step > 0; step /= 2 {
		if next := pos + step; next < len(f.t) && f.t[next] <= y {
			pos = next
			y -= f.t[next]
		}
	}
	return pos + 1
}

// overscan is how far past the view, above and below, the list keeps
// rows built, so a short scroll finds them ready.
const overscan = 300

// NewVirtualList returns an empty list that builds a row's node with
// build when the row comes into view.
func NewVirtualList(build func(Key) gunim.Node) *VirtualList {
	l := &VirtualList{
		scrolling: newScrolling(),
		Spacing:   ListSpacing,
		build:     build,
		gone:      map[Key]bool{},
		heights:   map[Key]float32{},
		live:      map[Key]*row{},
		animate:   map[Key]bool{},
	}
	return l
}

// Len returns how many items the list holds, leaving rows left out.
func (l *VirtualList) Len() int { return len(l.order) - len(l.gone) }

// Built returns how many rows are built right now.
func (l *VirtualList) Built() int { return len(l.live) }

// Row returns the node built for key's row, while the row is built.
func (l *VirtualList) Row(key Key) (gunim.Node, bool) {
	r, ok := l.live[key]
	if !ok || l.gone[key] {
		return nil, false
	}
	return r.child, true
}

// SetKeys makes keys the list's items, in order. Call it from a view's
// update function. Rows already built stay built and spring to their
// new places; a new item in view grows in, and a removed one in view
// collapses. Changes out of view take effect at once.
func (l *VirtualList) SetKeys(keys []Key, u *gunim.UI) {
	staying := make(map[Key]bool, len(keys))
	next := make([]Key, 0, len(keys))
	for _, k := range keys {
		if staying[k] {
			continue // a duplicate key: the first one wins
		}
		staying[k] = true
		next = append(next, k)
	}
	first := len(l.order) == 0
	leaving := map[Key]bool{}
	for _, k := range l.order {
		if staying[k] {
			if r, ok := l.live[k]; ok && l.gone[k] {
				// Back while collapsing: it opens again.
				u.Insert(l, r)
				delete(l.gone, k)
			}
			continue
		}
		if r, ok := l.live[k]; ok {
			leaving[k] = true
			l.gone[k] = true
			// A removed item collapses, even in a row that appeared at
			// once.
			r.instant = false
			u.Remove(r)
			continue
		}
		delete(l.heights, k)
	}
	known := make(map[Key]bool, len(l.order))
	for _, k := range l.order {
		known[k] = true
	}
	// Items before what was the first re-arm OnReachStart, and with HoldOnPrepend come at once.
	head := 0
	for head < len(next) && !known[next[head]] {
		head++
	}
	if head == len(next) {
		head = 0
	}
	if head > 0 {
		l.reached = false
	}
	if !l.HoldOnPrepend {
		head = 0
	}
	for i, k := range next {
		if !known[k] && !first && i >= head {
			l.animate[k] = true
		}
	}
	l.order = mergeOrder(l.order, next, leaving)
	l.dirty = true
	u.Invalidate()
}

// OpenAt opens the view with key's row at its top, rather than at the start or, for a list that sticks to its end,
// at the end: for a timeline that opens at the first message not yet read. Call it before the list is first laid
// out. The view sticks to the end once the user brings it there.
func (l *VirtualList) OpenAt(key Key) { l.openAt = key }

// AtEnd reports whether the view is at the end of the list, or heading there.
func (l *VirtualList) AtEnd() bool { return l.target >= l.end()-0.5 }

// ScrollToEnd glides the view to the end of the list, where a list that sticks to its end stays.
func (l *VirtualList) ScrollToEnd(motion anim.Motion) {
	l.ScrollTo(l.end(), motion)
	l.stuck = l.StickToEnd
}

// stickTo holds a stuck view at the end of content rows tall, in a view viewport tall. The first time it jumps
// there, and after that it moves by as much as the end did, so a glide toward the end carries on.
func (l *VirtualList) stickTo(content, viewport float32) float32 {
	end := max(0, content-viewport)
	switch {
	case !l.laidOut:
		l.jumpToY(end)
	case end != l.target:
		l.shift(end - l.target)
	}
	return l.offset.Value()
}

// ScrollToKey glides the list so key's row is at the top of the view,
// or as near as the list's end allows. Rows leaving above it count for
// the room they still take.
func (l *VirtualList) ScrollToKey(key Key, u *gunim.UI) {
	i := slices.Index(l.order, key)
	if i < 0 || l.gone[key] {
		return
	}
	var y float32
	if !l.dirty && i < len(l.entry) {
		y = float32(l.tops.sum(i))
	} else {
		// The keys changed since the last layout: sum the room afresh.
		spacing := l.Spacing.Get(u.Theme())
		for _, k := range l.order[:i] {
			y += l.entryFor(k, spacing)
		}
	}
	l.ScrollTo(y, Quick.Get(u.Theme()))
}

// drawnAt returns the row drawn at y in the view, leaving rows aside, with its top in the view and its height. A
// row springing to a new place is found where it is drawn now, and where two cross, the lower one.
func (l *VirtualList) drawnAt(y float32) (key Key, top, h float32, ok bool) {
	off := l.offset.Value()
	for k, r := range l.live {
		if l.gone[k] {
			continue
		}
		if at, rh := r.y.Value()-off, r.height.Value(); y >= at && y < at+rh && (!ok || at > top) {
			key, top, h, ok = k, at, rh, true
		}
	}
	return key, top, h, ok
}

// drawn returns where key's row is drawn, its top in the view and its height. A row not built is where the list
// would put it, and false says the list cannot tell yet, as before the layout after a change of keys.
func (l *VirtualList) drawn(key Key) (top, h float32, ok bool) {
	off := l.offset.Value()
	if r, ok := l.live[key]; ok {
		return r.y.Value() - off, r.height.Value(), true
	}
	i := slices.Index(l.order, key)
	if i < 0 || l.dirty || i >= len(l.entry) {
		return 0, 0, false
	}
	return float32(l.tops.sum(i)) - off, l.height(key), true
}

func (l *VirtualList) height(k Key) float32 {
	if h, ok := l.heights[k]; ok {
		return h
	}
	if l.Estimate > 0 {
		return l.Estimate
	}
	return 40
}

// Step implements [gunim.Animator].
func (l *VirtualList) Step(dt time.Duration) bool {
	moving := l.scrolling.Step(dt)
	again := l.again
	l.again = false
	return moving || again
}

// Handle implements [gunim.Handler].
func (l *VirtualList) Handle(e input.Event, u *gunim.UI) bool { return l.handle(e, u) }

// Reveal implements [gunim.Revealer]: it scrolls just far enough to
// bring r, in the list's own space, into view.
func (l *VirtualList) Reveal(r geom.Rect, u *gunim.UI) { l.reveal(r, u) }

// EdgeScroll implements [gunim.EdgeScroller]: a drag held near the top
// or the bottom scrolls the list.
func (l *VirtualList) EdgeScroll(p geom.Point, dt time.Duration, u *gunim.UI) geom.Point {
	return l.edgeScroll(p, dt, u)
}

// Layout implements [gunim.Node].
//
// It walks the keys once, adding up heights, and builds a row for each
// key in view that has none, drops the rows that have scrolled away,
// and lays out and places the rest.
func (l *VirtualList) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	own := c.Max
	width := own.W
	spacing := l.Spacing.Get(f.Theme)
	move := Quick.Get(f.Theme)
	if l.StickToEnd {
		// A view the pointer holds, or that coasts, goes where it is taken.
		l.stuck = !l.held && !l.gripped && !l.flinging && (!l.laidOut && l.openAt == "" || l.laidOut && l.AtEnd())
	}
	l.th, l.viewport = f.Theme, own.H

	// Rows the engine has taken out are gone for good.
	children := make(map[*row]gunim.Child, kids.Len())
	for kid := range kids.All {
		if r, ok := kid.Node().(*row); ok {
			children[r] = kid
		}
	}
	for k, r := range l.live {
		if _, ok := children[r]; !ok {
			delete(l.live, k)
			if l.gone[k] {
				delete(l.gone, k)
				delete(l.heights, k)
				l.order = deleteKey(l.order, k)
				l.dirty = true
			}
		}
	}
	if l.dirty || spacing != l.gap {
		// Items that come or go above the view shift what is in it.
		// Hold the first row in view where it is, unless the list is at
		// its top, where a new item should push in where it shows.
		anchor, at, ok := l.anchor()
		l.gap = spacing
		l.index()
		// A view just put somewhere stays there: the rows are new, and
		// holding the old first row would move it off.
		if ok && !l.jumped {
			if i := slices.Index(l.order, anchor); i >= 0 {
				if d := float32(l.tops.sum(i)) - at; d != 0 {
					l.shift(d)
					// The rows move by as much, so none moves on screen.
					for _, r := range l.live {
						anim.Shift(r.y, d)
					}
				}
			}
		}
	}

	if !l.laidOut && l.openAt != "" {
		if i := slices.Index(l.order, l.openAt); i >= 0 {
			l.jumpToY(float32(l.tops.sum(i)))
		}
	}
	if l.stuck {
		l.stickTo(float32(l.tops.sum(len(l.order)))-spacing, own.H)
	}
	offset := l.offset.Value()
	top, bottom := offset-overscan, offset+own.H+overscan
	keep := make(map[Key]bool, len(l.live))
	i := l.tops.find(float64(max(top, 0)))
	// A row closed to nothing just above takes no room, and may be one
	// growing in: lay it out too.
	for i > 0 && l.entry[i-1] == 0 {
		i--
	}
	y := float32(l.tops.sum(i))
	// carry is how far the rows after one above the view that changed
	// height have moved, along with the offset, this frame.
	var carry float32
	for ; i < len(l.order) && y <= bottom; i++ {
		k := l.order[i]
		r, built := l.live[k]
		if built && carry != 0 {
			anim.Shift(r.y, carry)
		}
		if !built {
			if l.gone[k] {
				y += l.entry[i]
				continue
			}
			r = newRow(k, l.build(k))
			// A row above a view just put somewhere comes at once:
			// growing in, it would push the view down from where it
			// was put.
			r.instant = !l.animate[k] || l.still || (l.jumped && y < offset)
			if r.instant {
				r.fade.Jump(1)
				r.slide.Jump(1)
			}
			delete(l.animate, k)
			r.y.Jump(y)
			l.live[k] = r
			children[r] = kids.Build(r)
		}
		keep[k] = true
		kid := children[r]
		r.presence = kid.Presence()
		size := kid.Layout(gunim.Constraints{Min: geom.Sz(width, 0), Max: geom.Sz(width, 0)})
		if !l.gone[k] {
			l.heights[k] = r.inner
		}
		if e := l.entryOf(size.H, r); e != l.entry[i] {
			d := e - l.entry[i]
			// Above the view means starting above it: a row at its top
			// edge that grows is one growing into view.
			above := y < offset && y+l.entry[i] <= offset
			l.entry[i] = e
			l.tops.add(i, float64(d))
			if above {
				// A row above the view changed height: move the offset
				// with it, so what is on screen stays still.
				offset += d
				l.shift(d)
				carry += d
			}
		}
		r.y.Animate(y, move)
		kid.Place(geom.Pt(0, r.y.Value()-onPixels(offset, f.Scale)))
		y += l.entry[i]
	}
	// Rows that have scrolled away go at once.
	for k, r := range l.live {
		if !keep[k] && !l.gone[k] {
			r.instant = true
			kids.Drop(r)
			delete(l.live, k)
		}
	}
	content := max(0, float32(l.tops.sum(len(l.order)))-spacing)
	// Near the start, ask for what comes before it; well away, be ready to ask again.
	if l.OnReachStart != nil && l.laidOut && len(l.order) > 0 {
		switch at := l.offset.Value(); {
		case !l.reached && at < own.H:
			l.reached = true
			if v := l.OnReachStart(f.UI()); v != nil {
				f.Send(l, v)
			}
		case l.reached && at > 3*own.H:
			l.reached = false
		}
	}
	if l.stuck {
		// Rows measured in view moved the end: follow it, and the rows with it.
		if at := l.stickTo(content, own.H); at != offset {
			for k := range keep {
				r := l.live[k]
				children[r].Place(geom.Pt(0, r.y.Value()-onPixels(at, f.Scale)))
			}
			l.again = true
		}
	}
	l.fit(content, own, f.Theme)
	l.still = false
	l.laidOut = true
	return own
}

// Paint implements [gunim.Node].
func (l *VirtualList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	func() {
		defer l.layer(p, geom.Rect{Max: box.Point()}, f.Theme)()
		offset := l.offset.Value()
		for kid := range kids.All {
			r, ok := kid.Node().(*row)
			if !ok {
				continue
			}
			// Only what reaches the view is drawn; the rows built
			// beyond it wait there for a scroll.
			top := r.y.Value() - offset
			if top > box.H || top+kid.Size().H < 0 {
				continue
			}
			kid.Paint(p)
		}
	}()
	l.paintBar(p, f)
}

func deleteKey(keys []Key, k Key) []Key {
	for i, o := range keys {
		if o == k {
			return append(keys[:i], keys[i+1:]...)
		}
	}
	return keys
}
