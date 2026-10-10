package widget

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Split tokens.
var (
	// SplitGap is the room between a Split's panes, which the pointer
	// grabs to move the divider.
	SplitGap = theme.Length("split.gap", 6)
	// SplitLine and SplitHot are the divider's colour at rest, and
	// while the pointer is on it or drags it.
	SplitLine = theme.Color("split.line", color.NRGBA{R: 0x2c, G: 0x31, B: 0x3d, A: 0xff})
	SplitHot  = theme.Color("split.hot", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff})
)

// splitMin is the smallest a pane gets while the divider is dragged.
const splitMin = 48

// Split shows two panes side by side, or one above the other, with a
// divider between them that the pointer drags. Splits nest, for any
// arrangement of panes.
//
// The first pane's share of the space glides to where SetShare aims
// it, so a pane opened with a Split whose share starts at 1 slides in
// as the divider springs into place. During a slide SlideFrom asks for,
// each pane is laid out once at the larger of the lengths it starts and
// ends at, and shows only what its place holds as it moves: what is in
// it, such as a terminal fitting its text to its size, is resized once
// as the slide ends, and not on every frame of it. At a share of 0 or 1 the divider
// narrows away and one pane has all the space. A double click on the
// divider evens the panes out.
//
// The divider takes the keyboard by Tab, between the two panes, and a
// ring fades in round it. The arrow keys along the split glide it a step,
// a fiftieth of the space, and with Shift a tenth; Home and End take it
// as far as a drag can, and Enter evens the panes out. Each move sends
// OnCommit, as letting go of a drag does.
type Split struct {
	anim.Group
	// Axis lays the panes side by side, which is the zero value, or one
	// above the other with [Vertical].
	Axis Axis
	// Fixed keeps the first pane's length, in logical pixels, as the
	// space changes, as a sidebar does. Share and SetShare then count
	// that length, where they otherwise count a share from 0 to 1.
	Fixed bool
	// Glide, when set, is the motion SetShare glides with, in place of
	// [Settle].
	Glide theme.Token[anim.Spring]
	// OnCommit, when set, runs on the UI goroutine once the pointer lets
	// the divider go, or a key moves it, with the first pane's new share; a
	// non-nil result is sent to the application as the split's intent.
	OnCommit func(share float32, u *gunim.UI) gunim.Intent

	// laid is set by the first layout; a share set before it shows at once.
	laid bool
	// slide says the first layout starts the share at from and glides it to its target, as SlideFrom asks.
	slide bool
	from  float32
	// sliding is set while that glide lasts, and slots are then the
	// places the first and the second pane show in.
	sliding       bool
	slots         [2]geom.Rect
	first, second gunim.Node
	// bar is the divider as the keyboard knows it, between the panes.
	bar   *splitBar
	share *anim.Float
	hot   *anim.Float
	// held is set while the pointer drags the divider, grab being how
	// far into the gap it took hold.
	held bool
	grab float32
	over bool
	// length is the space along the split the last layout had, gap the
	// room the divider took, and own the split's size.
	length, gap float32
	own         geom.Size
}

// NewSplit returns first and second split evenly.
func NewSplit(first, second gunim.Node) *Split {
	s := &Split{first: first, second: second, share: anim.NewFloat(0.5), hot: anim.NewFloat(0)}
	s.bar = &splitBar{s: s, ring: anim.NewFloat(0)}
	s.Add(s.share, s.hot, s.bar.ring)
	return s
}

// Children implements [gunim.Composite]: the first pane, the divider, and the second pane, in the order Tab visits
// them.
func (s *Split) Children() []gunim.Node { return []gunim.Node{s.first, s.bar, s.second} }

// Panes returns the two panes.
func (s *Split) Panes() (first, second gunim.Node) { return s.first, s.second }

