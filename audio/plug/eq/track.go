package eq

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
)

// The EQ reads the whole track, as a [plug.TrackPlugin], for Auto: as
// it reaches the EQ, through the plugins before it, as the host gives
// it. It reads on a goroutine of its own, when asked, and keeps the
// track's spectrum for the version it read, so asking again of the same
// track is at once.

// readBlock is how many frames each transform of the track takes, each
// overlapping the last by half, as Welch's method does; readQuiet is the
// level under which a block counts as silence, left out, so a track's
// gaps and fades weigh nothing.
const (
	readBlock = 8192
	readQuiet = -50
)

// reader is what the EQ knows of the track: the document the host gave
// it, the spectrum read of a version of its source, and the reading
// under way.
type reader struct {
	mu     sync.Mutex
	doc    plug.Document
	read   []float64
	readOf int
	// The reading under way: its version, how to stop it, and how far
	// it has got, from 0 to 1, as bits.
	cancel   context.CancelFunc
	reading  int
	progress atomic.Uint64
	failed   error
}

var _ plug.TrackPlugin = (*plugin)(nil)

// SetDocument implements [plug.TrackPlugin]: a new version of the track
// drops what was read of the old, and stops reading it.
func (p *plugin) SetDocument(d plug.Document) {
	r := &p.reader
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doc = d
	if v := sourceOf(d); v == nil || v.Version() != r.readOf {
		r.read = nil
	}
	if src := sourceOf(d); r.cancel != nil && (src == nil || src.Version() != r.reading) {
		r.cancel()
		r.cancel = nil
	}
}

// Analysed implements [plug.TrackPlugin]: how far the reading under way
// has got, or 1 where the track is read.
func (p *plugin) Analysed() float64 {
	r := &p.reader
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.read != nil:
		return 1
	case r.cancel != nil:
		return math.Float64frombits(r.progress.Load())
	}
	return 0
}

// sourceOf returns the first source of d, or nil.
func sourceOf(d plug.Document) plug.Source {
	if d == nil {
		return nil
	}
	if s := d.Sources(); len(s) > 0 {
		return s[0]
	}
	return nil
}

// canRead says the host gave the EQ a track to read.
func (p *plugin) canRead() bool {
	p.reader.mu.Lock()
	defer p.reader.mu.Unlock()
	return sourceOf(p.reader.doc) != nil
}

// startReading starts reading the track, where it is not read or being
// read already.
func (p *plugin) startReading() {
	r := &p.reader
	r.mu.Lock()
	defer r.mu.Unlock()
	src := sourceOf(r.doc)
	if src == nil || r.read != nil || r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.reading, r.failed = cancel, src.Version(), nil
	r.progress.Store(0)
	go func() {
		levels, err := spectrumOf(ctx, src, autoFreqs, func(at float64) { r.progress.Store(math.Float64bits(at)) })
		r.mu.Lock()
		defer r.mu.Unlock()
		if ctx.Err() != nil {
			// Stopped, or the track changed: nothing to keep.
			return
		}
		r.cancel = nil
		if err != nil {
			r.failed = err
			return
		}
		r.read, r.readOf = levels, src.Version()
	}()
}

// stopReading stops the reading under way.
func (p *plugin) stopReading() {
	r := &p.reader
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

// readState returns the track's spectrum, where it is read; whether it
// is being read, and how far; and what kept it from being read, the
// last time.
func (p *plugin) readState() (levels []float64, reading bool, progress float64, err error) {
	r := &p.reader
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.read, r.cancel != nil, math.Float64frombits(r.progress.Load()), r.failed
}

// errSilent is what reading a silent track finds.
var errSilent = errors.New("the track is silent")

// spectrumOf reads src whole and returns its power about each of freqs,
// in decibels: averaged over its blocks of sound, each band summed from
// halfway to the frequency below to halfway to the one above, so pink
// noise reads level. It tells progress how far it has got as it goes.
func spectrumOf(ctx context.Context, src plug.Source, freqs []float64, progress func(float64)) ([]float64, error) {
	fft := audio.NewFFT(readBlock)
	window := make([]float64, readBlock)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/readBlock)
	}
	x := make([]complex128, readBlock)
	block := [][]float32{make([]float32, readBlock), make([]float32, readBlock)}
	power := make([]float64, readBlock/2+1)
	frames, half := src.Frames(), int64(readBlock/2)
	blocks := 0
	for at := int64(0); at+readBlock <= frames; at += half {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The first half again from the last block, then a half new.
		if at > 0 {
			copy(block[0], block[0][half:])
			copy(block[1], block[1][half:])
		}
		from := int64(0)
		if at > 0 {
			from = half
		}
		n, err := src.ReadAt([][]float32{block[0][from:], block[1][from:]}, at+from)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if int64(n) < readBlock-from {
			break
		}
		progress(float64(at+readBlock) / float64(frames))
		var sum float64
		for i := range readBlock {
			m := 0.5 * float64(block[0][i]+block[1][i])
			sum += m * m
			x[i] = complex(m*window[i], 0)
		}
		if 10*math.Log10(max(sum/readBlock, 1e-20)) < readQuiet {
			continue
		}
		fft.Transform(x)
		for k := range power {
			re, im := real(x[k]), imag(x[k])
			power[k] += re*re + im*im
		}
		blocks++
	}
	if blocks == 0 {
		return nil, errSilent
	}
	return bandLevels(power, float64(blocks), src.Rate(), freqs), nil
}

// bandLevels sums power, a transform's bins summed over blocks, about
// each of freqs, from halfway to the frequency below to halfway to the
// one above, in log frequency, each bin as far as it falls inside, and
// returns the mean of each a block, in decibels.
func bandLevels(power []float64, blocks, rate float64, freqs []float64) []float64 {
	binHz := rate / float64(2*(len(power)-1))
	out := make([]float64, len(freqs))
	for i, f := range freqs {
		lo, hi := f, f
		if i > 0 {
			lo = math.Sqrt(freqs[i-1] * f)
		}
		if i+1 < len(freqs) {
			hi = math.Sqrt(f * freqs[i+1])
		}
		// The ends reach as far out as in.
		if i == 0 {
			lo = f * f / hi
		}
		if i+1 == len(freqs) {
			hi = f * f / lo
		}
		var sum float64
		for k := max(0, int(lo/binHz)); k < len(power) && float64(k)-0.5 <= hi/binHz; k++ {
			from, to := max((float64(k)-0.5)*binHz, lo), min((float64(k)+0.5)*binHz, hi)
			if to > from {
				sum += power[k] * (to - from) / binHz
			}
		}
		out[i] = 10 * math.Log10(max(sum/blocks, 1e-20))
	}
	return out
}
