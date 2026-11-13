package dynamics

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/geom"
)

// hitsAt returns n frames of a trigger: 0, with a pulse of each velocity on its frame, by frame and velocity in turn.
func hitsAt(n int, fv ...float64) []float32 {
	s := make([]float32, n)
	for i := 0; i+1 < len(fv); i += 2 {
		s[int(fv[i])] = float32(fv[i+1])
	}
	return s
}

// gating is the gate and the compressor at work with fast times, and the limiter off.
func gating() []plug.Change {
	return set(float64(GateOnID), 1, float64(GateThresholdID), -30, float64(GateRangeID), 40, float64(GateReleaseID), 20,
		float64(CompThresholdID), -20, float64(CompRatioID), 8, float64(CompAttackID), 1, float64(LimitOnID), 0)
}

// driven runs frames of loud sound through the plugin as ports say, in blocks of block, and returns what came out.
func driven(t *testing.T, n, block int, ports plugtest.Ports) (plugtest.Result, *processor) {
	t.Helper()
	p, _ := newDynamics(t)
	ports.In = [][]float32{loud(n, 1), loud(n, 2)}
	return plugtest.Drive(t, &Descriptor, &p.proc, plug.Setup{Rate: 48000, MaxBlock: block, Channels: 2}, n, ports), &p.proc
}

func TestAHitHoldsTheKeyFor20Milliseconds(t *testing.T) {
	for rate, want := range map[float64]int{48000: 960, 96000: 1920, 44100: 882} {
		var k hitKey
		k.prepare(rate)
		if k.step(0) != 0 || k.running() {
			t.Fatalf("at %v Hz the key sounds before any hit", rate)
		}
		held := 0
		for v := k.step(0.75); v == 0.75; v = k.step(0) {
			held++
			if running := k.running(); running != (held < want) {
				t.Fatalf("at %v Hz, %d frames into a hit, the hold runs: %v", rate, held, running)
			}
		}
		if held != want || k.step(0) != 0 {
			t.Errorf("at %v Hz a hit holds the key for %d frames, not %d, then silence", rate, held, want)
		}
	}
}

func TestALaterHitTakesTheKeyOverAtItsOwnVelocity(t *testing.T) {
	// Each case is hits by frame and velocity, and the level heard from each frame on, to the next.
	for name, c := range map[string]struct{ hits, want []float64 }{
		"a quieter hit after a louder":  {[]float64{0, 1, 480, 0.5}, []float64{0, 1, 480, 0.5, 1440, 0}},
		"a louder hit after a quieter":  {[]float64{0, 0.5, 480, 1}, []float64{0, 0.5, 480, 1, 1440, 0}},
		"three hits, each quieter":      {[]float64{0, 1, 240, 0.7, 480, 0.3}, []float64{0, 1, 240, 0.7, 480, 0.3, 1440, 0}},
		"a hit between two louder ones": {[]float64{0, 1, 240, 0.3, 480, 0.7}, []float64{0, 1, 240, 0.3, 480, 0.7, 1440, 0}},
		"a hit on a hold's last frame":  {[]float64{0, 0.5, 959, 0.25}, []float64{0, 0.5, 959, 0.25, 1919, 0}},
		"a hit on the frame after one":  {[]float64{0, 1, 1, 0.25}, []float64{0, 1, 1, 0.25, 961, 0}},
		"a hit after the hold is done":  {[]float64{0, 1, 960, 0.25}, []float64{0, 1, 960, 0.25, 1920, 0}},
	} {
		var k hitKey
		k.prepare(48000)
		hits, want := hitsAt(2000, c.hits...), float32(0)
		for f, hit := range hits {
			for i := 0; i+1 < len(c.want); i += 2 {
				if int(c.want[i]) == f {
					want = float32(c.want[i+1])
				}
			}
			if got := k.step(hit); got != want {
				t.Fatalf("%s: on frame %d the detector hears %v, want %v", name, f, got, want)
			}
		}
		if k.count != len(c.hits)/2 {
			t.Errorf("%s: %d hits counted, not %d", name, k.count, len(c.hits)/2)
		}
	}
}

