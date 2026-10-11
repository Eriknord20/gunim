package tempo

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// near reports whether two durations are within tol of one another.
func near(a, b, tol time.Duration) bool { return a-b < tol && b-a < tol }

// TestTheZeroMapIsASteadyFourFour: a map nobody filled in still answers,
// so a recording with no tempo set reads as 120 in four four.
func TestTheZeroMapIsASteadyFourFour(t *testing.T) {
	var m Map
	if got := m.BPM(0); got != DefaultBPM {
		t.Errorf("the zero map runs at %v, want %v", got, DefaultBPM)
	}
	if got, want := m.Meter(0), (Meter{Beats: 4, Unit: 4}); got != want {
		t.Errorf("the zero map is in %v, want %v", got, want)
	}
	// Four quarter notes at 120 take two seconds.
	if got := m.Time(4 * PPQ); !near(got, 2*time.Second, time.Microsecond) {
		t.Errorf("bar 2 falls at %v, want 2s", got)
	}
	if got, want := m.Pos(4*PPQ), (Pos{Bar: 2, Beat: 1}); got != want {
		t.Errorf("four quarters in is %v, want %v", got, want)
	}
}

// TestTimeWalksEveryTempoChange adds up the segments by hand and finds
// the map agrees.
func TestTimeWalksEveryTempoChange(t *testing.T) {
	m := New(120, 4, 4)
	m.SetTempo(Tempo{At: 8 * PPQ, BPM: 140})
	m.SetTempo(Tempo{At: 16 * PPQ, BPM: 60})

	// Eight quarters at 120 is 4s; eight more at 140 is 480/140 s; then
	// four at 60 is 4s.
	middle := float64(480) / 140
	want := 8*time.Second + time.Duration(middle*float64(time.Second))
	if got := m.Time(20 * PPQ); !near(got, want, time.Millisecond) {
		t.Errorf("twenty quarters in falls at %v, want %v", got, want)
	}
	if got := m.BPM(12 * PPQ); got != 140 {
		t.Errorf("the tempo at quarter 12 is %v, want 140", got)
	}
	if got := m.BPM(8*PPQ - 1); got != 120 {
		t.Errorf("the tempo just before the change is %v, want 120", got)
	}
}

// TestARampSlidesEvenlyInTime: a ramp from 120 to 180 over eight
// quarters lasts the quarters over the mean of the two tempos, and is
// half way up at half way through.
func TestARampSlidesEvenlyInTime(t *testing.T) {
	m := New(120, 4, 4)
	m.SetTempo(Tempo{At: 0, BPM: 120, Ramp: true})
	m.SetTempo(Tempo{At: 8 * PPQ, BPM: 180})

	secs := float64(120*8) / (120 + 180)
	want := time.Duration(secs * float64(time.Second))
	got := m.Time(8 * PPQ)
	if !near(got, want, time.Millisecond) {
		t.Fatalf("the ramp lasts %v, want %v", got, want)
	}
	// Half way through in time, the tempo is half way between the two.
	half := m.At(want / 2)
	if bpm := m.BPM(half); math.Abs(bpm-150) > 0.5 {
		t.Errorf("half way through the ramp the tempo is %v, want 150", bpm)
	}
	// The first half of a ramp is the slower half, so it covers fewer
	// than half the ramp's beats: 3.6 of the eight quarters here.
	if half >= 4*PPQ || half <= 3*PPQ {
		t.Errorf("half the ramp's time covered %v ticks, want between three and four quarters", half)
	}
}

// TestTimeAndPositionAreEachOthersUndoing walks a map of changes, ramps
// and meters, and finds every position comes back from its own time.
func TestTimeAndPositionAreEachOthersUndoing(t *testing.T) {
	m := New(96, 4, 4)
	m.SetStart(1500 * time.Millisecond)
	m.SetTempo(Tempo{At: 4 * PPQ, BPM: 96, Ramp: true})
	m.SetTempo(Tempo{At: 12 * PPQ, BPM: 174})
	m.SetTempo(Tempo{At: 40 * PPQ, BPM: 70})
	m.SetMeter(Meter{At: 16 * PPQ, Beats: 7, Unit: 8})

	for want := Ticks(-2 * PPQ); want < 64*PPQ; want += PPQ / 7 {
		if got := m.At(m.Time(want)); got < want-2 || got > want+2 {
			t.Fatalf("tick %d came back as %d, by way of %v", want, got, m.Time(want))
		}
		p := m.Pos(want)
		if got := m.Ticks(p); got < want-2 || got > want+2 {
			t.Fatalf("tick %d reads as %v, which is tick %d", want, p, got)
		}
	}
}

// TestTimeRisesWithThePosition: whatever the changes, later music never
// falls earlier, which is what a playhead and a ruler both lean on.
func TestTimeRisesWithThePosition(t *testing.T) {
	m := New(130, 4, 4)
	m.SetTempo(Tempo{At: 2 * PPQ, BPM: 130, Ramp: true})
	m.SetTempo(Tempo{At: 10 * PPQ, BPM: 65})
	m.SetTempo(Tempo{At: 11 * PPQ, BPM: 200})
	last := time.Duration(math.MinInt64)
	for t2 := Ticks(0); t2 < 20*PPQ; t2 += 17 {
		now := m.Time(t2)
		if now < last {
			t.Fatalf("tick %d falls at %v, before the tick before it at %v", t2, now, last)
		}
		last = now
	}
}

