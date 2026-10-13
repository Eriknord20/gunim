package vocal

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
)

const rate = 48000

// voiceOpts shape a voice made for the tests.
type voiceOpts struct {
	// level scales it, and hiss how loud its S's are against it.
	level, hiss float64
	// ring lifts a narrow band about ringHz, in decibels: a boxy room.
	ring, ringHz float64
}

// makeVoice returns seconds of a sung voice, mono in two channels: in
// phrases with gaps between, a melody of notes from 140 to 260 Hz, a
// vibrato, harmonics falling off as a voice's do, and an S at the start
// of each phrase.
func makeVoice(seconds float64, o voiceOpts) [2][]float32 {
	n := int(seconds * rate)
	out := make([]float32, n)
	r := rand.New(rand.NewPCG(7, 11))
	var hiss biquad
	hiss.set(highPass, 6000, 0, 0.707, rate)
	var hs biquadState
	var ring biquad
	ring.set(bell, max(o.ringHz, 100), o.ring, 6, rate)
	var rs biquadState
	phase := 0.0
	for i := range n {
		t := float64(i) / rate
		// Phrases of 0.8 s, a 0.2 s gap after each.
		at := math.Mod(t, 1)
		var x float64
		if at < 0.8 {
			f0 := melody[int(t*4)%len(melody)] * (1 + 0.02*math.Sin(2*math.Pi*5.5*t))
			phase += 2 * math.Pi * f0 / rate
			env := math.Min(1, at/0.03) * math.Min(1, (0.8-at)/0.05)
			for k := 1; k <= 30; k++ {
				if float64(k)*f0 > 0.45*rate {
					break
				}
				x += env * math.Sin(float64(k)*phase) / math.Pow(float64(k), 1.1)
			}
			x *= 0.25
		}
		h := hiss.run(&hs, r.NormFloat64())
		if at < 0.07 {
			x += o.hiss * h * math.Sin(math.Pi*at/0.07)
		}
		if o.ring != 0 {
			x = ring.run(&rs, x)
		}
		out[i] = float32(o.level * x)
	}
	return [2][]float32{out, append([]float32(nil), out...)}
}

// melody is the notes the voice sings, a quarter of a second each: a
// semitone apart from 140 to 260 Hz, in an order that leaps about, so
// each note is sung as long as any other.
var melody = func() []float64 {
	notes := make([]float64, 0, 11)
	for k := range 11 {
		notes = append(notes, 140*math.Pow(2, float64((k*7)%11)/12))
	}
	return notes
}()

// run runs frames through a chain set as values, in blocks of 512, and
// returns what came out.
func run(values map[uint32]float64, frames [2][]float32) ([2][]float32, *processor) {
	pr := &processor{}
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: 512, Channels: 2})
	out := [2][]float32{append([]float32(nil), frames[0]...), append([]float32(nil), frames[1]...)}
	changes := make([]plug.Change, 0, len(values))
	for id, v := range values {
		changes = append(changes, plug.Change{ID: id, Value: v})
	}
	for at := 0; at < len(out[0]); at += 512 {
		n := min(512, len(out[0])-at)
		ch := [][]float32{out[0][at : at+n], out[1][at : at+n]}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: n, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
	}
	return out, pr
}

// plain is the chain doing nothing: every part off, and no macro adding
// anything.
var plain = map[uint32]float64{DeEssOnID: 0, CleanOnID: 0, ToneOnID: 0, PeakOnID: 0, GlueOnID: 0, WidthID: 0}

func TestTheChainRegistersItself(t *testing.T) {
	d, ok := plug.Lookup("marrasen.vocal")
	if !ok {
		t.Fatal("the vocal chain did not register itself")
	}
	if _, ok := d.New(nil).(plug.TrackPlugin); !ok {
		t.Error("the chain does not read tracks, for Learn")
	}
}

func TestTheChainDoingNothingPassesTheVoiceAsItIs(t *testing.T) {
	in := makeVoice(1, voiceOpts{level: 0.5, hiss: 0.1})
	out, _ := run(plain, in)
	for c := range 2 {
		for i := range in[c] {
			if d := math.Abs(float64(out[c][i] - in[c][i])); d > 1e-6 {
				t.Fatalf("channel %d, frame %d: %g in, %g out", c, i, in[c][i], out[c][i])
			}
		}
	}
}

