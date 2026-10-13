// Package tempo holds a recording's musical time: where its bars and
// beats fall, how fast it is going, and how to turn one into the other.
//
// A [Map] is a list of tempo changes and a list of meter changes. Ask
// it for the time a musical position falls at, or the position a time
// falls on, and it answers through every change in between. A map with
// one tempo and one meter is a track that never wavers; a map with a
// change every few bars is a live recording that drifts, and nothing
// outside the map has to know which it is holding.
//
//	m := tempo.New(128, 4, 4)
//	m.Time(tempo.Quarters(32))   // where bar 9 falls
//	m.Pos(m.At(player.Position())) // the bar and beat being played
//
// Musical positions are [Ticks], [PPQ] to the quarter note, so they
// compare, sort and snap exactly: a cue written on a beat stays on that
// beat whatever the tempo around it later becomes. Times are
// [time.Duration], which is what a player reports.
//
// Tempo is in quarter notes a minute, as a sequencer writes it,
// whatever the meter calls a beat. A change either takes hold at once
// or ramps, sliding to the next change's tempo as time passes. A ramp
// is linear in time, the way a sequencer draws one, so the beats within
// it come out of a quadratic rather than being stepped through.
//
// Nothing here reads or plays sound. Package audio/beat finds the beats
// in a recording, and [Fit] turns those beats into a map.
package tempo

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// PPQ is the ticks in a quarter note. It divides by 2, 3, 4, 5, 6, 7
// and 8, so triplets, quintuplets and septuplets all land on whole
// ticks.
const PPQ = 3360

// DefaultBPM is the tempo a map with no tempo of its own reads as: a
// steady 120.
const DefaultBPM = 120.0

// DefaultBeats and DefaultUnit are the meter a map with no meter of
// its own reads as: four beats of a quarter note each, which is 4/4.
const (
	DefaultBeats = 4
	DefaultUnit  = 4
)

// MinBPM and MaxBPM bound a tempo, since a tempo of zero or less has no
// meaning and nothing is served by one too fast to hear.
const (
	MinBPM = 1.0
	MaxBPM = 1000.0
)

// Ticks is a musical position, or a length of musical time, in PPQ to
// the quarter note.
type Ticks int64

// Quarters returns the ticks in q quarter notes.
func Quarters(q float64) Ticks { return Ticks(math.Round(q * PPQ)) }

// A Tempo is a tempo change: from At onward the music runs at BPM
// quarter notes a minute.
//
// Ramp slides the tempo from BPM to the next change's, evenly in time,
// reaching it as that change falls. A ramp with no change after it, or
// one reaching the same tempo, is steady.
type Tempo struct {
	At   Ticks
	BPM  float64
	Ramp bool `json:",omitempty"`
}

// A Meter is a meter change: from At onward a bar holds Beats beats,
// each a note of one Unit — 4 for a quarter, 8 for an eighth, so 6 and
// 8 is six eighths to the bar.
//
// A change starts a new bar where it falls, cutting short the bar it
// lands in, since that is the only reading that keeps bar lines and
// beat lines agreeing.
type Meter struct {
	At    Ticks
	Beats int
	Unit  int
}

// Length is a bar of this meter in ticks.
func (m Meter) Length() Ticks { return Ticks(m.Beats) * m.Beat() }

// Beat is one beat of this meter in ticks. A meter with no unit, as the
// zero Meter has, counts in quarter notes.
func (m Meter) Beat() Ticks {
	unit := m.Unit
	if unit <= 0 {
		unit = DefaultUnit
	}
	return Ticks(4*PPQ) / Ticks(unit)
}

// A Pos is a musical position as a musician says it: the bar, the beat
// within the bar, and how far past that beat, from zero to one.
//
// Bars and beats count from one, so the very start is bar 1, beat 1.
type Pos struct {
	Bar, Beat int
	Frac      float64
}

// A Line is one beat on a ruler: where it falls and what it is. A line
// with Pos.Beat of 1 starts a bar.
type Line struct {
	At  Ticks
	Pos Pos
}

// MaxLines is the most lines [Map.Lines] returns, so a range asked for
// in error cannot eat the memory of the machine.
const MaxLines = 1 << 20

