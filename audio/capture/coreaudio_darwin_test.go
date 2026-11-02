//go:build darwin && !ios

package capture

import (
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/coreaudio"
	"github.com/marrasen/gunim/audio/internal/audiounit"
	"github.com/marrasen/gunim/audio/speaker"
)

// sine returns n frames of two channels: a tone of hz at rate on the
// first, from frame first on, and its inverse on the second.
func sine(first, n int, hz, rate float64) []float32 {
	out := make([]float32, 2*n)
	for i := range n {
		v := float32(0.5 * math.Sin(2*math.Pi*hz*float64(first+i)/rate))
		out[2*i], out[2*i+1] = v, -v
	}
	return out
}

// The converter makes, of a tone at one rate in blocks of every size,
// the same tone at the other, as many frames as the rates say, and
// stamps each piece with when its first frame came in, as the blocks
// it came of were stamped.
func TestConverterChangesTheRateAndKeepsTheStamps(t *testing.T) {
	for _, rates := range [][2]int{{44100, 48000}, {48000, 44100}, {96000, 48000}, {48000, 48000 * 2}} {
		from, to := rates[0], rates[1]
		c := newConverter(2, from, to, 512)
		step := float64(from) / float64(to)
		var made []float32
		in := 0
		sizes := []int{1, 7, 128, 513, 2000, 64, 3}
		for blk := 0; in < from; blk++ {
			k := sizes[blk%len(sizes)]
			now := in + k
			c.convert(sine(in, k, 1000, float64(from)), k, func(out []float32, ago int64) {
				o := len(made) / 2
				// Frame o made is frame o*step taken in, which came in
				// now-o*step frames ago.
				if want := (float64(now) - float64(o)*step) / step; math.Abs(float64(ago)-want) > 1 {
					t.Fatalf("%d to %d Hz: frame %d made is stamped %d frames ago, want %.1f", from, to, o, ago, want)
				}
				made = append(made, out...)
			})
			in = now
		}
		n := len(made) / 2
		// The last two frames taken in wait for the next, to make the
		// curve up to them.
		if want := float64(in) / step; math.Abs(float64(n)-want) > 3/step+1 {
			t.Errorf("%d to %d Hz: made %d frames of %d, want %.0f", from, to, n, in, want)
		}
		// Past the silence before the first frame, each frame made is
		// the tone at its time, and the second channel its inverse.
		worst := 0.0
		for o := 4; o < n; o++ {
			want := 0.5 * math.Sin(2*math.Pi*1000*float64(o)*step/float64(from))
			worst = max(worst, math.Abs(float64(made[2*o])-want))
			if made[2*o+1] != -made[2*o] {
				t.Fatalf("%d to %d Hz: frame %d's channels are %v and %v", from, to, o, made[2*o], made[2*o+1])
			}
		}
		if worst > 0.002 {
			t.Errorf("%d to %d Hz: the tone is off by as much as %.4f", from, to, worst)
		}
	}
}

// The converter takes more than four of the device's frames to a frame
// made, in blocks of every size, for ten seconds: it makes as many
// frames as the rates say, of the tone taken in, and each frame's stamp
// stays on when it came in.
func TestConverterTakesLargeSteps(t *testing.T) {
	for _, rates := range [][2]int{{192000, 44100}, {192000, 32000}, {48000, 8000}, {96000, 8000}} {
		from, to := rates[0], rates[1]
		step := float64(from) / float64(to)
		for _, sizes := range [][]int{{1}, {3}, {5}, {64}, {512}, {4096}, {1, 7, 128, 513, 2, 64, 3}} {
			t.Run(fmt.Sprintf("%d to %d in %v", from, to, sizes), func(t *testing.T) {
				c := newConverter(2, from, to, 512)
				made, in, late, off := 0, 0, 0.0, 0.0
				for blk := 0; in < 10*from; blk++ {
					k := sizes[blk%len(sizes)]
					now := in + k
					c.convert(sine(in, k, 100, float64(from)), k, func(out []float32, ago int64) {
						// Frame made is frame made*step taken in, which came
						// in now-made*step frames ago.
						want := (float64(now) - float64(made)*step) / step
						late = max(late, math.Abs(float64(ago)-want))
						for i := range len(out) / 2 {
							if o := made + i; o >= 4 {
								tone := 0.5 * math.Sin(2*math.Pi*100*float64(o)*step/float64(from))
								off = max(off, math.Abs(float64(out[2*i])-tone))
							}
						}
						made += len(out) / 2
					})
					in = now
				}
				if late > 1 {
					t.Errorf("a stamp is off by %.2f frames", late)
				}
				if off > 0.002 {
					t.Errorf("the tone is off by as much as %.4f", off)
				}
				if want := float64(in) / step; math.Abs(float64(made)-want) > 3/step+1 {
					t.Errorf("made %d frames of %d, want %.0f", made, in, want)
				}
			})
		}
	}
}

