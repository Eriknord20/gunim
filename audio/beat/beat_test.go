package beat

import (
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
)

// beats is where a click falls, beat by beat, at the tempo bpm gives
// for each.
func beats(lead time.Duration, n int, bpm func(k int) float64) []time.Duration {
	out := make([]time.Duration, n)
	at := lead
	for k := range n {
		out[k] = at
		at += time.Duration(60 / bpm(k) * float64(time.Second))
	}
	return out
}

// render makes a recording of a click at each beat, every fourth one a
// bass drum rather than a tick, over a quiet hiss. Four, because that
// is the bar these tests count in.
func render(rate int, at []time.Duration, total time.Duration) []float32 {
	const accent = 4
	n := int(total.Seconds() * float64(rate))
	out := make([]float32, 2*n)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range out {
		out[i] = float32(0.002 * (r.Float64()*2 - 1))
	}
	for i, t := range at {
		hz, decay, gain := 2000.0, 0.03, 0.5
		if accent > 0 && i%accent == 0 {
			hz, decay, gain = 60, 0.12, 0.9
		}
		start := int(t.Seconds() * float64(rate))
		for k := range int(decay * 4 * float64(rate)) {
			if start+k >= n || start+k < 0 {
				break
			}
			s := gain * math.Exp(-float64(k)/float64(rate)/decay) *
				math.Sin(2*math.Pi*hz*float64(k)/float64(rate))
			out[2*(start+k)] += float32(s)
			out[2*(start+k)+1] += float32(s)
		}
	}
	return out
}

// scan runs a recording through a scan in the chunks a player would
// read it in.
func scan(rate int, frames []float32) Result {
	s := NewScan(rate)
	for i := 0; i < len(frames); i += 2 * 4096 {
		s.Write(frames[i:min(i+2*4096, len(frames))])
	}
	return s.Done()
}

// stray is the furthest any beat found falls from the nearest beat
// played, and how many more were found than were played.
func stray(got, want []time.Duration) (worst time.Duration, over int) {
	for _, g := range got {
		near := time.Duration(math.MaxInt64)
		for _, w := range want {
			d := g - w
			if d < 0 {
				d = -d
			}
			near = min(near, d)
		}
		worst = max(worst, near)
	}
	return worst, len(got) - len(want)
}

// TestASteadyClickTrackIsFound, which is the easy case everything else
// is measured against.
func TestASteadyClickTrackIsFound(t *testing.T) {
	const rate = 48000
	want := beats(2*time.Second, 36, func(int) float64 { return 120 })
	r := scan(rate, render(rate, want, 21*time.Second))

	if math.Abs(r.BPM-120) > 0.5 {
		t.Errorf("the tempo came out at %v, want 120", r.BPM)
	}
	worst, over := stray(r.Beats, want)
	if worst > 10*time.Millisecond {
		t.Errorf("a beat was found %v from the nearest click", worst)
	}
	if over < -2 || over > 2 {
		t.Errorf("%d beats were found, want about %d", len(r.Beats), len(want))
	}
	if r.Confidence < 0.4 {
		t.Errorf("the scan is %v sure of a plain click track, want over 0.4", r.Confidence)
	}
}

// TestTheTempoIsNotHalvedOrDoubled: the lean towards 120 has to pick
// the beat people would clap, not the half bar or the eighth note.
func TestTheTempoIsNotHalvedOrDoubled(t *testing.T) {
	const rate = 48000
	for _, bpm := range []float64{72, 75, 90, 110, 128, 140, 150, 160, 175} {
		want := beats(time.Second, int(bpm/60*18), func(int) float64 { return bpm })
		r := scan(rate, render(rate, want, 21*time.Second))
		if math.Abs(r.BPM-bpm) > bpm*0.005 {
			t.Errorf("a track at %v came out at %v", bpm, r.BPM)
		}
	}
}

// TestADriftingTrackIsFollowed, as a band playing live does.
func TestADriftingTrackIsFollowed(t *testing.T) {
	const rate = 44100
	const n = 44
	want := beats(0, n, func(k int) float64 { return 120 + 12*float64(k)/n })
	r := scan(rate, render(rate, want, 22*time.Second))

	worst, over := stray(r.Beats, want)
	if worst > 30*time.Millisecond {
		t.Errorf("a beat was found %v from the nearest click, want the drift followed", worst)
	}
	if over < -3 || over > 3 {
		t.Errorf("%d beats were found, want about %d", len(r.Beats), n)
	}
	// Fitted, the map puts the bars where the music put them.
	m := r.Map(0)
	if bpm := m.BPM(0); math.Abs(bpm-120) > 4 {
		t.Errorf("the fitted map starts at %v, want about 120", bpm)
	}
}

