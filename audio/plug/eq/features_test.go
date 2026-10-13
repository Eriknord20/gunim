package eq

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audio/plug/tonal"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// loudBell is a dynamic bell at 1 kHz, flat until its band rises over
// -20 dB, then turning down at 4:1, 12 dB at most.
var loudBell = audio.Band{ID: 7, Kind: audio.Bell, Freq: 1000, Q: 1, On: true,
	Dynamic: audio.Dynamics{On: true, Threshold: -20, Ratio: 4, Attack: 1, Release: 50, Range: 12}}

func TestADynamicBandIsKeptAndABadOneRefused(t *testing.T) {
	p, _ := newEQ(t)
	p.setBands([]audio.Band{loudBell, bell[0]})
	q, _ := newEQ(t)
	if err := q.SetState(p.State()); err != nil {
		t.Fatal(err)
	}
	if got := q.Bands(); !slices.Equal(got, p.Bands()) {
		t.Fatalf("the bands come back as %+v, want %+v", got, p.Bands())
	}
	for name, d := range map[string]audio.Dynamics{
		"threshold": {On: true, Threshold: 6, Ratio: 2, Attack: 1, Release: 50, Range: 6},
		"ratio":     {On: true, Threshold: -20, Ratio: 0.5, Attack: 1, Release: 50, Range: 6},
		"attack":    {On: true, Threshold: -20, Ratio: 2, Attack: 0, Release: 50, Range: 6},
		"range":     {On: true, Threshold: -20, Ratio: 2, Attack: 1, Release: 50, Range: 40},
	} {
		b := loudBell
		b.Dynamic = d
		p.setBands([]audio.Band{b})
		if err := q.SetState(p.State()); err == nil {
			t.Errorf("a band with a bad %s was taken", name)
		}
	}
}

// through plays a second of a sine at hz and amp through p, and
// returns how much it lifts the last quarter, in decibels.
func through(t *testing.T, p *plugin, hz, amp float64, changes []plug.Change) float64 {
	t.Helper()
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 480, Channels: 2})
	var in, out []float32
	for at := 0; at < 48000; at += 480 {
		ch := [][]float32{sine(480, at, hz, 48000), sine(480, at, hz, 48000)}
		for i := range ch {
			for k := range ch[i] {
				ch[i][k] *= float32(amp / 0.5)
			}
		}
		in = append(in, ch[0]...)
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 480, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
		out = append(out, ch[0]...)
	}
	return rms(out[36000:]) - rms(in[36000:])
}

func TestTheAmountScalesTheBands(t *testing.T) {
	for _, c := range []struct{ amount, want float64 }{{100, 6}, {50, 3}, {0, 0}, {200, 12}} {
		p, _ := newEQ(t)
		p.setBands(bell)
		if got := through(t, p, 1000, 0.1, []plug.Change{{ID: AmountID, Value: c.amount}}); math.Abs(got-c.want) > 0.2 {
			t.Errorf("at %v%% a bell of +6 dB lifts %+.2f dB, want %+.1f", c.amount, got, c.want)
		}
	}
}

func TestADynamicBandTurnsDownAndTheViewsReadHowFar(t *testing.T) {
	p, h := newEQ(t)
	p.setBands([]audio.Band{loudBell})
	h.proc = &p.proc
	// A loud sine far from the bell passes as it was: the bell does not
	// hear it.
	if got := through(t, p, 100, 0.5, nil); math.Abs(got) > 0.2 {
		t.Errorf("a loud sine at 100 Hz through a dynamic bell at 1 kHz comes out %+.2f dB", got)
	}
	// A sine at -6 dB, 14 dB over: turned down 10.5 dB.
	if got := through(t, p, 1000, 0.5, nil); math.Abs(got+10.5) > 0.5 {
		t.Errorf("a loud sine through a dynamic bell comes out %+.2f dB, want -10.5", got)
	}
	if v, ok := h.Traced(bandTrace+uint32(loudBell.ID), 47000); !ok || math.Abs(float64(v)+10.5) > 0.5 {
		t.Errorf("the views read the bell turned down %v dB (%v), want -10.5", v, ok)
	}
	if _, ok := h.Traced(bandTrace+99, 47000); ok {
		t.Error("the views read a band the EQ has not")
	}
	// The level it hears, a sine at -6 dB, traced as its negative.
	if v, ok := h.Traced(heardTrace+uint32(loudBell.ID), 47000); !ok || math.Abs(float64(v)-6.02) > 0.5 {
		t.Errorf("the views read the bell hearing %v dB (%v), want -6", -v, ok)
	}
	// The graph draws the curve as it plays.
	g := newGraph(p, h)
	g.spec.playing, g.spec.at = true, 47001
	d := g.dot(p.Bands()[0], nil)
	g.followDown()
	for range 60 {
		g.Group.Step(time.Second / 60)
	}
	if got := d.playing(1, 1).Response(1000); math.Abs(got+10.5) > 0.5 {
		t.Errorf("the graph draws the bell at %+.2f dB at 1 kHz, want -10.5", got)
	}
	if got := d.heard.Value(); math.Abs(float64(got)+6.02) > 0.5 {
		t.Errorf("the graph draws the bell hearing %+.2f dB, want -6", got)
	}
	// So does the chip, and lets the bell back up once the sound stops.
	out := &tone{level: 0.5, at: 47001}
	h.out = out
	c := newChip(p, h)
	for range 60 {
		out.at += 16
		c.Step(time.Second / 60)
	}
	if low := slices.Min(c.curve[:]); math.Abs(float64(low)+10.5) > 0.7 {
		t.Errorf("as the sound plays, the chip draws the bell down to %+.2f dB, want -10.5", low)
	}
	for range 60 {
		c.Step(time.Second / 60)
	}
	if low := slices.Min(c.curve[:]); math.Abs(float64(low)) > 0.1 {
		t.Errorf("once the sound stops, the chip draws the bell down to %+.2f dB, want 0", low)
	}
}

