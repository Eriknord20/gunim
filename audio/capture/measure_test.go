package capture

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
)

// loopback plays a mixer into a fake speaker and its sound back into a
// stream, 10 milliseconds a tick: the speaker is outLat frames behind
// the mixer, and the input hands each block on inLat frames after it
// came in, though it reports reported. gain scales the sound on its way
// back, and hum is added to it.
type loopback struct {
	m                       *audio.Mixer
	s                       *Stream
	clock                   *fakeClock
	outLat, inLat, reported int
	gain, hum               float32
}

func (l *loopback) run(stop <-chan struct{}) {
	const n = 480
	var played, queue [][]float32
	for {
		select {
		case <-stop:
			return
		default:
		}
		out := make([]float32, 2*n)
		l.m.Mix(out)
		played = append(played, out)
		l.clock.now.Store(l.m.Mixed() - int64(l.outLat))
		if len(played) <= l.outLat/n {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		// What the speaker plays this tick was mixed outLat frames
		// ago; the input hears it as it plays.
		heard := played[0]
		played = played[1:]
		in := make([]float32, n)
		for i := range n {
			in[i] = heard[2*i]*l.gain + l.hum*float32(i%7-3)/3
		}
		queue = append(queue, in)
		// The input hands on what came in inLat frames ago.
		if len(queue) > l.inLat/n {
			l.s.putAgo(queue[0], int64(l.reported+n))
			queue = queue[1:]
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// measureLoop runs Measure over a loopback, and returns what it found.
func measureLoop(t *testing.T, l loopback) (Measurement, error) {
	t.Helper()
	var (
		got Measurement
		err error
	)
	synctest.Test(t, func(t *testing.T) {
		l.m = audio.NewMixer()
		l.m.SetRate(48000)
		l.clock = &fakeClock{}
		l.s, _ = openFake(t, Options{}, format{name: "Fake", channels: 1, rate: 48000, buffer: 480, latency: l.reported}, l.clock, 48000)
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			l.run(stop)
		}()
		// Let the input fill a while first, as a stream recording does.
		time.Sleep(200 * time.Millisecond)
		got, err = Measure(l.s, l.m, 0, 3*time.Second)
		close(stop)
		<-done
		synctest.Wait()
	})
	return got, err
}

// TestMeasureFindsTheClickWhereTheStampsPutIt measures a loopback whose
// input reports half the latency it has: the click comes in a frame
// for each frame of latency unreported later than its stamps say.
func TestMeasureFindsTheClickWhereTheStampsPutIt(t *testing.T) {
	got, err := measureLoop(t, loopback{outLat: 480, inLat: 960, reported: 480, gain: 0.3, hum: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offset != 480 {
		t.Errorf("Offset = %d, want 480", got.Offset)
	}
	if got.Level < 0.14 || got.Level > 0.17 {
		t.Errorf("Level = %v, want 0.15", got.Level)
	}
	// The speaker's 480 frames and the input's 960, at 48 kHz.
	if want := 30 * time.Millisecond; got.RoundTrip != want {
		t.Errorf("RoundTrip = %v, want %v", got.RoundTrip, want)
	}
}

// TestMeasureOfAnInputReportedRight finds the click where its stamps
// put it.
func TestMeasureOfAnInputReportedRight(t *testing.T) {
	got, err := measureLoop(t, loopback{outLat: 480, inLat: 1440, reported: 1440, gain: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Offset != 0 {
		t.Errorf("Offset = %d, want 0", got.Offset)
	}
}

// TestMeasureOfAnInputThatHearsNothing finds no click in the hum.
func TestMeasureOfAnInputThatHearsNothing(t *testing.T) {
	if _, err := measureLoop(t, loopback{outLat: 480, inLat: 960, reported: 960, gain: 0.001, hum: 0.05}); !errors.Is(err, ErrNoClick) {
		t.Fatalf("err = %v, want ErrNoClick", err)
	}
}
