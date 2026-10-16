package plug

import "time"

// StillAfter is how long a sound hears no new frames before a view
// takes it as stopped. Sound comes in chunks, tens of milliseconds
// apart, and the window draws frames between them that hear nothing
// new: a view that took those as silence would flicker.
const StillAfter = 250 * time.Millisecond

// A Listener reads a [Sound] a frame of the window at a time, each
// frame of the sound once: what a meter or a spectrum takes. It is for
// one view, on the window's goroutine.
type Listener struct {
	Sound Sound
	buf   []float32
	at    int64
	rate  int
	// still is how long it has heard nothing new, up to StillAfter.
	still time.Duration
}

// At returns the count of frames heard by the last of the frames Listen
// returned last: they run from At less their number to At.
func (l *Listener) At() int64 { return l.at }

// Hear is Listen for a view that steps a frame of the window, dt long,
// at a time: with the frames heard since, it reports whether the sound
// plays, as it does through the frames of the window between its
// chunks, until it has heard nothing new for StillAfter. Silence, every
// sample below [Silence], is nothing new: a host's engine may run on
// with the song stopped, and a view that took its silence as playing
// would draw every frame for nothing.
func (l *Listener) Hear(most int, dt time.Duration) (frames []float32, rate int, playing bool) {
	frames, rate = l.Listen(most)
	if len(frames) > 0 && !silent(frames) {
		l.still = 0
		return frames, rate, true
	}
	l.still = min(l.still+dt, StillAfter)
	return nil, rate, l.at != 0 && l.still < StillAfter
}

// Listen returns the frames heard since it was last called, at most
// most, stereo frames interleaved, with their rate: none while the
// sound stands still, and the last most at the first call and after a
// jump back. The frames are the Listener's, until the next call.
func (l *Listener) Listen(most int) (frames []float32, rate int) {
	if l.Sound == nil || most <= 0 {
		return nil, 0
	}
	if len(l.buf) < 2*most {
		l.buf = make([]float32, 2*most)
	}
	_, at, rate := l.Sound.Heard(l.buf[:0])
	if at == 0 || at == l.at {
		return nil, rate
	}
	want := int64(most)
	if at > l.at && l.at != 0 {
		want = min(want, at-l.at)
	}
	n, at, rate := l.Sound.Heard(l.buf[:2*want])
	l.at, l.rate = at, rate
	return l.buf[:2*n], rate
}

// Silence is the level, -120 dBFS, below which [Listener.Hear] takes a
// sound as silent.
const Silence = 1e-6

// silent reports whether every sample of frames is below Silence.
func silent(frames []float32) bool {
	for _, v := range frames {
		if v > Silence || v < -Silence {
			return false
		}
	}
	return true
}
