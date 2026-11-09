package speaker

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/internal/aaudio"
)

func TestMoveFor(t *testing.T) {
	const q = int64(aaDeviceQuiet)
	for _, c := range []struct {
		name                  string
		opened, changed       int64
		picked, listed, moves bool
	}{
		{"no change yet", 10, 0, true, true, false},
		{"Android's pick, a change after it opened", 10, 10 + q, true, true, true},
		{"Android's pick, a change just after it opened", 10, 10 + q - 1, true, true, false},
		{"Android's pick, a change as it opened", 10, 10, true, true, false},
		{"Android's pick, opened since the change", 30, 20, true, true, false},
		{"its own device, still listed", 10, 10 + q, false, true, false},
		{"its own device, gone", 10, 10 + q, false, false, true},
		{"its own device gone, opened since", 30 + q, 20 + q, false, false, false},
	} {
		if got := moveFor(c.opened, c.changed, c.picked, c.listed); got != c.moves {
			t.Errorf("%s: moveFor says %v, want %v", c.name, got, c.moves)
		}
	}
}

// TestAAudioReopensADeviceStandingStill has the output's device stop
// moving while its callbacks run on, as a device gone that AAudio does
// not tell of. Before Android 11 the speaker opens the stream again
// once the device has stood still for aaStill, and plays on; from 11 it
// leaves it to AAudio.
func TestAAudioReopensADeviceStandingStill(t *testing.T) {
	for _, older := range []bool{true, false} {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeAA{rate: 48000, burst: 240, delay: 960, older: older}
			useFakeAA(t, f)
			m := audio.NewMixer()
			v := m.Play(&ramp{}, audio.Options{})
			openAA(t, m, Options{Record: true}, v)
			time.Sleep(500 * time.Millisecond)
			outs, ins := f.streams()
			outs[0].stuck.Store(true)
			// Just short of aaStill: no stream reopens yet.
			time.Sleep(aaStill - 100*time.Millisecond)
			synctest.Wait()
			if o, _ := f.streams(); len(o) != 1 {
				t.Fatalf("older %v: %d outputs opened before the device stood still aaStill, want 1", older, len(o))
			}
			time.Sleep(300 * time.Millisecond)
			synctest.Wait()
			want := 1
			if older {
				want = 2
			}
			outs, _ = f.streams()
			if len(outs) != want {
				t.Fatalf("older %v: %d outputs opened after the device stood still, want %d", older, len(outs), want)
			}
			// The input records on, its own device moving.
			if _, i := f.streams(); len(i) != len(ins) {
				t.Errorf("older %v: %d inputs opened, want %d", older, len(i), len(ins))
			}
			h := m.Heard()
			time.Sleep(200 * time.Millisecond)
			synctest.Wait()
			if older && m.Heard() <= h {
				t.Error("after the stream opened again the speaker hears nothing more")
			}
		})
	}
}

// TestAAudioReopensNoStreamPausedLong suspends for longer than aaStill
// before Android 11: a device that stands still while paused, and as it
// resumes, reopens nothing.
func TestAAudioReopensNoStreamPausedLong(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960, older: true}
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{}, v)
		time.Sleep(500 * time.Millisecond)
		if err := s.Suspend(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * aaStill)
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(aaStill)
		synctest.Wait()
		if outs, _ := f.streams(); len(outs) != 1 {
			t.Fatalf("%d outputs opened, want 1", len(outs))
		}
	})
}

