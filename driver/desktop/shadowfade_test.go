package desktop

import "testing"

// A window fading for many frames before the main thread gets round to its shadow leaves one task to show it, at the
// newest fade, and not one a frame for the main thread to work through after the window has gone.
func TestAFadingShadowQueuesOneTaskAtATime(t *testing.T) {
	d := &Driver{posted: make(chan struct{}, 1)}
	// Covered, so the task shows nothing: there is no system window here to show it on.
	w := &Window{d: d, shadow: true, shadowAt: 1, shadowScale: 1, covered: true}
	for i := range 30 {
		k := float32(i+1) / 30
		w.SetFade(1-k, 1-0.06*k)
	}
	if n := len(d.tasks); n != 1 {
		t.Fatalf("30 frames of a fade left %d tasks for the main thread, want 1", n)
	}
	if w.shadowAt != 0 || w.shadowScale != 1-0.06 {
		t.Fatalf("the shadow waits to show at %v, scale %v, want the last frame's 0, %v", w.shadowAt, w.shadowScale, 1-0.06)
	}
	d.runTasks()
	if w.shadowOwed {
		t.Fatal("the shadow is still owed once its task has run")
	}
	w.SetFade(0.5, 0.97)
	if n := len(d.tasks); n != 1 {
		t.Fatalf("a fade after the task ran left %d tasks, want 1", n)
	}
}
