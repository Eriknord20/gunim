package plugui

import (
	"math"
	"slices"
	"testing"

	"github.com/marrasen/gunim/audioui"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
}

func (h *host) Param(id uint32) float64 { return h.values[id] }
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)                { h.log = append(h.log, "end") }
func (h *host) BeginEdit(string, *gunim.UI)          {}
func (h *host) Changed(*gunim.UI)                    {}
func (h *host) EndEdit(*gunim.UI)                    {}
func (h *host) Sound() (in, out plug.Sound)          { return nil, nil }
func (h *host) Traced(uint32, int64) (float32, bool) { return 0, false }

var release = plug.Param{ID: 3, Name: "Release", Min: 10, Max: 1000, Default: 100, Log: true}

func TestADragTurnsTheKnobAsOneGesture(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 1}, nil)
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 55)}, nil)
	// Up half its throw: by ratios, the geometric mean, 100.
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 10)}, nil)
	k.Handle(input.PointerUp{Pos: geom.Pt(20, 10), Button: input.ButtonPrimary}, nil)
	if !slices.Equal(h.log, []string{"begin", "set", "set", "end"}) {
		t.Errorf("the drag sent %v, want one gesture", h.log)
	}
	if v := h.values[3]; math.Abs(v-100) > 1e-9 {
		t.Errorf("up half its throw the knob is at %v, want 100", v)
	}
	// Held, it ignores the value set elsewhere; let go, it follows.
	k.held = true
	k.Follow(500, nil)
	k.held = false
	if k.Value() != 100 {
		t.Errorf("held, the knob followed to %v", k.Value())
	}
	k.Follow(500, nil)
	if k.Value() != 500 {
		t.Errorf("the knob follows to %v, want 500", k.Value())
	}
}

func TestKeysAndDoubleClickStepTheKnob(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	k.Handle(input.KeyPress{Key: input.KeyEnd}, nil)
	if h.values[3] != 1000 {
		t.Errorf("End took the knob to %v, want 1000", h.values[3])
	}
	// The wheel passes the knob by, to scroll the pane it is in.
	if k.Handle(input.Scroll{Delta: geom.Pt(0, 1)}, nil) || h.values[3] != 1000 {
		t.Errorf("the wheel turned the knob to %v, want it left at 1000", h.values[3])
	}
	k.Handle(input.KeyPress{Key: input.KeyDown}, nil)
	if v := h.values[3]; v >= 1000 || v < 900 {
		t.Errorf("a step of Down took the knob to %v", v)
	}
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 20), Button: input.ButtonPrimary, Clicks: 2}, nil)
	if h.values[3] != 100 {
		t.Errorf("a double click took the knob to %v, want its default, 100", h.values[3])
	}
	if want := []string{"begin", "set", "end", "begin", "set", "end", "begin", "set", "end"}; !slices.Equal(h.log, want) {
		t.Errorf("the steps sent %v, want a gesture each", h.log)
	}
	if a := k.Access(); a.Role != access.RoleSlider || a.Name != "Release" || a.Range.Value != 100 {
		t.Errorf("the knob reads as %+v", a)
	}
	k.AccessAct(access.Request{SetValue: true, Value: 20}, nil)
	if h.values[3] != 20 {
		t.Errorf("a screen reader set the knob to %v, want 20", h.values[3])
	}
}

func TestTheChipsScaleShowsSmallChangesPlainly(t *testing.T) {
	for _, c := range []struct{ v, want float64 }{{0, 0}, {0.75, 0.25}, {-3, -0.5}, {12, 1}, {-40, -1}} {
		if got := ChipScale(c.v, 12); math.Abs(float64(got)-c.want) > 1e-6 {
			t.Errorf("of 12, %v is drawn at %v, want %v", c.v, got, c.want)
		}
	}
}

