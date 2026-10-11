package audio

import (
	"math"
	"math/cmplx"
	"slices"
	"sync/atomic"
)

// FilterKind is the shape a [Band] of an [EQ] gives the sound.
type FilterKind int

// The kinds of band.
const (
	// Bell lifts or lowers the pitches about Freq, the more so the
	// nearer, over a width Q sets: a high Q is narrow.
	Bell FilterKind = iota
	// LowShelf lifts or lowers every pitch below Freq, and HighShelf
	// every pitch above it, by Gain.
	LowShelf
	HighShelf
	// LowCut takes away the pitches below Freq, and HighCut those
	// above it, steeper with a greater Slope.
	LowCut
	HighCut
	// Notch takes away a narrow stretch about Freq.
	Notch
)

// Band is one band of an [EQ].
type Band struct {
	// ID tells a band from the others as they change, so a band
	// moved glides there.
	ID   int
	Kind FilterKind
	// Freq is the band's frequency, in hertz; Gain how much a bell or
	// a shelf lifts, in decibels, below zero to lower; and Q how
	// narrow it is. A cut ignores Gain, and uses Q at a slope of 12.
	Freq, Gain, Q float32
	// Slope is how steeply a cut falls, in decibels an octave: 12,
	// 24, 36 or 48. Zero is 12.
	Slope int
	// On says the band changes the sound.
	On bool
	// Dynamic, where on, turns a bell's or a shelf's gain down as the
	// sound about its frequency rises over a threshold: a compressor of
	// that band alone. Other kinds ignore it.
	Dynamic Dynamics
}

// Dynamics is how a dynamic band turns itself down.
type Dynamics struct {
	On bool
	// Threshold is the level of the band's sound, in decibels, where it
	// starts to turn down, and Ratio how far over it: at 4, a sound 8 dB
	// over is turned down 6. It bends into it over a few decibels.
	Threshold, Ratio float32
	// Attack and Release are how fast it turns down and lets go again,
	// in milliseconds.
	Attack, Release float32
	// Range is the most it turns down, in decibels.
	Range float32
}

// dynamic says the band turns itself down.
func (b Band) dynamic() bool {
	return b.Dynamic.On && hasGain(b.Kind)
}

// hasGain says a band of kind k lifts and lowers by its Gain.
func hasGain(k FilterKind) bool { return k == Bell || k == LowShelf || k == HighShelf }

// bandPass is the shape of the filter a dynamic bell hears its band
// through: the pitches about its frequency, at their own level.
const bandPass FilterKind = -1

// stages returns how many second-order sections the band takes.
func (b Band) stages() int {
	if b.Kind != LowCut && b.Kind != HighCut {
		return 1
	}
	return min(max(b.Slope/12, 1), 4)
}

// biquad is one second-order section's coefficients, divided by a0.
type biquad struct{ b0, b1, b2, a1, a2 float64 }

// sections returns the band's sections at [SampleRate].
func (b Band) sections() []biquad { return b.appendSections(nil, SampleRate) }

// appendSections appends the band's sections, at rate, to out.
func (b Band) appendSections(out []biquad, rate float64) []biquad {
	n := b.stages()
	for k := range n {
		q := float64(b.Q)
		if n > 1 {
			// Butterworth, for a cut steeper than 12: each section's Q
			// from the poles of a filter of order 2n.
			q = 1 / (2 * math.Cos(float64(2*k+1)*math.Pi/float64(4*n)))
		}
		out = append(out, section(b.Kind, float64(b.Freq), float64(b.Gain), q, rate))
	}
	return out
}

