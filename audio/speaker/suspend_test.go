package speaker

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
)

// still is an output that plays nothing, and counts its suspends and
// resumes.
type still struct{ suspends, resumes int }

func (o *still) held() int64            { return 0 }
func (o *still) setAhead(time.Duration) {}
func (o *still) latency() time.Duration { return 0 }
func (o *still) rate() int              { return audio.SampleRate }
func (o *still) fill(*State)            {}
func (o *still) err() error             { return nil }
func (o *still) suspend() error         { o.suspends++; return nil }
func (o *still) resume() error          { o.resumes++; return nil }
func (o *still) close() error           { return nil }

func stillSpeaker(m *audio.Mixer) (*Speaker, *still) {
	o := &still{}
	s := &Speaker{m: m}
	s.out.Store(&outlet{o})
	return s, o
}

// level mixes a block of m and returns its first sample.
func level(m *audio.Mixer) float32 {
	out := make([]float32, 2*128)
	m.Mix(out)
	return out[0]
}

func TestSuspendStopsASilentDeviceAtOnce(t *testing.T) {
	m := audio.NewMixer()
	s, o := stillSpeaker(m)
	_ = s.Suspend()
	if o.suspends != 1 {
		t.Fatalf("with nothing playing the device stopped %d times, want 1", o.suspends)
	}
	_ = s.Resume()
	if o.resumes != 1 {
		t.Fatalf("the device started %d times, want 1", o.resumes)
	}
	// A sound played as it resumes starts at full: there is nothing to
	// fade back in.
	m.Play(audio.NewClip([]float32{1, 1, 1, 1}).Source(), audio.Options{})
	if got := level(m); got != 1 {
		t.Errorf("a sound played after a silent rest starts at %v, want 1", got)
	}
}

func TestSuspendFadesOutWhatPlaysAndResumeFadesItIn(t *testing.T) {
	m := audio.NewMixer()
	tone := make([]float32, 2*audio.SampleRate)
	for i := range tone {
		tone[i] = 0.5
	}
	m.Play(audio.NewClip(tone).Source(), audio.Options{})
	s, o := stillSpeaker(m)
	_ = s.Suspend()
	s.smu.Lock()
	early := o.suspends
	s.smu.Unlock()
	if early != 0 {
		t.Fatal("the device stopped before the sound faded out")
	}
	// A block more: each block's time rounds down.
	m.Mix(make([]float32, 2*m.Frames(SuspendFade)+256))
	if got := level(m); got != 0 {
		t.Errorf("after the fade the sound is at %v, want silence", got)
	}
	time.Sleep(SuspendFade + 50*time.Millisecond)
	s.smu.Lock()
	stopped := o.suspends
	s.smu.Unlock()
	if stopped != 1 {
		t.Fatalf("once the fade was heard the device stopped %d times, want 1", stopped)
	}
	_ = s.Resume()
	m.Mix(make([]float32, 2*m.Frames(SuspendFade/2)))
	if got := level(m); got <= 0.1 || got >= 0.4 {
		t.Errorf("half way through the fade in the sound is at %v, want about 0.25", got)
	}
	m.Mix(make([]float32, 2*m.Frames(SuspendFade)))
	if got := level(m); got != 0.5 {
		t.Errorf("after the fade in the sound is at %v, want 0.5", got)
	}
}

func TestResumeBeforeTheFadeEndsKeepsTheDeviceRunning(t *testing.T) {
	m := audio.NewMixer()
	m.Play(audio.NewClip(make([]float32, 2*audio.SampleRate)).Source(), audio.Options{})
	s, o := stillSpeaker(m)
	_ = s.Suspend()
	_ = s.Resume()
	time.Sleep(SuspendFade + 50*time.Millisecond)
	s.smu.Lock()
	defer s.smu.Unlock()
	if o.suspends != 0 || o.resumes != 0 {
		t.Errorf("the device stopped %d times and started %d, want neither", o.suspends, o.resumes)
	}
}