func TestWidthLeavesTheVoiceInMonoAsItWas(t *testing.T) {
	in := makeVoice(1, voiceOpts{level: 0.5, hiss: 0.1})
	// Some of the sides already, as a doubled voice has.
	for i := range in[1] {
		in[1][i] *= 0.8
	}
	wide := map[uint32]float64{}
	for k, v := range plain {
		wide[k] = v
	}
	wide[WidthID] = 100
	out, _ := run(wide, in)
	var side float64
	for i := range in[0] {
		if d := math.Abs(float64(out[0][i]+out[1][i]) - float64(in[0][i]+in[1][i])); d > 1e-5 {
			t.Fatalf("frame %d: the mono sum moved by %g", i, d)
		}
		side += math.Abs(float64(out[0][i]-out[1][i]) - float64(in[0][i]-in[1][i]))
	}
	if side < 1 {
		t.Errorf("width spread nothing to the sides: %g", side)
	}
}

// power returns the mean power of frames, in decibels.
func power(frames []float32) float64 {
	var s float64
	for _, f := range frames {
		s += float64(f) * float64(f)
	}
	return 10 * math.Log10(max(s/float64(len(frames)), 1e-20))
}

// tone returns seconds of a sine at hz, of amplitude a, in two channels.
func tone(seconds, hz, a float64) [2][]float32 {
	n := int(seconds * rate)
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(a * math.Sin(2*math.Pi*hz*float64(i)/rate))
	}
	return [2][]float32{out, append([]float32(nil), out...)}
}

func TestTheDeEsserTurnsDownHissAndLeavesTheVoice(t *testing.T) {
	deEss := map[uint32]float64{}
	for k, v := range plain {
		deEss[k] = v
	}
	deEss[DeEssOnID], deEss[DeEssThresholdID], deEss[DeEssRangeID], deEss[DeEssFreqID] = 1, -30, 12, 5000
	hiss, _ := run(deEss, tone(0.5, 8000, 0.5))
	if got := power(hiss[0][rate/10:]) - power(tone(0.5, 8000, 0.5)[0][rate/10:]); got > -6 {
		t.Errorf("a loud hiss came out %.1f dB from how it went in, want at least 6 dB down", got)
	}
	low, pr := run(deEss, tone(0.5, 300, 0.5))
	if got := power(low[0][rate/10:]) - power(tone(0.5, 300, 0.5)[0][rate/10:]); math.Abs(got) > 0.5 {
		t.Errorf("a low voice came out %.1f dB from how it went in, want it as it was", got)
	}
	if pr.Report(DeEssGRID) < -0.5 {
		t.Errorf("the de-esser reports %.1f dB on a low voice", pr.Report(DeEssGRID))
	}
}

func TestTheCompressorsTurnTheLoudDownAndReportIt(t *testing.T) {
	comps := map[uint32]float64{}
	for k, v := range plain {
		comps[k] = v
	}
	comps[PeakOnID], comps[GlueOnID], comps[PeakThresholdID], comps[GlueThresholdID] = 1, 1, -20, -24
	loud, pr := run(comps, tone(0.5, 440, 0.5))
	if got := power(loud[0][rate/4:]) - power(tone(0.5, 440, 0.5)[0][rate/4:]); got > -4 {
		t.Errorf("a loud tone came out %.1f dB from how it went in, want it turned down", got)
	}
	if pr.Report(PeakGRID) > -1 || pr.Report(GlueGRID) > -1 {
		t.Errorf("the compressors report %.1f and %.1f dB", pr.Report(PeakGRID), pr.Report(GlueGRID))
	}
	quiet, _ := run(comps, tone(0.5, 440, 0.01))
	if got := power(quiet[0][rate/4:]) - power(tone(0.5, 440, 0.01)[0][rate/4:]); math.Abs(got) > 0.1 {
		t.Errorf("a quiet tone came out %.2f dB from how it went in, want it as it was", got)
	}
	// Comp at nothing takes the ratio to 1:1.
	comps[SquashID] = 0
	flat, _ := run(comps, tone(0.5, 440, 0.5))
	if got := power(flat[0][rate/4:]) - power(tone(0.5, 440, 0.5)[0][rate/4:]); math.Abs(got) > 0.1 {
		t.Errorf("with Comp at nothing, a loud tone came out %.2f dB from how it went in", got)
	}
}

// levelAt returns how loud frames are at hz, in decibels, by a
// transform of their middle.
func levelAt(frames []float32, hz float64) float64 {
	const n = 1 << 14
	fft := audio.NewFFT(n)
	x := make([]complex128, n)
	from := len(frames)/2 - n/2
	for i := range n {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/n)
		x[i] = complex(float64(frames[from+i])*w, 0)
	}
	fft.Transform(x)
	k := int(math.Round(hz * n / rate))
	var best float64
	for j := k - 2; j <= k+2; j++ {
		best = max(best, real(x[j])*real(x[j])+imag(x[j])*imag(x[j]))
	}
	return 10 * math.Log10(max(best, 1e-20))
}

