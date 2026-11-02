//go:build darwin && !ios && (amd64 || arm64)

package audiounit

import (
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

// The default output device, where the Mac has one: the client plays
// silence for a few seconds (GUNIM_AU_SECONDS sets how many), the device
// takes every frame at its rate, and the frame heard, as the buffers'
// time stamps place it, moves on with the clock, a delay behind the
// frames written that agrees with the device's latencies. With
// GUNIM_AU_RECORD=1 it records the default input too, and the stamps
// move on with the output's; macOS asks for leave to record the first
// time. Where the Mac has no output device, as a build machine, it
// skips.
func TestLiveDevice(t *testing.T) {
	seconds := 2
	if s, err := strconv.Atoi(os.Getenv("GUNIM_AU_SECONDS")); err == nil && s > 0 {
		seconds = s
	}
	record := os.Getenv("GUNIM_AU_RECORD") == "1"
	cl, err := Open(Config{Record: record, Buffer: 128, Hold: time.Second, Log: t.Logf})
	if errors.Is(err, ErrNoOutput) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cl.Close(); err != nil {
			t.Error(err)
		}
		if err := cl.Close(); err != nil {
			t.Errorf("a second Close: %v", err)
		}
	}()
	info := cl.Info()
	t.Logf("%+v", info)
	if info.Rate <= 0 || info.Buffer <= 0 {
		t.Fatalf("rate %d, buffer %d", info.Rate, info.Buffer)
	}
	silence := make([]float32, 2048*cl.Channels())
	in := make([]float32, 4096*max(cl.Inputs(), 1))
	begin := time.Now()
	var worst, sum, samples int64
	var lastHeard, lastAt int64 = -1, -1
	stamps := 0
	for time.Since(begin) < time.Duration(seconds)*time.Second {
		target := int64(info.Rate / 20) // 50 ms ahead
		for cl.Queued() < target {
			cl.Write(silence)
		}
		if heard, ok := cl.Heard(); ok {
			if heard < lastHeard {
				t.Fatalf("heard went back from %d to %d", lastHeard, heard)
			}
			lastHeard = heard
			held := cl.Written() - heard
			// What the time stamps say against what the latencies add
			// up to: Core Audio asks for a buffer about a buffer and the
			// safety offset before its time, so the two differ by under
			// a buffer, as the moment between buffers moves.
			d := held - (cl.Queued() + int64(cl.Delay()))
			sum += d
			samples++
			worst = max(worst, d, -d)
		}
		for {
			n, at, ok := cl.Read(in)
			if !ok {
				break
			}
			if n > 0 && at < lastAt {
				t.Errorf("an input stamp went back from %d to %d", lastAt, at)
			}
			lastAt = at
			stamps++
		}
		e := cl.Poll()
		if e.Reopen != "" {
			t.Logf("the device changed: %s", e.Reopen)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	e := cl.Poll()
	taken := cl.Written() - cl.Queued()
	want := int64(seconds) * int64(info.Rate)
	t.Logf("took %d frames in %v (%d expected), %d buffers, %d short; held off the latencies by %d frames at "+
		"worst, %d on average; %d input blocks",
		taken, time.Since(begin).Round(time.Millisecond), want, e.Cycles, e.Short, worst, sum/max(samples, 1), stamps)
	if taken < want*8/10 {
		t.Errorf("the device took %d frames in %d s, want about %d", taken, seconds, want)
	}
	if samples == 0 {
		t.Fatal("no buffer carried a time stamp")
	}
	if worst > int64(2*info.Buffer+info.Rate/100) {
		t.Errorf("the time stamps place the frame heard %d frames off the latencies, more than two buffers", worst)
	}
	if record && info.InputProblem == nil && stamps == 0 {
		t.Error("the input handed on nothing")
	}
}