// A Map is a recording's musical time: its tempo changes, its meter
// changes, and where its first tick falls.
//
// The zero Map is a steady [DefaultBPM] in [DefaultBeats] and
// [DefaultUnit], starting at the start of the recording. A Map is for
// one goroutine at a time while it is being changed; once built it may
// be read from several, so a player and a drawing both hold the same
// one.
type Map struct { //nolint:recvcheck // MarshalJSON takes a value, so a Map held by value is written too.
	// start is where tick 0 falls in the recording, which is the first
	// downbeat of a track with silence or a count-in before it.
	start time.Duration
	// tempos and meters are sorted by At, with one of each at 0.
	tempos []Tempo
	meters []Meter
	// secs[i] is the seconds from tick 0 to tempos[i].At, and bars[i]
	// the bar number at meters[i].At. Both are worked out whenever the
	// map changes, so a reading walks no further than a search.
	secs []float64
	bars []int
}

// New returns a map at a steady tempo in one meter.
func New(bpm float64, beats, unit int) *Map {
	m := &Map{}
	m.SetTempos([]Tempo{{BPM: bpm}})
	m.SetMeters([]Meter{{Beats: beats, Unit: unit}})
	return m
}

// Start is where tick 0 falls in the recording, and SetStart moves it:
// the lead-in before the first downbeat of a track that does not begin
// on one.
func (m *Map) Start() time.Duration { return m.start }

// SetStart moves the whole map, keeping every musical position where it
// is and every time it falls at the later for it.
func (m *Map) SetStart(d time.Duration) { m.start = d }

// Tempos are the tempo changes the map holds, in order, the first at
// tick 0. The slice returned is a copy.
func (m *Map) Tempos() []Tempo {
	return slices.Clone(m.ready().tempos)
}

// Meters are the meter changes the map holds, in order, the first at
// tick 0. The slice returned is a copy.
func (m *Map) Meters() []Meter {
	return slices.Clone(m.ready().meters)
}

// SetTempos replaces every tempo change at once, which is what reading
// a map in, or fitting one to a recording, does.
//
// The changes are sorted and tidied: a tempo out of bounds is brought
// back into them, two changes at one position leave the later of the
// two, a change before the start moves to it, and a map left without a
// change at the start gains a steady [DefaultBPM] there.
func (m *Map) SetTempos(ts []Tempo) {
	m.tempos = slices.Clone(ts)
	m.tidy()
}

// SetMeters replaces every meter change at once, tidied as SetTempos
// tidies tempo changes.
func (m *Map) SetMeters(ms []Meter) {
	m.meters = slices.Clone(ms)
	m.tidy()
}

// SetTempo adds a tempo change, or replaces the one already at that
// position.
func (m *Map) SetTempo(t Tempo) {
	m.prep()
	m.tempos = append(m.tempos, t)
	m.tidy()
}

// SetMeter adds a meter change, or replaces the one already at that
// position.
func (m *Map) SetMeter(x Meter) {
	m.prep()
	m.meters = append(m.meters, x)
	m.tidy()
}

// RemoveTempo takes out the tempo change at a position, and reports
// whether there was one. The change at the start stays: something has
// to say what the music does before the first change the user made.
func (m *Map) RemoveTempo(at Ticks) bool {
	m.prep()
	if at <= 0 {
		return false
	}
	before := len(m.tempos)
	m.tempos = slices.DeleteFunc(m.tempos, func(t Tempo) bool { return t.At == at })
	m.tidy()
	return len(m.tempos) != before
}

// RemoveMeter takes out the meter change at a position, and reports
// whether there was one. As with a tempo, the change at the start
// stays.
func (m *Map) RemoveMeter(at Ticks) bool {
	m.prep()
	if at <= 0 {
		return false
	}
	before := len(m.meters)
	m.meters = slices.DeleteFunc(m.meters, func(x Meter) bool { return x.At == at })
	m.tidy()
	return len(m.meters) != before
}

// Clone returns a copy to change without disturbing a map being read.
func (m *Map) Clone() *Map {
	r := m.ready()
	return &Map{start: r.start, tempos: slices.Clone(r.tempos), meters: slices.Clone(r.meters),
		secs: slices.Clone(r.secs), bars: slices.Clone(r.bars)}
}

// tidied reports whether the map has been tidied since it last changed.
// Only the zero Map has not: everything that changes a map tidies it.
func (m *Map) tidied() bool {
	return len(m.tempos) > 0 && len(m.meters) > 0 && len(m.secs) == len(m.tempos)
}

// prep fills a map that has never been tidied, so the zero Map reads as
// the default tempo and meter. It is for the methods that change the
// map; a reading uses ready, which never writes.
func (m *Map) prep() {
	if !m.tidied() {
		m.tidy()
	}
}

