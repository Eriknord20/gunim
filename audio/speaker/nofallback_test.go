package speaker

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/audiounit"
)

// A backend is one of gunim's own, as the system's own sound plays it.
type backend struct {
	name string
	// first has it play where no driver is named, as on its system.
	first func(t *testing.T)
	// fail has it fail every open from then on, and works sets it
	// working; reopen asks an open speaker for a change that opens it
	// again.
	fail, works func(t *testing.T)
	reopen      func(s *Speaker) error
	// plays says the output is the backend's.
	plays func(o output) bool
}

// backends are the backends with nothing to fall back on: Windows',
// macOS' and Android's from 8.1.
func backends() []backend {
	var was *fakeWas
	var au *fakeAU
	var aa *fakeAA
	return []backend{{
		name:  driver.WASAPI,
		plays: func(o output) bool { _, ok := o.(*wasOutput); return ok },
		first: func(t *testing.T) { onlyFirst(t, &wasapiFirst) },
		works: func(t *testing.T) {
			was = &fakeWas{rate: 48000, period: 480, buffer: 960, latency: 480, chans: 2}
			useFakeWas(t, was)
		},
		fail:   func(*testing.T) { was.failOpen.Store(1 << 30) },
		reopen: func(s *Speaker) error { return s.Set(Options{Record: true}) },
	}, {
		name:  driver.CoreAudio,
		plays: func(o output) bool { _, ok := o.(*auOutput); return ok },
		first: func(t *testing.T) { onlyFirst(t, &coreAudioFirst) },
		works: func(t *testing.T) { au = useAU(t, macInfo()) },
		fail: func(*testing.T) {
			au.mu.Lock()
			au.failed = audiounit.ErrNoOutput
			au.mu.Unlock()
		},
		reopen: func(s *Speaker) error { return s.Set(Options{Buffer: 256}) },
	}, {
		name:  driver.AAudio,
		plays: func(o output) bool { _, ok := o.(*aaOutput); return ok },
		first: func(t *testing.T) { onlyFirst(t, &aaudioFirst) },
		works: func(t *testing.T) {
			aa = &fakeAA{rate: 48000, burst: 240}
			useFakeAA(t, aa)
		},
		fail:   func(*testing.T) { aa.failOut.Store(1 << 30) },
		reopen: func(s *Speaker) error { return s.Set(Options{Record: true}) },
	}}
}

// onlyFirst has the backend first says play where no driver is named,
// and no other.
func onlyFirst(t *testing.T, first *bool) {
	t.Helper()
	wasAA, wasCA, wasWA, wasPW := aaudioFirst, coreAudioFirst, wasapiFirst, pipeWireFirst
	aaudioFirst, coreAudioFirst, wasapiFirst, pipeWireFirst = false, false, false, false
	*first = true
	t.Cleanup(func() { aaudioFirst, coreAudioFirst, wasapiFirst, pipeWireFirst = wasAA, wasCA, wasWA, wasPW })
}

// noBackend has no backend of gunim's own play where no driver is
// named, as in the browser or on a BSD.
func noBackend(t *testing.T) {
	t.Helper()
	var none bool
	onlyFirst(t, &none)
}

// noSystemSound fails the test where the system's sound opens.
func noSystemSound(t *testing.T) {
	t.Helper()
	was := openSystemSound
	t.Cleanup(func() { openSystemSound = was })
	openSystemSound = func(*Speaker, Options) (startedOutput, error) {
		t.Error("the system's sound opened")
		return nil, errors.New("no")
	}
}

// Where the system's own backend fails to open, Open returns its error.
func TestADefaultBackendThatFailsReturnsItsError(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b.first(t)
				b.works(t)
				b.fail(t)
				noSystemSound(t)
				s, err := Open(audio.NewMixer(), Options{})
				if err == nil {
					_ = s.Close()
					t.Fatal("Open played, though the backend failed")
				}
			})
		})
	}
}

// Named, the system's own backend that fails to open returns its error
// too.
func TestANamedBackendThatFailsReturnsItsError(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b.first(t)
				b.works(t)
				b.fail(t)
				noSystemSound(t)
				s, err := Open(audio.NewMixer(), Options{Driver: b.name})
				if err == nil {
					_ = s.Close()
					t.Fatal("Open played, though the backend failed")
				}
			})
		})
	}
}