func TestProcessingDynamicBandsAllocatesNothing(t *testing.T) {
	p, _ := newEQ(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{sine(512, 0, 1000, 48000), sine(512, 0, 1000, 48000)}
	steps := [][]audio.Band{{loudBell}, {loudBell, bell[0]}, {bell[0]}, nil}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var made uint64
	for i := range 4 * len(steps) {
		if i%4 == 0 {
			setBands(t, p, steps[i/4])
		}
		b := &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(512 * i)},
			Changes: []plug.Change{{ID: AmountID, Value: float64(50 * (i % 4))}, {ID: QID, Value: float64(50 + 50*(i%4))},
				{ID: ThresholdID, Value: float64(i%4) - 2}, {ID: GainID, Value: float64(i % 4)}}}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pr.Process(b)
		runtime.ReadMemStats(&after)
		made += after.Mallocs - before.Mallocs
	}
	if made != 0 {
		t.Errorf("processing made %d things over %d blocks", made, 4*len(steps))
	}
}

// withUI runs fn on w's window goroutine, with a UI.
func withUI(t *testing.T, w *gunim.Window, run func(int), fn func(u *gunim.UI)) {
	t.Helper()
	gunim.RegisterPatch(w, "eq", func(_ gunim.Node, _ struct{}, u *gunim.UI) { fn(u) })
	if err := w.Client().Patch("eq", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
}

// boundsIn returns where n is in w.
func boundsIn(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) geom.Rect {
	t.Helper()
	var b geom.Rect
	withUI(t, w, run, func(u *gunim.UI) { b, _ = u.Bounds(n) })
	return b
}

// dragUp drags the middle of n up by dy.
func dragUp(t *testing.T, w *gunim.Window, run func(int), n gunim.Node, dy float32) {
	t.Helper()
	at := boundsIn(t, w, run, n).Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(0, -dy/2))})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(0, -dy))})
	w.Input(input.PointerUp{Pos: at.Add(geom.Pt(0, -dy)), Button: input.ButtonPrimary})
	run(1)
}

// fullOf returns p's full view, staged.
func fullOf(t *testing.T, p *plugin, h *host) (*full, *gunim.Window, func(int)) {
	t.Helper()
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	w, run := stage(t, geom.Sz(1200, 700), v)
	return v, w, run
}

func TestTheBandsRowPicksItsKindAndMakesItDynamic(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	withUI(t, w, run, func(u *gunim.UI) { v.g.sel = 1; v.changed(u) })
	run(30)
	if v.row.id != 1 || v.row.in.Value() < 0.99 {
		t.Fatalf("the band picked shows in a row %d, in %v", v.row.id, v.row.in.Value())
	}
	// The dynamics button: the band dynamic, its dynamics in.
	h.log = nil
	clickOn(t, w, run, v.row.dyn)
	run(30)
	if b := p.Bands()[0]; b.Dynamic != dynDefaults || v.row.dynIn.Value() < 0.99 || !v.row.dyn.Active {
		t.Fatalf("made dynamic, the band's dynamics are %+v, in %v", b.Dynamic, v.row.dynIn.Value())
	}
	// The threshold's knob turned up, as one gesture.
	h.log = nil
	dragUp(t, w, run, v.row.knobs[3], 45)
	if got := p.Bands()[0].Dynamic.Threshold; got <= dynDefaults.Threshold {
		t.Errorf("turned up, the threshold is %v, from %v", got, dynDefaults.Threshold)
	}
	if len(h.log) < 4 || h.log[0] != "edit Band 1 dynamics" || h.log[len(h.log)-1] != "end edit" {
		t.Errorf("the turn sent %v, want one gesture", h.log)
	}
	// The low cut's lamp: a cut, its Q a slope, its gain locked, and no
	// dynamics.
	lamps := boundsIn(t, w, run, v.row.kind)
	at := geom.Pt(lamps.Min.X+lamps.Size().W*3.5/6, lamps.Min.Y+8)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(30)
	if b := p.Bands()[0]; b.Kind != audio.LowCut {
		t.Fatalf("the low cut's lamp made the band a %v", b.Kind)
	}
	if v.row.knobs[2].Param().Name != "Slope" || !v.row.knobs[1].Locked || !v.row.dyn.Disabled || v.row.dynIn.Target() != 0 {
		t.Errorf("as a cut, the row turns %q, its gain locked %v, dynamics %v", v.row.knobs[2].Param().Name,
			v.row.knobs[1].Locked, v.row.dynIn.Target())
	}
	// Nothing picked: the row goes.
	withUI(t, w, run, func(u *gunim.UI) { v.g.sel = 0; v.changed(u) })
	run(30)
	if v.row.in.Value() > 0.01 {
		t.Errorf("with no band picked, the row stays in at %v", v.row.in.Value())
	}
}

