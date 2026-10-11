package widget

import (
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// barLinger is how long the scrollbar stays after the content stops
// moving, before it fades.
const barLinger = 800 * time.Millisecond

// flingDecay is how a fling slows: its velocity falls to a third in a
// third of a second, much as a finger's fling does on a phone.
var flingDecay = anim.Decay{Tau: 0.33}

// scrolling is the scrolling a [Scroll] and a [VirtualList] share: the
// offset, the wheel and the keys, dragging and flinging, the stretch
// at either end, and the bar.
//
// The wheel and the keys set a target, and a spring carries the
// content there. A drag moves the content with the pointer, and a
// release flings it on at the pointer's speed, slowing to a stop. Past
// either end a drag stretches, with less give the further it goes, and
// the content springs back on release; a fling that runs past an end
// carries on a little and springs back the same way.
//
// The bar shows while the content moves and while the pointer is over
// the widget, and fades a moment after both stop. The pointer on the
// bar widens it; dragging the thumb scrolls with it, and a press on
// the track either side of the thumb pages toward the press.
type scrolling struct {
	// DragScroll lets the pointer drag the content and fling it. A
	// press the content's own nodes take, such as a button's, is
	// theirs.
	DragScroll bool
	// NoFade keeps the content from fading where more of it lies past
	// the top or the bottom edge.
	NoFade bool

	offset *anim.Float
	target float32
	bar    *anim.Float
	// stillSince is when the content last moved, or the pointer left,
	// zero while it moves or the pointer keeps the bar up, and still the
	// frames' time since then; the bar fades once either has reached
	// barLinger, the window sleeping meanwhile.
	stillSince time.Time
	still      time.Duration
	// th is the window's live theme, kept from Layout for Step, which
	// has no frame. It is a handle to the live values, never a copy.
	th *theme.Live

	content, viewport, width float32

	// held is set while the pointer drags the content. grabY and
	// grabAt are where the drag started and the offset then, and
	// trail the latest pointer positions, for the fling's speed.
	held   bool
	grabY  float32
	grabAt float32
	trail  [8]sample
	trails int
	// flinging is set while the content coasts from a fling.
	flinging bool
	// jumped says the view was put somewhere at once, since the last
	// layout: bringing it back inside new content then jumps too.
	jumped bool

	// over is set while the pointer is over the widget, and onBar
	// while it is over the bar's strip. gripped is set while the
	// pointer drags the thumb, from grabY and grabAt as a drag of the
	// content does.
	over, onBar, gripped bool
	// wide grows the bar from 0 to 1 while the pointer is on it.
	wide *anim.Float
}

type sample struct {
	y float32
	t time.Time
}

func newScrolling() scrolling {
	return scrolling{offset: anim.NewFloat(0), bar: anim.NewFloat(0), wide: anim.NewFloat(0)}
}

// Offset returns how far the content is scrolled right now.
func (s *scrolling) Offset() float32 { return s.offset.Value() }

// JumpTo puts the view at y at once, for content that is new rather
// than moved, as another list shown in the same place.
func (s *scrolling) JumpTo(y float32) { s.jumpToY(y) }

// jumpTo puts the view at y at once, for content that is new rather
// than moved: a folder opened in a table, whose rows would otherwise
// glide in from wherever the last folder was scrolled to.
func (s *scrolling) jumpToY(y float32) {
	s.flinging = false
	s.target = max(0, y)
	s.offset.Jump(s.target)
	s.jumped = true
}

// ScrollTo sets the target offset, clamped to the content, and carries
// the content there with motion.
func (s *scrolling) ScrollTo(y float32, motion anim.Motion) {
	s.flinging = false
	s.target = s.clamp(y)
	s.offset.Retarget(s.target, motion)
	s.showBar()
}

func (s *scrolling) showBar() {
	s.bar.Animate(1, Quick.Get(s.th))
	s.stillSince, s.still = time.Time{}, 0
}

// end returns the largest offset.
func (s *scrolling) end() float32 { return max(0, s.content-s.viewport) }

func (s *scrolling) clamp(y float32) float32 { return max(0, min(y, s.end())) }

// stretch returns the offset a drag to y shows: y itself inside the
// content, and past an end, a part of the way there, less the further
// it goes.
func (s *scrolling) stretch(y float32) float32 {
	give := func(over float32) float32 {
		d := max(s.viewport, 1) * 0.55
		return d * (1 - 1/(over/d+1))
	}
	switch {
	case y < 0:
		return -give(-y)
	case y > s.end():
		return s.end() + give(y-s.end())
	}
	return y
}

// Step implements [gunim.Animator] for the widget. A fling that runs
// past an end springs back to it, keeping its speed, and once the
// content has rested for a moment the bar fades.
func (s *scrolling) Step(dt time.Duration) bool {
	moving := s.offset.Step(dt)
	if s.flinging {
		at := s.offset.Value()
		switch {
		case !moving:
			s.flinging = false
			s.target = s.clamp(at)
		case at < 0 || at > s.end():
			s.flinging = false
			s.target = s.clamp(at)
			s.offset.Retarget(s.target, Settle.Get(s.th))
		}
	}
	// The pointer keeps the bar up without drawing frames; the linger
	// counts from when it leaves.
	// The linger is counted by the clock, and the window sleeps through
	// it: WakeIn asks for the frame that fades the bar.
	kept := s.over || s.gripped
	switch {
	case moving || s.held || kept:
		s.stillSince, s.still = time.Time{}, 0
	case s.bar.Target() > 0:
		if s.stillSince.IsZero() {
			s.stillSince = time.Now()
		}
		s.still += dt
		if s.lingered() >= barLinger {
			s.bar.Animate(0, Settle.Get(s.th))
			s.stillSince, s.still = time.Time{}, 0
		}
	}
	barMoving := s.bar.Step(dt)
	wideMoving := s.wide.Step(dt)
	return moving || barMoving || wideMoving
}

// WakeIn implements [gunim.Waker]: the frame that fades the bar, once
// it has stayed barLinger.
func (s *scrolling) WakeIn() time.Duration {
	if s.stillSince.IsZero() || s.bar.Target() <= 0 {
		return 0
	}
	return max(barLinger-s.lingered(), time.Millisecond)
}

// lingered is how long the bar has stayed since the content was still:
// the frames' time, or the clock's where the window slept through it.
func (s *scrolling) lingered() time.Duration {
	return max(s.still, time.Since(s.stillSince))
}

// scrollable reports whether the content is taller than the view.
func (s *scrolling) scrollable() bool { return s.content > s.viewport }

// ClaimsPointer implements [gunim.PointerClaimer]: the strip along the
// right edge is the bar's, over whatever content lies under it.
func (s *scrolling) ClaimsPointer(p geom.Point) bool {
	if s.gripped {
		return true
	}
	if !s.scrollable() || s.th == nil {
		return false
	}
	return p.X >= s.width-ScrollbarGrabWidth.Get(s.th)-4
}

// barWidth returns the bar's width now, between its resting width and
// the width it grows to under the pointer.
func (s *scrolling) barWidth(th *theme.Live) float32 {
	rest, grab := ScrollbarWidth.Get(th), ScrollbarGrabWidth.Get(th)
	return rest + (grab-rest)*s.wide.Value()
}

// DragsTouch implements [gunim.TouchDragger] for the widgets that
// scroll: a finger on the bar's thumb drags it, and anywhere else
// scrolls the content.
func (s *scrolling) DragsTouch() bool { return s.gripped }

// thumb returns the part of the bar that stands for the view, w wide,
// in the widget's space.
func (s *scrolling) thumb(w float32) geom.Rect {
	length := min(s.viewport, max(s.viewport*s.viewport/s.content, 2*w, 24))
	at := max(0, min(s.offset.Value(), s.end()))
	top := (s.viewport - length) * at / s.end()
	return geom.Rc(s.width-w-2, top, w, length)
}

// setOnBar notes whether the pointer is on the bar, and grows or
// shrinks the bar to match.
func (s *scrolling) setOnBar(on bool, u *gunim.UI) {
	s.onBar = on
	var to float32
	if on || s.gripped {
		to = 1
	}
	if s.wide.Target() != to {
		s.wide.Animate(to, Quick.Get(u.Theme()))
		u.Invalidate()
	}
}

// barEvent follows the pointer over the widget and takes what is aimed
// at the bar: presses on it and drags of its thumb. It reports whether
// it took e. Other moves, enter and leave it only watches.
func (s *scrolling) barEvent(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		s.over = true
		s.showBar()
		s.setOnBar(s.ClaimsPointer(e.Pos), u)
		u.Invalidate()
	case input.PointerLeave:
		s.over = false
		s.setOnBar(false, u)
		u.Invalidate()
	case input.PointerMove:
		if s.gripped {
			span := s.viewport - s.thumb(s.barWidth(u.Theme())).Size().H
			if span > 0 {
				s.target = s.clamp(s.grabAt + (e.Pos.Y-s.grabY)*s.end()/span)
				s.offset.Jump(s.target)
			}
			u.Invalidate()
			return true
		}
		// A move only passes over the bar: a drag of the content, or
		// the widget's own, goes on under it.
		s.setOnBar(s.ClaimsPointer(e.Pos), u)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || !s.ClaimsPointer(e.Pos) {
			return false
		}
		s.flinging = false
		s.showBar()
		thumb := s.thumb(s.barWidth(u.Theme()))
		switch {
		case e.Pos.Y < thumb.Min.Y:
			s.ScrollTo(s.base()-s.viewport*0.9, Quick.Get(u.Theme()))
		case e.Pos.Y >= thumb.Max.Y:
			s.ScrollTo(s.base()+s.viewport*0.9, Quick.Get(u.Theme()))
		default:
			s.gripped = true
			s.grabY, s.grabAt = e.Pos.Y, s.clamp(s.offset.Value())
			s.target = s.grabAt
			s.offset.Jump(s.grabAt)
		}
		u.Invalidate()
		return true
	case input.PointerUp:
		if !s.gripped {
			return false
		}
		s.gripped = false
		s.setOnBar(s.over && s.ClaimsPointer(e.Pos), u)
		return true
	}
	return false
}