func TestOctaveDownSoundsAnOctaveUnderTheVoice(t *testing.T) {
	in := tone(1, 220, 0.3)
	oct := map[uint32]float64{}
	for k, v := range plain {
		oct[k] = v
	}
	oct[OctaveID] = 100
	out, _ := run(oct, in)
	if got, was := levelAt(out[0], 110), levelAt(in[0], 110); got < was+40 {
		t.Errorf("at 110 Hz, under a 220 Hz voice: %.0f dB out, %.0f dB in; want the octave under it", got, was)
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	pr := &processor{}
	pr.Prepare(plug.Setup{Rate: rate, MaxBlock: 512, Channels: 2})
	v := makeVoice(0.5, voiceOpts{level: 0.5, hiss: 0.1})
	blocks := make([]*plug.Block, 40)
	for i := range blocks {
		ch := [][]float32{v[0][512*i : 512*(i+1)], v[1][512*i : 512*(i+1)]}
		blocks[i] = &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(512 * i)},
			Changes: []plug.Change{{ID: ParallelID, Value: float64(i)}, {ID: TapeID, Value: float64(i)},
				{ID: OctaveID, Value: float64(i)}, {ID: MudFreqID, Value: float64(300 + i)}}}
	}
	made := plugtest.AllocsUnder(t, "vocal.(*processor).Process", func() {
		for _, b := range blocks {
			pr.Process(b)
			_ = pr.Report(PeakGRID)
		}
	})
	if made != 0 {
		t.Errorf("processing made %d things over %d blocks", made, len(blocks))
	}
}

// memSource is a track in memory, as a host gives a plugin.
type memSource struct{ frames [2][]float32 }

func (m *memSource) ID() int       { return 1 }
func (m *memSource) Version() int  { return 1 }
func (m *memSource) Name() string  { return "voice" }
func (m *memSource) Rate() float64 { return rate }
func (m *memSource) Channels() int { return 2 }
func (m *memSource) Frames() int64 { return int64(len(m.frames[0])) }
func (m *memSource) ReadAt(dst [][]float32, at int64) (int, error) {
	n := 0
	for c := range dst {
		n = copy(dst[c], m.frames[c%2][min(at, m.Frames()):])
	}
	return n, nil
}

// memDoc is a document of the one source, played from the start, with
// a loop.
type memDoc struct {
	src  *memSource
	loop plug.Loop
	on   bool
}

func (d memDoc) Sources() []plug.Source  { return []plug.Source{d.src} }
func (d memDoc) Regions() []plug.Region  { return []plug.Region{{Source: 1, Length: d.src.Frames()}} }
func (d memDoc) Loop() (plug.Loop, bool) { return d.loop, d.on }

// loudness returns the integrated loudness of frames, in LUFS.
func loudness(frames [2][]float32) float64 {
	m := audio.NewLoudnessMeter(rate)
	buf := make([]float32, 2*len(frames[0]))
	for i := range frames[0] {
		buf[2*i], buf[2*i+1] = frames[0][i], frames[1][i]
	}
	m.Write(buf)
	l, _ := m.Integrated()
	return l
}

