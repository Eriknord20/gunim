package vst3

import (
	"math"
	"os"
	"testing"
	"time"
)

// TestAnyPlugin runs the first effect of the plugin VST3_PLUGIN names,
// a .vst3 bundle or file: it processes, and a state given two fresh
// copies makes them sound alike. With VST3_EDITOR set to a number of
// seconds, its editor stays open that long, logging each edit made in
// it, whose value must lie from 0 to 1.
func TestAnyPlugin(t *testing.T) {
	path := os.Getenv("VST3_PLUGIN")
	if path == "" {
		t.Skip("set VST3_PLUGIN to a plugin to run")
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Effects()) == 0 {
		t.Fatalf("%s makes no effect: %+v", path, m.Classes)
	}
	c := m.Effects()[0]
	t.Logf("%s by %s, %s", c.Name, c.Vendor, c.SubCategories)
	p, err := m.New(c, Config{Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	in := sine(44100, 0.3)
	out := append([]float32(nil), in...)
	p.Process(out)
	if r := rms(out); math.IsNaN(r) || math.IsInf(r, 0) {
		t.Fatalf("the plugin played %v", r)
	}
	t.Logf("level out %.3f of %.3f in, latency %d, %d parameters, editor %v", rms(out), rms(in), p.Latency(),
		len(p.Params()), hasEditor(p))
	st, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	// Two copies of the same state sound alike: within a fifth, as some
	// plugins add noise of their own, as dither.
	var heard [2][]float32
	for i := range heard {
		q, err := m.New(c, Config{Rate: 44100, Offline: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.SetState(st); err != nil {
			t.Fatal(err)
		}
		heard[i] = append([]float32(nil), in...)
		q.Process(heard[i])
		q.Close()
	}
	if a, b := rms(heard[0]), rms(heard[1]); math.Abs(a-b) > 0.2*max(a, 1e-6) {
		t.Errorf("two copies of one state play at %.5f and %.5f", a, b)
	}
	secs, _ := time.ParseDuration(os.Getenv("VST3_EDITOR") + "s")
	if secs <= 0 {
		return
	}
	edits := make(chan [2]float64, 256)
	p.onEdit = func(id uint32, v float64) {
		select {
		case edits <- [2]float64{float64(id), v}:
		default:
		}
	}
	if err := p.OpenEditor(c.Name); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(secs); time.Now().Before(end) && p.EditorOpen(); time.Sleep(10 * time.Millisecond) {
		p.Process(append([]float32(nil), in[:1024]...))
	drain:
		for {
			select {
			case e := <-edits:
				t.Logf("edit: parameter %d to %.4f", uint32(e[0]), e[1])
				if e[1] < 0 || e[1] > 1 || math.IsNaN(e[1]) {
					t.Errorf("an edit came as %v, outside 0 to 1", e[1])
				}
			default:
				break drain
			}
		}
	}
	t.Logf("%d edits made in the editor; it is open: %v", p.Edits(), p.EditorOpen())
}

// hasEditor reports whether p says what size its editor opens at, as one
// with an editor here does, for the logs.
func hasEditor(p *Plugin) bool {
	_, _, ok := p.EditorSize()
	return ok
}
