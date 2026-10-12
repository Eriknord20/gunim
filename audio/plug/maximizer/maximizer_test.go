package maximizer

import (
	"context"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values  map[uint32]float64
	log     []string
	in, out plug.Sound
	proc    *processor
}

func (h *host) Param(id uint32) float64 {
	if v, ok := h.values[id]; ok {
		return v
	}
	if h.proc != nil {
		if p := param(id); p.Flags&plug.ReadOnly != 0 {
			return h.proc.Report(id)
		}
	}
	return param(id).Default
}
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)              { h.log = append(h.log, "end") }
func (h *host) BeginEdit(name string, _ *gunim.UI) { h.log = append(h.log, "edit "+name) }
func (h *host) Changed(*gunim.UI)                  { h.log = append(h.log, "changed") }
func (h *host) EndEdit(*gunim.UI)                  { h.log = append(h.log, "end edit") }
func (h *host) Sound() (in, out plug.Sound)        { return h.in, h.out }
func (h *host) Traced(id uint32, f int64) (float32, bool) {
	if h.proc == nil {
		return 0, false
	}
	return h.proc.Trace(id).Get(f)
}

func newMaximizer(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.maximizer")
	if !ok {
		t.Fatal("the maximizer did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the maximizer made something else")
	}
	return p, h
}

// set returns changes at frame 0, by ID and value in turn.
func set(kv ...float64) []plug.Change {
	var out []plug.Change
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, plug.Change{ID: uint32(kv[i]), Value: kv[i+1]})
	}
	return out
}

// music returns n frames of music-like sound on a channel: a bass line
// and a high tone near a quarter of the rate, whose peaks fall between
// the samples, under bursts of noise and lone spikes.
func music(n int, seed uint64) []float32 {
	r := rand.New(rand.NewPCG(seed, 9))
	s := make([]float32, n)
	for i := range s {
		v := 0.25*math.Sin(2*math.Pi*55*float64(i)/48000) + 0.15*math.Sin(2*math.Pi*11990*float64(i)/48000+0.7)
		if (i/3000)%4 == 0 {
			v += 0.35 * (2*r.Float64() - 1)
		}
		if i%24001 == 12000 {
			v = 0.95
		}
		s[i] = float32(v)
	}
	return s
}

// run plays in through a prepared maximizer pr in blocks of 512, and
// returns what comes out.
func run(pr plug.Processor, in [2][]float32, changes []plug.Change) [2][]float32 {
	var out [2][]float32
	for at := 0; at < len(in[0]); at += 512 {
		n := min(512, len(in[0])-at)
		ch := [][]float32{slices.Clone(in[0][at : at+n]), slices.Clone(in[1][at : at+n])}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
		out[0], out[1] = append(out[0], ch[0]...), append(out[1], ch[1]...)
	}
	return out
}

// truePeakOf returns the true peak of channels, in dBTP, as a meter
// apart from the maximizer's finds it.
func truePeakOf(ch [2][]float32) float64 {
	most := 0.0
	for _, c := range ch {
		var tp truePeak
		for _, v := range c {
			most = max(most, tp.push(float64(v)))
		}
	}
	return dbOf(most)
}

func rmsOf(s []float32) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return 10 * math.Log10(sum/float64(len(s)))
}

func TestTruePeaksStayUnderTheCeilingInEveryMode(t *testing.T) {
	in := [2][]float32{music(96000, 1), music(96000, 2)}
	for mode, name := range modeNames {
		for _, link := range []float64{100, 0} {
			p, _ := newMaximizer(t)
			pr := p.Processor()
			pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
			out := run(pr, in, set(float64(GainID), 12, float64(CeilingID), -1, float64(ModeID), float64(mode),
				float64(LinkID), link))
			if tp := truePeakOf(out); tp > -1+0.25 {
				t.Errorf("%s, linked %v%%: the true peak comes out at %.2f dBTP, over the ceiling of -1", name, link, tp)
			}
			if gain := rmsOf(out[0][24000:]) - rmsOf(in[0][24000:]); gain < 4 {
				t.Errorf("%s, linked %v%%: 12 dB of gain made the sound only %.1f dB louder", name, link, gain)
			}
		}
	}
}

