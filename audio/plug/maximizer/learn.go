package maximizer

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

// Learn: the gain that brings the whole track, as it reaches the
// maximizer, to the target loudness, limited as the maximizer limits it.
// The maximizer reads the track, as a [plug.TrackPlugin], measures its
// loudness, and guesses the gain; then runs the track through a
// maximizer of its own at that gain, measures what comes out, and moves
// the gain by what it misses, over how much louder a decibel of gain
// made it last, as limiting takes some of a gain's loudness back, until
// it comes within a tenth of a decibel.

// learnPasses is the most times Learn runs the track through, and
// learnClose how near the target is near enough, in LU.
const (
	learnPasses = 5
	learnClose  = 0.1
	learnBlock  = 4096
)

// learned is what Learn found: the gain, in decibels, and how loud the
// track comes out at it, in LUFS; and how many times it ran the track
// through to find it.
type learned struct {
	gain, loud float64
	passes     int
}

// learner is what the maximizer knows of the track, for Learn: the
// document the host gave it, and the learning under way.
type learner struct {
	mu       sync.Mutex
	doc      plug.Document
	cancel   context.CancelFunc
	progress atomic.Uint64
	result   *learned
	failed   error
}

var _ plug.TrackPlugin = (*plugin)(nil)

// SetDocument implements [plug.TrackPlugin]: a learning of the track
// before stops.
func (p *plugin) SetDocument(d plug.Document) {
	l := &p.learner
	l.mu.Lock()
	defer l.mu.Unlock()
	l.doc = d
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

// Analysed implements [plug.TrackPlugin]: how far the learning under
// way has got.
func (p *plugin) Analysed() float64 {
	l := &p.learner
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel == nil {
		return 0
	}
	return math.Float64frombits(l.progress.Load())
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

// canLearn says the host gave the maximizer a track to read.
func (p *plugin) canLearn() bool {
	p.learner.mu.Lock()
	defer p.learner.mu.Unlock()
	return sourceOf(p.learner.doc) != nil
}

// startLearning starts learning the gain for target, the rest of the
// maximizer set as s.
func (p *plugin) startLearning(s settings, target float64) {
	l := &p.learner
	l.mu.Lock()
	defer l.mu.Unlock()
	src := sourceOf(l.doc)
	if src == nil || l.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.result, l.failed = cancel, nil, nil
	l.progress.Store(0)
	go func() {
		got, err := learnGain(ctx, src, s, target, func(at float64) { l.progress.Store(math.Float64bits(at)) })
		l.mu.Lock()
		defer l.mu.Unlock()
		if ctx.Err() != nil {
			// Stopped, or the track changed: nothing to keep.
			return
		}
		l.cancel = nil
		if err != nil {
			l.failed = err
			return
		}
		l.result = &got
	}()
}

// stopLearning stops the learning under way.
func (p *plugin) stopLearning() {
	l := &p.learner
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

// learnState returns what the last learning found, where it is done;
// whether one is under way, and how far it has got; and what kept the
// last from learning.
func (p *plugin) learnState() (got *learned, learning bool, progress float64, err error) {
	l := &p.learner
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.result, l.cancel != nil, math.Float64frombits(l.progress.Load()), l.failed
}

// errSilent is what learning a silent track finds.
var errSilent = errors.New("the track is silent")

// learnGain returns the gain that brings src to target, through a
// maximizer set as s, and how loud it comes out at it.
func learnGain(ctx context.Context, src plug.Source, s settings, target float64, progress func(float64)) (learned, error) {
	pass := 0
	tell := func(at float64) { progress((float64(pass) + at) / (learnPasses + 1)) }
	loud, err := loudnessOf(ctx, src, nil, tell)
	if err != nil {
		return learned{}, err
	}
	top := param(GainID).Max
	gain := max(0, min(target-loud, top))
	// How much louder each decibel of gain makes the track: one, until
	// two passes say otherwise, as limiting takes some of it back.
	slope, lastGain, lastOut := 1.0, 0.0, loud
	var out float64
	for pass = 1; pass <= learnPasses; pass++ {
		s.gain = gain
		if out, err = loudnessOf(ctx, src, &s, tell); err != nil {
			return learned{}, err
		}
		miss := target - out
		if math.Abs(miss) < learnClose {
			break
		}
		if gain != lastGain {
			slope = min(max((out-lastOut)/(gain-lastGain), 0.1), 1)
		}
		lastGain, lastOut = gain, out
		next := max(0, min(gain+miss/slope, top))
		if next == gain {
			// As far as the gain goes, and the target out of reach.
			break
		}
		gain = next
	}
	progress(1)
	return learned{gain: math.Round(gain*10) / 10, loud: out, passes: min(pass, learnPasses)}, nil
}

// loudnessOf reads src whole and returns its integrated loudness, in
// LUFS: through a maximizer set as s, where s is not nil.
func loudnessOf(ctx context.Context, src plug.Source, s *settings, progress func(float64)) (float64, error) {
	rate := int(src.Rate())
	meter := audio.NewLoudnessMeter(rate)
	var proc *processor
	if s != nil {
		proc = &processor{unmetered: true}
		proc.Prepare(plug.Setup{Rate: src.Rate(), MaxBlock: learnBlock, Channels: 2, Offline: true})
		proc.take(plug.Change{ID: GainID, Value: s.gain})
		proc.take(plug.Change{ID: CeilingID, Value: s.ceiling})
		proc.take(plug.Change{ID: ModeID, Value: float64(s.mode)})
		proc.take(plug.Change{ID: CharacterID, Value: s.character})
		proc.take(plug.Change{ID: LinkID, Value: 100 * s.link})
	}
	ch := [][]float32{make([]float32, learnBlock), make([]float32, learnBlock)}
	frames := make([]float32, 2*learnBlock)
	total := src.Frames()
	for at := int64(0); at < total; at += learnBlock {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := src.ReadAt(ch, at)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if n == 0 {
			break
		}
		if proc != nil {
			proc.Process(&plug.Block{In: [][]float32{ch[0][:n], ch[1][:n]}, Out: [][]float32{ch[0][:n], ch[1][:n]},
				Frames: n, Position: plug.Position{Frame: at, Song: at}})
		}
		for i := range n {
			frames[2*i], frames[2*i+1] = ch[0][i], ch[1][i]
		}
		meter.Write(frames[:2*n])
		progress(float64(at+int64(n)) / float64(total))
	}
	loud, ok := meter.Integrated()
	if !ok {
		return 0, errSilent
	}
	return loud, nil
}