func TestTheListShowsEveryBandWithAllItsKnobs(t *testing.T) {
	p, h := newEQ(t)
	two := bell[0]
	two.ID, two.Freq = 2, 4000
	p.setBands([]audio.Band{bell[0], two})
	v, w, run := fullOf(t, p, h)
	sw := boundsIn(t, w, run, v.views)
	at := geom.Pt(sw.Min.X+sw.Size().W*0.75, sw.Center().Y)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(60)
	if v.listed.Value() < 0.99 {
		t.Fatalf("switched to the bands, the list is in %v", v.listed.Value())
	}
	rowOf := func(id int) *bandRow {
		for _, r := range v.list.rows {
			if r.id == id {
				return r
			}
		}
		t.Fatalf("band %d has no row", id)
		return nil
	}
	// The second band's dynamics, from its row, picked or not.
	second := rowOf(2)
	clickOn(t, w, run, second.dyn)
	run(30)
	dragUp(t, w, run, second.knobs[7], 40)
	if b := p.Bands()[1]; !b.Dynamic.On || b.Dynamic.Range <= dynDefaults.Range {
		t.Fatalf("from its row, the second band's dynamics are %+v", b.Dynamic)
	}
	// A band added comes in a row of its own, under the rest.
	clickOn(t, w, run, v.list.add)
	run(60)
	if len(p.Bands()) != 3 || rowOf(3).in.Value() < 0.99 {
		t.Fatalf("added, the bands are %v", p.Bands())
	}
	if f := p.Bands()[2].Freq; f == 1000 || f == 4000 {
		t.Errorf("added where the bands leave room, the band is at %v Hz, on another", f)
	}
	// The first removed: its row leaves, and the rows under it glide up.
	first := rowOf(1)
	third := rowOf(3)
	clickOn(t, w, run, first.del)
	run(60)
	if first.in.Value() > 0.01 || first.id != 0 {
		t.Errorf("its band removed, the row stays at %v, of band %d", first.in.Value(), first.id)
	}
	if third.place != 1 {
		t.Errorf("the third band's row is now at %d, want 1", third.place)
	}
}

func TestMatchingToPinkNoiseReadsShowsHowFarAndStops(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	withUI(t, w, run, func(u *gunim.UI) { v.changed(u) })
	if !v.match.Disabled || v.match.Tooltip != "The host gives the EQ no track to read" {
		t.Fatalf("with no track, the match button is disabled %v: %q", v.match.Disabled, v.match.Tooltip)
	}
	// A track that holds its reading back, so it can be stopped.
	src := noise(4, 1)
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	withUI(t, w, run, func(u *gunim.UI) { v.changed(u) })
	clickOn(t, w, run, v.match)
	run(60)
	if !v.listening || v.stopIn.Value() < 0.99 || v.match.Label != "Reading the track… 0%" {
		t.Fatalf("reading, the button says %q, the stop button is in %v", v.match.Label, v.stopIn.Value())
	}
	clickOn(t, w, run, v.stop)
	close(src.gate)
	run(30)
	if v.listening || v.stopIn.Value() > 0.01 || v.match.Label != matchWords(tonal.Pink) {
		t.Fatalf("stopped, the button reads on %v, says %q, the stop button in %v", v.listening, v.match.Label, v.stopIn.Value())
	}
	// Long enough for the reading to have run through, had it not
	// stopped.
	time.Sleep(200 * time.Millisecond)
	if levels, reading, _, _ := p.readState(); reading || levels != nil {
		t.Error("stopped, the EQ read on")
	}
	// Read through: matched, as one step, the button telling so a while.
	// The gate stays closed, so every read passes; the stopped reading
	// may still be on its way out, reading src.gate.
	h.log = nil
	clickOn(t, w, run, v.match)
	// The reading runs on its own goroutine, which under -race and a
	// loaded machine takes seconds: wait for it, then for the view to
	// see it.
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(time.Millisecond) {
		if _, reading, _, _ := p.readState(); !reading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reading ran on for 2 minutes")
		}
	}
	for range 60 {
		run(1)
		if !v.listening {
			break
		}
	}
	if v.match.Label != matchedWords(tonal.Pink) || len(p.Bands()) != len(autoShape) {
		t.Fatalf("read through, the button says %q, and the bands are %v", v.match.Label, p.Bands())
	}
	if want := []string{"edit Auto EQ", "changed", "end edit"}; !slices.Equal(h.log, want) {
		t.Errorf("matching sent %v, want one step", h.log)
	}
	run(int(tellFor/(time.Second/60)) + 5)
	if v.match.Label != matchWords(tonal.Pink) {
		t.Errorf("a while after, the button still says %q", v.match.Label)
	}
}

