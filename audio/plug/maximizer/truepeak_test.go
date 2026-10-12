package maximizer

import (
	"math"
	"testing"
)

func TestTheTruePeakFindsPeaksBetweenTheSamples(t *testing.T) {
	// A sine at a quarter of the rate, its samples where it is at 45°:
	// every sample at 0.707 of its peak, which lies between them.
	var tp truePeak
	most := 0.0
	for i := range 2000 {
		v := math.Sin(2*math.Pi*float64(i)/4 + math.Pi/4)
		most = max(most, tp.push(v))
	}
	if math.Abs(20*math.Log10(most)) > 0.3 {
		t.Errorf("samples at 0.707 of a sine of 1 read a true peak of %.3f, want 1", most)
	}
	// A low sine reads as its samples do.
	tp.reset()
	most = 0
	for i := range 4800 {
		most = max(most, tp.push(0.5*math.Sin(2*math.Pi*100*float64(i)/48000)))
	}
	if math.Abs(most-0.5) > 0.01 {
		t.Errorf("a low sine of 0.5 reads a true peak of %.3f", most)
	}
}
