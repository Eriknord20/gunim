package speaker

import (
	"errors"
	"testing"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
)

// useFakeSystem puts a fake device in place of the system's own sound
// for the test; fail has it fail to open.
func useFakeSystem(t *testing.T, info driver.Info, fail bool) *fakeDriver {
	t.Helper()
	f := &fakeDriver{info: info}
	was := openSystem
	openSystem = func(c driver.Config) (pullDevice, error) {
		if fail {
			return nil, errors.New("no device")
		}
		return f.open(c)
	}
	t.Cleanup(func() { openSystem = was })
	return f
}

// TestTheSystemsSoundMixesOnItsThread plays the system's own sound with
// a buffer chosen: the device's thread mixes each buffer as it takes it,
// at the buffer asked, the state telling no driver, and a new buffer
// opens it again.
func TestTheSystemsSoundMixesOnItsThread(t *testing.T) {
	f := useFakeSystem(t, driver.Info{Driver: "Speakers", Rate: 48000, Buffer: 128, Latency: 419, Bits: 32, Float: true}, false)
	m := audio.NewMixer()
	m.Play(steady(0.25), audio.Options{})
	s, err := Open(m, Options{Buffer: 128, Direct: true, Fixed: true, Outputs: []int{4, 5}, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f.mu.Lock()
	cfg := f.cfg
	f.mu.Unlock()
	if cfg.Name != "" || cfg.Buffer != 128 || cfg.Outputs != nil || cfg.Record != nil || cfg.Fill == nil {
		t.Errorf("opened with %+v", cfg)
	}
	st := s.State()
	if st.Driver != "" || st.Info == nil || st.Info.Buffer != 128 || st.Aux != 0 {
		t.Errorf("state %+v", st)
	}
	if want := audio.DurationAt(419, 48000); st.Latency != want {
		t.Errorf("latency %v, want the device's %v", st.Latency, want)
	}
	// The voice fades in over its first frames: read past them.
	for range 20 {
		f.take()
	}
	for i, v := range f.take() {
		if v != 0.25 {
			t.Fatalf("sample %d is %v, want 0.25", i, v)
		}
	}

	if err := s.Set(Options{Buffer: 64, Direct: true, Fixed: true}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	opens, buffer := f.opens, f.cfg.Buffer
	f.mu.Unlock()
	if opens != 2 || buffer != 64 {
		t.Errorf("after a new buffer: opened %d times, last with %d frames", opens, buffer)
	}
}

// TestTheSystemsSoundFallsBackToOto plays through oto where the
// system's device fails to open, the state telling why.
func TestTheSystemsSoundFallsBackToOto(t *testing.T) {
	useFakeSystem(t, driver.Info{}, true)
	m := audio.NewMixer()
	s, err := Open(m, Options{Buffer: 128, Direct: true})
	if err != nil {
		t.Skipf("no system sound here: %v", err)
	}
	defer func() { _ = s.Close() }()
	if _, ok := s.out.Load().output.(*otoOutput); !ok {
		t.Errorf("plays through %T, want oto", s.out.Load().output)
	}
	if st := s.State(); st.Problem == nil || st.Problem.Error() != "no device" || st.Info != nil {
		t.Errorf("state %+v", st)
	}
}
