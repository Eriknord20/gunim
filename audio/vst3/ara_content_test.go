package vst3

import (
	"errors"
	"math"
	"slices"
	"testing"
)

// The places of the document controller's functions, as offsetof in
// ARAInterface.h gives them, less the table's size.
func TestTheDocumentControllersTableMatchesTheHeader(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"createAudioSourceContentReader", dcCreateAudioSourceContentReader, 31},
		{"destroyContentReader", dcDestroyContentReader, 40},
		{"storeObjectsToArchive", dcStoreObjectsToArchive, 46},
		{"getProcessingAlgorithmsCount", dcGetProcessingAlgorithmsCount, 47},
		{"requestProcessingAlgorithmForAudioSource", dcRequestProcessingAlgorithmForAudioSource, 50},
	} {
		if c.got != c.want {
			t.Errorf("%s at %d; the header has %d", c.name, c.got, c.want)
		}
	}
}

// burstHost is a host whose every source sounds at 0.25 from 0.1 s to
// 0.5 s and at 0.5 from 1 s to 1.5 s, silent otherwise.
type burstHost struct{}

func (burstHost) ReadAudio(src *AudioSource, at int64, dst [][]float32) bool {
	rate := src.Props().Rate
	for _, ch := range dst {
		for i := range ch {
			s := float64(at+int64(i)) / rate
			switch {
			case s >= 0.1 && s < 0.5:
				ch[i] = 0.25
			case s >= 1 && s < 1.5:
				ch[i] = -0.5
			default:
				ch[i] = 0
			}
		}
	}
	return true
}

