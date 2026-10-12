package audio

import (
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/anim"
)

// block is how many frames the mixer steps volumes and pans by. Within
// a block each moves in a straight line, so a fade has no steps to hear.
const block = 128

// historyFrames is how much of what it played the mixer keeps for an
// [Analyzer]: a power of two, more than the speakers hold back, as far
// ahead as they grow to work at 192 kHz.
const historyFrames = 1 << 18

// declick is how long a pause, a resume and a stop with no fade of
// their own take to fade, so the sound never clicks off.
const declick = 6 * time.Millisecond

// fadeOver is a fade over d, or declick where d is shorter. It is made
// where a fade is asked for, as a motion made as the mixer mixes would
// be made anew each time.
func fadeOver(d time.Duration) anim.Motion {
	return anim.Tween{Duration: max(d, declick), Ease: anim.Linear}
}

// A Mixer sums the sounds playing into one stream. Its methods, and
// its voices', are safe to call from any goroutine, but Mix and Read,
// which one goroutine at a time calls.
//
// Mixing runs in real time: it takes no lock and makes nothing, and
// never waits for the goroutines that play and change sounds. They
// hand it each change, and it takes the changes up at the next block
// it mixes, a few milliseconds later at most; what they ask of a voice,
// as its volume or position, answers from what they set and what the
// mixer last made. A voice's source and insert run where it mixes, so
// they must not wait either: a sound read from a file plays through a
// [Stream].
type Mixer struct {
	// incoming are the voices played, waiting for the next block to
	// take them up; the mixer's alone are voices, those mixing, and buf.
	incoming atomic.Pointer[Voice]
	voices   *Voice
	buf      []float32
	// count counts the voices played and not yet dropped.
	count atomic.Int64
	// played counts the frames mixed so far, and heardTop the most
	// heard has returned.
	played   atomic.Int64
	heardTop atomic.Int64
	// latency returns how many of the frames mixed the speakers have
	// still to play.
	latency atomic.Pointer[func() int64]
	// history holds the last frames mixed, as mono, at played modulo
	// its length; the rings the rest, made as they are first wanted.
	history []atomic.Uint32
	rings   atomic.Pointer[mixRings]
	ringsMu sync.Mutex
	// gain is the limiter's gain, 1 while the mix fits.
	gain float32
	// fade lowers the whole mix, from 1 to silence, for FadeOut and
	// FadeIn, as fadeAsk asks: the mixer's alone.
	fade    *anim.Float
	fadeAsk atomic.Pointer[motionAsk]
	// sounding says a voice sounded in the last block mixed; sounds
	// counts the plays and resumes asked, and soundsSeen those the last
	// block took up, so a sound asked for since counts as sounding.
	sounding   atomic.Bool
	sounds     atomic.Int64
	soundsSeen atomic.Int64
	// out is Read's buffer; Read runs on one goroutine at a time.
	out []float32
	// rate is how many frames a second the mixer gives, and every
	// source it plays.
	rate atomic.Int64
	// news are the voices with news to tell, of an end or a turn: the
	// teller, a goroutine running while voices play, tells it, as
	// mixing touches no channel.
	news    atomic.Pointer[Voice]
	telling atomic.Bool
}

// mixRings are the history kept besides the mono mix, once wanted:
// dry, the block as the voices' inserts found it, and dryHistory the
// frames of it mixed so far, as history holds what was heard; stereo
// the frames mixed, both channels, and dryStereo the same as the
// inserts found them. Each is nil until wanted. A ring once made is
// kept: a new mixRings carries it on.
type mixRings struct {
	dry               []float32
	dryHistory        []atomic.Uint32
	stereo, dryStereo []atomic.Uint32
}

// NewMixer returns a mixer playing nothing, at [SampleRate].
func NewMixer() *Mixer {
	m := &Mixer{history: make([]atomic.Uint32, historyFrames), buf: make([]float32, 2*block), gain: 1,
		fade: anim.NewFloat(1), out: make([]float32, 2*1024)}
	m.rate.Store(SampleRate)
	m.rings.Store(&mixRings{})
	return m
}

// Rate returns how many frames a second the mixer gives. Every source
// it plays gives frames at this rate.
func (m *Mixer) Rate() int { return int(m.rate.Load()) }

