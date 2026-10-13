//go:build linux && !android

package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/speaker"
)

// nullSink makes a PulseAudio sink that plays nowhere, for the test,
// and returns its name and a func that removes it.
func nullSink(t *testing.T) (name string, remove func()) {
	t.Helper()
	name = fmt.Sprintf("gunim_loopback_%d", os.Getpid())
	out, err := exec.CommandContext(t.Context(), "pactl", "load-module", "module-null-sink", "sink_name="+name).Output()
	if err != nil {
		t.Fatalf("making a null sink: %v", err)
	}
	module := strings.TrimSpace(string(out))
	return name, func() { _ = exec.CommandContext(context.Background(), "pactl", "unload-module", module).Run() }
}

// moveTo moves the speaker's stream, of the application app, to sink.
func moveTo(t *testing.T, app, sink string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	head := regexp.MustCompile(`^Sink Input #(\d+)`)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(t.Context(), "pactl", "list", "sink-inputs").Output()
		if err != nil {
			t.Fatal(err)
		}
		id := ""
		for line := range strings.SplitSeq(string(out), "\n") {
			if m := head.FindStringSubmatch(line); m != nil {
				id = m[1]
			}
			if strings.Contains(line, `application.name = "`+app+`"`) && id != "" {
				if err := exec.CommandContext(t.Context(), "pactl", "move-sink-input", id, sink).Run(); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("found no stream of %s to move", app)
}

// TestLoopbackThroughPulseAudio plays a click through the speakers
// into a sink that plays nowhere, records the sink's monitor, and
// measures how far the stamps are off, five times; then removes the
// sink while recording, which ends the stream. It needs a PulseAudio or PipeWire
// server, and runs where GUNIM_LOOPBACK=1.
func TestLoopbackThroughPulseAudio(t *testing.T) {
	if os.Getenv("GUNIM_LOOPBACK") != "1" {
		t.Skip("set GUNIM_LOOPBACK=1 to play into a null sink of PulseAudio's and record it")
	}
	sink, remove := nullSink(t)
	removed := false
	defer func() {
		if !removed {
			remove()
		}
	}()
	app := fmt.Sprintf("gunim-loopback-%d", os.Getpid())
	mix := audio.NewMixer()
	spk, err := speaker.Open(mix, speaker.Options{Name: app, Rate: 48000, Latency: 100 * time.Millisecond, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spk.Close() }()
	moveTo(t, app, sink)
	devs, err := Devices()
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, d := range devs {
		if d.ID == sink+".monitor" && d.Monitor {
			id = d.ID
		}
	}
	if id == "" {
		t.Fatalf("Devices lists no monitor of %s: %+v", sink, devs)
	}
	in, err := Open(Options{Device: id, Name: app, Speaker: spk})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	t.Logf("recording %+v", in.Info())
	// Stamps reckoned from the system's latencies settle over the
	// stream's first seconds.
	time.Sleep(2 * time.Second)
	offsets := make([]int64, 0, 5)
	for range 5 {
		m, err := Measure(in, mix, 0, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("offset %d frames (%v), round trip %v, level %.3f", m.Offset, audio.DurationAt(m.Offset, 48000), m.RoundTrip, m.Level)
		if m.Level < 0.4 {
			t.Errorf("the click came back at %v, want 0.5", m.Level)
		}
		offsets = append(offsets, m.Offset)
	}
	// The stamps come from the latencies the server reports, which
	// leave some out: PipeWire's server on the box this was written on
	// left out 40 ms. What they leave out holds steady, to within
	// 10 ms, so one measurement lines a recording up.
	lo, hi := offsets[0], offsets[0]
	for _, o := range offsets {
		lo, hi = min(lo, o), max(hi, o)
	}
	if hi-lo > 480 {
		t.Errorf("offsets %v: the stamps wander more than 10 ms", offsets)
	}
	remove()
	removed = true
	select {
	case <-in.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the stream recorded on after its source went")
	}
	if !errors.Is(in.Err(), ErrLost) {
		t.Fatalf("Err = %v, want ErrLost", in.Err())
	}
}
