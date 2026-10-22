package tempo

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// near reports whether two durations are within tol of one another.
func near(a, b, tol time.Duration) bool { return a-b < tol && b-a < tol }

// TestTupletsLandOnWholeTicks: a quarter note splits into two to eight
// equal notes with no tick left over, so a septuplet snaps exactly.
func TestTupletsLandOnWholeTicks(t *testing.T) {
	for n := Ticks(2); n <= 8; n++ {
		if PPQ%n != 0 {
			t.Errorf("a quarter note of %d ticks leaves %d over in %d parts", PPQ, PPQ%n, n)
		}
	}
}

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

// TestLinesAreABeatEachAndMarkTheBars: a ruler asks for the beats in
// a stretch and is told which of them start a bar.
// TestAppendLinesAppendsWhatLinesReturns appends the lines of a map
// with a meter change to a slice holding one already: they follow it,
// as Lines returns them, and a slice with room enough takes them with
// no allocation.
func TestAppendLinesAppendsWhatLinesReturns(t *testing.T) {
	m := New(120, 4, 4)
	m.SetMeter(Meter{At: 4 * PPQ, Beats: 3, Unit: 4})
	want := m.Lines(-PPQ, 12*PPQ)
	first := Line{At: -1}
	got := m.AppendLines([]Line{first}, -PPQ, 12*PPQ)
	if len(got) != len(want)+1 || got[0] != first || !slices.Equal(got[1:], want) {
		t.Fatalf("AppendLines gave %v, want %v after %v", got, want, first)
	}
	buf := make([]Line, 0, len(want))
	if allocs := testing.AllocsPerRun(100, func() { buf = m.AppendLines(buf[:0], -PPQ, 12*PPQ) }); allocs != 0 {
		t.Errorf("AppendLines into a slice with room allocates %v times", allocs)
	}
}

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

// TestBeforeTheStartIsSteadyAtTheFirstTempo: a lead-in before tick 0
// runs at the first tempo even where that tempo ramps, and the ruler
// draws its beats too.
func TestBeforeTheStartIsSteadyAtTheFirstTempo(t *testing.T) {
	m := New(120, 4, 4)
	m.SetTempos([]Tempo{{BPM: 60, Ramp: true}, {At: Quarters(8), BPM: 180}})
	// Four quarters back at 60 is four seconds back.
	if got := m.Time(Quarters(-4)); got != -4*time.Second {
		t.Errorf("four quarters before the start fall at %v, want -4s", got)
	}
	if got := m.At(-2 * time.Second); got != Quarters(-2) {
		t.Errorf("two seconds before the start is %v ticks, want %v", got, Quarters(-2))
	}
	lines := m.Lines(Quarters(-4), Quarters(1))
	if len(lines) != 5 || lines[0].At != Quarters(-4) || lines[0].Pos != (Pos{Bar: 0, Beat: 1}) {
		t.Errorf("the lead-in's lines are %+v, want five from bar 0 beat 1", lines)
	}
}

// TestAGentleRampStaysExact: the closer a ramp's two tempos, the closer
// it must come to the steady tempo, with no rounding blowing up as the
// rise nears nothing.
func TestAGentleRampStaysExact(t *testing.T) {
	m := New(120, 4, 4)
	m.SetTempos([]Tempo{{BPM: 120, Ramp: true}, {At: Quarters(1000), BPM: 120.000001}})
	if got, want := m.Time(Quarters(500)), 250*time.Second; (got - want).Abs() > time.Millisecond {
		t.Errorf("500 quarters up a ramp of almost nothing fall at %v, want %v", got, want)
	}
}

// TestReadingAZeroMapFromManyGoroutines: a reading never writes, so a
// zero Map shared between goroutines is safe. The race detector finds
// it if not.
func TestReadingAZeroMapFromManyGoroutines(t *testing.T) {
	var m Map
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_ = m.Time(Quarters(4))
			_ = m.Pos(Quarters(5))
			_ = m.Lines(0, Quarters(8))
		})
	}
	wg.Wait()
}

