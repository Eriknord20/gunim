package anim

import (
	"math"
	"time"
)

// maxStep caps how much time a single Step may integrate.
//
// A window that was hidden, or a machine that went to sleep, hands back
// a delta of seconds. The cap keeps a spring to a sane number of
// substeps and keeps the integration stable.
const maxStep = 100 * time.Millisecond

// Spring moves a value the way a damped weight on a spring moves.
//
// Prefer it for anything the pointer drives. A spring carries its
// velocity across a retarget, so a hover that reverses halfway turns
// around smoothly. Use [Tween] for scripted motion where the exact
// duration matters.
type Spring struct {
	// Response is roughly how long the value takes to arrive, in
	// seconds. Smaller is faster. Zero is instant: [Animated.Animate]
	// puts the value where it is going at once, as Jump does, for a
	// theme that turns a motion off.
	Response float32
	// Damping is the damping ratio. 1 settles straight onto the target,
	// below 1 bounces, above 1 is sluggish.
	Damping float32
}

// Stock springs. Treat them as starting points and retune freely.
var (
	// Snappy is the default for pointer feedback: hover, press, focus.
	Snappy = Spring{Response: 0.25, Damping: 0.85}
	// Gentle settles straight onto the target. Good for things leaving.
	Gentle = Spring{Response: 0.45, Damping: 1.0}
	// Bouncy overshoots visibly. Good for things arriving.
	Bouncy = Spring{Response: 0.40, Damping: 0.58}
)

// Step implements [Motion].
func (sp Spring) Step(s *State, dt time.Duration) bool {
	resp := sp.Response
	if resp <= 0 {
		resp = 0.001
	}
	omega := float32(2*math.Pi) / resp
	zeta := sp.Damping
	if zeta < 0 {
		zeta = 0
	}

	if dt > maxStep {
		dt = maxStep
	}
	s.Elapsed += dt

	// The exact motion of a damped spring over dt, so any stiffness and
	// any step are stable and cost the same.
	x, v := springStep(float64(s.Position-s.To), float64(s.Velocity), float64(omega), float64(zeta), dt.Seconds())
	s.Position = s.To + float32(x)
	s.Velocity = float32(v)

	// Settle relative to the distance travelled, so a spring over 400
	// pixels and one over 0..1 both stop at the point they look still.
	scale := abs32(s.To - s.From)
	if scale < 1 {
		scale = 1
	}
	if abs32(s.Position-s.To) < 0.002*scale && abs32(s.Velocity) < 0.02*scale {
		s.Position = s.To
		s.Velocity = 0
		return true
	}
	return false
}

// Follow moves x, with velocity v, toward to over dt, for a value too
// large for an [Animated] float32 to hold to the pixel, such as a
// position far down a long list.
func (sp Spring) Follow(x, v, to float64, dt time.Duration) (at, speed float64) {
	resp := float64(max(sp.Response, 0.001))
	zeta := float64(max(sp.Damping, 0))
	dt = min(dt, maxStep)
	x, v = springStep(x-to, v, 2*math.Pi/resp, zeta, dt.Seconds())
	return to + x, v
}

// FollowScaled is Follow for something drawn moving, as a selection
// sliding or a view gliding, under the scale [SetScale] sets: stretched
// or shrunk with it, and at 0 landing at once. Follow itself keeps real
// time, for a value that measures something.
func (sp Spring) FollowScaled(x, v, to float64, dt time.Duration) (at, speed float64) {
	s := Scale()
	if s == 0 {
		return to, 0
	}
	if s == 1 {
		return sp.Follow(x, v, to, dt)
	}
	// As Animated does: a long frame counts as maxStep at most, in steps
	// no longer than that.
	dt = scaledStep(min(dt, maxStep), s)
	for dt > maxStep {
		x, v = sp.Follow(x, v, to, maxStep)
		dt -= maxStep
	}
	return sp.Follow(x, v, to, dt)
}

// An Ease reshapes linear progress in 0..1 into eased progress.
type Ease func(t float32) float32

// Tween moves a value over a fixed duration along an easing curve.
//
// Reach for it when the duration is the point: a scripted intro, motion
// that has to line up with something else. For anything interruptible,
// use [Spring] instead.
type Tween struct {
	Duration time.Duration
	// Ease shapes the curve. A nil Ease means [EaseOut].
	Ease Ease
}

