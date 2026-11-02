package audio

import (
	"math"
	"testing"
	"testing/synctest"
	"time"
)

// HeardAt is Heard moved on at the mixer's rate by the time from now,
// checked at each millisecond from two seconds before now to two after:
// a second each way at most, and never below zero.
func TestHeardAtMovesHeardOnByTheTimeFromNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMixer()
		held := int64(4800)
		m.SetLatency(func() int64 { return held })
		mix(m, 3*SampleRate)
		heard := m.Heard()
		if got := m.HeardAt(time.Time{}); got != heard {
			t.Fatalf("HeardAt the zero time: %d, want Heard, %d", got, heard)
		}
		now := time.Now()
		for ms := -2000; ms <= 2000; ms++ {
			d := time.Duration(ms) * time.Millisecond
			want := heard + FramesAt(min(max(d, -time.Second), time.Second), SampleRate)
			if got := m.HeardAt(now.Add(d)); got != want {
				t.Fatalf("HeardAt %v from now: %d, want %d", d, got, want)
			}
		}

		// Early on, a time long past reads as frame zero.
		early := NewMixer()
		mix(early, 100)
		for ms := -1000; ms <= 0; ms += 10 {
			d := time.Duration(ms) * time.Millisecond
			want := max(0, 100+FramesAt(d, SampleRate))
			if got := early.HeardAt(now.Add(d)); got != want || got < 0 {
				t.Fatalf("HeardAt %v from now, 100 frames in: %d, want %d", d, got, want)
			}
		}
	})
}

// A voice's position at a time ahead is its position now moved on by
// that time, up to the end of the last block mixed; one behind moves
// back. It is checked at each millisecond.
func TestPositionAtIsThePositionHeardThen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMixer()
		held := int64(4800)
		m.SetLatency(func() int64 { return held })
		v := m.Play(constant(4*SampleRate, 0.5, 0.5).Source(), Options{})
		mix(m, 12000)
		now := time.Now()
		pos := v.Position()
		if got := v.PositionAt(time.Time{}); got != pos {
			t.Fatalf("PositionAt the zero time: %v, want Position, %v", got, pos)
		}
		end := Duration(12000)
		for ms := -200; ms <= 300; ms++ {
			d := time.Duration(ms) * time.Millisecond
			want := min(max(pos+Duration(FramesAt(d, SampleRate)), 0), end)
			if got := v.PositionAt(now.Add(d)); got != want {
				t.Fatalf("PositionAt %v from now: %v, want %v", d, got, want)
			}
		}
		stopAll(m, v)
	})
}

// An analyzer set to a time ahead reads the sound heard then, as far
// as the mixer has mixed. Each frame of the sound holds its own number,
// so the wave's last sample says which frame the analyzer read up to.
// It is checked at each millisecond ahead.
func TestTheAnalyzerMeasuresAtItsTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMixer()
		held := int64(4800) // 100 ms
		m.SetLatency(func() int64 { return held })
		a := NewAnalyzer(m, 8)
		_, from := a.Heard(nil, 0) // starts keeping the frames
		numbered := make([]float32, 2*SampleRate)
		for i := range SampleRate {
			numbered[2*i], numbered[2*i+1] = float32(i)/1e6, float32(i)/1e6
		}
		v := m.Play(NewClip(numbered).Source(), Options{})
		mix(m, 12000)
		heard := m.Heard()
		now := time.Now()
		wave := make([]float32, 1)
		for ms := range 151 {
			d := time.Duration(ms) * time.Millisecond
			a.At = now.Add(d)
			want := min(heard+FramesAt(d, SampleRate), m.Mixed())
			frames, upTo := a.Heard(nil, from)
			if upTo != want {
				t.Fatalf("At %v ahead: heard up to frame %d, want %d", d, upTo, want)
			}
			if got := int64(len(frames) / 2); got != want-from {
				t.Fatalf("At %v ahead: %d frames heard, want %d", d, got, want-from)
			}
			a.Wave(wave)
			if got, w := wave[0], float32(want-1)/1e6; got < w-5e-7 || got > w+5e-7 {
				t.Fatalf("At %v ahead: the wave ends on frame %.0f, want %d", d, got*1e6, want-1)
			}
		}
		stopAll(m, v)
	})
}

// stopAll stops v and waits for the mixer's teller to end, as a test in
// a bubble must.
func stopAll(m *Mixer, v *Voice) {
	v.Stop(0)
	mix(m, 4096)
	time.Sleep(2 * tellIdle)
}

// A meter that reads each frame once, as At moves on a frame of the
// display at a time and now and then a little back, reads every frame
// heard once, in order.
func TestTheAnalyzerReadsEachFrameOnceAsItsTimeJitters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMixer()
		m.SetLatency(func() int64 { return 4800 })
		a := NewAnalyzer(m, 8)
		_, from := a.Heard(nil, 0)
		numbered := make([]float32, 2*SampleRate)
		for i := range SampleRate {
			numbered[2*i], numbered[2*i+1] = float32(i)/1e6, float32(i)/1e6
		}
		v := m.Play(NewClip(numbered).Source(), Options{})
		var buf []float32
		next := from
		backs := 0
		for i := range 60 {
			mix(m, 400) // 8.3 ms
			// Each frame shows 20 ms ahead; every third, 5 ms: the frame
			// heard then is before the one the frame before read up to.
			ahead := 20 * time.Millisecond
			if i%3 == 2 {
				ahead = 5 * time.Millisecond
			}
			a.At = time.Now().Add(ahead)
			if m.HeardAt(a.At) < from {
				backs++
			}
			var frames []float32
			frames, from = a.Heard(buf[:0], from)
			for k := 0; k < len(frames); k += 2 {
				if got := int64(math.Round(float64(frames[k]) * 1e6)); got != next {
					t.Fatalf("frame %d of the display: read sound frame %d, want %d", i, got, next)
				}
				next++
			}
			if from != next {
				t.Fatalf("frame %d of the display: heard up to %d, read up to %d", i, from, next)
			}
		}
		if backs < 10 {
			t.Fatalf("the time went back %d times, want 10 at least", backs)
		}
		if want := int64(60*400) - 4800; next < want {
			t.Fatalf("read up to frame %d, want %d", next, want)
		}
		stopAll(m, v)
	})
}
