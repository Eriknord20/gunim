package vst3

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio/plug"
)

// The places of the fields of ARA's structures, as gcc lays out
// ARAInterface.h on x86, packed to the byte, and on ARM, aligned.
func TestARALayoutsMatchTheHeaders(t *testing.T) {
	if ptrSize != 8 {
		t.Skip("ARA runs on 64-bit platforms")
	}
	cases := []struct {
		name            string
		fields          []int
		packed, natural []int
		psize, nsize    int
	}{
		{"ARAMusicalContextProperties", []int{fPtr, fPtr, fI32, fPtr}, []int{0, 8, 16, 20}, []int{0, 8, 16, 24}, 28, 32},
		{"ARARegionSequenceProperties", []int{fPtr, fPtr, fI32, fPtr, fPtr}, []int{0, 8, 16, 20, 28}, []int{0, 8, 16, 24, 32}, 36, 40},
		{"ARAAudioSourceProperties", []int{fPtr, fPtr, fPtr, fI64, fF64, fI32, fI32, fI32, fPtr},
			[]int{0, 8, 16, 24, 32, 40, 44, 48, 52}, []int{0, 8, 16, 24, 32, 40, 44, 48, 56}, 60, 64},
		{"ARAPlaybackRegionProperties", []int{fPtr, fI32, fF64, fF64, fF64, fF64, fPtr, fPtr, fPtr, fPtr},
			[]int{0, 8, 12, 20, 28, 36, 44, 52, 60, 68}, []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 72}, 76, 80},
		{"ARAContentBarSignature", []int{fI32, fI32, fF64}, []int{0, 4, 8}, []int{0, 4, 8}, 16, 16},
		{"ARAContentKeySignature", []int{fI32, bytesField(12), fPtr, fF64}, []int{0, 4, 16, 24}, []int{0, 4, 16, 24}, 32, 32},
		{"ARAContentChord", []int{fI32, fI32, bytesField(12), fPtr, fF64}, []int{0, 4, 8, 20, 28}, []int{0, 4, 8, 24, 32}, 36, 40},
		{"ARAInterfaceConfiguration", []int{fPtr, fI32, fPtr}, []int{0, 8, 12}, []int{0, 8, 16}, 20, 24},
		{"ARARestoreObjectsFilter", []int{fPtr, fI32, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr},
			[]int{0, 8, 12, 20, 28, 36, 44, 52}, []int{0, 8, 16, 24, 32, 40, 48, 56}, 60, 64},
		{"ARAStoreObjectsFilter", []int{fPtr, fI32, fPtr, fPtr, fPtr, fPtr}, []int{0, 8, 12, 20, 28, 36},
			[]int{0, 8, 16, 24, 32, 40}, 44, 48},
		{"ARAViewSelection", []int{fPtr, fPtr, fPtr, fPtr, fPtr, fPtr}, []int{0, 8, 16, 24, 32, 40}, []int{0, 8, 16, 24, 32, 40}, 48, 48},
		{"ARAFactory", araFactoryFields, []int{0, 8, 12, 16, 24, 32, 40, 48, 56, 64, 72, 80, 88, 96, 104, 112, 120, 124},
			[]int{0, 8, 12, 16, 24, 32, 40, 48, 56, 64, 72, 80, 88, 96, 104, 112, 120, 124}, 128, 128},
		{"ARAColor", []int{fF32, fF32, fF32}, []int{0, 4, 8}, []int{0, 4, 8}, 12, 12},
	}
	old := araPack
	defer func() { araPack = old }()
	for _, pack := range []int{1, 8} {
		araPack = pack
		for _, c := range cases {
			off, size := layout(c.fields...)
			want, wsize := c.packed, c.psize
			if pack == 8 {
				want, wsize = c.natural, c.nsize
			}
			if !slices.Equal(off, want) || size != wsize {
				t.Errorf("%s packed to %d: %v, %d bytes; the header has %v, %d", c.name, pack, off, size, want, wsize)
			}
		}
	}
}

// fakeDocument makes a document of the fake ARA plugin a, for host.
func fakeDocument(t *testing.T, a *fakeARAPlugin, host ARAHost) (*ARAFactory, *Document) {
	t.Helper()
	f := (&FakeARA{a: a}).Factory()
	d, err := f.NewDocument("Song", host)
	if err != nil {
		t.Fatal(err)
	}
	return f, d
}

