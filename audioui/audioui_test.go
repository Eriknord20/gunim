package audioui

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

const rate = 48000

// sine returns n frames of a sine at hz and amplitude a, both channels
// alike.
func sine(n int, hz, a float64) []float32 {
	out := make([]float32, 2*n)
	for i := range n {
		v := float32(a * math.Sin(2*math.Pi*hz*float64(i)/rate))
		out[2*i], out[2*i+1] = v, v
	}
	return out
}

func TestAMetersRMSHoldsStillOnSteadyNoise(t *testing.T) {
	l := NewLevels()
	rng := rand.New(rand.NewPCG(1, 2))
	chunk := make([]float32, 2*800)
	var last float32
	for f := range 180 {
		for i := range chunk {
			chunk[i] = float32(rng.NormFloat64() * 0.1)
		}
		l.Take(chunk, rate, time.Second/60)
		if f > 90 && math.Abs(float64(l.RMS[0]-last)) > 0.3 {
			t.Fatalf("frame %d: on steady noise the RMS moved %.2f dB", f, l.RMS[0]-last)
		}
		last = l.RMS[0]
	}
	if math.Abs(float64(last+20)) > 0.5 {
		t.Fatalf("noise at an RMS of 0.1 reads %.1f dB, want -20", last)
	}
}

func TestAMetersHoldWaitsThenFalls(t *testing.T) {
	l := NewLevels()
	l.Take(sine(800, 1000, 0.5), rate, time.Second/60)
	held := l.Hold[0]
	if math.Abs(float64(held+6)) > 0.2 {
		t.Fatalf("a sine peaking at 0.5 holds at %.1f dB, want -6", held)
	}
	for range 60 {
		l.Quiet(time.Second / 60)
	}
	if l.Hold[0] != held || l.Peak[0] > held-10 {
		t.Fatalf("a second on, the hold reads %.1f and the peak %.1f, from %.1f", l.Hold[0], l.Peak[0], held)
	}
	for range 60 {
		l.Quiet(time.Second / 60)
	}
	if l.Hold[0] >= held {
		t.Fatal("two seconds on, the hold has not fallen")
	}
	if l.Top[0] != held {
		t.Fatalf("the top peak reads %.1f, want %.1f", l.Top[0], held)
	}
}

func TestAFaderSetsItsGainByDragAndDoubleClick(t *testing.T) {
	var gain float32
	f := NewFader(func() float32 { return gain }, func(v float32, _ *gunim.UI) gunim.Intent { gain = v; return nil })
	w := gunimtest.New(t, geom.Sz(24, 224), nil)
	gunim.RegisterView(w, "fader", func(struct{}) gunim.Node { return f }, nil)
	if err := w.Client().Mount(gunim.Root, "fader", "fader", nil); err != nil {
		t.Fatal(err)
	}
	in := func(e input.Event) {
		w.Input(e)
		w.Frame(time.Second / 60)
	}
	w.Frame(time.Second / 60)
	mid := geom.Pt(12, 112)
	in(input.PointerMove{Pos: mid})
	in(input.PointerDown{Pos: mid, Button: input.ButtonPrimary, Clicks: 1})
	// Up a quarter of the travel, 200 points: half of 24 dB.
	in(input.PointerMove{Pos: mid.Add(geom.Pt(0, -50))})
	in(input.PointerUp{Pos: mid.Add(geom.Pt(0, -50)), Button: input.ButtonPrimary})
	if math.Abs(float64(gain-12)) > 0.01 {
		t.Fatalf("a drag up a quarter of the way set %.1f dB, want +12", gain)
	}
	// The wheel passes the fader by, to scroll the pane it is in.
	in(input.Scroll{Pos: mid, Notches: geom.Pt(0, 1)})
	if math.Abs(float64(gain-12)) > 0.01 {
		t.Fatalf("a notch of the wheel set %.1f dB, want it left at +12", gain)
	}
	in(input.PointerDown{Pos: mid, Button: input.ButtonPrimary, Clicks: 2})
	in(input.PointerUp{Pos: mid, Button: input.ButtonPrimary})
	if gain != 0 {
		t.Fatalf("a double-click set %.1f dB, want 0", gain)
	}
}

func TestASpectrometerReadsATonesLevelAtItsPitch(t *testing.T) {
	s := NewSpectrometer(4096)
	freqs := LogFreqs(120)
	out := make([]float32, len(freqs))
	if s.Measure(sine(100, 1000, 0.3), rate, freqs, out) {
		t.Fatal("100 frames were measured, fewer than the 4096 taken")
	}
	if !s.Measure(sine(8000, 1000, 0.3), rate, freqs, out) {
		t.Fatal("8000 frames were not measured")
	}
	k := 0
	for i, f := range freqs {
		if math.Abs(float64(f)-1000) < math.Abs(float64(freqs[k])-1000) {
			k = i
		}
		if math.Abs(float64(f)-1000) > 300 && out[i] > -50 {
			t.Fatalf("a 1 kHz tone reads %.1f dB at %.0f Hz", out[i], f)
		}
	}
	if math.Abs(float64(out[k])-DB(0.3)) > 1 {
		t.Fatalf("a 1 kHz tone at %.1f dBFS reads %.1f dB", DB(0.3), out[k])
	}
}