// SetPane puts n in place of pane i, 0 for the first and 1 for the
// second. The pane it replaces leaves the way it animates out.
func (s *Split) SetPane(i int, n gunim.Node, u *gunim.UI) {
	old, at := &s.first, 0
	if i == 1 {
		old, at = &s.second, 2
	}
	if *old == n {
		return
	}
	if u == nil {
		// The split is still to be mounted, and mounts the pane it holds then.
		*old = n
		return
	}
	u.Remove(*old)
	*old = n
	u.InsertAt(s, at, n)
}

// Held reports whether the pointer is dragging the divider.
func (s *Split) Held() bool { return s.held }

// Share returns the first pane's share of the space that the split is
// heading for, from 0 to 1.
func (s *Split) Share() float32 { return s.share.Target() }

// SetShare aims the first pane's share of the space at v, from 0 to 1,
// or its length with Fixed, and sends no intent. Once the split is laid
// out the divider glides there with Glide; before that, or with a nil u,
// it jumps.
func (s *Split) SetShare(v float32, u *gunim.UI) {
	if !s.Fixed {
		v = min(max(v, 0), 1)
	}
	v = max(v, 0)
	if s.laid {
		// Asked for anew: the panes follow the divider as it goes.
		s.sliding = false
	}
	if !s.laid || u == nil {
		s.share.Jump(v)
		return
	}
	s.share.Animate(v, s.glide().Get(u.Theme()))
	u.Invalidate()
}

// glide is the motion the divider glides with: Glide, or [Settle].
func (s *Split) glide() theme.Token[anim.Spring] {
	if s.Glide.Key() != "" {
		return s.Glide
	}
	return Settle
}

// SlideFrom has a split still to be laid out open with the first pane's share at v, and glide from there to the
// share SetShare gave it, with Glide: a pane slides in as it opens. Once the split is laid out it does nothing.
func (s *Split) SlideFrom(v float32) {
	if s.laid {
		return
	}
	s.slide, s.from = true, v
}

func (s *Split) along(p geom.Point) float32 {
	if s.Axis == Vertical {
		return p.Y
	}
	return p.X
}

// fraction returns the first pane's share of the space, from 0 to 1.
func (s *Split) fraction() float32 {
	v := s.share.Value()
	if s.Fixed {
		if s.length <= 0 {
			return 0
		}
		v /= s.length
	}
	return min(max(v, 0), 1)
}

// room returns the gap the divider takes: all of it, or less as one
// pane nears the whole space.
func (s *Split) room(gap float32) float32 {
	v := s.fraction()
	return gap * min(max(min(v, 1-v)*20, 0), 1)
}

// firstLength returns the first pane's length along the split.
func (s *Split) firstLength() float32 {
	space := s.length - s.gap
	if s.Fixed {
		return float32(int(min(max(s.share.Value(), 0), space) + 0.5))
	}
	return float32(int(space*s.fraction() + 0.5))
}

// Layout implements [gunim.Node]. The split fills the space it is
// given.
func (s *Split) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	if !s.laid && s.slide {
		to := s.share.Target()
		s.share.Jump(s.from)
		s.share.Animate(to, s.glide().Get(f.Theme))
		s.sliding = true
	}
	s.laid = true
	if s.held || !s.share.Active() {
		s.sliding = false
	}
	own := c.Max
	s.own = own
	s.length = own.W
	if s.Axis == Vertical {
		s.length = own.H
	}
	full := SplitGap.Get(f.Theme)
	s.gap = s.room(full)
	a := s.firstLength()
	b := max(0, s.length-s.gap-a)
	if s.sliding {
		a0, b0 := s.lengthsAt(s.from, full)
		a1, b1 := s.lengthsAt(s.share.Target(), full)
		s.slots[0] = s.slot(0, a, own)
		s.slots[1] = s.slot(a+s.gap, b, own)
		for kid := range kids.All {
			switch kid.Node() {
			case s.first:
				s.place(kid, 0, max(a0, a1), own)
			case s.second:
				s.place(kid, a+s.gap, max(b0, b1), own)
			case s.bar:
				s.place(kid, a, s.gap, own)
			default:
				s.place(kid, 0, a, own)
			}
		}
		return own
	}
	for kid := range kids.All {
		switch kid.Node() {
		case s.first:
			s.place(kid, 0, a, own)
		case s.second:
			s.place(kid, a+s.gap, b, own)
		case s.bar:
			s.place(kid, a, s.gap, own)
		default:
			// A pane on its way out keeps the first pane's place.
			s.place(kid, 0, a, own)
		}
	}
	return own
}