func TestTheReadoutBesideAPointFadesInAndOut(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	g := v.g
	at := boundsIn(t, w, run, v.panel).Min.Add(g.dotAt(g.dots[1]))
	w.Input(input.PointerMove{Pos: at})
	run(2)
	if mid := g.tipIn.Value(); mid <= 0 || mid >= 1 {
		t.Errorf("2 frames after the pointer came, the readout is in %v: not fading in", mid)
	}
	run(30)
	if g.tipIn.Value() < 0.99 || g.tipBand != 1 {
		t.Fatalf("over the point, the readout is in %v, of band %d", g.tipIn.Value(), g.tipBand)
	}
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(300, 200))})
	run(2)
	if mid := g.tipIn.Value(); mid <= 0 || mid >= 1 {
		t.Errorf("2 frames after the pointer left, the readout is in %v: not fading out", mid)
	}
}

// memSource is a track in memory, of version, as a host gives a plugin.
type memSource struct {
	frames  [][]float32
	version int
	// gate, where set, holds each read until it is closed.
	gate chan struct{}
}

func (m *memSource) ID() int       { return 1 }
func (m *memSource) Version() int  { return m.version }
func (m *memSource) Name() string  { return "noise" }
func (m *memSource) Rate() float64 { return 48000 }
func (m *memSource) Channels() int { return 2 }
func (m *memSource) Frames() int64 { return int64(len(m.frames[0])) }
func (m *memSource) ReadAt(dst [][]float32, at int64) (int, error) {
	if m.gate != nil {
		<-m.gate
	}
	n := 0
	for c := range dst {
		n = copy(dst[c], m.frames[c%2][min(at, m.Frames()):])
	}
	return n, nil
}

type memDoc struct{ src *memSource }

func (d memDoc) Sources() []plug.Source { return []plug.Source{d.src} }
func (d memDoc) Regions() []plug.Region { return []plug.Region{{Source: 1, Length: d.src.Frames()}} }

// noise returns seconds of white noise at 48 kHz, as a source.
func noise(seconds, version int) *memSource {
	r := rand.New(rand.NewPCG(3, 4))
	m := &memSource{frames: [][]float32{make([]float32, seconds*48000), make([]float32, seconds*48000)}, version: version}
	for c := range m.frames {
		for i := range m.frames[c] {
			m.frames[c][i] = float32(0.2 * (2*r.Float64() - 1))
		}
	}
	return m
}

func TestTheEQReadsATracksSpectrum(t *testing.T) {
	var at float64
	levels, err := spectrumOf(context.Background(), noise(6, 1), autoFreqs, func(p float64) { at = p })
	if err != nil {
		t.Fatal(err)
	}
	// White noise's power rises 3 dB an octave, sixth-octave bands
	// summing it: a line through the levels.
	var sx, sy, sxx, sxy float64
	for i, f := range autoFreqs {
		x := math.Log2(f / 100)
		sx, sy, sxx, sxy = sx+x, sy+levels[i], sxx+x*x, sxy+x*levels[i]
	}
	n := float64(len(autoFreqs))
	if slope := (n*sxy - sx*sy) / (n*sxx - sx*sx); math.Abs(slope-3) > 0.15 {
		t.Errorf("white noise rises %.2f dB an octave, want 3", slope)
	}
	if at < 0.95 {
		t.Errorf("read whole, the reading says it got %.2f of the way", at)
	}
	silent := &memSource{frames: [][]float32{make([]float32, 48000), make([]float32, 48000)}}
	if _, err := spectrumOf(context.Background(), silent, autoFreqs, func(float64) {}); !errors.Is(err, errSilent) {
		t.Errorf("a silent track reads with %v", err)
	}
}

func TestAReadingStopsAndANewTrackIsReadAnew(t *testing.T) {
	p, _ := newEQ(t)
	if p.canRead() {
		t.Fatal("with no document the EQ says it can read")
	}
	src := noise(2, 1)
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	p.startReading()
	if _, reading, _, _ := p.readState(); !reading {
		t.Fatal("started, the EQ is not reading")
	}
	p.stopReading()
	close(src.gate)
	// Long enough for the reading to have run through, had it not
	// stopped.
	time.Sleep(200 * time.Millisecond)
	if levels, reading, _, _ := p.readState(); reading || levels != nil {
		t.Fatal("stopped, the EQ reads on, or kept what it read")
	}
	// Read through, started again at once, the reading stopped ending
	// meanwhile: it leaves the new one be.
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	p.startReading()
	p.stopReading()
	p.startReading()
	close(src.gate)
	waitRead(t, p)
	if levels, _, _, err := p.readState(); len(levels) != len(autoFreqs) || p.Analysed() != 1 || err != nil {
		t.Fatalf("read, the EQ has %d levels, says %.2f, and failed with %v", len(levels), p.Analysed(), err)
	}
	// A new version of the track: what was read of the old goes.
	p.SetDocument(memDoc{noise(2, 2)})
	if levels, _, _, _ := p.readState(); levels != nil || p.Analysed() != 0 {
		t.Error("a new version of the track keeps what was read of the old")
	}
}

