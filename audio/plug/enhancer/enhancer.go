// Package enhancer brings out a sound by adding harmonics to it, a band
// of pitches at a time, as a mastering engineer's exciter does: warmth
// added low, presence in the middle, air at the top. It splits the sound
// into four bands at three crossovers, drives each through a curve of
// its own style, as hard as the band's drive, and adds what the curve
// adds, the band's harmonics, to the sound as it was, as much as the
// band's amount, so a band at none is left exactly as it was. The
// driven band is brought back to the band's own level first, so the
// curve adds harmonics without taking the band's pitch down with it, and
// an amount past all adds more of them still.
package enhancer

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/xover"
)

// bands is how many bands the enhancer splits the sound into.
const bands = xover.Bands

// The parameters' IDs: the crossovers, each band's amount, its style and
// its drive, the mix and the output, and how much each band adds as it
// plays.
const (
	CrossoverID uint32 = 1  // to 3
	AmountID    uint32 = 10 // to 13
	StyleID     uint32 = 20 // to 23
	MixID       uint32 = 30
	OutputID    uint32 = 31
	AddedID     uint32 = 40 // to 43
	DriveID     uint32 = 50 // to 53
)

// The styles, each a curve the band is driven through.
const (
	Tape  = iota // soft and even, odd harmonics
	Tube         // leaning, even harmonics first
	Warm         // tape and tube, half each
	Crisp        // sharper, the high harmonics further up
	styles
)

var (
	styleNames = []string{"Tape", "Tube", "Warm", "Crisp"}
	styleTips  = []string{
		"Tape: rounds the peaks off softly, adding odd harmonics: glue and weight",
		"Tube: leans to one side, adding even harmonics first: warm and full, an octave over the note",
		"Warm: tape and tube, half each",
		"Crisp: a sharper bend, its harmonics reaching higher: bite and air",
	}
)

// bandNames name the bands, low to high.
var bandNames = [bands]string{"Low", "Low mid", "High mid", "High"}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

func hz(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.3g kHz", v/1000)
	}
	return fmt.Sprintf("%.0f Hz", v)
}

func db(v float64) string { return fmt.Sprintf("%+.1f dB", v) }

func driveFmt(v float64) string { return fmt.Sprintf("%.1f dB", v) }

// Descriptor is the plugin, as it is registered.
var Descriptor = func() plug.Descriptor {
	d := plug.Descriptor{
		ID:       "marrasen.enhancer",
		Name:     "Enhancer",
		Vendor:   "Marras",
		Version:  "0.1.0",
		Kind:     "Fx|Distortion|Mastering",
		Tagline:  "Harmonics, band by band",
		Keywords: []string{"exciter", "saturation", "harmonics", "drive", "warmth", "presence", "air", "distortion", "multiband"},
		New:      func(h plug.Host) plug.Plugin { return &plugin{} },
	}
	homes := [bands - 1][3]float64{{30, 1000, 120}, {200, 5000, 1200}, {1000, 16000, 6000}}
	for i, x := range homes {
		d.Params = append(d.Params, plug.Param{ID: CrossoverID + uint32(i), Name: fmt.Sprintf("Split %d", i+1),
			Unit: "Hz", Min: x[0], Max: x[1], Default: x[2], Log: true, Flags: plug.Automatable, Format: hz})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: AmountID + uint32(b), Name: "Amount", Unit: "%", Max: 200,
			Flags: plug.Automatable, Format: pct})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: StyleID + uint32(b), Name: "Style", Max: styles - 1,
			Steps: styles - 1, Flags: plug.Automatable,
			Format: func(v float64) string { return styleNames[int(math.Round(v))] }})
	}
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: DriveID + uint32(b), Name: "Drive", Unit: "dB", Max: 36, Default: 12,
			Flags: plug.Automatable, Format: driveFmt})
	}
	d.Params = append(d.Params,
		plug.Param{ID: MixID, Name: "Mix", Unit: "%", Max: 100, Default: 100, Flags: plug.Automatable, Format: pct},
		plug.Param{ID: OutputID, Name: "Output", Unit: "dB", Min: -12, Max: 12, Flags: plug.Automatable, Format: db})
	for b := range bands {
		d.Params = append(d.Params, plug.Param{ID: AddedID + uint32(b), Name: bandNames[b] + " added", Unit: "dB",
			Min: addedFloor, Max: 0, Default: addedFloor, Flags: plug.ReadOnly, Format: db})
	}
	d.Patches = []plug.Patch{
		{Name: "Default"},
		{Name: "Air", Params: map[uint32]float64{AmountID + 3: 35, StyleID + 3: Tape, DriveID + 3: 10}},
		{Name: "Presence", Params: map[uint32]float64{AmountID + 2: 30, StyleID + 2: Tube, DriveID + 2: 9}},
		{Name: "Warm low end", Params: map[uint32]float64{AmountID: 30, StyleID: Tube, DriveID: 9,
			AmountID + 1: 15, StyleID + 1: Warm, DriveID + 1: 6}},
		{Name: "Tape glue", Params: map[uint32]float64{AmountID: 20, AmountID + 1: 20, AmountID + 2: 20, AmountID + 3: 20,
			DriveID: 6, DriveID + 1: 6, DriveID + 2: 6, DriveID + 3: 6}},
		{Name: "Crisp top", Params: map[uint32]float64{AmountID + 2: 15, StyleID + 2: Crisp, DriveID + 2: 6,
			AmountID + 3: 30, StyleID + 3: Crisp, DriveID + 3: 9}},
		{Name: "Excite", Params: map[uint32]float64{AmountID + 2: 120, StyleID + 2: Tube, DriveID + 2: 18,
			AmountID + 3: 150, StyleID + 3: Crisp, DriveID + 3: 24}},
	}
	return d
}()