// handle takes the bar, the wheel, the keys, and, with DragScroll,
// drags.
func (s *scrolling) handle(e input.Event, u *gunim.UI) bool {
	if s.barEvent(e, u) {
		return true
	}
	th := u.Theme()
	var to float32
	switch e := e.(type) {
	case input.PointerDown:
		if !s.DragScroll || e.Button != input.ButtonPrimary {
			return false
		}
		s.held, s.flinging = true, false
		s.grabY, s.grabAt = e.Pos.Y, s.offset.Value()
		s.trails = 0
		s.track(e.Pos.Y, e.Time)
		s.offset.Jump(s.grabAt)
		s.showBar()
		return true
	case input.PointerMove:
		if !s.held {
			return false
		}
		s.track(e.Pos.Y, e.Time)
		s.offset.Jump(s.stretch(s.grabAt + s.grabY - e.Pos.Y))
		s.showBar()
		u.Invalidate()
		return true
	case input.PointerUp:
		if !s.held {
			return false
		}
		s.held = false
		s.release(th)
		u.Invalidate()
		return true
	case input.Scroll:
		to = s.base() - e.Delta.Y
	case input.KeyPress:
		switch e.Key {
		case input.KeyUp:
			to = s.base() - ScrollLine.Get(th)
		case input.KeyDown:
			to = s.base() + ScrollLine.Get(th)
		case input.KeyPageUp:
			to = s.base() - s.viewport*0.9
		case input.KeyPageDown:
			to = s.base() + s.viewport*0.9
		case input.KeyHome:
			to = 0
		case input.KeyEnd:
			to = s.content
		default:
			return false
		}
	default:
		return false
	}
	// At an end, the event is for whatever scrolls outside.
	if s.clamp(to) == s.base() {
		return false
	}
	s.ScrollTo(to, Quick.Get(th))
	u.Invalidate()
	return true
}

