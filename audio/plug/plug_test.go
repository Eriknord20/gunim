package plug

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestClampTurnsNaNToTheDefaultAndInfinityToTheEnds(t *testing.T) {
	for _, p := range []Param{
		{Min: 0.3, Max: 1.5, Default: 0.8},
		{Min: -24, Max: 24, Default: 6, Steps: 4},
		{Min: 20, Max: 20000, Default: 440, Log: true},
	} {
		if got := p.Clamp(math.NaN()); got != p.Clamp(p.Default) {
			t.Errorf("%+v: Clamp(NaN) = %g, want the default %g", p, got, p.Clamp(p.Default))
		}
		if got := p.Clamp(math.Inf(1)); got != p.Max {
			t.Errorf("%+v: Clamp(+Inf) = %g, want %g", p, got, p.Max)
		}
		if got := p.Clamp(math.Inf(-1)); got != p.Min {
			t.Errorf("%+v: Clamp(-Inf) = %g, want %g", p, got, p.Min)
		}
	}
}

func TestParamValuesStayInRangeAndOnSteps(t *testing.T) {
	p := Param{Min: -24, Max: 24, Steps: 4}
	for _, c := range []struct{ in, want float64 }{{-30, -24}, {30, 24}, {5, 0}, {7, 12}, {-13, -12}} {
		if got := p.Clamp(c.in); got != c.want {
			t.Errorf("Clamp(%g) = %g, want %g", c.in, got, c.want)
		}
	}
	smooth := Param{Min: 20, Max: 20000}
	if n := smooth.Normal(10010); math.Abs(n-0.5) > 1e-9 {
		t.Errorf("Normal halfway = %g, want 0.5", n)
	}
	if v := smooth.Plain(smooth.Normal(440)); math.Abs(v-440) > 1e-9 {
		t.Errorf("Plain(Normal(440)) = %g", v)
	}
	// By ratios, halfway is the geometric mean.
	ratios := Param{Min: 1, Max: 1000, Log: true}
	if n := ratios.Normal(31.622776601683793); math.Abs(n-0.5) > 1e-9 {
		t.Errorf("Normal of the geometric mean = %g, want 0.5", n)
	}
	if v := ratios.Plain(ratios.Normal(250)); math.Abs(v-250) > 1e-9 {
		t.Errorf("by ratios, Plain(Normal(250)) = %g", v)
	}
	if s := (Param{Unit: "dB"}).Text(-1.5); s != "-1.50 dB" {
		t.Errorf("Text = %q", s)
	}
}

func TestRegisterRefusesMistakes(t *testing.T) {
	newer := func(Host) Plugin { return nil }
	for name, d := range map[string]Descriptor{
		"no ID":       {Name: "X", New: newer},
		"no New":      {ID: "test.nonew", Name: "X"},
		"two params":  {ID: "test.two", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1}, {ID: 1, Max: 1}}},
		"empty range": {ID: "test.range", Name: "X", New: newer, Params: []Param{{ID: 1}}},
		"default out": {ID: "test.def", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1, Default: 2}}},
		"log from 0":  {ID: "test.log", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1, Log: true}}},
		"patch twice": {ID: "test.twice", Name: "X", New: newer, Patches: []Patch{{Name: "A"}, {Name: "A"}}},
		"patch of no param": {ID: "test.noparam", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1}},
			Patches: []Patch{{Name: "A", Params: map[uint32]float64{2: 0}}}},
		"patch of a meter": {ID: "test.meter", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1, Flags: ReadOnly}},
			Patches: []Patch{{Name: "A", Params: map[uint32]float64{1: 0}}}},
		"instrument without notes":  {ID: "test.deaf", Name: "X", New: newer, Kind: "Instrument|Synth"},
		"generator without control": {ID: "test.mute", Name: "X", New: newer, Kind: "Generator|LFO"},
		"unnamed aux input":         {ID: "test.aux", Name: "X", New: newer, AuxIn: []AudioPort{{Channels: 2}}},
		"aux outputs of one name": {ID: "test.auxes", Name: "X", New: newer,
			AuxOut: []AudioPort{{Name: "Kick"}, {Name: "Kick"}}},
		"negative channels": {ID: "test.chans", Name: "X", New: newer, AuxIn: []AudioPort{{Name: "Key", Channels: -1}}},
		"control of no range": {ID: "test.ctl", Name: "X", New: newer,
			ControlIn: []ControlPort{{Name: "Cutoff", Min: 1, Max: 1, Default: 1}}},
		"control default out": {ID: "test.ctldef", Name: "X", New: newer,
			ControlOut: []ControlPort{{Name: "LFO", Min: -1, Max: 1, Default: 2}}},
		"look-ahead without notes": {ID: "test.ahead", Name: "X", New: newer, LookAhead: time.Second},
		"look-ahead below zero": {ID: "test.behind", Name: "X", New: newer, Kind: "Instrument|Synth", Notes: true,
			LookAhead: -time.Millisecond},
		"controls of one name": {ID: "test.ctls", Name: "X", New: newer,
			ControlIn: []ControlPort{{Name: "A", Max: 1}, {Name: "A", Max: 1}}},
	} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("%s: Register took it", name)
				}
			}()
			Register(d)
		}()
	}
}