func TestASpectrumRisesAtOnceAndFallsSlowly(t *testing.T) {
	s := NewSpectrum(8)
	loud, quiet := make([]float32, 8), make([]float32, 8)
	for i := range loud {
		loud[i], quiet[i] = -10, -80
	}
	for range 10 {
		s.Step(time.Second/60, loud, nil)
	}
	if v := s.Out[4] - s.Tilted(4, -10); math.Abs(float64(v)) > 1.5 {
		t.Fatalf("a sixth of a second of sound leaves the spectrum %.1f dB off it", v)
	}
	s.Step(time.Second/60, quiet, nil)
	if s.Out[4] < s.Tilted(4, -10)-8 {
		t.Fatalf("a frame of silence dropped the spectrum to %.1f", s.Out[4])
	}
	var bands [3]float32
	s.Bands(bands[:])
	if bands[1] < 0.5 {
		t.Fatalf("loud sound reads %.2f in the middle band", bands[1])
	}
}

func TestASpectrogramKeepsTheTilesItShows(t *testing.T) {
	g := NewSpectrogram()
	g.keep = 3
	col := make([]float32, 120)
	for range 10 * GramTile {
		g.Push(col)
	}
	if len(g.tiles) != 3 || g.n != 0 || g.Columns() != 3*GramTile {
		t.Fatalf("after 10 tiles' columns, %d tiles are kept, %d columns filling", len(g.tiles), g.n)
	}
}

func TestAFilesSpectrogramIsDrawnAtLevelsForTheViewZoomedOut(t *testing.T) {
	g := &Gram{Cols: 10000, Rate: rate, Data: make([]byte, 10000*GramRows)}
	tiles := NewGramTiles(g)
	if len(tiles.levels) != 3 || tiles.levels[1].per != 4 {
		t.Fatalf("10,000 columns are drawn at %d levels", len(tiles.levels))
	}
	if w, h := tiles.Tile(); w != GramTileCols || h != GramRows {
		t.Fatalf("a tile is %dx%d", w, h)
	}
}

func TestAGramScanFindsATonesPitch(t *testing.T) {
	s := NewGramScan(rate)
	s.Write(make([]float32, 2*rate/2))
	s.Write(sine(rate, 1000, 0.3))
	g := s.Done()
	col := func(at float64) int { return int(at * rate / GramHop) }
	row := func(hz float64) int { return int(math.Log(hz/20) / math.Log(1000) * GramRows) }
	if db := g.At(col(1), row(1000)); math.Abs(db-DB(0.3)) > 1.5 {
		t.Fatalf("in the tone, its pitch reads %.1f dB, want %.1f", db, DB(0.3))
	}
	if db := g.At(col(0.1), row(1000)); db > -90 {
		t.Fatalf("in the silence before, 1 kHz reads %.1f dB", db)
	}
}

func TestAWavesCoarserLevelsHoldTheFinersExtremes(t *testing.T) {
	frames := 20 * rate
	s := NewWaveScan(int64(frames), rate)
	rng := rand.New(rand.NewPCG(3, 4))
	buf := make([]float32, 2*4096)
	for at := 0; at < frames; at += 4096 {
		n := min(4096, frames-at)
		for i := range 2 * n {
			buf[i] = float32(rng.NormFloat64() * 0.2)
		}
		s.Write(buf[:2*n])
	}
	w := s.Done()
	ls := w.Levels
	if len(ls) < 2 || ls[0].Per != WaveFinest {
		t.Fatalf("the waveform has %d levels, the finest of %d frames", len(ls), ls[0].Per)
	}
	for i := 1; i < len(ls); i++ {
		fine, coarse := ls[i-1], ls[i]
		if coarse.Per != 4*fine.Per {
			t.Fatalf("level %d is of %d frames, after %d", i, coarse.Per, fine.Per)
		}
		for b := range coarse.Max[0] {
			var hi float32
			for j := 4 * b; j < min(4*b+4, len(fine.Max[0])); j++ {
				hi = max(hi, fine.Max[0][j])
			}
			if coarse.Max[0][b] != hi {
				t.Fatalf("level %d, stretch %d: highest %v, the finer's %v", i, b, coarse.Max[0][b], hi)
			}
		}
	}
	if lv := w.Level(10000); lv.Per > 10000 || lv.Per*4 <= 10000 && lv != &ls[len(ls)-1] {
		t.Fatalf("a column of 10,000 frames reads the level of %d", lv.Per)
	}
	if th := w.Thumbnail(64); th[10] < 0.18 || th[10] > 0.3 {
		t.Fatalf("noise at an RMS of 0.2 has a thumbnail of %.2f", th[10])
	}
}

