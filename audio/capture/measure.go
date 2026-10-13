package capture

import (
	"errors"
	"io"
	"math"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
)

// A Measurement is what [Measure] found.
type Measurement struct {
	// Offset is how many mixer frames after the click was heard the
	// stream's stamps place it: zero where the devices report their
	// latencies right. Subtract it from a block's Heard to line the
	// recording up with what played.
	Offset int64
	// RoundTrip is how long the click took from the mixer, out of the
	// speakers and back in at the input, to the stream: the speaker's
	// latency and the stream's, as they report them, put right by
	// Offset.
	RoundTrip time.Duration
	// Level is the click's peak as recorded, from 0 to 1.
	Level float32
}

// ErrNoClick is returned by [Measure] where the click stood out from
// none of what was recorded: the input hears too little of the output.
var ErrNoClick = errors.New("capture: the click was not heard in the recording")

// Measure plays a click through mix, the mixer the stream's speaker
// plays, and finds it in channel of the recording, to tell how far the
// stream's stamps are off. The output must reach the input: a cable
// from an output to an input, or a microphone near a speaker. Measure
// reads the stream itself, so it must be one read through
// [Stream.Read], with a speaker, and nothing else may read it
// meanwhile. It gives up after timeout, or 3 seconds where timeout is
// zero. Stamps reckoned from the system's latencies settle over a
// stream's first seconds: measure a stream open a few seconds.
func Measure(s *Stream, mix *audio.Mixer, channel int, timeout time.Duration) (Measurement, error) {
	if s.q == nil || s.clock == nil {
		return Measurement{}, errors.New("capture: Measure needs a stream read through Read, with a speaker")
	}
	if channel < 0 || channel >= s.info.Channels {
		return Measurement{}, errors.New("capture: Measure was given a channel the stream records none of")
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ch := s.info.Channels
	buf := make([]float32, 1024*ch)
	for s.Read(buf).Len() > 0 {
	}
	rate := mix.Rate()
	c := &click{mix: mix, n: max(1, rate/1000)}
	c.at.Store(-1)
	v := mix.Play(c, audio.Options{})
	defer v.Stop(0)
	// window is how far from the click's place in the mix its stamps
	// are looked for.
	window := int64(rate)
	var (
		samples []float32
		stamps  []int64
	)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b := s.Read(buf)
		if b.Len() == 0 {
			if at := c.at.Load(); at >= 0 && len(stamps) > 0 && stamps[len(stamps)-1] > at+window {
				break
			}
			time.Sleep(2 * time.Millisecond)
			continue
		}
		for i := range b.Len() {
			samples = append(samples, b.Frames[i*ch+channel])
			stamps = append(stamps, b.Heard+int64(math.Round(float64(i)*s.ratio)))
		}
	}
	at := c.at.Load()
	if at < 0 {
		return Measurement{}, errors.New("capture: the mixer played none of the click: does the speaker play it?")
	}
	found, level, ok := findClick(samples, stamps, at, window, int64(rate/200))
	if !ok {
		return Measurement{Level: level}, ErrNoClick
	}
	offset := found - at
	return Measurement{Offset: offset, Level: level,
		RoundTrip: s.clock.Latency() + s.Latency() + audio.DurationAt(offset, rate)}, nil
}

// findClick finds the click played at mixer frame at in samples,
// stamped with the mixer frames they sounded with, among those stamped
// from window before at to window after. The frames stamped margin or
// more before its peak tell the noise it has to stand out from. It
// returns the stamp where the click starts, its peak, and whether it
// stood out.
func findClick(samples []float32, stamps []int64, at, window, margin int64) (onset int64, peak float32, ok bool) {
	top := -1
	for i, x := range samples {
		if a := abs(x); stamps[i] >= at-window && stamps[i] <= at+window && a > peak {
			top, peak = i, a
		}
	}
	if top < 0 || peak < 1e-3 {
		return 0, peak, false
	}
	var noise float32
	start := top
	for i := top; i >= 0 && stamps[i] >= at-window; i-- {
		a := abs(samples[i])
		if stamps[i] < stamps[top]-margin {
			noise = max(noise, a)
		} else if a >= peak/2 {
			start = i
		}
	}
	if peak < 3*noise {
		return 0, peak, false
	}
	return stamps[start], peak, true
}

func abs(x float32) float32 { return max(x, -x) }

// click is a sound of a millisecond, n frames, at half of full scale.
// It notes the mixer frame it starts at.
type click struct {
	mix  *audio.Mixer
	n    int
	done int
	at   atomic.Int64
}

func (c *click) Read(dst []float32) (int, error) {
	if c.done == 0 {
		// The mixer counts the frames before the block it reads a new
		// voice's first frames into.
		c.at.Store(c.mix.Mixed())
	}
	n := min(len(dst)/2, c.n-c.done)
	for i := range 2 * n {
		dst[i] = 0.5
	}
	c.done += n
	if c.done >= c.n {
		return n, io.EOF
	}
	return n, nil
}