// waitRead waits for p to finish reading.
func waitRead(t *testing.T, p *plugin) {
	t.Helper()
	for range 1000 {
		if _, reading, _, _ := p.readState(); !reading {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the EQ never finished reading")
}

func TestAutoReadsTheTrackAndShapesTheBands(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	// White noise, brighter than pink: Auto darkens it.
	p.SetDocument(memDoc{noise(4, 1)})
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	w, run := stage(t, geom.Sz(1200, 600), v)
	h.log = nil
	withUI(t, w, run, func(u *gunim.UI) { v.changed(u) })
	clickOn(t, w, run, v.match)
	// The reading runs on its own goroutine, which under -race and a
	// loaded machine takes seconds: wait for it, then for the view to
	// see it.
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(time.Millisecond) {
		if _, reading, _, _ := p.readState(); !reading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reading ran on for 2 minutes")
		}
	}
	for range 60 {
		run(1)
		if !v.listening {
			break
		}
	}
	if v.listening || v.match.Label != matchedWords(tonal.Pink) {
		t.Fatalf("Auto still reads %v, and says %q", v.listening, v.match.Label)
	}
	if low, high := audio.Response(p.Bands(), 100), audio.Response(p.Bands(), 8000); high > low-6 {
		t.Errorf("for white noise, Auto sets 100 Hz to %+.1f dB and 8 kHz to %+.1f dB: not darker", low, high)
	}
	if want := []string{"edit Auto EQ", "changed", "end edit"}; !slices.Equal(h.log, want) {
		t.Errorf("Auto sent %v, want one step", h.log)
	}
}

// clickOn clicks the middle of node n in w.
func clickOn(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) {
	t.Helper()
	var b geom.Rect
	gunim.RegisterPatch(w, "eq", func(_ gunim.Node, _ struct{}, u *gunim.UI) { b, _ = u.Bounds(n) })
	if err := w.Client().Patch("eq", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	at := b.Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
}

// toBands turns v's switch to the list of bands.
func toBands(t *testing.T, w *gunim.Window, run func(int), v *full) {
	t.Helper()
	sw := boundsIn(t, w, run, v.views)
	at := geom.Pt(sw.Min.X+sw.Size().W*0.75, sw.Center().Y)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(60)
}

func TestTheGraphShowsDarkenedBehindTheBands(t *testing.T) {
	p, h := newEQ(t)
	two := bell[0]
	two.ID, two.Freq = 2, 4000
	p.setBands([]audio.Band{bell[0], two})
	v, w, run := fullOf(t, p, h)
	// The band picked's row stands over the graph, centred, on glass.
	withUI(t, w, run, func(u *gunim.UI) { v.g.sel = 1; v.changed(u) })
	run(30)
	before, row := boundsIn(t, w, run, v.panel), boundsIn(t, w, run, v.row)
	if !before.Contains(row.Min) || !before.Contains(row.Max.Sub(geom.Pt(1, 1))) || math.Abs(float64(row.Center().X-before.Center().X)) > 1 || !v.row.glass {
		t.Fatalf("the band's row is at %v, over the graph at %v: not centred over it", row, before)
	}
	toBands(t, w, run, v)
	graph, list := boundsIn(t, w, run, v.panel), boundsIn(t, w, run, v.scroll)
	if v.g.behind != 1 || graph != before || graph.Max.Y != list.Max.Y {
		t.Fatalf("under the list, the graph is %v (from %v), behind %v, the list %v", graph, before, v.g.behind, list)
	}
	// A double-click where the graph shows under the list adds no band.
	at := geom.Pt(graph.Max.X-40, graph.Max.Y-40)
	for _, n := range []int{1, 2} {
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: n})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	}
	run(2)
	if len(p.Bands()) != 2 {
		t.Errorf("a double-click on the list reached the graph under it: %d bands", len(p.Bands()))
	}
	// The pointer on a band's row lights the band on the graph.
	var second *bandRow
	for _, r := range v.list.rows {
		if r.id == 2 {
			second = r
		}
	}
	w.Input(input.PointerMove{Pos: boundsIn(t, w, run, second).Center()})
	run(1)
	if v.g.lit != 2 {
		t.Errorf("the pointer on the second band's row lights band %d", v.g.lit)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(5, graph.Max.Y-5)})
	run(1)
	if v.g.lit != 0 {
		t.Errorf("the pointer off the rows still lights band %d", v.g.lit)
	}
	// A knob of the first band's row, held and dragged over the second
	// band's row, keeps the first band lit: the band it turns.
	var first *bandRow
	for _, r := range v.list.rows {
		if r.id == 1 {
			first = r
		}
	}
	knob := boundsIn(t, w, run, first.knobs[1]).Center()
	w.Input(input.PointerMove{Pos: knob})
	w.Input(input.PointerDown{Pos: knob, Button: input.ButtonPrimary, Clicks: 1})
	over := boundsIn(t, w, run, second).Center()
	w.Input(input.PointerMove{Pos: over})
	run(1)
	if v.g.lit != 1 {
		t.Errorf("a knob of the first band's row, dragged over the second's, lights band %d", v.g.lit)
	}
	w.Input(input.PointerUp{Pos: over, Button: input.ButtonPrimary})
	w.Input(input.PointerMove{Pos: over.Add(geom.Pt(1, 0))})
	run(1)
	if v.g.lit != 2 {
		t.Errorf("let go over the second band's row, the pointer lights band %d", v.g.lit)
	}
}