// TestTheBarStartsOnTheBassDrum: nothing here hears a time signature,
// but it can tell which of four beats the kick lands on.
func TestTheBarStartsOnTheBassDrum(t *testing.T) {
	const rate = 48000
	want := beats(1500*time.Millisecond, 40, func(int) float64 { return 126 })
	r := scan(rate, render(rate, want, 22*time.Second))

	if r.PerBar != 4 {
		t.Fatalf("the scan counted in %d, want 4", r.PerBar)
	}
	if len(r.Beats) < 16 {
		t.Fatalf("only %d beats were found", len(r.Beats))
	}
	// Every beat the scan calls a downbeat should be one of the kicks,
	// which are the clicks four apart from the first.
	kick := func(at time.Duration) bool {
		for i := 0; i < len(want); i += 4 {
			if d := at - want[i]; d < 30*time.Millisecond && d > -30*time.Millisecond {
				return true
			}
		}
		return false
	}
	for i := r.Downbeat; i < len(r.Beats); i += r.PerBar {
		if !kick(r.Beats[i]) {
			t.Fatalf("beat %d at %v is called a downbeat, but no kick falls there", i, r.Beats[i])
		}
	}
}

// TestSoundWithNoBeatSaysSo, rather than inventing one and sounding
// sure about it.
func TestSoundWithNoBeatSaysSo(t *testing.T) {
	const rate = 48000
	r := rand.New(rand.NewPCG(3, 4))
	hiss := make([]float32, 2*rate*12)
	for i := range hiss {
		hiss[i] = float32(0.2 * (r.Float64()*2 - 1))
	}
	if got := scan(rate, hiss); got.Confidence > 0.3 {
		t.Errorf("the scan is %v sure of a beat in white noise", got.Confidence)
	}

	silence := make([]float32, 2*rate*12)
	got := scan(rate, silence)
	if len(got.Beats) != 0 {
		t.Errorf("%d beats were found in silence", len(got.Beats))
	}
	if got.Confidence != 0 {
		t.Errorf("the scan is %v sure of a beat in silence", got.Confidence)
	}
	// A recording too short to hold a beat is not guessed at either.
	if short := scan(rate, make([]float32, 2*rate/2)); len(short.Beats) != 0 {
		t.Errorf("%d beats were found in half a second", len(short.Beats))
	}
}

// TestTheBeatsBecomeATempoMap, which is what an application keeps.
func TestTheBeatsBecomeATempoMap(t *testing.T) {
	const rate = 48000
	want := beats(900*time.Millisecond, 40, func(int) float64 { return 128 })
	r := scan(rate, render(rate, want, 21*time.Second))
	m := r.Map(0)

	if math.Abs(m.BPM(0)-128) > 2 {
		t.Errorf("the map runs at %v, want about 128", m.BPM(0))
	}
	// The map's first tick is the first downbeat the scan found.
	if d := m.Start() - r.Beats[r.Downbeat]; d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("the map starts %v from the first downbeat", d)
	}
}

// reader gives a recording out as a sound to be read, as a decoded file
// is.
type reader struct {
	frames []float32
	at     int
}

func (r *reader) Read(dst []float32) (int, error) {
	n := copy(dst, r.frames[r.at:])
	r.at += n
	if n == 0 {
		return 0, io.EOF
	}
	return n / 2, nil
}

// TestFindReadsASoundToItsEnd, which is the short way in for an
// application that is not drawing the waveform as well.
func TestFindReadsASoundToItsEnd(t *testing.T) {
	const rate = 48000
	want := beats(time.Second, 28, func(int) float64 { return 100 })
	got, err := Find(&reader{frames: render(rate, want, 19*time.Second)}, rate)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.BPM-100) > 0.5 {
		t.Errorf("the tempo came out at %v, want 100", got.BPM)
	}
	if worst, over := stray(got.Beats, want); worst > 10*time.Millisecond || over < -2 || over > 2 {
		t.Errorf("%d beats found, the furthest %v from a click", len(got.Beats), worst)
	}
}

// TestASongLikePatternIsFound: something closer to music than a click
// track — kick, snare, hats on the eighths and a bass note held under
// them — comes back at the right tempo, on the beat, and with the bars
// starting where the kick does.
func TestASongLikePatternIsFound(t *testing.T) {
	const rate, bpm, bars = 48000, 124.0, 14
	out, want := song(rate, bpm, bars)
	got := scan(rate, out)
	if math.Abs(got.BPM-bpm) > 0.5 {
		t.Errorf("the tempo came out at %v, want %v", got.BPM, bpm)
	}
	if worst, over := stray(got.Beats, want); worst > 15*time.Millisecond || over < -2 || over > 2 {
		t.Errorf("%d beats found of %d, the furthest %v from where it was played",
			len(got.Beats), len(want), worst)
	}
	if got.Confidence < 0.5 {
		t.Errorf("the scan is only %v sure of a plain pattern", got.Confidence)
	}
	// The bars have to start on a kick: every beat called a downbeat
	// falls where one was played. The kick plays on one and three, and
	// the two sound the same, so either will do; see Result.Downbeat.
	for i := got.Downbeat; i < len(got.Beats); i += got.PerBar {
		played := -1
		for j, w := range want {
			if d := got.Beats[i] - w; d < 20*time.Millisecond && d > -20*time.Millisecond {
				played = j
			}
		}
		if played < 0 {
			t.Fatalf("beat %d at %v is called a downbeat, but nothing was played there", i, got.Beats[i])
		}
		if played%2 != 0 {
			t.Fatalf("beat %d at %v is called a downbeat, but beat %d of the bar was played there",
				i, got.Beats[i], played%4+1)
		}
	}
}

