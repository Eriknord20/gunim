package vocal

import (
	"math"

	"github.com/marrasen/gunim/audio/plug"
)

// The chain's fixed settings: what the user does not turn.
const (
	// deEssRatio is how hard the de-esser turns down the hiss over its
	// threshold, before Range stops it.
	deEssRatio = 4
	// The parallel compressor: squeezed hard and made up, so the voice's
	// quiet parts come up under it.
	parThreshold = -34
	parRatio     = 8
	parAttack    = 3
	parRelease   = 90
	parMakeup    = 14
	// The knee every compressor bends over, in decibels.
	knee = 4
	// tapeDrive is how far Tape & Dist drives the tape, at full, in
	// decibels, and tapeDull how far it dulls the top.
	tapeDrive = 20
	tapeDull  = -3
	// The octave below: the band of the voice it follows, and the band
	// of the sound it makes.
	octaveFollow = 500
	octaveTone   = 1400
	// widthDelay is how late, in seconds, the highs come into the sides
	// that Width spreads, as late as the ear hears as width and not as
	// an echo.
	widthDelay = 0.009
	// glide is how long a gain takes to get most of the way to a new
	// setting, so a move makes no click.
	glide = 0.01
)

func dbOf(v float64) float64 { return 20 * math.Log10(max(v, 1e-9)) }

func gainOf(db float64) float64 { return math.Pow(10, db/20) }

// coef is how much of the way a smoother of ms milliseconds goes in a
// frame at rate.
func coef(ms, rate float64) float64 { return 1 - math.Exp(-1/(ms/1000*rate)) }

// biquad is a second-order filter, as the Audio EQ Cookbook gives them,
// run in transposed direct form II.
type biquad struct {
	b0, b1, b2, a1, a2 float64
}

// biquadState is what one channel's filter holds from frame to frame.
type biquadState struct{ z1, z2 float64 }

func (f *biquad) run(s *biquadState, x float64) float64 {
	y := f.b0*x + s.z1
	s.z1 = f.b1*x - f.a1*y + s.z2
	s.z2 = f.b2*x - f.a2*y
	return y
}

// The kinds of filter.
const (
	lowShelf = iota
	highShelf
	bell
	highPass
	lowPass
)

// set makes f a filter of kind about freq, gain decibels up, of width
// q, at rate. A frequency past the top of the rate is pulled under it.
func (f *biquad) set(kind int, freq, gain, q, rate float64) {
	freq = min(freq, 0.45*rate)
	w := 2 * math.Pi * freq / rate
	cw, sw := math.Cos(w), math.Sin(w)
	a := math.Pow(10, gain/40)
	alpha := sw / (2 * q)
	var b0, b1, b2, a0, a1, a2 float64
	switch kind {
	case bell:
		b0, b1, b2 = 1+alpha*a, -2*cw, 1-alpha*a
		a0, a1, a2 = 1+alpha/a, -2*cw, 1-alpha/a
	case lowShelf:
		sa := 2 * math.Sqrt(a) * alpha
		b0, b1, b2 = a*((a+1)-(a-1)*cw+sa), 2*a*((a-1)-(a+1)*cw), a*((a+1)-(a-1)*cw-sa)
		a0, a1, a2 = (a+1)+(a-1)*cw+sa, -2*((a-1)+(a+1)*cw), (a+1)+(a-1)*cw-sa
	case highShelf:
		sa := 2 * math.Sqrt(a) * alpha
		b0, b1, b2 = a*((a+1)+(a-1)*cw+sa), -2*a*((a-1)+(a+1)*cw), a*((a+1)+(a-1)*cw-sa)
		a0, a1, a2 = (a+1)-(a-1)*cw+sa, 2*((a-1)-(a+1)*cw), (a+1)-(a-1)*cw-sa
	case highPass:
		b0, b1, b2 = (1+cw)/2, -(1 + cw), (1+cw)/2
		a0, a1, a2 = 1+alpha, -2*cw, 1-alpha
	case lowPass:
		b0, b1, b2 = (1-cw)/2, 1-cw, (1-cw)/2
		a0, a1, a2 = 1+alpha, -2*cw, 1-alpha
	}
	f.b0, f.b1, f.b2, f.a1, f.a2 = b0/a0, b1/a0, b2/a0, a1/a0, a2/a0
}