func TestAFlingGlidesOnSlowingAndStopsAtTheEnd(t *testing.T) {
	var f Fling
	f.Grab(0)
	v0 := 0.0
	for range 10 {
		v0 += 0.1
		f.Held(v0, time.Second/60)
	}
	f.Release(0.05)
	if !f.Gliding() {
		t.Fatal("let go moving at 6 a second, the view does not glide")
	}
	was, step := v0, 1.0
	for range 30 {
		next, _ := f.Step(v0, 10, 0, 100, time.Second/60)
		if next-v0 >= step {
			t.Fatalf("the glide quickened from %.3f to %.3f", step, next-v0)
		}
		step, v0 = next-v0, next
	}
	if v0-was < 0.5 {
		t.Fatalf("half a second of glide moved the view %.2f", v0-was)
	}
	_, gliding := f.Step(v0, 10, 0, v0+10.01, time.Second/60)
	if gliding {
		t.Fatal("the glide runs on past the end")
	}
	f.Grab(0)
	for range 10 {
		f.Held(1, time.Second/60)
	}
	f.Release(0.05)
	if f.Gliding() {
		t.Fatal("let go held still, the view glides")
	}
}

func TestLoudnessReadsATonesLevelAndRange(t *testing.T) {
	l := NewLoudness(rate)
	tone := sine(rate/60, 1000, 0.3)
	for range 8 * 60 {
		l.Write(tone)
		l.Step(time.Second/60, true)
	}
	// A 1 kHz sine in both channels reads as loud as its peak in dBFS.
	want := float32(DB(0.3))
	if math.Abs(float64(l.Integrated()-want)) > 0.2 || math.Abs(float64(l.Short-want)) > 0.3 {
		t.Fatalf("a steady tone reads %.1f integrated, %.1f short-term, want %.1f", l.Integrated(), l.Short, want)
	}
	if !l.Ranged || l.High-l.Low > 0.5 {
		t.Fatalf("a steady tone ranges %v, from %.1f to %.1f", l.Ranged, l.Low, l.High)
	}
	if tp := DB(l.TruePeak()); math.Abs(tp-DB(0.3)) > 0.2 {
		t.Fatalf("the true peak reads %.1f dB, want %.1f", tp, DB(0.3))
	}
	for range 60 {
		l.Step(time.Second/60, false)
	}
	if l.Short > -60 {
		t.Fatalf("a second stopped, the short-term loudness reads %.1f", l.Short)
	}
}

func TestAScopeReadsTheChannelsCorrelation(t *testing.T) {
	for _, c := range []struct {
		name string
		sign float32
		want float32
	}{{"alike", 1, 1}, {"opposed", -1, -1}} {
		s := NewScope()
		frames := sine(ScopeFrames, 440, 0.5)
		for i := 1; i < len(frames); i += 2 {
			frames[i] *= c.sign
		}
		s.Write(frames)
		for range 60 {
			s.Step(time.Second / 60)
		}
		if math.Abs(float64(s.Corr-c.want)) > 0.05 {
			t.Fatalf("channels %s read a correlation of %.2f, want %.0f", c.name, s.Corr, c.want)
		}
	}
}

func TestRunningLoudnessFollowsTheIntegratedLoudness(t *testing.T) {
	// Ten seconds of blocks at -20 LUFS, then ten at -10.
	blocks := make([]float64, 200)
	for i := range blocks {
		l := -20.0
		if i >= 100 {
			l = -10
		}
		blocks[i] = math.Pow(10, (l+0.691)/10)
	}
	run := RunningLoudness(blocks)
	if len(run) != 20 {
		t.Fatalf("twenty seconds of blocks made %d points", len(run))
	}
	if math.Abs(float64(run[5]+20)) > 0.01 || run[19] <= run[9] {
		t.Fatalf("the running loudness reads %.2f at 5 s and %.2f, %.2f at 10 and 20 s", run[5], run[9], run[19])
	}
	if l := LUFSOf(blocks[150]); math.Abs(l+10) > 1e-9 {
		t.Fatalf("a block at -10 LUFS reads %.2f", l)
	}
}

func TestAMetersLoudnessStartsAsItIsFirstAskedFor(t *testing.T) {
	l := NewLevels()
	tone := sine(4800, 1000, 0.1)
	l.Take(tone, rate, 100*time.Millisecond)
	if l.lm != nil {
		t.Fatal("levels no one asked the loudness of measure it")
	}
	if _, ok := l.ShortTerm(); ok {
		t.Fatal("the loudness reads before any sound was measured for it")
	}
	for range 40 {
		l.Take(tone, rate, 100*time.Millisecond)
	}
	s, ok := l.ShortTerm()
	if !ok || math.Abs(s+20.4) > 1 {
		t.Fatalf("four seconds of a 1 kHz sine at 0.1 read %.1f LUFS, ok %v; want about -20.4", s, ok)
	}
}