// SetRate sets how many frames a second the mixer gives, as package
// audio/speaker does to match the device it plays on. Sounds made for
// the rate before play at the wrong speed from then: an application
// that changes the rate plays its sounds again, made for the new one.
func (m *Mixer) SetRate(hz int) {
	if hz > 0 {
		m.rate.Store(int64(hz))
	}
}

// Frames returns how many frames last d, at the mixer's rate.
func (m *Mixer) Frames(d time.Duration) int64 { return FramesAt(d, m.Rate()) }

// Duration returns how long n frames last, at the mixer's rate.
func (m *Mixer) Duration(n int64) time.Duration { return DurationAt(n, m.Rate()) }

// Options says how a sound starts playing.
type Options struct {
	// Volume scales the sound, 1 playing it as recorded. Zero, the
	// default, plays it at 1 too: a sound starts silent with FadeIn.
	Volume float32
	// Pan places the sound from -1, the left speaker only, to 1, the
	// right only.
	Pan float32
	// FadeIn rises the sound from silence over this long.
	FadeIn time.Duration
	// Loop starts the sound over as it ends. The source must be a
	// [Seeker].
	Loop bool
	// Paused readies the sound without playing it, until
	// [Voice.Resume].
	Paused bool
	// Insert, where set, changes the sound as it plays, after its
	// volume and pan, as an equalizer does; see [EQ.Insert]. The
	// mixer keeps the sound as it was before the inserts too, for an
	// [Analyzer] to compare.
	Insert Insert
}

// rated is an insert of this package's that works at the mixer's rate,
// as an EQ does, told it before each block.
type rated interface{ setRate(hz int) }

// An Insert changes a voice's sound as it plays, in place, a block of
// interleaved stereo frames at a time. It runs where the mixer mixes,
// in real time, so it must be quick, must not wait or make anything,
// and must not call the mixer.
type Insert interface {
	Process(frames []float32)
}

// Play starts src playing and returns its voice. It plays from the
// next block the mixer mixes.
func (m *Mixer) Play(src Source, o Options) *Voice {
	vol := o.Volume
	if vol == 0 {
		vol = 1
	}
	v := &Voice{
		m:      m,
		loop:   o.Loop,
		done:   make(chan struct{}),
		turned: make(chan struct{}, 4),
		src:    &voiceSource{src: src},
		vol:    anim.NewFloat(vol),
		pan:    anim.NewFloat(o.Pan),
		gate:   anim.NewFloat(1),
		paused: o.Paused,
		insert: o.Insert,
	}
	v.rated, _ = o.Insert.(rated)
	v.volTo.Store(floatBits(vol))
	v.pausedTo.Store(o.Paused)
	v.length.Store(lengthOf(src))
	_, seeks := src.(Seeker)
	v.canSeek.Store(seeks)
	if o.FadeIn > 0 {
		v.gate.Jump(0)
		v.gate.Animate(1, anim.Tween{Duration: o.FadeIn, Ease: anim.Linear})
	}
	if o.Insert != nil {
		m.wantRings(true, false, false)
	}
	if !o.Paused {
		m.sounds.Add(1)
	}
	v.marks[0].set(mark{mixed: m.played.Load()})
	v.nextMark = 1
	v.markAt.Store(1)
	v.nmarks.Store(1)
	m.count.Add(1)
	for {
		head := m.incoming.Load()
		v.nextIn = head
		if m.incoming.CompareAndSwap(head, v) {
			break
		}
	}
	m.startTelling()
	return v
}

// lengthOf returns src's length in frames, or -1.
func lengthOf(src Source) int64 {
	if s, ok := src.(Seeker); ok {
		return s.Len()
	}
	return -1
}

