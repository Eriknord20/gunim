package plugtest

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// scroller is a view that says how far it is scrolled and the size it was laid out at, as one that embeds a
// [plug.Room] does.
type scroller interface {
	gunim.Node
	Scrolled() geom.Point
	Laid() geom.Size
}

// scrollsUnderLeast fails t unless a view made by view, shown at size under its least size, scrolls: the wheel moves
// it and the move glides, a drag on empty ground moves it, and Tab to a control out of sight brings it into view.
func scrollsUnderLeast(t testing.TB, view func() gunim.Node, size geom.Size) {
	t.Helper()
	s, _, _ := showScroller(t, view, size)
	if s == nil {
		return
	}
	free, over := places(t, view, size)
	wheelScrolls(t, view, size, append(free, over...))
	dragScrolls(t, view, size, append(free, over...))
	tabReveals(t, view, size)
}

// wheelScrolls fails t unless the wheel at one of at scrolls the view, gliding. The wheel turns a few times, as a part
// inside the view may take it first, up to its end.
func wheelScrolls(t testing.TB, view func() gunim.Node, size geom.Size, at []geom.Point) {
	t.Helper()
	for _, p := range at {
		s, w, _ := showScroller(t, view, size)
		if s == nil {
			return
		}
		var seen []geom.Point
		for range 6 {
			w.Input(input.Scroll{Pos: p, Delta: geom.Pt(0, -400)})
			if seen = scrolledFrames(w, s, 60); seen[len(seen)-1] != (geom.Point{}) {
				break
			}
		}
		end := seen[len(seen)-1]
		if end == (geom.Point{}) {
			continue
		}
		if seen[0] == end {
			t.Errorf("at %v×%v the wheel at %v jumped the view to %v in one frame", size.W, size.H, p, end)
		}
		for i := 1; i < len(seen); i++ {
			if seen[i].X < seen[i-1].X-0.5 || seen[i].Y < seen[i-1].Y-0.5 {
				t.Errorf("at %v×%v the wheel at %v scrolled the view back, from %v to %v", size.W, size.H, p,
					seen[i-1], seen[i])
				break
			}
		}
		return
	}
	t.Errorf("at %v×%v the wheel scrolls the view nowhere", size.W, size.H)
}

// dragScrolls fails t unless a drag scrolls the view: a drag from one of at or from another place on the view, or,
// where its parts take every press, a drag sent to the view itself. The drags run one after another in one view,
// until one scrolls it.
func dragScrolls(t testing.TB, view func() gunim.Node, size geom.Size, at []geom.Point) {
	t.Helper()
	s, w, u := showScroller(t, view, size)
	if s == nil {
		return
	}
	for y := float32(10); y < size.H; y += 20 {
		for x := float32(10); x < size.W; x += 20 {
			at = append(at, geom.Pt(x, y))
		}
	}
	drag := func(send func(input.Event), p geom.Point) bool {
		send(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1})
		for i := float32(1); i <= 6; i++ {
			send(input.PointerMove{Pos: p.Sub(geom.Pt(5*i, 5*i))})
		}
		send(input.PointerUp{Pos: p.Sub(geom.Pt(30, 30)), Button: input.ButtonPrimary})
		return s.Scrolled() != (geom.Point{})
	}
	for _, p := range at {
		if drag(func(e input.Event) { w.Input(e) }, p) {
			return
		}
	}
	if h, ok := s.(gunim.Handler); ok {
		for _, p := range at {
			if drag(func(e input.Event) { h.Handle(e, u) }, p) {
				return
			}
		}
	}
	t.Errorf("at %v×%v a drag on empty ground scrolls the view nowhere", size.W, size.H)
}

