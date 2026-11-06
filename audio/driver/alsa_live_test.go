//go:build linux && !android && (amd64 || arm64)

package driver

import (
	"math"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestALSALive plays a quiet tone through a real card and records its
// inputs, where GUNIM_ALSA_LIVE names the card:
//
//	GUNIM_ALSA_LIVE="ALSA: M4" GUNIM_ALSA_BUFFER=64 go test -run ALSALive -v ./audio/driver
func TestALSALive(t *testing.T) {
	name := os.Getenv("GUNIM_ALSA_LIVE")
	if name == "" {
		t.Skip("GUNIM_ALSA_LIVE names no card")
	}
	buffer, _ := strconv.Atoi(os.Getenv("GUNIM_ALSA_BUFFER"))
	secs := 10
	if v, err := strconv.Atoi(os.Getenv("GUNIM_ALSA_SECONDS")); err == nil {
		secs = v
	}
	var fills, xruns, recorded atomic.Int64
	var slowest atomic.Int64
	var last time.Time
	ph := 0.0
	// -40 dB: a tone to count, not to listen to.
	const level = 0.01
	inPeak := make([]atomic.Uint32, 64)
	d, err := Open(Config{Name: name, Buffer: buffer, Rate: 48000,
		Fill: func(f []float32) {
			now := time.Now()
			if !last.IsZero() {
				if g := int64(now.Sub(last)); g > slowest.Load() {
					slowest.Store(g)
				}
			}
			last = now
			fills.Add(1)
			for i := 0; i+1 < len(f); i += 2 {
				v := float32(level * math.Sin(ph))
				ph += 2 * math.Pi * 440 / 48000
				f[i], f[i+1] = v, v
			}
		},
		Record: func(in []float32, ch int) {
			recorded.Add(1)
			for i := 0; i < len(in); i += ch {
				for c := range ch {
					v := math.Float32bits(float32(math.Abs(float64(in[i+c]))))
					if math.Float32frombits(v) > math.Float32frombits(inPeak[c].Load()) {
						inPeak[c].Store(v)
					}
				}
			}
		},
		Overrun: func() { xruns.Add(1) },
		Log:     t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	info := d.Info()
	t.Logf("%+v", info)
	time.Sleep(time.Duration(secs) * time.Second)
	if err := d.Close(); err != nil {
		t.Error(err)
	}
	want := int64(secs) * int64(info.Rate) / int64(info.Buffer)
	t.Logf("%d fills of %d expected, %d recorded, %d xruns, slowest gap %.2f ms against %.2f ms a buffer", fills.Load(), want, recorded.Load(), xruns.Load(),
		float64(slowest.Load())/1e6, float64(info.Buffer)/float64(info.Rate)*1000)
	for c := range info.Recorded {
		t.Logf("input %d peak %.1f dB", c+1, 20*math.Log10(float64(math.Float32frombits(inPeak[c].Load()))+1e-9))
	}
	if d := fills.Load() - want; d < -want/10 || d > want/10 {
		t.Errorf("%d fills, want about %d", fills.Load(), want)
	}
}