// TestAMapHeldByValueIsWritten: a struct holding a Map, not a pointer
// to one, writes it as a map.
func TestAMapHeldByValueIsWritten(t *testing.T) {
	held := struct{ Map Map }{Map: *New(128, 3, 4)}
	data, err := json.Marshal(held)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"BPM":128`) {
		t.Errorf("a map held by value was written as %s", data)
	}
}

// TestAMapWrittenWithLowerCaseKeysReads: a file written before the
// keys took the fields' names, as "tempos" and "bpm", reads the same.
func TestAMapWrittenWithLowerCaseKeysReads(t *testing.T) {
	old := `{"startMs":500,"tempos":[{"at":0,"bpm":90},{"at":13440,"bpm":140,"ramp":true}],"meters":[{"at":0,"beats":3,"unit":4}]}`
	var m Map
	if err := json.Unmarshal([]byte(old), &m); err != nil {
		t.Fatal(err)
	}
	if got := m.Start(); got != 500*time.Millisecond {
		t.Errorf("the start read as %v, want 500ms", got)
	}
	if got := m.BPM(0); got != 90 {
		t.Errorf("the first tempo read as %v, want 90", got)
	}
	if ts := m.Tempos(); len(ts) != 2 || !ts[1].Ramp || ts[1].BPM != 140 {
		t.Errorf("the tempos read as %+v", ts)
	}
	if got, want := m.Meter(0), (Meter{Beats: 3, Unit: 4}); got != want {
		t.Errorf("the meter read as %v, want %v", got, want)
	}
}

// TestTheStartReadsBackExactly: a start in milliseconds that binary
// cannot hold exactly still reads back to the nanosecond.
func TestTheStartReadsBackExactly(t *testing.T) {
	m := New(120, 4, 4)
	m.SetStart(1234567 * time.Microsecond)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Map
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Start() != m.Start() {
		t.Errorf("the start read back as %v, want %v", back.Start(), m.Start())
	}
}

// TestFramesDoNotOverflow: a position days in, at a high rate, is
// still counted right.
func TestFramesDoNotOverflow(t *testing.T) {
	m := New(60, 4, 4)
	// A quarter a second, so three days is 259200 quarters.
	q := Quarters(3 * 24 * 60 * 60)
	if got, want := m.Frames(q, 192000), int64(3*24*60*60)*192000; got != want {
		t.Errorf("three days at 192 kHz is %d frames, want %d", got, want)
	}
	if got := m.AtFrame(int64(3*24*60*60)*192000, 192000); got != q {
		t.Errorf("three days of frames read back as %d ticks, want %d", got, q)
	}
}

// TestTheZeroMeterCountsQuarters: a Meter with no unit does not divide
// by zero.
func TestTheZeroMeterCountsQuarters(t *testing.T) {
	if got := (Meter{}).Beat(); got != PPQ {
		t.Errorf("the zero meter's beat is %d ticks, want %d", got, PPQ)
	}
}

func TestQuartersAtAFrameAreExact(t *testing.T) {
	const rate = 48000
	m := New(60, 4, 4)
	m.SetTempos([]Tempo{{At: 0, BPM: 60, Ramp: true}, {At: 4 * PPQ, BPM: 180}})
	m.SetStart(250 * time.Millisecond)
	for f := int64(0); f < 5*rate; f += 997 {
		q := m.QuartersAtFrame(f, rate)
		// AtFrame rounds the same place to a tick.
		if d := math.Abs(q*PPQ - float64(m.AtFrame(f, rate))); d > 0.5 {
			t.Fatalf("frame %d falls at %v quarter notes, %v ticks from AtFrame's", f, q, d)
		}
		if f < rate/4 && f > 0 && q >= 0 {
			t.Fatalf("frame %d, before the start, falls at %v quarter notes", f, q)
		}
	}
	// The ramp's middle, in time: 60 to 180 over 4 quarter notes lasts
	// two seconds, and a second in it plays 1.5 quarter notes at 120.
	if q, bpm := m.QuartersAtFrame(rate+rate/4, rate), m.BPMAtFrame(rate+rate/4, rate); math.Abs(q-1.5) > 1e-12 || math.Abs(bpm-120) > 1e-9 {
		t.Errorf("a second into the ramp is %v quarter notes at %v, want 1.5 at 120", q, bpm)
	}
	if bpm := m.BPMAtFrame(10*rate, rate); bpm != 180 {
		t.Errorf("after the ramp the tempo is %v, want 180", bpm)
	}
	// The ramp ends on frame 108000: reaching it, the tempo is the
	// ramp's end; from it, the next change's.
	m.SetTempos([]Tempo{{At: 0, BPM: 60, Ramp: true}, {At: 4 * PPQ, BPM: 180}, {At: 10 * PPQ, BPM: 90}})
	if a, b := m.BPMReachingFrame(rate/4+4*rate, rate), m.BPMAtFrame(rate/4+4*rate, rate); a != 180 || b != 90 {
		t.Errorf("at the step to 90, the tempo reaching it is %v and from it %v, want 180 and 90", a, b)
	}
}
