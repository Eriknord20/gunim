//go:build kmspi && linux && (arm64 || amd64)

// The run on a real screen, as on the trial Raspberry Pi: it takes the
// screen, shows the test pattern moving with a touch script and then
// still, and checks the figures. No CI machine has a screen, so it runs
// only with the kmspi tag, as root for the VT and the backlight:
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags kmspi -o kmspi.test ./driver/kms
//	./kmspi.test -test.v
//
// GUNIM_KMSPI_ROTATE turns the picture (90 by default), and GUNIM_KMSPI_VT
// names the VT ("free" by default). It skips where there is no screen to
// take. It needs no root: a user in the groups video, render and input,
// with the backlight's udev rule in the README, will do.
package kms_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/driver/kms/kmsbench"
)

// knownMisses are goals a screen misses for a reason an issue holds: the
// test logs them and checks every other goal. The goals themselves stay as
// the plan has them. The demo redraws the whole screen on every frame,
// which #417 is about.
var knownMisses = map[string]map[string]string{
	"demo": {
		"touch to flip p50": "https://github.com/marrasen/gunim/issues/417",
		"touch to flip p99": "https://github.com/marrasen/gunim/issues/417",
	},
	// The gallery redraws its whole window as it scrolls, as the demo does
	// on every frame.
	"widgets": {
		"touch to flip p50": "https://github.com/marrasen/gunim/issues/417",
		"touch to flip p99": "https://github.com/marrasen/gunim/issues/417",
	},
}

func TestOnTheScreen(t *testing.T) {
	rotate := 90
	if v := os.Getenv("GUNIM_KMSPI_ROTATE"); v != "" {
		rotate, _ = strconv.Atoi(v)
	}
	vt := "free"
	if v, ok := os.LookupEnv("GUNIM_KMSPI_VT"); ok {
		vt = v
	}
	t.Run("demo", func(t *testing.T) { bench(t, "demo", kms.Options{Rotate: rotate, VT: vt}) })
	t.Run("widgets", func(t *testing.T) { bench(t, "widgets", kms.Options{Rotate: rotate, VT: vt}) })
}

// A GPU that fails as the driver starts leaves Mesa's software renderer to
// draw, in the same process: GUNIM_KMS_FAIL_GPU fails the GPU's attempt
// once it has made its context.
func TestFallsBackToTheCPUOnTheScreen(t *testing.T) {
	t.Setenv("GUNIM_KMS_FAIL_GPU", "1")
	o := kmsbench.Options{
		KMS:   kms.Options{Rotate: 90, VT: "free"},
		Scene: benchScene(t, "demo"),
		Warm:  time.Second, Moving: 3 * time.Second, Rest: 2 * time.Second, NoBlank: true,
		Log: func(s string) { t.Log(s) },
	}
	r, err := kmsbench.Bench(context.Background(), o)
	if err != nil && strings.Contains(err.Error(), "no connected screen") {
		t.Skipf("no screen to take: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !r.Software {
		t.Fatal("the CPU does not draw after the GPU failed")
	}
	if r.Moving.Frames == 0 {
		t.Fatal("no frames drawn on the CPU")
	}
	t.Logf("on the CPU: %d frames in %v", r.Moving.Frames, r.MovingFor)
}

func bench(t *testing.T, screen string, ko kms.Options) {
	o := kmsbench.Options{
		KMS: ko, Scene: benchScene(t, screen),
		// 10 s warm: the driver's pacer learns its lead in the first seconds,
		// missing a vblank or two as it finds the edge. 30 s moving: a rate
		// of 1 missed flip in 1 000 needs that many frames to tell.
		Warm: 10 * time.Second, Moving: 30 * time.Second, Rest: 5 * time.Second,
		Log: func(s string) { t.Log(s) },
	}
	r, err := kmsbench.Bench(context.Background(), o)
	if err != nil && strings.Contains(err.Error(), "no connected screen") {
		t.Skipf("no screen to take: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range r.Goals() {
		t.Logf("%-24s %-20s %s", g.Name, g.Target, g.Got)
	}
	// The plan's goals, as kmsbench checks them: the frame rate, missed
	// flips with slow frames among them, frame times, touch to flip, the
	// start, and nothing drawn at rest.
	for _, g := range r.Goals() {
		switch {
		case g.OK:
		case knownMisses[screen][g.Name] != "":
			t.Logf("%s: %s, want %s: a known miss, %s", g.Name, g.Got, g.Target, knownMisses[screen][g.Name])
		default:
			t.Errorf("%s: %s, want %s", g.Name, g.Got, g.Target)
		}
	}
	if r.Moving.Touch.N == 0 {
		t.Error("no touch from the script reached a frame")
	}
}