// lengthsAt returns the panes' lengths along the split at share v, with
// a divider full wide between them.
func (s *Split) lengthsAt(v, full float32) (a, b float32) {
	frac := min(max(v, 0), 1)
	if s.Fixed {
		frac = 0
		if s.length > 0 {
			frac = min(max(v/s.length, 0), 1)
		}
	}
	gap := full * min(max(min(frac, 1-frac)*20, 0), 1)
	space := s.length - gap
	if s.Fixed {
		a = float32(int(min(max(v, 0), space) + 0.5))
	} else {
		a = float32(int(space*frac + 0.5))
	}
	return a, max(0, space-a)
}

// slot returns the place from at along the split, length long.
func (s *Split) slot(at, length float32, own geom.Size) geom.Rect {
	if s.Axis == Vertical {
		return geom.Rc(0, at, own.W, length)
	}
	return geom.Rc(at, 0, length, own.H)
}

func (s *Split) place(kid gunim.Child, at, length float32, own geom.Size) {
	size := geom.Sz(length, own.H)
	pos := geom.Pt(at, 0)
	if s.Axis == Vertical {
		size, pos = geom.Sz(own.W, length), geom.Pt(0, at)
	}
	kid.Layout(gunim.Tight(size))
	kid.Place(pos)
}

// Paint implements [gunim.Node]. A pane with no room draws nothing.
func (s *Split) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		if sz := kid.Size(); sz.W < 1 || sz.H < 1 {
			continue
		}
		i := -1
		if s.sliding {
			switch kid.Node() {
			case s.first:
				i = 0
			case s.second:
				i = 1
			}
		}
		if i < 0 {
			kid.Paint(p)
			continue
		}
		// Sliding, a pane shows only what its place holds.
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: s.slots[i], Opacity: 1, Clip: true})()
			kid.Paint(p)
		}()
	}
	if s.gap < 0.5 {
		return
	}
	line := SplitLine.Get(f.Theme)
	if t := min(max(s.hot.Value(), 0), 1); t > 0 {
		line = mixColor(line, SplitHot.Get(f.Theme), t)
	}
	at := s.firstLength() + s.gap/2
	width := 1 + s.hot.Value()
	r := geom.Rc(at-width/2, 0, width, box.H)
	if s.Axis == Vertical {
		r = geom.Rc(0, at-width/2, box.W, width)
	}
	p.RRect(r, 0, paint.Solid(line))
}

// inGap reports whether p, in the split's space, is on the divider.
func (s *Split) inGap(p geom.Point) bool {
	a := s.firstLength()
	at := s.along(p)
	return s.gap >= 0.5 && at >= a && at < a+s.gap
}

// Cursor implements [gunim.CursorShaper]: resize arrows over the
// divider.
func (s *Split) Cursor(p geom.Point) input.Cursor {
	if !s.held && !s.inGap(p) {
		return input.CursorArrow
	}
	if s.Axis == Vertical {
		return input.CursorResizeV
	}
	return input.CursorResizeH
}

// ClaimsPointer implements [gunim.PointerClaimer]: the split takes the pointer on the divider, for dragging it.
func (s *Split) ClaimsPointer(p geom.Point) bool { return s.inGap(p) }

// DragsTouch implements [gunim.TouchDragger]: a finger on the divider
// drags it.
func (s *Split) DragsTouch() bool { return s.held }