// ready is the map tidied, for a reading. A map that has not been is
// tidied into a copy, so several goroutines may read a zero Map at once
// without writing to it.
func (m *Map) ready() *Map {
	if m.tidied() {
		return m
	}
	r := &Map{start: m.start, tempos: slices.Clone(m.tempos), meters: slices.Clone(m.meters)}
	r.tidy()
	return r
}

// tidy sorts the changes, brings them within bounds, drops the earlier
// of two at one position, makes sure each list starts at tick 0, and
// works out the seconds and bar numbers the readings search through.
func (m *Map) tidy() {
	for i := range m.tempos {
		t := &m.tempos[i]
		t.At = max(t.At, 0)
		if !(t.BPM >= MinBPM) { // also catches a BPM that is not a number
			t.BPM = MinBPM
		}
		t.BPM = min(t.BPM, MaxBPM)
	}
	for i := range m.meters {
		x := &m.meters[i]
		x.At = max(x.At, 0)
		x.Beats = min(max(x.Beats, 1), 64)
		if !slices.Contains([]int{1, 2, 4, 8, 16, 32}, x.Unit) {
			x.Unit = DefaultUnit
		}
	}
	// A later change at the same position wins, which is what setting
	// one over another means.
	slices.SortStableFunc(m.tempos, func(a, b Tempo) int { return cmp.Compare(a.At, b.At) })
	slices.SortStableFunc(m.meters, func(a, b Meter) int { return cmp.Compare(a.At, b.At) })
	m.tempos = lastOfEach(m.tempos, func(t Tempo) Ticks { return t.At })
	m.meters = lastOfEach(m.meters, func(x Meter) Ticks { return x.At })
	if len(m.tempos) == 0 || m.tempos[0].At != 0 {
		m.tempos = append([]Tempo{{BPM: DefaultBPM}}, m.tempos...)
	}
	if len(m.meters) == 0 || m.meters[0].At != 0 {
		m.meters = append([]Meter{{Beats: DefaultBeats, Unit: DefaultUnit}}, m.meters...)
	}

	m.secs = make([]float64, len(m.tempos))
	for i := 1; i < len(m.tempos); i++ {
		m.secs[i] = m.secs[i-1] + m.span(i-1, m.tempos[i].At)
	}
	m.bars = make([]int, len(m.meters))
	m.bars[0] = 1
	for i := 1; i < len(m.meters); i++ {
		// A meter change cuts its bar short, and that short bar still
		// counts, so the bar numbers never repeat.
		run := m.meters[i].At - m.meters[i-1].At
		m.bars[i] = m.bars[i-1] + int(ceilDiv(run, m.meters[i-1].Length()))
	}
}