// wantRings makes the rings asked for that are not made yet: the dry
// mix and its history, the stereo history, and the dry stereo history.
func (m *Mixer) wantRings(dry, stereo, dryStereo bool) {
	have := m.rings.Load()
	if (!dry || have.dryHistory != nil) && (!stereo || have.stereo != nil) && (!dryStereo || have.dryStereo != nil) {
		return
	}
	m.ringsMu.Lock()
	defer m.ringsMu.Unlock()
	r := *m.rings.Load()
	if dry && r.dryHistory == nil {
		r.dry = make([]float32, 2*block)
		r.dryHistory = make([]atomic.Uint32, historyFrames)
		// Until now no insert changed anything: the sound heard is the
		// sound before.
		for i := range r.dryHistory {
			r.dryHistory[i].Store(m.history[i].Load())
		}
	}
	if stereo && r.stereo == nil {
		r.stereo = make([]atomic.Uint32, 2*historyFrames)
	}
	if dryStereo && r.dryStereo == nil {
		r.dryStereo = make([]atomic.Uint32, 2*historyFrames)
	}
	m.rings.Store(&r)
}

// Playing returns how many voices are playing or paused.
func (m *Mixer) Playing() int { return int(m.count.Load()) }

// Sounding reports whether a voice sounds: one playing, or fading as
// it pauses or stops, or played or resumed since the mixer last mixed.
// Voices paused, and none, are silence.
func (m *Mixer) Sounding() bool {
	return m.sounding.Load() || m.sounds.Load() != m.soundsSeen.Load()
}

// FadeOut fades the whole mix to silence over fade, and holds it
// silent until [Mixer.FadeIn]; the voices play on under it. Package
// audio/speaker fades out so as the speakers suspend.
func (m *Mixer) FadeOut(fade time.Duration) {
	m.fadeAsk.Store(&motionAsk{to: 0, m: fadeOver(fade)})
}

// FadeIn fades the whole mix back in over fade, after
// [Mixer.FadeOut].
func (m *Mixer) FadeIn(fade time.Duration) {
	m.fadeAsk.Store(&motionAsk{to: 1, m: fadeOver(fade)})
}

// SetLatency tells the mixer how many of the frames it mixed are still
// to be heard, as the speakers hold them back. Package audio/speaker
// sets it; a voice's position and an [Analyzer] count with it. f is
// called from any goroutine, and must not wait.
func (m *Mixer) SetLatency(f func() int64) { m.latency.Store(&f) }

// Mixed returns how many frames the mixer has mixed.
func (m *Mixer) Mixed() int64 { return m.played.Load() }

// heard returns how many frames have been heard. It never goes back:
// played and the frames the speakers hold are read a moment apart, and
// where the mixer hands the speakers a block between the two, the
// speakers hold it before played counts it, and the frames heard would
// seem to go back a block.
func (m *Mixer) heard() int64 {
	h := m.played.Load()
	if f := m.latency.Load(); f != nil {
		h = max(0, h-(*f)())
	}
	for {
		top := m.heardTop.Load()
		if h <= top {
			return top
		}
		if m.heardTop.CompareAndSwap(top, h) {
			return h
		}
	}
}

// Mix fills dst, len(dst)/2 frames, with the sounds playing, and
// silence where none plays. One goroutine at a time mixes.
func (m *Mixer) Mix(dst []float32) {
	for len(dst) >= 2 {
		n := min(len(dst)/2, block)
		m.mixBlock(dst[:2*n])
		dst = dst[2*n:]
	}
}

// Read implements [io.Reader] for a device that takes the mix as
// little-endian float32 samples, as package audio/speaker does. It
// always fills whole frames.
func (m *Mixer) Read(p []byte) (int, error) {
	frames := len(p) / 8
	if frames == 0 {
		return 0, io.ErrShortBuffer
	}
	done := 0
	for done < frames {
		n := min(frames-done, len(m.out)/2)
		m.Mix(m.out[:2*n])
		putFloats(p[8*done:], m.out[:2*n])
		done += n
	}
	return 8 * frames, nil
}

// takeIncoming takes up the voices played since the last block, in
// the order they were played.
func (m *Mixer) takeIncoming() {
	in := m.incoming.Swap(nil)
	// The stack holds the last played first: turned about.
	var first *Voice
	for in != nil {
		next := in.nextIn
		in.nextIn = first
		first, in = in, next
	}
	tail := &m.voices
	for *tail != nil {
		tail = &(*tail).next
	}
	for first != nil {
		next := first.nextIn
		first.nextIn, first.next = nil, nil
		*tail = first
		tail = &first.next
		first = next
	}
}