func TestQuietSoundPassesUnchangedButLate(t *testing.T) {
	for mode := range modeNames {
		p, _ := newMaximizer(t)
		pr := p.Processor()
		pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
		late := pr.Latency()
		if late != 240+tpDelay {
			t.Fatalf("the maximizer is %d frames late, want its 5 ms and the true peaks' filter, %d", late, 240+tpDelay)
		}
		s := make([]float32, 4800)
		for i := range s {
			s[i] = float32(0.2 * math.Sin(2*math.Pi*1000*float64(i)/48000))
		}
		out := run(pr, [2][]float32{s, s}, set(float64(ModeID), float64(mode)))
		for i := late; i < len(s); i++ {
			if math.Abs(float64(out[0][i]-s[i-late])) > 1e-6 {
				t.Fatalf("mode %d: frame %d came out %v, want %v", mode, i, out[0][i], s[i-late])
			}
		}
	}
}

func TestTheStereoLinkTurnsTheOtherChannelDownToo(t *testing.T) {
	loud, quiet := make([]float32, 9600), make([]float32, 9600)
	for i := range loud {
		loud[i] = float32(0.9 * math.Sin(2*math.Pi*200*float64(i)/48000))
		quiet[i] = float32(0.1 * math.Sin(2*math.Pi*300*float64(i)/48000))
	}
	quietOut := func(link float64) float64 {
		p, _ := newMaximizer(t)
		pr := p.Processor()
		pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
		out := run(pr, [2][]float32{loud, quiet}, set(float64(GainID), 6, float64(LinkID), link))
		return rmsOf(out[1][4800:]) - rmsOf(quiet[4800:])
	}
	if linked, apart := quietOut(100), quietOut(0); linked > apart-3 || math.Abs(apart-6) > 0.1 {
		t.Errorf("the quiet channel comes out %+.2f dB linked and %+.2f dB apart: want it turned down with the loud "+
			"one, and up 6 dB apart", linked, apart)
	}
}

// recovery returns how many frames after loud ends the gain takes to
// come back within half a decibel, through a maximizer set by changes:
// loud, a burst over the ceiling as long as burst frames.
func recovery(t *testing.T, burst int, changes []plug.Change) int {
	t.Helper()
	p, _ := newMaximizer(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 1, Channels: 2})
	changes = append(set(float64(GainID), 6), changes...)
	for i := range burst + 96000 {
		v := float32(0.05)
		if i < burst {
			v = 0.9
		}
		ch := [][]float32{{v}, {v}}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 1, Changes: changes, Position: plug.Position{Frame: int64(i)}})
		changes = nil
		if i > burst && pr.cores[0].gain > gainOf(-0.5) {
			return i - burst
		}
	}
	return math.MaxInt
}

func TestTransparentComesBackFastAfterAPeakAndSlowAfterAPassage(t *testing.T) {
	transparent := set(float64(ModeID), Transparent)
	peak, passage := recovery(t, 48, transparent), recovery(t, 96000, transparent)
	if peak*2 > passage {
		t.Errorf("Transparent came back in %d frames after a lone peak and %d after two seconds loud: want the "+
			"peak far faster", peak, passage)
	}
	// Character slows it.
	fast := recovery(t, 4800, set(float64(ModeID), Punchy, float64(CharacterID), 1))
	slow := recovery(t, 4800, set(float64(ModeID), Punchy, float64(CharacterID), 8))
	if fast*4 > slow {
		t.Errorf("at a character of 1 the gain came back in %d frames, and at 8 in %d: want it far slower", fast, slow)
	}
}

