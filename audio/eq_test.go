package audio

import (
	"math"
	"runtime"
	"testing"
)

func TestABandsResponseIsItsGainAtItsFrequency(t *testing.T) {
	bell := Band{Kind: Bell, Freq: 1000, Gain: 6, Q: 1, On: true}
	if db := bell.Response(1000); math.Abs(db-6) > 0.05 {
		t.Errorf("a bell of +6 dB at 1 kHz lifts 1 kHz by %.2f dB", db)
	}
	if db := bell.Response(50); math.Abs(db) > 0.2 {
		t.Errorf("a bell at 1 kHz lifts 50 Hz by %.2f dB, want next to nothing", db)
	}
	shelf := Band{Kind: LowShelf, Freq: 200, Gain: -9, Q: 0.707, On: true}
	if db := shelf.Response(30); math.Abs(db+9) > 0.3 {
		t.Errorf("a low shelf of -9 dB at 200 Hz lowers 30 Hz by %.2f dB", db)
	}
	for _, slope := range []int{12, 24, 48} {
		cut := Band{Kind: LowCut, Freq: 100, Q: 0.707, Slope: slope, On: true}
		// Two octaves under it, a cut has fallen about two slopes.
		if db := cut.Response(25); math.Abs(db+2*float64(slope)) > 3 {
			t.Errorf("a low cut at 100 Hz, %d dB an octave, is at %.1f dB at 25 Hz, want about %d", slope, db, -2*slope)
		}
		if db := cut.Response(100); math.Abs(db+3) > 0.3 {
			t.Errorf("a Butterworth low cut, %d dB an octave, is at %.2f dB at its frequency, want -3", slope, db)
		}
	}
	off := bell
	off.On = false
	if db := off.Response(1000); db != 0 {
		t.Errorf("a band turned off lifts %v dB", db)
	}
}

// sine returns n frames of a stereo sine at hz, amplitude amp.
func sine(n int, hz, amp float64, from int) []float32 {
	out := make([]float32, 2*n)
	for i := range n {
		v := float32(amp * math.Sin(2*math.Pi*hz*float64(from+i)/SampleRate))
		out[2*i], out[2*i+1] = v, v
	}
	return out
}

// peak returns the largest sample of frames.
func peak(frames []float32) float64 {
	p := 0.0
	for _, v := range frames {
		p = max(p, math.Abs(float64(v)))
	}
	return p
}

func TestAnEQLiftsTheSoundAsItsBandsSayAndGlidesWhenTheyMove(t *testing.T) {
	eq := NewEQ()
	in := eq.Insert()
	run := func(blocks int, hz float64) (last float64, steps []float64) {
		at := 0
		for range blocks {
			b := sine(block, hz, 0.25, at)
			at += block
			in.Process(b)
			last = peak(b)
			steps = append(steps, last)
		}
		return last, steps
	}
	if got, _ := run(20, 1000); math.Abs(got-0.25) > 0.002 {
		t.Fatalf("an EQ with no bands changed a sine's peak from 0.25 to %.4f", got)
	}
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: 12, Q: 1, On: true}})
	got, steps := run(100, 1000)
	if want := 0.25 * math.Pow(10, 12.0/20); math.Abs(got-want) > 0.01 {
		t.Fatalf("a bell of +12 dB at 1 kHz brought a 1 kHz sine to %.3f, want %.3f", got, want)
	}
	// The band fades in over some 25 ms, a tenth of the way a block,
	// never in a leap.
	for i := 1; i < len(steps); i++ {
		if steps[i]-steps[i-1] > 0.1 {
			t.Fatalf("block %d: the peak leapt from %.3f to %.3f", i, steps[i-1], steps[i])
		}
	}
	if got, _ := run(100, 8000); math.Abs(got-0.25) > 0.01 {
		t.Fatalf("a bell at 1 kHz brought an 8 kHz sine to %.3f, want it as it was, 0.25", got)
	}
	eq.SetBypass(true)
	if got, _ := run(100, 1000); math.Abs(got-0.25) > 0.003 {
		t.Fatalf("bypassed, the EQ brought a 1 kHz sine to %.3f, want 0.25", got)
	}
}

func TestTheAnalyzerHearsTheSoundBeforeAndAfterAnInsert(t *testing.T) {
	m := NewMixer()
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: -12, Q: 1, On: true}})
	src := &tone{hz: 1000, amp: 0.5}
	m.Play(src, Options{Insert: eq.Insert()})
	m.Mix(make([]float32, 2*spectrumSize*2))
	a := NewAnalyzer(m, 8)
	freqs := []float32{500, 1000, 2000}
	heard, before := make([]float32, 3), make([]float32, 3)
	a.Spectrum(freqs, heard, before)
	if d := before[1] - heard[1]; math.Abs(float64(d)-12) > 1 {
		t.Fatalf("at 1 kHz the sound before is %.1f dB and heard %.1f dB, want 12 dB apart", before[1], heard[1])
	}
	if math.Abs(float64(before[1])+6) > 1 {
		t.Fatalf("a sine of half full scale reads %.1f dB, want -6", before[1])
	}
}