// bound makes an instance of the fake plugin of a bound to d in roles.
func bound(t *testing.T, a *fakeARAPlugin, d *Document, roles Roles) (*Plugin, *fakeInstance) {
	t.Helper()
	p, err := d.NewPlugin(Config{Rate: 48000, Block: 256, Offline: true}, roles)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return p, a.instances[len(a.instances)-1]
}

// noViolations fails the test where the fake saw a rule of ARA's broken.
func noViolations(t *testing.T, a *fakeARAPlugin) {
	t.Helper()
	for _, v := range a.broken() {
		t.Error("ARA's rule broken: " + v)
	}
}

// pollFor polls d until an update keep takes comes, and returns it.
func pollFor(t *testing.T, d *Document, keep func(u Update) bool) Update {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		for _, u := range d.Poll() {
			if keep(u) {
				return u
			}
		}
	}
	t.Fatal("the update never came")
	return Update{}
}

var cMajor = [12]bool{true, false, true, false, true, true, false, true, false, true, false, true}

func TestAFactoryIsInitializedWhileADocumentIsOpen(t *testing.T) {
	a := makeFakeARA()
	f, d := fakeDocument(t, a, &sineHost{})
	again := (&FakeARA{a: a}).Factory()
	d2, err := again.NewDocument("Two", &sineHost{})
	if err != nil {
		t.Fatal(err)
	}
	if again != f || a.inits != 1 || a.generation != araGeneration2_3Final || f.Generation != araGeneration2_3Final {
		t.Fatalf("again %v, inits %d, generation %d and %d", again == f, a.inits, a.generation, f.Generation)
	}
	d.Close()
	if a.uninits != 0 {
		t.Fatal("ARA ended with a document open")
	}
	d2.Close()
	if a.uninits != 1 {
		t.Fatal("ARA went on with no document open")
	}
	d3, _ := f.NewDocument("Three", &sineHost{})
	d3.Close()
	if a.inits != 2 || a.uninits != 2 {
		t.Fatalf("inits %d, uninits %d", a.inits, a.uninits)
	}
	if f.Name != "Fake" || f.Vendor != "gunim" || f.Version != "1.0" || f.ID != "org.gunim.fake" ||
		f.ArchiveID != fakeArchiveID || !slices.Equal(f.Compatible, []string{"org.gunim.fake.old"}) ||
		!slices.Equal(f.Analyzes, []ContentType{ContentNotes}) || f.Transforms != Timestretch {
		t.Fatalf("factory %+v", f)
	}
	if !f.Reads("org.gunim.fake.old") || f.Reads("other") {
		t.Fatal("Reads")
	}
}

func TestTheMusicalContextReadsAsTheHostWroteIt(t *testing.T) {
	a := makeFakeARA()
	_, d := fakeDocument(t, a, &sineHost{})
	defer d.Close()
	content := Content{
		Tempo:  []TempoEntry{{0, 0}, {2, 4}, {3.5, 7}},
		Bars:   []BarSignature{{4, 4, 0}, {6, 8, 16}},
		Keys:   []KeySignature{{Root: -1, Scale: cMajor, Name: "F major", Quarter: 0}, {Root: 3, Name: "A", Quarter: 8}},
		Chords: []Chord{{Root: 0, Bass: 4, Intervals: [12]byte{1, 0, 0, 0, 3, 0, 0, 5}, Name: "C/E", Quarter: 0}, {Root: -1, Bass: -1, Quarter: 4}},
	}
	var mc *MusicalContext
	if err := d.Edit(func(e *Edit) {
		mc = e.AddContext(ContextProps{Name: "Song", Content: content})
		e.SetName("Renamed")
	}); err != nil {
		t.Fatal(err)
	}
	fd := a.docs[len(a.docs)-1]
	c := fd.contexts[mc.ref]
	if c.name != "Song" || fd.name != "Renamed" || !slices.Equal(c.tempo, content.Tempo) || !slices.Equal(c.bars, content.Bars) ||
		!slices.Equal(c.keys, content.Keys) || !slices.Equal(c.chords, content.Chords) {
		t.Fatalf("the plugin read %+v of %+v", c, content)
	}
	// Properties alone read nothing again; new content is read again.
	_ = d.Edit(func(e *Edit) { e.UpdateContext(mc, ContextProps{Name: "Song 2", Content: content}) })
	if c.reads != 1 || c.name != "Song 2" {
		t.Fatalf("%d reads, name %q", c.reads, c.name)
	}
	content.Tempo = []TempoEntry{{0, 0}, {0.5, 1}}
	_ = d.Edit(func(e *Edit) { e.UpdateContext(mc, ContextProps{Name: "Song 2", Content: content}) })
	if c.reads != 2 || !slices.Equal(c.tempo, content.Tempo) {
		t.Fatalf("%d reads, tempo %v", c.reads, c.tempo)
	}
	noViolations(t, a)
}

