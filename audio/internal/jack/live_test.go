//go:build linux && !android && (amd64 || arm64)

package jack

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// A real server, where one answers: jackd, or PipeWire's, as under
// pw-jack. The client plays silence and records for a few seconds
// (GUNIM_JACK_SECONDS sets how many), and the server takes every frame
// in time, the inputs' stamps move on with the outputs, and the ports
// connect to the system's. Where no server answers, it skips.
func TestLiveServer(t *testing.T) {
	start := time.Now()
	if !Available() {
		t.Skipf("no JACK server answers (asked in %v)", time.Since(start).Round(time.Millisecond))
	}
	seconds := 3
	if s, err := strconv.Atoi(os.Getenv("GUNIM_JACK_SECONDS")); err == nil && s > 0 {
		seconds = s
	}
	cl, err := Open(Config{Name: "gunim-test", Record: true, Hold: time.Second, Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cl.Close(); err != nil {
			t.Error(err)
		}
	}()
	info := cl.Info()
	t.Logf("%+v", info)
	if info.Rate <= 0 || info.Buffer <= 0 {
		t.Fatalf("rate %d, buffer %d", info.Rate, info.Buffer)
	}
	for k, p := range info.Played {
		if p < 0 {
			continue
		}
		links := cl.a.connections(cl.outPorts[k])
		if len(links) != 1 || links[0] != info.Outputs[p] {
			t.Errorf("out_%d connects to %v, want %s", k+1, links, info.Outputs[p])
		}
	}
	silence := make([]float32, cl.Channels()*256)
	in := make([]float32, max(1, cl.Inputs())*maxBuffer)
	var short, xruns, lost int64
	var lastAt int64 = -1
	read := 0
	t0 := time.Now()
	// The buffers before the first write ran short: the first Poll
	// tells of them, and is let be.
	first := true
	for time.Since(t0) < time.Duration(seconds)*time.Second {
		for cl.Queued() < int64(4*cl.Buffer()) {
			cl.Write(silence)
		}
		for {
			n, at, ok := cl.Read(in)
			if !ok {
				break
			}
			if at < lastAt {
				t.Fatalf("an input stamp went back: %d after %d", at, lastAt)
			}
			lastAt = at
			read += n
		}
		e := cl.Poll()
		if !first {
			short += e.Short
		}
		first = false
		xruns += e.Xruns
		lost += e.Lost
		if e.Shutdown {
			t.Fatal("the server shut down")
		}
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(t0)
	taken := cl.Taken()
	want := int64(elapsed.Seconds() * float64(info.Rate))
	t.Logf("took %d frames in %v (%d expected), read %d; %d short, %d xruns, %d lost; since cycle %d; latency %d out, %d in",
		taken, elapsed.Round(time.Millisecond), want, read, short, xruns, lost, cl.SinceCycle(), info.Latency, info.InputLatency)
	if taken < want*9/10 || taken > want*11/10 {
		t.Errorf("the server took %d frames in %v, want about %d", taken, elapsed, want)
	}
	if short > 1 {
		t.Errorf("%d buffers ran short", short)
	}
	if cl.Inputs() > 0 && read == 0 {
		t.Error("nothing recorded")
	}
}
