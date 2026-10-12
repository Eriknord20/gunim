package dynamics

import (
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
	// out is the sound the views hear come out, or nil; proc, where
	// set, is the processor whose traces the views read.
	in, out plug.Sound
	proc    *processor
}

func (h *host) Param(id uint32) float64 {
	if v, ok := h.values[id]; ok {
		return v
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

func newDynamics(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.dynamics")
	if !ok {
		t.Fatal("the plugin did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the plugin made something else")
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

// limiting is the limiter alone, driven by drive into a ceiling.
func limiting(drive, ceiling float64) []plug.Change {
	return set(float64(CompOnID), 0, float64(GateOnID), 0, float64(InputID), drive,
		float64(LimitCeilingID), ceiling, float64(LimitReleaseID), 50)
}

// loud returns n frames of music-like sound: a sine under bursts of
// noise, with lone spikes, the sharpest a limiter meets.
func loud(n int, seed uint64) []float32 {
	r := rand.New(rand.NewPCG(seed, 1))
	s := make([]float32, n)
	for i := range s {
		v := 0.3 * math.Sin(2*math.Pi*110*float64(i)/48000)
		if (i/2000)%3 == 0 {
			v += 0.6 * (2*r.Float64() - 1)
		}
		if i%3001 == 1500 {
			v = 1
		}
		s[i] = float32(v)
	}
	return s
}

// level returns n frames at v.
func level(n int, v float32) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func TestTheLimitersGainIsDownBeforeEachPeakComesOut(t *testing.T) {
	p, _ := newDynamics(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 1, Channels: 2})
	const drive, ceiling = 12.0, -1.0
	l, r := loud(48000, 1), loud(48000, 2)
	top := math.Pow(10, ceiling/20)
	ahead := pr.Latency()
	changes := limiting(drive, ceiling)
	// A frame at a time, so the gain each frame goes out at is seen
	// before the clamp after it could hide a gain too high.
	outL, outR := make([]float32, 1), make([]float32, 1)
	peaks := make([]float64, len(l))
	worst := 0.0
	for i := range l {
		if i == len(l)/2 {
			// The input changes halfway, and glides there.
			changes = set(float64(InputID), drive+6)
		}
		pr.Process(&plug.Block{In: [][]float32{l[i : i+1], r[i : i+1]}, Out: [][]float32{outL, outR}, Frames: 1, Changes: changes})
		changes = nil
		peaks[i] = max(math.Abs(float64(l[i])), math.Abs(float64(r[i]))) * pr.in
		if i >= ahead {
			worst = max(worst, peaks[i-ahead]*pr.lim.gain/top)
		}
	}
	if worst > 1+1e-9 {
		t.Errorf("a peak went out %.2g dB over the ceiling, before the clamp", 20*math.Log10(worst))
	}
	if worst < 0.99 {
		t.Errorf("the loudest peak went out %.2f dB under the ceiling, want at it", 20*math.Log10(worst))
	}
}

func TestQuietSoundPassesUnchangedButLate(t *testing.T) {
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	if pr.Latency() != 240 {
		t.Fatalf("the plugin is %d frames late at 48 kHz, want its 5 ms, 240", pr.Latency())
	}
	in := make([]float32, 4096)
	for i := range in {
		in[i] = float32(0.25 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}
	out := make([]float32, len(in))
	changes := limiting(0, -1)
	for at := 0; at < len(in); at += 512 {
		pr.Process(&plug.Block{In: [][]float32{in[at : at+512]}, Out: [][]float32{out[at : at+512]}, Frames: 512, Changes: changes})
		changes = nil
	}
	for i := range out {
		want := float32(0)
		if i >= 240 {
			want = in[i-240]
		}
		if math.Abs(float64(out[i]-want)) > 1e-6 {
			t.Fatalf("frame %d came out %v, want %v", i, out[i], want)
		}
	}
	if p.proc.Report(LimitGRID) != 0 || p.proc.Report(CompGRID) != 0 {
		t.Error("the plugin reports turning quiet sound down")
	}
}

func TestTheCompressorTurnsDownByItsRatioOverItsThreshold(t *testing.T) {
	k := comp{threshold: -20, ratio: 4, knee: 10}
	for _, c := range []struct{ level, want float64 }{
		{-40, 0},               // well under
		{-26, 0},               // under the knee
		{-20, -0.75 * 25 / 20}, // at the threshold, halfway round the knee
		{-16, -0.75 * 81 / 20}, // 4 over, still in the knee
		{-8, -9},               // 12 over, by 4:1
	} {
		if got := k.curve(c.level); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("at %v dB the compressor turns down %v dB, want %v", c.level, got, c.want)
		}
	}
	// Steady sound, 12 dB over, comes out 9 dB down, made up by 3.
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 4800, Channels: 2})
	ch := [][]float32{level(4800, 0.5), level(4800, 0.5)}
	in := 20 * math.Log10(0.5)
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 4800, Changes: set(float64(CompThresholdID), in-12, float64(CompRatioID), 4,
		float64(CompKneeID), 0, float64(CompAttackID), 1, float64(CompMakeupID), 3, float64(LimitOnID), 0)})
	if got := 20*math.Log10(float64(ch[0][4799])) - in; math.Abs(got+6) > 0.05 {
		t.Errorf("12 dB over at 4:1, made up by 3 dB, the sound comes out %+.2f dB, want -6", got)
	}
	if got := p.proc.Report(CompGRID); math.Abs(got+9) > 0.05 {
		t.Errorf("the compressor reports %.2f dB, want -9", got)
	}
}