// edgeScroll scrolls for a drag held at p, in the widget's space, over
// a frame of dt, and returns how far the content moved. It scrolls as
// [gunim.EdgeSpeed] says.
func (s *scrolling) edgeScroll(p geom.Point, dt time.Duration, u *gunim.UI) geom.Point {
	if s.held || s.end() <= 0 {
		return geom.Point{}
	}
	v := gunim.EdgeSpeed(p.Y, 0, s.viewport)
	if v == 0 {
		return geom.Point{}
	}
	// A frame after a pause covers the pause; the scroll picks up from
	// where it was.
	secs := float32(min(dt, 50*time.Millisecond).Seconds())
	from := s.offset.Value()
	to := s.clamp(from + v*secs)
	if to == from {
		return geom.Point{}
	}
	s.flinging = false
	s.target = to
	s.offset.Jump(to)
	s.showBar()
	u.Invalidate()
	return geom.Pt(0, from-to)
}

// base is the offset the wheel and the keys count from: the target, or
// where a fling has got to.
func (s *scrolling) base() float32 {
	if s.flinging {
		return s.clamp(s.offset.Value())
	}
	return s.target
}

// track records where the pointer is, for the fling's speed.
func (s *scrolling) track(y float32, t time.Time) {
	if t.IsZero() {
		t = time.Now()
	}
	copy(s.trail[1:], s.trail[:len(s.trail)-1])
	s.trail[0] = sample{y, t}
	s.trails = min(s.trails+1, len(s.trail))
}

