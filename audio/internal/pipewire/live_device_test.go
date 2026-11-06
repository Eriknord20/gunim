//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveDevice plays a quiet tone at a different level on each channel of
// the device GUNIM_PIPEWIRE_DEVICE names part of, and records its inputs:
//
//	GUNIM_PIPEWIRE_DEVICE=M4 GUNIM_PIPEWIRE_SECONDS=5 go test -run LiveDevice -v ./audio/internal/pipewire
//
// To see the channels land one for one, record the device's monitor as it
// plays, with parec.
func TestLiveDevice(t *testing.T) {
	want := os.Getenv("GUNIM_PIPEWIRE_DEVICE")
	if want == "" {
		t.Skip("GUNIM_PIPEWIRE_DEVICE names no device")
	}
	ds, err := Devices()
	if err != nil {
		t.Fatal(err)
	}
	var d Device
	for _, x := range ds {
		t.Logf("device %q: %d out, %d in, %s", x.Name, len(x.OutPositions), len(x.InPositions), x.Output)
		if strings.Contains(x.Name, want) {
			d = x
		}
	}
	if d.Output == "" {
		t.Fatalf("no device of %q", want)
	}
	secs := 5 * time.Second
	if v, err := time.ParseDuration(os.Getenv("GUNIM_PIPEWIRE_SECONDS") + "s"); err == nil {
		secs = v
	}
	cl, err := Open(Config{Name: "gunim live device", Rate: 48000, Quantum: 256, Hold: 200 * time.Millisecond, Record: d.Input != "",
		Output: d.Output, Input: d.Input, OutPositions: d.OutPositions, InPositions: d.InPositions})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	ch := cl.Info().OutChannels
	in := cl.Info().InChannels
	buf := make([]float32, ch*128)
	rec := make([]float32, in*8192)
	var got, ph int
	end := time.Now().Add(secs)
	for time.Now().Before(end) {
		for cl.Queued() < 1024 {
			for i := range 128 {
				v := float32(math.Sin(2 * math.Pi * 440 * float64(ph) / 48000))
				ph++
				for c := range ch {
					// Each channel at twice the level of the one before, from -46 dB.
					buf[i*ch+c] = v * 0.005 * float32(int(1)<<c)
				}
			}
			cl.Write(buf)
		}
		if n, _, ok := cl.Read(rec); ok {
			got += n
		}
		time.Sleep(time.Millisecond)
	}
	e := cl.Poll()
	t.Logf("%d channels out, %d in: %d frames taken, %d recorded, %d short cycles, states %v %v", ch, in, cl.Taken(), got, e.Short, e.Out, e.In)
	if cl.Taken() == 0 {
		t.Error("the device took nothing")
	}
}