func TestTheGateShutsUnderItsThresholdAndOpensOver(t *testing.T) {
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 9600, Channels: 1})
	gating := set(float64(GateOnID), 1, float64(GateThresholdID), -50, float64(GateRangeID), 40,
		float64(GateReleaseID), 20, float64(CompOnID), 0)
	// Quiet, at -60 dB, for 200 ms: shut, 40 dB down.
	quiet := [][]float32{level(9600, 0.001)}
	pr.Process(&plug.Block{In: quiet, Out: quiet, Frames: 9600, Changes: gating})
	if got := 20 * math.Log10(float64(quiet[0][9599])/0.001); math.Abs(got+40) > 0.1 {
		t.Errorf("under the threshold the gate turns down %.2f dB, want its range, 40", got)
	}
	// Loud: open within its attack, the lookahead later.
	loudly := [][]float32{level(9600, 0.5)}
	pr.Process(&plug.Block{In: loudly, Out: loudly, Frames: 9600})
	if got := loudly[0][9599]; math.Abs(float64(got)-0.5) > 1e-3 {
		t.Errorf("over the threshold the gate lets through %v, want 0.5", got)
	}
}

func TestEachPartsReductionIsKeptForTheFramesItShapes(t *testing.T) {
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	// Silence, then loud from 240 frames before frame 2048: the
	// compressor turns down from there on, and the frames it turns
	// down come out from frame 2048 on, the lookahead later.
	const loudAt = 2048 - 240
	n := 4096
	src := make([]float32, n)
	for i := loudAt; i < n; i++ {
		src[i] = 0.5
	}
	changes := set(float64(CompThresholdID), -30, float64(CompRatioID), 4, float64(CompAttackID), 0.1, float64(LimitOnID), 0)
	for at := 0; at < n; at += 512 {
		ch := [][]float32{slices.Clone(src[at : at+512]), slices.Clone(src[at : at+512])}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
	}
	if v, ok := p.proc.compTrace.Get(2047); !ok || v != 0 {
		t.Errorf("the frames out before 2048, of the silence, are kept as turned down %v dB (%v)", v, ok)
	}
	if v, ok := p.proc.compTrace.Get(3000); !ok || v > -10 {
		t.Errorf("the loud frames out are kept as turned down %v dB (%v), want about -18", v, ok)
	}
	if v, ok := p.proc.limitTrace.Get(3000); !ok || v != 0 {
		t.Errorf("with the limiter off, it is kept as turning down %v dB (%v)", v, ok)
	}
}