func TestAHitOfNoVelocityIsNoHit(t *testing.T) {
	var k hitKey
	k.prepare(48000)
	// Before a hit, and in the middle of a hold, which runs on at its level to its own end.
	for _, v := range []float32{0, -0.5, float32(math.NaN())} {
		if got := k.step(v); got != 0 || k.running() || k.count != 0 {
			t.Fatalf("a pulse of %v is heard as %v, with %d hits counted", v, got, k.count)
		}
	}
	k.step(0.5)
	for f := 1; f < 960; f++ {
		if got := k.step([]float32{0, -0.5, float32(math.NaN())}[f%3]); got != 0.5 {
			t.Fatalf("on frame %d of a hold a pulse of no velocity leaves the detector hearing %v, not 0.5", f, got)
		}
	}
	if got := k.step(0); got != 0 || k.running() || k.count != 1 {
		t.Errorf("after the hold the detector hears %v, with %d hits counted", got, k.count)
	}
	k.step(1)
	k.clear()
	if k.running() || k.step(0) != 0 {
		t.Error("cleared, the key still holds its hit")
	}
}

func TestTheKeysReportsAreHiddenAndNeverSet(t *testing.T) {
	for _, id := range []uint32{KeyWiredID, KeyHitsID} {
		prm := param(id)
		if prm.Flags != plug.ReadOnly|plug.Hidden {
			t.Errorf("%q has flags %b, not read-only and hidden alone", prm.Name, prm.Flags)
		}
		for _, patch := range Descriptor.Patches {
			if _, ok := patch.Params[id]; ok {
				t.Errorf("the patch %q sets %q", patch.Name, prm.Name)
			}
		}
	}
	p, _ := newDynamics(t)
	if p.State() != nil {
		t.Error("the plugin keeps a state, which could hold Key's reports")
	}
}

func TestWithKeyWiredAndNoHitsLoudSoundIsNotCompressedAndTheGateStaysShut(t *testing.T) {
	const n = 48000
	// Loud sound on the sidechain too: wired, Key takes its place.
	for name, aux := range map[string][][][]float32{"alone": nil, "under a loud sidechain": {{loud(n, 3), loud(n, 4)}}} {
		r, pr := driven(t, n, 512, plugtest.Ports{AuxIn: aux, ControlIn: [][]float32{make([]float32, n)}, Changes: gating()})
		gate, comp := r.ControlOut[GateOut], r.ControlOut[CompOut]
		for f := range n {
			if comp[f] != 0 {
				t.Fatalf("%s: frame %d is compressed by %v dB with no hit on Key", name, f, comp[f])
			}
			if f > 0 && gate[f] > gate[f-1] {
				t.Fatalf("%s: the gate opens on frame %d, from %v to %v dB, with no hit on Key", name, f, gate[f-1], gate[f])
			}
		}
		if gate[n-1] > -39.9 {
			t.Errorf("%s: after a second with no hit the gate is at %v dB, not shut at -40", name, gate[n-1])
		}
		if pr.Report(KeyWiredID) != 1 || pr.Report(KeyHitsID) != 0 {
			t.Errorf("%s: the plugin reports Key wired as %v, with %v hits", name, pr.Report(KeyWiredID), pr.Report(KeyHitsID))
		}
	}
}

func TestAHitActsAs20MillisecondsOfItsLevelOnTheSidechain(t *testing.T) {
	// Keyed, the gate rests shut from the start. Under a sidechain it starts open and has shut by settled, well before
	// the hit.
	const n, at, settled = 72000, 48000, 40000
	for name, v := range map[string]float32{"velocity 1.0, as 0 dBFS": 1, "velocity 0.5, as -6 dBFS": 0.5} {
		side := make([]float32, n)
		for f := at; f < at+960; f++ {
			side[f] = v
		}
		hit, _ := driven(t, n, 512, plugtest.Ports{ControlIn: [][]float32{hitsAt(n, at, float64(v))}, Changes: gating()})
		sound, _ := driven(t, n, 512, plugtest.Ports{AuxIn: [][][]float32{{side, side}}, Changes: gating()})
		moved := false
		for f := settled; f < n; f++ {
			for c := range 2 {
				if hit.Out[c][f] != sound.Out[c][f] {
					t.Fatalf("%s: frame %d of channel %d comes out %v by the hit, %v by the sidechain", name, f, c,
						hit.Out[c][f], sound.Out[c][f])
				}
			}
			for port := range hit.ControlOut {
				if hit.ControlOut[port][f] != sound.ControlOut[port][f] {
					t.Fatalf("%s: on frame %d control output %d is %v dB by the hit, %v dB by the sidechain", name, f, port,
						hit.ControlOut[port][f], sound.ControlOut[port][f])
				}
			}
			moved = moved || hit.ControlOut[CompOut][f] < -1
		}
		if !moved {
			t.Errorf("%s: the hit never compressed the sound by 1 dB", name)
		}
	}
}