func TestChipTextIsSmallAndCutShort(t *testing.T) {
	c := NewChipText("-12.5 dB")
	whole := c.Layout(gunim.Constraints{Max: geom.Sz(400, 40)}, gunim.Frame{}, gunim.Children{})
	cut := c.Layout(gunim.Constraints{Max: geom.Sz(20, 40)}, gunim.Frame{}, gunim.Children{})
	if whole.W < 30 || whole.H > ChipTextSize+5 || cut.W != 20 {
		t.Errorf("a chip's words stand %v, and %v in 20 pixels", whole, cut)
	}
	if c.Access().Name != "-12.5 dB" {
		t.Errorf("a chip's words read as %q", c.Access().Name)
	}
}

func TestATrimPotsSlotTurnsWithItsValue(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	tp := NewTrimPot(h, release)
	if s := tp.Layout(gunim.Constraints{Max: geom.Sz(100, 100)}, gunim.Frame{}, gunim.Children{}); s != geom.Sz(TrimPotSize, TrimPotSize) {
		t.Errorf("a trim pot stands %v", s)
	}
	for _, c := range []struct{ v, deg float64 }{{10, -135}, {1000, 135}, {100, 0}} {
		tp.Follow(c.v, nil)
		if got := tp.slotAngle() * 180 / math.Pi; math.Abs(got-c.deg) > 0.5 {
			t.Errorf("at %v its slot turns %.1f°, want %v", c.v, got, c.deg)
		}
	}
}

func TestAPowerLampSwitchesAndGlides(t *testing.T) {
	p := NewPower("gate")
	var told []bool
	p.OnChange = func(on bool, _ *gunim.UI) gunim.Intent {
		told = append(told, on)
		return nil
	}
	if p.Checked() || p.tooltip() != "Switch the gate on" {
		t.Errorf("new, it is on %v and says %q", p.Checked(), p.tooltip())
	}
	p.SetChecked(true, nil)
	if !p.Checked() || p.on.Value() != 1 || p.tooltip() != "Switch the gate off" {
		t.Errorf("set on, it is on %v, lit %v, and says %q", p.Checked(), p.on.Value(), p.tooltip())
	}
	if s := p.Layout(gunim.Constraints{Max: geom.Sz(100, 100)}, gunim.Frame{}, gunim.Children{}); s != geom.Sz(PowerSize, PowerSize) {
		t.Errorf("it stands %v", s)
	}
	if info := p.Access(); info.Role != access.RoleSwitch || info.State != access.StateChecked || info.Name != "gate" {
		t.Errorf("it reads as %+v", info)
	}
}

func TestASectionsPowerLampStandsBeforeItsName(t *testing.T) {
	d := &plug.Descriptor{Params: []plug.Param{{ID: 1, Name: "On", Max: 1, Steps: 1}, {ID: 2, Name: "A", Max: 1}}}
	h := &host{values: map[uint32]float64{1: 1}}
	s := NewSection(h, d, "COMPRESSOR", 1, 2)
	if s.Switch == nil || !s.Switch.Checked() || s.Switch.Name != "compressor" {
		t.Fatalf("a section switched by a parameter on has %+v", s.Switch)
	}
	s.Switch.OnChange(false, nil)
	if h.values[1] != 0 || !s.Knobs[0].Disabled {
		t.Errorf("switched off, the parameter is %v and its knob greyed %v", h.values[1], s.Knobs[0].Disabled)
	}
}

func TestASectionIsAsWideAsItsName(t *testing.T) {
	d := &plug.Descriptor{Params: []plug.Param{{ID: 1, Name: "On", Max: 1, Steps: 1}, {ID: 2, Name: "A", Max: 1}}}
	h := &host{values: map[uint32]float64{1: 1}}
	short := NewSection(h, d, "EQ", 0, 2)
	if want := float32(KnobW + 2*SectionPad); short.Width() != want {
		t.Errorf("a knob under a short name stands %v wide, want %v", short.Width(), want)
	}
	long := NewSection(h, d, "A NAME FAR WIDER THAN ONE KNOB", 1, 2)
	name := audioui.Shaped(long.Name, sectionText, true, false).Advance
	if w := long.Width(); w < SectionPad+PowerSize+name+SectionPad {
		t.Errorf("a knob under a long name, after a lamp, stands %v wide, short of its name's %v", w, name)
	}
}