// The converter allocates nothing as it converts.
func TestConverterAllocatesNothing(t *testing.T) {
	c := newConverter(2, 44100, 48000, 512)
	in := sine(0, 4096, 1000, 44100)
	put := func([]float32, int64) {}
	if n := testing.AllocsPerRun(100, func() { c.convert(in, 4096, put) }); n != 0 {
		t.Errorf("%v allocations a call", n)
	}
}

// openDefault opens the default input as o says, skipping where there
// is none, or the system lets the test record nothing.
func openDefault(t *testing.T, o Options) *Stream {
	t.Helper()
	devs, err := Devices()
	if err != nil {
		t.Fatal(err)
	}
	def := false
	for _, d := range devs {
		t.Logf("%+v", d)
		def = def || d.Default
	}
	if !def {
		t.Skip("this machine has no default input")
	}
	in, err := Open(o)
	if errors.Is(err, ErrDenied) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	t.Logf("recording %+v", in.Info())
	return in
}

// readFor reads in for d, and returns how many frames came, checking
// they come in order, with no gap.
func readFor(t *testing.T, in *Stream, d time.Duration) int {
	t.Helper()
	buf := make([]float32, 4096*in.Info().Channels)
	got := 0
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		b := in.Read(buf)
		if b.Len() == 0 {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if b.Frame != int64(got) {
			t.Fatalf("a block came as frame %d after %d frames", b.Frame, got)
		}
		got += b.Len()
	}
	if err := in.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestMacInputsListAndRecord records the default input for a moment:
// blocks come in at its rate, of the channels asked for.
func TestMacInputsListAndRecord(t *testing.T) {
	in := openDefault(t, Options{Channels: []int{0}})
	if in.Info().Channels != 1 {
		t.Fatalf("records %d channels, want the one asked for", in.Info().Channels)
	}
	const rec = 500 * time.Millisecond
	got, want := readFor(t, in, rec), in.Info().Rate/2
	if got < want*8/10 || got > want*12/10+4*in.Info().Buffer {
		t.Fatalf("recorded %d frames in %v, want about %d", got, rec, want)
	}
}

// An input of the device the output plays through keeps the output's
// rate, and the stream converts it to the rate asked for.
func TestMacInputConvertsTheOutputsRate(t *testing.T) {
	out, err := audiounit.Open(audiounit.Config{Hold: time.Second})
	if err != nil {
		t.Skipf("no output to play through: %v", err)
	}
	defer func() { _ = out.Close() }()
	devs, _ := coreaudio.Inputs()
	id := ""
	for _, d := range devs {
		if d.Name == out.Info().Device {
			id = d.UID
		}
	}
	if id == "" {
		t.Skipf("%s, which plays, records nothing", out.Info().Device)
	}
	const rate = 44100
	in := openDefault(t, Options{Device: id, Rate: rate})
	if in.Info().Rate != rate {
		t.Fatalf("records at %d Hz, want %d", in.Info().Rate, rate)
	}
	if out.Info().Rate == rate {
		t.Skipf("the output plays at %d Hz itself", rate)
	}
	if in.Info().Problem == nil {
		t.Error("the stream converts and tells no Problem")
	}
	const rec = time.Second
	got := readFor(t, in, rec)
	if got < rate*8/10 || got > rate*12/10 {
		t.Fatalf("recorded %d frames in %v, want about %d", got, rec, rate)
	}
}

// TestLoopbackThroughCoreAudio plays a click through the speakers and
// finds it in the default input, five times, to tell how far the stamps
// are off and that they hold steady. The output must reach the input:
// a cable, a microphone near a speaker, or an interface's own loopback,
// as the MOTU M4's third and fourth inputs. It runs where
// GUNIM_LOOPBACK=1, and finds the click in whichever channel hears it.
func TestLoopbackThroughCoreAudio(t *testing.T) {
	if os.Getenv("GUNIM_LOOPBACK") != "1" {
		t.Skip("set GUNIM_LOOPBACK=1 to play a click into the default input and record it")
	}
	mix := audio.NewMixer()
	spk, err := speaker.Open(mix, speaker.Options{Name: "gunim-loopback", Rate: 48000, Buffer: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spk.Close() }()
	in := openDefault(t, Options{Speaker: spk})
	time.Sleep(2 * time.Second)
	channel := -1
	offsets := make([]int64, 0, 5)
	for range 5 {
		var m Measurement
		err := ErrNoClick
		for c := range in.Info().Channels {
			if channel >= 0 && c != channel {
				continue
			}
			if m, err = Measure(in, mix, c, time.Second); err == nil {
				channel = c
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("channel %d: offset %d frames (%v), round trip %v, level %.3f", channel, m.Offset,
			audio.DurationAt(m.Offset, 48000), m.RoundTrip, m.Level)
		offsets = append(offsets, m.Offset)
	}
	lo, hi := offsets[0], offsets[0]
	for _, o := range offsets {
		lo, hi = min(lo, o), max(hi, o)
	}
	if hi-lo > 48 {
		t.Errorf("offsets %v: the stamps wander more than a millisecond", offsets)
	}
}
