package widget

import (
	"slices"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// reorder is a row the pointer holds in a [List].
type reorder struct {
	// key is the row. pressed is set from the press, and active once
	// the pointer has moved far enough to pick the row up.
	key     Key
	pressed bool
	active  bool
	// from is where the press was, grab how far below the row's top,
	// and y where the row's top is now, all in the list's space.
	from, grab, y float32
	// again is set for a press that goes on a double or triple click.
	again bool
}

// pickUp is how far the pointer moves before it picks a row up, so a
// click stays a click.
const pickUp = 4

// Handle implements [gunim.Handler]. With OnReorder set, a press on a row
// that nothing inside the row takes, and a move of a few pixels, picks
// the row up: it lifts, follows the pointer, and the other rows spring
// aside to open a gap where it would land. Letting go drops it into the
// gap and runs OnReorder with the new order. With OnActivate set, a press let go
// on the row it began on without picking it up is a click, unless
// ClickOnce is set and the press goes on a double click. A press puts
// the keys' cursor on its row.
func (l *List) Handle(e input.Event, u *gunim.UI) bool {
	if l.OnReorder == nil && l.OnActivate == nil {
		return false
	}
	th := u.Theme()
	switch e := e.(type) {
	case input.KeyPress:
		return l.key(e, u)
	case input.FocusGained:
		l.enter(e, u)
		return true
	case input.FocusLost:
		l.walk.Animate(0, Settle.Get(th))
		return true
	case input.FocusRing:
		l.whole = e.On && !e.Grouped
		l.ring.Animate(ringTo(e), RingFade.Get(th))
		u.Invalidate()
		return true
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		k, top, ok := l.rowAt(e.Pos.Y)
		if !ok {
			return false
		}
		l.drag = reorder{key: k, pressed: true, from: e.Pos.Y, grab: e.Pos.Y - top, y: top, again: e.Clicks > 1}
		l.cursor = k
		l.walk.Animate(0, Settle.Get(th))
	case input.PointerMove:
		if !l.drag.pressed {
			return false
		}
		if l.OnReorder != nil && !l.drag.active && abs32(e.Pos.Y-l.drag.from) >= pickUp && l.movable(l.drag.key) {
			l.drag.active = true
			l.lift.Animate(1, Quick.Get(th))
		}
		if l.drag.active {
			l.drag.y = e.Pos.Y - l.drag.grab
		}
	case input.PointerUp:
		if !l.drag.pressed {
			return false
		}
		once := l.ClickOnce && l.drag.again
		if k, _, ok := l.rowAt(e.Pos.Y); ok && !l.drag.active && l.OnActivate != nil && k == l.drag.key && !once {
			act(u, l, gunim.CuePress, l.OnActivate, k)
		}
		l.drop(u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// DragHeld implements [gunim.DragHolder]: a row held up by the pointer
// scrolls the views around the list when it nears their edges.
func (l *List) DragHeld() bool { return l.drag.active }

// drop ends a drag, into the gap, and tells the application of the new
// order. The row springs into its place from where it was let go.
func (l *List) drop(u *gunim.UI) {
	if l.drag.active {
		next := l.dropOrder()
		if !slices.Equal(next, l.order) {
			l.order = next
			l.arrange(u)
			send(u, l, l.OnReorder(slices.Clone(next), u))
		}
		l.lift.Animate(0, Settle.Get(u.Theme()))
	}
	l.drag.pressed, l.drag.active = false, false
}

// rowAt returns the row drawn at y in the list's space, and its top there. A row springing to a new place is found
// where it is drawn now, and where two cross, the one drawn over the other.
func (l *List) rowAt(y float32) (Key, float32, bool) {
	var key Key
	var top float32
	found := false
	for _, k := range l.order {
		r, ok := l.rows[k]
		s, laid := l.slots[k]
		if !ok || !laid || r.presence == gunim.Exiting {
			continue
		}
		if at := r.y.Value(); y >= at && y < at+s[1] {
			key, top, found = k, at, true
		}
	}
	return key, top, found
}

// movable reports whether row k can be moved: with ReorderGroup set,
// only a row in a group.
func (l *List) movable(k Key) bool {
	return l.ReorderGroup == nil || l.ReorderGroup(k) != ""
}

// keepInGroup puts k, taken out of rest, back at at, or as near it as
// its group lets: within the run of the rows of its group.
func (l *List) keepInGroup(rest []Key, k Key, at int) []Key {
	if l.ReorderGroup != nil {
		g := l.ReorderGroup(k)
		lo, hi := -1, -1
		for i, o := range rest {
			if l.ReorderGroup(o) == g {
				if lo < 0 {
					lo = i
				}
				hi = i + 1
			}
		}
		if lo < 0 {
			// Alone in its group: it stays where it was.
			return slices.Clone(l.order)
		}
		at = min(max(at, lo), hi)
	}
	return slices.Insert(slices.Clone(rest), at, k)
}

// moveRow moves the cursor's row a place down, or up, within its group,
// and tells the application, as Alt and an arrow key do.
func (l *List) moveRow(down bool, u *gunim.UI) bool {
	k := l.cursor
	at := slices.Index(l.order, k)
	if at < 0 || !l.movable(k) {
		return false
	}
	rest := slices.Delete(slices.Clone(l.order), at, at+1)
	to := at - 1
	if down {
		to = at + 1
	}
	if to < 0 || to > len(rest) {
		return true
	}
	next := l.keepInGroup(rest, k, to)
	if slices.Equal(next, l.order) {
		return true
	}
	l.order = next
	l.arrange(u)
	act(u, l, gunim.CueTick, l.OnReorder, slices.Clone(next))
	u.Invalidate()
	return true
}

// dropOrder returns the order a drop now would leave: the dragged row
// taken out and put back where its middle falls among the others, kept
// among the rows of its group.
func (l *List) dropOrder() []Key {
	k := l.drag.key
	h := l.slots[k][1]
	middle := l.drag.y + h/2
	out := make([]Key, 0, len(l.order))
	placed := false
	y := float32(0)
	for _, o := range l.order {
		if o == k {
			continue
		}
		oh := l.slots[o][1]
		if !placed && middle < y+oh/2 {
			out = append(out, k)
			placed = true
		}
		out = append(out, o)
		y += oh + l.spacing
	}
	if !placed {
		out = append(out, k)
	}
	if l.ReorderGroup == nil {
		return out
	}
	at := slices.Index(out, k)
	return l.keepInGroup(slices.Delete(out, at, at+1), k, at)
}

// paintLifted draws a lifted row: a little larger, over a shadow that
// deepens as it rises.
func (l *List) paintLifted(p *paint.Painter, f gunim.Frame, _ geom.Size, kid gunim.Child) {
	t := min(max(l.lift.Value(), 0), 1)
	r := l.rows[l.drag.key]
	box := geom.Rc(0, r.y.Value(), kid.Size().W, kid.Size().H)
	defer p.Push(paint.Scale(1+0.02*t, box.Center()))()
	shadow := DialogShadow.Get(f.Theme)
	shadow.A = uint8(float32(shadow.A) * t)
	p.ShadowRRect(box, RowRadius.Get(f.Theme), paint.Fill{}, paint.Shadow{
		Offset: geom.Pt(0, 6*t), Blur: 18 * t, Color: shadow,
	})
	kid.Paint(p)
}
