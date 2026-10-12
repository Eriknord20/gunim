package plug

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

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