func TestAHitActsFromItsOwnFrameNotFromTheBlocksStart(t *testing.T) {
	const n = 9600
	for _, block := range []int{64, 512, 1000} {
		for _, at := range []int{4800, 4801, 5311, 5999} {
			hits := [][]float32{hitsAt(n, float64(at), 1)}
			r, pr := driven(t, n, block, plugtest.Ports{ControlIn: hits, Changes: gating()})
			// A frame's reduction comes out with its sound, the lookahead later.
			out, gate := at+pr.Latency(), r.ControlOut[GateOut]
			for f := 1; f < out; f++ {
				if gate[f] > gate[f-1] {
					t.Fatalf("in blocks of %d, a hit on frame %d opens the gate on frame %d, before its own", block, at, f-pr.Latency())
				}
			}
			if gate[out] <= gate[out-1] {
				t.Errorf("in blocks of %d, on a hit's own frame, %d, the gate goes from %v to %v dB", block, at, gate[out-1], gate[out])
			}
			if pr.Report(KeyHitsID) != 1 {
				t.Errorf("in blocks of %d the plugin reports %v hits, not 1", block, pr.Report(KeyHitsID))
			}
			// The compressor hears a hit through the gate, so it is tried with the gate off.
			r, _ = driven(t, n, block, plugtest.Ports{ControlIn: hits, Changes: append(gating(), set(float64(GateOnID), 0)...)})
			comp := r.ControlOut[CompOut]
			for f := range out {
				if comp[f] != 0 {
					t.Fatalf("in blocks of %d, a hit on frame %d compresses frame %d, before its own", block, at, f-pr.Latency())
				}
			}
			if comp[out] >= 0 {
				t.Errorf("in blocks of %d, on a hit's own frame, %d, the compressor is at %v dB", block, at, comp[out])
			}
		}
	}
}

// unkeyedHash runs loud sound through every part with Key not wired, as control says, alone and under a sidechain,
// with parameters changing on the way, and returns a hash of every frame out and of each part's reduction.
func unkeyedHash(t *testing.T, control [][]float32) string {
	t.Helper()
	const n = 48000
	sum := sha256.New()
	var four [4]byte
	for _, side := range [][][]float32{nil, {loud(n, 5), loud(n, 6)}} {
		p, _ := newDynamics(t)
		changes := set(float64(GateOnID), 1, float64(GateThresholdID), -24, float64(CompThresholdID), -30,
			float64(CompRatioID), 6, float64(InputID), 6, float64(SideModeID), SideBand, float64(SideFreqID), 400)
		changes = append(changes, plug.Change{Frame: 12345, ID: SideModeID, Value: SideFull},
			plug.Change{Frame: 30001, ID: CompMakeupID, Value: 4}, plug.Change{Frame: 40000, ID: GateOnID, Value: 0})
		ports := plugtest.Ports{In: [][]float32{loud(n, 1), loud(n, 2)}, ControlIn: control, Changes: changes}
		if side != nil {
			ports.AuxIn = [][][]float32{side}
		}
		r := plugtest.Drive(t, &Descriptor, &p.proc, plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2}, n, ports)
		for f := range n {
			g, _ := p.proc.gateTrace.Get(int64(f))
			c, _ := p.proc.compTrace.Get(int64(f))
			l, _ := p.proc.limitTrace.Get(int64(f))
			for _, v := range []float32{r.Out[0][f], r.Out[1][f], g, c, l} {
				binary.LittleEndian.PutUint32(four[:], math.Float32bits(v))
				sum.Write(four[:])
			}
		}
		if p.proc.Report(KeyWiredID) != 0 || p.proc.Tail() != 0 {
			t.Errorf("not wired, the plugin reports Key wired as %v and a tail of %d", p.proc.Report(KeyWiredID), p.proc.Tail())
		}
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// unkeyedGolden is unkeyedHash of the plugin as it was before it had Key, on amd64.
const unkeyedGolden = "98013e1d08c5f4c10692145d393930e0ef538c1549ea44b7b832b7abd7c6f117"

func TestWithKeyNotWiredTheOutputIsWhatItWasBeforeKeyToTheBit(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the hash is of amd64's arithmetic")
	}
	for name, control := range map[string][][]float32{"no control inputs": nil, "a nil Key": {nil}} {
		if got := unkeyedHash(t, control); got != unkeyedGolden {
			t.Errorf("with %s the output hashes to %s, not %s as before Key", name, got, unkeyedGolden)
		}
	}
}

// block returns a stereo block of n frames of loud sound starting at frame, with hits on Key, or Key not wired where
// hits is nil.
func block(frame int64, n int, hits []float32) *plug.Block {
	ch := [][]float32{loud(n, 1), loud(n, 2)}
	b := &plug.Block{In: ch, Out: ch, Frames: n, Position: plug.Position{Frame: frame}}
	if hits != nil {
		b.ControlIn = [][]float32{hits}
	}
	return b
}