// section returns the coefficients of one section, from Robert
// Bristow-Johnson's Audio EQ Cookbook.
func section(kind FilterKind, freq, gain, q, rate float64) biquad {
	freq = max(10, min(freq, rate*0.49))
	q = max(q, 0.025)
	w := 2 * math.Pi * freq / rate
	cos, sin := math.Cos(w), math.Sin(w)
	alpha := sin / (2 * q)
	a := math.Pow(10, gain/40)
	var b0, b1, b2, a0, a1, a2 float64
	switch kind {
	case Bell:
		b0, b1, b2 = 1+alpha*a, -2*cos, 1-alpha*a
		a0, a1, a2 = 1+alpha/a, -2*cos, 1-alpha/a
	case LowShelf:
		r := 2 * math.Sqrt(a) * alpha
		b0 = a * ((a + 1) - (a-1)*cos + r)
		b1 = 2 * a * ((a - 1) - (a+1)*cos)
		b2 = a * ((a + 1) - (a-1)*cos - r)
		a0 = (a + 1) + (a-1)*cos + r
		a1 = -2 * ((a - 1) + (a+1)*cos)
		a2 = (a + 1) + (a-1)*cos - r
	case HighShelf:
		r := 2 * math.Sqrt(a) * alpha
		b0 = a * ((a + 1) + (a-1)*cos + r)
		b1 = -2 * a * ((a - 1) + (a+1)*cos)
		b2 = a * ((a + 1) + (a-1)*cos - r)
		a0 = (a + 1) - (a-1)*cos + r
		a1 = 2 * ((a - 1) - (a+1)*cos)
		a2 = (a + 1) - (a-1)*cos - r
	case LowCut:
		b0, b1, b2 = (1+cos)/2, -(1 + cos), (1+cos)/2
		a0, a1, a2 = 1+alpha, -2*cos, 1-alpha
	case HighCut:
		b0, b1, b2 = (1-cos)/2, 1-cos, (1-cos)/2
		a0, a1, a2 = 1+alpha, -2*cos, 1-alpha
	case Notch:
		b0, b1, b2 = 1, -2*cos, 1
		a0, a1, a2 = 1+alpha, -2*cos, 1-alpha
	case bandPass:
		b0, b1, b2 = alpha, 0, -alpha
		a0, a1, a2 = 1+alpha, -2*cos, 1-alpha
	}
	return biquad{b0 / a0, b1 / a0, b2 / a0, a1 / a0, a2 / a0}
}

// at returns the section's gain at hz, as a complex number.
func (s biquad) at(hz float64) complex128 {
	z := cmplx.Exp(complex(0, -2*math.Pi*hz/SampleRate))
	z2 := z * z
	return (complex(s.b0, 0) + complex(s.b1, 0)*z + complex(s.b2, 0)*z2) /
		(1 + complex(s.a1, 0)*z + complex(s.a2, 0)*z2)
}

// Response returns how much the band lifts the sound at hz, in
// decibels, below zero where it lowers it; zero while it is off.
func (b Band) Response(hz float64) float64 {
	if !b.On {
		return 0
	}
	g := complex(1, 0)
	for _, s := range b.sections() {
		g *= s.at(hz)
	}
	return 20 * math.Log10(max(cmplx.Abs(g), 1e-9))
}

// Response returns how much bands together lift the sound at hz, in
// decibels.
func Response(bands []Band, hz float64) float64 {
	var db float64
	for _, b := range bands {
		db += b.Response(hz)
	}
	return db
}

// An EQ is a parametric equalizer: bands of filters, each a bell, a
// shelf, a cut or a notch, at a frequency of its own. Its [EQ.Insert]
// puts it on a voice. Bands set glide to their new settings over a
// few tens of milliseconds, so a band dragged about sweeps without
// clicks. Its methods are safe from any goroutine.
//
// Its inserts run in real time: they take no lock and make nothing
// new as they process, however the settings change. [EQ.Set] makes the
// bands anew and hands them over whole, and each insert holds room for
// [MaxBands] bands, and as many fading out, from the start.
// [EQ.SetBypass] is real time too, so an insert's own goroutine may
// call it.
type EQ struct {
	settings atomic.Pointer[eqSettings]
	bypass   atomic.Bool
	amount   atomic.Uint64
	qScale   atomic.Uint64
	shift    atomic.Uint64
}

// MaxBands is how many bands an EQ plays. [EQ.Set] keeps the first
// MaxBands of more.
const MaxBands = 32

// eqSettings are an EQ's bands, as Set hands them over: never changed
// once handed.
type eqSettings struct {
	bands []Band
}

// NewEQ returns an EQ with no bands, which leaves the sound as it is.
func NewEQ() *EQ {
	e := &EQ{}
	e.settings.Store(&eqSettings{})
	e.SetAmount(1)
	e.SetQScale(1)
	return e
}

// SetAmount scales the EQ's bells' and shelves' gains, and how far its
// dynamic bands turn down, by a, from 0 to 2, gliding there: 1 plays
// them as set, and 0 leaves them flat. Cuts and notches stay as set: a
// cut has no gain to scale, and a cut mixed with the sound it cut, to
// do it by halves, would cancel some of what it passes. It is real
// time, as SetBypass is.
func (e *EQ) SetAmount(a float64) { e.amount.Store(math.Float64bits(max(0, min(a, 2)))) }

// amountNow returns the amount set.
func (e *EQ) amountNow() float64 { return math.Float64frombits(e.amount.Load()) }

