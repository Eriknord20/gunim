// Package beat finds the beats in a recording: where each one falls,
// which of them start the bars, and how fast the music is going.
//
// A [Scan] takes a recording's frames as they are read and gives a
// [Result] when they run out, so the beats come out of the same pass
// over a file that draws its waveform:
//
//	s := beat.NewScan(rate)
//	for {
//		n, err := song.Read(buf)
//		s.Write(buf[:2*n])
//		...
//	}
//	r := s.Done()
//	m := r.Map(0) // the recording's musical time
//
// The beats come from the sound's onsets: the recording is taken in
// short windows a hundredth of a second apart, each window's loudness
// measured in forty bands of pitch, and the rises from one window to
// the next added up. Music beats where that rises.
//
// How far apart the beats are is found by how well those rises line up
// with themselves a given distance later, weighed towards a tempo near
// 130, which is what keeps a track at 140 from being read as one at 70.
// Where the beats fall is then chosen all at once, over the whole
// recording, as the run of beats catching the most onset strength while
// staying near a beat apart — so a few bars of silence do not throw the
// beat off, and a recording that drifts is followed rather than lost.
//
// What it is good at and what it is not: a recording made to a grid
// comes back right, and one that speeds up or slows down as a band does
// is followed. A recording in free time is not, and says so through
// [Result.Confidence]. Nothing here hears a time signature, so the
// beats in a bar are told to it rather than found.
package beat

import (
	"errors"
	"io"
	"math"
	"math/cmplx"
	"slices"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/tempo"
)

// Bands is how many bands of pitch the onsets are measured in, evenly
// by octaves.
const Bands = 40

// BandLow and BandHigh are the pitches those bands span.
const (
	BandLow  = 30.0
	BandHigh = 10000.0
)

// floor is the quietest a band is counted as, about -90 dB.
var floor = math.Pow(10, -90.0/20)

// BassTop is the top of the bands counted again on their own, since it
// is the bass drum that says which beat starts a bar.
const BassTop = 150.0

// DefaultMinBPM and DefaultMaxBPM are the tempos a scan looks between
// when it is told no others: wide enough for a ballad and for drum and
// bass, narrow enough that the halves and doubles outside it cannot win.
const (
	DefaultMinBPM = 70.0
	DefaultMaxBPM = 180.0
)

// DefaultPerBar is the beats in a bar a scan counts in when it is told
// no others.
const DefaultPerBar = 4

// A Result is what a scan found.
type Result struct {
	// Beats is where each beat falls, from the start of the recording.
	// It is empty for sound with no beat to find.
	Beats []time.Duration
	// Downbeat is the beat in Beats that starts a bar, the first one to
	// do so: with PerBar of 4 it is 0, 1, 2 or 3.
	//
	// It is the beat the bass drum lands on most. Where the kick plays
	// on one and three alike, as in most dance music, the two are heard
	// the same, and the bar may start on three; [tempo.Map.SetStart]
	// moves it.
	Downbeat int
	// PerBar is the beats in a bar the scan counted in.
	PerBar int
	// BPM is the middle tempo between the beats. A recording that
	// drifts has no single tempo, and this is the one to show.
	BPM float64
	// Confidence is how much more the recording's onsets repeat a beat
	// apart than they repeat at any other spacing: 0 for sound with no
	// beat in it, above 0.5 for music with a clear one, near 1 for a
	// plain steady beat.
	Confidence float64
}

// Map returns the beats as a tempo map, bars counted from the first
// downbeat, fitted to within maxError, which is [tempo.FitError] when
// it is zero. A result with no beats gives a map at the default tempo.
func (r Result) Map(maxError time.Duration) *tempo.Map {
	perBar := max(r.PerBar, 1)
	if r.Downbeat >= len(r.Beats) {
		return tempo.Fit(nil, perBar, maxError)
	}
	return tempo.Fit(r.Beats[r.Downbeat:], perBar, maxError)
}