// mixBlock mixes one block, at most block frames, into dst.
func (m *Mixer) mixBlock(dst []float32) {
	seen := m.sounds.Load()
	m.takeIncoming()
	if a := m.fadeAsk.Swap(nil); a != nil {
		set(m.fade, a.to, a.m)
	}
	clear(dst)
	frames := len(dst) / 2
	rate := int(m.rate.Load())
	played := m.played.Load()
	dt := DurationAt(int64(frames), rate)
	rings := m.rings.Load()
	var dry []float32
	if rings.dry != nil {
		dry = rings.dry[:2*frames]
		clear(dry)
	}
	sounding := false
	for link := &m.voices; *link != nil; {
		v := *link
		if v.mix(dst, dry, m.buf[:2*frames], dt, played, rate) {
			sounding = sounding || !v.paused
			link = &v.next
			continue
		}
		*link = v.next
		v.next = nil
		m.count.Add(-1)
		m.tell(v, true)
	}
	m.sounding.Store(sounding)
	m.soundsSeen.Store(seen)
	// The limiter: where the block would pass full scale, the gain
	// drops to fit it from the block's start, as its peak is known
	// before any of it is heard, and glides back up after.
	peak := float32(0)
	for _, v := range dst {
		peak = max(peak, v, -v)
	}
	g0 := m.gain
	g1 := min(1, g0+(1-g0)*float32(frames)/(float32(rate)*limiterRelease))
	if peak*g1 > limiterCeiling {
		g1 = limiterCeiling / peak
		g0 = min(g0, g1)
	}
	m.gain = g1
	// The fade, as the voices' own, moves in a straight line within
	// the block.
	f0 := m.fade.Value()
	m.fade.Step(dt)
	f1 := m.fade.Value()
	for i := range frames {
		t := float32(i+1) / float32(frames)
		g := (g0 + (g1-g0)*t) * (f0 + (f1-f0)*t)
		l, r := clip(dst[2*i]*g), clip(dst[2*i+1]*g)
		dst[2*i], dst[2*i+1] = l, r
		at := (played + int64(i)) & (historyFrames - 1)
		m.history[at].Store(floatBits((l + r) / 2))
		if dry != nil {
			rings.dryHistory[at].Store(floatBits((dry[2*i] + dry[2*i+1]) / 2))
		}
		if rings.stereo != nil {
			rings.stereo[2*at].Store(floatBits(l))
			rings.stereo[2*at+1].Store(floatBits(r))
		}
		if rings.dryStereo != nil {
			if dry != nil {
				rings.dryStereo[2*at].Store(floatBits(dry[2*i]))
				rings.dryStereo[2*at+1].Store(floatBits(dry[2*i+1]))
			} else {
				rings.dryStereo[2*at].Store(floatBits(l))
				rings.dryStereo[2*at+1].Store(floatBits(r))
			}
		}
	}
	m.played.Store(played + int64(frames))
}

// The limiter's ceiling, full scale, which a block's peak is brought
// under, and how many seconds it takes to come most of the way back to
// full gain.
const (
	limiterCeiling = 1
	limiterRelease = 0.08
)

// clip keeps a sample within -1 to 1, where rounding might take it a
// hair past.
func clip(s float32) float32 { return max(-1, min(1, s)) }

