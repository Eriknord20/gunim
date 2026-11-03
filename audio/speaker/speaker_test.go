package speaker

import (
	"math"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
)

// TestASoundReachesTheSpeakers plays a quiet 1 kHz tone for a second.
// It runs with GUNIM_SPEAKER=1, as it makes a sound; record the
// system's output meanwhile to hear it.
func TestASoundReachesTheSpeakers(t *testing.T) {
	if os.Getenv("GUNIM_SPEAKER") != "1" {
		t.Skip("set GUNIM_SPEAKER=1 to play a tone through the speakers")
	}
	m := audio.NewMixer()
	lat, _ := time.ParseDuration(os.Getenv("GUNIM_SPEAKER_LATENCY"))
	s, err := Open(m, Options{Name: "gunim speaker test", Latency: lat})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	// The speaker finds how far ahead it must work on this machine.
	time.Sleep(1500 * time.Millisecond)
	t.Logf("working %v ahead", s.Latency())
	tone := make([]float32, 2*audio.SampleRate)
	for i := range audio.SampleRate {
		v := float32(0.1 * math.Sin(2*math.Pi*1000*float64(i)/audio.SampleRate))
		tone[2*i], tone[2*i+1] = v, v
	}
	start := time.Now()
	v := m.Play(audio.NewClip(tone).Source(), audio.Options{})
	select {
	case <-v.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the tone never finished playing")
	}
	took := time.Since(start)
	// Done means mixed; the speakers play the last of it after.
	time.Sleep(s.Latency() + 50*time.Millisecond)
	if took < 900*time.Millisecond || took > 1500*time.Millisecond {
		t.Errorf("a second's tone took %v to play", took)
	}
	if err := s.Err(); err != nil {
		t.Error(err)
	}
	t.Logf("played a second's tone in %v", took)
}

// TestSetOpensTheSpeakersAgain plays through the system's sound at
// 48 kHz, sets 44.1 kHz, and plays on at it. It runs with
// GUNIM_SPEAKER=1, as it makes a sound.
func TestSetOpensTheSpeakersAgain(t *testing.T) {
	if os.Getenv("GUNIM_SPEAKER") != "1" {
		t.Skip("set GUNIM_SPEAKER=1 to play through the speakers")
	}
	m := audio.NewMixer()
	o := Options{Name: "gunim speaker test", Rate: 48000, Bits: 16, Dither: true}
	s, err := Open(m, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	time.Sleep(300 * time.Millisecond)
	o.Rate = 44100
	if err := s.Set(o); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Changed():
	default:
		t.Error("Changed told nothing of the speakers opening again")
	}
	st := s.State()
	if m.Rate() != 44100 || st.Rate != 44100 {
		t.Errorf("the mixer plays at %d Hz and the speakers at %d, want 44100", m.Rate(), st.Rate)
	}
	before := m.Mixed()
	time.Sleep(500 * time.Millisecond)
	if got := m.Mixed() - before; got < 44100/4 || got > 44100 {
		t.Errorf("half a second mixed %d frames at 44.1 kHz", got)
	}
	t.Logf("state %+v", st)
	if err := s.Err(); err != nil {
		t.Error(err)
	}
}

// stillOut is an output that plays nothing, at hz, as far ahead as
// the speaker asks.
type stillOut struct {
	hz    int
	ahead atomic.Int64
}

func (o *stillOut) start(ahead time.Duration) { o.setAhead(ahead) }
func (o *stillOut) held() int64               { return 0 }
func (o *stillOut) setAhead(d time.Duration)  { o.ahead.Store(int64(d)) }
func (o *stillOut) latency() time.Duration    { return time.Duration(o.ahead.Load()) }
func (o *stillOut) rate() int                 { return o.hz }
func (o *stillOut) fill(*State)               {}
func (o *stillOut) err() error                { return nil }
func (o *stillOut) suspend() error            { return nil }
func (o *stillOut) resume() error             { return nil }
func (o *stillOut) close() error              { return nil }

// TestSpeakerForgetsTheDryReadsOfAnOutputClosed has the output run dry
// once, then opens another at a new rate, which runs dry once too,
// all within one of the watch's looks. Once each is no sign the buffer
// is too small: the lead stays as it was. Counted together, the two
// would grow it.
func TestSpeakerForgetsTheDryReadsOfAnOutputClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wasAA, wasCA, wasWA, wasOto := aaudioFirst, coreAudioFirst, wasapiFirst, openSystemSound
		aaudioFirst, coreAudioFirst, wasapiFirst = false, false, false
		openSystemSound = func(_ *Speaker, o Options) (startedOutput, error) { return &stillOut{hz: o.rate()}, nil }
		t.Cleanup(func() { aaudioFirst, coreAudioFirst, wasapiFirst, openSystemSound = wasAA, wasCA, wasWA, wasOto })
		s, err := Open(audio.NewMixer(), Options{Rate: 48000})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		time.Sleep(300 * time.Millisecond)
		s.ranDry()
		if err := s.Set(Options{Rate: 44100}); err != nil {
			t.Fatal(err)
		}
		s.ranDry()
		time.Sleep(time.Second)
		if l := s.Latency(); l != 30*time.Millisecond {
			t.Errorf("working %v ahead, want 30ms: a dry read of the output closed counted against the one opened", l)
		}
		if d := s.State().Dropouts; d != 2 {
			t.Errorf("%d dropouts, want 2", d)
		}
	})
}
