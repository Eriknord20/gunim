package speaker

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/audiounit"
)

// A device that changes again as soon as the output opens, as AirPods
// switch their rate as their microphone opens and back as it closes,
// opens the output again at once twice, then after 1, 2, 4 and 8
// seconds; from the third in a row State.Problem says why, until an
// output plays steady.
func TestCoreAudioReopensMoreSlowlyInARow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := useAU(t, macInfo())
		var mu sync.Mutex
		var opens []time.Time
		inner := f.open
		openAU = func(c audiounit.Config) (auDevice, error) {
			d, err := inner(c)
			mu.Lock()
			opens = append(opens, time.Now())
			mu.Unlock()
			return d, err
		}
		count := func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(opens)
		}
		s, err := Open(audio.NewMixer(), Options{Driver: driver.CoreAudio, Record: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		var problems []bool
		for n := 1; n < 7; n++ {
			problems = append(problems, s.State().Problem != nil)
			f.tell(func(e *audiounit.Events) { e.Reopen = "AirPods's rate changed from 48000 to 24000 Hz" })
			for count() == n {
				time.Sleep(time.Millisecond)
			}
		}
		problems = append(problems, s.State().Problem != nil)
		mu.Lock()
		gaps := make([]time.Duration, len(opens)-1)
		for i := range gaps {
			gaps[i] = opens[i+1].Sub(opens[i])
		}
		mu.Unlock()
		want := []time.Duration{0, 0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
		for i, g := range gaps {
			if g < want[i] || g > want[i]+10*time.Millisecond {
				t.Errorf("open %d came %v after the one before, want %v", i+2, g, want[i])
			}
		}
		wantProblems := []bool{false, false, false, true, true, true, true}
		for i, p := range problems {
			if p != wantProblems[i] {
				t.Errorf("output %d: a problem told %v, want %v", i+1, p, wantProblems[i])
			}
		}
		// Played steady, the problem clears, and a change after opens
		// again at once.
		time.Sleep(auSteady + 10*time.Millisecond)
		if p := s.State().Problem; p != nil {
			t.Errorf("steady, the problem stays: %v", p)
		}
		f.tell(func(e *audiounit.Events) { e.Reopen = "the default output device changed" })
		before := time.Now()
		for count() == 7 {
			time.Sleep(time.Millisecond)
		}
		if g := time.Since(before); g > 10*time.Millisecond {
			t.Errorf("a change after a steady while opened %v later", g)
		}
	})
}
