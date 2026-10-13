package vocal

import (
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
)

// Learn: the chain set for the voice, as it reaches the plugin, where
// the host loops, over the loop alone. It reads the voice a few times
// over, on a goroutine of its own:
//
//  1. Once as it is, for its loudness, which sets Input to bring it to
//     the target, and for its balance, its power a sixth of an octave
//     at a time, which sets the EQs: the low shelf by how much rumble
//     and boom there is under the voice; the mud cut at the boxiest
//     ring between 280 and 700 Hz, as deep as it stands out; the
//     presence lift into the dip it finds between 2.5 and 5 kHz, as far
//     as the voice lacks bite; the air by how dull the top is; and the
//     de-esser at the band where the hiss stands out.
//  2. Once for the hiss, over the de-esser's band, which sets its
//     threshold just under the loudest S's.
//  3. Through the chain as set so far, again and again, to find the
//     peak compressor's threshold, where it takes learnPeakGR decibels
//     off the voice's peaks, and then the glue compressor's, where it
//     takes learnGlueGR off the voice on average.
//  4. Through the whole chain, for Output, to bring it back to the
//     target, so the chain on and off are as loud.
//
// The macros stay as they are: Learn sets the chain under them.

// What Learn aims for, and how it works.
const (
	// learnPeakGR is how far the peak compressor turns down the voice's
	// peaks, the loudest tenth of its moments, and learnGlueGR how far
	// the glue compressor turns down the voice on average, in decibels.
	learnPeakGR = 2.0
	learnGlueGR = 2.0
	// learnClose is how near a compressor's reduction comes to its aim,
	// in decibels, and learnFits the most times the voice runs through
	// the chain to find a threshold.
	learnClose = 0.2
	learnFits  = 6
	// learnPasses is how many times Learn reads the voice, at most, for
	// its progress.
	learnPasses = 3 + 2*learnFits
	// learnChunk is how many frames Learn reads at a time, and
	// learnBlock how many frames of the voice make a moment, a hundredth
	// of a second at 48 kHz.
	learnChunk = 8192
	// specBlock is how many frames each transform of the voice takes,
	// each overlapping the last by half; specQuiet is the level under
	// which a block counts as silence, left out; and the voice's moments
	// more than learnActive decibels under its loudness are left out too.
	specBlock   = 4096
	specQuiet   = -65
	learnActive = 25
)

// learned is what Learn found: the parameters it set, by ID; and
// whether it read the loop alone.
type learned struct {
	values map[uint32]float64
	loop   bool
}

// learnt is what Learn last learned, for its dial: for the target, and
// over the loop or the track; ok says it has learned at all.
type learnt struct {
	target   float64
	loop, ok bool
}

// learner is what the plugin knows of the voice, for Learn: the
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

// SetDocument implements [plug.TrackPlugin]: a learning of the voice
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

// canLearn says the host gave the plugin a voice to read, and whether
// Learn would read the loop alone.
func (p *plugin) canLearn() (ok, loop bool) {
	p.learner.mu.Lock()
	defer p.learner.mu.Unlock()
	d := p.learner.doc
	if sourceOf(d) == nil {
		return false, false
	}
	_, loop = plug.LoopOf(d)
	return true, loop
}

