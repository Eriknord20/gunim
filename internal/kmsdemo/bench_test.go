//go:build linux && (arm64 || amd64)

package kmsdemo

import (
	"testing"
	"time"
)

// goal returns the goal named name.
func goal(t *testing.T, r Result, name string) Goal {
	t.Helper()
	for _, g := range r.Goals() {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("no goal %q", name)
	return Goal{}
}

// A screen that kept drawing after the scene stopped fails the rest, even
// with no frame in the little rest left, and a quiet one passes.
func TestAScreenThatNeverWentStillFailsTheRest(t *testing.T) {
	busy := Result{Rate: 60, Still: false, StillAfter: 50 * time.Second, RestFor: time.Second}
	for _, name := range []string{"went still once stopped", "CPU at rest, screen on", "frames drawn at rest"} {
		if goal(t, busy, name).OK {
			t.Errorf("%s passed for a screen still drawing 50 s on", name)
		}
	}
	quiet := Result{Rate: 60, Still: true, StillAfter: 300 * time.Millisecond, RestFor: 58 * time.Second}
	for _, name := range []string{"went still once stopped", "CPU at rest, screen on", "frames drawn at rest"} {
		if !goal(t, quiet, name).OK {
			t.Errorf("%s failed for a screen that went still", name)
		}
	}
}
