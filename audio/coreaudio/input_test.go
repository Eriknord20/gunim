//go:build darwin && !ios

package coreaudio

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// The inputs list with their UIDs, and the default one records at the
// device's pace, each buffer stamped as having come in after its frames
// did, and stops handing them on once it closes. A machine without an
// input skips.
func TestInputsListAndRecord(t *testing.T) {
	devs, err := Inputs()
	if err != nil {
		t.Fatal(err)
	}
	var def *InputDevice
	for i, d := range devs {
		t.Logf("%+v", d)
		if d.UID == "" || d.Channels <= 0 || d.Rate <= 0 {
			t.Errorf("input %+v", d)
		}
		if d.Default {
			def = &devs[i]
		}
	}
	if def == nil {
		t.Skip("this machine has no default input")
	}
	var frames, calls, early, peak atomic.Int64
	in, err := OpenInput(InputConfig{Device: def.UID, Buffer: 128, Log: t.Logf, Record: func(f []float32, ago int) {
		calls.Add(1)
		n := len(f) / def.Channels
		frames.Add(int64(n))
		if ago < n {
			early.Add(1)
		}
		for _, s := range f {
			peak.Store(max(peak.Load(), int64(max(s, -s)*1e6)))
		}
	}})
	if errors.Is(err, ErrDenied) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	info := in.Info()
	t.Logf("recording %+v", info)
	if info.UID != def.UID || info.Channels != def.Channels || info.Rate <= 0 || info.Latency < info.Buffer {
		t.Fatalf("info %+v of %+v", info, *def)
	}
	const rec = 500 * time.Millisecond
	time.Sleep(rec)
	if err := in.Err(); err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-in.Done():
	default:
		t.Error("Done is open after Close")
	}
	if in.Err() != nil {
		t.Errorf("Err %v after Close", in.Err())
	}
	got, want := frames.Load(), int64(info.Rate)*int64(rec)/int64(time.Second)
	if got < want*8/10 || got > want*12/10+int64(info.Buffer)*4 {
		t.Errorf("recorded %d frames in %v, want about %d", got, rec, want)
	}
	if n := early.Load(); n > 0 {
		t.Errorf("%d buffers stamped as handed on before their frames came in", n)
	}
	t.Logf("%d buffers, peak %.6f", calls.Load(), float64(peak.Load())/1e6)
	n := calls.Load()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != n {
		t.Error("recorded after Close")
	}
}

// Two inputs of one device record at once, and each closes on its own.
func TestTwoInputsOfOneDevice(t *testing.T) {
	var a, b atomic.Int64
	ia, err := OpenInput(InputConfig{Record: func(f []float32, _ int) { a.Add(int64(len(f))) }})
	if errors.Is(err, ErrDenied) {
		t.Skip(err)
	}
	if err != nil {
		t.Skipf("no input to record: %v", err)
	}
	ib, err := OpenInput(InputConfig{Record: func(f []float32, _ int) { b.Add(int64(len(f))) }})
	if err != nil {
		_ = ia.Close()
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := ia.Close(); err != nil {
		t.Fatal(err)
	}
	before := b.Load()
	time.Sleep(200 * time.Millisecond)
	if err := ib.Close(); err != nil {
		t.Fatal(err)
	}
	if a.Load() == 0 || before == 0 || b.Load() == before {
		t.Errorf("recorded %d and %d, then %d more on the second alone", a.Load(), before, b.Load()-before)
	}
}

// An input of a device that is not there fails to open.
func TestNoSuchInput(t *testing.T) {
	if _, err := OpenInput(InputConfig{Device: "no such device", Record: func([]float32, int) {}}); err == nil {
		t.Fatal("an input that is not there opened")
	}
}
