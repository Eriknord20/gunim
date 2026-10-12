package vst3

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests run LSP's plugins, where they are: LSP_VST3 names the bundle.
func lsp(t *testing.T) *Module {
	t.Helper()
	path := os.Getenv("LSP_VST3")
	if path == "" {
		path = "/tmp/lsp/lsp-plugins-1.2.35-Linux-x86_64/VST3/lsp-plugins.vst3"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("no LSP plugins at", path)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func class(t *testing.T, m *Module, name string) Class {
	t.Helper()
	for _, c := range m.Effects() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q among %d effects", name, len(m.Effects()))
	return Class{}
}

// sine returns n stereo frames of a sine at 1 kHz, at amplitude a.
func sine(n int, a float64) []float32 {
	out := make([]float32, 2*n)
	for i := range n {
		v := float32(a * math.Sin(2*math.Pi*1000*float64(i)/44100))
		out[2*i], out[2*i+1] = v, v
	}
	return out
}

func rms(b []float32) float64 {
	var s float64
	for _, v := range b {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(len(b)))
}

func param(t *testing.T, p *Plugin, title string) Param {
	t.Helper()
	for _, q := range p.Params() {
		if strings.EqualFold(q.Title, title) {
			return q
		}
	}
	var names []string
	for _, q := range p.Params() {
		names = append(names, q.Title)
	}
	t.Fatalf("no parameter %q: %v", title, names)
	return Param{}
}

func TestAPluginProcessesAndRestoresItsState(t *testing.T) {
	m := lsp(t)
	c := class(t, m, "Parametric Equalizer x16 Stereo")
	p, err := m.New(c, Config{Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	in := sine(44100, 0.5)
	out := append([]float32(nil), in...)
	p.Process(out)
	tail := func(b []float32) float64 { return rms(b[len(b)/2:]) }
	if r := tail(out) / tail(in); math.Abs(r-1) > 0.05 {
		t.Fatalf("at its defaults, the equalizer changed the level by %.3f", r)
	}
	t.Logf("single %v, connected %v", p.single, p.compCP != 0 && p.ctrlCP != 0)
	// Down goes the output gain, and the level with it.
	g := param(t, p, "Output gain")
	p.Set(g.ID, g.Default/2)
	quieter := append([]float32(nil), in...)
	p.Process(quieter)
	if r := tail(quieter) / tail(in); r > 0.9 {
		t.Fatalf("with the output gain down, the level is %.3f of the input's", r)
	}
	st, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	// A second equalizer, of the first's state, sounds as it does.
	q, err := m.New(c, Config{Rate: 44100, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := q.SetState(st); err != nil {
		t.Fatal(err)
	}
	again := append([]float32(nil), in...)
	q.Process(again)
	if a, b := tail(again), tail(quieter); math.Abs(a-b) > 0.01*b {
		t.Fatalf("restored, the equalizer plays at %.4f, the first at %.4f", a, b)
	}
}

func TestAPluginOfASidechainProcesses(t *testing.T) {
	m := lsp(t)
	p, err := m.New(class(t, m, "Sidechain Compressor Stereo"), Config{Rate: 44100, Block: 512})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	in := sine(44100, 0.9)
	out := append([]float32(nil), in...)
	p.Process(out)
	if r := rms(out[len(out)/2:]); r == 0 || math.IsNaN(r) {
		t.Fatalf("the compressor played %v", r)
	}
	t.Logf("buses in %d out %d, level %.3f of %.3f", len(p.ins), len(p.outs), rms(out[len(out)/2:]), rms(in))
}

func TestABundleListsTheEffectsItsModuleMakes(t *testing.T) {
	m := lsp(t)
	listed := moduleInfo(m.Path)
	made := m.Effects()
	if len(listed) == 0 || len(listed) != len(made) {
		t.Fatalf("the bundle lists %d effects, the module makes %d", len(listed), len(made))
	}
	names := map[uid]string{}
	for _, c := range made {
		names[c.ID] = c.Name
	}
	for _, c := range listed {
		if names[c.ID] != c.Name {
			t.Fatalf("listed %s as %x, which the module makes as %q", c.Name, c.ID, names[c.ID])
		}
	}
	if b := Scan(filepath.Dir(m.Path)); len(b) != 1 || b[0].Name != "lsp-plugins" {
		t.Fatalf("the scan found %v", b)
	}
}

// testPlugin loads the plugin the tests of the processor run: LSP's
// equalizer, and its output gain, unless VST3_TEST names a bundle, with
// VST3_TEST_CLASS its effect and VST3_TEST_PARAM a parameter, as for a
// plugin built for another system.
func testPlugin(t *testing.T) (*Plugin, Param) {
	t.Helper()
	var m *Module
	name, title := "Parametric Equalizer x16 Stereo", "Output gain"
	if path := os.Getenv("VST3_TEST"); path != "" {
		var err error
		if m, err = Open(path); err != nil {
			t.Fatal(err)
		}
		name, title = os.Getenv("VST3_TEST_CLASS"), os.Getenv("VST3_TEST_PARAM")
	} else {
		m = lsp(t)
	}
	p, err := m.New(class(t, m, name), Config{Rate: 44100, Block: 512})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, param(t, p, title)
}

// TestProcessingTakesNoLockAndMakesNothing sets a plugin's parameter
// from another goroutine as it processes, for the race detector, then
// counts every allocation in Process as sets come between blocks.
func TestProcessingTakesNoLockAndMakesNothing(t *testing.T) {
	p, gain := testPlugin(t)
	frames := sine(1500, 0.5)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 300 {
			p.edits.put(gain.ID, float64(i%10)/10)
		}
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			p.Process(frames)
		}
	}
	if raceOn {
		return
	}
	if runtime.GOOS != "windows" {
		// Go's own callbacks, on Windows, neither lock nor allocate;
		// purego's, elsewhere, lock a mutex and call by reflection on
		// every call a plugin makes into the host.
		t.Skip("purego's callbacks lock and allocate on every call into Go")
	}
	// Only what processing makes counts: the plugins' thread makes what
	// it makes meanwhile, on a goroutine of its own.
	made := allocsUnder(t, "(*Plugin).Process", func() {
		for i := range 100 {
			p.edits.put(gain.ID, float64(i%10)/10)
			p.Process(frames)
		}
	})
	if made != 0 {
		t.Fatalf("processing made %d things over 100 blocks", made)
	}
}

// allocsUnder runs run, and returns how many things were made in it
// with fn among their callers: the allocations of one call, as other
// goroutines allocate as they like meanwhile.
func allocsUnder(t *testing.T, fn string, run func()) int64 {
	t.Helper()
	was := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = was }()
	count := func() int64 {
		// The profile is as of the last collection, finished.
		runtime.GC()
		runtime.GC()
		var recs []runtime.MemProfileRecord
		n, _ := runtime.MemProfile(nil, true)
		for {
			recs = make([]runtime.MemProfileRecord, n+50)
			var ok bool
			if n, ok = runtime.MemProfile(recs, true); ok {
				recs = recs[:n]
				break
			}
		}
		var total int64
		for _, r := range recs {
			frames := runtime.CallersFrames(r.Stack())
			for {
				f, more := frames.Next()
				if strings.Contains(f.Function, fn) {
					total += r.AllocObjects
					break
				}
				if !more {
					break
				}
			}
		}
		return total
	}
	before := count()
	run()
	return count() - before
}

// TestASetWaitsForTheProcessorAsItsLastValue sets a parameter many
// times while the plugin processes nothing: the processor takes the
// last, once.
func TestASetWaitsForTheProcessorAsItsLastValue(t *testing.T) {
	p, gain := testPlugin(t)
	for i := range 5000 {
		p.edits.put(gain.ID, float64(i%7)/10)
	}
	if !p.edits.waits() {
		t.Fatal("no set waits")
	}
	p.inChanges.clear()
	taken := 0
	if p.edits.look() {
		for i := range p.edits.slots {
			if id, v, ok := p.edits.take(i); ok {
				taken++
				if id != gain.ID || v != float64(4999%7)/10 {
					t.Fatalf("took %d at %v, want %d at %v", id, v, gain.ID, float64(4999%7)/10)
				}
			}
		}
	}
	if taken != 1 || p.edits.waits() {
		t.Fatalf("took %d values, and some still wait: %v", taken, p.edits.waits())
	}
	if p.edits.put(0xfffffff0, 1) {
		t.Fatal("a parameter the plugin does not list was put")
	}
}