// A Voice is one sound playing in a [Mixer].
type Voice struct {
	m    *Mixer
	loop bool
	done chan struct{}
	// turned hears each turn to the source Then gave.
	turned chan struct{}

	// The mixer's alone: next links the voices mixing, nextIn those
	// played and not yet taken up.
	next, nextIn *Voice
	// src is the source playing, and then the one to play once it
	// ends, without a gap.
	src, then *voiceSource
	// vol and pan are the voice's own; gate fades it for a pause, a
	// resume, a stop and FadeIn.
	vol, pan, gate *anim.Float
	paused         bool
	insert         Insert
	rated          rated
	// pausing and stopping say the gate is closing for a pause or a
	// stop.
	pausing, stopping bool
	// at is the frame of the source the voice plays next; gen counts
	// the turns, and turns holds where in the block being mixed each
	// fell.
	at     int64
	gen    int
	turns  [4]int
	nturns int
	ended  bool
	// nextMark is where the next mark goes.
	nextMark int

	// The mailboxes, which the mixer takes from at the start of each
	// block: the last asked of each takes the place of one not taken.
	volAsk, panAsk atomic.Pointer[motionAsk]
	gateAsk        atomic.Pointer[gateAsk]
	stopAsk        atomic.Pointer[gateAsk]
	seekAsk        atomic.Pointer[seekAsk]
	thenAsk        atomic.Pointer[voiceSource]

	// What the voice answers with, from any goroutine: what it was
	// last asked, and what the mixer last made. seeks and seeked count
	// the seeks asked and made, and seekTo is the last asked, so a
	// voice asked where it is answers from there until the mixer moves
	// it.
	volTo    atomic.Uint32
	pausedTo atomic.Bool
	stopped  atomic.Bool
	length   atomic.Int64
	canSeek  atomic.Bool
	err      atomic.Pointer[error]
	seeks    atomic.Int64
	seeked   atomic.Int64
	seekTo   atomic.Int64
	// marks say which frames of the source the last blocks mixed, so
	// Position can tell which the speakers are playing; markSeq is odd
	// while the mixer writes them, so a reader that sees it change
	// reads them again. gens is the turns, for the readers.
	marks   [markCount]atomicMark
	nmarks  atomic.Int64
	markAt  atomic.Int64
	markSeq atomic.Uint64
	gens    atomic.Int64
	// told counts the turns told, by the teller; inNews says the voice
	// waits in the mixer's news, and nextNews links the news.
	told     int64
	inNews   atomic.Bool
	nextNews atomic.Pointer[Voice]
	ending   atomic.Bool
}

// voiceSource holds a source, so a turn to it is the swap of a pointer.
type voiceSource struct{ src Source }

// motionAsk is a volume or pan asked, and how to move there.
type motionAsk struct {
	to float32
	m  anim.Motion
}

// gateAsk is a pause, a resume or a stop asked, which fades as fade
// moves.
type gateAsk struct {
	pause, resume, stop bool
	fade                anim.Motion
}

// seekAsk is a seek asked, to frame f of the source: the seq'th.
type seekAsk struct{ f, seq int64 }

// markCount is how many blocks a voice remembers: more than the
// speakers hold back.
const markCount = 512

// A mark records that the block mixed at mixer frame mixed took n
// frames of the source from frame at.
type mark struct {
	mixed, at int64
	n         int
	// gen is the turn the block's frames belong to.
	gen int
}

// atomicMark is a mark kept for readers on other goroutines.
type atomicMark struct{ mixed, at, n, gen atomic.Int64 }

func (a *atomicMark) set(k mark) {
	a.mixed.Store(k.mixed)
	a.at.Store(k.at)
	a.n.Store(int64(k.n))
	a.gen.Store(int64(k.gen))
}

func (a *atomicMark) get() mark {
	return mark{mixed: a.mixed.Load(), at: a.at.Load(), n: int(a.n.Load()), gen: int(a.gen.Load())}
}

// addMark records a block, for the readers.
func (v *Voice) addMark(k mark) {
	v.markSeq.Add(1)
	v.marks[v.nextMark].set(k)
	v.nextMark = (v.nextMark + 1) % markCount
	v.markAt.Store(int64(v.nextMark))
	v.nmarks.Store(min(v.nmarks.Load()+1, markCount))
	v.markSeq.Add(1)
}

// resetMarks forgets the blocks, for a seek, and records k alone.
func (v *Voice) resetMarks(k mark) {
	v.markSeq.Add(1)
	v.marks[0].set(k)
	v.nextMark = 1
	v.markAt.Store(1)
	v.nmarks.Store(1)
	v.markSeq.Add(1)
}