// listedRuns counts the runs of the listing test, so each registers IDs
// of its own: the registry is the program's, and a run again, as with
// -count, would find the last run's there.
var listedRuns int

func TestRegisteredPluginsAreListedAndFound(t *testing.T) {
	listedRuns++
	prefix := fmt.Sprintf("listed%d.", listedRuns)
	newer := func(Host) Plugin { return nil }
	Register(Descriptor{ID: prefix + "b", Name: "Bee", Vendor: "zed", New: newer})
	Register(Descriptor{ID: prefix + "a", Name: "Ay", Vendor: "Zed", New: newer})
	if _, ok := Lookup(prefix + "a"); !ok {
		t.Fatalf("Lookup did not find %sa", prefix)
	}
	var names []string
	for _, d := range All() {
		if strings.HasPrefix(d.ID, prefix) {
			names = append(names, d.Name)
		}
	}
	if strings.Join(names, ",") != "Ay,Bee" {
		t.Errorf("All lists %v, want Ay then Bee", names)
	}
	defer func() {
		if recover() == nil {
			t.Errorf("a second %sa was registered", prefix)
		}
	}()
	Register(Descriptor{ID: prefix + "a", Name: "Again", New: newer})
}

func TestAGeneratorIsKnownByItsKind(t *testing.T) {
	for kind, want := range map[string]bool{
		"Generator": true, "Generator|LFO": true,
		"": false, "Fx|Generator": false, "Instrument|Synth": false, "Generators|LFO": false,
	} {
		d := Descriptor{Kind: kind}
		if got := d.Generator(); got != want {
			t.Errorf("of kind %q, Generator = %v, want %v", kind, got, want)
		}
		if d.Generator() && d.Instrument() {
			t.Errorf("of kind %q, the plugin is both a generator and an instrument", kind)
		}
	}
	listedRuns++
	id := fmt.Sprintf("listed%d.lfo", listedRuns)
	Register(Descriptor{ID: id, Name: "LFO", Kind: "Generator|LFO", New: func(Host) Plugin { return nil },
		ControlOut: []ControlPort{{Name: "LFO", Min: -1, Max: 1}}})
	if d, _ := Lookup(id); !d.Generator() {
		t.Errorf("the LFO registered as %+v", d)
	}
}

func TestAnInstrumentIsKnownByItsKind(t *testing.T) {
	for kind, want := range map[string]bool{
		"Instrument": true, "Instrument|Synth": true, "Instrument|Drum": true,
		"": false, "Fx|EQ": false, "Fx|Instrument": false, "Instruments|Synth": false,
	} {
		d := Descriptor{Kind: kind}
		if got := d.Instrument(); got != want {
			t.Errorf("of kind %q, Instrument = %v, want %v", kind, got, want)
		}
	}
	// An instrument registers as it takes notes, and so does an effect
	// that takes them beside its sound.
	listedRuns++
	prefix := fmt.Sprintf("listed%d.", listedRuns)
	newer := func(Host) Plugin { return nil }
	Register(Descriptor{ID: prefix + "synth", Name: "Synth", Kind: "Instrument|Synth", Notes: true, New: newer})
	Register(Descriptor{ID: prefix + "vocoder", Name: "Vocoder", Kind: "Fx|Modulation", Notes: true, New: newer})
	if d, _ := Lookup(prefix + "synth"); !d.Instrument() || !d.Notes {
		t.Errorf("the synth registered as %+v", d)
	}
}