// Handle implements [gunim.Handler]: the pointer drags the divider.
func (s *Split) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || !s.inGap(e.Pos) {
			return false
		}
		if e.Clicks == 2 && !s.Fixed {
			s.share.Animate(0.5, Settle.Get(th))
			s.moved(u)
			return true
		}
		s.held = true
		s.grab = s.along(e.Pos) - s.firstLength()
		s.light(true, u)
		return true
	case input.PointerMove:
		if s.held {
			space := s.length - s.gap
			if space > 0 {
				lo, hi := limits(space)
				a := min(max(s.along(e.Pos)-s.grab, lo), hi)
				if s.Fixed {
					s.share.Jump(a)
				} else {
					s.share.Jump(a / space)
				}
			}
			u.Invalidate()
			return true
		}
		s.light(s.inGap(e.Pos), u)
		return false
	case input.PointerUp:
		if !s.held {
			return false
		}
		s.held = false
		s.light(s.inGap(e.Pos), u)
		s.moved(u)
		return true
	case input.PointerLeave:
		if !s.held {
			s.light(false, u)
		}
	}
	return false
}

// light brightens the divider while the pointer is on it.
func (s *Split) light(on bool, u *gunim.UI) {
	if on == s.over {
		return
	}
	s.over = on
	to := float32(0)
	if on {
		to = 1
	}
	s.hot.Animate(to, Quick.Get(u.Theme()))
	u.Invalidate()
}

// limits returns how near the ends a drag or a key takes the first pane's length along space.
func limits(space float32) (lo, hi float32) {
	return min(splitMin, space/2), max(space-splitMin, space/2)
}

// key moves the divider for k, and reports whether k was one of its keys.
func (s *Split) key(k input.KeyPress, u *gunim.UI) bool {
	space := s.length - s.gap
	if space <= 0 || k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModAlt) {
		return false
	}
	back, on := input.KeyLeft, input.KeyRight
	if s.Axis == Vertical {
		back, on = input.KeyUp, input.KeyDown
	}
	step := space / 50
	if k.Mods.Has(input.ModShift) {
		step = space / 10
	}
	lo, hi := limits(space)
	a := s.share.Target()
	if !s.Fixed {
		a *= space
	}
	switch k.Key {
	case back:
		a = max(min(a, hi)-step, lo)
	case on:
		a = min(max(a, lo)+step, hi)
	case input.KeyHome:
		a = lo
	case input.KeyEnd:
		a = hi
	case input.KeyEnter, input.KeyKPEnter:
		if s.Fixed {
			return false
		}
		a = space / 2
	default:
		return false
	}
	if !s.Fixed {
		a /= space
	}
	if a != s.share.Target() {
		s.share.Animate(a, Quick.Get(u.Theme()))
		u.Invalidate()
		s.moved(u)
	}
	return true
}

func (s *Split) moved(u *gunim.UI) {
	if s.OnCommit != nil {
		send(u, s, s.OnCommit(s.share.Target(), u))
	}
}

// splitBar is a split's divider as the keyboard knows it: a node in the gap between the panes that takes the keys
// and shows a ring round the divider's line. The split itself takes the pointer there.
type splitBar struct {
	s    *Split
	ring *anim.Float
}

// Focusable implements [gunim.Focusable].
func (b *splitBar) Focusable() bool { return true }

// FocusOnPress implements [gunim.PressFocuser]: dragging the divider leaves the keyboard in the pane that has it.
func (b *splitBar) FocusOnPress() bool { return false }

// Handle implements [gunim.Handler].
func (b *splitBar) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.KeyPress:
		return b.s.key(e, u)
	case input.FocusRing:
		b.ring.Animate(ringTo(e), RingFade.Get(u.Theme()))
	case input.FocusLost:
		b.ring.Animate(0, RingFade.Get(u.Theme()))
	default:
		return false
	}
	return true
}

// Layout implements [gunim.Node]: the bar is the gap it is given.
func (b *splitBar) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Min
}

// Paint implements [gunim.Node]: the ring round the divider's line.
func (b *splitBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	FocusRing(p, geom.Rect{Max: box.Point()}, min(box.W, box.H)/2, b.ring.Value(), f.Theme)
}

// mixColor blends from a toward b by t, from 0 to 1.
func mixColor(a, b color.NRGBA, t float32) color.NRGBA {
	mix := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: mix(a.A, b.A)}
}