func init() { plug.Register(Descriptor) }

// param returns the parameter id; every ID above is one.
func param(id uint32) plug.Param {
	p, _ := Descriptor.Param(id)
	return p
}

// plugin is one enhancer.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: it keeps nothing but its parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("enhancer: the plugin keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the bands in a chip, and the panel,
// which serves in full too.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel:
		return newPanel(h)
	default:
		return nil
	}
}

// glideMS is how long an amount, a style, a drive, the mix or the
// output, or a crossover, takes to glide where it is set; xoverEvery how many frames
// apart a gliding crossover's filters are worked out anew; dcHz where
// what a band adds is cut under, so a leaning curve adds no offset; and
// addedMS how quickly a band's added level follows it, addedFloor the
// least it shows. matchCycles is over how many cycles of the lowest
// pitch a band passes, matchLowest for the low band, a band's peak is
// found, before a frame and after it, to match the driven band's level
// to the band's by, or over matchShortest seconds at least, for a high
// band's frames, few to a cycle, catch its peaks unevenly over less: the
// enhancer looks as far ahead as that is for the lowest of all. matchMS
// is how quickly the match glides to it.
const (
	glideMS    = 20
	xoverEvery = 32
	dcHz       = 10
	addedMS    = 30
	addedFloor = -90

	matchCycles   = 0.5
	matchLowest   = 30
	matchMS       = 1
	matchShortest = 0.001
)

// lead is how many frames ahead of the frame worked a band is driven:
// as many as the filter back down needs; harmRing how many points of
// what a band adds are kept, at twice the rate, from downAhead before
// the frame worked to lead after.
const (
	lead     = (downAhead + 1) / 2
	harmRing = 128
)

// processor splits each channel into four bands, drives each band
// through its curve, brings it back to the band's level, and adds to the
// channel what the curves add, each band's as far as its amount, all as
// far as the mix.
type processor struct {
	rate float64
	// The crossovers as set, and as they glide, in hertz.
	xTo, x [bands - 1]float64
	// Each band's amount as set and as it glides, from 0, as a share; its
	// style, as how much of each curve it takes; and its drive, in
	// decibels: each as set and as it glides.
	aTo, a   [bands]float64
	stTo, st [bands][styles]float64
	drTo, dr [bands]float64
	// Each band's drive, as a gain, and the drive it was worked out for.
	drive, drAt [bands]float64
	// The mix, from 0 to 1, and the output, as a gain, as set and as
	// they glide.
	mixTo, mix   float64
	gainTo, gain float64
	glide        float64
	fresh        bool
	split        [2]xover.Splitter
	// Each channel's band's last point, as it was and driven, and its cut
	// of offset.
	last [2][bands]float64
	from [2][bands]point
	dc   [2][bands]dcCut
	dcR  float64
	// Each channel as it waits to be worked, ahead frames, and each
	// band's match; how many frames each band's peak is found over, and
	// how quickly its match glides; and the frames come in so far.
	lines     [2]line
	harm      [2][bands][2 * harmRing]float64
	ahead     int
	g         [2][bands]float64
	matchSpan [bands]int
	span      int
	matchK    float64
	t         int64
	// Each band's added level, following it, and its trace.
	env    [bands]float64
	envK   float64
	traces [bands]plug.Trace
	tick   int
}

