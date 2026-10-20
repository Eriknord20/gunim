package decibel

import (
	"math"
	"testing"
)

func TestOfIsLog10ToATenMillionthOfADecibel(t *testing.T) {
	worst := 0.0
	for v := 1e-9; v < 1e6; v *= 1.0001 {
		want := 20 * math.Log10(v)
		worst = max(worst, math.Abs(Of(v)-want))
	}
	if worst > 1e-7 {
		t.Errorf("off by up to %g dB", worst)
	}
	if got := Of(0); got != Of(1e-9) {
		t.Errorf("0 reads as %g dB, not as 1e-9 does, %g dB", got, Of(1e-9))
	}
	if got := Of(1); got != 0 {
		t.Errorf("1 is %g dB, not 0", got)
	}
}

func TestGainIsPowToAFewPartsInTenBillion(t *testing.T) {
	worst := 0.0
	for db := -400.0; db < 400; db += 0.0137 {
		want := math.Pow(10, db/20)
		worst = max(worst, math.Abs(Gain(db)/want-1))
	}
	if worst > 1e-9 {
		t.Errorf("off by up to %g of the gain", worst)
	}
	if got := Gain(0); got != 1 {
		t.Errorf("0 dB is %g, not 1", got)
	}
	if got := Gain(-1e9); got != 0 {
		t.Errorf("-1e9 dB is %g, not 0", got)
	}
	if got := Gain(1e9); math.IsInf(got, 0) || got != Gain(6000) {
		t.Errorf("1e9 dB is %g, not as 6000 dB is", got)
	}
}

// values returns a thousand values from from, step apart, and room for
// what is made of them: the benchmarks below work them out none waiting
// on the last, as a plugin's samples are.
func values(from, step float64) (in, out []float64) {
	in, out = make([]float64, 1024), make([]float64, 1024)
	for i := range in {
		in[i] = from + step*float64(i)
	}
	return in, out
}

func BenchmarkOfMany(b *testing.B) {
	in, out := values(0.001, 0.001)
	for b.Loop() {
		for i, v := range in {
			out[i] = Of(v)
		}
	}
}

func BenchmarkLog10Many(b *testing.B) {
	in, out := values(0.001, 0.001)
	for b.Loop() {
		for i, v := range in {
			out[i] = 20 * math.Log10(v)
		}
	}
}

func BenchmarkGainMany(b *testing.B) {
	in, out := values(0, -0.05)
	for b.Loop() {
		for i, v := range in {
			out[i] = Gain(v)
		}
	}
}

func BenchmarkPowMany(b *testing.B) {
	in, out := values(0, -0.05)
	for b.Loop() {
		for i, v := range in {
			out[i] = math.Pow(10, v/20)
		}
	}
}

func BenchmarkOf(b *testing.B) {
	v, s := 0.001, 0.0
	for b.Loop() {
		s += Of(v)
		v *= 1.0000001
	}
	_ = s
}

func BenchmarkLog10(b *testing.B) {
	v, s := 0.001, 0.0
	for b.Loop() {
		s += 20 * math.Log10(v)
		v *= 1.0000001
	}
	_ = s
}

func BenchmarkGain(b *testing.B) {
	v, s := -20.0, 0.0
	for b.Loop() {
		s += Gain(v)
		v += 1e-6
	}
	_ = s
}

func BenchmarkPow(b *testing.B) {
	v, s := -20.0, 0.0
	for b.Loop() {
		s += math.Pow(10, v/20)
		v += 1e-6
	}
	_ = s
}