// defaults returns every parameter at its default, with over set.
func defaults(over map[uint32]float64) map[uint32]float64 {
	out := map[uint32]float64{}
	for _, p := range Descriptor.Params {
		out[p.ID] = p.Default
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func learn(t *testing.T, d plug.Document, values map[uint32]float64) learned {
	t.Helper()
	got, err := learnChain(context.Background(), d, d.Sources()[0], values, func(float64) {})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLearnBringsTheVoiceToTheTargetAndEachCompressorToItsCoupleOfDecibels(t *testing.T) {
	src := &memSource{frames: makeVoice(8, voiceOpts{level: 0.08, hiss: 0.15})}
	values := defaults(nil)
	got := learn(t, memDoc{src: src}, values)
	set := defaults(nil)
	for k, v := range got.values {
		set[k] = v
	}
	target := set[TargetID]
	if want := target - loudness(src.frames); math.Abs(got.values[InputID]-want) > 0.2 {
		t.Errorf("Input = %+.1f dB, want %+.1f, to bring the voice to %.0f LUFS", got.values[InputID], want, target)
	}
	v := &voice{src: src, spans: plug.Spans(memDoc{src: src}, src), frames: src.Frames(), rate: rate, progress: func(float64) {},
		buf: [][]float32{make([]float32, learnChunk), make([]float32, learnChunk)}}
	solo := func(id uint32) map[uint32]float64 {
		run := defaults(set)
		run[ParallelID], run[TapeID], run[OctaveID] = 0, 0, 0
		if id == PeakThresholdID {
			run[GlueOnID] = 0
		}
		return run
	}
	peak, err := reductionOf(context.Background(), v, solo(PeakThresholdID), PeakThresholdID, target-learnActive)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(peak-learnPeakGR) > 0.5 {
		t.Errorf("the peak compressor takes %.2f dB off the peaks, want about %.0f", peak, learnPeakGR)
	}
	glue, err := reductionOf(context.Background(), v, solo(GlueThresholdID), GlueThresholdID, target-learnActive)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(glue-learnGlueGR) > 0.5 {
		t.Errorf("the glue compressor takes %.2f dB off on average, want about %.0f", glue, learnGlueGR)
	}
	out, _ := run(set, src.frames)
	if l := loudness(out); math.Abs(l-target) > 0.5 {
		t.Errorf("the chain learned brings the voice out at %.1f LUFS, want %.0f", l, target)
	}
	if got.loop {
		t.Error("Learn says it read a loop, where none was on")
	}
}

func TestLearnReadsTheLoopAloneWhereTheHostLoops(t *testing.T) {
	// A quiet verse, then a chorus 12 dB louder.
	verse := makeVoice(4, voiceOpts{level: 0.05, hiss: 0.1})
	chorus := makeVoice(4, voiceOpts{level: 0.2, hiss: 0.1})
	src := &memSource{frames: [2][]float32{append(verse[0], chorus[0]...), append(verse[1], chorus[1]...)}}
	values := defaults(nil)
	whole := learn(t, memDoc{src: src}, values)
	looped := learn(t, memDoc{src: src, loop: plug.Loop{From: 4 * rate, To: 8 * rate}, on: true}, values)
	if !looped.loop {
		t.Error("Learn does not say it read the loop alone")
	}
	want := values[TargetID] - loudness(chorus)
	if math.Abs(looped.values[InputID]-want) > 0.2 {
		t.Errorf("over the chorus looped, Input = %+.1f dB, want %+.1f", looped.values[InputID], want)
	}
	if looped.values[InputID] > whole.values[InputID]-1 {
		t.Errorf("over the louder chorus, Input = %+.1f dB, no lower than over the track, %+.1f", looped.values[InputID],
			whole.values[InputID])
	}
	// A loop the host turned off reads the track.
	off := learn(t, memDoc{src: src, loop: plug.Loop{From: 4 * rate, To: 8 * rate}}, values)
	if off.values[InputID] != whole.values[InputID] {
		t.Errorf("with the loop off, Input = %+.1f dB, want the whole track's %+.1f", off.values[InputID], whole.values[InputID])
	}
	// A loop over none of the track has nothing to learn.
	if _, err := learnChain(context.Background(), memDoc{src: src, loop: plug.Loop{From: 20 * rate, To: 30 * rate}, on: true},
		src, values, func(float64) {}); !errors.Is(err, errNoLoop) {
		t.Errorf("a loop past the track: %v, want %v", err, errNoLoop)
	}
}

func TestLearnFindsTheBoxyRingAndCutsIt(t *testing.T) {
	for _, hz := range []float64{320, 500} {
		src := &memSource{frames: makeVoice(6, voiceOpts{level: 0.1, hiss: 0.1, ring: 9, ringHz: hz})}
		got := learn(t, memDoc{src: src}, defaults(nil))
		if f := got.values[MudFreqID]; math.Abs(math.Log2(f/hz)) > 1.0/6+0.01 {
			t.Errorf("a ring at %.0f Hz: the cut is at %.0f Hz", hz, f)
		}
		if g := got.values[MudGainID]; g > -3 {
			t.Errorf("a ring 9 dB over the voice at %.0f Hz is cut only %.1f dB", hz, g)
		}
	}
}

func TestLearnStopsWhenTheVoiceChanges(t *testing.T) {
	p := Descriptor.New(nil).(*plugin)
	src := &memSource{frames: makeVoice(20, voiceOpts{level: 0.1, hiss: 0.1})}
	p.SetDocument(memDoc{src: src})
	if ok, loop := p.canLearn(); !ok || loop {
		t.Fatalf("canLearn = %v, %v, want a track and no loop", ok, loop)
	}
	p.startLearning(defaults(nil))
	p.SetDocument(memDoc{src: src, loop: plug.Loop{To: rate}, on: true})
	if _, learning, _, _ := p.learnState(); learning {
		t.Error("a new document leaves the learning under way")
	}
	if _, loop := p.canLearn(); !loop {
		t.Error("with the loop on, Learn would not read the loop alone")
	}
	p.Close()
}