// dcCut cuts the offset out of what a band adds: a first-order high
// pass at dcHz.
type dcCut struct{ x1, y1 float64 }

func (c *dcCut) run(x, r float64) float64 {
	y := x - c.x1 + r*c.y1
	c.x1, c.y1 = x, y
	return y
}

// line is a channel as it waits to be worked, in rings a frame longer
// than the enhancer looks ahead: the channel, each of its bands, kept
// twice over for between, and each band's peak over the frames to each,
// as they came in.
//
// A driven band is matched to the band's own level by its peak: the
// lesser of its peak over half a cycle of the lowest pitch the band
// passes before a frame, and over as long after it, so the match falls
// as soon as the band falls quiet and rises as the band grows, never
// before it. The driven band is divided by how much of a tone at that
// peak its curve carries, so what the curve then adds is its harmonics
// alone: none of the band's own pitch is taken away with it.
type line struct {
	dry          []float64
	part, peaked [bands][]float64
	peaks        [bands]peaks
}

// peaks finds the peak of the last frames come in, of any number, as
// they come in: the frames that may yet be the peak, from the oldest,
// each louder than the ones after it, in a ring.
type peaks struct {
	at       []int64
	v        []float64
	first, n int
}

// put takes v, come in at t, and returns the peak of it and of the
// frames before it over span frames.
func (q *peaks) put(t int64, v float64, span int) float64 {
	size := len(q.v)
	wrap := func(i int) int {
		if i >= size {
			return i - size
		}
		return i
	}
	for q.n > 0 && q.v[wrap(q.first+q.n-1)] <= v {
		q.n--
	}
	end := wrap(q.first + q.n)
	q.at[end], q.v[end] = t, v
	q.n++
	for q.at[q.first] <= t-int64(span) {
		q.first, q.n = wrap(q.first+1), q.n-1
	}
	return q.v[q.first]
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.glide = 1 - math.Exp(-1/(glideMS/1000*s.Rate))
	p.dcR = 1 - 2*math.Pi*dcHz/s.Rate
	p.envK = 1 - math.Exp(-1/(addedMS/1000*s.Rate))
	p.matchK = 1 - math.Exp(-1/(matchMS/1000*s.Rate))
	p.span = max(int(math.Ceil(matchCycles/matchLowest*s.Rate)), 1)
	p.ahead = p.span + lead + upAhead
	for c := range p.lines {
		l := &p.lines[c]
		l.dry = make([]float64, p.ahead+1)
		for k := range bands {
			l.part[k], l.peaked[k] = make([]float64, 2*(p.ahead+1)), make([]float64, p.ahead+1)
			l.peaks[k] = peaks{at: make([]int64, p.ahead+1), v: make([]float64, p.ahead+1)}
		}
	}
	for _, prm := range Descriptor.Params {
		if prm.Flags&plug.ReadOnly == 0 {
			p.take(plug.Change{ID: prm.ID, Value: prm.Default})
		}
	}
	p.Reset()
}

// take takes a change to a parameter.
func (p *processor) take(c plug.Change) {
	prm, ok := Descriptor.Param(c.ID)
	if !ok || prm.Flags&plug.ReadOnly != 0 {
		return
	}
	v := prm.Clamp(c.Value)
	switch {
	case c.ID == MixID:
		p.mixTo = v / 100
	case c.ID == OutputID:
		p.gainTo = math.Pow(10, v/20)
	case c.ID >= DriveID:
		p.drTo[c.ID-DriveID] = v
	case c.ID >= StyleID:
		b, s := c.ID-StyleID, int(math.Round(v))
		for k := range styles {
			p.stTo[b][k] = float64(onOff(k == s))
		}
	case c.ID >= AmountID:
		p.aTo[c.ID-AmountID] = v / 100
	default:
		p.xTo[c.ID-CrossoverID] = v
	}
	if p.fresh {
		p.a, p.st, p.dr, p.mix, p.gain = p.aTo, p.stTo, p.drTo, p.mixTo, p.gainTo
		p.setCrossovers(p.xTo)
	}
}

