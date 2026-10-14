package anim

import (
	"math"
	"sync/atomic"
	"time"
)

// scale is how long every animation takes, against the time its motion
// says: the bits of the float64, flipped against those of 1, so the
// zero value is 1.
var scale atomic.Uint64

// one is the bits of 1.
var one = math.Float64bits(1)

// SetScale sets how long every animation takes, against the time its
// motion says: 1 is as made, 2 twice as long, 0.5 half as long, and 0
// turns animation off, so a value lands where it is going at once. It
// is the whole program's, for a user who wants motion slower, quicker
// or gone; gunim sets it from the theme a window takes.
//
// It leaves a [Decay] alone, as a fling follows the finger that threw
// it, and a value marked by [Animated.RealTime], such as a fade in
// sound. It is safe to call from any goroutine.
func SetScale(s float64) {
	if math.IsNaN(s) || s < 0 {
		s = 0
	}
	scale.Store(math.Float64bits(s) ^ one)
}

// Scale returns the scale [SetScale] set, or 1.
func Scale() float64 { return math.Float64frombits(scale.Load() ^ one) }

// RealTime marks a as keeping real time: [SetScale] never stretches its
// motions or cuts them short, as it must not for a fade in sound, or a
// value that measures something rather than moving to show it.
func (a *Animated[T]) RealTime() { a.real = true }

// scaled reports whether m stretches with the scale: every motion but a
// fling's, on a value not marked [Animated.RealTime].
func (a *Animated[T]) scaled(m Motion) bool {
	if a.real {
		return false
	}
	_, decay := m.(Decay)
	return !decay
}

// scaledStep is how far dt moves a motion under the scale s.
func scaledStep(dt time.Duration, s float64) time.Duration {
	return time.Duration(float64(dt) / s)
}