// tabReveals fails t unless Tab to each control out of sight brings it into view.
func tabReveals(t testing.TB, view func() gunim.Node, size geom.Size) {
	t.Helper()
	s, w, u := showScroller(t, view, size)
	if s == nil {
		return
	}
	box := geom.Rect{Max: size.Point()}
	wrong := 0
	var first gunim.Node
	for range 400 {
		u.FocusNext(true)
		n := u.Focused()
		if n == nil || n == first {
			break
		}
		if first == nil {
			first = n
		}
		before, ok := u.Bounds(n)
		laid := geom.Rect{Max: s.Laid().Point()}
		if !ok || !laid.Contains(before.Min.Add(s.Scrolled())) || within(before, box, 0.5) {
			// In sight already, or put away outside the view on purpose.
			scrolledFrames(w, s, 2)
			continue
		}
		scrolledFrames(w, s, 60)
		after, _ := u.Bounds(n)
		shown := after.Min.X >= -0.5 && after.Min.Y >= -0.5
		if after.Size().W <= size.W && after.Size().H <= size.H {
			shown = shown && after.Max.X <= box.Max.X+0.5 && after.Max.Y <= box.Max.Y+0.5
		}
		if !shown {
			wrong++
			if wrong <= 3 {
				t.Errorf("at %v×%v Tab to %s at %v left it at %v, out of sight", size.W, size.H, nameOf(n), before,
					after)
			}
		}
	}
	if wrong > 3 {
		t.Errorf("at %v×%v Tab left %d controls in all out of sight", size.W, size.H, wrong)
	}
}

// places returns where to turn the wheel or start a drag over the view: places over no part that takes input, and a
// few places spread over the parts that do.
func places(t testing.TB, view func() gunim.Node, size geom.Size) (free, over []geom.Point) {
	t.Helper()
	s, _, u := showScroller(t, view, size)
	if s == nil {
		return nil, nil
	}
	var taken []geom.Rect
	var walk func(n gunim.Node)
	walk = func(n gunim.Node) {
		if _, ok := n.(gunim.Handler); ok && n != gunim.Node(s) {
			if b, ok := u.Bounds(n); ok {
				taken = append(taken, b)
			}
		}
		if c, ok := n.(gunim.Composite); ok {
			for _, k := range c.Children() {
				walk(k)
			}
		}
	}
	walk(s)
	var all []geom.Point
	for y := float32(4); y < size.H; y += 12 {
		for x := float32(4); x < size.W; x += 12 {
			p := geom.Pt(x, y)
			hit := false
			for _, r := range taken {
				if r.Contains(p) {
					hit = true
					break
				}
			}
			if hit {
				all = append(all, p)
			} else {
				free = append(free, p)
			}
		}
	}
	for i := 0; i < len(all); i += max(len(all)/24, 1) {
		over = append(over, all[i])
	}
	// The free place nearest each of nine points spread over the view is enough.
	var some []geom.Point
	for a := float32(1); a < 6; a += 2 {
		for b := float32(1); b < 6; b += 2 {
			at := geom.Pt(size.W*a/6, size.H*b/6)
			best, far := -1, float32(0)
			for i, p := range free {
				if d := (p.X-at.X)*(p.X-at.X) + (p.Y-at.Y)*(p.Y-at.Y); best < 0 || d < far {
					best, far = i, d
				}
			}
			if best >= 0 && !slices.Contains(some, free[best]) {
				some = append(some, free[best])
			}
		}
	}
	free = some
	return free, over
}

// showScroller returns a view made by view, laid out at size in an offscreen window after a few frames, the window
// and its UI. It fails t and returns a nil view where the view does not scroll or is laid out no larger than size.
func showScroller(t testing.TB, view func() gunim.Node, size geom.Size) (scroller, *gunim.Window, *gunim.UI) {
	t.Helper()
	s, ok := view().(scroller)
	if !ok {
		t.Errorf("the view embeds no plug.Room and does not scroll")
		return nil, nil, nil
	}
	pr := &probe{kid: s}
	w := mount(t, size, pr)
	scrolledFrames(w, s, 10)
	if pr.u == nil {
		t.Errorf("the view was never laid out")
		return nil, nil, nil
	}
	if l := s.Laid(); l.W <= size.W && l.H <= size.H {
		t.Errorf("at %v×%v the view is laid out at %v×%v, no larger than its box", size.W, size.H, l.W, l.H)
		return nil, nil, nil
	}
	return s, w, pr.u
}

// scrolledFrames runs n frames of a sixtieth of a second, and returns how far s was scrolled after each.
func scrolledFrames(w *gunim.Window, s scroller, n int) []geom.Point {
	at := make([]geom.Point, 0, n)
	for range n {
		w.Frame(time.Second / 60)
		at = append(at, s.Scrolled())
	}
	return at
}
