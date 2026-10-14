// Package anim animates values over time.
//
// The unit of animation is [Animated], a value that holds where it is
// now, where it is heading, and the [Motion] carrying it there. The
// package deals purely in elapsed time: you advance values with
// [Animated.Step], passing the real delta. That is what makes a 60 Hz
// display, a 144 Hz display and a dropped frame all produce the same
// motion.
//
// A widget usually owns several animated values and embeds a [Group] so
// the engine can step them in one call:
//
//	type Button struct {
//	    anim.Group
//	    hover *anim.Float
//	}
//
//	b.hover.Animate(1, anim.Snappy)
package anim

import (
	"slices"
	"time"
)

// MaxScalars is the most scalars a single [Animated] value can hold.
//
// Four covers every type worth animating as a unit: a float, a point, a
// size, a rectangle, a colour. Anything larger is really several
// independent animations and belongs in several values. So the limit
// buys an allocation-free fixed array for free.
const MaxScalars = 4

// A Codec takes a value apart into scalars and puts it back together.
//
// Springs need a position *and* a velocity per scalar, so values come
// apart into scalars first: a colour animates as four independent
// springs, and retargeting mid-flight stays smooth on every channel.
// Holding that logic in a Codec also lets Animated work on ordinary
// types like float32 and color.NRGBA, which carry no methods of their
// own.
type Codec[T any] struct {
	// N is how many scalars T uses, from 1 to MaxScalars.
	N int
	// Encode returns T's scalars in the first N slots.
	Encode func(v T) [MaxScalars]float32
	// Decode rebuilds a T from the first N slots of src.
	Decode func(src [MaxScalars]float32) T
}

// State is the live state of one animated scalar. Implementations of
// [Motion] read and write it; widgets work through [Animated] instead.
type State struct {
	// Position is the value right now.
	Position float32
	// Velocity is the rate of change, in units per second. Springs
	// carry it across a retarget, so an interrupted animation turns
	// around smoothly.
	Velocity float32
	// From is the position when the current animation started, and To
	// is where it is heading.
	From, To float32
	// Elapsed is how long the current animation has been running.
	Elapsed time.Duration
}

// A Motion advances one scalar toward its target and reports whether it
// has arrived. [Spring] and [Tween] implement it; so can you.
type Motion interface {
	Step(s *State, dt time.Duration) (settled bool)
}

// A Stepper is anything the engine can advance by a time delta. It
// reports whether it is still animating, which is how the engine knows
// a window can stop drawing and go back to sleep.
type Stepper interface {
	Step(dt time.Duration) (animating bool)
}

// Animated is a value of type T that moves toward a target over time.
//
// Build one with [New], or with a typed constructor such as
// [NewFloat].
type Animated[T any] struct {
	codec  Codec[T]
	state  [MaxScalars]State
	motion Motion
	active bool
	// real keeps the value out of the scale's reach; see
	// [Animated.RealTime].
	real bool
}

// New returns an Animated holding v, at rest.
func New[T any](v T, c Codec[T]) *Animated[T] {
	if c.N < 1 || c.N > MaxScalars {
		panic("anim: codec N out of range")
	}
	a := &Animated[T]{codec: c}
	a.Jump(v)
	return a
}

// Value returns where the animation is right now.
func (a *Animated[T]) Value() T {
	var buf [MaxScalars]float32
	for i := range a.codec.N {
		buf[i] = a.state[i].Position
	}
	return a.codec.Decode(buf)
}

// Target returns where the animation is heading. At rest it matches
// [Animated.Value].
func (a *Animated[T]) Target() T {
	var buf [MaxScalars]float32
	for i := range a.codec.N {
		buf[i] = a.state[i].To
	}
	return a.codec.Decode(buf)
}

// Active reports whether an animation is in flight.
func (a *Animated[T]) Active() bool { return a.active }

// Animate starts moving toward to under m.
//
// Animate returns at once when to is already the target, leaving an
// animation heading there to carry on under its original motion. That
// makes it safe to call every frame, which is how [gunim.Transitioner]
// works: a node can say "I am leaving, fade out" on each frame of its
// exit and the fade runs once, start to finish. Use [Animated.Retarget]
// to change the motion of something already in flight.
func (a *Animated[T]) Animate(to T, m Motion) {
	buf := a.codec.Encode(to)
	if a.heading(&buf) {
		return
	}
	a.startMotion(&buf, m)
}