// SetQScale scales the Q of the EQ's bells, shelves and notches by s,
// from 0.25 to 4, gliding there: over 1 narrows them, and under 1
// widens them. Cuts stay as set, as their slope is their shape. It is
// real time, as SetBypass is.
func (e *EQ) SetQScale(s float64) { e.qScale.Store(math.Float64bits(max(0.25, min(s, 4)))) }

// qScaleNow returns the Q's scale set.
func (e *EQ) qScaleNow() float64 { return math.Float64frombits(e.qScale.Load()) }

// SetThresholdShift moves every dynamic band's threshold by db
// decibels, from -48 to 48: up, they turn down later, and down, sooner.
// It is real time, as SetBypass is.
func (e *EQ) SetThresholdShift(db float64) { e.shift.Store(math.Float64bits(max(-48, min(db, 48)))) }

// shiftNow returns the thresholds' shift set.
func (e *EQ) shiftNow() float64 { return math.Float64frombits(e.shift.Load()) }

// Scaled returns b with its Q scaled by s, as [EQ.SetQScale] plays it:
// a cut's as it was.
func (b Band) Scaled(s float64) Band {
	if b.Kind != LowCut && b.Kind != HighCut {
		b.Q = float32(float64(b.Q) * max(0.25, min(s, 4)))
	}
	return b
}

// Set sets the EQ's bands. Each glides from where it was, matched by
// its ID; a band new to the EQ fades in, and one gone fades out.
func (e *EQ) Set(bands []Band) {
	e.settings.Store(&eqSettings{bands: slices.Clone(bands[:min(len(bands), MaxBands)])})
}

// SetBypass turns the whole EQ off, or on again, fading.
func (e *EQ) SetBypass(on bool) { e.bypass.Store(on) }

// Insert returns an insert that puts the EQ on one voice, for
// [Options.Insert]: each voice needs its own, which follows the EQ's
// settings.
func (e *EQ) Insert() Insert { return newEQInsert(e) }

// InsertAt returns an insert that puts the EQ on a stream of
// interleaved stereo frames at hz frames a second, played other than
// through a [Mixer], as a plugin's sound is. A mixer it plays in sets
// its own rate.
func (e *EQ) InsertAt(hz int) Insert {
	in := newEQInsert(e)
	in.setRate(hz)
	return in
}

// glideAt is how much of the way to its settings a band goes each
// block of 128 frames at rate: most of it in about 25 ms.
func glideAt(rate float64) float64 { return 1 - math.Exp(-float64(block)/rate/0.025) }

// liveBands is how many bands an insert has room for: as many as an EQ
// plays, and as many again fading out as others fade in.
const liveBands = 2 * MaxBands

// maxStages is the most sections a band takes: a cut at 48 dB an
// octave.
const maxStages = 4

// eqInsert is an EQ on one voice: each band's settings as they glide,
// and the state of its sections.
type eqInsert struct {
	eq *EQ
	// bands are the bands playing, in pool, and free those of pool not.
	pool  [liveBands]liveBand
	bands []*liveBand
	free  []*liveBand
	// mix fades the whole EQ in and out for a bypass, and amount and
	// qScale glide to the EQ's.
	mix, amount, qScale float64
	// rate is the mixer's, which the bands' sections are worked out
	// for, and glide how far they glide each block at it.
	rate, glide float64
}

func newEQInsert(e *EQ) *eqInsert {
	in := &eqInsert{eq: e, bands: make([]*liveBand, 0, liveBands), free: make([]*liveBand, 0, liveBands),
		amount: e.amountNow(), qScale: e.qScaleNow()}
	for i := range in.pool {
		in.free = append(in.free, &in.pool[i])
	}
	return in
}

// setRate tells the insert the rate of the mixer it plays in, which
// the mixer does before each block.
func (e *eqInsert) setRate(hz int) {
	if r := float64(hz); r != e.rate {
		e.rate, e.glide = r, glideAt(r)
		for _, b := range e.bands {
			// Worked out anew at the new rate.
			b.fresh = true
		}
	}
}

// liveBand is a band as it plays: its settings now, gliding, its
// sections, and their state, two values a section and channel.
type liveBand struct {
	id                  int
	kind                FilterKind
	slope               int
	logFreq, gain, logQ float64
	// mix fades the band in and out as it turns on and off, or comes
	// and goes.
	mix  float64
	want Band
	gone bool
	// secs are the band's sections, in room, and fresh says they are
	// to be worked out anew; dirty says its settings moved since, and
	// played is the gain they were last worked out for.
	secs   []biquad
	room   [maxStages]biquad
	fresh  bool
	dirty  bool
	played float64
	state  [maxStages][4]float64
	// A dynamic band hears its band through side, with its state; level
	// is the band's power, smoothed, and down how far it turns down, in
	// decibels.
	side      biquad
	sideState [4]float64
	level     float64
	down      float64
}