func TestPunchyTurnsDownLaterBeforeAPeak(t *testing.T) {
	// How many frames before a peak comes out the gain starts to fall.
	before := func(mode int) int {
		p, _ := newMaximizer(t)
		pr := &p.proc
		pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 1, Channels: 2})
		changes := set(float64(ModeID), float64(mode))
		late := pr.Latency()
		first := -1
		for i := range 4000 {
			v := float32(0.1)
			if i == 2000 {
				v = 2
			}
			ch := [][]float32{{v}, {v}}
			pr.Process(&plug.Block{In: ch, Out: ch, Frames: 1, Changes: changes})
			changes = nil
			if first < 0 && pr.cores[0].gain < 0.99 {
				first = i
			}
		}
		return 2000 + late - first
	}
	if transparent, punchy := before(Transparent), before(Punchy); punchy*2 > transparent {
		t.Errorf("the gain starts to fall %d frames before a peak, Transparent, and %d, Punchy: want Punchy far later",
			transparent, punchy)
	}
}

func TestTheMaximizerReportsAndResetLetsGo(t *testing.T) {
	p, _ := newMaximizer(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	run(pr, [2][]float32{music(24000, 1), music(24000, 2)}, set(float64(GainID), 12, float64(CeilingID), -2))
	if r := pr.Report(ReductionID); r > -1 {
		t.Errorf("driven 12 dB, the maximizer reports %.2f dB of reduction", r)
	}
	if hold := pr.Report(PeakHoldID); hold > -2+0.25 || hold < -3 {
		t.Errorf("under a ceiling of -2, the most true peak reported is %.2f dBTP", hold)
	}
	if v, ok := pr.trace.Get(20000); !ok || v > -1 {
		t.Errorf("the reduction is kept for frame 20000 as %v (%v)", v, ok)
	}
	// Quiet after: the last block's peak falls, the most holds.
	run(pr, [2][]float32{make([]float32, 4800), make([]float32, 4800)}, nil)
	if pr.Report(PeakID) > -40 || pr.Report(PeakHoldID) < -3 {
		t.Errorf("quiet after, the true peak reads %.1f dBTP, and the most %.1f", pr.Report(PeakID), pr.Report(PeakHoldID))
	}
	pr.Reset()
	if pr.Report(ReductionID) != 0 || pr.Report(PeakHoldID) > -59 {
		t.Error("after a reset the maximizer reports what it did before")
	}
	quiet := [][]float32{make([]float32, 512), make([]float32, 512)}
	pr.Process(&plug.Block{In: quiet, Out: quiet, Frames: 512})
	for _, v := range quiet[0] {
		if v != 0 {
			t.Fatal("sound from before the reset came out after it")
		}
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newMaximizer(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{music(512, 1), music(512, 2)}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	blocks := make([]*plug.Block, 64)
	for i := range blocks {
		blocks[i] = &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(512 * i)},
			Changes: set(float64(GainID), float64(i%24), float64(ModeID), float64(i%3), float64(CharacterID),
				float64(i%10), float64(LinkID), float64(i%2*100))}
	}
	// Only what Process makes counts, not what other goroutines do.
	made := plugtest.AllocsUnder(t, "maximizer.(*processor).Process", func() {
		for _, b := range blocks {
			pr.Process(b)
			_ = p.proc.Report(PeakHoldID)
		}
	})
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

// memSource is a track in memory, as a host gives a plugin.
type memSource struct {
	frames [2][]float32
	gate   chan struct{}
}

func (m *memSource) ID() int       { return 1 }
func (m *memSource) Version() int  { return 1 }
func (m *memSource) Name() string  { return "music" }
func (m *memSource) Rate() float64 { return 48000 }
func (m *memSource) Channels() int { return 2 }
func (m *memSource) Frames() int64 { return int64(len(m.frames[0])) }
func (m *memSource) ReadAt(dst [][]float32, at int64) (int, error) {
	if m.gate != nil {
		<-m.gate
	}
	n := 0
	for c := range dst {
		n = copy(dst[c], m.frames[c%2][min(at, m.Frames()):])
	}
	return n, nil
}

type memDoc struct{ src *memSource }

func (d memDoc) Sources() []plug.Source { return []plug.Source{d.src} }
func (d memDoc) Regions() []plug.Region { return []plug.Region{{Source: 1, Length: d.src.Frames()}} }

// quietMusic returns seconds of music at scale, as a track.
func quietMusic(seconds int, scale float32) *memSource {
	m := &memSource{frames: [2][]float32{music(seconds*48000, 3), music(seconds*48000, 4)}}
	for c := range m.frames {
		for i := range m.frames[c] {
			m.frames[c][i] *= scale
		}
	}
	return m
}

func TestLearnFindsTheGainThatReachesTheTarget(t *testing.T) {
	src := quietMusic(8, 0.25)
	s := settingsOf(func(uint32) (float64, bool) { return 0, false })
	var at float64
	// A target the track reaches only limited, so the first guess falls
	// short.
	got, err := learnGain(context.Background(), src, s, -7, func(p float64) { at = p })
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.loud+7) > 0.15 || got.gain <= 0 || at != 1 || got.passes > 3 {
		t.Fatalf("Learn found %+.1f dB in %d passes, coming out at %.2f LUFS, having got %v of the way; want -7, "+
			"in 3 at most", got.gain, got.passes, got.loud, at)
	}
	// Measured apart: the track at that gain comes out at the target.
	s.gain = got.gain
	loud, err := loudnessOf(context.Background(), src, &s, func(float64) {})
	if err != nil || math.Abs(loud+7) > 0.2 {
		t.Errorf("at the gain learned, the track comes out at %.2f LUFS (%v)", loud, err)
	}
	// Out of reach: as much gain as there is, and no further.
	got, err = learnGain(context.Background(), quietMusic(4, 0.003), s, -6, func(float64) {})
	if err != nil || got.gain != param(GainID).Max || got.loud > -7 {
		t.Errorf("for a track too quiet, Learn found %+.1f dB, coming out at %.2f LUFS (%v)", got.gain, got.loud, err)
	}
}

func TestLearnStopsAndANewTrackStopsIt(t *testing.T) {
	p, _ := newMaximizer(t)
	if p.canLearn() {
		t.Fatal("with no track, the maximizer says it can learn")
	}
	src := quietMusic(2, 0.25)
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	s := settingsOf(func(uint32) (float64, bool) { return 0, false })
	p.startLearning(s, -14)
	if _, learning, _, _ := p.learnState(); !learning {
		t.Fatal("started, the maximizer is not learning")
	}
	p.stopLearning()
	close(src.gate)
	time.Sleep(200 * time.Millisecond)
	if got, learning, _, _ := p.learnState(); learning || got != nil {
		t.Fatal("stopped, the maximizer learned on")
	}
	src.gate = make(chan struct{})
	p.startLearning(s, -14)
	p.SetDocument(memDoc{quietMusic(2, 0.25)})
	close(src.gate)
	time.Sleep(200 * time.Millisecond)
	if got, learning, _, _ := p.learnState(); learning || got != nil {
		t.Fatal("a new track left the learning of the old going")
	}
}

func TestTheLoudnessMeterAgreesWithGunims(t *testing.T) {
	// The learning measures with gunim's meter: a sanity check that a
	// sine at -20 dBFS RMS reads about -20 LUFS, as BS.1770 has it near
	// 1 kHz.
	src := &memSource{frames: [2][]float32{make([]float32, 96000), make([]float32, 96000)}}
	for i := range src.frames[0] {
		v := float32(math.Sqrt2 * math.Pow(10, -20.0/20) * math.Sin(2*math.Pi*1000*float64(i)/48000))
		src.frames[0][i], src.frames[1][i] = v, v
	}
	loud, err := loudnessOf(context.Background(), src, nil, func(float64) {})
	if err != nil || math.Abs(loud-(-20+3.01+0.69)) > 1 {
		t.Errorf("a stereo sine at -20 dBFS RMS reads %.2f LUFS (%v)", loud, err)
	}
	_ = audio.SampleRate
}
