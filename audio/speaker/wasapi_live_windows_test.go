package speaker

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/driver"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/internal/wasapi"
)

// liveDevice opens the default output and returns what it says of
// itself. It skips where Windows has no output, as a build server has
// none, and fails on any other error, as of a bug in the COM code.
func liveDevice(t *testing.T) wasapi.Info {
	t.Helper()
	got := make(chan wasapi.Info, 1)
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		d, err := wasapi.Open(wasapi.Config{Record: true})
		if err != nil {
			errc <- err
			return
		}
		got <- d.Info()
		d.Close()
	}()
	select {
	case err := <-errc:
		// ErrLost stands for E_NOTFOUND, AUDCLNT_E_DEVICE_INVALIDATED
		// and AUDCLNT_E_SERVICE_NOT_RUNNING.
		if errors.Is(err, wasapi.ErrLost) {
			t.Skipf("no default output: %v", err)
		}
		t.Fatalf("opening the default output: %v", err)
	case info := <-got:
		t.Logf("%+v", info)
		return info
	}
	return wasapi.Info{}
}

// TestWASAPIPlaysOnThisMachine plays silence through the default output
// for two seconds, recording the default input where there is one: the
// frame heard moves on at the device's rate, the delay is sane, and
// packets come in.
func TestWASAPIPlaysOnThisMachine(t *testing.T) {
	info := liveDevice(t)
	m := audio.NewMixer()
	s, err := Open(m, Options{Driver: driver.WASAPI, Record: info.InputErr == nil})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st := s.State()
	t.Logf("state %+v, info %+v", st, st.Info)
	if st.Driver != driver.WASAPI || st.Rate != info.Rate {
		t.Fatalf("plays through %q at %d Hz, want WASAPI at %d", st.Driver, st.Rate, info.Rate)
	}
	var packets atomic.Int64
	if tap := duplex.Current(); tap != nil {
		detach := tap.Attach(func([]float32, int, int64) { packets.Add(1) })
		defer detach()
	}
	time.Sleep(500 * time.Millisecond)
	h0, t0 := m.Heard(), time.Now()
	time.Sleep(2 * time.Second)
	frames, took := m.Heard()-h0, time.Since(t0)
	want := audio.FramesAt(took, info.Rate)
	if frames < want*9/10 || frames > want*11/10 {
		t.Errorf("in %v the speaker heard %d frames, want about %d", took, frames, want)
	}
	if l := s.Latency(); l <= 0 || l > 200*time.Millisecond {
		t.Errorf("the latency is %v", l)
	}
	st = s.State()
	t.Logf("delay %v, latency %v, dropouts %d, packets %d", audio.DurationAt(int64(st.Info.Latency), st.Rate),
		s.Latency(), st.Dropouts, packets.Load())
	if duplex.Current() != nil && packets.Load() == 0 {
		t.Error("the input handed on no packets")
	}
}