// Find reads a sound to its end and finds the beats in it, for an
// application holding a decoded file rather than reading one.
//
// A sound that gives nothing again and again without ending is given
// up on, with what was read and [io.ErrNoProgress], as package bufio
// does.
func Find(src audio.Source, rate int) (Result, error) {
	s := NewScan(rate)
	buf := make([]float32, 2*4096)
	empty := 0
	for {
		n, err := src.Read(buf)
		s.Write(buf[:2*n])
		switch {
		case errors.Is(err, io.EOF):
			return s.Done(), nil
		case err != nil:
			return s.Done(), err
		case n > 0:
			empty = 0
		default:
			empty++
			if empty >= 100 {
				return s.Done(), io.ErrNoProgress
			}
		}
	}
}

// A Scan finds the beats in a recording from its frames as they are
// read. Set its fields before the first Write.
type Scan struct {
	// PerBar is the beats in a bar, [DefaultPerBar] when it is zero. It
	// is what the scan counts in to find which beat starts a bar;
	// nothing here hears the time signature itself.
	PerBar int
	// MinBPM and MaxBPM bound the tempo looked for, [DefaultMinBPM] and
	// [DefaultMaxBPM] when they are zero. A tempo outside them is read
	// as its half or its double.
	MinBPM, MaxBPM float64

	rate      int
	hop, size int
	fft       *audio.FFT
	window    []float64
	// ring holds the last size frames, mono, filled to n, and since
	// counts the frames since the last column.
	ring  []float64
	n     int
	since int
	x     []complex128
	// bands are the bins each band of pitch spans, and bass how many of
	// them are counted as the bass.
	bands [Bands][2]int
	bass  int
	// last is the band levels of the column before, in decibels.
	last [Bands]float64
	// rise is how much the bands rose at each column, and bass rise the
	// same over the bass alone.
	rise, bassRise []float32
	// warm counts the columns taken so far. Until a window holds
	// nothing but the recording itself, what rises in it is the
	// recording starting rather than anything in it.
	warm int
	done bool
	out  Result
}

// NewScan returns a scan of sound at rate. Its columns fall about every
// ten milliseconds, each taken over a window four times as long. A rate
// of zero or less is taken as [audio.SampleRate].
func NewScan(rate int) *Scan {
	if rate <= 0 {
		rate = audio.SampleRate
	}
	hop := 1
	for hop*100 < rate {
		hop *= 2
	}
	size := 4 * hop
	s := &Scan{rate: rate, hop: hop, size: size, fft: audio.NewFFT(size),
		window: make([]float64, size), ring: make([]float64, size), x: make([]complex128, size)}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(size))
	}
	bin := func(hz float64) float64 { return hz * float64(size) / float64(rate) }
	top := min(BandHigh, 0.45*float64(rate))
	// No bin is counted twice. At the bottom a bin is wider than a band,
	// and several bands would land on the same one and count its rise
	// several times over; the first takes it and the rest stay empty.
	next := 0
	for b := range Bands {
		lo := int(math.Round(bin(BandLow * math.Pow(top/BandLow, float64(b)/Bands))))
		hi := int(math.Round(bin(BandLow * math.Pow(top/BandLow, float64(b+1)/Bands))))
		b0 := min(max(lo, next), size/2)
		b1 := min(max(hi, lo+1), size/2)
		if b1 <= b0 {
			b1 = b0
		}
		s.bands[b] = [2]int{b0, b1}
		next = b1
		if BandLow*math.Pow(top/BandLow, float64(b+1)/Bands) <= BassTop {
			s.bass = b + 1
		}
	}
	// Half a window of silence first, so the second column is centred on
	// the first frame. The first three columns each hear some of that
	// silence; see edge.
	s.n = size / 2
	return s
}

// Write takes frames, interleaved stereo. Writing after [Scan.Done]
// does nothing.
func (s *Scan) Write(frames []float32) {
	if s.done {
		return
	}
	for i := 0; i+1 < len(frames); i += 2 {
		s.ring[s.n%s.size] = float64(frames[i]+frames[i+1]) / 2
		s.n++
		s.since++
		if s.since == s.hop {
			s.since = 0
			s.column()
		}
	}
}