func TestAnActionDialShowsItsTaskRunAndEnd(t *testing.T) {
	d := NewActionDial("Learn", nil)
	clicks := 0
	d.OnClick = func(*gunim.UI) { clicks++ }
	if s := d.Layout(gunim.Constraints{Max: geom.Sz(200, 200)}, gunim.Frame{}, gunim.Children{}); s != geom.Sz(KnobW, KnobH) {
		t.Errorf("a dial stands %v, want as a knob", s)
	}
	d.Start(nil)
	d.SetProgress(0.4, nil)
	if !d.Running() || d.progress.Value() != 0.4 {
		t.Errorf("running, it runs %v at %v", d.Running(), d.progress.Value())
	}
	d.Finish(true, nil)
	if d.Running() {
		t.Error("finished, it still runs")
	}
	d.Disabled = true
	d.click(nil)
	if clicks != 0 {
		t.Error("disabled, a click clicked it")
	}
	if info := d.Access(); info.Role != access.RoleButton || info.Name != "Learn" || info.State != access.StateDisabled {
		t.Errorf("it reads as %+v", info)
	}
}

func TestTheHistoryFillsToItsLeftEdgeAsItCatchesUp(t *testing.T) {
	hs := &History{}
	const w = 1060
	for _, lag := range []float64{0, 0.5, 3, historyCatchUp} {
		hs.n = 2000
		hs.shown = float64(hs.n) - lag
		if x0, _ := hs.columnAt(hs.firstShown(), w); x0 > 0 {
			t.Errorf("lagging %v columns, the first column drawn starts at %v: the left edge is bare", lag, x0)
		}
	}
	// Fewer columns than fill it: they start at the first.
	hs.n, hs.shown = 10, 10
	if hs.firstShown() != 0 {
		t.Errorf("with 10 columns, the first drawn is %d", hs.firstShown())
	}
}

func TestATaperTurnsTheKnobUnevenly(t *testing.T) {
	gain := plug.Param{ID: 4, Name: "Gain", Min: -10, Max: 10}
	h := &host{values: map[uint32]float64{4: 0}}
	k := NewKnob(h, gain)
	// The top half of the turn spans the top tenth of the values.
	k.Taper = &Taper{
		To: func(n float64) float64 {
			if n <= 0.9 {
				return n / 0.9 / 2
			}
			return 0.5 + (n-0.9)/0.1/2
		},
		From: func(t float64) float64 {
			if t <= 0.5 {
				return t * 2 * 0.9
			}
			return 0.9 + (t-0.5)*2*0.1
		},
	}
	// From 0, at 0.278 of the turn, up a quarter of the throw: 0.528,
	// which is 0.9056 along, or 8.11.
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 1}, nil)
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 55)}, nil)
	k.Handle(input.PointerUp{Pos: geom.Pt(20, 55), Button: input.ButtonPrimary}, nil)
	want := gain.Plain(k.Taper.From(k.Taper.To(gain.Normal(0)) + 0.25))
	if v := h.values[4]; math.Abs(v-want) > 1e-9 || v < 8 {
		t.Errorf("a quarter of the throw up took the knob to %v, want %v", v, want)
	}
	// A step of a key moves a hundredth of the turn, not of the values.
	k.Handle(input.KeyPress{Key: input.KeyUp}, nil)
	if want := gain.Plain(k.Taper.From(k.Taper.To(gain.Normal(want)) + 0.01)); math.Abs(h.values[4]-want) > 1e-9 {
		t.Errorf("a step of Up took the knob to %v, want %v", h.values[4], want)
	}
}
