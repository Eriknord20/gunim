package speaker

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/audiounit"
)

// A device change while suspended closes the output and opens nothing,
// so the device and its microphone stay off while the application is
// away; Resume opens it again, started.
func TestReopenWaitsForResume(t *testing.T) {
	f := useAU(t, macInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Suspend(); err != nil {
		t.Fatal(err)
	}
	f.tell(func(e *audiounit.Events) { e.Reopen = "the default input device changed" })
	waitFor(t, "the output closed", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.closes == 1
	})
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	opens := f.opens
	f.mu.Unlock()
	if opens != 1 || s.out.Load() != nil {
		t.Fatalf("suspended, the output opened again: %d opens", opens)
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("the output never opened as it resumed")
	}
	if auOf(t, s).paused.Load() {
		t.Fatal("resumed, the output opened paused")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 2 || f.suspends != 1 {
		t.Fatalf("opens %d, suspends %d; want 2 and 1", f.opens, f.suspends)
	}
}

// The same holds for any driver, and for a change Set asks for: here
// JACK's.
func TestSetWhileSuspendedWaitsForResume(t *testing.T) {
	f := useJack(t, stereoInfo())
	s, err := Open(audio.NewMixer(), Options{Driver: driver.JACK})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(Options{Driver: driver.JACK, Record: true}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	opens := f.opens
	f.mu.Unlock()
	if opens != 1 {
		t.Fatalf("suspended, Set opened the output: %d opens", opens)
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	if jackOf(t, s).paused.Load() {
		t.Fatal("resumed, the output opened paused")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opens != 2 || !f.cfg.Record {
		t.Fatalf("opens %d, record %v; want 2 and true", f.opens, f.cfg.Record)
	}
}