func TestTheFirstGainsAreTakenAtOnceAndLaterOnesGlide(t *testing.T) {
	p, _ := newDynamics(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1})
	ch := [][]float32{make([]float32, 64)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 64, Changes: set(float64(InputID), 12, float64(OutputID), -6)})
	if want := math.Pow(10, 12.0/20); math.Abs(pr.in-want) > 1e-9 || math.Abs(pr.out-0.5011872336272722) > 1e-9 {
		t.Fatalf("the first block's gains are %v and %v, want +12 dB's %v and -6 dB's at once", pr.in, pr.out, want)
	}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 64, Changes: set(float64(InputID), 0)})
	if pr.in < 2 {
		t.Errorf("a later input gain jumped to %v in a block's 64 frames", pr.in)
	}
}

func TestResetLetsGoOfEverything(t *testing.T) {
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 4800, Channels: 2})
	ch := [][]float32{loud(4800, 1), loud(4800, 2)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 4800, Changes: append(limiting(12, -1), set(float64(CompOnID), 1)...)})
	if p.proc.Report(LimitGRID) > -3 || p.proc.Report(CompGRID) > -3 {
		t.Fatalf("driven 12 dB the limiter reports %.2f dB", p.proc.Report(LimitGRID))
	}
	pr.Reset()
	// What the compressor and gate had turned down, held for the frames
	// coming out, goes too.
	if p.proc.Report(LimitGRID) != 0 || p.proc.Report(CompGRID) != 0 || p.proc.Report(GateGRID) != 0 {
		t.Error("after a reset the plugin reports turning down")
	}
	quiet := [][]float32{make([]float32, 240), make([]float32, 240)}
	pr.Process(&plug.Block{In: quiet, Out: quiet, Frames: 240, Position: plug.Position{Frame: 1 << 20}})
	for _, v := range quiet[0] {
		if v != 0 {
			t.Fatal("sound from before the reset came out after it")
		}
	}
	if v, _ := p.proc.compTrace.Get(1 << 20); v != 0 {
		t.Errorf("after a reset, the frames out are kept as compressed %v dB", v)
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{loud(512, 1), loud(512, 2)}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts, so other
	// goroutines allocate as little as may be meanwhile; every
	// allocation in Process counted, not a mean rounded down.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var made uint64
	for i := range 64 {
		b := &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(512 * i)},
			Changes: set(float64(InputID), float64(i%24), float64(GateOnID), float64(i%2), float64(CompRatioID), float64(1+i%8),
				float64(LimitCeilingID), -float64(i%12))}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pr.Process(b)
		_ = p.proc.Report(LimitGRID)
		runtime.ReadMemStats(&after)
		made += after.Mallocs - before.Mallocs
	}
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

func TestThePluginKeepsNoState(t *testing.T) {
	p, _ := newDynamics(t)
	if p.State() != nil || p.SetState(nil) != nil {
		t.Error("the plugin has state")
	}
	if p.SetState([]byte("x")) == nil {
		t.Error("the plugin took state it cannot have")
	}
}

func TestTheNeedleSwingsAsAVUMetersDoesAndStopsAtThePins(t *testing.T) {
	m := newVU("TEST", func(time.Duration) (float32, bool) { return 0, true })
	from, to := m.needle.Value(), vuAt(0)
	step := time.Millisecond
	var reached time.Duration
	most := float32(0)
	for at := step; at <= time.Second; at += step {
		m.Step(step)
		x := (m.needle.Value() - from) / (to - from)
		if reached == 0 && x >= 0.99 {
			reached = at
		}
		most = max(most, x)
	}
	if reached < 270*time.Millisecond || reached > 330*time.Millisecond {
		t.Errorf("the needle swung 99%% of the way in %v, want 300 ms", reached)
	}
	if over := (most - 1) * 100; over < 0.5 || over > 2 {
		t.Errorf("the needle overshot by %.2f%%, want about 1.3", over)
	}
	// Far over the scale, the needle goes no further than the pin.
	m = newVU("TEST", func(time.Duration) (float32, bool) { return 20, true })
	m.Step(step)
	if got := m.needle.Target(); got > vuAt(vuPin) {
		t.Errorf("at +20 VU the needle heads %.3f along the scale, past the pin at %.3f", got, vuAt(vuPin))
	}
}

