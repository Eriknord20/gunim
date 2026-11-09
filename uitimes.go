package gunim

import (
	"log"
	"os"
	"time"
)

// uiFramesDebug is set by GUNIM_DEBUG_FRAMES, which also logs, each
// second, how long each window took to build its frames: applying the
// application's commands, and laying out and painting; and how long
// after it was built, and after its due time, a frame lights the screen.
var uiFramesDebug = os.Getenv("GUNIM_DEBUG_FRAMES") != ""

// uiTimes sums a window's frame building for uiFramesDebug.
type uiTimes struct {
	from              time.Time
	n                 int
	apply, build      time.Duration
	applyMax, buildMx time.Duration
	// ahead sums how long after it was built each frame lights the
	// screen.
	ahead time.Duration
}

// add counts a frame that took apply to apply commands and build to lay
// out and paint, and lights the screen ahead after it was built, and
// logs the second's sums once one has passed.
func (t *uiTimes) add(w *Window, apply, build, ahead time.Duration) {
	now := time.Now()
	if t.from.IsZero() {
		t.from = now
	}
	t.n++
	t.apply += apply
	t.build += build
	t.ahead += ahead
	t.applyMax = max(t.applyMax, apply)
	t.buildMx = max(t.buildMx, build)
	if now.Sub(t.from) < time.Second {
		return
	}
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	n := time.Duration(t.n)
	log.Printf("gunim ui %p %q: %d frames built; commands %.1f ms (max %.1f), layout and paint %.1f ms (max %.1f); "+
		"on screen %.1f ms after built, %.1f ms after due",
		w, w.title, t.n, ms(t.apply/n), ms(t.applyMax), ms(t.build/n), ms(t.buildMx), ms(t.ahead/n), ms(w.late))
	*t = uiTimes{from: now}
}