func onOff(b bool) int {
	if b {
		return 1
	}
	return 0
}

// setCrossovers sets the crossovers at x, kept in order, and works their
// filters out, and how quickly each band's match follows: over cycles of
// the lowest pitch it passes.
func (p *processor) setCrossovers(x [bands - 1]float64) {
	p.x = xover.Order(x)
	for c := range p.split {
		p.split[c].Set(p.x, p.rate)
	}
	low := float64(matchLowest)
	for k := range bands {
		if k > 0 {
			low = p.x[k-1]
		}
		p.matchSpan[k] = min(int(max(matchCycles/low, matchShortest)*p.rate), p.span)
	}
}

// Process implements [plug.Processor].
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 || p.rate == 0 {
		return
	}
	chans := min(len(b.In), len(b.Out), 2)
	next := 0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.step()
		for k := range bands {
			if p.dr[k] != p.drAt[k] {
				p.drAt[k], p.drive[k] = p.dr[k], math.Exp(p.dr[k]/20*math.Ln10)
			}
		}
		// The frame come in now is put by; the one ahead frames before
		// it, which has waited for the frames after it, is worked.
		t := p.t
		p.t++
		now, ring := int(t%int64(p.ahead+1)), int64(p.ahead+1)
		var peak [bands]float64
		for c := range chans {
			l := &p.lines[c]
			l.dry[now] = float64(b.In[c][i])
			for k, v := range p.split[c].Split(l.dry[now]) {
				l.part[k][now], l.part[k][now+p.ahead+1] = v, v
				l.peaked[k][now] = l.peaks[k].put(t, math.Abs(v), p.matchSpan[k])
			}
			// Each band is driven lead frames ahead of the frame worked,
			// at twice the rate, so the filter back down has the points
			// after it that it needs.
			if ahd := t - int64(p.ahead) + lead; ahd >= 0 {
				for k := range bands {
					p.driveAt(c, k, ahd)
				}
			}
			was := t - int64(p.ahead)
			if was < 0 {
				b.Out[c][i] = 0
				continue
			}
			added := 0.0
			for k := range bands {
				if p.a[k] < 1e-6 {
					continue
				}
				h := p.dc[c][k].run(down(p.harm[c][k][:], 2*was), p.dcR) * p.a[k]
				added += h
				peak[k] = max(peak[k], math.Abs(h))
			}
			at := int(was % ring)
			b.Out[c][i] = float32((l.dry[at] + added*p.mix) * p.gain)
		}
		// Each band's added level, a quick envelope of its peaks.
		f := b.Position.Frame + int64(i)
		for k := range bands {
			p.env[k] += (peak[k]*p.mix - p.env[k]) * p.envK
			p.traces[k].Put(f, -float32(max(20*math.Log10(p.env[k]+1e-12), addedFloor)))
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
}

// driveAt drives channel c's band k at frame n, at twice the rate: the
// frame and the point half-way to the next, each driven along
// its way from the point before, matched to the band's level there, and
// less the way straight, put in the band's harmonics as what its curve
// adds there. A band at none adds nothing.
func (p *processor) driveAt(c, k int, n int64) {
	drive := p.drive[k]
	l, hs := &p.lines[c], &p.harm[c][k]
	ring := int64(len(l.dry))
	v, mid := l.part[k][n%ring], between(l.part[k], ring, n)
	at := (2 * n) % harmRing
	if p.a[k] < 1e-6 {
		p.last[c][k], p.g[c][k] = mid, 1
		p.from[c][k] = point{u: mid * drive}
		hs[at], hs[at+1], hs[at+harmRing], hs[at+1+harmRing] = 0, 0, 0, 0
		return
	}
	lvl := min(l.peaked[k][n%ring], l.peaked[k][(n+int64(p.matchSpan[k]))%ring])
	p.g[c][k] += (carried(&p.st[k], drive*lvl) - p.g[c][k]) * p.matchK
	g, last := p.g[c][k], p.last[c][k]
	from := &p.from[c][k]
	hs[at] = p.driven(k, from, v*drive)/drive/g - (last+v)/2
	hs[at+1] = p.driven(k, from, mid*drive)/drive/g - (v+mid)/2
	hs[at+harmRing], hs[at+1+harmRing] = hs[at], hs[at+1]
	p.last[c][k] = mid
}

