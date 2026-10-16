package braid

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
	// in and out are the sounds the views hear go in and come out, or
	// nil.
	in, out plug.Sound
}

func (h *host) Param(id uint32) float64 { return h.values[id] }
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)                { h.log = append(h.log, "end") }
func (h *host) BeginEdit(name string, _ *gunim.UI)   { h.log = append(h.log, "edit "+name) }
func (h *host) Changed(*gunim.UI)                    { h.log = append(h.log, "changed") }
func (h *host) EndEdit(*gunim.UI)                    { h.log = append(h.log, "end edit") }
func (h *host) Sound() (in, out plug.Sound)          { return h.in, h.out }
func (h *host) Traced(uint32, int64) (float32, bool) { return 0, false }

func newPlugin(t *testing.T) (plug.Plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.braid")
	if !ok {
		t.Fatal("the braid did not register itself")
	}
	h := &host{values: map[uint32]float64{}}
	for _, p := range d.Params {
		h.values[p.ID] = p.Default
	}
	return d.New(h), h
}

// defaults are every parameter at its default, as the first block after
// Prepare brings them.
func defaults() []plug.Change {
	cs := make([]plug.Change, 0, len(Descriptor.Params))
	for _, p := range Descriptor.Params {
		cs = append(cs, plug.Change{ID: p.ID, Value: p.Default})
	}
	return cs
}

// wave returns n frames of a channel, each different, as a sound is.
func wave(n int, f float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(0.8 * math.Sin(float64(i)*f+f))
	}
	return s
}

// run prepares a fresh braid and plays one block of l and r through it,
// with the defaults and then changes at frame 0, and returns what came
// out.
func run(t *testing.T, l, r []float32, changes ...plug.Change) (outL, outR []float32) {
	t.Helper()
	p, _ := newPlugin(t)
	pr := p.Processor()
	n := len(l)
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: n, Channels: 2})
	outL, outR = make([]float32, n), make([]float32, n)
	pr.Process(&plug.Block{In: [][]float32{l, r}, Out: [][]float32{outL, outR}, Frames: n,
		Changes: append(defaults(), changes...)})
	return outL, outR
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-6 }

func TestTheBraidListsAsAGainAndStereoTool(t *testing.T) {
	d, _ := plug.Lookup("marrasen.braid")
	if d.Name != "Braid" || d.Tagline != "Gain & stereo" || d.Instrument() || d.Notes {
		t.Fatalf("the braid lists as %q, %q, instrument %v, notes %v", d.Name, d.Tagline, d.Instrument(), d.Notes)
	}
	for _, k := range []string{"mono", "phase", "trim", "pan"} {
		if !slices.Contains(d.Keywords, k) {
			t.Errorf("%q does not find the braid", k)
		}
	}
	if _, ok := plug.Lookup("marrasen.trim"); ok {
		t.Error("the trim is still registered")
	}
}

func TestAtItsDefaultsTheSoundGoesThroughExactly(t *testing.T) {
	l, r := wave(512, 0.01), wave(512, 0.037)
	outL, outR := run(t, l, r)
	if !slices.Equal(outL, l) || !slices.Equal(outR, r) {
		t.Fatal("at the defaults the sound changed")
	}
}

func TestEachInvertFlipsItsSideAlone(t *testing.T) {
	l, r := wave(256, 0.01), wave(256, 0.037)
	outL, outR := run(t, l, r, plug.Change{ID: InvertLID, Value: 1})
	for i := range l {
		if outL[i] != -l[i] || outR[i] != r[i] {
			t.Fatalf("with the left inverted, frame %d is %v, %v; want %v, %v", i, outL[i], outR[i], -l[i], r[i])
		}
	}
	outL, outR = run(t, l, r, plug.Change{ID: InvertRID, Value: 1})
	for i := range l {
		if outL[i] != l[i] || outR[i] != -r[i] {
			t.Fatalf("with the right inverted, frame %d is %v, %v; want %v, %v", i, outL[i], outR[i], l[i], -r[i])
		}
	}
}

