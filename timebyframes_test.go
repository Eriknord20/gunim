package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/input"
)

func TestAWindowOnFrameTimeReadsTheTimeItsFramesStepped(t *testing.T) {
	w := newTestWindow()
	w.TimeByFrames(true)
	start := w.ui.Clock()
	if start.IsZero() {
		t.Fatal("the clock reads no time before the first frame")
	}
	// The wall clock moves on; the window's time does not.
	time.Sleep(20 * time.Millisecond)
	if got := w.ui.Clock(); !got.Equal(start) {
		t.Fatalf("with no frames, the clock moved %v", got.Sub(start))
	}
	run(w, 30)
	if got, want := w.ui.Clock().Sub(start), 30*(time.Second/60); got != want {
		t.Fatalf("30 frames of a sixtieth moved the clock %v, want %v", got, want)
	}
	w.TimeByFrames(false)
	if got := w.ui.Clock(); time.Since(got) > time.Second || time.Until(got) > time.Second {
		t.Fatalf("off frame time, the clock reads %v, want the wall clock's time", got)
	}
}

func TestAWindowOnFrameTimeRunsATimerSetBeforeItsFirstFrameAfterItsTime(t *testing.T) {
	w := newTestWindow()
	w.TimeByFrames(true)
	ran := 0
	w.ui.After(6*(time.Second/60), func(*UI) { ran++ })
	run(w, 5)
	if ran != 0 {
		t.Fatal("a timer of 6 frames' time ran within 5 frames")
	}
	run(w, 1)
	if ran != 1 {
		t.Fatalf("the timer ran %d times by its sixth frame, want once", ran)
	}
}

func TestAWindowOnFrameTimeStampsItsOwnEventsWithTheFramesTime(t *testing.T) {
	w := newTestWindow()
	w.TimeByFrames(true)
	p := &focusPair{fa: &focusRecorder{}, fb: &focusRecorder{}}
	w.ui.Insert(w.ui.Root(), p)
	run(w, 1)
	w.ui.Focus(p.fb)
	run(w, 9)
	now := w.ui.Now()
	time.Sleep(20 * time.Millisecond)
	w.ui.handlePlatform(driver.WindowFocus{Focused: false})
	for _, e := range p.fb.events {
		if l, ok := e.(input.WindowFocusLost); ok {
			if !l.Time.Equal(now) {
				t.Fatalf("the window losing the keyboard came at %v, want the frame's time, %v", l.Time, now)
			}
			return
		}
	}
	t.Fatal("the focused node never heard the window lose the keyboard")
}