// sine is a sine at amp, heard to frame at.
type sine struct {
	amp float64
	at  int64
}

func (s *sine) Heard(dst []float32) (frames int, at int64, rate int) {
	n := len(dst) / 2
	for i := range n {
		v := float32(s.amp * math.Sin(2*math.Pi*1000*float64(s.at-int64(n)+int64(i))/48000))
		dst[2*i], dst[2*i+1] = v, v
	}
	return n, s.at, 48000
}

func TestTheMetersReadTheReductionOfTheFramesHeardAndTheSound(t *testing.T) {
	p, h := newDynamics(t)
	// A sine at the meter's 0 VU, -14 dBFS RMS by default.
	s := &sine{amp: math.Sqrt2 * math.Pow(10, -14.0/20)}
	h.out = s
	h.proc = &p.proc
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	// The plugin compresses steady sound from frame 0 to 48000.
	changes := set(float64(CompThresholdID), -30, float64(CompRatioID), 4, float64(CompAttackID), 0.1)
	for at := 0; at < 48000; at += 512 {
		ch := [][]float32{level(512, 0.5), level(512, 0.5)}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
	}
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	s.at = 24000
	if db, live := pn.out.read(0); !live || math.Abs(float64(db)) > 0.05 {
		t.Errorf("a sine at -14 dBFS reads %.2f VU, want 0", db)
	}
	if db, live := pn.gr.read(0); !live || db > -15 {
		t.Errorf("the reduction of the frames heard reads %v, want about -18", db)
	}
	// Between the chunks the sound comes in, both hold.
	frame := plug.StillAfter / 10
	if db, live := pn.out.read(frame); !live || math.Abs(float64(db)) > 0.05 {
		t.Errorf("between chunks the sound reads %.2f VU, want 0 held", db)
	}
	if db, live := pn.gr.read(frame); !live || db > -15 {
		t.Errorf("between chunks the reduction reads %v, want it held", db)
	}
	// The sound stops: both meters fall to rest.
	for range 10 {
		pn.out.read(frame)
		pn.gr.read(frame)
	}
	if db, live := pn.out.read(frame); live || db > vuLow {
		t.Errorf("a still sound reads %.2f VU, want under the scale", db)
	}
	if db, live := pn.gr.read(frame); live || db != 0 {
		t.Errorf("a still sound's reduction reads %v, want 0", db)
	}
	// The meter's 0 VU moved to -20 dBFS: the same sine reads +6.
	pn.Follow(MeterID, -20, nil)
	s.at += 4800
	if db, _ := pn.out.read(0); math.Abs(float64(db)-6) > 0.05 {
		t.Errorf("with 0 VU at -20 dBFS a sine at -14 reads %.2f VU, want +6", db)
	}
}