func TestSumPlaysHalfOfEachSideOnBoth(t *testing.T) {
	l, r := wave(256, 0.01), wave(256, 0.037)
	outL, outR := run(t, l, r, plug.Change{ID: MonoID, Value: MonoSum})
	for i := range l {
		want := (l[i] + r[i]) / 2
		if !near(outL[i], want) || !near(outR[i], want) {
			t.Fatalf("summed, frame %d is %v, %v; want %v on both", i, outL[i], outR[i], want)
		}
	}
	// With one side inverted, the sum is the difference: the sides.
	outL, _ = run(t, l, r, plug.Change{ID: MonoID, Value: MonoSum}, plug.Change{ID: InvertRID, Value: 1})
	if want := (l[100] - r[100]) / 2; !near(outL[100], want) {
		t.Errorf("summed with the right inverted, %v; want the difference %v", outL[100], want)
	}
}

func TestLeftOnlyAndRightOnlyDropTheOtherSideCompletely(t *testing.T) {
	l, noise := wave(256, 0.01), wave(256, 1.3)
	// The pans stand aside: Left only plays the left on both sides
	// wherever they are.
	outL, outR := run(t, l, noise, plug.Change{ID: MonoID, Value: MonoLeft}, plug.Change{ID: PanLID, Value: 30})
	if !slices.Equal(outL, l) || !slices.Equal(outR, l) {
		t.Fatal("Left only does not play the left alone, exactly, on both sides")
	}
	outL, outR = run(t, noise, l, plug.Change{ID: MonoID, Value: MonoRight})
	if !slices.Equal(outL, l) || !slices.Equal(outR, l) {
		t.Fatal("Right only does not play the right alone, exactly, on both sides")
	}
	// Silence on the kept side leaves silence: none of the noise.
	outL, outR = run(t, make([]float32, 256), noise, plug.Change{ID: MonoID, Value: MonoLeft})
	for i := range outL {
		if outL[i] != 0 || outR[i] != 0 {
			t.Fatalf("the dropped side's noise came through at frame %d: %v, %v", i, outL[i], outR[i])
		}
	}
}

func TestEachChannelPansOnItsOwnAtConstantPower(t *testing.T) {
	for _, c := range []struct {
		pan         float64
		left, right float64
	}{{-100, 1, 0}, {0, math.Sqrt2 / 2, math.Sqrt2 / 2}, {100, 0, 1}} {
		l, r := PanGains(c.pan)
		if math.Abs(l-c.left) > 1e-12 || math.Abs(r-c.right) > 1e-12 {
			t.Errorf("at %v the pan gains are %v, %v; want %v, %v", c.pan, l, r, c.left, c.right)
		}
	}
	// The left in the middle, the right swapped over to the left.
	l, r := wave(64, 0.01), wave(64, 0.037)
	outL, outR := run(t, l, r, plug.Change{ID: PanLID, Value: 0}, plug.Change{ID: PanRID, Value: -100})
	h := float32(math.Sqrt2 / 2)
	if !near(outL[10], l[10]*h+r[10]) || !near(outR[10], l[10]*h) {
		t.Errorf("panned, frame 10 is %v, %v; want %v, %v", outL[10], outR[10], l[10]*h+r[10], l[10]*h)
	}
	if panText(-40) != "L40" || panText(0) != "C" || panText(100) != "R100" {
		t.Errorf("pans read %q, %q, %q", panText(-40), panText(0), panText(100))
	}
}

func TestTheLowestGainIsSilenceAndTheHighestIs48dB(t *testing.T) {
	l, r := wave(64, 0.01), wave(64, 0.037)
	for _, v := range []float64{GainOff, -500} {
		outL, outR := run(t, l, r, plug.Change{ID: GainID, Value: v})
		for i := range outL {
			if outL[i] != 0 || outR[i] != 0 {
				t.Fatalf("at %v dB frame %d is %v, %v; want silence", v, i, outL[i], outR[i])
			}
		}
	}
	outL, _ := run(t, l, r, plug.Change{ID: GainID, Value: 100})
	if want := l[10] * float32(math.Pow(10, 48.0/20)); math.Abs(float64(outL[10]-want)) > 1e-3 {
		t.Errorf("at +100 dB the gain gives %v, want +48 dB's %v", outL[10], want)
	}
	if gainText(GainOff) != "-∞ dB" || gainText(-3) != "-3.0 dB" {
		t.Errorf("gains read %q and %q", gainText(GainOff), gainText(-3))
	}
}