// TestAMeterChangeStartsItsOwnBar, cutting short the bar it lands in
// rather than leaving the bar lines and the beat lines disagreeing.
func TestAMeterChangeStartsItsOwnBar(t *testing.T) {
	m := New(120, 4, 4)
	m.SetMeter(Meter{At: 6 * PPQ, Beats: 6, Unit: 8})

	for _, c := range []struct {
		at   Ticks
		want Pos
	}{
		{0, Pos{Bar: 1, Beat: 1}},
		{PPQ, Pos{Bar: 1, Beat: 2}},
		{4 * PPQ, Pos{Bar: 2, Beat: 1}},
		// The change falls half way through bar 2, which is cut short.
		{6 * PPQ, Pos{Bar: 3, Beat: 1}},
		{6*PPQ + PPQ/2, Pos{Bar: 3, Beat: 2}},
		// Six eighths is three quarters to the bar.
		{9 * PPQ, Pos{Bar: 4, Beat: 1}},
	} {
		if got := m.Pos(c.at); got != c.want {
			t.Errorf("tick %d is %v, want %v", c.at, got, c.want)
		}
	}
}

// TestLinesAreABeatEachAndMarkTheBars.
func TestLinesAreABeatEachAndMarkTheBars(t *testing.T) {
	m := New(120, 4, 4)
	m.SetMeter(Meter{At: 8 * PPQ, Beats: 3, Unit: 4})

	lines := m.Lines(0, 14*PPQ)
	// Eight quarters of four four, then six of three four.
	if len(lines) != 14 {
		t.Fatalf("a fourteen quarter range drew %d lines, want 14", len(lines))
	}
	var bars []Ticks
	for _, l := range lines {
		if l.Pos.Beat == 1 {
			bars = append(bars, l.At)
		}
	}
	want := []Ticks{0, 4 * PPQ, 8 * PPQ, 11 * PPQ}
	if len(bars) != len(want) {
		t.Fatalf("the bars fall at %v, want %v", bars, want)
	}
	for i := range want {
		if bars[i] != want[i] {
			t.Fatalf("the bars fall at %v, want %v", bars, want)
		}
	}
	// A range starting part way through begins at the next whole beat.
	if got := m.Lines(PPQ/2, 3*PPQ); len(got) == 0 || got[0].At != PPQ {
		t.Errorf("a range from half a beat in starts at %v, want one beat in", got[0].At)
	}
}

// TestChangesAreTidied: what the map holds is sorted, in bounds, and
// has a change at the start, whatever it was handed.
func TestChangesAreTidied(t *testing.T) {
	var m Map
	m.SetTempos([]Tempo{
		{At: 8 * PPQ, BPM: 150},
		{At: 2 * PPQ, BPM: 0},     // too slow to mean anything
		{At: 8 * PPQ, BPM: 160},   // replaces the one before it
		{At: -5 * PPQ, BPM: 9999}, // before the start, and too fast
	})
	ts := m.Tempos()
	if len(ts) != 3 {
		t.Fatalf("the map holds %v, want three changes", ts)
	}
	if ts[0].At != 0 || ts[0].BPM != MaxBPM {
		t.Errorf("the first change is %v, want one at the start at %v", ts[0], MaxBPM)
	}
	if ts[1].BPM != MinBPM {
		t.Errorf("a tempo of zero came out as %v, want %v", ts[1].BPM, MinBPM)
	}
	if ts[2].BPM != 160 {
		t.Errorf("two changes at one position left %v, want the later one", ts[2].BPM)
	}

	// The change at the start cannot be taken away, since something has
	// to say what the music does before the first change made.
	if m.RemoveTempo(0) {
		t.Error("the change at the start was removed")
	}
	if !m.RemoveTempo(8 * PPQ) {
		t.Error("a change that is there was not removed")
	}
	if m.RemoveTempo(8 * PPQ) {
		t.Error("a change that is not there was removed")
	}
}

// TestAMapIsWrittenAndReadBack, so an application keeps a recording's
// musical time in its own file.
func TestAMapIsWrittenAndReadBack(t *testing.T) {
	m := New(174, 7, 8)
	m.SetStart(1234 * time.Millisecond)
	m.SetTempo(Tempo{At: 14 * PPQ, BPM: 87, Ramp: true})
	m.SetTempo(Tempo{At: 28 * PPQ, BPM: 174})
	m.SetMeter(Meter{At: 28 * PPQ, Beats: 4, Unit: 4})

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Map
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Start() != m.Start() {
		t.Errorf("the start came back as %v, want %v", back.Start(), m.Start())
	}
	for at := Ticks(0); at < 40*PPQ; at += PPQ / 3 {
		if a, b := m.Time(at), back.Time(at); a != b {
			t.Fatalf("tick %d falls at %v, and at %v once read back", at, a, b)
		}
		if a, b := m.Pos(at), back.Pos(at); a != b {
			t.Fatalf("tick %d is %v, and %v once read back", at, a, b)
		}
	}
	// A file with nothing in it reads as the default, not as a broken
	// map.
	var empty Map
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.BPM(0) != DefaultBPM {
		t.Errorf("an empty map reads as %v, want %v", empty.BPM(0), DefaultBPM)
	}
}