// lastOfEach keeps the last of every run of entries sharing a key.
func lastOfEach[T any](in []T, key func(T) Ticks) []T {
	out := in[:0]
	for i, v := range in {
		if i+1 < len(in) && key(in[i+1]) == key(v) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// ceilDiv divides, rounding up, for positive divisors.
func ceilDiv(a, b Ticks) Ticks {
	if b <= 0 {
		return 0
	}
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// span is the seconds from tempo change i to the position to, which
// must not be past the change after it.
func (m *Map) span(i int, to Ticks) float64 {
	a := m.tempos[i]
	q := float64(to-a.At) / PPQ
	// Before the change, which only the first can be asked about, the
	// music goes back at its tempo: a ramp starts where it starts.
	if b, ramp := m.ramp(i); ramp && to > a.At {
		// The tempo rises evenly in time across the segment, so the
		// segment lasts its quarter notes over the mean of the two
		// tempos, and the beats within it come out of a quadratic:
		//	q = (a.BPM·s + k·s²/2) / 60, k the tempo's rise a second.
		// Solved for s in the form that stays exact as k nears zero.
		total := float64(b.At-a.At) / PPQ
		secs := 120 * total / (a.BPM + b.BPM)
		k := (b.BPM - a.BPM) / secs
		return 2 * 60 * q / (math.Sqrt(max(0, a.BPM*a.BPM+2*k*60*q)) + a.BPM)
	}
	return 60 * q / a.BPM
}

// ticksAfter is the position reached s seconds after tempo change i,
// which must not be more than the segment lasts.
func (m *Map) ticksAfter(i int, s float64) Ticks {
	return m.tempos[i].At + Quarters(m.quartersAfter(i, s))
}

// ramp reports whether tempo change i slides, and to which change. A
// ramp to the same tempo, or one with nothing after it, is steady.
func (m *Map) ramp(i int) (Tempo, bool) {
	if !m.tempos[i].Ramp || i+1 >= len(m.tempos) {
		return Tempo{}, false
	}
	b := m.tempos[i+1]
	if b.BPM == m.tempos[i].BPM || b.At <= m.tempos[i].At {
		return Tempo{}, false
	}
	return b, true
}

// tempoIndex is the tempo change in force at a position.
func (m *Map) tempoIndex(t Ticks) int {
	i, found := slices.BinarySearchFunc(m.tempos, t, func(x Tempo, t Ticks) int { return cmp.Compare(x.At, t) })
	if found {
		return i
	}
	return max(i-1, 0)
}

// meterIndex is the meter change in force at a position.
func (m *Map) meterIndex(t Ticks) int {
	i, found := slices.BinarySearchFunc(m.meters, t, func(x Meter, t Ticks) int { return cmp.Compare(x.At, t) })
	if found {
		return i
	}
	return max(i-1, 0)
}

// Time is where a musical position falls in the recording.
//
// A position before the start goes back at the first tempo, so it may
// come out negative: the map says where the music is, not where the
// file begins.
func (m *Map) Time(t Ticks) time.Duration {
	r := m.ready()
	i := r.tempoIndex(t)
	return r.start + time.Duration(math.Round((r.secs[i]+r.span(i, t))*float64(time.Second)))
}

// At is the musical position a time in the recording falls on.
func (m *Map) At(d time.Duration) Ticks {
	r := m.ready()
	s := float64(d-r.start) / float64(time.Second)
	i, found := slices.BinarySearchFunc(r.secs, s, cmp.Compare[float64])
	if !found {
		i = max(i-1, 0)
	}
	return r.ticksAfter(i, s-r.secs[i])
}

// Frames is where a musical position falls, in frames at rate, as a
// player counts them. A position before the start comes out negative.
func (m *Map) Frames(t Ticks, rate int) int64 {
	return framesAt(m.Time(t), int64(rate))
}

// framesAt is d in frames at rate, in whole seconds and the rest, so a
// time of days at a high rate does not overflow on the way.
func framesAt(d time.Duration, rate int64) int64 {
	whole, rest := int64(d/time.Second), int64(d%time.Second)
	return whole*rate + rest*rate/int64(time.Second)
}

// AtFrame is the musical position frame f at rate falls on.
func (m *Map) AtFrame(f int64, rate int) Ticks {
	r := int64(max(rate, 1))
	return m.At(time.Duration(f/r)*time.Second + time.Duration(f%r*int64(time.Second)/r))
}

// QuartersAtFrame is where frame f at rate falls, in quarter notes
// from tick 0, unrounded: exact where [Map.AtFrame] rounds to a tick. A
// host fills a plugin's beat with it, as plug's Position.Beat.
func (m *Map) QuartersAtFrame(f int64, rate int) float64 {
	r, i, s := m.segmentAtFrame(f, rate)
	return float64(r.tempos[i].At)/PPQ + r.quartersAfter(i, s)
}

// BPMAtFrame is the tempo at frame f at rate, part way up a ramp where
// it falls inside one: as plug's Position.Tempo and EndTempo take it.
func (m *Map) BPMAtFrame(f int64, rate int) float64 {
	r, i, s := m.segmentAtFrame(f, rate)
	a := r.tempos[i]
	b, ramp := r.ramp(i)
	if !ramp || s <= 0 {
		return a.BPM
	}
	secs := 120 * float64(b.At-a.At) / PPQ / (a.BPM + b.BPM)
	return a.BPM + (b.BPM-a.BPM)*min(s, secs)/secs
}

// BPMReachingFrame is the tempo frame f at rate is reached at: the
// tempo of the stretch of the map frame f-1 falls in, steady or ramping,
// run on to f. So a change falling between f-1 and f leaves the tempo
// before it. A host fills plug's Position.EndTempo with it at the frame
// after a block's last, so the block's tempo runs evenly from its start
// to its end.
func (m *Map) BPMReachingFrame(f int64, rate int) float64 {
	r, i, s := m.segmentAtFrame(f-1, rate)
	s += 1 / float64(max(rate, 1))
	a := r.tempos[i]
	b, ramp := r.ramp(i)
	if !ramp {
		return a.BPM
	}
	secs := 120 * float64(b.At-a.At) / PPQ / (a.BPM + b.BPM)
	return a.BPM + (b.BPM-a.BPM)*s/secs
}

// segmentAtFrame returns the map ready to read, the tempo change in
// force at frame f at rate, and the seconds from that change to f.
func (m *Map) segmentAtFrame(f int64, rate int) (r *Map, i int, s float64) {
	r = m.ready()
	rt := int64(max(rate, 1))
	s = float64(f/rt) + float64(f%rt)/float64(rt) - r.start.Seconds()
	i, found := slices.BinarySearchFunc(r.secs, s, cmp.Compare[float64])
	if !found {
		i = max(i-1, 0)
	}
	return r, i, s - r.secs[i]
}

// quartersAfter is the quarter notes s seconds after tempo change i,
// which must not be more than the segment lasts, unrounded.
func (m *Map) quartersAfter(i int, s float64) float64 {
	a := m.tempos[i]
	if b, ramp := m.ramp(i); ramp && s > 0 {
		total := float64(b.At-a.At) / PPQ
		secs := 120 * total / (a.BPM + b.BPM)
		k := (b.BPM - a.BPM) / secs
		return (a.BPM*s + k*s*s/2) / 60
	}
	return s * a.BPM / 60
}

// BPM is the tempo in force at a position, part way up a ramp where it
// falls inside one.
func (m *Map) BPM(t Ticks) float64 {
	r := m.ready()
	i := r.tempoIndex(t)
	a := r.tempos[i]
	b, ramp := r.ramp(i)
	if !ramp || t <= a.At {
		return a.BPM
	}
	// The tempo rises evenly in time, so read how far along in time the
	// position is, not how far along in beats.
	secs := 120 * float64(b.At-a.At) / PPQ / (a.BPM + b.BPM)
	s := min(r.span(i, t), secs)
	return a.BPM + (b.BPM-a.BPM)*s/secs
}

// Meter is the meter in force at a position.
func (m *Map) Meter(t Ticks) Meter {
	r := m.ready()
	return r.meters[r.meterIndex(t)]
}

// Pos is the bar and beat a position falls on.
func (m *Map) Pos(t Ticks) Pos {
	r := m.ready()
	i := r.meterIndex(t)
	x := r.meters[i]
	off := t - x.At
	bar := r.bars[i] + int(floorDiv(off, x.Length()))
	in := mod(off, x.Length())
	beat := int(in / x.Beat())
	return Pos{Bar: bar, Beat: beat + 1, Frac: float64(in-Ticks(beat)*x.Beat()) / float64(x.Beat())}
}

// Ticks is the position a bar and beat falls on. A beat past the end of
// its bar carries into the bars after it, so bar 1 beat 5 of 4/4 is
// bar 2 beat 1.
func (m *Map) Ticks(p Pos) Ticks {
	r := m.ready()
	// The meter in force is the last one starting at or before this
	// bar.
	i := 0
	for j, b := range r.bars {
		if b <= p.Bar {
			i = j
		}
	}
	x := r.meters[i]
	return x.At + Ticks(p.Bar-r.bars[i])*x.Length() +
		Ticks(p.Beat-1)*x.Beat() + Ticks(math.Round(p.Frac*float64(x.Beat())))
}

// Lines are the beats from one position to another, for a ruler to
// draw, the first at or after from and the last before to. At most
// [MaxLines] come back.
func (m *Map) Lines(from, to Ticks) []Line {
	r := m.ready()
	var out []Line
	for i := r.meterIndex(from); i < len(r.meters) && len(out) < MaxLines; i++ {
		x := r.meters[i]
		end := to
		if i+1 < len(r.meters) {
			end = min(end, r.meters[i+1].At)
		}
		beat := x.Beat()
		// Start on the first beat of this meter at or after from, which
		// is the meter's own start where from is behind it. The first
		// meter runs back before tick 0 too, for a lead-in.
		t := x.At
		if from > t || i == 0 {
			t -= floorDiv(t-from, beat) * beat
		}
		for ; t < end && len(out) < MaxLines; t += beat {
			out = append(out, Line{At: t, Pos: r.Pos(t)})
		}
		if end == to {
			break
		}
	}
	return out
}

// floorDiv divides, rounding down, for a positive divisor and a
// numerator that may be negative.
func floorDiv(a, b Ticks) Ticks {
	if b <= 0 {
		return 0
	}
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}

// mod is the remainder of a floored division, never negative.
func mod(a, b Ticks) Ticks {
	if b <= 0 {
		return 0
	}
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}
