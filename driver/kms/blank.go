//go:build linux && (arm64 || amd64)

package kms

import (
	"math"
	"time"
)

// Blank is when an untouched screen dims, and when it turns off. The zero
// value never does either. Hjortron's console dims after a minute and
// turns off after five.
type Blank struct {
	// Dim is how long the screen stays at full brightness untouched before
	// it fades, over a second, to dimLevel of it. Zero never dims.
	Dim time.Duration
	// Off is how long the screen stays on untouched before it fades to
	// black over 600 ms and turns off: the backlight at 0, and the display
	// controller off, so no frames are drawn. Zero never turns it off.
	Off time.Duration
}

// The fades' lengths, and how far a dim screen dims.
const (
	dimFade   = time.Second
	offFade   = 600 * time.Millisecond
	wakeFade  = 300 * time.Millisecond
	levelFade = 250 * time.Millisecond
	// dimLevel is the share of the brightness a dim screen keeps.
	dimLevel = 0.3
)

// screenState is where the screen is in blanking.
type screenState uint8

const (
	// screenOn is lit at the app's brightness, or fading to it.
	screenOn screenState = iota
	// screenDim is dimmed, or dimming, after Blank.Dim untouched.
	screenDim
	// screenFading is fading to black, after Blank.Off untouched.
	screenFading
	// screenOff is dark: backlight 0 and the display controller off.
	screenOff
)

func (s screenState) String() string {
	return [...]string{"on", "dim", "fading", "off"}[s]
}

// fade is the brightness moving from one level to another.
type fade struct {
	from, to float32
	start    time.Time
	dur      time.Duration
}

// at returns the brightness at now, eased in and out.
func (f fade) at(now time.Time) float32 {
	if f.dur <= 0 || !now.Before(f.start.Add(f.dur)) {
		return f.to
	}
	x := float32(now.Sub(f.start)) / float32(f.dur)
	if x < 0 {
		return f.from
	}
	x = x * x * (3 - 2*x)
	return f.from + (f.to-f.from)*x
}

// moving reports whether the brightness still changes after now.
func (f fade) moving(now time.Time) bool { return f.dur > 0 && now.Before(f.start.Add(f.dur)) }

// blanker is the screen's brightness and blanking, as a state machine on
// the times it is handed, so a test drives it with a fake clock. The
// driver keeps it under its mu: touches reach it from the input readers,
// and the render thread steps it and draws what it says.
type blanker struct {
	o Blank
	// level is the app's brightness, 0 to 1: SetBrightness's, or the
	// backlight's as found.
	level float32
	// last is when the screen was last touched or woken.
	last time.Time
	// keep says a window keeps the screen awake, and fingers that a
	// finger is down: neither dims while it lasts.
	keep, fingers bool
	state         screenState
	fade          fade
}

func newBlanker(o Blank, level float32, now time.Time) *blanker {
	return &blanker{o: o, level: level, last: now, fade: fade{from: level, to: level}}
}

// bright returns the brightness the screen shows at now, 0 to 1.
func (b *blanker) bright(now time.Time) float32 { return b.fade.at(now) }

// to fades from the brightness now to v over d.
func (b *blanker) to(v float32, d time.Duration, now time.Time) {
	b.fade = fade{from: b.fade.at(now), to: v, start: now, dur: d}
}

// setLevel sets the app's brightness, and fades to it where the screen is
// on, or to its dim share where it is dim.
func (b *blanker) setLevel(v float32, now time.Time) {
	b.level = v
	switch b.state {
	case screenOn:
		b.to(v, levelFade, now)
	case screenDim:
		b.to(v*dimLevel, levelFade, now)
	case screenFading, screenOff:
		// It takes the new level as it wakes.
	}
}

// touched takes a touch at now. A touch on a screen that is not fully on
// wakes it, and is to be swallowed, so a tap on a dark screen never
// presses what lies under it; it reports that.
func (b *blanker) touched(now time.Time) (swallow bool) {
	b.last = now
	if b.state == screenOn {
		return false
	}
	b.wake(now)
	return true
}

// wake turns the screen on, from dim, fading or off, and fades up to the
// app's brightness.
func (b *blanker) wake(now time.Time) {
	b.last = now
	if b.state == screenOn {
		return
	}
	if b.state == screenOff {
		b.fade = fade{from: 0, to: 0}
	}
	b.state = screenOn
	b.to(b.level, wakeFade, now)
}

// setKeep holds the screen awake, or lets it blank again from now on.
func (b *blanker) setKeep(on bool, now time.Time) {
	if on == b.keep {
		return
	}
	b.keep = on
	b.last = now
	if on {
		b.wake(now)
	}
}

// step moves the state on to now: it dims after Dim untouched, fades after
// Off, and turns off once the fade has ended. It reports whether the screen
// turned off just now, for the render thread to turn the display off.
func (b *blanker) step(now time.Time) (off bool) {
	if b.keep || b.fingers {
		return false
	}
	idle := now.Sub(b.last)
	switch b.state {
	case screenOn:
		if b.o.Off > 0 && idle >= b.o.Off {
			b.state = screenFading
			b.to(0, offFade, now)
		} else if b.o.Dim > 0 && idle >= b.o.Dim && (b.o.Off <= 0 || b.o.Dim < b.o.Off) {
			b.state = screenDim
			b.to(b.level*dimLevel, dimFade, now)
		}
	case screenDim:
		if b.o.Off > 0 && idle >= b.o.Off {
			b.state = screenFading
			b.to(0, offFade, now)
		}
	case screenFading, screenOff:
	}
	if b.state == screenFading && !b.fade.moving(now) {
		b.state = screenOff
		return true
	}
	return false
}

// next returns when step has something to do after now, or the zero time
// for nothing.
func (b *blanker) next(now time.Time) time.Time {
	if b.keep || b.fingers {
		return time.Time{}
	}
	var at time.Time
	soonest := func(t time.Time) {
		if at.IsZero() || t.Before(at) {
			at = t
		}
	}
	switch b.state {
	case screenOn:
		if b.o.Dim > 0 {
			soonest(b.last.Add(b.o.Dim))
		}
		if b.o.Off > 0 {
			soonest(b.last.Add(b.o.Off))
		}
	case screenDim:
		if b.o.Off > 0 {
			soonest(b.last.Add(b.o.Off))
		}
	case screenFading:
		soonest(b.fade.start.Add(b.fade.dur))
	case screenOff:
	}
	if b.fade.moving(now) {
		// Frames carry a fade, each set off by the flip before. One the
		// fade has yet to change waits this long for the next look.
		soonest(now.Add(fadeTick))
	}
	return at
}

// fadeTick is how often the render thread looks at a fade between frames.
const fadeTick = 8 * time.Millisecond

// lightFor works out how to show brightness v, 0 to 1, on a backlight of
// steps: the nearest step at or above v, and the share of black over
// the picture that makes up the rest, so a fade is smooth though the
// backlight has few steps. The picture's values are gamma-encoded, so the
// black share is worked out in light, not in values. With no backlight,
// steps 0, the black share does it all.
func lightFor(v float32, steps int) (level int, shade float32) {
	v = min(max(v, 0), 1)
	if steps <= 0 {
		return 0, 1 - float32(math.Pow(float64(v), 1/2.2))
	}
	level = int(math.Ceil(float64(v)*float64(steps) - 1e-6))
	if level <= 0 {
		return 0, 1
	}
	f := float64(v) * float64(steps) / float64(level)
	return level, float32(1 - math.Pow(min(f, 1), 1/2.2))
}