func TestTheTunerTurnsTheLookAndResetsIt(t *testing.T) {
	t.Cleanup(func() { plugui.Look = plugui.DefaultLook })
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	toBands(t, w, run, v)
	tn := v.tuner
	clickOn(t, w, run, tn.Button)
	run(60)
	card := boundsIn(t, w, run, tn.Card)
	if !tn.Open() || card.Size().W == 0 {
		t.Fatalf("clicked, the tuner is open %v, its card %v", tn.Open(), card)
	}
	dragUp(t, w, run, tn.Card.Knobs[0], 60)
	dragUp(t, w, run, tn.Card.Knobs[1], 60)
	if plugui.Look.Dark <= plugui.DefaultLook.Dark || plugui.Look.Blur <= plugui.DefaultLook.Blur {
		t.Fatalf("turned up, the look is %+v", plugui.Look)
	}
	// A click on the card, on none of its own controls, over the first
	// band's lamps, reaches nothing under it.
	var first *bandRow
	for _, r := range v.list.rows {
		if r.id == 1 {
			first = r
		}
	}
	lamps := boundsIn(t, w, run, first.kind)
	own := make([]geom.Rect, 0, 1+len(tn.Card.Knobs))
	own = append(own, boundsIn(t, w, run, tn.Card.Reset))
	for _, k := range tn.Card.Knobs {
		own = append(own, boundsIn(t, w, run, k))
	}
	var at geom.Point
	found := false
	for y := card.Min.Y + 2; y < card.Max.Y-2 && !found; y += 2 {
		for x := card.Min.X + 2; x < card.Max.X-2 && !found; x += 2 {
			pt := geom.Pt(x, y)
			if lamps.Contains(pt) && !slices.ContainsFunc(own, func(r geom.Rect) bool { return r.Contains(pt) }) {
				at, found = pt, true
			}
		}
	}
	if !found {
		t.Fatalf("no spot of the card, %v, lies over the lamps, %v, clear of its own controls", card, lamps)
	}
	h.log = nil
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(2)
	if len(h.log) != 0 {
		t.Errorf("a click on the tuner's card reached the bands: %v", h.log)
	}
	clickOn(t, w, run, tn.Card.Reset)
	if plugui.Look != plugui.DefaultLook || tn.Card.Knobs[0].Value() != plugui.DefaultLook.Dark*100 {
		t.Errorf("reset, the look is %+v, its darkness knob at %v", plugui.Look, tn.Card.Knobs[0].Value())
	}
	clickOn(t, w, run, tn.Button)
	run(60)
	if tn.Open() || boundsIn(t, w, run, tn.Card).Size().W != 0 {
		t.Errorf("clicked again, the tuner is still open")
	}
}

func TestTheBandsRowStandsClearOfTheFrequencies(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	withUI(t, w, run, func(u *gunim.UI) { v.g.sel = 1; v.changed(u) })
	run(30)
	graph, row := boundsIn(t, w, run, v.panel), boundsIn(t, w, run, v.row)
	// The frequencies are written in the graph's last 16 pixels, and the
	// row's card reaches 4 under the row.
	if card := row.Max.Y + 4; card > graph.Max.Y-18 {
		t.Errorf("the row's card reaches %v, over the frequencies from %v", card, graph.Max.Y-16)
	}
}

func TestTheTopControlsStandOnGlassOverTheGraph(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	graph, bar := boundsIn(t, w, run, v.panel), boundsIn(t, w, run, v.bar)
	if graph.Min != (geom.Point{}) || !graph.Contains(bar.Min) || !graph.Contains(bar.Max.Sub(geom.Pt(1, 1))) {
		t.Fatalf("the graph is %v and the bar %v: the graph not under the bar", graph, bar)
	}
	for _, n := range []gunim.Node{v.views, v.match, v.knobs[1], v.knobs[4]} {
		if b := boundsIn(t, w, run, n); !bar.Contains(b.Min) || !bar.Contains(b.Max.Sub(geom.Pt(1, 1))) {
			t.Errorf("a control along the top, %v, is off the bar, %v", b, bar)
		}
	}
	// A double-click on the bar, between its controls, adds no band to
	// the graph under it.
	at := geom.Pt(v.barAt.Center().X-60, v.barAt.Center().Y)
	for _, n := range []int{1, 2} {
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: n})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	}
	run(2)
	if len(p.Bands()) != 1 {
		t.Errorf("a double-click on the bar reached the graph: %d bands", len(p.Bands()))
	}
}