func TestTheHistoryScrollsSmoothlyAndDrawsTheReduction(t *testing.T) {
	p, h := newDynamics(t)
	s := &sine{amp: 0.5}
	h.in, h.out, h.proc = s, s, &p.proc
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	changes := set(float64(CompThresholdID), -30, float64(CompRatioID), 4, float64(CompAttackID), 0.1)
	for at := 0; at < 96000; at += 512 {
		ch := [][]float32{level(512, 0.5), level(512, 0.5)}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
	}
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	hs := pn.hist
	// At 144 frames a second, a chunk of 1024 frames every third.
	frame := time.Second / 144
	var moves []float64
	for i := range 144 {
		if i%3 == 0 {
			s.at += 1024
		}
		was := hs.Scrolled()
		hs.Step(frame)
		if i > 20 {
			moves = append(moves, hs.Scrolled()-was)
		}
	}
	if hs.Columns() < 100 {
		t.Fatalf("a second of sound made %d columns", hs.Columns())
	}
	// Each frame moves on about as far as the last: no stalls between
	// chunks and leaps as they come.
	least, most := slices.Min(moves), slices.Max(moves)
	if least < 0.5*most {
		t.Errorf("the scroll moves from %.2f to %.2f columns a frame, unevenly", least, most)
	}
	// The compressor and the limiter, the second reduction drawn.
	_, out, down, _ := hs.Column(hs.Columns() - 1)
	if down[1] > -10 || out < 0.4 {
		t.Errorf("the last column shows the sound out at %v, turned down %v dB; want 0.5, about -18", out, down[1])
	}
	// The window stalls for longer than its views read at once: the
	// history goes on past the frames missed.
	made := hs.Columns()
	s.at += 20000
	hs.Step(frame)
	if want := made + 20000/(2*plug.TraceFrames); hs.Columns() < want-1 || hs.Columns() > want+1 {
		t.Errorf("past 20000 frames missed, the history has %d columns, want about %d", hs.Columns(), want)
	}
	// Playing jumps back: the history starts again.
	s.at -= 48000
	hs.Step(frame)
	if hs.Columns() > 16 {
		t.Errorf("after a jump back the history keeps %d columns", hs.Columns())
	}
}

func TestTheViewsWorkThroughTheHost(t *testing.T) {
	p, h := newDynamics(t)
	h.values[CompOnID] = 0
	h.values[LimitCeilingID] = -0.3
	if p.View(plug.Full, h) != nil {
		t.Error("the plugin has a full view of its own")
	}
	c, ok := p.View(plug.Chip, h).(*chip)
	if !ok || c.label.Text != "-0.3 dB" {
		t.Fatalf("with the compressor off the chip shows %q, want the ceiling, -0.3 dB", c.label.Text)
	}
	c.Follow(CompOnID, 1, nil)
	if c.label.Text != "3.0:1" {
		t.Errorf("with the compressor on the chip shows %q, want its ratio", c.label.Text)
	}

	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	comp := pn.sections[2]
	if comp.Switch.Checked() || !comp.Knobs[0].Disabled {
		t.Error("the compressor, off, shows on")
	}
	// Switched on, as one gesture, its knobs lit.
	comp.Switch.OnChange(true, nil)
	if h.values[CompOnID] != 1 || comp.Knobs[0].Disabled {
		t.Errorf("switched on, the host has %v and the knobs are greyed %v", h.values[CompOnID], comp.Knobs[0].Disabled)
	}
	if !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("the switch sent %v, want one gesture", h.log)
	}
	pn.Follow(CompRatioID, 8, nil)
	if comp.Knobs[1].Value() != 8 {
		t.Errorf("the ratio's knob follows to %v, want 8", comp.Knobs[1].Value())
	}
	pn.Follow(GateOnID, 1, nil)
	if !pn.sections[1].Switch.Checked() || pn.sections[1].Knobs[0].Disabled || !pn.marks.gateOn {
		t.Error("the gate switched on elsewhere stays off in the panel")
	}
}

// tones returns n frames of a quiet low tone at 100 Hz under a loud high
// one at 5 kHz.
func tones(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		t := float64(i) / 48000
		out[i] = float32(0.05*math.Sin(2*math.Pi*100*t) + 0.5*math.Sin(2*math.Pi*5000*t))
	}
	return out
}

