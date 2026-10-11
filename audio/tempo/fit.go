package tempo

import "time"

// FitError is how far [Fit] lets a fitted map fall from a beat before
// it writes another tempo change: near enough that nobody hears it,
// loose enough that a track made to a grid fits in a single change.
const FitError = 5 * time.Millisecond

// FitRun is the most beats [Fit] holds under one tempo change, so a
// perfectly steady recording still gains a change now and then and a
// map stays searchable.
const FitRun = 512

// Fit returns a map whose beats fall on the times given, to within
// maxError, which is [FitError] when it is zero.
//
// The first time given is taken as the first downbeat: it becomes the
// map's start, and bar 1 beat 1. A detector that found beats before it
// should leave them off, or pass them and read them back at the
// negative positions they land on.
//
// A steady recording comes back as one tempo change. A recording that
// drifts comes back as a run of them, each covering as many beats as it
// can without any of them straying past maxError — so a live take keeps
// its bars over the music rather than sliding away from it over four
// minutes.
//
// Every beat is taken for a quarter note, whatever perBar says a bar
// holds, since that is the beat a detector finds.
func Fit(beats []time.Duration, perBar int, maxError time.Duration) *Map {
	if maxError <= 0 {
		maxError = FitError
	}
	m := New(DefaultBPM, perBar, 4)
	if len(beats) == 0 {
		return m
	}
	m.SetStart(beats[0])
	if len(beats) < 2 {
		return m
	}

	var tempos []Tempo
	for i := 0; i < len(beats)-1; {
		j := i + 1
		for j+1 < len(beats) && j+1-i <= FitRun && steady(beats[i:j+2], maxError) {
			j++
		}
		secs := float64(beats[j]-beats[i]) / float64(time.Second)
		tempos = append(tempos, Tempo{At: Ticks(i) * PPQ, BPM: 60 * float64(j-i) / secs})
		i = j
	}
	m.SetTempos(tempos)
	return m
}

// steady reports whether one tempo holds every beat of a run within
// maxError, the run's first and last beat setting that tempo.
func steady(run []time.Duration, maxError time.Duration) bool {
	n := len(run) - 1
	if n < 1 {
		return true
	}
	first, last := run[0], run[n]
	for k := 1; k < n; k++ {
		want := first + time.Duration(int64(last-first)*int64(k)/int64(n))
		if d := run[k] - want; d > maxError || -d > maxError {
			return false
		}
	}
	return true
}
