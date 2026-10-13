package vst3

import (
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio/plug"
)

// The tests run Celemony's ARA test plugin, from ARA_Examples, where it
// is: ARA_TEST_VST3 names the plugin. Built for debugging, it checks
// each call against ARA's rules, and asserts where one breaks them.
func araTestPlugin(t *testing.T) *Module {
	t.Helper()
	path := os.Getenv("ARA_TEST_VST3")
	if path == "" {
		path = "/tmp/ara-src/build-debug/bin/Debug/ARATestPlugIn.vst3"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("no ARA test plugin at", path)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// sineHost is a host whose every source is a sine of its own, a
// frequency a channel.
type sineHost struct {
	reads atomic.Int64
}

func (h *sineHost) ReadAudio(src *AudioSource, at int64, dst [][]float32) bool {
	h.reads.Add(1)
	p := src.Props()
	for c, ch := range dst {
		for i := range ch {
			f := at + int64(i)
			if f < 0 || f >= p.Frames {
				ch[i] = 0
				continue
			}
			ch[i] = float32(0.5 * math.Sin(2*math.Pi*float64(220*(c+1))*float64(f)/p.Rate))
		}
	}
	return true
}

// countAsserts counts the plugin's asserts while the test runs.
func countAsserts(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	var mu sync.Mutex
	old := onARAAssert
	onARAAssert = func(category int32, diagnosis string) {
		n.Add(1)
		mu.Lock()
		defer mu.Unlock()
		t.Logf("the plugin asserts (%d): %s", category, diagnosis)
	}
	t.Cleanup(func() { onARAAssert = old })
	return &n
}

func TestTheARATestPluginRendersAndRestoresItsDocument(t *testing.T) {
	m := araTestPlugin(t)
	asserts := countAsserts(t)
	var c Class
	for _, k := range m.Effects() {
		if k.ARA {
			c = k
		}
	}
	if !c.ARA {
		t.Fatalf("no class of %v has ARA", m.Classes)
	}
	f, err := m.ARAFactory(c)
	if err != nil {
		t.Fatal(err)
	}
	if f.ArchiveID == "" || f.Generation < araGeneration2Final {
		t.Fatalf("factory %+v", f)
	}
	host := &sineHost{}
	d, err := f.NewDocument("Test", host)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	const rate = 48000
	var src *AudioSource
	var mod *AudioModification
	var region *PlaybackRegion
	err = d.Edit(func(e *Edit) {
		mc := e.AddContext(ContextProps{Name: "Song", Content: Content{
			Tempo:  []TempoEntry{{0, 0}, {0.5, 1}},
			Bars:   []BarSignature{{4, 4, 0}},
			Keys:   []KeySignature{{Root: 0, Scale: [12]bool{true, false, true, false, true, true, false, true, false, true, false, true}, Name: "C major"}},
			Chords: []Chord{{Root: 0, Bass: 0, Intervals: [12]byte{1, 0, 0, 0, 3, 0, 0, 5}, Name: "C"}},
		}})
		seq := e.AddSequence(SequenceProps{Name: "Vocal", Context: mc, Color: &Color{1, 0.5, 0}})
		src = e.AddSource(SourceProps{Name: "take 1", ID: "media-1", Frames: 2 * rate, Rate: rate, Channels: 2})
		mod = e.AddModification(src, ModificationProps{Name: "take 1", ID: "take-1"})
		region = e.AddRegion(mod, RegionProps{Start: 0, Duration: 2, At: 1, Length: 2, Sequence: seq, Name: "take 1"})
		e.EnableAudio(src, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	// The analysis runs on a thread of the plugin's, which reads the
	// source through the host.
	d.Analyse(src)
	deadline := time.Now().Add(10 * time.Second)
	done := false
	for !done && time.Now().Before(deadline) {
		for _, u := range d.Poll() {
			if u.Kind == Analysis && u.Source == src && u.Done {
				done = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !done || d.Analysing(src) {
		t.Fatalf("the analysis never finished: done %v, analysing %v", done, d.Analysing(src))
	}
	if host.reads.Load() == 0 {
		t.Fatal("the plugin read nothing")
	}

	// A renderer plays the source where the region puts it.
	p, err := d.NewPlugin(Config{Rate: rate, Block: 512, Offline: true}, PlaybackRenderer)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ARA().SetRegions(region); err != nil {
		t.Fatal(err)
	}
	out := [][]float32{make([]float32, 512), make([]float32, 512)}
	in := [][]float32{make([]float32, 512), make([]float32, 512)}
	b := &plug.Block{Frames: 512, In: in, Out: out, Position: plug.Position{Frame: rate + 1000, Playing: true}}
	p.ProcessBlock(b)
	want := [2][]float32{make([]float32, 512), make([]float32, 512)}
	host.ReadAudio(src, 1000, want[:])
	for ch := range 2 {
		for i := range 512 {
			if math.Abs(float64(out[ch][i]-want[ch][i])) > 1e-6 {
				t.Fatalf("channel %d frame %d: %v, the source has %v", ch, i, out[ch][i], want[ch][i])
			}
		}
	}
	// Before the region, silence.
	b.Position.Frame = 0
	p.ProcessBlock(b)
	if rms(out[0]) != 0 {
		t.Fatal("the renderer played before its region")
	}

	// An editor view takes the selection.
	v, err := d.NewPlugin(Config{Rate: rate}, EditorView)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.ARA().Select(Selection{Regions: []*PlaybackRegion{region}, Range: &TimeRange{1, 2}}); err != nil {
		t.Fatal(err)
	}

	// The archive restores into a new document.
	whole, err := d.Store(nil)
	if err != nil || len(whole) == 0 {
		t.Fatalf("store: %d bytes, %v", len(whole), err)
	}
	part, err := d.Store(&StoreFilter{Modifications: []*AudioModification{mod}})
	if err != nil || len(part) == 0 {
		t.Fatalf("store a modification: %d bytes, %v", len(part), err)
	}
	d2, err := f.NewDocument("Again", host)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	err = d2.Edit(func(e *Edit) {
		mc := e.AddContext(ContextProps{Name: "Song", Content: Content{Tempo: []TempoEntry{{0, 0}, {0.5, 1}}}})
		seq := e.AddSequence(SequenceProps{Name: "Vocal", Context: mc})
		s2 := e.AddSource(SourceProps{Name: "take 1", ID: "media-1", Frames: 2 * rate, Rate: rate, Channels: 2})
		m2 := e.AddModification(s2, ModificationProps{Name: "take 1", ID: "take-1"})
		e.AddRegion(m2, RegionProps{Start: 0, Duration: 2, At: 1, Length: 2, Sequence: seq})
		if err := e.Restore(whole, f.ArchiveID, nil); err != nil {
			t.Error(err)
		}
		if err := e.Restore(part, f.ArchiveID, &RestoreFilter{Modifications: []Rename{{"take-1", "take-1"}}}); err != nil {
			t.Error(err)
		}
		e.EnableAudio(s2, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	d2.Poll()
	p.Close()
	v.Close()
	if n := asserts.Load(); n > 0 {
		t.Fatalf("the plugin asserted %d times", n)
	}
}
