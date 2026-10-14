package audio

import (
	"math"
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
	// Dynamic bands, turning down and back, their dynamics on and off.
	dyn := make([]Band, MaxBands)
	for k := range dyn {
		dyn[k] = Band{ID: 200 + k, Kind: FilterKind(k % 3), Freq: float32(60 + 400*k), Gain: 3, Q: 1, On: true,
			Dynamic: Dynamics{On: k%2 == 0, Threshold: -60, Ratio: 4, Attack: 1, Release: 20, Range: 12}}
	}
	steps = append(steps, &eqSettings{bands: dyn})
	frames := sine(block, 300, 0.5, 0)
	// Every allocation counted, not their mean a block, which rounds
	// the few a change makes down to none.
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	n := allocsUnder(t, "audio.(*eqInsert).Process", func() {
		for i := range 4 * len(steps) {
			eq.settings.Store(steps[i/4])
			eq.SetBypass(i%28 == 13)
			eq.SetAmount(float64(i%5) / 2)
			in.Process(frames)
		}
	})
	if n != 0 {
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

// playFor plays seconds of a stereo sine at hz, amplitude amp, through
// in, a block at a time, and returns the peak of the last block.
func playFor(in Insert, seconds, hz, amp float64, from *int) float64 {
	var last float64
	n := int(seconds * SampleRate / block)
	for i := range n {
		frames := sine(block, hz, amp, *from)
		*from += block
		in.Process(frames)
		// The peak over the last 100 ms, a few of a low sine's periods.
		if n-i <= SampleRate/10/block {
			last = max(last, peak(frames))
		}
	}
	return last
}

func db(v float64) float64 { return 20 * math.Log10(v) }

func TestADynamicBandTurnsItsBandDownOverItsThreshold(t *testing.T) {
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Q: 1, On: true,
		Dynamic: Dynamics{On: true, Threshold: -20, Ratio: 4, Attack: 1, Release: 50, Range: 12}}})
	in, ok := eq.InsertAt(SampleRate).(*eqInsert)
	if !ok {
		t.Fatal("the EQ's insert is of another type")
	}
	at := 0
	// At 1 kHz, 14 dB over the threshold at 4:1: turned down 10.5 dB.
	got := db(playFor(in, 0.5, 1000, 0.5, &at)) - db(0.5)
	if math.Abs(got+10.5) > 0.5 {
		t.Errorf("a sine at 1 kHz, 14 dB over at 4:1, comes out %+.2f dB, want -10.5", got)
	}
	if r, ok := in.Reduction(1); !ok || math.Abs(r+10.5) > 0.5 {
		t.Errorf("the band says it turns down %.2f dB (%v), want -10.5", r, ok)
	}
	if h, ok := in.Heard(1); !ok || math.Abs(h-db(0.5)) > 0.5 {
		t.Errorf("the band says it hears %.2f dB (%v), want the sine's peak, %.2f", h, ok, db(0.5))
	}
	if _, ok := in.Heard(2); ok {
		t.Error("the insert says it hears a band it has not")
	}
	// Under the threshold, it lets go over its release, and the sound
	// passes as it is.
	got = db(playFor(in, 0.5, 1000, 0.01, &at)) - db(0.01)
	if math.Abs(got) > 0.2 {
		t.Errorf("a sine at 1 kHz under the threshold comes out %+.2f dB, want as it was", got)
	}
	// Far from its frequency, the band does not hear it.
	got = db(playFor(in, 0.5, 100, 0.5, &at)) - db(0.5)
	if r, _ := in.Reduction(1); math.Abs(got) > 0.2 || r < -0.5 {
		t.Errorf("a loud sine at 100 Hz through a dynamic band at 1 kHz comes out %+.2f dB, the band turned down %.2f dB", got, r)
	}
	// At half the amount, it turns down half as far.
	eq.SetAmount(0.5)
	got = db(playFor(in, 0.5, 1000, 0.5, &at)) - db(0.5)
	if math.Abs(got+5.25) > 0.5 {
		t.Errorf("at half the amount, the sine 14 dB over comes out %+.2f dB, want -5.25", got)
	}
	eq.SetAmount(1)
	// Turned down no further than its range.
	set := eq.settings.Load().bands[0]
	set.Dynamic.Threshold, set.Dynamic.Ratio = -40, 20
	eq.Set([]Band{set})
	got = db(playFor(in, 0.5, 1000, 0.5, &at)) - db(0.5)
	if math.Abs(got+12) > 0.5 {
		t.Errorf("far over the threshold the band turns down %+.2f dB, want its range, 12", got)
	}
	// Dynamics off: back to as set, gliding, not at once.
	set.Dynamic.On = false
	eq.Set([]Band{set})
	playFor(in, 0.003, 1000, 0.5, &at)
	if r, _ := in.Reduction(1); r > -3 {
		t.Errorf("3 ms after its dynamics went off, the band is back up from -12 to %.2f dB at once", r)
	}
	got = db(playFor(in, 0.5, 1000, 0.5, &at)) - db(0.5)
	if math.Abs(got) > 0.2 {
		t.Errorf("its dynamics off, the band turns down %+.2f dB", got)
	}
	if h, _ := in.Heard(1); h != silentDB {
		t.Errorf("its dynamics off, the band says it hears %.2f dB", h)
	}
}

