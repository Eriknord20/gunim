//go:build linux && (arm64 || amd64)

package kms

import (
	"os"
	"path/filepath"
	"testing"
)

// Recordings of real fingers on the trial Pi's Touch Display 2, made with
// kmsdemo -record and kept in testdata as *.evdev, play through the
// decoder: every finger that comes down lifts, slots stay in range,
// positions stay on the panel, and a finger's moves keep its tracking id.
//
// To add one: run kmsdemo -record touch.evdev on the Pi, touch the screen
// (taps, five fingers, a drag, a palm), end it, and copy the file here.
func TestRecordingsFromThePiDecode(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "*.evdev"))
	if len(paths) == 0 {
		t.Skip("no recordings in testdata yet: they need a finger on the Pi's screen")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			p, err := readRecording(f)
			if err != nil {
				t.Fatal(err)
			}
			m := newMTDecoder(p.slots)
			down := map[int]int32{}
			reports := 0
			for _, e := range p.events {
				for _, c := range m.feed(e) {
					reports++
					if c.slot < 0 || c.slot >= p.slots {
						t.Fatalf("slot %d of %d", c.slot, p.slots)
					}
					if c.x < p.x.minimum || c.x > p.x.maximum || c.y < p.y.minimum || c.y > p.y.maximum {
						t.Fatalf("%+v off the panel", c)
					}
					switch c.phase {
					case phaseDown:
						if _, ok := down[c.slot]; ok {
							t.Fatalf("slot %d came down twice", c.slot)
						}
						down[c.slot] = c.id
					case phaseMove:
						if id, ok := down[c.slot]; !ok || id != c.id {
							t.Fatalf("%+v moves a finger that is not down", c)
						}
					case phaseUp:
						delete(down, c.slot)
					}
				}
			}
			if reports == 0 {
				t.Fatal("the recording holds no touches")
			}
			if len(down) != 0 {
				t.Fatalf("fingers left down at the end: %v", down)
			}
		})
	}
}
