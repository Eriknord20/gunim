package beat

import (
	"math"
	"time"
)

// DefaultHear is how much sound a [Live] finds the tempo in, when it is
// told no other length.
const DefaultHear = 6 * time.Second

// A Live follows the beat of sound as it comes in, such as a band on
// stage or a DJ's mix through a microphone: fed frames as they arrive,
// it finds the tempo in the last few seconds, keeps a beat running on
// it, and says where the next beat falls, so a light can fire on the
// beat rather than after it.
//
// It finds the tempo as [Scan] does, by how well the onsets line up
// with themselves, every half second, over the last Hear of sound. It
// moves to a new tempo smoothly, and only once two looks agree on it,
// so one odd bar does not throw it, and it leaves the half and the
// double of the tempo it has alone. Its beat runs on its own between
// the onsets: the strongest onset near each beat pulls the beat a
// quarter of the way to it, so it locks to the music and stays locked
// through a fill, and where two looks running find the beat well out
// of place it is moved at once. In silence it keeps the tempo and the
// beat running, as a drummer counting through a break does.
//
// It is not safe for use from more than one goroutine at a time.
type Live struct {
	// MinBPM and MaxBPM bound the tempo looked for, [DefaultMinBPM] and
	// [DefaultMaxBPM] when they are zero. Hear is how much sound the
	// tempo is found in, [DefaultHear] when it is zero. Set them before
	// the first Write.
	MinBPM, MaxBPM float64
	Hear           time.Duration

	d *detector
	// hist holds the rise of the last columns, as a ring, written to
	// col: the columns counted since the start.
	hist []float32
	col  int64
	// recent holds the last columns' rise, to measure each onset
	// against.
	recent [64]float32
	// per is the beat's length in columns, 0 before a tempo is found,
	// and want the tempo a look found that is not taken yet, as the
	// next look may say it again.
	per, want float64
	clarity   float64
	// next is the column the next beat falls on, and count the beats
	// gone by.
	next  float64
	count int64
	// before and peak are the onset strength of the last two columns,
	// to find each onset's peak a column late.
	before, peak float32
	// quiet counts the columns since an onset was heard.
	quiet int
	// loud is the strongest rise heard lately, falling slowly, which
	// an onset has to reach a part of: in a break, hiss stands out
	// against hiss, and must not pull the beat.
	loud float32
	// shift is how far a look last found the beat out of place, which
	// the next look has to find again before the beat is moved.
	shift float64
	// aim is the beat, as a column, the strongest onset near it was
	// heard for, 0 for none, and best and bestAt that onset's strength
	// and column. Only the strongest pulls the beat, so a stray onset
	// near a beat does not.
	aim, bestAt float64
	best        float32
}

// NewLive returns a beat follower of sound at rate. A rate of zero or
// less is taken as [audio.SampleRate].
func NewLive(rate int) *Live {
	return &Live{d: newDetector(rate)}
}

func (l *Live) hear() time.Duration {
	if l.Hear > 0 {
		return l.Hear
	}
	return DefaultHear
}

func (l *Live) minBPM() float64 {
	if l.MinBPM > 0 {
		return l.MinBPM
	}
	return DefaultMinBPM
}

func (l *Live) maxBPM() float64 {
	if l.MaxBPM > l.minBPM() {
		return l.MaxBPM
	}
	return max(DefaultMaxBPM, 2*l.minBPM())
}

// The live follower's measures: how often it looks for the tempo, how
// strong an onset is to pull the beat, how far, in beats, from a beat
// an onset may be and still pull it, how far it pulls, and how much of
// that it moves the tempo by.
const (
	lookEvery = 0.5
	strong    = 3.0
	reach     = 0.12
	pull      = 0.25
	nudge     = 0.03
	// An onset reaches a part of the loudest lately, which halves in
	// loudFor seconds.
	loudPart = 0.25
	loudFor  = 3.0
	// sure is how clearly a first look has to hear a tempo to take it
	// alone, without a second to agree.
	sure = 0.4
	// phaseBeats is how many beats back the beat is placed from.
	phaseBeats = 4
)

