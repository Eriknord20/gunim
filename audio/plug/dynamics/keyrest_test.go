package dynamics

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"runtime"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
)

// halfSine returns n frames of a 220 Hz sine that peaks at 0.5, from frame on.
func halfSine(frame int64, n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(0.5 * math.Sin(2*math.Pi*220*float64(frame+int64(i))/48000))
	}
	return s
}

// played is what a block of the sine gave: the sound out, and the gate's and the compressor's reductions.
type played struct {
	in, out, gate, comp []float32
}

// play runs a block of n frames of the sine through pr at frame, with hits on Key, or Key not wired where hits is nil.
func play(pr plug.Processor, frame int64, n int, hits []float32, changes []plug.Change) played {
	r := played{in: halfSine(frame, n), out: make([]float32, n), gate: make([]float32, n), comp: make([]float32, n)}
	b := &plug.Block{
		In: [][]float32{r.in, r.in}, Out: [][]float32{r.out, make([]float32, n)}, Frames: n,
		Position:   plug.Position{Frame: frame},
		ControlOut: [][]float32{r.gate, r.comp, make([]float32, n)},
		Changes:    changes,
	}
	if hits != nil {
		b.ControlIn = [][]float32{hits}
	}
	pr.Process(b)
	return r
}

// prepared returns the plugin's processor, prepared for blocks of 512 at 48 kHz.
func prepared(t *testing.T) *processor {
	t.Helper()
	p, _ := newDynamics(t)
	p.proc.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	return &p.proc
}

func TestAfterAResetAKeyedGateIsShutFromTheFirstFrame(t *testing.T) {
	const block, blocks = 512, 10
	shut := gainOf(-40)
	// check plays 100 ms with no hit and holds every frame at the gate's range.
	check := func(name string, pr *processor, changes []plug.Change) {
		var last []float32
		for i := range blocks {
			r := play(pr, int64(i*block), block, make([]float32, block), changes)
			changes = nil
			for f, v := range r.out {
				// A frame comes out the lookahead after it went in.
				want, at := 0.0, f-pr.Latency()
				if at >= 0 {
					want = float64(r.in[at]) * shut
				} else if last != nil {
					want = float64(last[block+at]) * shut
				}
				if math.Abs(float64(v)-want) > 1e-6 {
					t.Fatalf("%s: frame %d comes out %v, not %v, the sine at the gate's range", name, i*block+f, v, want)
				}
				if i*block+f >= pr.Latency() && (r.gate[f] != -40 || r.comp[f] != 0) {
					t.Fatalf("%s: on frame %d the gate is at %v dB and the compressor at %v dB, not -40 and 0", name,
						i*block+f, r.gate[f], r.comp[f])
				}
			}
			last = r.in
		}
	}
	// The first block after Prepare carries the gate's settings, as a host sends them.
	pr := prepared(t)
	check("after Prepare", pr, gating())
	// A hit opens the gate and compresses, and the reset comes in the middle of both.
	play(pr, blocks*block, block, hitsAt(block, 10, 1), nil)
	if pr.gate.gain > -1 && pr.comp.gr < -1 {
		pr.Reset()
		check("after Reset", pr, nil)
	} else {
		t.Fatalf("the hit left the gate at %v dB and the compressor at %v dB, not open and at work", pr.gate.gain, pr.comp.gr)
	}
}