// Retarget is Animate, and it always adopts m, even mid-flight.
// Velocity carries over, so the change of motion stays invisible.
func (a *Animated[T]) Retarget(to T, m Motion) {
	buf := a.codec.Encode(to)
	a.startMotion(&buf, m)
}

// Jump moves to v at once, cancelling anything in flight. Use it to set
// an initial value. To end an animation early, prefer animating to
// where you want it: cutting motion dead is the jarring thing this
// package exists to avoid.
func (a *Animated[T]) Jump(v T) {
	buf := a.codec.Encode(v)
	for i := range a.codec.N {
		a.state[i] = State{Position: buf[i], From: buf[i], To: buf[i]}
	}
	a.active = false
	a.motion = nil
}

// Shift moves a number by d, along with where its animation started
// and where it is heading, so a motion in flight carries on unchanged
// from its new place. It is for a value measured from a point that
// moved, such as a row's place in a list whose scroll offset jumped.
func Shift(a *Float, d float32) {
	s := &a.state[0]
	s.Position += d
	s.From += d
	s.To += d
}

// Step advances the animation by dt and reports whether it is still
// running. The engine calls this once per frame.
func (a *Animated[T]) Step(dt time.Duration) bool {
	if !a.active {
		return false
	}
	if a.scaled(a.motion) {
		if s := Scale(); s == 0 {
			// Animation was turned off on the way: land now.
			a.land()
			return false
		} else if s != 1 {
			// A long frame counts as maxStep at most, as in a motion's
			// own step. At a scale under 1 that is more motion than one
			// step may take, so it goes in steps of maxStep.
			dt = scaledStep(min(dt, maxStep), s)
			for dt > maxStep && a.stepBy(maxStep) {
				dt -= maxStep
			}
			if !a.active {
				return false
			}
		}
	}
	return a.stepBy(dt)
}

// stepBy moves every scalar by dt of its motion, and reports whether
// any is still moving.
func (a *Animated[T]) stepBy(dt time.Duration) bool {
	settled := true
	for i := range a.codec.N {
		if !a.motion.Step(&a.state[i], dt) {
			settled = false
		}
	}
	if settled {
		a.active = false
		a.motion = nil
	}
	return a.active
}

// land puts the value where it is going, at rest.
func (a *Animated[T]) land() {
	for i := range a.codec.N {
		to := a.state[i].To
		a.state[i] = State{Position: to, From: to, To: to}
	}
	a.active, a.motion = false, nil
}

// heading reports whether the animation is already on its way to buf,
// or already sitting there.
func (a *Animated[T]) heading(buf *[MaxScalars]float32) bool {
	for i := range a.codec.N {
		if a.state[i].To != buf[i] {
			return false
		}
	}
	// The target matches, so either we are on our way there or we have
	// arrived.
	return true
}

func (a *Animated[T]) startMotion(buf *[MaxScalars]float32, m Motion) {
	// An instant motion lands now, so the frame that asks for it shows
	// the value where it is going.
	if instant(m) || a.scaled(m) && Scale() == 0 {
		for i := range a.codec.N {
			a.state[i] = State{Position: buf[i], From: buf[i], To: buf[i]}
		}
		a.active, a.motion = false, nil
		return
	}
	moving := false
	for i := range a.codec.N {
		s := &a.state[i]
		s.From = s.Position
		s.To = buf[i]
		s.Elapsed = 0
		if s.From != s.To {
			moving = true
		}
	}
	a.motion = m
	a.active = moving
	if !moving {
		a.motion = nil
	}
}

// instant reports whether m takes no time: a spring of no response, or
// a tween of no duration.
func instant(m Motion) bool {
	switch m := m.(type) {
	case Spring:
		return m.Response <= 0
	case Tween:
		return m.Duration <= 0
	default:
		return false
	}
}

// Group collects animated values so a widget can step them all at once.
// Embed it in a widget and register each value in the constructor.
type Group struct {
	vs []Stepper
}

// Add registers values with the group.
func (g *Group) Add(vs ...Stepper) { g.vs = append(g.vs, vs...) }

// Remove takes values out of the group, for a widget whose parts come
// and go, as the rows of a list.
func (g *Group) Remove(vs ...Stepper) {
	g.vs = slices.DeleteFunc(g.vs, func(v Stepper) bool { return slices.Contains(vs, v) })
}

// Step advances every registered value and reports whether any of them
// is still animating.
func (g *Group) Step(dt time.Duration) bool {
	animating := false
	for _, v := range g.vs {
		if v.Step(dt) {
			animating = true
		}
	}
	return animating
}