// point is where a band, driven, last was, and each curve's integral
// there, where it has been worked out.
type point struct {
	u  float64
	i  [styles]float64
	ok [styles]bool
}

// driven returns band k driven through its curve from where it last
// was, at, on to u: the curve's mean between them, so it does not alias
// as a curve read a step at a time would, and so half a step late, as
// the line between them is. It moves at on to u, each integral at u
// kept for the next step.
func (p *processor) driven(k int, at *point, u float64) float64 {
	y := 0.0
	for s, w := range p.st[k] {
		if w < 1e-4 {
			at.ok[s] = false
			continue
		}
		if !at.ok[s] {
			at.i[s], at.ok[s] = integral(s, at.u), true
		}
		i := integral(s, u)
		if d := u - at.u; math.Abs(d) > 1e-6 {
			y += w * (i - at.i[s]) / d
		} else {
			y += w * curve(s, (at.u+u)/2)
		}
		at.i[s] = i
	}
	at.u = u
	return y
}

// step glides the amounts, the styles, the drives, the mix, the output
// and the crossovers a frame's way to where they are set, the
// crossovers' filters worked out anew every xoverEvery frames as they
// move.
func (p *processor) step() {
	for k := range bands {
		p.a[k] += (p.aTo[k] - p.a[k]) * p.glide
		p.dr[k] += (p.drTo[k] - p.dr[k]) * p.glide
		for s := range styles {
			p.st[k][s] += (p.stTo[k][s] - p.st[k][s]) * p.glide
		}
	}
	p.mix += (p.mixTo - p.mix) * p.glide
	p.gain += (p.gainTo - p.gain) * p.glide
	p.tick++
	if p.tick < xoverEvery {
		return
	}
	p.tick = 0
	moved := false
	x := p.x
	for i := range x {
		if math.Abs(x[i]-p.xTo[i]) > 0.01 {
			// In octaves, so a low crossover glides as a high one does.
			x[i] *= math.Pow(p.xTo[i]/x[i], 1-math.Pow(1-p.glide, xoverEvery))
			moved = true
		} else if x[i] != p.xTo[i] {
			x[i], moved = p.xTo[i], true
		}
	}
	if moved {
		p.setCrossovers(x)
	}
}

// Latency implements [plug.Processor]: the enhancer is as late as it
// looks ahead.
func (p *processor) Latency() int { return p.ahead }

// Tail implements [plug.Processor]: nothing rings on.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	for c := range p.split {
		p.split[c].Reset()
		p.last[c] = [bands]float64{}
		p.from[c] = [bands]point{}
		p.dc[c] = [bands]dcCut{}
		l := &p.lines[c]
		p.harm[c] = [bands][2 * harmRing]float64{}
		clear(l.dry)
		for k := range bands {
			p.g[c][k] = 1
			clear(l.part[k])
			clear(l.peaked[k])
			l.peaks[k].first, l.peaks[k].n = 0, 0
		}
	}
	p.env = [bands]float64{}
	for k := range bands {
		p.drAt[k] = math.NaN()
	}
	p.t = 0
	p.fresh = true
	p.a, p.st, p.dr, p.mix, p.gain = p.aTo, p.stTo, p.drTo, p.mixTo, p.gainTo
	p.setCrossovers(p.xTo)
}

var (
	_ plug.Tracer   = (*processor)(nil)
	_ plug.Follower = (*panel)(nil)
	_ plug.Follower = (*chip)(nil)
)

// Trace implements [plug.Tracer]: each band's added level, by frame, as
// the negative of its decibels, as a trace keeps the least and the views
// want the most.
func (p *processor) Trace(id uint32) *plug.Trace {
	if id >= AddedID && id < AddedID+bands {
		return &p.traces[id-AddedID]
	}
	return nil
}
