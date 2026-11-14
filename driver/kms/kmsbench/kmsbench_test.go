//go:build linux && (arm64 || amd64)

package kmsbench

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A scene with no way to open its window is refused before the screen is opened.
func TestASceneWithNoOpenIsRefused(t *testing.T) {
	if _, err := Bench(context.Background(), Options{Scene: Scene{Name: "status"}}); err == nil {
		t.Fatal("a scene with no Open ran")
	}
}

// Report counts the goals missed, and says so last.
func TestReportCountsTheGoalsMissed(t *testing.T) {
	var b strings.Builder
	busy := Result{Rate: 60, StillAfter: 50 * time.Second, RestFor: time.Second}
	missed, err := Report(&b, busy)
	if err != nil {
		t.Fatal(err)
	}
	if missed == 0 || !strings.Contains(b.String(), "MISSED") || !strings.HasSuffix(b.String(), "goals missed\n") {
		t.Fatalf("a screen still drawing 50 s on reports %d missed:\n%s", missed, b.String())
	}
}

// The default script starts with the replay's header, as the driver reads it.
func TestDragScriptHasItsHeader(t *testing.T) {
	if s := DragScript(90, time.Second); !strings.HasPrefix(string(s), "gunim-evdev 1 ") {
		t.Fatalf("the script starts %q", s[:min(len(s), 20)])
	}
}

// A program that brings its own scene links none of gunim's: not the demo, and not the gallery
// of every widget.
func TestTheBenchLinksNoSceneOfItsOwn(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "github.com/marrasen/gunim/driver/kms/kmsbench")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasSuffix(p, "/internal/gallery") || strings.HasSuffix(p, "/internal/kmsdemo") {
			t.Errorf("kmsbench links %s", p)
		}
	}
}

// The start goal counts from the program's start where Options.Started gives it, and from the
// bench's start where it doesn't.
func TestTheStartCountsFromTheProgramsStart(t *testing.T) {
	began, now := time.Now().Add(-2*time.Second), time.Now()
	if got := (&Options{Started: began}).startedAt(now); !got.Equal(began) {
		t.Errorf("with the program's start, the goal counts from %v", got)
	}
	if got := (&Options{}).startedAt(now); !got.Equal(now) {
		t.Errorf("without it, the goal counts from %v", got)
	}
}