func TestAfterAResetAGateThatIsOffOrNotKeyedStartsOpen(t *testing.T) {
	// Keyed, with the gate off, the sound passes whole.
	pr := prepared(t)
	r := play(pr, 0, 512, make([]float32, 512), append(gating(), set(float64(GateOnID), 0)...))
	for f := pr.Latency(); f < 512; f++ {
		if r.out[f] != r.in[f-pr.Latency()] || r.gate[f] != 0 {
			t.Fatalf("keyed with the gate off, frame %d comes out %v with the gate at %v dB, not %v at 0", f, r.out[f],
				r.gate[f], r.in[f-pr.Latency()])
		}
	}
	// Not keyed, the gate starts open and shuts over its release, as the sound it hears is under its threshold.
	pr = prepared(t)
	r = play(pr, 0, 512, nil, append(gating(), set(float64(GateThresholdID), 0)...))
	if f := pr.Latency(); r.gate[f] < -1 || r.gate[511] >= r.gate[f] {
		t.Errorf("not keyed, the gate starts at %v dB and comes to %v dB, not open and on its way shut", r.gate[f], r.gate[511])
	}
}

// resetHash runs loud sound through every part with Key not wired, resets in the middle of it, and returns a hash of
// every frame out and of the gate's and compressor's reductions from the reset on.
func resetHash(t *testing.T) string {
	t.Helper()
	const block = 512
	pr := prepared(t)
	changes := set(float64(GateOnID), 1, float64(GateThresholdID), -24, float64(CompThresholdID), -30,
		float64(CompRatioID), 6, float64(InputID), 6)
	in := [2][]float32{loud(40*block, 1), loud(40*block, 2)}
	sum := sha256.New()
	var four [4]byte
	for i := range 40 {
		if i == 20 {
			pr.Reset()
		}
		out := [][]float32{make([]float32, block), make([]float32, block)}
		ctl := [][]float32{make([]float32, block), make([]float32, block), make([]float32, block)}
		pr.Process(&plug.Block{
			In: [][]float32{in[0][i*block:][:block], in[1][i*block:][:block]}, Out: out, Frames: block,
			Position: plug.Position{Frame: int64(i * block)}, ControlOut: ctl, Changes: changes,
		})
		changes = nil
		if i < 20 {
			continue
		}
		for f := range block {
			for _, v := range []float32{out[0][f], out[1][f], ctl[GateOut][f], ctl[CompOut][f], ctl[LimitOut][f]} {
				binary.LittleEndian.PutUint32(four[:], math.Float32bits(v))
				sum.Write(four[:])
			}
		}
	}
	if pr.Tail() != 0 {
		t.Errorf("not wired, the tail is %d", pr.Tail())
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// resetGolden is resetHash of the plugin as it was before a keyed gate rested shut, on amd64.
const resetGolden = "c305536b915c23576e1dcb588c05d545bbadf45d0768042ea7225bf08e287fbe"

func TestWithKeyNotWiredTheOutputAfterAResetIsWhatItWasToTheBit(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the hash is of amd64's arithmetic")
	}
	if got := resetHash(t); got != resetGolden {
		t.Errorf("after a reset the output hashes to %s, not %s as before", got, resetGolden)
	}
}

func TestWithKeyWiredTheTailIsEndlessUntilTheGateIsShutAndTheCompressorAtRest(t *testing.T) {
	const block = 512
	for name, c := range map[string]struct {
		release  float64
		gate, on float64
	}{
		"a release of 20 ms":                     {20, 1, 1},
		"a release of 2 s":                       {2000, 1, 1},
		"a release of 2 s, the gate off":         {2000, 0, 1},
		"a release of 2 s, the compressor off":   {2000, 1, 0},
		"a release of 20 ms, the gate off":       {20, 0, 1},
		"a release of 20 ms, the compressor off": {20, 1, 0},
	} {
		pr := prepared(t)
		changes := append(gating(), set(float64(GateReleaseID), c.release, float64(CompReleaseID), c.release,
			float64(GateOnID), c.gate, float64(CompOnID), c.on)...)
		play(pr, 0, block, make([]float32, block), changes)
		if pr.Tail() != 0 {
			t.Fatalf("%s: at rest before any hit the tail is %d, not 0", name, pr.Tail())
		}
		play(pr, block, block, hitsAt(block, 100, 1), nil)
		if c.gate == 1 && pr.gate.gain < -1 || c.on == 1 && pr.comp.gr > -1 {
			t.Fatalf("%s: the hit left the gate at %v dB and the compressor at %v dB", name, pr.gate.gain, pr.comp.gr)
		}
		// Thirty times the release is the most the last billionth of a decibel takes.
		most := 2 + int(30*c.release/1000*48000)/block
		ran := 2
		for ; pr.Tail() != 0; ran++ {
			if pr.Tail() != math.MaxInt32 {
				t.Fatalf("%s: the tail is %d, neither none nor endless", name, pr.Tail())
			}
			if ran > most {
				t.Fatalf("%s: the tail never ends: the gate is at %v dB and the compressor at %v dB", name, pr.gate.gain, pr.comp.gr)
			}
			play(pr, int64(ran*block), block, make([]float32, block), nil)
		}
		// Where the tail ends, the sleep's wake moves nothing that can be heard.
		rest := -40 * c.gate
		if d := math.Abs(pr.gate.gain - rest); d > 1e-9 || pr.comp.gr != 0 || pr.hits.running() {
			t.Errorf("%s: the tail ends after %d blocks with the gate %v dB from its rest and the compressor at %v dB", name,
				ran, d, pr.comp.gr)
		}
		if took := float64((ran-2)*block) / 48; took < c.release {
			t.Errorf("%s: the tail ends %v ms after the hit, before one release is over", name, took)
		}
		// A host that sleeps the device here wakes it to the same gains.
		gate, comp := pr.gate.gain, pr.comp.gr
		r := play(pr, int64(ran*block)+96000, block, make([]float32, block), nil)
		if math.Abs(pr.gate.gain-gate) > 1e-9 || pr.comp.gr != comp || pr.Tail() != 0 {
			t.Errorf("%s: a sleep after the tail's end moves the gate from %v to %v dB", name, gate, pr.gate.gain)
		}
		for f := 1; f < block; f++ {
			if r.gate[f] != r.gate[0] || r.comp[f] != 0 {
				t.Fatalf("%s: on frame %d after the sleep the gate goes from %v to %v dB", name, f, r.gate[0], r.gate[f])
			}
		}
	}
}

func TestAHitAfterRestOpensTheGateOnItsOwnFrame(t *testing.T) {
	const block, at = 512, 3*512 + 77
	pr := prepared(t)
	changes := gating()
	// hit plays four blocks from rest, with a hit in the last, and holds the gate shut up to the hit's own frame.
	hit := func(name string, from int64) {
		var gate, out []float32
		for i := range 4 {
			hits := make([]float32, block)
			if i == 3 {
				hits[at-3*block] = 1
			}
			r := play(pr, from+int64(i*block), block, hits, changes)
			changes = nil
			gate, out = append(gate, r.gate...), append(out, r.out...)
		}
		// A frame's reduction comes out with its sound, the lookahead later.
		opens := at + pr.Latency()
		for f := pr.Latency(); f < opens; f++ {
			if gate[f] != -40 {
				t.Fatalf("%s: on frame %d, before the hit on %d, the gate is at %v dB, not shut", name, f-pr.Latency(), at, gate[f])
			}
		}
		if gate[opens] <= -40 {
			t.Errorf("%s: on the hit's own frame the gate is still at %v dB", name, gate[opens])
		}
		peak := float32(0)
		for _, v := range out[opens:] {
			peak = max(peak, float32(math.Abs(float64(v))))
		}
		if peak < 0.05 {
			t.Errorf("%s: after the hit the sound peaks at %v, so the gate did not open", name, peak)
		}
	}
	hit("after Prepare", 0)
	pr.Reset()
	hit("after Reset", 48000)
	// On to the tail's end, and a sleep after it.
	frame := int64(48000 + 4*block)
	for ; pr.Tail() != 0; frame += block {
		play(pr, frame, block, make([]float32, block), nil)
	}
	hit("after a sleep", frame+96000)
}