// compressed plays a second of tones through the compressor alone, set
// by changes, and returns how far it turns down at the end, and the
// level that comes out over the last tenth at 100 Hz and at 5 kHz, in
// decibels, a sine of 1 at 0.
func compressed(t *testing.T, changes ...float64) (gr, low, high float64) {
	t.Helper()
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 48000, Channels: 2})
	ch := [][]float32{tones(48000), tones(48000)}
	kv := make([]float64, 0, 10+len(changes))
	kv = append(kv, float64(CompThresholdID), -20, float64(CompRatioID), 10, float64(CompKneeID), 0,
		float64(CompAttackID), 1, float64(LimitOnID), 0)
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 48000, Changes: set(append(kv, changes...)...)})
	at := func(hz float64) float64 {
		var re, im float64
		for i, v := range ch[0][43200:] {
			ph := 2 * math.Pi * hz * float64(i) / 48000
			re, im = re+float64(v)*math.Cos(ph), im+float64(v)*math.Sin(ph)
		}
		return 20 * math.Log10(2*math.Hypot(re, im)/4800+1e-12)
	}
	return p.proc.Report(CompGRID), at(100), at(5000)
}

func TestTheSidechainHasTheCompressorListenToABand(t *testing.T) {
	// The whole sound: the loud high tone, 14 dB over, turns it down.
	full, _, _ := compressed(t)
	// Under 300 Hz: only the quiet low tone, 6 dB under, which leaves it.
	low, _, _ := compressed(t, float64(SideModeID), SideLow, float64(SideFreqID), 300)
	// About 5 kHz, and over 2 kHz: the loud tone again.
	band, _, _ := compressed(t, float64(SideModeID), SideBand, float64(SideFreqID), 5000)
	high, _, _ := compressed(t, float64(SideModeID), SideHigh, float64(SideFreqID), 2000)
	// About 100 Hz, the quiet tone: under the threshold.
	quiet, _, _ := compressed(t, float64(SideModeID), SideBand, float64(SideFreqID), 100)
	if full > -10 || band > -10 || high > -10 {
		t.Errorf("listening to the loud tone, the compressor turns down %.1f, %.1f and %.1f dB", full, band, high)
	}
	if low < -1 || quiet < -1 {
		t.Errorf("listening to the quiet tone, under 300 Hz and about 100 Hz, it turns down %.1f and %.1f dB", low, quiet)
	}
	// Listening, the sound out is what it listens to: under 300 Hz, the
	// quiet low tone, at -26 dB, as it was; the loud high tone gone.
	_, lo, hi := compressed(t, float64(SideModeID), SideLow, float64(SideFreqID), 300, float64(SideListenID), 1)
	if math.Abs(lo+26) > 0.5 || hi > -50 {
		t.Errorf("listening under 300 Hz, the low tone comes out at %.1f dB, want -26, and the high one at %.1f dB", lo, hi)
	}
	// Over 2 kHz, the high tone as it was, the low one gone.
	_, lo, hi = compressed(t, float64(SideModeID), SideHigh, float64(SideFreqID), 2000, float64(SideListenID), 1)
	if math.Abs(hi+6) > 0.5 || lo > -50 {
		t.Errorf("listening over 2 kHz, the high tone comes out at %.1f dB, want -6, and the low one at %.1f dB", hi, lo)
	}
}

func TestTheCompressorsAttackRunsToFourHundredMilliseconds(t *testing.T) {
	if got := param(CompAttackID).Max; got != 400 {
		t.Fatalf("the attack runs to %v ms", got)
	}
	// At 400 ms, after 400 ms a step 20 dB over is turned down about
	// 63% of the way; at 10 ms, all of it.
	for _, c := range []struct{ attack, want float64 }{{400, 0.63}, {10, 1}} {
		k := comp{on: true, threshold: -20, ratio: 1e9, attack: coef(c.attack, 48000), release: coef(100, 48000)}
		var gr float64
		for range 19200 {
			gr = k.step(0)
		}
		if got := gr / -20; math.Abs(got-c.want) > 0.02 {
			t.Errorf("at an attack of %v ms, after 400 ms it has turned down %.2f of the way, want %.2f", c.attack, got, c.want)
		}
	}
}