// analysed waits for the analysis of src to end, as the host hears its
// content changed.
func analysed(t *testing.T, d *Document, src *AudioSource) {
	t.Helper()
	d.Analyse(src)
	pollFor(t, d, func(u Update) bool { return u.Kind == SourceChanged && u.Source == src })
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func sameNotes(got, want []Note) bool {
	if len(got) != len(want) {
		return false
	}
	for i, g := range got {
		w := want[i]
		if !near(g.Frequency, w.Frequency) || g.Pitch != w.Pitch || !near(g.Volume, w.Volume) || !near(g.Start, w.Start) ||
			!near(g.Attack, w.Attack) || !near(g.Duration, w.Duration) || !near(g.Signal, w.Signal) {
			return false
		}
	}
	return true
}

func TestTheContentThePluginFindsReadsBackBySourceModificationAndRegion(t *testing.T) {
	a := makeFakeARA()
	fake := &FakeARA{a: a}
	_, d := fakeDocument(t, a, burstHost{})
	var src *AudioSource
	var mod *AudioModification
	var region *PlaybackRegion
	err := d.Edit(func(e *Edit) {
		mc := e.AddContext(ContextProps{Name: "Song"})
		seq := e.AddSequence(SequenceProps{Name: "Vocal", Context: mc})
		src = e.AddSource(SourceProps{Name: "one", ID: "media-1", Frames: 96000, Rate: 48000, Channels: 2})
		mod = e.AddModification(src, ModificationProps{Name: "one", ID: "take-1"})
		// The second half of the source, from 0.9 s, stretched to twice
		// its length at 3 s.
		region = e.AddRegion(mod, RegionProps{Transforms: Timestretch, Start: 0.9, Duration: 1, At: 3, Length: 2,
			Sequence: seq})
		e.EnableAudio(src, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is there before the analysis.
	if c, err := d.SourceContent(src); err != nil || len(c.Grades) != 0 || len(c.Notes) != 0 {
		t.Fatalf("before the analysis: %+v, %v", c, err)
	}
	analysed(t, d, src)
	notes := []Note{
		{Frequency: 440, Pitch: 69, Volume: 0.25, Start: 0.1, Duration: 0.4, Signal: 0.4},
		{Frequency: 440, Pitch: 69, Volume: 0.5, Start: 1, Duration: 0.5, Signal: 0.5},
	}
	c, err := d.SourceContent(src)
	if err != nil {
		t.Fatal(err)
	}
	if !sameNotes(c.Notes, notes) || len(c.Tempo) != 0 || len(c.Chords) != 0 ||
		len(c.Grades) != 1 || c.Grades[ContentNotes] != GradeDetected {
		t.Fatalf("source: %+v", c)
	}

	// With a tempo and chords detected too.
	chords := []Chord{{Root: 0, Bass: 0, Intervals: [12]byte{1, 0, 0, 0, 3, 0, 0, 5}, Name: "C", Quarter: 0},
		{Root: 1, Bass: 1, Intervals: [12]byte{1, 0, 0, 3, 0, 0, 0, 5}, Name: "Gm", Quarter: 4}}
	fake.Detect(d, 120, chords)
	pollFor(t, d, func(u Update) bool { return u.Kind == SourceChanged && u.Source == src })
	c, err = d.SourceContent(src, ContentTempo, ContentSheetChords, ContentKeys)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Notes) != 0 || !slices.Equal(c.Tempo, []TempoEntry{{0, 0}, {0.5, 1}}) || !slices.Equal(c.Chords, chords) ||
		len(c.Grades) != 2 || c.Grades[ContentTempo] != GradeDetected || c.Grades[ContentSheetChords] != GradeDetected {
		t.Fatalf("source, tempo and chords: %+v", c)
	}

	// An edit of the second note's gain changes the modification's notes
	// and the region's, which hold the second alone, stretched.
	fake.Edit(d, "take-1", 0.5, TimeRange{0.9, 1})
	c, err = d.ModificationContent(mod)
	if err != nil {
		t.Fatal(err)
	}
	edited := slices.Clone(notes)
	edited[1].Volume = 0.25
	if !sameNotes(c.Notes, edited) || !slices.Equal(c.Tempo, []TempoEntry{{0, 0}, {0.5, 1}}) || len(c.Grades) != 3 {
		t.Fatalf("modification: %+v", c)
	}
	c, err = d.RegionContent(region)
	if err != nil {
		t.Fatal(err)
	}
	want := []Note{{Frequency: 440, Pitch: 69, Volume: 0.25, Start: 3.2, Duration: 1, Signal: 1}}
	if !sameNotes(c.Notes, want) || len(c.Grades) != 1 {
		t.Fatalf("region: %+v", c)
	}

	if _, err := d.SourceContent(src, ContentTuning); err == nil {
		t.Fatal("tuning read into an ObjectContent")
	}
	d.Close()
	if _, err := d.RegionContent(region); !errors.Is(err, ErrClosed) {
		t.Fatalf("a closed document's content: %v", err)
	}
	noViolations(t, a)
}

func TestASourceTakesTheAlgorithmRequestedAndKeepsItInItsArchive(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	if got := x.d.Algorithms(); !slices.Equal(got, fakeAlgorithms) {
		t.Fatalf("algorithms %+v", got)
	}
	if i := x.d.AlgorithmOf(x.src); i != 0 {
		t.Fatalf("algorithm %d", i)
	}
	if err := x.d.Edit(func(e *Edit) {
		if err := e.RequestAlgorithm(x.src, 2); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	// The source is analysed again, and the host hears it changed.
	var progress, changed bool
	pollFor(t, x.d, func(u Update) bool {
		progress = progress || u.Kind == Analysis && u.Source == x.src && u.Done
		changed = changed || progress && u.Kind == SourceChanged && u.Source == x.src
		return changed
	})
	if i := x.d.AlgorithmOf(x.src); i != 2 || x.d.AlgorithmOf(x.src64) != 0 {
		t.Fatalf("algorithms %d and %d", i, x.d.AlgorithmOf(x.src64))
	}
	if err := x.d.Edit(func(e *Edit) { _ = e.RequestAlgorithm(x.src, 3) }); err == nil {
		t.Fatal("an algorithm the plugin has none of requested")
	}

	// The source's archive holds its algorithm.
	b, err := x.d.Store(&StoreFilter{Sources: []*AudioSource{x.src}})
	if err != nil {
		t.Fatal(err)
	}
	_, d2 := fakeDocument(t, x.a, x.host)
	defer d2.Close()
	var s2 *AudioSource
	err = d2.Edit(func(e *Edit) {
		s2 = e.AddSource(SourceProps{Name: "one", ID: "media-1", Frames: 96000, Rate: 48000, Channels: 2})
		if err := e.Restore(b, fakeArchiveID, nil); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if i := d2.AlgorithmOf(s2); i != 2 {
		t.Fatalf("restored, algorithm %d", i)
	}
	_ = d2.Edit(func(e *Edit) { e.RemoveSource(s2) })
	noViolations(t, x.a)
}

func TestAnEditStopsEachRendererOnceForTheRegionsItTakesOut(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	p, fp := bound(t, x.a, x.d, PlaybackRenderer)
	defer p.Close()
	if err := p.ARA().SetRegions(x.r1, x.r2); err != nil {
		t.Fatal(err)
	}
	fp.mu.Lock()
	on, off := fp.activations, fp.deactivations
	fp.mu.Unlock()
	err := x.d.Edit(func(e *Edit) {
		e.RemoveRegion(x.r1)
		e.RemoveRegion(x.r2)
		fp.mu.Lock()
		defer fp.mu.Unlock()
		if fp.isActive {
			t.Error("the renderer runs while the edit takes its regions out")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if fp.activations-on != 1 || fp.deactivations-off != 1 || !fp.isActive {
		t.Fatalf("%d activations, %d deactivations, active %v", fp.activations-on, fp.deactivations-off, fp.isActive)
	}
	if len(p.ARA().Regions()) != 0 || len(fp.araExt.regions) != 0 {
		t.Fatal("the renderer still holds regions taken out")
	}
	noViolations(t, x.a)
}

func TestTheFakeStretchesAndFadesARegionAsItsTransformsAsk(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	var r *PlaybackRegion
	if err := x.d.Edit(func(e *Edit) {
		r = e.AddRegion(x.mod, RegionProps{Transforms: Timestretch | FadeAtHead | FadeAtTail, Start: 0.5, Duration: 1,
			At: 10, Length: 2, Sequence: x.seq, Name: "slow"})
	}); err != nil {
		t.Fatal(err)
	}
	got := (&FakeARA{a: x.a}).Regions(x.d)
	if len(got) != 3 || got[2].Name != "slow" || got[2].Sequence != x.seq || got[2].Transforms != r.Props().Transforms ||
		got[2].Duration != 1 || got[2].Length != 2 {
		t.Fatalf("regions %+v", got)
	}
	p, _ := bound(t, x.a, x.d, PlaybackRenderer)
	defer p.Close()
	_ = p.ARA().SetRegions(r)
	src := [][]float32{make([]float32, 96000), make([]float32, 96000)}
	x.host.ReadAudio(x.src, 0, src)
	// want is the region's sound at song frame f, at half speed, without
	// its fades.
	want := func(f int64) float64 {
		pos := (0.5 + (float64(f)/48000-10)*0.5) * 48000
		at := math.Floor(pos)
		fr := pos - at
		return float64(src[0][int(at)])*(1-fr) + float64(src[0][int(at)+1])*fr
	}
	check := func(from int64, gain func(i int) float64) {
		t.Helper()
		out := render(p, from, 256)
		for i, v := range out[0] {
			if w := gain(i) * want(from+int64(i)); math.Abs(float64(v)-w) > 1e-4 {
				t.Fatalf("song frame %d: %v, want %v", from+int64(i), v, w)
			}
		}
	}
	// In the middle, the source at half speed; at the head and the tail,
	// faded in and out over 2 ms, 96 frames.
	check(11*48000, func(int) float64 { return 1 })
	check(10*48000, func(i int) float64 { return min(float64(i)/96, 1) })
	check(12*48000-256, func(i int) float64 { return min(float64(256-i)/96, 1) })
	_ = p.ARA().SetRegions()
	_ = x.d.Edit(func(e *Edit) { e.RemoveRegion(r) })
	noViolations(t, x.a)
}

func TestAModificationTakesTheEditsOfAnotherSourcesModification(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	fake := &FakeARA{a: x.a}
	fake.Edit(x.d, "take-1", 0.25, TimeRange{0.5, 1})
	part, err := x.d.Store(&StoreFilter{Modifications: []*AudioModification{x.mod}})
	if err != nil {
		t.Fatal(err)
	}
	x.d.Poll()
	// take-9 is of the second source, take-1 of the first.
	var m9 *AudioModification
	err = x.d.Edit(func(e *Edit) {
		m9 = e.AddModification(x.src64, ModificationProps{Name: "copy", ID: "take-9"})
		if err := e.Restore(part, fakeArchiveID, &RestoreFilter{Modifications: []Rename{{"take-1", "take-9"}}}); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	pollFor(t, x.d, func(u Update) bool { return u.Kind == ModificationChanged && u.Modification == m9 })
	fd := x.a.docs[0]
	fd.mu.Lock()
	m := fd.mods[m9.ref]
	gain, edit := m.gain, m.edit
	fd.mu.Unlock()
	if gain != 0.25 || edit == nil || *edit != (TimeRange{0.5, 1}) || fake.Gain(x.d, "take-2") != 1 {
		t.Fatalf("gain %v over %v", gain, edit)
	}
	_ = x.d.Edit(func(e *Edit) { e.RemoveModification(m9) })
	noViolations(t, x.a)
}

func TestAnInstancesStateKeepsItsView(t *testing.T) {
	x := newADocument(t)
	fake := &FakeARA{a: x.a}
	v, _ := bound(t, x.a, x.d, EditorView)
	defer v.Close()
	fake.SetView(v, 2.5)
	st, err := v.State()
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := bound(t, x.a, x.d, EditorView)
	defer v2.Close()
	if fake.View(v2) != 0 {
		t.Fatal("a new instance has a view of its own")
	}
	if err := v2.SetState(st); err != nil {
		t.Fatal(err)
	}
	if z := fake.View(v2); z != 2.5 {
		t.Fatalf("zoom %v", z)
	}
	noViolations(t, x.a)
}