// tone is an endless sine.
type tone struct {
	hz, amp float64
	at      int
}

func (s *tone) Read(dst []float32) (int, error) {
	n := len(dst) / 2
	copy(dst, sine(n, s.hz, s.amp, s.at))
	s.at += n
	return n, nil
}

// TestAnEQFollowsTheMixersRate plays a 1 kHz bell at 44.1 kHz and
// finds the sections worked out for that rate.
func TestAnEQFollowsTheMixersRate(t *testing.T) {
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: 6, Q: 1, On: true}})
	in, ok := eq.Insert().(*eqInsert)
	if !ok {
		t.Fatal("the EQ's insert is of another type")
	}
	in.setRate(44100)
	frames := make([]float32, 2*block)
	for range 200 {
		in.Process(frames)
	}
	want := Band{Kind: Bell, Freq: 1000, Gain: 6, Q: 1}.appendSections(nil, 44100)
	if len(in.bands) != 1 || in.bands[0].secs[0] != want[0] {
		t.Fatalf("the sections are %+v, want %+v at 44.1 kHz", in.bands[0].secs, want)
	}
}

// TestAnEQInsertedAtARateWorksAtIt runs an EQ at 44.1 kHz outside a
// mixer and finds the sections worked out for that rate.
func TestAnEQInsertedAtARateWorksAtIt(t *testing.T) {
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: 6, Q: 1, On: true}})
	in, ok := eq.InsertAt(44100).(*eqInsert)
	if !ok {
		t.Fatal("the EQ's insert is of another type")
	}
	frames := make([]float32, 2*block)
	for range 200 {
		in.Process(frames)
	}
	want := Band{Kind: Bell, Freq: 1000, Gain: 6, Q: 1}.appendSections(nil, 44100)
	if len(in.bands) != 1 || in.bands[0].secs[0] != want[0] {
		t.Fatalf("the sections are %+v, want %+v at 44.1 kHz", in.bands[0].secs, want)
	}
}

// TestAnEQInsertMakesNothingNewAsItsSettingsChange runs an insert
// through every kind of change, bands coming and going, changing kind
// and slope, and the bypass, and finds it makes nothing new.
func TestAnEQInsertMakesNothingNewAsItsSettingsChange(t *testing.T) {
	eq := NewEQ()
	in := eq.InsertAt(44100)
	steps := make([]*eqSettings, 0, MaxBands+2)
	for i := range MaxBands {
		bands := make([]Band, i+1)
		for k := range bands {
			bands[k] = Band{ID: k + 1, Kind: FilterKind((i + k) % 6), Slope: 12 * (1 + (i+k)%4), Freq: float32(40 + 300*k), Gain: 3, Q: 1, On: true}
		}
		steps = append(steps, &eqSettings{bands: bands})
	}
	// Then all gone at once, and all back, other bands, as the room
	// fills with bands fading out.
	steps = append(steps, &eqSettings{})
	again := make([]Band, MaxBands)
	for k := range again {
		again[k] = Band{ID: 100 + k, Kind: HighCut, Slope: 48, Freq: 8000, Q: 0.7, On: true}
	}
	steps = append(steps, &eqSettings{bands: again})
	frames := make([]float32, 2*block)
	// Every allocation counted, not their mean a block, which rounds
	// the few a change makes down to none.
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts, so other
	// goroutines allocate as little as may be meanwhile.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 4 * len(steps) {
		eq.settings.Store(steps[i/4])
		eq.SetBypass(i%28 == 13)
		in.Process(frames)
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("the insert made %d things over %d blocks", n, 4*len(steps))
	}
}

// TestAnEQIsSetWhileItPlays sets an EQ from one goroutine while an
// insert plays it on another, for the race detector.
func TestAnEQIsSetWhileItPlays(t *testing.T) {
	eq := NewEQ()
	in := eq.InsertAt(48000)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 500 {
			eq.Set([]Band{{ID: 1, Kind: Bell, Freq: float32(100 + i), Gain: 6, Q: 1, On: true}})
			eq.SetBypass(i%50 == 0)
		}
	}()
	frames := make([]float32, 2*block)
	for {
		select {
		case <-done:
			return
		default:
			in.Process(frames)
		}
	}
}

// TestAnEQPlaysAtMostMaxBands keeps the first MaxBands bands of more.
func TestAnEQPlaysAtMostMaxBands(t *testing.T) {
	eq := NewEQ()
	bands := make([]Band, MaxBands+5)
	for k := range bands {
		bands[k] = Band{ID: k + 1, Kind: Bell, Freq: 1000, Gain: 1, Q: 1, On: true}
	}
	eq.Set(bands)
	in, ok := eq.Insert().(*eqInsert)
	if !ok {
		t.Fatal("the EQ's insert is of another type")
	}
	in.Process(make([]float32, 2*block))
	if len(in.bands) != MaxBands || in.bands[MaxBands-1].id != MaxBands {
		t.Fatalf("the EQ plays %d bands, want the first %d", len(in.bands), MaxBands)
	}
}
