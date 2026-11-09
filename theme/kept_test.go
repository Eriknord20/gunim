package theme

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/anim"
)

// unkept returns the colour key's slot in l works out afresh, with no
// value kept.
func unkept(t *testing.T, l *Live, key string) color.NRGBA {
	t.Helper()
	s, ok := l.slots[key].(*slot[color.NRGBA])
	if !ok {
		t.Fatalf("%q has no colour slot of its own", key)
	}
	return s.a.Value()
}

func TestAKeptColourMovesWithEveryFrameOfASwitch(t *testing.T) {
	dark := color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}
	light := color.NRGBA{R: 0xf0, G: 0xe0, B: 0xd0, A: 0xff}
	l := NewLive(Make("dark", Set(fill, dark)))
	if got := fill.Get(l); got != dark {
		t.Fatalf("setup: fill = %v, want %v", got, dark)
	}
	if fill.Get(l) != dark {
		t.Fatal("a second read at rest gives a different colour")
	}

	l.Use(Make("light", Set(fill, light)))
	changes := 0
	prev := fill.Get(l)
	for frame := range 300 {
		l.Step(time.Second / 60)
		got := fill.Get(l)
		if want := unkept(t, l, "test.fill"); got != want {
			t.Fatalf("frame %d of the switch: fill = %v, want the blend %v", frame, got, want)
		}
		if got != prev {
			changes++
		}
		prev = got
	}
	if changes < 10 {
		t.Fatalf("fill changed on %d frames of the switch, want it to move on many", changes)
	}
	if got := fill.Get(l); got != light {
		t.Fatalf("once settled, fill = %v, want %v", got, light)
	}
}

func TestAKeptColourTurnsMidSwitch(t *testing.T) {
	a := color.NRGBA{R: 0xff, A: 0xff}
	b := color.NRGBA{G: 0xff, A: 0xff}
	l := NewLive(Make("a", Set(fill, a)))
	fill.Get(l)
	l.Use(Make("b", Set(fill, b)))
	step(l, 5)
	mid := fill.Get(l)

	// Turning back keeps the frame's colour until the next step moves
	// it, then follows the new heading.
	l.Use(Make("a", Set(fill, a)))
	if got := fill.Get(l); got != mid {
		t.Fatalf("right after turning back, fill = %v, want where it was, %v", got, mid)
	}
	l.Step(time.Second / 60)
	if got, want := fill.Get(l), unkept(t, l, "test.fill"); got != want {
		t.Fatalf("a frame after turning back, fill = %v, want %v", got, want)
	}
}

func TestAKeptColourJumpsWithAnimationOff(t *testing.T) {
	anim.SetScale(0)
	t.Cleanup(func() { anim.SetScale(1) })
	light := color.NRGBA{R: 0xf0, G: 0xe0, B: 0xd0, A: 0xff}
	l := NewLive(Make("plain"))
	fill.Get(l)
	l.Use(Make("light", Set(fill, light)))
	if got := fill.Get(l); got != light {
		t.Fatalf("with animation off, fill = %v right after the switch, want %v", got, light)
	}
}

func TestAKeptColourInAScopeFollowsTheWindow(t *testing.T) {
	own := color.NRGBA{R: 0xff, G: 0x80, A: 0xff}
	window := NewLive(Make("plain"))
	scope := NewLive(Make("callout", Set(fill, own)))
	scope.Under(window)
	fill.Get(window)
	fill.Get(scope)

	// The scope lets go of fill while the window switches it, so the
	// scope's fill chases a target that moves every frame.
	window.Use(Make("blue", Set(fill, color.NRGBA{B: 0xff, A: 0xff})))
	scope.Use(Make("plain"))
	for frame := range 300 {
		window.Step(time.Second / 60)
		scope.Step(time.Second / 60)
		if _, own := scope.slots["test.fill"]; !own {
			if fill.Get(scope) != fill.Get(window) {
				t.Fatalf("frame %d: the scope reads through, yet its fill differs from the window's", frame)
			}
			return
		}
		if got, want := fill.Get(scope), unkept(t, scope, "test.fill"); got != want {
			t.Fatalf("frame %d: the scope's fill = %v, want %v", frame, got, want)
		}
	}
	t.Fatal("the scope's fill never caught up with the window's")
}