func TestAChangeCrossfadesWithoutAJump(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 4800, Channels: 2})
	ones := func(n int) []float32 {
		s := make([]float32, n)
		for i := range s {
			s[i] = 0.5
		}
		return s
	}
	for _, c := range []plug.Change{{Frame: 100, ID: InvertLID, Value: 1}, {Frame: 100, ID: MonoID, Value: MonoRight},
		{Frame: 100, ID: GainID, Value: GainOff}} {
		pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 4800, Channels: 2})
		pr.Process(&plug.Block{In: [][]float32{ones(10), ones(10)}, Out: [][]float32{make([]float32, 10), make([]float32, 10)},
			Frames: 10, Changes: defaults()})
		l, r := ones(4800), make([]float32, 4800)
		outL, outR := make([]float32, 4800), make([]float32, 4800)
		pr.Process(&plug.Block{In: [][]float32{l, r}, Out: [][]float32{outL, outR}, Frames: 4800, Changes: []plug.Change{c}})
		for i := 1; i < len(outL); i++ {
			if math.Abs(float64(outL[i]-outL[i-1])) > 0.01 || math.Abs(float64(outR[i]-outR[i-1])) > 0.01 {
				t.Fatalf("change %d jumped at frame %d: %v to %v", c.ID, i, outL[i-1], outL[i])
			}
		}
		if math.Abs(float64(outL[0]-0.5)) > 1e-6 {
			t.Errorf("before change %d the left was %v", c.ID, outL[0])
		}
		// 100 ms on, the change has arrived.
		want := map[uint32]float32{InvertLID: -0.5, MonoID: 0, GainID: 0}[c.ID]
		if math.Abs(float64(outL[4799]-want)) > 1e-4 {
			t.Errorf("change %d leaves the left at %v, want %v", c.ID, outL[4799], want)
		}
	}
}

func TestAMonoSoundTakesTheGainAndTheLeftsInvert(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1})
	in, out := wave(64, 0.01), make([]float32, 64)
	pr.Process(&plug.Block{In: [][]float32{in}, Out: [][]float32{out}, Frames: 64,
		Changes: append(defaults(), plug.Change{ID: InvertLID, Value: 1}, plug.Change{ID: GainID, Value: -6})})
	g := float32(math.Pow(10, -6.0/20))
	if !near(out[10], -in[10]*g) {
		t.Errorf("a mono sound comes out %v, want %v", out[10], -in[10]*g)
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 256, Channels: 2})
	b := &plug.Block{In: [][]float32{wave(256, 0.1), wave(256, 0.2)}, Out: [][]float32{make([]float32, 256), make([]float32, 256)},
		Frames: 256, Changes: []plug.Change{{Frame: 3, ID: PanLID, Value: 10}, {Frame: 9, ID: MonoID, Value: MonoSum}}}
	if n := testing.AllocsPerRun(50, func() { pr.Process(b) }); n != 0 {
		t.Errorf("a block allocates %v times", n)
	}
}

func TestTheBraidKeepsNoState(t *testing.T) {
	p, _ := newPlugin(t)
	if p.State() != nil || p.SetState(nil) != nil {
		t.Error("a braid has state")
	}
	if p.SetState([]byte("x")) == nil {
		t.Error("a braid took state it cannot have")
	}
}

func TestTheGainKnobTurnsMostOfItsWayNearUnity(t *testing.T) {
	g := param(GainID)
	for _, c := range []struct{ db, at float64 }{{GainOff, 0}, {-24, 0.1}, {0, 0.5}, {24, 0.9}, {48, 1}} {
		if at := gainTaper.To(g.Normal(c.db)); math.Abs(at-c.at) > 1e-9 {
			t.Errorf("%v dB turns the knob %v of the way, want %v", c.db, at, c.at)
		}
	}
	for at := 0.0; at <= 1; at += 0.05 {
		if back := gainTaper.To(gainTaper.From(at)); math.Abs(back-at) > 1e-9 {
			t.Fatalf("%v of the way round comes back as %v", at, back)
		}
	}
}

