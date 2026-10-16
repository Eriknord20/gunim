package plug_test

import (
	"math"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/tempo"
)

// position is a block's position as a host fills it from m: from frame
// f, n frames long, at rate.
func position(m *tempo.Map, f int64, n, rate int) plug.Position {
	return plug.Position{Frame: f, Playing: true, Beat: m.QuartersAtFrame(f, rate),
		Tempo: m.BPMAtFrame(f, rate), EndTempo: m.BPMReachingFrame(f+int64(n), rate)}
}

func TestTheBeatIsExactAtEveryFrameAcrossARamp(t *testing.T) {
	const rate = 48000
	// 60 to 180 over two seconds' worth of quarter notes, a step to 90,
	// and a ramp back up to 140.
	m := tempo.New(60, 4, 4)
	m.SetTempos([]tempo.Tempo{{At: 0, BPM: 60, Ramp: true}, {At: 4 * tempo.PPQ, BPM: 180},
		{At: 10 * tempo.PPQ, BPM: 90, Ramp: true}, {At: 14 * tempo.PPQ, BPM: 140}})
	edges := make([]int64, 0, len(m.Tempos()))
	// Each change's first frame at or after it.
	for _, c := range m.Tempos() {
		e := m.Frames(c.At, rate)
		if m.QuartersAtFrame(e, rate) < float64(c.At)/tempo.PPQ {
			e++
		}
		edges = append(edges, e)
	}
	end := m.Frames(18*tempo.PPQ, rate)
	for _, size := range []int{37, 1024} {
		worst := 0.0
		for f := int64(0); f < end; {
			// A host splits the block at each tempo change.
			n := int64(size)
			for _, e := range edges {
				if e > f && e < f+n {
					n = e - f
				}
			}
			pos := position(m, f, int(n), rate)
			for i := range n {
				want := m.QuartersAtFrame(f+i, rate)
				got := pos.BeatAt(int(i), int(n), rate)
				worst = max(worst, math.Abs(got-want))
				if math.Abs(got-want) > 1e-9 {
					t.Fatalf("blocks of %d: at frame %d the beat is %.12f, the map's %.12f", size, f+i, got, want)
				}
			}
			f += n
		}
		t.Logf("blocks of %d: worst %.3g quarter notes", size, worst)
	}
}

func TestABeatOfASteadyTempoHoldsWithoutAnEndTempo(t *testing.T) {
	pos := plug.Position{Tempo: 120, Beat: 3}
	if got := pos.BeatAt(24000, 48000, 48000); got != 4 {
		t.Errorf("half a second at 120 from beat 3 is %v, want 4", got)
	}
}

func TestABeatHoldsWhileTheSongStandsStill(t *testing.T) {
	pos := plug.Position{Tempo: 120, EndTempo: 120, Beat: 3, Still: true}
	for _, f := range []int{0, 100, 47999} {
		if got := pos.BeatAt(f, 48000, 48000); got != 3 {
			t.Errorf("standing still, frame %d is beat %v, want 3", f, got)
		}
	}
}