// dynSub is how many frames a dynamic band, or an EQ whose amount
// moves, plays between working its sections out anew.
const dynSub = 32

// Process implements [Insert]: it follows the EQ's settings a block's
// way, and filters frames through each band in turn.
func (e *eqInsert) Process(frames []float32) {
	set, bypass := e.eq.settings.Load(), e.eq.bypass.Load()
	if e.rate == 0 {
		e.setRate(SampleRate)
	}
	e.follow(set.bands)
	target := 1.0
	if bypass {
		target = 0
	}
	e.mix += (target - e.mix) * e.glide
	if !bypass && e.mix > 0.999 {
		e.mix = 1
	}
	if e.mix < 0.001 && bypass {
		e.mix = 0
		return
	}
	want := e.eq.amountNow()
	e.amount += (want - e.amount) * e.glide
	if math.Abs(want-e.amount) < 1e-4 {
		e.amount = want
	}
	// The Q's scale glides in octaves, and each band is worked out anew
	// as it moves.
	if wantQ := e.eq.qScaleNow(); wantQ != e.qScale {
		e.qScale *= math.Pow(wantQ/e.qScale, e.glide)
		if math.Abs(math.Log(wantQ/e.qScale)) < 1e-4 {
			e.qScale = wantQ
		}
		for _, b := range e.bands {
			b.dirty = true
		}
	}
	shift := e.eq.shiftNow()
	n := len(frames) / 2
	for at := 0; at < n; at += dynSub {
		part := frames[2*at : 2*min(at+dynSub, n)]
		for _, b := range e.bands {
			if b.mix == 0 {
				continue
			}
			b.hear(part, e.rate, shift)
			b.shape(e.rate, e.amount, e.qScale)
			b.filter(part, b.mix*e.mix)
		}
	}
}

// filter filters frames through the band, mixed in by wet.
func (b *liveBand) filter(frames []float32, wet float64) {
	for i := range len(frames) / 2 {
		for ch := range 2 {
			x := float64(frames[2*i+ch])
			y := x
			for k, s := range b.secs {
				st := &b.state[k]
				// Transposed direct form II, a channel's two values side
				// by side.
				out := s.b0*y + st[2*ch]
				st[2*ch] = s.b1*y - s.a1*out + st[2*ch+1]
				st[2*ch+1] = s.b2*y - s.a2*out
				y = out
			}
			frames[2*i+ch] = float32(x + (y-x)*wet)
		}
	}
}

// hear has a dynamic band hear frames, the sound coming to it, about
// its frequency, and work out how far to turn down: its power, smoothed
// over a few of its periods, over the threshold, moved by shift, by the
// ratio, gliding there at its attack and back at its release.
func (b *liveBand) hear(frames []float32, rate, shift float64) {
	n := len(frames) / 2
	if !b.want.dynamic() || b.kind != b.want.Kind {
		// No longer dynamic: back up over 50 ms, without a click.
		b.level = 0
		if b.down *= math.Exp(-float64(n) / (0.05 * rate)); b.down < 0.01 {
			b.down = 0
		}
		return
	}
	s, st := b.side, &b.sideState
	var sum float64
	for i := range n {
		power := 0.0
		for ch := range 2 {
			x := float64(frames[2*i+ch])
			y := s.b0*x + st[2*ch]
			st[2*ch] = s.b1*x - s.a1*y + st[2*ch+1]
			st[2*ch+1] = s.b2*x - s.a2*y
			power = max(power, y*y)
		}
		sum += power
	}
	// Smoothed over two periods of the band's frequency, and no less
	// than 5 ms, so a low band's own waves do not move it.
	window := max(0.005, 2/math.Exp(b.logFreq))
	b.level += (sum/float64(n) - b.level) * (1 - math.Exp(-float64(n)/(window*rate)))
	// A sine's peak is 3 dB over its power.
	db := 10*math.Log10(max(b.level, 1e-12)) + 3
	d := b.want.Dynamic
	want := min(max(curve(db-float64(d.Threshold)-shift, float64(d.Ratio)), 0), float64(max(d.Range, 0)))
	ms := float64(d.Release)
	if want > b.down {
		ms = float64(d.Attack)
	}
	b.down += (want - b.down) * (1 - math.Exp(-float64(n)/(max(ms, 0.05)/1000*rate)))
}

// dynKnee is how far, in decibels, a dynamic band bends into its ratio
// about its threshold.
const dynKnee = 6