// take takes up what was asked of the voice since the last block.
func (v *Voice) take(played int64, rate int) {
	if a := v.volAsk.Swap(nil); a != nil {
		set(v.vol, a.to, a.m)
	}
	if a := v.panAsk.Swap(nil); a != nil {
		set(v.pan, a.to, a.m)
	}
	if a := v.thenAsk.Swap(nil); a != nil {
		if a.src == nil {
			a = nil
		}
		v.then = a
	}
	if a := v.stopAsk.Swap(nil); a != nil {
		if v.paused {
			v.ended = true
		} else {
			v.stopping = true
			v.gate.Animate(0, a.fade)
		}
	}
	if a := v.gateAsk.Swap(nil); a != nil && !v.stopping {
		switch {
		case a.pause && !v.paused:
			v.pausing = true
			v.gate.Animate(0, a.fade)
		case a.resume:
			v.paused, v.pausing = false, false
			v.gate.Animate(1, a.fade)
		}
	}
	if a := v.seekAsk.Swap(nil); a != nil {
		if s, ok := v.src.src.(Seeker); ok {
			if err := s.SeekFrame(a.f); err == nil {
				v.at = a.f
				v.ended = false
				// Until the speakers play what is mixed next, the voice
				// is heard where it moved to.
				v.resetMarks(mark{mixed: played, at: a.f, gen: v.gen})
			}
		}
		v.seeked.Store(a.seq)
	}
	if v.rated != nil {
		v.rated.setRate(rate)
	}
}

// mix adds the voice's next frames into dst, using buf, and reports
// whether it plays on; dry, where not nil, takes them as they were
// before the voice's insert.
func (v *Voice) mix(dst, dry, buf []float32, dt time.Duration, played int64, rate int) bool {
	v.take(played, rate)
	if v.ended {
		return false
	}
	frames := len(dst) / 2
	if v.paused {
		return true
	}
	at, gen := v.at, v.gen
	l0, r0 := v.gains()
	v.vol.Step(dt)
	v.pan.Step(dt)
	v.gate.Step(dt)
	l1, r1 := v.gains()
	v.nturns = 0
	n := v.fill(buf)
	switch {
	case v.nturns > 0:
		// It turned to the source after: the block's frames run from
		// the first source's, then from the start of each turned to.
		v.addMark(mark{mixed: played, at: at, n: v.turns[0], gen: gen})
		for i := range v.nturns {
			end := n
			if i+1 < v.nturns {
				end = v.turns[i+1]
			}
			v.addMark(mark{mixed: played + int64(v.turns[i]), n: end - v.turns[i], gen: gen + i + 1})
		}
		v.gens.Store(int64(v.gen))
		v.length.Store(lengthOf(v.src.src))
		_, seeks := v.src.src.(Seeker)
		v.canSeek.Store(seeks)
		v.m.tell(v, false)
	case v.at >= at:
		v.addMark(mark{mixed: played, at: at, n: n, gen: gen})
	default:
		// It looped: the block's frames run from the start.
		v.addMark(mark{mixed: played, at: v.at - int64(n), n: n, gen: gen})
	}
	for i := range n {
		t := float32(i) / float32(frames)
		buf[2*i] *= l0 + (l1-l0)*t
		buf[2*i+1] *= r0 + (r1-r0)*t
	}
	if dry != nil {
		for i := range 2 * n {
			dry[i] += buf[i]
		}
	}
	if v.insert != nil && n > 0 {
		v.insert.Process(buf[:2*n])
	}
	for i := range 2 * n {
		dst[i] += buf[i]
	}
	if v.gate.Value() == 0 && !v.gate.Active() {
		switch {
		case v.stopping:
			return false
		case v.pausing:
			v.paused, v.pausing = true, false
		}
	}
	return !v.ended
}

// gains returns the left and right gains the voice is at.
func (v *Voice) gains() (l, r float32) {
	g := max(0, v.vol.Value()) * max(0, min(1, v.gate.Value()))
	p := max(-1, min(1, v.pan.Value()))
	return g * min(1, 1-p), g * min(1, 1+p)
}

// fill reads the voice's next frames into buf, starting over as a
// looping voice ends, and returns how many it read. It marks the voice
// ended once its source is. A source with no frames yet, as a stream
// not decoded so far, leaves the rest of the block silent.
func (v *Voice) fill(buf []float32) int {
	want := len(buf) / 2
	n := 0
	restarted := false
	for n < want {
		got, err := v.src.src.Read(buf[2*n : 2*want])
		n += got
		v.at += int64(got)
		if got > 0 {
			restarted = false
		}
		if err == nil {
			if got == 0 {
				break
			}
			continue
		}
		if errors.Is(err, io.EOF) && v.then != nil {
			// The next source carries on at once, sample after sample.
			v.src, v.then = v.then, nil
			v.at = 0
			v.gen++
			if v.nturns < len(v.turns) {
				v.turns[v.nturns] = n
				v.nturns++
			}
			continue
		}
		if errors.Is(err, io.EOF) && v.loop && !restarted {
			// A looping source with nothing in it ends, rather than
			// start over without end.
			if s, ok := v.src.src.(Seeker); ok && s.SeekFrame(0) == nil {
				v.at, restarted = 0, true
				continue
			}
		}
		if !errors.Is(err, io.EOF) {
			// A copy of its own, as taking err's address would have
			// every read make it anew.
			e := err
			v.err.Store(&e)
		}
		v.ended = true
		break
	}
	clear(buf[2*n:])
	return n
}

