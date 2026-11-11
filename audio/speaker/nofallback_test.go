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
	fail, works, heal func(t *testing.T)
	reopen            func(s *Speaker) error
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
		heal:   func(*testing.T) { was.failOpen.Store(0) },
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
		heal: func(*testing.T) {
			au.mu.Lock()
			au.failed = nil
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
		heal:   func(*testing.T) { aa.failOut.Store(0) },
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
// tells of it in the state. Set with the same options, Reopen and
// Resume each try again, and play once the backend works.
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
				if err := b.reopen(s); err == nil {
					t.Error("Set with the same options returned no error, though nothing plays")
				}
				// Reopen and Resume fail the same way.
				s.Reopen()
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
				// The backend back, Set with the same options plays again.
				b.heal(t)
				if err := b.reopen(s); err != nil {
					t.Fatalf("Set with the backend back: %v", err)
				}
				if out := s.out.Load(); out == nil || !b.plays(out.output) || s.State().Problem != nil {
					t.Fatalf("after Set, it plays through %v and tells of %v", out, s.State().Problem)
				}
				// Failed again, Reopen plays once the backend is back.
				b.fail(t)
				reopen(s)
				if s.out.Load() != nil {
					t.Fatal("Reopen opened the output, though the backend failed")
				}
				if n := failedOpens(s); n != 1 {
					t.Fatalf("after Reopen, %d opens failed, want 1", n)
				}
				b.heal(t)
				reopen(s)
				if out := s.out.Load(); out == nil || !b.plays(out.output) {
					t.Fatalf("after Reopen with the backend back, it plays through %v", out)
				}
			})
		})
	}
}

// reopen asks s to open its output again and waits until it has tried: an
// output closing waits on its device, wasWait at most, which
// synctest.Wait alone takes for done.
func reopen(s *Speaker) {
	s.Reopen()
	time.Sleep(2 * wasWait)
	synctest.Wait()
}

// failedOpens returns how many opens of s failed in a row.
func failedOpens(s *Speaker) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fails
}

// An output that fails to open again, as a device goes or changes its
// rate, opens by itself once the backend works again, with no call
// from the application. While it fails, it tries less and less often,
// and not at all while suspended or after Close.
func TestAFailedOpenTriesAgainByItself(t *testing.T) {
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
				fails := func() int {
					s.mu.Lock()
					defer s.mu.Unlock()
					return s.fails
				}
				b.fail(t)
				// As the backend asks, as on a device gone.
				s.askReset()
				time.Sleep(10 * time.Second)
				synctest.Wait()
				// The first and then 0.25, 0.5, 1, 2, 2, 2 and 2 s on.
				if n := fails(); n < 5 || n > 9 {
					t.Errorf("in 10 s it tried %d times, want 5 to 9", n)
				}
				// Suspended, it waits for Resume.
				if err := s.Suspend(); err != nil {
					t.Fatal(err)
				}
				n := fails()
				time.Sleep(10 * time.Second)
				synctest.Wait()
				if m := fails(); m != n {
					t.Errorf("suspended, it tried %d times", m-n)
				}
				if err := s.Resume(); err == nil {
					t.Error("Resume opened the output, though the backend failed")
				}
				b.heal(t)
				select {
				case <-s.Changed():
				case <-time.After(3 * time.Second):
					t.Fatal("the backend back, the output opened not in 3 s")
				}
				if out := s.out.Load(); out == nil || !b.plays(out.output) || s.State().Problem != nil {
					t.Fatalf("it plays through %v and tells of %v", out, s.State().Problem)
				}
				if n := fails(); n != 0 {
					t.Errorf("playing, it counts %d failed opens", n)
				}
				// Closed, it tries nothing, and leaves no goroutine:
				// synctest fails a bubble that ends with one running.
				b.fail(t)
				s.askReset()
				synctest.Wait()
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Second)
				synctest.Wait()
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
