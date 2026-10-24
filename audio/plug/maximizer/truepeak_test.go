package maximizer

import (
	"math"
	"testing"
)

func TestTheTruePeakFindsPeaksBetweenTheSamples(t *testing.T) {
	// A sine at a quarter of the rate, its samples where it is at 45°:
	// every sample at 0.707 of its peak, which lies between them. Read
	// once the filter is full of it.
	var tp truePeak
	most := 0.0
	for i := range 2000 {
		v := math.Sin(2*math.Pi*float64(i)/4 + math.Pi/4)
		if p := tp.push(v); i >= tpTaps {
			most = max(most, p)
		}
	}
	if math.Abs(20*math.Log10(most)) > 0.01 {
		t.Errorf("samples at 0.707 of a sine of 1 read a true peak of %.4f, want 1", most)
	}
	// A low sine reads as its samples do.
	tp.reset()
	most = 0
	for i := range 4800 {
		most = max(most, tp.push(0.5*math.Sin(2*math.Pi*100*float64(i)/48000)))
	}
	if math.Abs(most-0.5) > 0.001 {
		t.Errorf("a low sine of 0.5 reads a true peak of %.4f", most)
	}
}

func TestTheTruePeakReadsHighTonesBetweenTheSamples(t *testing.T) {
	// Tones up to 20 kHz, peaking half a frame from a sample, read once the
	// filter is full of them.
	for _, f := range []float64{1000, 8000, 12000, 16000, 18000, 20000} {
		w := 2 * math.Pi * f / 48000
		var tp truePeak
		most := 0.0
		for i := range 4 * tpTaps {
			if p := tp.push(math.Cos(w * (float64(i) - 0.5))); i >= tpTaps {
				most = max(most, p)
			}
		}
		if db := 20 * math.Log10(most); math.Abs(db) > 0.01 {
			t.Errorf("a %.0f Hz tone of full scale reads %.4f dBTP", f, db)
		}
	}
}

func TestTheTruePeakIsTheFiltersEveryPhase(t *testing.T) {
	// Phase 0 is the middle sample, and each other phase is its mirror's
	// backwards, as push takes it to be.
	for k := range tpTaps {
		want := 0.0
		if k == tpTaps/2-1 {
			want = 1
		}
		if math.Abs(tpFilter[0][k]-want) > 1e-15 {
			t.Fatalf("phase 0, tap %d: %g, want %g", k, tpFilter[0][k], want)
		}
	}
	for p := 1; p < tpPhases; p++ {
		for k := range tpTaps {
			a, b := tpFilter[p][k], tpFilter[tpPhases-p][tpTaps-1-k]
			if math.Abs(a-b) > 1e-15 {
				t.Fatalf("phase %d, tap %d: %g, its mirror %g", p, k, a, b)
			}
		}
	}
	// And push finds the loudest of the phases' sums, written out.
	var tp truePeak
	w := make([]float64, 0, 3000)
	for i := range 3000 {
		v := math.Sin(float64(i)*0.37) * math.Cos(float64(i)*0.011)
		w = append(w, v)
		got := tp.push(v)
		want := 0.0
		for p := range tpPhases {
			y := 0.0
			for k := range tpTaps {
				if j := len(w) - tpTaps + k; j >= 0 {
					y += tpFilter[p][k] * w[j]
				}
			}
			want = max(want, math.Abs(y))
		}
		if math.Abs(got-want) > 1e-14 {
			t.Fatalf("sample %d: %g, not %g", i, got, want)
		}
	}
}