// startLearning starts learning the chain, the rest of it set as values.
func (p *plugin) startLearning(values map[uint32]float64) {
	l := &p.learner
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.doc
	src := sourceOf(d)
	if src == nil || l.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.result, l.failed = cancel, nil, nil
	l.progress.Store(0)
	values = cloneValues(values)
	go func() {
		got, err := learnChain(ctx, d, src, values, func(at float64) { l.progress.Store(math.Float64bits(at)) })
		l.mu.Lock()
		defer l.mu.Unlock()
		if ctx.Err() != nil {
			// Stopped, or the voice changed: nothing to keep.
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

func cloneValues(m map[uint32]float64) map[uint32]float64 {
	out := make(map[uint32]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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

// What learning finds wrong with the voice.
var (
	errSilent   = errors.New("the voice is silent")
	errTooShort = errors.New("the loop is too short to learn from")
	errNoLoop   = errors.New("the loop holds none of the track")
)

// voice is the voice Learn reads: the stretches of the source it
// hears, and how far through its passes it has got.
type voice struct {
	src      plug.Source
	spans    []plug.Span
	frames   int64
	rate     float64
	pass     int
	progress func(float64)
	buf      [][]float32
}

// read reads the voice through, a chunk at a time, handing each to fn
// with whether it starts a stretch of its own.
func (v *voice) read(ctx context.Context, fn func(ch [][]float32, n int, fresh bool)) error {
	var done int64
	for _, s := range v.spans {
		for at := s.From; at < s.To; at += learnChunk {
			if err := ctx.Err(); err != nil {
				return err
			}
			want := int(min(learnChunk, s.To-at))
			ch := [][]float32{v.buf[0][:want], v.buf[1][:want]}
			n, err := v.src.ReadAt(ch, at)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			if n == 0 {
				break
			}
			fn([][]float32{ch[0][:n], ch[1][:n]}, n, at == s.From)
			done += int64(n)
			v.progress((float64(v.pass) + float64(done)/float64(v.frames)) / learnPasses)
		}
	}
	v.pass++
	return nil
}

// learnChain returns the chain set for the voice src plays in d, the
// rest of it set as values.
func learnChain(ctx context.Context, d plug.Document, src plug.Source, values map[uint32]float64, progress func(float64)) (learned, error) {
	spans := plug.Spans(d, src)
	_, loop := plug.LoopOf(d)
	v := &voice{src: src, spans: spans, rate: src.Rate(), progress: progress,
		buf: [][]float32{make([]float32, learnChunk), make([]float32, learnChunk)}}
	for _, s := range spans {
		v.frames += s.To - s.From
	}
	switch {
	case loop && len(spans) == 0:
		return learned{}, errNoLoop
	case v.frames < specBlock:
		return learned{}, errTooShort
	}
	set := cloneValues(values)
	for _, prm := range Descriptor.Params {
		if _, ok := set[prm.ID]; !ok {
			set[prm.ID] = prm.Default
		}
	}
	target := set[TargetID]

	// 1. The voice's loudness and balance.
	loud, levels, err := listen(ctx, v)
	if err != nil {
		return learned{}, err
	}
	set[InputID] = round(param(InputID).Clamp(target-loud), 10)
	balance(levels, set)

	// 2. The hiss.
	if set[DeEssThresholdID], err = hissThreshold(ctx, v, set[DeEssFreqID], loud); err != nil {
		return learned{}, err
	}

	// 3. The compressors, each through the chain before it, and nothing
	// after it.
	measure := func(id uint32, t float64) (float64, error) {
		run := cloneValues(set)
		run[id] = t
		run[ParallelID], run[TapeID], run[OctaveID] = 0, 0, 0
		if id == PeakThresholdID {
			run[GlueOnID] = 0
		}
		return reductionOf(ctx, v, run, id, target-learnActive)
	}
	// With Comp at nothing, neither compressor turns anything
	// down, at any threshold: they stay as they are.
	if set[SquashID] > 0 {
		set[PeakOnID], set[GlueOnID] = 1, 1
		if set[PeakThresholdID], err = fitThreshold(func(t float64) (float64, error) { return measure(PeakThresholdID, t) },
			learnPeakGR, target+6, set[PeakRatioID]); err != nil {
			return learned{}, err
		}
		if set[GlueThresholdID], err = fitThreshold(func(t float64) (float64, error) { return measure(GlueThresholdID, t) },
			learnGlueGR, target-2, set[GlueRatioID]); err != nil {
			return learned{}, err
		}
		// Learn leaves the switches as the user set them, having heard
		// each compressor on.
		set[PeakOnID], set[GlueOnID] = valueOr(values, PeakOnID), valueOr(values, GlueOnID)
	}

	// 4. Output.
	run := cloneValues(set)
	run[OutputID] = 0
	out, err := loudnessThrough(ctx, v, run)
	if err != nil {
		return learned{}, err
	}
	set[OutputID] = round(param(OutputID).Clamp(target-out), 10)
	progress(1)

	got := map[uint32]float64{}
	for _, id := range learnedIDs {
		got[id] = set[id]
	}
	return learned{values: got, loop: loop}, nil
}

// valueOr returns the value of id in values, or its default.
func valueOr(values map[uint32]float64, id uint32) float64 {
	if v, ok := values[id]; ok {
		return v
	}
	return param(id).Default
}

// learnedIDs are the parameters Learn sets.
var learnedIDs = []uint32{
	InputID, OutputID,
	DeEssFreqID, DeEssThresholdID,
	LowShelfID, MudFreqID, MudGainID,
	PresenceFreqID, PresenceGainID, AirGainID,
	PeakThresholdID, GlueThresholdID,
}

// round returns v to the nearest 1/per.
func round(v, per float64) float64 { return math.Round(v*per) / per }

// listen reads the voice as it is and returns its integrated loudness,
// in LUFS, and its power about each of specFreqs, in decibels.
func listen(ctx context.Context, v *voice) (loud float64, levels []float64, err error) {
	meter := audio.NewLoudnessMeter(int(v.rate))
	fft := audio.NewFFT(specBlock)
	window := make([]float64, specBlock)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/specBlock)
	}
	x := make([]complex128, specBlock)
	power := make([]float64, specBlock/2+1)
	mid := make([]float64, 0, specBlock)
	frames := make([]float32, 2*learnChunk)
	blocks := 0
	transform := func() {
		var sum float64
		for i, m := range mid {
			sum += m * m
			x[i] = complex(m*window[i], 0)
		}
		if 10*math.Log10(max(sum/specBlock, 1e-20)) >= specQuiet {
			fft.Transform(x)
			for k := range power {
				re, im := real(x[k]), imag(x[k])
				power[k] += re*re + im*im
			}
			blocks++
		}
		// Half the block again, overlapping the next.
		mid = append(mid[:0], mid[specBlock/2:]...)
	}
	err = v.read(ctx, func(ch [][]float32, n int, fresh bool) {
		if fresh {
			mid = mid[:0]
		}
		for i := range n {
			frames[2*i], frames[2*i+1] = ch[0][i], ch[1][i]
			mid = append(mid, 0.5*float64(ch[0][i]+ch[1][i]))
			if len(mid) == specBlock {
				transform()
			}
		}
		meter.Write(frames[:2*n])
	})
	if err != nil {
		return 0, nil, err
	}
	loud, ok := meter.Integrated()
	if !ok || blocks == 0 {
		return 0, nil, errSilent
	}
	return loud, bandLevels(power, float64(blocks), v.rate, specFreqs), nil
}

// specFreqs are the frequencies the voice's balance is measured at: a
// sixth of an octave apart, from 40 Hz to 16 kHz.
var specFreqs = func() []float64 {
	var out []float64
	for f := 40.0; f <= 16000*1.0001; f *= math.Pow(2, 1.0/6) {
		out = append(out, f)
	}
	return out
}()

// bandLevels sums power, a transform's bins summed over blocks, about
// each of freqs, from halfway to the frequency below to halfway to the
// one above, in log frequency, and returns the mean of each a block, in
// decibels: so pink noise reads level.
func bandLevels(power []float64, blocks, rate float64, freqs []float64) []float64 {
	binHz := rate / float64(2*(len(power)-1))
	out := make([]float64, len(freqs))
	for i, f := range freqs {
		lo, hi := f/math.Pow(2, 1.0/12), f*math.Pow(2, 1.0/12)
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

// The voice's bands, as Learn weighs them, in hertz.
const (
	refLow, refHigh           = 250.0, 4000.0
	rumbleLow, rumbleHigh     = 50.0, 150.0
	mudLow, mudHigh           = 280.0, 700.0
	presenceLow, presenceHigh = 2500.0, 5000.0
	airLow, airHigh           = 8000.0, 14000.0
	hissLow, hissHigh         = 4500.0, 10000.0
)

// What a voice that needs nothing from the EQs measures, in decibels
// from its level between refLow and refHigh, as a close microphone in a
// treated room hears a singer: the bands' typical levels, a sixth of an
// octave each, as listen measures them.
const (
	usualRumble   = -3.0
	usualPresence = -3.0
	usualAir      = -15.0
)

// balance sets the EQs and the de-esser's frequency from the voice's
// levels about specFreqs, in set.
func balance(levels []float64, set map[uint32]float64) {
	ref := meanOver(levels, refLow, refHigh)
	trend := smoothed(levels, 6)
	stands := func(i int) float64 { return levels[i] - trend[i] }

	// Rumble and boom: her 6 dB at 150 Hz, deeper where there is more.
	rumble := meanOver(levels, rumbleLow, rumbleHigh) - ref
	set[LowShelfID] = round(-clamp(6+0.5*(rumble-usualRumble), 3, 12), 2)

	// The boxy ring: where it stands out most over the voice round it,
	// cut as deep as it stands, and no less than a decibel and a half.
	if i, by := peakOver(levels, mudLow, mudHigh, stands); i >= 0 {
		set[MudFreqID] = math.Round(specFreqs[i])
		set[MudGainID] = round(-clamp(1.5+0.8*max(by, 0), 1.5, 6), 2)
	}

	// Presence: into the deepest dip, as far as the voice lacks bite.
	presence := meanOver(levels, presenceLow, presenceHigh) - ref
	set[PresenceGainID] = round(clamp(2+0.5*(usualPresence-presence), 0, 6), 2)
	if i, _ := peakOver(levels, presenceLow, presenceHigh, func(i int) float64 { return -stands(i) }); i >= 0 {
		set[PresenceFreqID] = math.Round(specFreqs[i])
	}

	// Air: as far as the top is dull.
	air := meanOver(levels, airLow, airHigh) - ref
	set[AirGainID] = round(clamp(3+0.25*(usualAir-air), 0, 6), 2)

	// The hiss: where it stands out, or where it usually is.
	set[DeEssFreqID] = 6500
	if i, by := peakOver(levels, hissLow, hissHigh, stands); i >= 0 && by > 1 {
		// The de-esser's band starts a little under the hiss's peak.
		set[DeEssFreqID] = math.Round(param(DeEssFreqID).Clamp(specFreqs[i] * 0.8))
	}
}

func clamp(v, lo, hi float64) float64 { return max(lo, min(v, hi)) }

// meanOver returns the mean of levels about specFreqs from lo to hi.
func meanOver(levels []float64, lo, hi float64) float64 {
	var sum float64
	n := 0
	for i, f := range specFreqs {
		if f >= lo && f <= hi {
			sum += levels[i]
			n++
		}
	}
	return sum / float64(max(n, 1))
}

// smoothed returns levels, each the mean of those within by either
// side.
func smoothed(levels []float64, by int) []float64 {
	out := make([]float64, len(levels))
	for i := range levels {
		var s float64
		n := 0
		for j := max(0, i-by); j <= min(len(levels)-1, i+by); j++ {
			s += levels[j]
			n++
		}
		out[i] = s / float64(n)
	}
	return out
}

// peakOver returns the index of specFreqs from lo to hi where score is
// highest, and the score: -1 where none is in the span.
func peakOver(levels []float64, lo, hi float64, score func(i int) float64) (at int, best float64) {
	best, at = math.Inf(-1), -1
	for i, f := range specFreqs {
		if f < lo || f > hi || i >= len(levels) {
			continue
		}
		if s := score(i); s > best {
			best, at = s, i
		}
	}
	return at, best
}

// moment returns how many frames make a moment of the voice, a
// hundredth of a second, at rate.
func moment(rate float64) int { return max(1, int(rate/100)) }

// hissThreshold returns the de-esser's threshold for the voice: just
// under its loudest S's, the loudest twentieth of its moments in the
// band over freq, among the moments within learnActive of its loudness,
// loud.
func hissThreshold(ctx context.Context, v *voice, freq, loud float64) (float64, error) {
	var hp biquad
	hp.set(highPass, freq, 0, 0.707, v.rate)
	var st [2]biquadState
	step := moment(v.rate)
	var hiss []float64
	var bandPeak, fullPeak float64
	k := 0
	err := v.read(ctx, func(ch [][]float32, n int, fresh bool) {
		if fresh {
			st = [2]biquadState{}
		}
		for i := range n {
			for c := range 2 {
				x := float64(ch[c][i])
				bandPeak = max(bandPeak, math.Abs(hp.run(&st[c], x)))
				fullPeak = max(fullPeak, math.Abs(x))
			}
			k++
			if k == step {
				if dbOf(fullPeak) > loud-learnActive {
					hiss = append(hiss, dbOf(bandPeak))
				}
				k, bandPeak, fullPeak = 0, 0, 0
			}
		}
	})
	if err != nil {
		return 0, err
	}
	if len(hiss) == 0 {
		return 0, errSilent
	}
	return round(param(DeEssThresholdID).Clamp(percentile(hiss, 0.95)-3), 2), nil
}

// percentile returns the value p of the way up vs, sorted.
func percentile(vs []float64, p float64) float64 {
	s := slices.Clone(vs)
	slices.Sort(s)
	return s[min(len(s)-1, int(p*float64(len(s))))]
}

// newChain returns a processor set as values, for the voice at rate.
func newChain(rate float64, values map[uint32]float64) *processor {
	pr := &processor{}
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: learnChunk, Channels: 2, Offline: true})
	for id, val := range values {
		pr.take(plug.Change{ID: id, Value: val})
	}
	pr.retune()
	pr.in, pr.out = pr.inTarget, pr.outTarget
	return pr
}

// reductionOf runs the voice through a chain set as values and returns
// how far the compressor whose threshold is id turns it down: for the
// peak compressor, the loudest tenth of the voice's moments over
// active, in its loudness, at Input; for the glue compressor, the
// average over them.
func reductionOf(ctx context.Context, v *voice, values map[uint32]float64, id uint32, active float64) (float64, error) {
	pr := newChain(v.rate, values)
	step := moment(v.rate)
	var downs []float64
	var sum float64
	err := v.read(ctx, func(ch [][]float32, n int, fresh bool) {
		if fresh {
			pr.Reset()
		}
		for at := 0; at < n; at += step {
			m := min(step, n-at)
			b := [][]float32{ch[0][at : at+m], ch[1][at : at+m]}
			pr.Process(&plug.Block{In: b, Out: b, Frames: m})
			st := pr.stats
			if st.peakIn <= active {
				continue
			}
			if id == PeakThresholdID {
				downs = append(downs, -st.peakLeast)
			} else {
				sum += -st.glueSum / float64(st.frames)
				downs = append(downs, 0)
			}
		}
	})
	switch {
	case err != nil:
		return 0, err
	case len(downs) == 0:
		return 0, errSilent
	case id == PeakThresholdID:
		return percentile(downs, 0.9), nil
	}
	return sum / float64(len(downs)), nil
}

// loudnessThrough runs the voice through a chain set as values and
// returns how loud it comes out, in LUFS.
func loudnessThrough(ctx context.Context, v *voice, values map[uint32]float64) (float64, error) {
	pr := newChain(v.rate, values)
	meter := audio.NewLoudnessMeter(int(v.rate))
	frames := make([]float32, 2*learnChunk)
	err := v.read(ctx, func(ch [][]float32, n int, fresh bool) {
		if fresh {
			pr.Reset()
		}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: n})
		for i := range n {
			frames[2*i], frames[2*i+1] = ch[0][i], ch[1][i]
		}
		meter.Write(frames[:2*n])
	})
	if err != nil {
		return 0, err
	}
	loud, ok := meter.Integrated()
	if !ok {
		return 0, errSilent
	}
	return loud, nil
}

// fitThreshold returns the threshold, from -60 to 0 dB, at which
// measure, how far a compressor of ratio turns the voice down, comes
// to want: a step at a time by how far a decibel of threshold moves it,
// within the span still open, from guess. It returns the threshold that
// came nearest.
func fitThreshold(measure func(t float64) (float64, error), want, guess, ratio float64) (float64, error) {
	lo, hi := -60.0, 0.0
	slope := max(1-1/ratio, 0.1)
	t := clamp(guess, lo, hi)
	best, bestMiss := t, math.Inf(1)
	for range learnFits {
		got, err := measure(t)
		if err != nil {
			return 0, err
		}
		miss := got - want
		if math.Abs(miss) < bestMiss {
			best, bestMiss = t, math.Abs(miss)
		}
		if math.Abs(miss) < learnClose {
			break
		}
		// Too much turned down raises the threshold; too little lowers it.
		if miss > 0 {
			lo = t
		} else {
			hi = t
		}
		next := t + miss/slope
		if next <= lo || next >= hi {
			next = (lo + hi) / 2
		}
		t = next
	}
	return round(best, 10), nil
}