// Step implements [Motion].
func (tw Tween) Step(s *State, dt time.Duration) bool {
	if dt > maxStep {
		dt = maxStep
	}
	s.Elapsed += dt
	if tw.Duration <= 0 {
		s.Position, s.Velocity = s.To, 0
		return true
	}
	p := float32(s.Elapsed) / float32(tw.Duration)
	if p >= 1 {
		s.Position, s.Velocity = s.To, 0
		return true
	}
	ease := tw.Ease
	if ease == nil {
		ease = EaseOut
	}
	prev := s.Position
	s.Position = s.From + (s.To-s.From)*ease(p)
	// Keep velocity current so a spring taking over mid-tween inherits
	// the motion.
	if secs := float32(dt.Seconds()); secs > 0 {
		s.Velocity = (s.Position - prev) / secs
	}
	return false
}

// Easing curves.
var (
	Linear Ease = func(t float32) float32 { return t }

	// EaseOut starts fast and slows into place. It is the right default
	// for interface motion: the result shows up immediately.
	EaseOut Ease = func(t float32) float32 {
		u := 1 - t
		return 1 - u*u*u
	}

	// EaseInOut accelerates and decelerates. Use it for something
	// travelling a long way across the screen.
	EaseInOut Ease = func(t float32) float32 {
		if t < 0.5 {
			return 4 * t * t * t
		}
		u := -2*t + 2
		return 1 - u*u*u/2
	}
)

// springStep advances a damped spring, x” = -ω²x - 2ζωx', by t seconds
// from displacement x and velocity v, using the closed-form solution for
// each damping regime.
func springStep(x, v, omega, zeta, t float64) (xt, vt float64) {
	switch {
	case math.Abs(zeta-1) < 1e-6:
		// Critically damped: x(t) = (a + bt)e^(-ωt).
		b := v + omega*x
		e := math.Exp(-omega * t)
		return (x + b*t) * e, (b - omega*(x+b*t)) * e
	case zeta < 1:
		// Underdamped: a decaying oscillation at the damped frequency.
		wd := omega * math.Sqrt(1-zeta*zeta)
		e := math.Exp(-zeta * omega * t)
		c, sn := math.Cos(wd*t), math.Sin(wd*t)
		return e * (x*c + (v+zeta*omega*x)/wd*sn),
			e * (v*c - (omega*omega*x+zeta*omega*v)/wd*sn)
	default:
		// Overdamped: the sum of two decaying exponentials.
		root := omega * math.Sqrt(zeta*zeta-1)
		r1, r2 := -zeta*omega+root, -zeta*omega-root
		c1 := (v - r2*x) / (r1 - r2)
		c2 := x - c1
		e1, e2 := math.Exp(r1*t), math.Exp(r2*t)
		return c1*e1 + c2*e2, c1*r1*e1 + c2*r2*e2
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Decay coasts a value on its velocity, which falls away exponentially,
// the way content flung by a finger slows to a stop. It ignores the
// target: start it with [Fling], which aims the target at where the
// value will come to rest.
type Decay struct {
	// Tau is how long, in seconds, the velocity takes to fall to about
	// a third of itself. The value travels velocity times Tau in all.
	Tau float32
}

// Step implements [Motion].
func (d Decay) Step(s *State, dt time.Duration) bool {
	dt = min(dt, maxStep)
	tau := float64(max(d.Tau, 0.01))
	e := math.Exp(-dt.Seconds() / tau)
	s.Position += float32(float64(s.Velocity) * tau * (1 - e))
	s.Velocity *= float32(e)
	s.Elapsed += dt
	if abs32(s.Velocity) < 2 {
		s.Velocity = 0
		s.To = s.Position
		return true
	}
	return false
}

// Fling sets a moving at velocity v, in units a second, coasting to a
// stop under d. A spring started on a afterwards carries the velocity
// on, which is how content flung past its end bounces back.
func Fling(a *Float, v float32, d Decay) {
	s := &a.state[0]
	s.Velocity = v
	rest := s.Position + v*max(d.Tau, 0.01)
	a.Retarget(rest, d)
}