// A named driver that fails to open plays through the system's own
// backend, and the state says why. Where that fails too, Open returns
// both errors.
func TestANamedDriverThatFailsPlaysThroughTheBackend(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b.first(t)
				b.works(t)
				noSystemSound(t)
				wasPull := openPull
				t.Cleanup(func() { openPull = wasPull })
				openPull = func(driver.Config) (pullDevice, error) { return nil, errors.New("unplugged") }
				s, err := Open(audio.NewMixer(), Options{Driver: "M4"})
				if err != nil {
					t.Fatal(err)
				}
				st := s.State()
				if out := s.out.Load(); !b.plays(out.output) || st.Problem == nil ||
					!strings.Contains(st.Problem.Error(), "unplugged") {
					t.Errorf("it plays through %T, and tells of %v", out.output, st.Problem)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				b.fail(t)
				s, err = Open(audio.NewMixer(), Options{Driver: "M4"})
				if err == nil {
					_ = s.Close()
					t.Fatal("Open played, though the driver and the backend failed")
				}
				if !strings.Contains(err.Error(), "unplugged") {
					t.Errorf("Open returned %v, which tells nothing of the driver", err)
				}
			})
		})
	}
}

// An output open that opens again, as on a change Set asks, Reopen or
// the device going, where the backend fails, returns the error and
// tells of it in the state.
func TestOpeningAgainWhereTheBackendFailsSaysWhy(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b.first(t)
				b.works(t)
				noSystemSound(t)
				s, err := Open(audio.NewMixer(), Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = s.Close() }()
				b.fail(t)
				if err := b.reopen(s); err == nil {
					t.Fatal("it opened again, though the backend failed")
				}
				if st := s.State(); st.Problem == nil || st.Driver != "" {
					t.Errorf("it plays through %q, and tells of %v", st.Driver, st.Problem)
				}
				if s.Err() == nil {
					t.Error("Err tells of nothing")
				}
				// Reopen and Resume, which open the output on the speaker's
				// own goroutine, fail the same way.
				s.Reopen()
				time.Sleep(time.Second)
				synctest.Wait()
				if err := s.Suspend(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second)
				s.Reopen()
				time.Sleep(time.Second)
				synctest.Wait()
				if err := s.Resume(); err == nil {
					t.Error("Resume opened the output, though the backend failed")
				}
				if s.State().Problem == nil {
					t.Error("after Resume, the state tells of no problem")
				}
			})
		})
	}
}

// Android 8.0 has no AAudio: the speaker plays through the system's
// sound, oto, and the state says why.
func TestWithoutAAudioTheSystemsSoundPlays(t *testing.T) {
	onlyFirst(t, &aaudioFirst)
	was, wasSys := openAAStream, openSystemSound
	t.Cleanup(func() { openAAStream, openSystemSound = was, wasSys })
	openAAStream = nil
	opened := 0
	openSystemSound = func(_ *Speaker, o Options) (startedOutput, error) {
		opened++
		return &stillOut{hz: o.rate()}, nil
	}
	for _, name := range []string{"", driver.AAudio} {
		s, err := Open(audio.NewMixer(), Options{Driver: name})
		if err != nil {
			t.Fatal(err)
		}
		if s.State().Problem == nil {
			t.Errorf("named %q, the state tells of no problem", name)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if opened != 2 {
		t.Errorf("the system's sound opened %d times, want 2", opened)
	}
}

// Where the system has no backend of gunim's own, as the browser,
// the system's sound plays, a named driver that fails too.
func TestWithNoBackendTheSystemsSoundPlays(t *testing.T) {
	noBackend(t)
	wasSys, wasPull := openSystemSound, openPull
	t.Cleanup(func() { openSystemSound, openPull = wasSys, wasPull })
	openPull = func(driver.Config) (pullDevice, error) { return nil, errors.New("unplugged") }
	opened := 0
	openSystemSound = func(_ *Speaker, o Options) (startedOutput, error) {
		opened++
		return &stillOut{hz: o.rate()}, nil
	}
	for _, name := range []string{"", "M4"} {
		s, err := Open(audio.NewMixer(), Options{Driver: name})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if opened != 2 {
		t.Errorf("the system's sound opened %d times, want 2", opened)
	}
}
