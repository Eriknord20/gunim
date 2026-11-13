//go:build kmsvkms && linux && (arm64 || amd64)

// The driver on the kernel's virtual display, vkms, with Mesa drawing on
// the CPU: the whole driver on a machine with no screen, as a CI runner.
// It needs the vkms module loaded, Mesa's GBM, EGL and GLES libraries, and
// root or the group video:
//
//	sudo modprobe vkms
//	go test -c -tags kmsvkms -o vkms.test ./driver/kms
//	sudo ./vkms.test -test.v
//
// GUNIM_VKMS_CONNECTOR names vkms's connector where another card has a
// screen too.
package kms_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/internal/kmsdemo"
)

func TestOnVKMS(t *testing.T) {
	o := kmsdemo.BenchOptions{
		KMS:  kms.Options{Software: true, Connector: os.Getenv("GUNIM_VKMS_CONNECTOR")},
		Warm: time.Second, Moving: 5 * time.Second, Rest: 2 * time.Second,
		Log: func(s string) { t.Log(s) },
	}
	r, err := kmsdemo.Bench(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range r.Goals() {
		t.Logf("%-36s %-32s %s", g.Name, g.Target, g.Got)
	}
	if r.Moving.Frames < 10 {
		t.Errorf("%d frames in %v", r.Moving.Frames, r.MovingFor)
	}
	if r.Moving.Touch.N == 0 {
		t.Error("no touch from the script reached a frame")
	}
	if r.RestFrames != 0 {
		t.Errorf("%d frames drawn at rest", r.RestFrames)
	}
	b := r.Blank
	if b == nil || !b.Off || b.DisplayOn || b.Frames != 0 {
		t.Errorf("blanking: %+v, want off, the CRTC off and no frames", b)
	}
	if b != nil && (!b.Woke || !b.Swallowed) {
		t.Errorf("the tap: %+v, want the screen woken and the tap swallowed", b)
	}
}