func TestTheHintStandsCentredWhereTheRowWould(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, w, run := fullOf(t, p, h)
	graph := boundsIn(t, w, run, v.panel)
	card, _ := v.hintCard(12, graph.Size())
	if math.Abs(float64(card.Center().X-graph.Center().X)) > 1 || card.Min.X < rowInset {
		t.Errorf("the hint's card is at %v in a graph %v: not centred", card, graph)
	}
	if card.Max.Y > graph.Max.Y-18 || card.Max.Y < graph.Max.Y-rowFoot-plugui.KnobH {
		t.Errorf("the hint's card is at %v: not where the row stands, clear of the frequencies at %v", card,
			graph.Max.Y-16)
	}
}

func TestTheQThresholdAndGainWorkOnEveryBand(t *testing.T) {
	// A bell of +6 dB at 1 kHz, an octave over it: narrower at 400%,
	// wider at 25%.
	for _, c := range []struct{ q, want float64 }{{100, 1}, {400, 4}, {25, 0.25}} {
		p, _ := newEQ(t)
		p.setBands(bell)
		want := audio.Response([]audio.Band{bell[0].Scaled(c.want)}, 2000)
		if got := through(t, p, 2000, 0.1, []plug.Change{{ID: QID, Value: c.q}}); math.Abs(got-want) > 0.3 {
			t.Errorf("at a Q of %v%%, a bell of +6 dB lifts %+.2f dB an octave over, want %+.2f", c.q, got, want)
		}
	}
	// A dynamic bell, 14 dB over its threshold at 4:1: with the
	// thresholds up 8 dB, 6 dB over.
	p, _ := newEQ(t)
	p.setBands([]audio.Band{loudBell})
	if got := through(t, p, 1000, 0.5, []plug.Change{{ID: ThresholdID, Value: 8}}); math.Abs(got+4.5) > 0.5 {
		t.Errorf("with the thresholds up 8 dB, a sine 14 dB over comes out %+.2f dB, want -4.5", got)
	}
	// The gain, and none of it while the EQ is bypassed.
	p, _ = newEQ(t)
	if got := through(t, p, 1000, 0.1, []plug.Change{{ID: GainID, Value: 6}}); math.Abs(got-6) > 0.05 {
		t.Errorf("at a gain of +6 dB the sound comes out %+.2f dB", got)
	}
	p, _ = newEQ(t)
	if got := through(t, p, 1000, 0.1, []plug.Change{{ID: GainID, Value: 6}, {ID: BypassID, Value: 1}}); math.Abs(got) > 0.05 {
		t.Errorf("bypassed, at a gain of +6 dB the sound comes out %+.2f dB", got)
	}
}

func TestTheViewsDrawTheQAndTheThresholds(t *testing.T) {
	p, h := newEQ(t)
	dyn := loudBell
	dyn.Gain = 6
	p.setBands([]audio.Band{dyn})
	g := newGraph(p, h)
	c := newChip(p, h)
	g.Follow(QID, 400, nil)
	g.Follow(ThresholdID, -6, nil)
	c.Follow(QID, 400, nil)
	for range 60 {
		g.Group.Step(time.Second / 60)
	}
	want := audio.Response([]audio.Band{dyn.Scaled(4)}, 2000)
	if got := g.dots[dyn.ID].playing(g.amount.Value(), g.qScale.Value()).Response(2000); math.Abs(got-want) > 0.05 {
		t.Errorf("at a Q of 400%%, the graph draws the bell at %+.2f dB an octave over, want %+.2f", got, want)
	}
	i := int(math.Round(math.Log(2000/eqLow) / math.Log(eqHigh/eqLow) * (chipPoints - 1)))
	hz := eqLow * math.Pow(eqHigh/eqLow, float64(i)/(chipPoints-1))
	if got, want := float64(c.curve[i]), audio.Response([]audio.Band{dyn.Scaled(4)}, hz); math.Abs(got-want) > 0.05 {
		t.Errorf("at a Q of 400%%, the chip draws the bell at %+.2f dB at %.0f Hz, want %+.2f", got, hz, want)
	}
	if g.shift.Value() != -6 {
		t.Errorf("the thresholds moved down 6 dB, the graph draws them moved %v", g.shift.Value())
	}
	// A bell hears an octave about its frequency at a Q of 1.41, and a
	// shelf from its frequency out.
	from, to := g.heardSpan(audio.Band{Kind: audio.Bell, Freq: 1000, Q: 1.4142})
	if math.Abs(from-707.1) > 1 || math.Abs(to-1414.2) > 1 {
		t.Errorf("a bell at 1 kHz of Q 1.41 hears %.1f to %.1f Hz, want an octave about it", from, to)
	}
	if from, to := g.heardSpan(audio.Band{Kind: audio.HighShelf, Freq: 8000, Q: 0.7}); from != 8000 || to != eqHigh {
		t.Errorf("a high shelf at 8 kHz hears %v to %v Hz", from, to)
	}
}