// aDocument is a document of the fake: a context, a sequence, two
// sources of two seconds at 48 kHz, one read as doubles, a modification
// of each and a region of each, the first at 1 s, the second at 4 s.
type aDocument struct {
	a          *fakeARAPlugin
	f          *ARAFactory
	d          *Document
	host       *sineHost
	seq        *RegionSequence
	src, src64 *AudioSource
	mod, mod2  *AudioModification
	r1, r2     *PlaybackRegion
}

func newADocument(t *testing.T) *aDocument {
	t.Helper()
	x := &aDocument{a: makeFakeARA(), host: &sineHost{}}
	x.f, x.d = fakeDocument(t, x.a, x.host)
	t.Cleanup(x.d.Close)
	err := x.d.Edit(func(e *Edit) {
		mc := e.AddContext(ContextProps{Name: "Song", Content: Content{Tempo: []TempoEntry{{0, 0}, {0.5, 1}}}})
		x.seq = e.AddSequence(SequenceProps{Name: "Vocal", Context: mc, Color: &Color{1, 0, 0}})
		x.src = e.AddSource(SourceProps{Name: "one", ID: "media-1", Frames: 96000, Rate: 48000, Channels: 2})
		x.src64 = e.AddSource(SourceProps{Name: "two", ID: "media-64", Frames: 96000, Rate: 48000, Channels: 1, Merits64: true})
		x.mod = e.AddModification(x.src, ModificationProps{Name: "one", ID: "take-1"})
		x.mod2 = e.AddModification(x.src64, ModificationProps{Name: "two", ID: "take-2"})
		x.r1 = e.AddRegion(x.mod, RegionProps{Start: 0, Duration: 2, At: 1, Length: 2, Sequence: x.seq})
		x.r2 = e.AddRegion(x.mod2, RegionProps{Start: 0.5, Duration: 1, At: 4, Length: 1, Sequence: x.seq, Name: "two"})
		e.EnableAudio(x.src, true)
		e.EnableAudio(x.src64, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	return x
}

// analyse analyses both sources, and returns the progress told.
func (x *aDocument) analyse(t *testing.T) []Update {
	t.Helper()
	x.d.Analyse(x.src)
	x.d.Analyse(x.src64)
	var got []Update
	done := map[*AudioSource]bool{}
	for end := time.Now().Add(5 * time.Second); len(done) < 2 && time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		for _, u := range x.d.Poll() {
			if u.Kind == Analysis {
				got = append(got, u)
				if u.Done {
					done[u.Source] = true
				}
			}
		}
	}
	if len(done) < 2 || x.d.Analysing(x.src) || x.d.Analysing(x.src64) {
		t.Fatalf("the analysis finished for %d sources", len(done))
	}
	return got
}

// render renders frames at song frame from through p, and returns its
// left and right.
func render(p *Plugin, from int64, frames int) [][]float32 {
	out := [][]float32{make([]float32, frames), make([]float32, frames)}
	b := &plug.Block{Frames: frames, In: [][]float32{make([]float32, frames), make([]float32, frames)}, Out: out,
		Position: plug.Position{Frame: from, Playing: true}}
	p.ProcessBlock(b)
	return out
}

func TestAnAnalysisReadsTheSourcesAndTellsItsProgress(t *testing.T) {
	x := newADocument(t)
	got := x.analyse(t)
	// Each source: started at 0, done at 1, the float passed whole.
	var started, finished int
	for _, u := range got {
		switch {
		case u.Done && u.Progress == 1:
			finished++
		case !u.Done && u.Progress == 0:
			started++
		default:
			t.Errorf("progress %+v", u)
		}
	}
	if started != 2 || finished != 2 {
		t.Fatalf("%d started, %d finished", started, finished)
	}
	// The fake read every frame as the host has them, as floats and as
	// doubles.
	fd := x.a.docs[0]
	for _, s := range []*AudioSource{x.src, x.src64} {
		fs := fd.sources[s.ref]
		want := make([][]float32, s.Props().Channels)
		for c := range want {
			want[c] = make([]float32, s.Props().Frames)
		}
		x.host.ReadAudio(s, 0, want)
		for c := range want {
			if !slices.Equal(fs.samples[c], want[c]) {
				t.Fatalf("source %s channel %d read otherwise", s.Props().ID, c)
			}
		}
	}
	noViolations(t, x.a)
}

func TestARendererRendersItsRegionsWhereTheSongIs(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	p, fp := bound(t, x.a, x.d, PlaybackRenderer)
	defer p.Close()
	if p.ARA() == nil || p.ARA().Roles() != PlaybackRenderer || p.ARA().Document() != x.d {
		t.Fatal("the plugin is bound to nothing")
	}
	if err := p.ARA().SetRegions(x.r1); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fp.araExt.regions, []uintptr{x.r1.ref}) || !slices.Equal(p.ARA().Regions(), []*PlaybackRegion{x.r1}) {
		t.Fatal("the renderer holds other regions")
	}
	if !fp.isActive {
		t.Fatal("the renderer stayed stopped after its regions changed")
	}
	// Song frame 48000+100 is the source's frame 100.
	out := render(p, 48100, 256)
	want := [][]float32{make([]float32, 256), make([]float32, 256)}
	x.host.ReadAudio(x.src, 100, want)
	for c := range 2 {
		if !slices.Equal(out[c], want[c]) {
			t.Fatalf("channel %d: %v, the source %v", c, out[c][:4], want[c][:4])
		}
	}
	// Before the region, silence; with both, the second where it is.
	if rms(render(p, 0, 256)[0]) != 0 {
		t.Fatal("sound before the region")
	}
	if err := p.ARA().SetRegions(x.r1, x.r2); err != nil {
		t.Fatal(err)
	}
	out = render(p, 4*48000, 256)
	mono := [][]float32{make([]float32, 256)}
	x.host.ReadAudio(x.src64, 24000, mono)
	if !slices.Equal(out[0], mono[0]) || !slices.Equal(out[1], mono[0]) {
		t.Fatalf("the second region: %v, the source %v", out[0][:4], mono[0][:4])
	}
	// Only the second.
	_ = p.ARA().SetRegions(x.r2)
	if rms(render(p, 48100, 256)[0]) != 0 {
		t.Fatal("the first region still sounds")
	}
	noViolations(t, x.a)
}

func TestAnEditInThePluginComesBackAsNewsAndChangesTheSound(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	p, _ := bound(t, x.a, x.d, PlaybackRenderer)
	defer p.Close()
	_ = p.ARA().SetRegions(x.r1)
	before := render(p, 48100, 256)
	x.a.docs[0].edit("take-1", 0.5, 0.25, 0.5)
	u := pollFor(t, x.d, func(u Update) bool { return u.Kind == ModificationChanged })
	if u.Modification != x.mod || u.Range == nil || *u.Range != (TimeRange{0.25, 0.5}) || u.Flags != NotesUnchanged {
		t.Fatalf("news %+v", u)
	}
	after := render(p, 48100, 256)
	for i := range after[0] {
		if after[0][i] != 0.5*before[0][i] {
			t.Fatalf("frame %d: %v after, %v before", i, after[0][i], before[0][i])
		}
	}
	noViolations(t, x.a)
}

func TestArchivesStoreWholeOrByObjectAndRestoreUnderNewNames(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	x.a.docs[0].data = "settings"
	x.a.docs[0].edit("take-1", 0.25, 0, 2)
	whole, err := x.d.Store(nil)
	if err != nil {
		t.Fatal(err)
	}
	var ar fakeArchive
	if err := json.Unmarshal(whole, &ar); err != nil {
		t.Fatalf("%v: %q", err, whole)
	}
	if ar.Doc != "settings" || len(ar.Sources) != 2 || ar.Mods["take-1"] != 0.25 || ar.Mods["take-2"] != 1 {
		t.Fatalf("whole: %+v", ar)
	}
	part, err := x.d.Store(&StoreFilter{Modifications: []*AudioModification{x.mod}})
	if err != nil {
		t.Fatal(err)
	}
	ar = fakeArchive{}
	_ = json.Unmarshal(part, &ar)
	if ar.Doc != "" || len(ar.Sources) != 0 || len(ar.Mods) != 1 || ar.Mods["take-1"] != 0.25 {
		t.Fatalf("part: %+v", ar)
	}
	src, err := x.d.Store(&StoreFilter{Document: true, Sources: []*AudioSource{x.src}})
	if err != nil {
		t.Fatal(err)
	}
	ar = fakeArchive{}
	_ = json.Unmarshal(src, &ar)
	if ar.Doc != "settings" || len(ar.Sources) != 1 || len(ar.Mods) != 0 {
		t.Fatalf("source: %+v", ar)
	}

	// A take copied: its new modification takes the old one's edits.
	var m9 *AudioModification
	err = x.d.Edit(func(e *Edit) {
		m9 = e.AddModification(x.src, ModificationProps{Name: "copy", ID: "take-9"})
		if err := e.Restore(part, fakeArchiveID, &RestoreFilter{Modifications: []Rename{{"take-1", "take-9"}}}); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	fd := x.a.docs[0]
	if fd.mods[m9.ref].gain != 0.25 || fd.mods[x.mod2.ref].gain != 1 {
		t.Fatalf("gains %v and %v", fd.mods[m9.ref].gain, fd.mods[x.mod2.ref].gain)
	}
	// An archive of a form the plugin reads none of fails, and so does
	// one it cannot read.
	err = x.d.Edit(func(e *Edit) { _ = e.Restore(part, "someone.else", nil) })
	if err == nil {
		t.Fatal("an archive of another form restored")
	}
	err = x.d.Edit(func(e *Edit) { _ = e.Restore([]byte("{"), "org.gunim.fake.old", nil) })
	if err == nil {
		t.Fatal("a broken archive restored")
	}
	_ = x.d.Edit(func(e *Edit) { e.RemoveModification(m9) })
	noViolations(t, x.a)
}

func TestAViewTakesTheSelectionAndTheEditorRendererItsRegions(t *testing.T) {
	x := newADocument(t)
	v, fp := bound(t, x.a, x.d, EditorView|EditorRenderer)
	defer v.Close()
	ext := v.ARA()
	if err := ext.SetRegions(x.r1); err == nil {
		t.Fatal("a view renders regions")
	}
	if err := ext.Select(Selection{Regions: []*PlaybackRegion{x.r2, x.r1}, Sequences: []*RegionSequence{x.seq},
		Range: &TimeRange{1, 4}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fp.araExt.selection, []uintptr{x.r2.ref, x.r1.ref}) || *fp.araExt.selRange != (TimeRange{1, 4}) {
		t.Fatalf("selection %v, range %v", fp.araExt.selection, fp.araExt.selRange)
	}
	if names, span := (&FakeARA{a: x.a}).Selected(x.d); !slices.Equal(names, []string{"two", ""}) || *span != (TimeRange{1, 4}) {
		t.Fatalf("Selected: %q, %v", names, span)
	}
	_ = ext.Select(Selection{})
	if len(fp.araExt.selection) != 0 || fp.araExt.selRange != nil {
		t.Fatal("an empty selection kept the last")
	}
	_ = ext.Hide(x.seq)
	if !slices.Equal(fp.araExt.hidden, []uintptr{x.seq.ref}) {
		t.Fatal("hide")
	}
	_ = ext.SetEditorRegions([]*PlaybackRegion{x.r1}, []*RegionSequence{x.seq})
	_ = ext.SetEditorRegions([]*PlaybackRegion{x.r2}, nil)
	if !slices.Equal(fp.araExt.editorRegions, []uintptr{x.r2.ref}) || len(fp.araExt.editorSeqs) != 0 {
		t.Fatalf("editor regions %v, sequences %v", fp.araExt.editorRegions, fp.araExt.editorSeqs)
	}
	// A region taken out leaves the instances that held it first.
	_ = x.d.Edit(func(e *Edit) { e.RemoveRegion(x.r2) })
	if len(fp.araExt.editorRegions) != 0 {
		t.Fatal("the editor renderer still holds the region taken out")
	}
	noViolations(t, x.a)
}

func TestThePluginsTransportRequestsComeAsUpdates(t *testing.T) {
	x := newADocument(t)
	fake := &FakeARA{a: x.a}
	fake.Request(x.d, Play, 0, 0)
	fake.Request(x.d, Locate, 12.5, 0)
	fake.Request(x.d, Cycle, 2, 6.25)
	fake.Request(x.d, Stop, 0, 0)
	got := x.d.Poll()
	want := []Update{{Kind: Play}, {Kind: Locate, Time: 12.5}, {Kind: Cycle, Time: 2, Duration: 6.25}, {Kind: CycleOn, On: true},
		{Kind: Stop}}
	if !slices.Equal(got, want) {
		t.Fatalf("updates %+v", got)
	}
	if h, tl := x.d.HeadAndTail(x.r1); h != 0.01 || tl != 0.02 {
		t.Fatalf("head %v, tail %v", h, tl)
	}
}

func TestADocumentClosesItsPluginsAndObjects(t *testing.T) {
	x := newADocument(t)
	x.analyse(t)
	p, _ := bound(t, x.a, x.d, PlaybackRenderer)
	_ = p.ARA().SetRegions(x.r1, x.r2)
	x.d.Close()
	x.d.Close()
	fd := x.a.docs[0]
	if !fd.destroyed || !p.closed {
		t.Fatalf("destroyed %v, plugin closed %v", fd.destroyed, p.closed)
	}
	if err := x.d.Edit(func(e *Edit) {}); err == nil {
		t.Fatal("a closed document took an edit")
	}
	if _, err := x.d.Store(nil); err == nil {
		t.Fatal("a closed document stored")
	}
	noViolations(t, x.a)
}

func TestAFakesEditsAreHeardThroughItsExportedSide(t *testing.T) {
	x := newADocument(t)
	fake := &FakeARA{a: x.a}
	fake.Edit(x.d, "take-2", 0.75, TimeRange{0, 1})
	if g := fake.Gain(x.d, "take-2"); g != 0.75 {
		t.Fatalf("gain %v", g)
	}
	u := pollFor(t, x.d, func(u Update) bool { return u.Kind == ModificationChanged })
	if u.Modification != x.mod2 || len(fake.Broken()) != 0 {
		t.Fatalf("news %+v, broken %v", u, fake.Broken())
	}
}

func TestABindFailsForAPluginWithoutARA(t *testing.T) {
	x := newADocument(t)
	fp := &fakePlugin{outs: []fakeBus{{name: "Out", channels: 2}}}
	fp.makeObjects()
	_, err := x.d.newPluginWith(Config{Rate: 48000}, PlaybackRenderer, func(p *Plugin) error { return p.build(fp.comp, nil) })
	if err == nil {
		t.Fatal("a plugin without ARA bound")
	}
	if _, err := x.d.newPluginWith(Config{Rate: 48000}, 0, nil); err == nil {
		t.Fatal("a plugin of no role bound")
	}
	if len(x.d.livePlugins()) != 0 {
		t.Fatal("a failed bind is kept")
	}
}

func TestAScanMarksTheClassesAnARAFactoryPairsWith(t *testing.T) {
	dir := t.TempDir()
	res := filepath.Join(dir, "Melodyne.vst3", "Contents", "Resources")
	if err := os.MkdirAll(res, 0o755); err != nil {
		t.Fatal(err)
	}
	info := `{"Factory Info": {"Vendor": "Celemony"}, "Classes": [
		{"CID": "5653544D6C70676D656C6F64796E6500", "Category": "Audio Module Class", "Name": "Melodyne", "Sub Categories": ["Fx"]},
		{"CID": "5653544D6C70676D656C6F64796E6501", "Category": "ARA Main Factory Class", "Name": "Melodyne"},
		{"CID": "5653544D6C70676D656C6F64796E6502", "Category": "Audio Module Class", "Name": "Other", "Sub Categories": ["Fx"]},
	]}`
	if err := os.WriteFile(filepath.Join(res, "moduleinfo.json"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	b := Scan(dir)
	if len(b) != 1 || len(b[0].Effects) != 2 {
		t.Fatalf("the scan found %+v", b)
	}
	for _, c := range b[0].Effects {
		if c.ARA != (c.Name == "Melodyne") {
			t.Fatalf("%s: ARA %v", c.Name, c.ARA)
		}
	}
}