// song makes bars of a song-like pattern at bpm: a kick on one and
// three, a snare on two and four, hats on and off the beat, and a bass
// note under it all. It returns the recording and where each beat was
// played.
func song(rate int, bpm float64, bars int) ([]float32, []time.Duration) {
	beat := 60 / bpm
	total := int(float64(rate) * beat * 4 * float64(bars))
	out := make([]float32, 2*total)
	r := rand.New(rand.NewPCG(7, 9))
	hit := func(at, hz, decay, gain float64, noise bool) {
		start := int(at * float64(rate))
		for k := range int(decay * 5 * float64(rate)) {
			if start+k >= total {
				return
			}
			level := gain * math.Exp(-float64(k)/float64(rate)/decay)
			s := level * math.Sin(2*math.Pi*hz*float64(k)/float64(rate))
			if noise {
				s = level * (r.Float64()*2 - 1)
			}
			out[2*(start+k)] += float32(s)
			out[2*(start+k)+1] += float32(s)
		}
	}
	// A bar of silence first, so the recording does not begin on the
	// very beat it is being asked about.
	lead := 4 * beat
	want := make([]time.Duration, 0, bars*4-4)
	for b := range bars*4 - 4 {
		at := lead + float64(b)*beat
		want = append(want, time.Duration(at*float64(time.Second)))
		switch b % 4 {
		case 0, 2:
			hit(at, 55, 0.12, 0.9, false) // the kick, on one and three
		default:
			hit(at, 180, 0.08, 0.5, true) // the snare, on two and four
		}
		hit(at+beat/2, 8000, 0.02, 0.15, true) // a hat on the and
		hit(at, 6000, 0.01, 0.1, true)
		hit(at, 82.4, 0.3, 0.35, false) // a bass note, held, with no attack of its own
	}
	return out, want
}

// TestAClickTrackFitsInOneTempo: a recording made to a grid comes back
// as a map with a single tempo, at the rates a file is most often
// recorded at. The beats found wobble by a few milliseconds, and the
// fit must not follow that wobble.
func TestAClickTrackFitsInOneTempo(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		want := beats(2*time.Second, 36, func(int) float64 { return 120 })
		r := scan(rate, render(rate, want, 21*time.Second))
		if n := len(r.Map(0).Tempos()); n != 1 {
			t.Errorf("at %d Hz a steady click track fits in %d tempo changes, want 1", rate, n)
		}
	}
}

// stuck is a sound that never ends and never gives anything.
type stuck struct{}

func (stuck) Read([]float32) (int, error) { return 0, nil }

// TestFindGivesUpOnASoundThatGivesNothing: a sound that answers every
// read with nothing, and never says it has ended, must not hang Find.
func TestFindGivesUpOnASoundThatGivesNothing(t *testing.T) {
	if _, err := Find(stuck{}, 48000); !errors.Is(err, io.ErrNoProgress) {
		t.Errorf("Find returned %v, want io.ErrNoProgress", err)
	}
}

// TestNoBinIsCountedTwice: the bands at the bottom are narrower than a
// bin, and each bin belongs to one band at most.
func TestNoBinIsCountedTwice(t *testing.T) {
	for _, rate := range []int{22050, 44100, 48000, 96000} {
		s := NewScan(rate)
		end := 0
		for b, span := range s.d.bands {
			if span[0] < end || span[1] < span[0] {
				t.Errorf("at %d Hz band %d spans bins %v, overlapping the band below it", rate, b, span)
			}
			end = max(end, span[1])
		}
	}
}

// TestAScanAtNoRateStillWorks: a rate of zero is taken as the
// package's own, rather than dividing by it.
func TestAScanAtNoRateStillWorks(t *testing.T) {
	want := beats(time.Second, 28, func(int) float64 { return 100 })
	s := NewScan(0)
	s.Write(render(audio.SampleRate, want, 19*time.Second))
	if r := s.Done(); math.Abs(r.BPM-100) > 0.5 {
		t.Errorf("a scan at rate 0 found %v BPM, want 100", r.BPM)
	}
}