// curve returns how far a compressor turns a level down, in decibels,
// as a negative number: over its threshold by over decibels, at ratio,
// bending softly over the knee.
func curve(over, ratio float64) float64 {
	slope := 1 - 1/ratio
	switch {
	case over <= -knee/2:
		return 0
	case over < knee/2:
		x := over + knee/2
		return -slope * x * x / (2 * knee)
	}
	return -slope * over
}

// comp is a compressor's gain, in decibels, as it follows the level it
// hears: down at attack, back at release.
type comp struct {
	gr, attack, release float64
}

// follow moves the gain toward target, a reduction in decibels, and
// returns it.
func (c *comp) follow(target float64) float64 {
	if target < c.gr {
		c.gr += (target - c.gr) * c.attack
	} else {
		c.gr += (target - c.gr) * c.release
	}
	return c.gr
}

// maxChannels is how many channels the chain runs: a voice in mono or
// in stereo.
const maxChannels = 2

// processor runs the chain.
type processor struct {
	rate float64
	// values are the parameters' values, by ID, as they last came.
	values [128]float64
	// dirty says the filters must be worked out again before the next
	// frame.
	dirty bool
	fresh bool

	// The de-esser: the band it listens to, the voice under the band it
	// turns down, and its gain.
	deEss    biquad
	deEssSt  [maxChannels]biquadState
	deEssLow biquad
	deEssLo  [maxChannels]biquadState
	deEssGR  comp
	deEssOn  bool
	deEssAmt float64

	// The gains, gliding: Input and Output.
	in, inTarget, out, outTarget, step float64

	// The EQs: the clean EQ's shelf and cut, the tone EQ's high-pass,
	// lift and air, each with its channels' state.
	eq      [5]biquad
	eqSt    [5][maxChannels]biquadState
	cleanOn bool
	toneOn  bool

	// The compressors in series, and the parallel one.
	peak, glue, par    comp
	peakOn, glueOn     bool
	glueMS             float64
	glueMSCoef         float64
	parMS, parMSCoef   float64
	parMix             float64
	tapeAmt, octaveAmt float64
	widthAmt           float64

	// Tape: its tone after the drive.
	tape   biquad
	tapeSt [maxChannels]biquadState

	// The octave below: the band it follows, the flip-flop that halves
	// it, how loud it is, and its tone.
	octFollow       [2]biquad
	octFollowSt     [2]biquadState
	octTone         biquad
	octToneSt       biquadState
	octFlip, octEnv float64
	octArmed        bool
	octEnvUp        float64
	octEnvDown      float64

	// Width: the highs of the centre and the sides, and the line that
	// delays the centre's.
	widthHP  biquad
	widthSt  [2]biquadState
	line     []float64
	lineAt   int
	lineSize int

	// What the processor reports: how far each part turns the voice
	// down, the least over the last block, and by frame.
	deEssMeter, peakMeter, glueMeter plug.Meter
	deEssTrace, peakTrace, glueTrace plug.Trace
	// stats are the last block's, for Learn: how far each compressor
	// turned it down, the most and the average, and its loudest frame
	// going into the peak compressor.
	stats blockStats
}

// blockStats is what a block did, for Learn.
type blockStats struct {
	peakLeast, glueLeast, glueSum float64
	peakIn                        float64
	frames                        int
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.step = coef(glide*1000, s.Rate)
	p.in, p.inTarget, p.out, p.outTarget = 1, 1, 1, 1
	for _, prm := range Descriptor.Params {
		p.values[prm.ID] = prm.Default
	}
	p.lineSize = int(math.Ceil(widthDelay*s.Rate)) + 1
	if len(p.line) < p.lineSize {
		p.line = make([]float64, p.lineSize)
	}
	p.octEnvUp, p.octEnvDown = coef(2, s.Rate), coef(40, s.Rate)
	p.glueMSCoef, p.parMSCoef = coef(10, s.Rate), coef(5, s.Rate)
	p.fresh = true
	p.retune()
	p.Reset()
}

// Latency implements [plug.Processor]: the chain is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: Width's highs ring on as long as
// it delays them.
func (p *processor) Tail() int { return p.lineSize }