func TestOnlyTheBandInFocusWritesItsThreshold(t *testing.T) {
	p, h := newEQ(t)
	a, b := loudBell, loudBell
	b.ID, b.Freq = 8, 4000
	p.setBands([]audio.Band{a, b})
	g := newGraph(p, h)
	g.sel = a.ID
	if g.focusOf(a.ID) != 1 || g.focusOf(b.ID) != 0 {
		t.Errorf("band %d picked, the two stand out %v and %v", a.ID, g.focusOf(a.ID), g.focusOf(b.ID))
	}
	g.hover(b.ID, nil)
	for range 30 {
		g.Group.Step(time.Second / 60)
	}
	if g.focusOf(b.ID) < 0.99 {
		t.Errorf("the pointer on band %d, it stands out %v", b.ID, g.focusOf(b.ID))
	}
}

func TestTheMatchButtonSaysLessWhereTheBarIsNarrow(t *testing.T) {
	for _, c := range []struct {
		w      float32
		words  string
		hidden bool
	}{{1200, matchWords(tonal.Pink), false}, {560, matchShort(tonal.Pink), false}, {380, matchShort(tonal.Pink), true}} {
		p, h := newEQ(t)
		v, ok := p.View(plug.Full, h).(*full)
		if !ok {
			t.Fatal("the full view is of another type")
		}
		w, run := stage(t, geom.Sz(c.w, 500), v)
		run(10)
		views, match := boundsIn(t, w, run, v.views), boundsIn(t, w, run, v.match)
		// Hidden, it is not drawn, so it stands nowhere.
		hidden := match.Size().W == 0 && v.matchAt.Size().W == 0
		if v.match.Label != c.words || hidden != c.hidden {
			t.Errorf("%v wide, the match button says %q at %v, hidden %v", c.w, v.match.Label, match, hidden)
		}
		if !c.hidden && match.Min.X < views.Max.X+gap+32 {
			t.Errorf("%v wide, the match button at %v runs into the switch at %v and the tuner after it", c.w, match, views)
		}
	}
}

func TestTheMenuBesideTheMatchButtonPicksTheTarget(t *testing.T) {
	p, h := newEQ(t)
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	w, run := stage(t, geom.Sz(1200, 500), v)
	run(10)
	if v.match.Label != matchWords(tonal.Pink) || !v.target.Items()[1].Checked || !v.knobs[0].Disabled {
		t.Fatalf("at first, the button says %q, the menu ticks %v, and the knee is greyed %v", v.match.Label,
			v.target.Items(), v.knobs[0].Disabled)
	}
	if b := boundsIn(t, w, run, v.knobs[0]); b.Size().W != 0 {
		t.Errorf("for pink, the knee's knob takes room on the bar, %v", b)
	}
	// The caption, then pink, then the music slope.
	withUI(t, w, run, func(u *gunim.UI) { v.target.OnPick(2, u) })
	bar := boundsIn(t, w, run, v.bar)
	if b := boundsIn(t, w, run, v.knobs[0]); b.Size().W == 0 || !bar.Contains(b.Min) {
		t.Errorf("for the music slope, the knee's knob is at %v, not on the bar, %v", b, bar)
	}
	if h.values[TargetID] != 1 || v.match.Label != matchWords(tonal.Music) || !v.target.Items()[2].Checked ||
		v.target.Items()[1].Checked || v.knobs[0].Disabled {
		t.Errorf("the music slope picked, the target is %v, the button says %q, the menu ticks %v, and the knee "+
			"is greyed %v", h.values[TargetID], v.match.Label, v.target.Items(), v.knobs[0].Disabled)
	}
	if v.knobs[0].Param().ID != KneeID || v.to().Knee != tonal.Knee {
		t.Errorf("the first knob is %v, and the target's knee %v", v.knobs[0].Param().Name, v.to().Knee)
	}
	h.values[KneeID] = 800
	if v.to().Knee != 800 {
		t.Errorf("the knee set to 800 Hz, the target's is %v", v.to().Knee)
	}
	// Set back by the host, as by Undo.
	h.values[TargetID] = 0
	withUI(t, w, run, func(u *gunim.UI) { v.Follow(TargetID, 0, u) })
	if v.match.Label != matchWords(tonal.Pink) || !v.target.Items()[1].Checked || !v.knobs[0].Disabled {
		t.Errorf("set back to pink, the button says %q, the menu ticks %v, and the knee is greyed %v", v.match.Label,
			v.target.Items(), v.knobs[0].Disabled)
	}
}