func TestPortsReadAsNilWhereUnconnected(t *testing.T) {
	ch := []float32{1, 2}
	// A host that knows no ports leaves the lists empty; another leaves
	// a port nil, or lists fewer than the plugin has.
	for name, b := range map[string]*Block{
		"none":      {Frames: 2},
		"nil":       {Frames: 2, AuxIn: [][][]float32{nil}, AuxOut: [][][]float32{{}}, ControlIn: [][]float32{nil}, ControlOut: [][]float32{{}}},
		"too short": {Frames: 2, AuxIn: [][][]float32{{ch}}, AuxOut: [][][]float32{{ch}}, ControlIn: [][]float32{ch}, ControlOut: [][]float32{ch}},
	} {
		i := 0
		if name == "too short" {
			i = 1
		}
		if b.AuxInput(i) != nil || b.AuxOutput(i) != nil || b.ControlInput(i) != nil || b.ControlOutput(i) != nil {
			t.Errorf("%s: an unconnected port reads as connected", name)
		}
		if b.AuxInput(-1) != nil {
			t.Errorf("%s: port -1 reads as connected", name)
		}
	}
	b := &Block{Frames: 2, AuxIn: [][][]float32{nil, {ch}}, ControlOut: [][]float32{nil, ch}}
	if got := b.AuxInput(1); len(got) != 1 || &got[0][0] != &ch[0] {
		t.Errorf("aux input 1 reads as %v", got)
	}
	if got := b.ControlOutput(1); len(got) != 2 || &got[0] != &ch[0] {
		t.Errorf("control output 1 reads as %v", got)
	}
}

func TestAControlSignalDrivesAParameterByChanges(t *testing.T) {
	p := Param{ID: 9, Min: -12, Max: 12}
	values := make([]float32, 100)
	for f := range values {
		values[f] = float32(f) / 4
		if f >= 64 {
			values[f] = 20 // past the top: held at Max, and sent once
		}
	}
	got := AppendControl([]Change{{Frame: 0, ID: 1, Value: 3}}, p, values, 16)
	want := []Change{{0, 1, 3}, {0, 9, 0}, {16, 9, 4}, {32, 9, 8}, {48, 9, 12}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the changes are %v, want %v", got, want)
	}
	// Every frame, at a step of 1, or of less.
	if got := AppendControl(nil, p, values[:3], 0); len(got) != 3 || got[2] != (Change{2, 9, 0.5}) {
		t.Errorf("at a step of 0, the changes are %v", got)
	}
	// On a parameter's steps: a switch moves once.
	sw := Param{ID: 2, Max: 1, Steps: 1}
	if got := AppendControl(nil, sw, []float32{0.1, 0.2, 0.7, 0.9}, 1); fmt.Sprint(got) != fmt.Sprint([]Change{{0, 2, 0}, {2, 2, 1}}) {
		t.Errorf("a switch changes %v", got)
	}
	dst := make([]Change, 0, 512/ControlStep)
	signal := make([]float32, 512)
	for f := range signal {
		signal[f] = float32(math.Sin(float64(f) / 40))
	}
	if n := testing.AllocsPerRun(100, func() { dst = AppendControl(dst[:0], p, signal, ControlStep) }); n != 0 {
		t.Errorf("with room, AppendControl made %v things", n)
	}
	if len(dst) != 512/ControlStep {
		t.Errorf("a block of 512 frames takes %d changes, want %d", len(dst), 512/ControlStep)
	}
}

// TestAFinderMakesAPluginKnownAsItIsAskedFor looks up a plugin no one
// registered: a finder registers it as it is asked, once, and an ID it
// finds nothing of stays unknown.
func TestAFinderMakesAPluginKnownAsItIsAskedFor(t *testing.T) {
	asked := map[string]int{}
	AddFinder(func(id string) bool {
		asked[id]++
		if id != "test.found" {
			return false
		}
		Register(Descriptor{ID: id, Name: "Found", Kind: "Fx", New: func(Host) Plugin { return nil }})
		return true
	})
	for range 2 {
		if d, ok := Lookup("test.found"); !ok || d.Name != "Found" {
			t.Fatalf("looked up, the plugin found is %+v, %v", d, ok)
		}
	}
	if _, ok := Lookup("test.nowhere"); ok {
		t.Fatal("a plugin no finder finds was found")
	}
	if asked["test.found"] != 1 || asked["test.nowhere"] != 1 {
		t.Fatalf("the finder was asked %v", asked)
	}
}