// Reset implements [plug.Processor]: the filters, the gains and the
// line forget what they held, and the gains stop gliding.
func (p *processor) Reset() {
	p.deEssSt = [maxChannels]biquadState{}
	p.deEssLo = [maxChannels]biquadState{}
	p.eqSt = [5][maxChannels]biquadState{}
	p.tapeSt = [maxChannels]biquadState{}
	p.octFollowSt = [2]biquadState{}
	p.octToneSt = biquadState{}
	p.widthSt = [2]biquadState{}
	clear(p.line)
	p.lineAt = 0
	p.deEssGR.gr, p.peak.gr, p.glue.gr, p.par.gr = 0, 0, 0, 0
	p.glueMS, p.parMS = 0, 0
	p.octFlip, p.octEnv, p.octArmed = 1, 0, false
	p.in, p.out = p.inTarget, p.outTarget
	p.deEssMeter.Set(0)
	p.peakMeter.Set(0)
	p.glueMeter.Set(0)
}

// take takes a change to a parameter.
func (p *processor) take(c plug.Change) {
	prm, ok := Descriptor.Param(c.ID)
	if !ok || prm.Flags&plug.ReadOnly != 0 {
		return
	}
	p.values[c.ID] = prm.Clamp(c.Value)
	p.dirty = true
}

// retune works out the chain from the values: the filters, the
// compressors' times and the gains to glide to.
func (p *processor) retune() {
	v, rate := &p.values, p.rate
	p.dirty = false
	p.inTarget, p.outTarget = gainOf(v[InputID]), gainOf(v[OutputID])
	if p.fresh {
		p.in, p.out, p.fresh = p.inTarget, p.outTarget, false
	}

	p.deEssOn, p.deEssAmt = v[DeEssOnID] > 0.5, v[DeEssID]/100
	p.deEss.set(highPass, v[DeEssFreqID], 0, 0.707, rate)
	p.deEssLow.set(lowPass, v[DeEssFreqID], 0, 0.707, rate)
	p.deEssGR.attack, p.deEssGR.release = coef(0.5, rate), coef(60, rate)

	rumble, lift := v[RumbleID]/100, v[PresenceID]/100
	p.cleanOn, p.toneOn = v[CleanOnID] > 0.5, v[ToneOnID] > 0.5
	p.eq[0].set(lowShelf, v[LowShelfFqID], rumble*v[LowShelfID], 0.707, rate)
	p.eq[1].set(bell, v[MudFreqID], rumble*v[MudGainID], v[MudQID], rate)
	p.eq[2].set(highPass, v[HighPassID], 0, 0.707, rate)
	p.eq[3].set(bell, v[PresenceFreqID], lift*v[PresenceGainID], 0.8, rate)
	p.eq[4].set(highShelf, v[AirFreqID], lift*v[AirGainID], 0.707, rate)

	p.peakOn, p.glueOn = v[PeakOnID] > 0.5, v[GlueOnID] > 0.5
	p.peak.attack, p.peak.release = coef(v[PeakAttackID], rate), coef(v[PeakReleaseID], rate)
	p.glue.attack, p.glue.release = coef(v[GlueAttackID], rate), coef(v[GlueReleaseID], rate)
	p.par.attack, p.par.release = coef(parAttack, rate), coef(parRelease, rate)

	p.parMix, p.tapeAmt, p.octaveAmt, p.widthAmt = v[ParallelID]/100, v[TapeID]/100, v[OctaveID]/100, v[WidthID]/100
	p.tape.set(highShelf, 9000, tapeDull*p.tapeAmt, 0.707, rate)
	p.octFollow[0].set(lowPass, octaveFollow, 0, 0.707, rate)
	p.octFollow[1].set(lowPass, octaveFollow, 0, 0.707, rate)
	p.octTone.set(lowPass, octaveTone, 0, 0.707, rate)
	p.widthHP.set(highPass, v[WidthFreqID], 0, 0.707, rate)
}

// ratioOf returns a compressor's ratio, as Comp scales how far
// it is over 1:1.
func (p *processor) ratioOf(id uint32) float64 {
	return 1 + (p.values[id]-1)*p.values[SquashID]/100
}