func TestWithTheGateAndTheCompressorOffTheTailIsEndlessDuringAHoldAndNoneAfterIt(t *testing.T) {
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	off := set(float64(GateOnID), 0, float64(CompOnID), 0)
	if pr.Tail() != 0 {
		t.Fatalf("before any block the tail is %d, not 0", pr.Tail())
	}
	// A hit on frame 100 is held to frame 1060: through the first two blocks of 512, and done in the third.
	for i, c := range []struct {
		hits []float32
		want int
	}{
		{make([]float32, 512), 0},
		{hitsAt(512, 100, 0.8), math.MaxInt32},
		{make([]float32, 512), math.MaxInt32},
		{make([]float32, 512), 0},
		{hitsAt(512, 511, 0.8), math.MaxInt32},
		// Key let go of in the middle of a hold ends it.
		{nil, 0},
		{make([]float32, 512), 0},
	} {
		b := block(int64(512*i), 512, c.hits)
		b.Changes, off = off, nil
		pr.Process(b)
		if got := pr.Tail(); got != c.want {
			t.Errorf("after block %d the tail is %d, want %d", i, got, c.want)
		}
	}
	pr.Process(block(512*7, 512, hitsAt(512, 0, 1)))
	pr.Reset()
	if pr.Tail() != 0 {
		t.Errorf("reset in the middle of a hold, the tail is %d, not 0", pr.Tail())
	}
}