// column measures the last window's bands and how far each rose.
func (s *Scan) column() {
	for i := range s.size {
		s.x[i] = complex(s.ring[(s.n+i)%s.size]*s.window[i], 0)
	}
	s.fft.Transform(s.x)
	// A full-scale sine is a magnitude of a quarter of the size,
	// through the window.
	norm := 4.0 / float64(s.size)
	var all, bass float64
	for b, span := range &s.bands {
		var sum float64
		for k := span[0]; k < span[1]; k++ {
			sum += cmplx.Abs(s.x[k])
		}
		// Below about -90 dB is silence. Without a floor, sound rising
		// out of digital silence would count tens of decibels of rise
		// that nobody hears.
		db := 20 * math.Log10(max(sum*norm, floor))
		// Only a rise counts: a band falling silent is not an onset,
		// and counting it would make every note's end a beat.
		up := db - s.last[b]
		s.last[b] = db
		if up <= 0 {
			continue
		}
		all += up
		if b < s.bass {
			bass += up
		}
	}
	s.warm++
	if s.warm <= s.edge() {
		all, bass = 0, 0
	}
	s.rise = append(s.rise, float32(all))
	s.bassRise = append(s.bassRise, float32(bass))
}

// Done finds the beats in what has been written. Writing after it does
// nothing, and calling it again gives the same result over.
func (s *Scan) Done() (r Result) {
	if s.done {
		return s.out
	}
	// Run the last frames out through half a window of silence, so the
	// final column is taken like the rest. The recording ending is a
	// rise of its own — cutting sound off spreads it over every band —
	// so the columns that hear the end are quieted, as those that hear
	// the start are.
	s.Write(make([]float32, 2*(s.size/2)))
	s.done = true
	for i := max(len(s.rise)-s.edge(), 0); i < len(s.rise); i++ {
		s.rise[i], s.bassRise[i] = 0, 0
	}

	defer func() { s.out = r }()
	r = Result{PerBar: s.perBar()}
	fps := float64(s.rate) / float64(s.hop)
	strength := level(s.rise, int(fps/2))
	bass := level(s.bassRise, int(fps/2))
	per, clarity, ok := period(strength, fps, s.minBPM(), s.maxBPM())
	if !ok {
		return r
	}
	at := trim(strength, walk(strength, per))
	if len(at) < 2 {
		return r
	}

	r.Beats = make([]time.Duration, len(at))
	for i, c := range at {
		r.Beats[i] = time.Duration(place(strength, c) / fps * float64(time.Second))
	}
	r.BPM = 60 / middle(r.Beats).Seconds()
	r.Downbeat = downbeat(bass, at, r.PerBar)
	r.Confidence = clarity
	return r
}

// edge is how many columns at each end of a recording hear its start or
// its end in their window, rather than only what is in it. A window is
// four columns long, so three columns hear each end.
func (s *Scan) edge() int { return s.size/s.hop - 1 }

func (s *Scan) perBar() int {
	if s.PerBar > 0 {
		return s.PerBar
	}
	return DefaultPerBar
}

func (s *Scan) minBPM() float64 {
	if s.MinBPM > 0 {
		return s.MinBPM
	}
	return DefaultMinBPM
}

func (s *Scan) maxBPM() float64 {
	if s.MaxBPM > s.minBPM() {
		return s.MaxBPM
	}
	return max(DefaultMaxBPM, 2*s.minBPM())
}

// middle is how long a beat lasts in a recording: the middle of the
// gaps between the beats, and then the mean of every gap near it. The
// middle alone is steady against a passage at another tempo but no
// finer than the columns; the mean of the gaps around it is as fine as
// the beats are.
func middle(beats []time.Duration) time.Duration {
	gaps := make([]time.Duration, len(beats)-1)
	for i := range gaps {
		gaps[i] = beats[i+1] - beats[i]
	}
	slices.Sort(gaps)
	mid := gaps[len(gaps)/2]
	var sum, n time.Duration
	for _, g := range gaps {
		if 10*(g-mid) < mid && 10*(mid-g) < mid {
			sum += g
			n++
		}
	}
	if n == 0 {
		return mid
	}
	return sum / n
}