// tell puts v among the voices with news, for the teller: of its end,
// or of a turn.
func (m *Mixer) tell(v *Voice, ended bool) {
	if ended {
		v.ending.Store(true)
	}
	if !v.inNews.CompareAndSwap(false, true) {
		return
	}
	for {
		head := m.news.Load()
		v.nextNews.Store(head)
		if m.news.CompareAndSwap(head, v) {
			return
		}
	}
}

// The teller's pace: how often it looks for news while there is some,
// and while there has been none for a while, and how long it waits
// with no voice playing before it stops.
const (
	tellEvery = 2 * time.Millisecond
	tellQuiet = 25 * time.Millisecond
	tellIdle  = time.Second
)

// startTelling starts the teller, where it is not running.
func (m *Mixer) startTelling() {
	if m.telling.CompareAndSwap(false, true) {
		go m.tellNews()
	}
}

// tellNews tells the voices' news, as the mixer leaves it: it closes
// the Done of each that ended, and sends on the Turned of each that
// turned. It runs while voices play, and a moment after.
func (m *Mixer) tellNews() {
	t := time.NewTimer(tellEvery)
	defer t.Stop()
	idle, quiet := time.Duration(0), time.Duration(0)
	for range t.C {
		news := m.news.Swap(nil)
		every := tellEvery
		if news == nil {
			if quiet += every; quiet > 100*time.Millisecond {
				every = tellQuiet
			}
		} else {
			quiet = 0
		}
		t.Reset(every)
		for v := news; v != nil; {
			next := v.nextNews.Load()
			v.inNews.Store(false)
			for g := v.gens.Load(); v.told < g; v.told++ {
				select {
				case v.turned <- struct{}{}:
				default:
				}
			}
			if v.ending.Load() {
				select {
				case <-v.done:
				default:
					close(v.done)
				}
			}
			v = next
		}
		if news != nil || m.count.Load() > 0 {
			idle = 0
			continue
		}
		if idle += every; idle < tellIdle {
			continue
		}
		// Stopping: unless a voice came meanwhile, which starts it again.
		m.telling.Store(false)
		if m.count.Load() == 0 && m.news.Load() == nil || !m.telling.CompareAndSwap(false, true) {
			return
		}
		idle = 0
	}
}

// Done is closed when the voice has ended or stopped, a moment after
// the mixer drops it.
func (v *Voice) Done() <-chan struct{} { return v.done }

// Err returns the error that ended the voice early, if any.
func (v *Voice) Err() error {
	if e := v.err.Load(); e != nil {
		return *e
	}
	return nil
}

// Stop fades the voice out over fade and ends it. A fade of zero still
// takes a few milliseconds, so the sound never clicks off.
func (v *Voice) Stop(fade time.Duration) {
	v.stopped.Store(true)
	v.stopAsk.Store(&gateAsk{stop: true, fade: fadeOver(fade)})
}

// Pause fades the voice out over fade and holds it where it is. A fade
// of zero still takes a few milliseconds, so the sound never clicks
// off.
func (v *Voice) Pause(fade time.Duration) {
	if v.stopped.Load() {
		return
	}
	v.pausedTo.Store(true)
	v.gateAsk.Store(&gateAsk{pause: true, fade: fadeOver(fade)})
}

// Resume plays a paused voice on from where it paused, fading in over
// fade. A fade of zero still takes a few milliseconds, so the sound
// never clicks on.
func (v *Voice) Resume(fade time.Duration) {
	if v.stopped.Load() {
		return
	}
	v.pausedTo.Store(false)
	v.m.sounds.Add(1)
	v.gateAsk.Store(&gateAsk{resume: true, fade: fadeOver(fade)})
}