func TestTheChipShowsTheGainAndMarks(t *testing.T) {
	p, h := newPlugin(t)
	h.values[GainID] = -3
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the braid has no views")
	}
	if v.View(plug.Full, h) != nil {
		t.Error("the braid has a full view of its own")
	}
	c, ok := v.View(plug.Chip, h).(*chip)
	if !ok || c.label.Text != "-3.0 dB" {
		t.Fatalf("the chip shows %q, want -3.0 dB", c.label.Text)
	}
	c.Follow(InvertRID, 1, nil)
	c.Follow(MonoID, MonoSum, nil)
	c.Follow(GainID, 1.5, nil)
	if c.label.Text != "+1.5 dB Ø M" {
		t.Errorf("the chip follows to %q, want +1.5 dB Ø M", c.label.Text)
	}
}

func panelOf(t *testing.T) (*panel, *host) {
	t.Helper()
	p, h := newPlugin(t)
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the braid has no views")
	}
	pn, ok := v.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	return pn, h
}

func TestThePanelsControlsWorkThroughTheHost(t *testing.T) {
	pn, h := panelOf(t)
	var u *gunim.UI
	pn.invert[1].OnChange(true, u)
	pn.mono.OnPick(MonoLeft, u)
	want := []string{"begin", "set", "end", "begin", "set", "end"}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	if h.values[InvertRID] != 1 || h.values[MonoID] != MonoLeft {
		t.Errorf("the host holds invert R %v and mono %v", h.values[InvertRID], h.values[MonoID])
	}
	if !pn.pan[0].Disabled || !pn.pan[1].Disabled {
		t.Error("with Left only the pan knobs are not greyed")
	}
	pn.Follow(MonoID, MonoOff, u)
	pn.Follow(GainID, 6, u)
	pn.Follow(InvertLID, 1, u)
	if pn.mono.Selected() != MonoOff || pn.gain.Value() != 6 || !pn.invert[0].Checked() || pn.pan[0].Disabled {
		t.Error("the panel does not follow values set elsewhere")
	}
}

// ribbons paints pn's ribbons at size, its look settled, and returns
// them.
func ribbons(t *testing.T, pn *panel, size geom.Size) []*paint.RibbonOp {
	t.Helper()
	pn.look.aim(pn.set, nil)
	pn.size = size
	l := &pn.lay
	l.inX, l.outX = edge, size.W-edge-8
	l.x0, l.x1 = 30, size.W-30
	l.invert, l.pan, l.mono, l.gain = 80, 160, 300, 420
	l.lane = [2]float32{100, 180}
	l.mid = 140
	var p paint.Painter
	pn.paintRibbons(&p, theme.NewLive(widget.Dark()))
	var rs []*paint.RibbonOp
	for _, op := range p.Ops() {
		if r, ok := op.(*paint.RibbonOp); ok {
			rs = append(rs, r)
		}
	}
	return rs
}

// widthAt returns how wide r is nearest x.
func widthAt(r *paint.RibbonOp, x float32) float32 {
	best := r.Points[0]
	for _, pt := range r.Points {
		if math.Abs(float64(pt.At.X-x)) < math.Abs(float64(best.At.X-x)) {
			best = pt
		}
	}
	return best.Width
}

