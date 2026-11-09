//go:build !js

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A main thread that stops running its tasks has the watch write where
// each goroutine is, once, and one that runs them again is watched anew.
func TestAHungMainThreadSaysWhere(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	d := &Driver{posted: make(chan struct{}, 1)}
	stop := watchHang(d, 10*time.Millisecond, 50*time.Millisecond)
	defer stop()
	// Nothing runs the tasks: the main thread is held.
	deadline := time.Now().Add(5 * time.Second)
	var found []string
	for time.Now().Before(deadline) {
		found, _ = filepath.Glob(filepath.Join(tmp, "gunim-hang-*.txt"))
		if len(found) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(found) != 1 {
		t.Fatalf("the watch wrote %v", found)
	}
	raw, _ := os.ReadFile(found[0])
	if !strings.Contains(string(raw), "has not answered") || !strings.Contains(string(raw), "goroutine ") {
		t.Fatalf("the report reads %.200q", raw)
	}
	time.Sleep(200 * time.Millisecond)
	if again, _ := filepath.Glob(filepath.Join(tmp, "gunim-hang-*.txt")); len(again) != 1 {
		t.Fatalf("one hang was told %d times", len(again))
	}
}
