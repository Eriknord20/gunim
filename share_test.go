package gunim

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

func TestShareGoesToTheWindowsSharer(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	path := filepath.Join(t.TempDir(), "song.wav")
	if err := os.WriteFile(path, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got driver.Share
	w.Offscreen().SetSharer(func(s driver.Share) error { got = s; return nil })
	want := driver.Share{Text: "Listen", Subject: "A song", Paths: []string{path}}
	if err := w.Client().Share(want); err != nil {
		t.Fatal(err)
	}
	if got.Text != want.Text || got.Subject != want.Subject || !slices.Equal(got.Paths, want.Paths) {
		t.Fatalf("the sharer got %+v, want %+v", got, want)
	}
}

func TestShareChecksTheFilesAreThere(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	shared := false
	w.Offscreen().SetSharer(func(driver.Share) error { shared = true; return nil })
	err := w.Client().Share(driver.Share{Paths: []string{filepath.Join(t.TempDir(), "gone.wav")}})
	if !errors.Is(err, os.ErrNotExist) || shared {
		t.Fatalf("Share returned %v and shared %v, want ErrNotExist and nothing shared", err, shared)
	}
}

func TestShareAndVibrateSayWhenThereIsNone(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	if err := w.Client().Share(driver.Share{Text: "x"}); !errors.Is(err, driver.ErrNoSharer) {
		t.Fatalf("Share returned %v, want ErrNoSharer", err)
	}
	if err := w.Client().Vibrate(time.Second); !errors.Is(err, driver.ErrNoVibrator) {
		t.Fatalf("Vibrate returned %v, want ErrNoVibrator", err)
	}
}

func TestVibrateHandsOverItsPattern(t *testing.T) {
	w := NewOffscreen(geom.Sz(400, 300), nil)
	var got []time.Duration
	w.Offscreen().SetVibrator(func(p []time.Duration) error { got = p; return nil })
	want := []time.Duration{100 * time.Millisecond, 50 * time.Millisecond, 200 * time.Millisecond}
	if err := w.Client().Vibrate(want...); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the motor got %v, want %v", got, want)
	}
}