// speed returns how fast the pointer moved over the last tenth of a
// second, in pixels a second down the screen, or zero when it had come
// to rest before letting go.
func (s *scrolling) speed() float32 {
	if s.trails < 2 {
		return 0
	}
	last := s.trail[0]
	first := last
	for _, p := range s.trail[1:s.trails] {
		if last.t.Sub(p.t) > 100*time.Millisecond {
			break
		}
		first = p
	}
	dt := last.t.Sub(first.t).Seconds()
	if dt <= 0 || time.Since(last.t) > 100*time.Millisecond {
		return 0
	}
	return float32(float64(last.y-first.y) / dt)
}

// release ends a drag: flinging on at the pointer's speed, or springing
// back from past an end.
func (s *scrolling) release(th *theme.Live) {
	at := s.offset.Value()
	v := -s.speed()
	if at < 0 || at > s.end() || math.Abs(float64(v)) < 50 {
		s.target = s.clamp(at)
		s.offset.Retarget(s.target, Settle.Get(th))
		return
	}
	s.flinging = true
	anim.Fling(s.offset, v, flingDecay)
}

// shift moves the content by d along with everything aimed at it: the
// target, a drag's grip and a motion in flight. It is for content
// that moved under the view, such as rows measured above it, and
// leaves what is on screen where it is.
func (s *scrolling) shift(d float32) {
	s.target += d
	s.grabAt += d
	anim.Shift(s.offset, d)
}

// reveal scrolls just far enough to bring r, in the widget's own
// space, fully into view, with a little room around it.
func (s *scrolling) reveal(r geom.Rect, u *gunim.UI) {
	const room = 8
	offset := s.offset.Value()
	switch {
	case r.Min.Y < 0:
		s.ScrollTo(offset+r.Min.Y-room, Quick.Get(u.Theme()))
	case r.Max.Y > s.viewport:
		s.ScrollTo(offset+r.Max.Y-s.viewport+room, Quick.Get(u.Theme()))
	}
}

// revealContent is reveal for r in the content's space, measured from
// its top rather than from the top of the view.
func (s *scrolling) revealContent(r geom.Rect, u *gunim.UI) {
	off := s.offset.Value()
	s.reveal(geom.Rect{Min: geom.Pt(r.Min.X, r.Min.Y-off), Max: geom.Pt(r.Max.X, r.Max.Y-off)}, u)
}

// fit takes the content's height and the view's size from a layout,
// and brings the offset back inside when the content has shrunk under
// it.
func (s *scrolling) fit(content float32, view geom.Size, th *theme.Live) {
	s.th = th
	s.content, s.viewport, s.width = content, view.H, view.W
	if s.held || s.flinging {
		return
	}
	if t := s.clamp(s.target); t != s.target {
		s.target = t
		if s.jumped {
			s.offset.Jump(t)
		} else {
			s.offset.Animate(t, Settle.Get(th))
		}
	}
	s.jumped = false
}

// layer opens the layer the content draws in, over r: clipped to it, and fading toward its top and bottom edges where
// more content lies past them. It returns what closes the layer.
func (s *scrolling) layer(p *paint.Painter, r geom.Rect, th *theme.Live) func() {
	return p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Fade: s.fade(th)})
}

// fade is how far in from the top and the bottom edge the content fades: by the length of the first stretch of what
// lies past each edge, up to [ScrollFade].
func (s *scrolling) fade(th *theme.Live) geom.Insets {
	if s.NoFade || !s.scrollable() {
		return geom.Insets{}
	}
	at, fade := s.offset.Value(), ScrollFade.Get(th)
	return geom.Insets{Top: fadeFor(at, fade), Bottom: fadeFor(s.end()-at, fade)}
}

// fadeFor is how far in from an edge content fades with some of it lying past the edge: none with nothing past it,
// growing to fade as the first stretch of what lies past it comes by.
func fadeFor(past, fade float32) float32 { return max(0, min(past, fade)) }

// paintBar draws the bar, fading in and out, and stronger while the
// pointer is on it.
func (s *scrolling) paintBar(p *paint.Painter, f gunim.Frame) {
	t := s.bar.Value()
	if t <= 0 || !s.scrollable() {
		return
	}
	w := s.barWidth(f.Theme)
	col := ScrollbarColor.Get(f.Theme)
	strength := min(t, 1) * (1 + max(0, s.wide.Value()))
	col.A = uint8(min(255, float32(col.A)*strength))
	p.RRect(s.thumb(w), w/2, paint.Solid(col))
}
