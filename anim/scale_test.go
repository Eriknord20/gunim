package anim

import (
	"math"
	"testing"
	"time"
)

// scaledTo sets the scale to s until the test ends.
func scaledTo(t *testing.T, s float64) {
	t.Helper()
	SetScale(s)
	t.Cleanup(func() { SetScale(1) })
}

// framesTo counts the 60 Hz frames a takes to settle.
func framesTo(a *Float) int {
	n := 0
	for a.Step(time.Second/60) && n < 10000 {
		n++
	}
	return n
}

func TestTheScaleStretchesAndShrinksEveryMotion(t *testing.T) {
	tween := Tween{Duration: 300 * time.Millisecond}
	for _, c := range []struct {
		scale float64
		want  int
	}{{1, 17}, {2, 35}, {0.5, 8}, {5, 89}} {
		scaledTo(t, c.scale)
		a := NewFloat(0)
		a.Animate(1, tween)
		if got := framesTo(a); got < c.want-1 || got > c.want+1 {
			t.Errorf("at %v a 300 ms tween took %d frames, want about %d", c.scale, got, c.want)
		}
		if a.Value() != 1 {
			t.Errorf("at %v it ended at %v", c.scale, a.Value())
		}
	}
}

func TestAScaleOfNoughtLandsAtOnce(t *testing.T) {
	scaledTo(t, 0)
	a := NewFloat(0)
	a.Animate(1, Spring{Response: 0.5, Damping: 1})
	if a.Active() || a.Value() != 1 {
		t.Fatalf("with animation off it is at %v, moving %v", a.Value(), a.Active())
	}
}

func TestTurningAnimationOffLandsWhatIsMoving(t *testing.T) {
	a := NewFloat(0)
	a.Animate(1, Spring{Response: 0.5, Damping: 1})
	a.Step(time.Second / 60)
	scaledTo(t, 0)
	if a.Step(time.Second/60) || a.Value() != 1 {
		t.Fatalf("it is at %v, moving %v", a.Value(), a.Active())
	}
}

func TestAQuickScaleStepsALongFrameWhole(t *testing.T) {
	// A tenth: a 100 ms frame is a second of motion, past the longest
	// step a motion takes at once, and must all be taken.
	scaledTo(t, 0.1)
	a := NewFloat(0)
	a.Animate(1, Tween{Duration: time.Second})
	if a.Step(100*time.Millisecond) || a.Value() != 1 {
		t.Fatalf("after a 100 ms frame it is at %v, moving %v", a.Value(), a.Active())
	}
}

func TestRealTimeAndFlingsKeepRealTime(t *testing.T) {
	scaledTo(t, 0)
	a := NewFloat(0)
	a.RealTime()
	a.Animate(1, Tween{Duration: 300 * time.Millisecond})
	if !a.Active() {
		t.Fatal("a value in real time landed at once")
	}
	if got := framesTo(a); got < 16 || got > 18 {
		t.Fatalf("it took %d frames, want about 17", got)
	}
	f := NewFloat(0)
	Fling(f, 1000, Decay{Tau: 0.3})
	if !f.Active() {
		t.Fatal("a fling landed at once")
	}
}

func TestScaleReadsBackAndRefusesLessThanNought(t *testing.T) {
	scaledTo(t, 2.5)
	if Scale() != 2.5 {
		t.Fatalf("Scale is %v", Scale())
	}
	SetScale(-1)
	if Scale() != 0 {
		t.Fatalf("Scale is %v after -1", Scale())
	}
	SetScale(1)
	if Scale() != 1 {
		t.Fatalf("Scale is %v after 1", Scale())
	}
}

// FollowScaled stretches and shrinks with the scale, and lands at once at
// 0; Follow keeps real time whatever the scale.
func TestFollowScaledGoesWithTheScale(t *testing.T) {
	sp := Spring{Response: 0.3, Damping: 1}
	const frame = time.Second / 60
	frames := func(f func(x, v, to float64, dt time.Duration) (float64, float64)) int {
		x, v := 0.0, 0.0
		for n := 1; n < 2000; n++ {
			x, v = f(x, v, 100, frame)
			if math.Abs(x-100) < 0.01 && math.Abs(v) < 0.1 {
				return n
			}
		}
		t.Fatal("it never landed")
		return 0
	}
	normal := frames(sp.FollowScaled)
	scaledTo(t, 2)
	if slow := frames(sp.FollowScaled); slow < normal*2-3 || slow > normal*2+3 {
		t.Errorf("at scale 2 it took %d frames, from %d", slow, normal)
	}
	if kept := frames(sp.Follow); kept != normal {
		t.Errorf("Follow at scale 2 took %d frames, from %d", kept, normal)
	}
	scaledTo(t, 0.25)
	if quick := frames(sp.FollowScaled); quick > normal/4+2 {
		t.Errorf("at scale 0.25 it took %d frames, from %d", quick, normal)
	}
	scaledTo(t, 0)
	if x, v := sp.FollowScaled(0, 5, 100, frame); x != 100 || v != 0 {
		t.Errorf("at scale 0 it is at %v, moving %v", x, v)
	}
}