// Paused reports whether the voice is paused, or pausing.
func (v *Voice) Paused() bool { return v.pausedTo.Load() }

// SetVolume moves the voice's volume to vol with motion m, or jumps
// there with a nil m.
func (v *Voice) SetVolume(vol float32, m anim.Motion) {
	v.volTo.Store(floatBits(vol))
	v.volAsk.Store(&motionAsk{to: vol, m: m})
}

// Volume returns the volume the voice is heading for.
func (v *Voice) Volume() float32 { return floatFrom(v.volTo.Load()) }

// SetPan moves the voice's pan to p, from -1, left, to 1, right, with
// motion m, or jumps there with a nil m.
func (v *Voice) SetPan(p float32, m anim.Motion) {
	v.panAsk.Store(&motionAsk{to: p, m: m})
}

func set(a *anim.Float, to float32, m anim.Motion) {
	if m == nil {
		a.Jump(to)
		return
	}
	a.Animate(to, m)
}

// ErrNotSeekable is returned by [Voice.Seek] for a source that is not a
// [Seeker].
var ErrNotSeekable = errors.New("audio: the source cannot seek")

// Seek moves the voice to d from the start of its sound, from the next
// block the mixer mixes; Position answers from there at once. A source
// that fails the seek plays on where it was.
func (v *Voice) Seek(d time.Duration) error {
	if !v.canSeek.Load() {
		return ErrNotSeekable
	}
	f := max(0, FramesAt(d, v.m.Rate()))
	if l := v.length.Load(); l >= 0 {
		f = min(f, l)
	}
	v.seekTo.Store(f)
	v.seekAsk.Store(&seekAsk{f: f, seq: v.seeks.Add(1)})
	return nil
}

// Then has the voice play src once what it plays now ends, without a
// gap: the sound runs on into src sample after sample, at the voice's
// volume and pan, through its insert, and Position, Len and Seek speak
// of src from then. A later Then replaces a source still waiting, and
// nil takes it away. Turned hears each turn.
func (v *Voice) Then(src Source) {
	v.thenAsk.Store(&voiceSource{src: src})
}

// Turned returns a channel that receives as the voice turns to the
// source Then gave it, a moment after the mixer reaches it: the
// speakers play it a moment later, as long as they hold back.
func (v *Voice) Turned() <-chan struct{} { return v.turned }

// Position returns how far into its sound the voice is, as heard: the
// frames the speakers hold back are not counted yet. Just after a turn
// to the source Then gave, while the speakers still play the source
// before, it is zero.
func (v *Voice) Position() time.Duration {
	rate := v.m.Rate()
	if v.seeks.Load() != v.seeked.Load() {
		// A seek the mixer has yet to make: heard from there.
		return DurationAt(v.seekTo.Load(), rate)
	}
	for {
		// The marks read again where the mixer wrote them meanwhile.
		seq := v.markSeq.Load()
		if seq&1 == 1 {
			continue
		}
		at := v.positionAt(v.m.heard(), rate)
		if v.markSeq.Load() == seq {
			return at
		}
	}
}

// positionAt reads the marks for the position of the frame heard.
func (v *Voice) positionAt(heard int64, rate int) time.Duration {
	gen := v.gens.Load()
	n, next := v.nmarks.Load(), v.markAt.Load()
	// The latest block mixed at or before the frame being heard says
	// where in the source that is.
	var k mark
	for i := int64(1); i <= n; i++ {
		k = v.marks[(next-i+markCount)%markCount].get()
		if k.mixed <= heard {
			if int64(k.gen) < gen {
				return 0
			}
			return DurationAt(k.at+min(heard-k.mixed, int64(k.n)), rate)
		}
	}
	return DurationAt(k.at, rate)
}

// Len returns the length of the voice's sound, or -1 when it is
// unknown.
func (v *Voice) Len() time.Duration {
	l := v.length.Load()
	if l < 0 {
		return -1
	}
	return DurationAt(l, v.m.Rate())
}

func floatBits(f float32) uint32 { return math.Float32bits(f) }

func floatFrom(b uint32) float32 { return math.Float32frombits(b) }