func TestTheRibbonsShowTheWayTheSoundGoes(t *testing.T) {
	pn, _ := panelOf(t)
	size := geom.Sz(520, 260)
	// At the defaults: two ribbons in, and a strand each straight out.
	rs := ribbons(t, pn, size)
	if len(rs) != 4 {
		t.Fatalf("at the defaults %d ribbons, want 2 in and 2 out", len(rs))
	}
	for _, r := range rs[2:] {
		if y := r.Points[len(r.Points)-1].At.Y; y != r.Points[0].At.Y {
			t.Errorf("a strand at the defaults moves from %v to %v", r.Points[0].At.Y, y)
		}
	}
	// An invert pinches its ribbon at the lamp.
	pn.Follow(InvertLID, 1, nil)
	rs = ribbons(t, pn, size)
	if w := widthAt(rs[1], 80); w > 2 {
		t.Errorf("the left ribbon is %v wide at its invert, want a twist", w)
	}
	if w := widthAt(rs[0], 80); w != ribbonW {
		t.Errorf("the right ribbon is %v wide at the left's invert, want %v", w, float32(ribbonW))
	}
	// The left in the middle: two strands from it.
	pn.Follow(InvertLID, 0, nil)
	pn.Follow(PanLID, 0, nil)
	if rs = ribbons(t, pn, size); len(rs) != 5 {
		t.Fatalf("with the left in the middle %d ribbons, want 5", len(rs))
	}
	// Left only: the right ribbon drawn back, and the left to both
	// sides as wide as it came.
	pn.Follow(MonoID, MonoLeft, nil)
	rs = ribbons(t, pn, size)
	if len(rs) != 4 {
		t.Fatalf("with Left only %d ribbons, want 2 in and the left's 2 out", len(rs))
	}
	if w := widthAt(rs[0], 120); w != 0 {
		t.Errorf("the dropped right ribbon is %v wide before its pan, want none", w)
	}
	for _, r := range rs[2:] {
		if w := widthAt(r, 380); math.Abs(float64(w-ribbonW)) > 1e-3 {
			t.Errorf("a strand of the left alone is %v wide, want %v", w, float32(ribbonW))
		}
	}
	// No sound at all: the strands pinched to nothing past the gain.
	pn.Follow(GainID, GainOff, nil)
	for _, r := range ribbons(t, pn, size)[2:] {
		if w := widthAt(r, 480); w != 0 {
			t.Errorf("at -∞ a strand is %v wide past the gain", w)
		}
	}
}

// steady is a sound at one level, heard to frame at.
type steady struct {
	level float32
	at    int64
}

func (s *steady) Heard(dst []float32) (frames int, at int64, rate int) {
	for i := range dst {
		dst[i] = s.level
	}
	return len(dst) / 2, s.at, 48000
}

func TestTheChipMetersTheSoundAndRestsWhenItStops(t *testing.T) {
	p, h := newPlugin(t)
	s := &steady{level: 0.5}
	h.out = s
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the braid has no views")
	}
	c, ok := v.View(plug.Chip, h).(*chip)
	if !ok {
		t.Fatal("the chip is of another type")
	}
	frame := time.Second / 60
	for range 60 {
		s.at += 800
		if !c.Step(frame) {
			t.Fatal("the chip rests while the sound plays")
		}
	}
	if rms := c.out.levels.RMS[0]; math.Abs(float64(rms)+6.02) > 0.5 {
		t.Fatalf("a steady half of full scale meters %v dB, want -6", rms)
	}
	rested := false
	for range 60 * 20 {
		if !c.Step(frame) {
			rested = true
			break
		}
	}
	if !rested {
		t.Fatal("the chip never rests once the sound stops")
	}
}

func TestTheLightFlowsOnlyWhileSoundComesIn(t *testing.T) {
	pn, h := panelOf(t)
	s := &steady{level: 0.5}
	h.in = s
	pn.in = newMeter(s)
	frame := time.Second / 60
	for range 30 {
		s.at += 800
		pn.Step(frame)
	}
	if pn.flow < 0.4 {
		t.Fatalf("after half a second of sound the light flowed %v s", pn.flow)
	}
	was := pn.flow
	for range 60 * 20 {
		if !pn.Step(frame) {
			break
		}
	}
	if pn.Step(frame) {
		t.Fatal("the panel never rests once the sound stops")
	}
	// The listener counts the sound as playing a moment past its last
	// chunk, as chunks come apart.
	if pn.flow-was > 0.5 {
		t.Errorf("the light flowed on %v s after the sound stopped", pn.flow-was)
	}
}

func TestAnEffectPlaysTheSameWithNotesInItsBlocks(t *testing.T) {
	play := func(events []plug.Event) []float32 {
		p, _ := newPlugin(t)
		pr := p.Processor()
		pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
		ch := [][]float32{wave(512, 0.01), wave(512, 0.02)}
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512, Events: events,
			Changes: append(defaults(), plug.Change{ID: GainID, Value: -6}, plug.Change{Frame: 100, ID: GainID, Value: 3})})
		return ch[0]
	}
	notes := []plug.Event{{Frame: 10, Kind: plug.NoteOn, Note: 1, Pitch: 60, Velocity: 1},
		{Frame: 200, Kind: plug.Pedal, Value: 1}, {Frame: 300, Kind: plug.NoteOff, Note: 1}}
	if !slices.Equal(play(nil), play(notes)) {
		t.Errorf("the braid's sound changed with notes in its block")
	}
}