// Process implements [plug.Processor]: each change takes effect from
// its frame on.
func (p *processor) Process(b *plug.Block) {
	chans := min(len(b.Out), len(b.In), maxChannels)
	next := 0
	st := blockStats{peakIn: math.Inf(-1)}
	var deEssLeast float64
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		if p.dirty {
			p.retune()
		}
		var x [maxChannels]float64
		for ch := range chans {
			x[ch] = float64(b.In[ch][i])
		}
		f := b.Position.Frame + int64(i)
		deEssLeast = min(deEssLeast, p.runDeEss(&x, chans, f))
		p.in += (p.inTarget - p.in) * p.step
		for ch := range chans {
			x[ch] *= p.in
		}
		p.runEQ(&x, chans)
		p.runComps(&x, chans, f, &st)
		p.runColour(&x, chans)
		if chans == 2 {
			p.runWidth(&x)
		}
		p.out += (p.outTarget - p.out) * p.step
		for ch := range chans {
			b.Out[ch][i] = float32(x[ch] * p.out)
		}
		for ch := chans; ch < len(b.Out); ch++ {
			b.Out[ch][i] = b.Out[0][i]
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	st.frames = b.Frames
	p.stats = st
	p.deEssMeter.Set(deEssLeast)
	p.peakMeter.Set(st.peakLeast)
	p.glueMeter.Set(st.glueLeast)
}

// runDeEss turns down the hiss in x, frame f, and returns how far. It
// listens to the band over its frequency, and turns down what is over
// the voice under it: the voice less its low band, so with no turning
// down, the voice comes out as it went in, and turned down, the hiss
// comes down by as much as it is turned down, whatever its phase.
func (p *processor) runDeEss(x *[maxChannels]float64, chans int, f int64) float64 {
	var low [maxChannels]float64
	var level float64
	for ch := range chans {
		level = max(level, math.Abs(p.deEss.run(&p.deEssSt[ch], x[ch])))
		low[ch] = p.deEssLow.run(&p.deEssLo[ch], x[ch])
	}
	target := 0.0
	if p.deEssOn && p.deEssAmt > 0 {
		lim := -p.values[DeEssRangeID] * p.deEssAmt
		target = max(curve(dbOf(level)-p.values[DeEssThresholdID], deEssRatio), lim)
	}
	gr := p.deEssGR.follow(target)
	p.deEssTrace.Put(f, float32(gr))
	if gr < -0.001 {
		g := gainOf(gr)
		for ch := range chans {
			x[ch] -= (1 - g) * (x[ch] - low[ch])
		}
	}
	return gr
}

// runEQ runs the clean EQ and the tone EQ over x.
func (p *processor) runEQ(x *[maxChannels]float64, chans int) {
	from, to := 0, 5
	if !p.cleanOn {
		from = 2
	}
	if !p.toneOn {
		to = 2
	}
	for k := from; k < to; k++ {
		for ch := range chans {
			x[ch] = p.eq[k].run(&p.eqSt[k][ch], x[ch])
		}
	}
}

// runComps runs the peak and the glue compressors over x, frame f, in
// series, and then the parallel one under them, keeping what they did
// in st.
func (p *processor) runComps(x *[maxChannels]float64, chans int, f int64, st *blockStats) {
	var level float64
	for ch := range chans {
		level = max(level, math.Abs(x[ch]))
	}
	st.peakIn = max(st.peakIn, dbOf(level))
	// The peak compressor hears each frame's peak, and its gain follows.
	target := 0.0
	if p.peakOn {
		target = curve(dbOf(level)-p.values[PeakThresholdID], p.ratioOf(PeakRatioID))
	}
	gr := p.peak.follow(target)
	p.peakTrace.Put(f, float32(gr))
	st.peakLeast = min(st.peakLeast, gr)
	g := gainOf(gr)
	var power float64
	for ch := range chans {
		x[ch] *= g
		power = max(power, x[ch]*x[ch])
	}
	// The glue compressor hears the power over ten milliseconds, and
	// lets go slower the further down it is, as an optical cell.
	p.glueMS += (power - p.glueMS) * p.glueMSCoef
	target = 0
	if p.glueOn {
		target = curve(10*math.Log10(max(p.glueMS, 1e-18))-p.values[GlueThresholdID], p.ratioOf(GlueRatioID))
	}
	rel := p.glue.release
	p.glue.release = rel / (1 + 0.15*-p.glue.gr)
	gr = p.glue.follow(target)
	p.glue.release = rel
	p.glueTrace.Put(f, float32(gr))
	st.glueLeast = min(st.glueLeast, gr)
	st.glueSum += gr
	g = gainOf(gr)
	power = 0
	for ch := range chans {
		x[ch] *= g
		power = max(power, x[ch]*x[ch])
	}
	if p.parMix <= 0 {
		return
	}
	// The parallel compressor squeezes the voice hard, and the voice
	// comes out with it under: its quiet parts come up the most.
	p.parMS += (power - p.parMS) * p.parMSCoef
	gr = p.par.follow(curve(10*math.Log10(max(p.parMS, 1e-18))-parThreshold, parRatio))
	g = gainOf(gr + parMakeup)
	for ch := range chans {
		x[ch] = (x[ch] + p.parMix*x[ch]*g) / (1 + 0.5*p.parMix)
	}
}

// runColour runs tape and the octave below over x.
func (p *processor) runColour(x *[maxChannels]float64, chans int) {
	if p.tapeAmt > 0 {
		// Driven into a curve that leans a little, as tape does, with
		// the small sound coming out as loud as it went in, and the top
		// dulled.
		drive := gainOf(tapeDrive * p.tapeAmt)
		bias := 0.15 * p.tapeAmt
		wet := min(1, 3*p.tapeAmt)
		for ch := range chans {
			u := x[ch] * drive
			sat := (math.Tanh(u+bias) - math.Tanh(bias)) / drive
			sat = p.tape.run(&p.tapeSt[ch], sat)
			x[ch] += wet * (sat - x[ch])
		}
	}
	if p.octaveAmt <= 0 {
		return
	}
	// The octave below, as an analog octaver makes it: the voice's low
	// band flips a flip-flop each time it rises through zero, so the
	// flip-flop runs at half its pitch; a square of it, as loud as the
	// band, distorted and dulled, goes under the voice.
	var m float64
	for ch := range chans {
		m += x[ch]
	}
	m /= float64(chans)
	low := p.octFollow[1].run(&p.octFollowSt[1], p.octFollow[0].run(&p.octFollowSt[0], m))
	a := math.Abs(low)
	if a > p.octEnv {
		p.octEnv += (a - p.octEnv) * p.octEnvUp
	} else {
		p.octEnv += (a - p.octEnv) * p.octEnvDown
	}
	h := 0.1 * p.octEnv
	switch {
	case low < -h:
		p.octArmed = true
	case low > h && p.octArmed:
		p.octArmed = false
		p.octFlip = -p.octFlip
	}
	sub := p.octTone.run(&p.octToneSt, math.Tanh(3*p.octFlip*p.octEnv))
	for ch := range chans {
		x[ch] += p.octaveAmt * 0.5 * sub
	}
}

// runWidth spreads the highs of x, a stereo frame, to the sides: the
// sides' own highs grow, and the centre's, a few milliseconds late,
// come in to the sides, the left as much up as the right down, so the
// two summed to mono stay as they were.
func (p *processor) runWidth(x *[maxChannels]float64) {
	mid, side := (x[0]+x[1])/2, (x[0]-x[1])/2
	hiMid := p.widthHP.run(&p.widthSt[0], mid)
	hiSide := p.widthHP.run(&p.widthSt[1], side)
	late := p.line[p.lineAt]
	p.line[p.lineAt] = hiMid
	p.lineAt = (p.lineAt + 1) % p.lineSize
	if p.widthAmt <= 0 {
		return
	}
	side += p.widthAmt * (hiSide + 0.45*late)
	x[0], x[1] = mid+side, mid-side
}

var (
	_ plug.Reporter = (*processor)(nil)
	_ plug.Tracer   = (*processor)(nil)
)

// Report implements [plug.Reporter].
func (p *processor) Report(id uint32) float64 {
	switch id {
	case DeEssGRID:
		return p.deEssMeter.Get()
	case PeakGRID:
		return p.peakMeter.Get()
	case GlueGRID:
		return p.glueMeter.Get()
	}
	return 0
}

// Trace implements [plug.Tracer]: how far each part turns the voice
// down, by frame.
func (p *processor) Trace(id uint32) *plug.Trace {
	switch id {
	case DeEssGRID:
		return &p.deEssTrace
	case PeakGRID:
		return &p.peakTrace
	case GlueGRID:
		return &p.glueTrace
	}
	return nil
}