// curve returns how far a compressor at ratio turns down a sound over
// its threshold by over decibels, bending in over dynKnee.
func curve(over, ratio float64) float64 {
	slope := 1 - 1/max(ratio, 1)
	switch {
	case 2*over <= -dynKnee:
		return 0
	case 2*over < dynKnee:
		x := over + dynKnee/2
		return slope * x * x / (2 * dynKnee)
	default:
		return slope * over
	}
}

// shape works the band's sections out anew where its settings moved,
// or the gain it plays at, scaled by amount and turned down as it hears
// its band, moved; its Q is scaled by qScale.
func (b *liveBand) shape(rate, amount, qScale float64) {
	gain := (b.gain - b.down) * amount
	if !b.dirty && math.Abs(gain-b.played) < 0.005 {
		return
	}
	now := Band{Kind: b.kind, Slope: b.slope, Freq: float32(math.Exp(b.logFreq)), Gain: float32(gain), Q: float32(math.Exp(b.logQ))}.Scaled(qScale)
	n := len(b.secs)
	b.secs = now.appendSections(b.room[:0], rate)
	if len(b.secs) != n {
		// Other sections start from silence.
		b.state = [maxStages][4]float64{}
	}
	if b.dirty {
		switch b.kind {
		case LowShelf:
			b.side = section(HighCut, float64(now.Freq), 0, 0.707, rate)
		case HighShelf:
			b.side = section(LowCut, float64(now.Freq), 0, 0.707, rate)
		default:
			b.side = section(bandPass, float64(now.Freq), 0, float64(now.Q), rate)
		}
	}
	b.played, b.dirty = gain, false
}

// Reduction returns how far the insert's band id turns itself down now,
// in decibels, below zero, as the amount scales it; zero for a band
// that is not dynamic, and false for none.
func (e *eqInsert) Reduction(id int) (float64, bool) {
	for _, b := range e.bands {
		if b.id == id {
			return -b.down * e.amount, true
		}
	}
	return 0, false
}

// follow moves each band a block's way toward its settings, adding
// bands new to the EQ and dropping those faded out. A band new while
// the room is full of bands fading out comes in once one has.
func (e *eqInsert) follow(want []Band) {
	for _, b := range e.bands {
		b.gone = true
	}
	for _, w := range want {
		var lb *liveBand
		for _, b := range e.bands {
			if b.id == w.ID {
				lb = b
				break
			}
		}
		if lb == nil {
			if len(e.free) == 0 {
				continue
			}
			lb = e.free[len(e.free)-1]
			e.free = e.free[:len(e.free)-1]
			*lb = liveBand{id: w.ID, kind: w.Kind, slope: w.Slope, fresh: true,
				logFreq: math.Log(float64(max(w.Freq, 10))), gain: float64(w.Gain), logQ: math.Log(float64(max(w.Q, 0.025)))}
			e.bands = append(e.bands, lb)
		}
		lb.gone, lb.want = false, w
	}
	live := e.bands[:0]
	for _, b := range e.bands {
		on := !b.gone && b.want.On
		if b.want.Kind != b.kind || b.want.Slope != b.slope {
			// A band changing kind fades out, and in again as the new
			// kind.
			on = false
			if b.mix == 0 {
				b.kind, b.slope = b.want.Kind, b.want.Slope
				b.fresh, b.secs, b.state = true, nil, [maxStages][4]float64{}
			}
		}
		target := 0.0
		if on {
			target = 1
		}
		b.mix += (target - b.mix) * e.glide
		if math.Abs(target-b.mix) < 0.001 {
			b.mix = target
		}
		if b.gone && b.mix == 0 {
			e.free = append(e.free, b)
			continue
		}
		b.move(e.glide)
		live = append(live, b)
	}
	clear(e.bands[len(live):])
	e.bands = live
}

// move glides the band's frequency, gain and Q toward its settings,
// and marks its sections to be worked out again where they moved.
func (b *liveBand) move(glide float64) {
	w := b.want
	tf, tg, tq := math.Log(float64(max(w.Freq, 10))), float64(w.Gain), math.Log(float64(max(w.Q, 0.025)))
	moved := b.fresh
	step := func(v *float64, to, near float64) {
		if math.Abs(to-*v) < near {
			if *v != to {
				moved = true
			}
			*v = to
			return
		}
		*v += (to - *v) * glide
		moved = true
	}
	step(&b.logFreq, tf, 1e-4)
	step(&b.gain, tg, 1e-3)
	step(&b.logQ, tq, 1e-4)
	if moved {
		b.dirty, b.fresh = true, false
	}
}