// Write takes frames, interleaved stereo, and returns the beats that
// fell in them, as times from the first frame written. A beat is
// reported as the sound reaches it: one predicted ahead is read from
// [Live.Next].
func (l *Live) Write(frames []float32) []time.Duration {
	if l.hist == nil {
		l.hist = make([]float32, int(l.hear().Seconds()*l.d.fps())+1)
	}
	var beats []time.Duration
	l.d.write(frames, func(rise, _ float32) {
		if b, ok := l.column(rise); ok {
			beats = append(beats, b)
		}
	})
	return beats
}

// column takes a column's rise, and reports a beat that falls on it.
func (l *Live) column(rise float32) (time.Duration, bool) {
	fps := l.d.fps()
	l.hist[l.col%int64(len(l.hist))] = rise
	l.recent[l.col%int64(len(l.recent))] = rise
	l.col++
	every := int64(lookEvery * fps)
	if l.col%max(every, 1) == 0 && l.col >= int64(len(l.hist)/2) {
		l.look()
	}
	// The onset's strength: how far the rise stands above the last
	// columns', against how much they vary.
	var mean, sq float64
	for _, v := range l.recent {
		mean += float64(v)
	}
	mean /= float64(len(l.recent))
	for _, v := range l.recent {
		sq += (float64(v) - mean) * (float64(v) - mean)
	}
	sd := math.Sqrt(sq / float64(len(l.recent)))
	o := float32(0)
	if sd > 1e-6 {
		o = float32((float64(rise) - mean) / sd)
	}
	l.loud = max(l.loud*float32(math.Exp2(-1/(loudFor*fps))), rise)
	if rise < loudPart*l.loud {
		o = 0
	}
	// The column before is an onset's peak where it stands over both
	// its neighbours.
	at := float64(l.col - 2)
	if l.peak > strong && l.peak >= l.before && l.peak >= o {
		l.quiet = 0
		l.onset(at, l.peak)
	} else {
		l.quiet++
	}
	l.before, l.peak = l.peak, o
	if l.aim > 0 && float64(l.col-1) > l.aim+reach*l.per {
		l.pullTo()
	}
	if l.per <= 0 || float64(l.col-1) < l.next {
		return 0, false
	}
	// The beat is due: reported, and the next one a beat on.
	at = l.next
	l.next += l.per
	l.count++
	return l.timeOf(at), true
}

// onset takes an onset at column at, of strength o, where it falls
// near a beat, the last or the next, as the strongest near it yet.
func (l *Live) onset(at float64, o float32) {
	if l.per <= 0 {
		return
	}
	var beat float64
	switch last := l.next - l.per; {
	case math.Abs(at-l.next) <= reach*l.per:
		beat = l.next
	case math.Abs(at-last) <= reach*l.per:
		beat = last
	default:
		return
	}
	if beat != l.aim {
		l.pullTo()
		l.aim = beat
	}
	if o > l.best {
		l.best, l.bestAt = o, at
	}
}

// pullTo pulls the beat towards the strongest onset near the beat
// aimed at, now that no other can come.
func (l *Live) pullTo() {
	if l.best > 0 && l.per > 0 {
		// The beat aimed at is the next or the last; either way the
		// next moves.
		off := l.bestAt - l.aim
		l.next += pull * off
		// A beat found late time after time is a tempo a little slow:
		// the tempo is nudged too, so it keeps up between looks.
		l.per += nudge * off
	}
	l.aim, l.best, l.bestAt = 0, 0, 0
}