// TestAAudioFollowsTheDevicesAndroidTellsOf has Android tell of devices
// coming and going before Android 11, as a headset plugged in, which
// brings an output and an input at once, in several reports. Once
// Android is quiet the speaker opens each stream again once, on
// Android's pick; while suspended it waits for the resume. From 11 it
// reopens nothing.
func TestAAudioFollowsTheDevicesAndroidTellsOf(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960, older: true}
		useFakeAA(t, f)
		m := audio.NewMixer()
		v := m.Play(&ramp{}, audio.Options{})
		s := openAA(t, m, Options{Record: true}, v)
		time.Sleep(500 * time.Millisecond)
		count := func(wantOuts, wantIns int, when string) {
			t.Helper()
			synctest.Wait()
			if outs, ins := f.streams(); len(outs) != wantOuts || len(ins) != wantIns {
				t.Fatalf("%s: %d outputs and %d inputs opened, want %d and %d", when, len(outs), len(ins), wantOuts, wantIns)
			}
		}
		for range 3 {
			f.outChanges.Add(1)
			f.inChanges.Add(1)
			time.Sleep(100 * time.Millisecond)
		}
		count(1, 1, "while Android tells")
		time.Sleep(aaDeviceQuiet + 100*time.Millisecond)
		count(2, 2, "once Android is quiet")
		time.Sleep(time.Second)
		count(2, 2, "a second later")

		// An output alone moves the output alone.
		f.outChanges.Add(1)
		time.Sleep(aaDeviceQuiet + 100*time.Millisecond)
		count(3, 2, "after an output came")

		// While suspended nothing moves; the resume opens the microphone
		// anew, and the output moves.
		time.Sleep(time.Second)
		if err := s.Suspend(); err != nil {
			t.Fatal(err)
		}
		f.outChanges.Add(1)
		f.inChanges.Add(1)
		time.Sleep(time.Second)
		count(3, 2, "while suspended")
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(aaDeviceQuiet + 100*time.Millisecond)
		count(4, 3, "after resuming")
		h := m.Heard()
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if m.Heard() <= h {
			t.Error("after the streams opened again the speaker hears nothing more")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960}
		useFakeAA(t, f)
		openAA(t, audio.NewMixer(), Options{Record: true})
		f.outChanges.Add(1)
		f.inChanges.Add(1)
		time.Sleep(time.Second)
		synctest.Wait()
		if outs, ins := f.streams(); len(outs) != 1 || len(ins) != 1 {
			t.Fatalf("from Android 11: %d outputs and %d inputs opened, want 1 and 1", len(outs), len(ins))
		}
	})
}

// TestAAudioKeepsTheBuiltInMicWhileListed records on the phone's own
// microphone as Bluetooth plays, before Android 11. A device coming or
// going leaves it be while Android lists it, and opens it again once
// Android lists it no more.
func TestAAudioKeepsTheBuiltInMicWhileListed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const mic = 15
		f := &fakeAA{rate: 48000, burst: 192, delay: 960, older: true}
		f.route.Store(&aaudio.Route{Known: true, Type: 8, Latency: 20 * time.Millisecond, Mic: mic})
		useFakeAA(t, f)
		openAA(t, audio.NewMixer(), Options{Record: true})
		time.Sleep(500 * time.Millisecond)
		f.inChanges.Add(1)
		time.Sleep(aaDeviceQuiet + 100*time.Millisecond)
		synctest.Wait()
		if _, ins := f.streams(); len(ins) != 1 {
			t.Fatalf("with its device listed %d inputs opened, want 1", len(ins))
		}
		// No device has the stream's ID now.
		f.route.Store(&aaudio.Route{Known: true, Type: 0, Latency: 20 * time.Millisecond, Mic: mic})
		f.inChanges.Add(1)
		time.Sleep(aaDeviceQuiet + 100*time.Millisecond)
		synctest.Wait()
		if _, ins := f.streams(); len(ins) != 2 || ins[1].c.device != mic {
			t.Fatalf("with its device gone %d inputs opened, the last on %d; want 2, on %d", len(ins),
				ins[len(ins)-1].c.device, mic)
		}
	})
}

// TestAAudioReopensOnceWhereAAudioTellsToo has AAudio tell of a device
// gone before Android 11, as it does on many phones, and Java tell of
// it a moment later: the output opens again once, not twice.
func TestAAudioReopensOnceWhereAAudioTellsToo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAA{rate: 48000, burst: 240, delay: 960, older: true}
		useFakeAA(t, f)
		openAA(t, audio.NewMixer(), Options{})
		time.Sleep(500 * time.Millisecond)
		outs, _ := f.streams()
		outs[0].sh.Err.Store(-899) // AAUDIO_ERROR_DISCONNECTED
		time.Sleep(60 * time.Millisecond)
		f.outChanges.Add(1)
		time.Sleep(time.Second)
		synctest.Wait()
		if outs, _ := f.streams(); len(outs) != 2 {
			t.Fatalf("%d outputs opened, want 2", len(outs))
		}
	})
}
