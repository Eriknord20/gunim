package tempo

import (
	"math"
	"testing"
	"time"
)

// track makes beat times from a tempo that may change along the way:
// beat k follows beat k-1 by a beat at bpm(k).
func track(lead time.Duration, n int, bpm func(k int) float64) []time.Duration {
	out := make([]time.Duration, n)
	at := lead
	for k := range n {
		out[k] = at
		at += time.Duration(60 / bpm(k) * float64(time.Second))
	}
	return out
}

// worst is the furthest any beat falls from where the map puts it.
func worst(m *Map, beats []time.Duration) time.Duration {
	var out time.Duration
	for k, want := range beats {
		d := m.Time(Ticks(k)*PPQ) - want
		if d < 0 {
			d = -d
		}
		out = max(out, d)
	}
	return out
}

// TestASteadyTrackFitsInOneTempo, since a recording made to a grid
// needs nothing else said about it.
func TestASteadyTrackFitsInOneTempo(t *testing.T) {
	beats := track(2*time.Second, 64, func(int) float64 { return 128 })
	m := Fit(beats, 4, 0)

	if ts := m.Tempos(); len(ts) != 1 {
		t.Errorf("a steady track fitted %d tempo changes, want one", len(ts))
	}
	if bpm := m.BPM(0); math.Abs(bpm-128) > 0.01 {
		t.Errorf("the fitted tempo is %v, want 128", bpm)
	}
	if m.Start() != beats[0] {
		t.Errorf("the map starts at %v, want the first beat at %v", m.Start(), beats[0])
	}
	if d := worst(m, beats); d > time.Millisecond {
		t.Errorf("a beat falls %v from where the map puts it, want under a millisecond", d)
	}
	// The first beat given is the first downbeat, so four beats on is
	// bar 2.
	if got, want := m.Pos(4*PPQ), (Pos{Bar: 2, Beat: 1}); got != want {
		t.Errorf("four beats in is %v, want %v", got, want)
	}
}

// TestATrackThatDriftsKeepsItsBarsOverTheMusic: a live take speeding up
// gains tempo changes rather than sliding away from the beat.
func TestATrackThatDriftsKeepsItsBarsOverTheMusic(t *testing.T) {
	const n = 240
	beats := track(0, n, func(k int) float64 { return 120 + 6*float64(k)/n })
	m := Fit(beats, 4, FitError)

	if ts := m.Tempos(); len(ts) < 2 {
		t.Errorf("a drifting track fitted %d tempo changes, want more than one", len(ts))
	}
	if d := worst(m, beats); d > FitError {
		t.Errorf("a beat falls %v from where the map puts it, want no more than %v", d, FitError)
	}
	// The tempo at the end is where the music got to, not where it
	// started.
	if bpm := m.BPM(Ticks(n-2) * PPQ); math.Abs(bpm-126) > 1 {
		t.Errorf("the tempo at the end is %v, want about 126", bpm)
	}
	// Fitting it tighter follows the music more closely.
	tight := Fit(beats, 4, time.Millisecond)
	if d := worst(tight, beats); d > time.Millisecond {
		t.Errorf("fitted to a millisecond, a beat is still %v out", d)
	}
	if len(tight.Tempos()) <= len(m.Tempos()) {
		t.Error("a tighter fit used no more tempo changes than a looser one")
	}
}

// TestATrackThatChangesTempoOutrightIsFollowed, as a half-time section
// does.
func TestATrackThatChangesTempoOutrightIsFollowed(t *testing.T) {
	beats := track(0, 64, func(k int) float64 {
		if k >= 32 {
			return 90
		}
		return 128
	})
	m := Fit(beats, 4, FitError)
	if d := worst(m, beats); d > FitError {
		t.Errorf("a beat falls %v from where the map puts it, want no more than %v", d, FitError)
	}
	if bpm := m.BPM(40 * PPQ); math.Abs(bpm-90) > 1 {
		t.Errorf("the tempo after the change is %v, want about 90", bpm)
	}
}

// TestFittingTooFewBeatsStillGivesAUsableMap, since a detector that
// found nothing should not leave the application holding nothing.
func TestFittingTooFewBeatsStillGivesAUsableMap(t *testing.T) {
	for _, beats := range [][]time.Duration{nil, {3 * time.Second}} {
		m := Fit(beats, 4, 0)
		if m.BPM(0) != DefaultBPM {
			t.Errorf("fitting %v gave a map at %v, want %v", beats, m.BPM(0), DefaultBPM)
		}
		if len(beats) == 1 && m.Start() != beats[0] {
			t.Errorf("fitting one beat started the map at %v, want %v", m.Start(), beats[0])
		}
	}
}
