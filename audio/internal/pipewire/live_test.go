//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// A real server, where libpipewire loads and a server listens. The
// client plays silence and records for a few seconds
// (GUNIM_PIPEWIRE_SECONDS sets how many). The output's cycles must
// come in time and never find the ring dry, Held must stay within the
// ring and the delay, and no input stamp may pass the frames the
// output took. Where the graph kept time, the input's stamps must move
// on with the frames read. Where no server listens, it skips.
func TestLiveServer(t *testing.T) {
	if !Available() {
		t.Skip("no PipeWire server listens")
	}
	seconds := 3
	if s, err := strconv.Atoi(os.Getenv("GUNIM_PIPEWIRE_SECONDS")); err == nil && s > 0 {
		seconds = s
	}
	cl, err := Open(Config{Name: "gunim-test", Rate: 48000, Quantum: 256, Record: true, Hold: time.Second, Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cl.Close(); err != nil {
			t.Error(err)
		}
	}()
	t.Logf("%+v", cl.Info())
	const ahead = 4800 // 100 ms
	silence := make([]float32, Channels*ahead)
	in := make([]float32, Channels*8192)
	start := time.Now()
	var read, stamps int64
	var firstAt, lastAt int64
	var firstRead int64
	var maxHeld int64
	var e Events
	for time.Since(start) < time.Duration(seconds)*time.Second {
		if q := cl.Queued(); q < ahead {
			cl.Write(silence[:Channels*(ahead-q)])
		}
		for {
			n, at, ok := cl.Read(in)
			if !ok {
				break
			}
			if taken := cl.Taken(); at+int64(n) > taken {
				t.Errorf("input stamped %d to %d, past the %d frames taken", at, at+int64(n), taken)
			}
			if stamps == 0 {
				firstAt, firstRead = at, read
			}
			lastAt = at - (read - firstRead)
			read += int64(n)
			stamps++
		}
		maxHeld = max(maxHeld, cl.Held())
		p := cl.Poll()
		e.Short += p.Short
		e.Lost += p.Lost
		e.Cycles, e.Quantum, e.DeviceRate, e.Delay, e.Out, e.In = p.Cycles, p.Quantum, p.DeviceRate, p.Delay, p.Out, p.In
		if p.Error != "" {
			t.Fatalf("the server failed a stream: %s", p.Error)
		}
		if p.Moved {
			t.Logf("streams %v and %v at %v", p.Out, p.In, time.Since(start).Round(time.Millisecond))
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("%d cycles of %d frames, the device at %d Hz, %d frames of delay; %d short; held at most %d; "+
		"%d frames recorded in %d reads, %d lost; stamps drifted %d frames",
		e.Cycles, e.Quantum, e.DeviceRate, e.Delay, e.Short, maxHeld, read, stamps, e.Lost, lastAt-firstAt)
	if e.Out != StateStreaming {
		t.Skipf("the output is %v: no device to play on", e.Out)
	}
	if want := int64(seconds) * 48000 / int64(max(e.Quantum, 1)) / 2; e.Cycles < want {
		t.Fatalf("%d cycles in %d s, want %d at least", e.Cycles, seconds, want)
	}
	if e.Short > 1 {
		t.Errorf("%d cycles found the ring dry", e.Short)
	}
	if maxHeld > ahead+int64(e.Delay)+2*int64(e.Quantum)+64 {
		t.Errorf("held %d, past the ring's %d, the delay's %d and the quantum's %d", maxHeld, ahead, e.Delay, e.Quantum)
	}
	// A graph that stalls, as a busy machine's may, misses time the
	// stamps count and the frames read do not: the stamps keep pace only
	// where the graph ran every cycle, within one of the time.
	kept := e.Cycles*int64(e.Quantum) >= int64(time.Since(start).Seconds()*48000)-2*int64(e.Quantum)
	switch {
	case stamps > 0 && kept:
		// The input's clock and the output's run at one rate: the stamps
		// keep pace with the frames read, within a cycle.
		if d := lastAt - firstAt; d > int64(e.Quantum)+64 || d < -int64(e.Quantum)-64 {
			t.Errorf("the stamps drifted %d frames from the frames read", d)
		}
	case stamps > 0:
		t.Logf("the graph ran %d cycles of %d frames in %v: no drift check", e.Cycles, e.Quantum, time.Since(start).Round(time.Millisecond))
	case e.In == StateStreaming:
		t.Error("the input streams and recorded nothing")
	}
}