func TestTheAmountScalesTheGainsAndLeavesTheCuts(t *testing.T) {
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: 6, Q: 1, On: true}, {ID: 2, Kind: LowCut, Freq: 400, Q: 0.707, Slope: 48, On: true}})
	in := eq.InsertAt(SampleRate)
	at := 0
	for _, c := range []struct{ amount, bell, cut float64 }{
		{1, 6, -80},
		{0.5, 3, -80},
		{2, 12, -80},
		{0, 0, -80},
	} {
		eq.SetAmount(c.amount)
		bell := db(playFor(in, 0.3, 1000, 0.1, &at)) - db(0.1)
		cut := db(playFor(in, 0.3, 50, 0.1, &at)) - db(0.1)
		if math.Abs(bell-c.bell) > 0.3 {
			t.Errorf("at an amount of %v the bell of +6 dB lifts %+.2f dB, want %+.1f", c.amount, bell, c.bell)
		}
		if cut > -50 {
			t.Errorf("at an amount of %v the cut lets 50 Hz through at %+.1f dB", c.amount, cut)
		}
	}
}

func TestTheQScaleNarrowsTheBellsAndLeavesTheCuts(t *testing.T) {
	bands := []Band{{ID: 1, Kind: Bell, Freq: 1000, Gain: 12, Q: 1, On: true},
		{ID: 2, Kind: LowCut, Freq: 100, Q: 0.707, Slope: 12, On: true}}
	eq := NewEQ()
	eq.Set(bands)
	in := eq.InsertAt(SampleRate)
	at := 0
	// An octave over the bell, and at the cut, at the bands' Q and at a
	// quarter and four times it: as the bands scaled by Scaled sound.
	for _, scale := range []float64{1, 4, 0.25} {
		eq.SetQScale(scale)
		playFor(in, 0.3, 2000, 0.1, &at)
		for _, hz := range []float64{2000, 100} {
			want := Response([]Band{bands[0].Scaled(scale), bands[1].Scaled(scale)}, hz)
			if got := db(playFor(in, 0.2, hz, 0.1, &at)) - db(0.1); math.Abs(got-want) > 0.3 {
				t.Errorf("at a Q scale of %v the EQ is %+.2f dB at %v Hz, want %+.2f", scale, got, hz, want)
			}
		}
	}
	if b := bands[1].Scaled(4); b.Q != 0.707 {
		t.Errorf("a cut scaled has a Q of %v", b.Q)
	}
	if b := (Band{Kind: Notch, Q: 2}).Scaled(10); b.Q != 8 {
		t.Errorf("a notch scaled past the most has a Q of %v, want 8", b.Q)
	}
	if narrow, wide := Response([]Band{bands[0].Scaled(4)}, 2000), Response([]Band{bands[0].Scaled(0.25)}, 2000); narrow > 1 || wide < 9 {
		t.Errorf("an octave over a bell of 12 dB, at four times its Q it lifts %+.2f dB, at a quarter %+.2f dB", narrow, wide)
	}
}

func TestTheThresholdShiftMovesEveryDynamicBand(t *testing.T) {
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 1000, Q: 1, On: true,
		Dynamic: Dynamics{On: true, Threshold: -20, Ratio: 4, Attack: 1, Release: 50, Range: 24}}})
	in := eq.InsertAt(SampleRate)
	at := 0
	// A sine 14 dB over: with the thresholds up 8 dB, 6 dB over, so
	// turned down 4.5 dB; down 4 dB, 18 dB over, so 13.5 dB.
	for _, c := range []struct{ shift, want float64 }{{0, -10.5}, {8, -4.5}, {-4, -13.5}} {
		eq.SetThresholdShift(c.shift)
		if got := db(playFor(in, 0.5, 1000, 0.5, &at)) - db(0.5); math.Abs(got-c.want) > 0.5 {
			t.Errorf("with the thresholds moved %+v dB, a sine 14 dB over comes out %+.2f dB, want %+.1f", c.shift, got, c.want)
		}
	}
}

func TestAnEQHoldsNoSubnormalsInTheSilenceAfterSound(t *testing.T) {
	eq := NewEQ()
	eq.Set([]Band{{ID: 1, Kind: Bell, Freq: 200, Gain: 9, Q: 4, On: true}, {ID: 2, Kind: LowCut, Freq: 40, Q: 0.7, On: true}})
	in := eq.Insert()
	at := 0
	for range 200 {
		in.Process(sine(block, 200, 0.5, at))
		at += block
	}
	// Ten seconds of silence: left alone, the sections decay to
	// subnormals and stick there, many times slower to work.
	for range 48000 * 10 / block {
		in.Process(make([]float32, 2*block))
	}
	for _, b := range in.(*eqInsert).bands {
		for k, st := range b.state {
			for _, v := range st {
				if v != 0 && math.Abs(v) < 0x1p-1022 {
					t.Fatalf("after ten seconds of silence, section %d holds %g", k, v)
				}
			}
		}
	}
}