func TestProcessAllocatesNothingWithHitsArriving(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	p, _ := newDynamics(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	// Hits a few frames apart, each taking the key over from the one before.
	hits := make([]float32, 512)
	for f := 0; f < 512; f += 3 {
		hits[f] = 1 - float32(f%96)/100
	}
	ctl := [][]float32{make([]float32, 512), make([]float32, 512), make([]float32, 512)}
	b := block(0, 512, hits)
	b.ControlOut = ctl
	b.Changes = gating()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	allocs := testing.AllocsPerRun(200, func() {
		pr.Process(b)
		_ = pr.Tail()
		_ = p.proc.Report(KeyHitsID)
		b.Position.Frame += 512
	})
	if allocs != 0 {
		t.Errorf("Process allocates %v times a block with hits arriving", allocs)
	}
	if p.proc.Report(KeyHitsID) == 0 {
		t.Error("the plugin reports no hits")
	}
}

func TestAKeyedDeviceThatSleptWakesWithItsGateShut(t *testing.T) {
	// A hit opens the gate and compresses, then the host skips two seconds of its clock, as when it slept the device.
	run := func(hits func(n int, fv ...float64) []float32, gap int64) (*processor, *plug.Block) {
		p, _ := newDynamics(t)
		p.proc.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
		first := block(0, 512, hits(512, 10, 1))
		first.Changes = gating()
		p.proc.Process(first)
		var silent []float32
		if hits(512) != nil {
			silent = make([]float32, 512)
		}
		woken := block(512+gap, 512, silent)
		woken.ControlOut = [][]float32{make([]float32, 512), make([]float32, 512), make([]float32, 512)}
		p.proc.Process(woken)
		return &p.proc, woken
	}
	pr, woken := run(hitsAt, 96000)
	// The frames shaped before the sleep come out first, the lookahead long.
	for f := pr.Latency(); f < 512; f++ {
		if g, c := woken.ControlOut[GateOut][f], woken.ControlOut[CompOut][f]; g > -39.9 || c != 0 {
			t.Fatalf("keyed, on frame %d after a sleep the gate is at %v dB and the compressor at %v dB, not shut and at rest", f, g, c)
		}
	}
	// Not wired, a gap in the host's clock changes nothing.
	unwired := func(int, ...float64) []float32 { return nil }
	_, gapped := run(unwired, 96000)
	_, whole := run(unwired, 0)
	for f := range 512 {
		if gapped.Out[0][f] != whole.Out[0][f] || gapped.ControlOut[GateOut][f] != whole.ControlOut[GateOut][f] {
			t.Fatalf("not wired, frame %d after a gap in the host's clock comes out %v, not %v", f, gapped.Out[0][f], whole.Out[0][f])
		}
	}
}

// wiredHosted is a host that knows the wires into its plugin.
type wiredHosted struct {
	*host
	wired bool
}

func (h *wiredHosted) ControlInWired(key string) bool { return h.wired && key == "Key" }

func TestKeysBoltFadesInAndOutAndStepsOnlyWhileItMoves(t *testing.T) {
	const frame = time.Second / 60
	_, h := newDynamics(t)
	h.values[KeyWiredID] = 1
	if b := newKeyBolt(h); !b.on || b.in.Value() != 1 || b.Step(frame) {
		t.Fatalf("a bolt made with Key wired starts at %v, or moves", b.in.Value())
	}
	h.values[KeyWiredID] = 0
	b := newKeyBolt(h)
	if b.on || b.in.Value() != 0 || b.Step(frame) {
		t.Fatalf("a bolt made with Key not wired starts at %v, or moves", b.in.Value())
	}
	for _, to := range []float32{1, 0} {
		h.values[KeyWiredID] = float64(to)
		last, frames := b.in.Value(), 0
		for b.Step(frame) {
			v := b.in.Value()
			if d := float32(math.Abs(float64(v - last))); d > 0.3 {
				t.Fatalf("on its way to %v the bolt jumps from %v to %v in a frame", to, last, v)
			}
			if last, frames = v, frames+1; frames > 240 {
				t.Fatalf("on its way to %v the bolt never rests", to)
			}
		}
		if frames < 5 || b.in.Value() != to {
			t.Errorf("the bolt came to %v in %d frames, want %v on a spring", b.in.Value(), frames, to)
		}
		if b.Step(frame) {
			t.Errorf("at rest at %v the bolt still steps", to)
		}
	}
	// A host that knows its wires is asked in place of the plugin playing, which may sleep.
	wh := &wiredHosted{host: h, wired: true}
	if b := newKeyBolt(wh); !b.on {
		t.Error("the bolt does not show for a host that says Key is wired")
	}
	h.values[KeyWiredID], wh.wired = 1, false
	if b := newKeyBolt(wh); b.on {
		t.Error("the bolt shows for a host that says Key is not wired")
	}
}

func TestKeysBoltFlashesOnEachHitWithoutAJump(t *testing.T) {
	const frame = time.Second / 120
	_, h := newDynamics(t)
	h.values[KeyWiredID], h.values[KeyHitsID] = 1, 41
	b := newKeyBolt(h)
	if b.Step(frame) || b.flash.Value() != 0 {
		t.Fatalf("the hits before the panel opened light the bolt to %v", b.flash.Value())
	}
	for hit := range 3 {
		h.values[KeyHitsID]++
		last, peak, frames := b.flash.Value(), float32(0), 0
		for b.Step(frame) {
			v := b.flash.Value()
			if d := float32(math.Abs(float64(v - last))); d > 0.5 {
				t.Fatalf("hit %d lights the bolt from %v to %v in a frame", hit, last, v)
			}
			peak = max(peak, v)
			if last, frames = v, frames+1; frames > 480 {
				t.Fatalf("after hit %d the bolt never rests", hit)
			}
			if hit == 1 && frames == 6 {
				// The next hit comes before this one's light is out.
				break
			}
		}
		if peak < 0.9 && hit != 1 {
			t.Errorf("hit %d lights the bolt to %v at most, want 0.9", hit, peak)
		}
		if hit == 2 && (b.flash.Value() != 0 || b.in.Value() != 1 || b.Step(frame)) {
			t.Errorf("after the last hit the bolt rests at light %v, faded in %v", b.flash.Value(), b.in.Value())
		}
	}
	// Not wired, a hit lights nothing.
	h.values[KeyWiredID] = 0
	for b.Step(frame) {
	}
	h.values[KeyHitsID]++
	if b.Step(frame) || b.flash.Value() != 0 {
		t.Errorf("with Key not wired a hit lights the bolt to %v", b.flash.Value())
	}
}

func TestKeysBoltStandsInTheHeadOfTheSidechainsSection(t *testing.T) {
	p, h := newDynamics(t)
	h.values[KeyWiredID] = 1
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	side := pn.sections[keySection]
	if side.Name != "SIDECHAIN" {
		t.Fatalf("Key's bolt stands by %q, not by the sidechain", side.Name)
	}
	for _, size := range []geom.Size{geom.Sz(1600, 860), geom.Sz(700, 600)} {
		b := boundsOf(t, pn, size, pn.key, side)
		head := geom.Rect{Min: b[1].Min, Max: geom.Pt(b[1].Max.X, b[1].Min.Y+plugui.SectionHead)}
		if b[0].Empty() || b[0].Min.X < (head.Min.X+head.Max.X)/2 || b[0].Max.X > head.Max.X || b[0].Min.Y < head.Min.Y || b[0].Max.Y > head.Max.Y {
			t.Errorf("in a panel of %v Key's bolt is at %v, not at the right of the section's head, %v", size, b[0], head)
		}
	}
	var _ interface{ FollowWires(*gunim.UI) } = pn
}