// look finds the tempo in the sound heard last, and moves towards it,
// and moves the beat where it has drifted from the music.
func (l *Live) look() {
	n := min(l.col, int64(len(l.hist)))
	f := make([]float32, n)
	for i := range f {
		f[i] = l.hist[(l.col-n+int64(i))%int64(len(l.hist))]
	}
	fps := l.d.fps()
	strength := level(f, int(fps/2))
	per, clarity, ok := period(strength, fps, l.minBPM(), l.maxBPM())
	if !ok || clarity < 0.1 {
		return
	}
	switch {
	case l.per <= 0:
		// The first tempo, heard clearly or twice: the beat set where
		// the onsets heard last fall most, a beat apart.
		if clarity >= sure || l.want > 0 && near(per, l.want) {
			l.per, l.clarity, l.want = per, clarity, 0
			l.next = float64(l.col-1) + phase(strength, per)
			l.shift = 0
			return
		}
		l.want = per
	case math.Abs(per-l.per) < 0.06*l.per:
		// Near the tempo held: moved towards, smoothly.
		l.per += 0.3 * (per - l.per)
		l.clarity = clarity
		l.want = 0
	case near(per, 2*l.per) || near(per, l.per/2) || near(per, 1.5*l.per) || near(per, l.per/1.5):
		// Its half, its double: the same music, read another way.
	case l.want > 0 && near(per, l.want):
		// Another tempo, heard twice: taken, and the beat placed
		// afresh.
		l.per, l.clarity, l.want = per, clarity, 0
		l.next = float64(l.col-1) + phase(strength, per)
		l.shift, l.aim, l.best = 0, 0, 0
		return
	default:
		l.want = per
	}
	l.place(strength)
}

// place moves the beat where the onsets heard last say it falls, once
// two looks running agree it is out of place by more than an onset can
// pull it. Not in a break: there is nothing to place it by.
func (l *Live) place(strength []float32) {
	if l.per <= 0 || float64(l.quiet) > 2*l.per {
		l.shift = 0
		return
	}
	at := float64(l.col-1) + phase(strength, l.per)
	// How far the beat is out, the nearer way round.
	off := math.Mod(at-l.next, l.per)
	switch {
	case off > l.per/2:
		off -= l.per
	case off < -l.per/2:
		off += l.per
	}
	if math.Abs(off) <= reach*l.per {
		l.shift = 0
		return
	}
	if l.shift == 0 || math.Abs(off-l.shift) > reach*l.per {
		l.shift = off
		return
	}
	l.next += off
	l.aim, l.best = 0, 0
	if l.next <= float64(l.col-1) {
		// Moved back past now: the beat it would have called is gone.
		l.next += l.per
	}
	l.shift = 0
}

// near reports whether a and b are within 4% of each other.
func near(a, b float64) bool { return math.Abs(a-b) < 0.04*b }

// phase is how many columns after the last of strength the next beat
// falls: where the onsets of the last few beats line up most, a beat
// apart.
func phase(strength []float32, per float64) float64 {
	n := len(strength)
	stop := float64(n-1) - phaseBeats*per
	best, at := -1.0, 0.0
	steps := int(math.Ceil(per))
	for s := range steps {
		var sum float64
		for k := float64(n - 1 - s); k >= max(stop, 0); k -= per {
			sum += float64(strength[int(math.Round(k))])
		}
		if sum > best {
			best, at = sum, float64(s)
		}
	}
	// A beat fell at steps back from the end; the next a beat after it.
	ahead := per - at
	for ahead <= 0 {
		ahead += per
	}
	return ahead
}

// timeOf is when column c falls, from the first frame written, as
// [Scan] counts it.
func (l *Live) timeOf(c float64) time.Duration {
	return time.Duration(max(c, 0) / l.d.fps() * float64(time.Second))
}

// BPM is the tempo followed, 0 before one is found.
func (l *Live) BPM() float64 {
	if l.per <= 0 {
		return 0
	}
	return 60 * l.d.fps() / l.per
}

// Confidence is how clearly the sound beats at the tempo followed, as
// [Result.Confidence] says, as last looked.
func (l *Live) Confidence() float64 { return l.clarity }

// Next is when the next beat falls, from the first frame written, and
// false before a tempo is found.
func (l *Live) Next() (time.Duration, bool) {
	if l.per <= 0 {
		return 0, false
	}
	return l.timeOf(l.next), true
}

// Beats is how many beats have fallen, and how far the sound written
// has come towards the next, from 0 to 1: the count a clock runs on.
func (l *Live) Beats() float64 {
	if l.per <= 0 {
		return 0
	}
	since := (float64(l.col) - (l.next - l.per)) / l.per
	return float64(l.count) + min(max(since, 0), 1)
}

// Quiet is how long since an onset was heard: the beat goes on through
// it, kept by the tempo alone.
func (l *Live) Quiet() time.Duration {
	return time.Duration(float64(l.quiet) / l.d.fps() * float64(time.Second))
}
