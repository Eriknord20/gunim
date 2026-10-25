package vst3

import (
	"cmp"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"
	"unsafe"
)

// FakeARA is an ARA plugin written in Go, laid out as a plugin's ARA
// side and its VST3 component are, for the tests of a host that has no
// ARA plugin to hand. It keeps ARA's rules as the ARA library's debug
// builds check them, and Broken lists each it saw broken. Its analysis
// reads each source whole through the host, on a goroutine of its own,
// reading a source whose ID ends in "64" as doubles, with one of the
// algorithms of fakeAlgorithms, and finds a note, an A4, in each stretch
// of sound between silences of 10 ms or more; Detect adds a tempo and
// chords to what it finds. Its renderers play the regions they hold
// where the process context says the song is, stretched and faded as
// their transforms ask, each sample of the source times its
// modification's gain, which Edit sets as a user does in a plugin's
// editor; its archives are JSON, and an instance's state holds its
// view's zoom.
type FakeARA struct{ a *fakeARAPlugin }

// fakeAlgorithms are the fake's processing algorithms; a source starts
// with the first.
var fakeAlgorithms = []Algorithm{
	{"org.gunim.fake.melodic", "Melodic"},
	{"org.gunim.fake.percussive", "Percussive"},
	{"org.gunim.fake.polyphonic", "Polyphonic"},
}

// fakeFade is how long the fake's fades at a region's head and tail
// last, in seconds.
const fakeFade = 0.002

// NewFakeARA makes a fake ARA plugin.
func NewFakeARA() *FakeARA { return &FakeARA{a: makeFakeARA()} }

// Factory returns the fake's ARA factory, of the class "Fake": its
// documents' NewPlugin makes instances of the fake.
func (f *FakeARA) Factory() *ARAFactory {
	var fa *ARAFactory
	onUI(func() {
		fa, _ = araFactoryAt(f.a.factory.addr(), Class{Name: "Fake", Category: "Audio Module Class", SubCategories: "Fx", ARA: true}, nil)
	})
	fa.build = f.a.build
	return fa
}

// Edit sets the gain of the modification of ID id in document d, as a
// user does in the plugin's editor, and has the plugin tell d's host
// that changed over r, of the modification's time, as it next polls.
func (f *FakeARA) Edit(d *Document, id string, gain float64, r TimeRange) {
	if fd := f.a.docOf(d); fd != nil {
		fd.edit(id, gain, r.Start, r.Duration)
	}
}

// Gain returns the gain of the modification of ID id in document d.
func (f *FakeARA) Gain(d *Document, id string) float64 {
	fd := f.a.docOf(d)
	if fd == nil {
		return 0
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	for _, m := range fd.mods {
		if m.id == id {
			return m.gain
		}
	}
	return 0
}

// Detect has the fake's analysis of each source of document d find a
// constant tempo of bpm and chords too, graded as detected; a bpm of 0
// finds no tempo. The host hears that each source analysed changed.
func (f *FakeARA) Detect(d *Document, bpm float64, chords []Chord) {
	fd := f.a.docOf(d)
	if fd == nil {
		return
	}
	fd.mu.Lock()
	fd.bpm, fd.chords = bpm, slices.Clone(chords)
	var changed []*fakeSource
	for _, s := range fd.sources {
		if s.analysed {
			changed = append(changed, s)
		}
	}
	fd.mu.Unlock()
	for _, s := range changed {
		fd.later(func() { fd.host.model.call(1, s.host, 0, uintptr(SignalUnchanged|NotesUnchanged)) })
	}
}

// Regions returns the properties of the regions of document d as the
// fake read them, in the order of their places in the song: each with
// its transforms, times, sequence and name.
func (f *FakeARA) Regions(d *Document) []RegionProps {
	fd := f.a.docOf(d)
	if fd == nil {
		return nil
	}
	var out []RegionProps
	onUI(func() {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		for _, r := range fd.regions {
			p := r.props
			if q := fd.seqs[r.seq]; q != nil {
				p.Sequence = handle[RegionSequence](q.host)
			}
			out = append(out, p)
		}
	})
	slices.SortFunc(out, func(a, b RegionProps) int {
		if a.At != b.At {
			return cmp.Compare(a.At, b.At)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// SetView sets the zoom of the view of p, an instance of the fake, as a
// user does in its editor; the instance's state holds it.
func (f *FakeARA) SetView(p *Plugin, zoom float64) {
	if fi := f.a.instanceOf(p); fi != nil {
		fi.mu.Lock()
		fi.view = zoom
		fi.mu.Unlock()
	}
}

// View returns the zoom of the view of p, an instance of the fake.
func (f *FakeARA) View(p *Plugin) float64 {
	fi := f.a.instanceOf(p)
	if fi == nil {
		return 0
	}
	fi.mu.Lock()
	defer fi.mu.Unlock()
	return fi.view
}

// instanceOf returns the fake's instance that is p.
func (a *fakeARAPlugin) instanceOf(p *Plugin) *fakeInstance {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, fi := range a.instances {
		if fi.comp == p.comp {
			return fi
		}
	}
	return nil
}

// Broken lists the rules of ARA's the fake saw broken.
func (f *FakeARA) Broken() []string { return f.a.broken() }

// Selected returns what the host last picked for the editor view of
// document d to show: the names of the regions, and the span, or nil.
func (f *FakeARA) Selected(d *Document) (regions []string, span *TimeRange) {
	fd := f.a.docOf(d)
	if fd == nil {
		return nil, nil
	}
	var sel []uintptr
	onUI(func() {
		for _, x := range fd.exts {
			if x.roles&EditorView != 0 {
				sel, span = slices.Clone(x.selection), x.selRange
			}
		}
	})
	fd.mu.Lock()
	defer fd.mu.Unlock()
	for _, ref := range sel {
		if r := fd.regions[ref]; r != nil {
			regions = append(regions, r.props.Name)
		}
	}
	return regions, span
}

// Request has the fake ask document d's host's transport, as a plugin's
// editor does: to play, to stop, to move to at seconds, or to loop from
// at for length seconds, and to turn the loop on.
func (f *FakeARA) Request(d *Document, what UpdateKind, at, length float64) {
	fd := f.a.docOf(d)
	if fd == nil {
		return
	}
	pb := fd.host.playback
	switch what { //nolint:exhaustive // the transport's requests alone
	case Play:
		pb.call(0)
	case Stop:
		pb.call(1)
	case Locate:
		callLocate(pb, at)
	case Cycle:
		callCycle(pb, at, length)
		pb.call(4, 1)
	}
}

// docOf returns the fake's document of the host's d.
func (a *fakeARAPlugin) docOf(d *Document) *fakeDoc {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, fd := range a.docs {
		if fd.ref == d.dc.ref {
			return fd
		}
	}
	return nil
}

type fakeARAPlugin struct {
	mu         sync.Mutex
	factory    *cstruct
	inits      int
	uninits    int
	generation int
	docs       []*fakeDoc
	violations []string
	// instances are the fake's instances, as made.
	instances []*fakeInstance
	// algorithms are fakeAlgorithms, as the host reads them.
	algorithms []*cstruct
}

// fakeArchiveID is the fake's archives' form.
const fakeArchiveID = "org.gunim.fake.archive"

func makeFakeARA() *fakeARAPlugin {
	a := &fakeARAPlugin{}
	f := newStruct(araFactoryFields...)
	f.put(afLowest, araGeneration2Final)
	f.put(afHighest, araGeneration2_3Final)
	f.putString(afID, "org.gunim.fake")
	f.putPtr(afInit, newCallback(func(cfg uintptr) uintptr {
		r := readAt(cfg, fPtr, fI32, fPtr)
		a.mu.Lock()
		a.inits++
		a.generation = int(r.i32(1))
		a.mu.Unlock()
		return 0
	}), nil)
	f.putPtr(afUninit, newCallback(func() uintptr {
		a.mu.Lock()
		a.uninits++
		a.mu.Unlock()
		return 0
	}), nil)
	f.putString(afName, "Fake")
	f.putString(afVendor, "gunim")
	f.putString(afURL, "https://example.com")
	f.putString(afVersion, "1.0")
	f.putPtr(afCreate, newCallback(func(host, props uintptr) uintptr { return a.create(host, props) }), nil)
	f.putString(afArchiveID, fakeArchiveID)
	compat := cstr("org.gunim.fake.old")
	ids := []uintptr{uintptr(unsafe.Pointer(&compat[0]))}
	f.put(afCompatibleN, 1)
	f.putPtr(afCompatible, uintptr(unsafe.Pointer(&ids[0])), ids)
	f.pins = append(f.pins, compat)
	types := []int32{int32(ContentNotes)}
	f.put(afAnalyzesN, 1)
	f.putPtr(afAnalyzes, uintptr(unsafe.Pointer(&types[0])), types)
	f.put(afTransforms, uint64(Timestretch|FadeAtHead|FadeAtTail))
	a.factory = f
	for _, al := range fakeAlgorithms {
		s := newStruct(algorithmFields...)
		s.putString(1, al.ID)
		s.putString(2, al.Name)
		a.algorithms = append(a.algorithms, s)
	}
	return a
}

// violate writes down a rule broken.
func (a *fakeARAPlugin) violate(format string, args ...any) {
	a.mu.Lock()
	a.violations = append(a.violations, fmt.Sprintf(format, args...))
	a.mu.Unlock()
}

func (a *fakeARAPlugin) broken() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.violations)
}

// The host's half, as the fake reads it.
type fakeHostSide struct {
	audio, archive, content, model, playback iface
}

type fakeDoc struct {
	a         *fakeARAPlugin
	ref       uintptr
	host      fakeHostSide
	inst      *cstruct
	name      string
	editing   bool
	destroyed bool
	data      string

	mu       sync.Mutex
	contexts map[uintptr]*fakeContext
	seqs     map[uintptr]*fakeSeq
	sources  map[uintptr]*fakeSource
	mods     map[uintptr]*fakeMod
	regions  map[uintptr]*fakeRegion
	// news is what the fake tells at its next notifyModelUpdates.
	news []func()
	// readers are the audio readers made, to destroy.
	readers []uintptr
	exts    []*fakeExt
	wg      sync.WaitGroup
	// The edit cycle's restores, for ARA's rules of partial
	// persistence: the sources made in it, the sources and
	// modifications whose states it restored, and whether it restored
	// the document's data.
	made                     map[uintptr]bool
	srcRestored, modRestored map[uintptr]bool
	docRestored              bool
	// bpm and chords are what Detect has the analysis find besides notes.
	bpm    float64
	chords []Chord
	// contentReaders counts the content readers the host holds.
	contentReaders int
}

type fakeContext struct {
	host   uintptr
	name   string
	tempo  []TempoEntry
	bars   []BarSignature
	keys   []KeySignature
	chords []Chord
	reads  int
}

type fakeSeq struct {
	host, ctx uintptr
	name      string
}

type fakeSource struct {
	host      uintptr
	id        string
	frames    int64
	rate      float64
	channels  int
	enabled   bool
	requested bool
	analysing bool
	analysed  bool
	samples   [][]float32
	peak      float64
	// algo is the index of its algorithm in fakeAlgorithms; again says
	// it changed while an analysis ran, which must run again.
	algo  int
	again bool
	notes []Note
}

type fakeMod struct {
	host, src uintptr
	id        string
	gain      float64
	// edit is the span of the source's time whose notes the gain makes
	// louder or softer, or nil for all of it.
	edit *TimeRange
}

type fakeRegion struct {
	host, mod, seq uintptr
	props          RegionProps
}

// create makes a document controller for the host's instance.
func (a *fakeARAPlugin) create(host, props uintptr) uintptr {
	h := readAt(host, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr)
	if !h.has(10) {
		a.violate("the host's instance is short: %d bytes", h.get(0))
	}
	d := &fakeDoc{a: a, contexts: map[uintptr]*fakeContext{}, seqs: map[uintptr]*fakeSeq{},
		sources: map[uintptr]*fakeSource{}, mods: map[uintptr]*fakeMod{}, regions: map[uintptr]*fakeRegion{}}
	d.host = fakeHostSide{audio: iface{h.ptr(1), h.ptr(2)}, archive: iface{h.ptr(3), h.ptr(4)},
		content: iface{h.ptr(5), h.ptr(6)}, model: iface{h.ptr(7), h.ptr(8)}, playback: iface{h.ptr(9), h.ptr(10)}}
	d.name = readAt(props, fPtr, fPtr).str(1)
	d.ref = newHandle(d)
	d.inst = newStruct(fPtr, fPtr, fPtr)
	d.inst.put(1, uint64(d.ref))
	d.inst.putPtr(2, fakeDCTable().addr(), nil)
	a.mu.Lock()
	a.docs = append(a.docs, d)
	a.mu.Unlock()
	return d.inst.addr()
}

// mustEdit writes down a change of the model made outside an edit.
func (d *fakeDoc) mustEdit(what string) {
	if !d.editing {
		d.a.violate("%s outside beginEditing and endEditing", what)
	}
}

// later queues news for the next notifyModelUpdates.
func (d *fakeDoc) later(fn func()) {
	d.mu.Lock()
	d.news = append(d.news, fn)
	d.mu.Unlock()
}

// readContent reads a musical context's content through the host.
func (d *fakeDoc) readContent(c *fakeContext) {
	c.reads++
	c.tempo, c.bars, c.keys, c.chords = nil, nil, nil, nil
	ca := d.host.content
	for _, t := range []ContentType{ContentTempo, ContentBars, ContentKeys, ContentSheetChords} {
		if ca.call(0, c.host, uintptr(t))&0xffffffff == 0 {
			continue
		}
		if g := ca.call(1, c.host, uintptr(t)); g != gradeApproved {
			d.a.violate("the host grades its content %d", g)
		}
		rd := ca.call(2, c.host, uintptr(t), 0)
		var got ObjectContent
		n := int(int32(uint32(ca.call(6, rd))))
		for i := range n {
			decodeEvent(t, ca.call(7, rd, uintptr(i)), &got)
		}
		ca.call(8, rd)
		c.tempo, c.bars, c.keys, c.chords = append(c.tempo, got.Tempo...), append(c.bars, got.Bars...),
			append(c.keys, got.Keys...), append(c.chords, got.Chords...)
	}
}

// startAnalysis reads the source whole through a reader of the host's,
// on a goroutine of the fake's own, as a plugin's thread does.
func (d *fakeDoc) startAnalysis(s *fakeSource) {
	d.mu.Lock()
	if !s.enabled || !s.requested || s.analysing || s.analysed || d.editing {
		d.mu.Unlock()
		return
	}
	s.analysing = true
	d.mu.Unlock()
	use64 := len(s.id) > 2 && s.id[len(s.id)-2:] == "64"
	rd := d.host.audio.call(0, s.host, araBool(use64))
	if rd == 0 {
		d.a.violate("the host made no audio reader")
		return
	}
	d.mu.Lock()
	d.readers = append(d.readers, rd)
	d.mu.Unlock()
	d.later(func() { callProgress(d.host.model, s.host, 0, 0) })
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		n := int(s.frames)
		samples := make([][]float32, s.channels)
		// The host reads where bufs lies, as a number. Go would keep so
		// small a list on this goroutine's stack, which moves as the
		// host's code, run on it, grows it; pinned, it lies on the heap.
		bufs := make([]uintptr, s.channels)
		var pin runtime.Pinner
		if len(bufs) > 0 {
			pin.Pin(&bufs[0])
		}
		defer pin.Unpin()
		var d64 [][]float64
		for c := range samples {
			samples[c] = make([]float32, n)
			if use64 {
				d64 = append(d64, make([]float64, n))
				bufs[c] = uintptr(unsafe.Pointer(&d64[c][0]))
			} else {
				bufs[c] = uintptr(unsafe.Pointer(&samples[c][0]))
			}
		}
		// In two reads, the second past the end.
		half := int64(n / 2)
		ok := d.host.audio.call(1, rd, 0, uintptr(half), uintptr(unsafe.Pointer(&bufs[0])))&0xffffffff != 0
		for c := range bufs {
			if use64 {
				bufs[c] = uintptr(unsafe.Pointer(&d64[c][half]))
			} else {
				bufs[c] = uintptr(unsafe.Pointer(&samples[c][half]))
			}
		}
		ok = ok && d.host.audio.call(1, rd, uintptr(half), uintptr(int64(n)-half), uintptr(unsafe.Pointer(&bufs[0])))&0xffffffff != 0
		if !ok {
			d.a.violate("a read of the source failed")
		}
		peak := 0.0
		for c := range samples {
			if use64 {
				for i, v := range d64[c] {
					samples[c][i] = float32(v)
				}
			}
			for _, v := range samples[c] {
				peak = max(peak, math.Abs(float64(v)))
			}
		}
		notes := findNotes(samples, s.rate)
		d.mu.Lock()
		again := s.again
		s.samples, s.peak, s.notes, s.analysing, s.analysed, s.again = samples, peak, notes, false, !again, false
		d.mu.Unlock()
		if again {
			// Its algorithm changed meanwhile: it reads the source again.
			d.later(func() { d.startAnalysis(s) })
			return
		}
		d.later(func() {
			callProgress(d.host.model, s.host, 2, 1)
			d.host.model.call(1, s.host, 0, uintptr(SignalUnchanged))
		})
	}()
}

// setAlgorithm has s analysed with algorithm k from now on, again where
// it was analysed with another. d.mu is held.
func (d *fakeDoc) setAlgorithm(s *fakeSource, k int) {
	if s.algo == k {
		return
	}
	s.algo, s.requested = k, true
	if s.analysing {
		s.again = true
	} else {
		s.analysed = false
	}
}

// findNotes returns a note, an A4 as loud as its peak, for each stretch
// of samples between silences of 10 ms or more.
func findNotes(samples [][]float32, rate float64) []Note {
	if len(samples) == 0 {
		return nil
	}
	quiet := int(math.Ceil(0.01 * rate))
	var notes []Note
	add := func(from, to int, peak float64) {
		dur := float64(to-from) / rate
		notes = append(notes, Note{Frequency: 440, Pitch: 69, Volume: peak, Start: float64(from) / rate, Duration: dur,
			Signal: dur})
	}
	start, run, peak := -1, 0, 0.0
	for i := range samples[0] {
		v := 0.0
		for _, ch := range samples {
			v = max(v, math.Abs(float64(ch[i])))
		}
		if v < 1e-4 {
			run++
			if start >= 0 && run >= quiet {
				add(start, i-run+1, peak)
				start = -1
			}
			continue
		}
		if start < 0 {
			start, peak = i, 0
		}
		run, peak = 0, max(peak, v)
	}
	if start >= 0 {
		add(start, len(samples[0])-run, peak)
	}
	return notes
}

// The fake's content, by its objects. d.mu is held.

// sourceEvents returns the content of kind k the fake has of s, its
// notes as notes gives them, or nil where it has none.
func (d *fakeDoc) sourceEvents(s *fakeSource, k ContentType, notes func() []Note) *contentEvents {
	if s == nil || !s.analysed {
		return nil
	}
	switch k { //nolint:exhaustive // the kinds the fake finds
	case ContentNotes:
		return noteEvents(notes())
	case ContentTempo:
		if d.bpm > 0 {
			return contentOf(Content{Tempo: []TempoEntry{{0, 0}, {60 / d.bpm, 1}}})[ContentTempo]
		}
	case ContentSheetChords:
		return contentOf(Content{Chords: d.chords})[ContentSheetChords]
	}
	return nil
}

// modNotes returns the notes of m: its source's, those in its edit as
// loud as its gain makes them.
func (d *fakeDoc) modNotes(m *fakeMod) []Note {
	notes := slices.Clone(d.sources[m.src].notes)
	for i, n := range notes {
		if m.edit == nil || n.Start < m.edit.Start+m.edit.Duration && n.Start+n.Duration > m.edit.Start {
			notes[i].Volume = min(n.Volume*m.gain, 1)
		}
	}
	return notes
}

// regionNotes returns the notes of r's modification in the stretch it
// plays, cut to it, in the song's time.
func (d *fakeDoc) regionNotes(r *fakeRegion) []Note {
	p := r.props
	scale := 1.0
	if p.Duration > 0 {
		scale = p.Length / p.Duration
	}
	end := p.Start + p.Duration
	var out []Note
	for _, n := range d.modNotes(d.mods[r.mod]) {
		from, to, sig := max(n.Start, p.Start), min(n.Start+n.Duration, end), min(n.Start+n.Signal, end)
		if to <= from {
			continue
		}
		n.Start, n.Duration, n.Signal = p.At+(from-p.Start)*scale, (to-from)*scale, max(sig-from, 0)*scale
		n.Attack *= scale
		out = append(out, n)
	}
	return out
}

// fakeDocOf returns the fake document of ref.
func fakeDocOf(ref uintptr) *fakeDoc { return handle[fakeDoc](ref) }

// The document controller's functions, made once.
var fakeDCTable = sync.OnceValue(func() fnTable {
	none := func(ctl, a, b, c uintptr) uintptr { return 0 }
	fns := make([]uintptr, dcRequestProcessingAlgorithmForAudioSource+1)
	for i := range fns {
		fns[i] = newCallback(none)
	}
	set := func(i int, fn any) { fns[i] = newCallback(fn) }
	set(dcDestroy, func(ctl uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.wg.Wait()
		d.mu.Lock()
		left := len(d.contexts) + len(d.seqs) + len(d.sources) + len(d.mods) + len(d.regions)
		d.mu.Unlock()
		if left > 0 {
			d.a.violate("the document went with %d objects in it", left)
		}
		if d.contentReaders > 0 {
			d.a.violate("the document went with %d content readers open", d.contentReaders)
		}
		d.destroyed = true
		return 0
	})
	set(dcGetFactory, func(ctl uintptr) uintptr { return fakeDocOf(ctl).a.factory.addr() })
	set(dcBeginEditing, func(ctl uintptr) uintptr {
		d := fakeDocOf(ctl)
		if d.editing {
			d.a.violate("beginEditing twice")
		}
		d.editing = true
		d.made, d.srcRestored, d.modRestored, d.docRestored = map[uintptr]bool{}, map[uintptr]bool{}, map[uintptr]bool{}, false
		return 0
	})
	set(dcEndEditing, func(ctl uintptr) uintptr {
		d := fakeDocOf(ctl)
		if !d.editing {
			d.a.violate("endEditing with no beginEditing")
		}
		d.editing = false
		d.mu.Lock()
		srcs := make([]*fakeSource, 0, len(d.sources))
		for _, s := range d.sources {
			srcs = append(srcs, s)
		}
		d.mu.Unlock()
		for _, s := range srcs {
			d.startAnalysis(s)
		}
		return 0
	})
	set(dcNotifyModelUpdates, func(ctl uintptr) uintptr {
		d := fakeDocOf(ctl)
		if d.editing {
			d.a.violate("notifyModelUpdates while editing")
		}
		d.mu.Lock()
		news := d.news
		d.news = nil
		d.mu.Unlock()
		for _, fn := range news {
			fn()
		}
		return 0
	})
	set(dcUpdateDocumentProperties, func(ctl, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updateDocumentProperties")
		d.name = readAt(props, fPtr, fPtr).str(1)
		return 0
	})
	ctxFields := []int{fPtr, fPtr, fI32, fPtr}
	set(dcCreateMusicalContext, func(ctl, host, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("createMusicalContext")
		c := &fakeContext{host: host, name: readAt(props, ctxFields...).str(1)}
		d.readContent(c)
		ref := newHandle(c)
		d.mu.Lock()
		d.contexts[ref] = c
		d.mu.Unlock()
		return ref
	})
	set(dcUpdateMusicalContextProperties, func(ctl, ref, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updateMusicalContextProperties")
		d.contexts[ref].name = readAt(props, ctxFields...).str(1)
		return 0
	})
	set(dcUpdateMusicalContextContent, func(ctl, ref, rng, flags uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updateMusicalContextContent")
		d.readContent(d.contexts[ref])
		return 0
	})
	set(dcDestroyMusicalContext, func(ctl, ref uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("destroyMusicalContext")
		for _, s := range d.seqs {
			if s.ctx == ref {
				d.a.violate("a musical context went with a sequence in it")
			}
		}
		d.mu.Lock()
		delete(d.contexts, ref)
		d.mu.Unlock()
		return 0
	})
	srcFields := []int{fPtr, fPtr, fPtr, fI64, fF64, fI32, fI32, fI32, fPtr}
	readSource := func(props uintptr, s *fakeSource) {
		r := readAt(props, srcFields...)
		s.id, s.frames, s.rate, s.channels = r.str(2), int64(r.get(3)), r.f64(4), int(r.i32(5))
	}
	set(dcCreateAudioSource, func(ctl, host, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("createAudioSource")
		s := &fakeSource{host: host}
		readSource(props, s)
		ref := newHandle(s)
		d.made[ref] = true
		d.mu.Lock()
		d.sources[ref] = s
		d.mu.Unlock()
		return ref
	})
	set(dcUpdateAudioSourceProperties, func(ctl, ref, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updateAudioSourceProperties")
		s := d.sources[ref]
		if s.enabled {
			d.a.violate("an audio source's properties changed while its samples were open")
		}
		readSource(props, s)
		return 0
	})
	set(dcEnableAudioSourceSamplesAccess, func(ctl, ref, on uintptr) uintptr {
		d := fakeDocOf(ctl)
		s := d.sources[ref]
		s.enabled = on&0xffffffff != 0
		if !s.enabled {
			d.wg.Wait()
		}
		d.startAnalysis(s)
		return 0
	})
	set(dcDestroyAudioSource, func(ctl, ref uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("destroyAudioSource")
		if d.sources[ref].enabled {
			d.a.violate("an audio source went with its samples open")
		}
		for _, m := range d.mods {
			if m.src == ref {
				d.a.violate("an audio source went with a modification of it")
			}
		}
		d.mu.Lock()
		delete(d.sources, ref)
		d.mu.Unlock()
		return 0
	})
	modFields := []int{fPtr, fPtr, fPtr}
	set(dcCreateAudioModification, func(ctl, src, host, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("createAudioModification")
		if d.sources[src] == nil {
			d.a.violate("a modification of no source")
		}
		m := &fakeMod{host: host, src: src, id: readAt(props, modFields...).str(2), gain: 1}
		ref := newHandle(m)
		d.mu.Lock()
		d.mods[ref] = m
		d.mu.Unlock()
		return ref
	})
	set(dcUpdateAudioModificationProperties, func(ctl, ref, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updateAudioModificationProperties")
		d.mods[ref].id = readAt(props, modFields...).str(2)
		return 0
	})
	set(dcDestroyAudioModification, func(ctl, ref uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("destroyAudioModification")
		for _, r := range d.regions {
			if r.mod == ref {
				d.a.violate("a modification went with a region of it")
			}
		}
		d.mu.Lock()
		delete(d.mods, ref)
		d.mu.Unlock()
		return 0
	})
	regionFields := []int{fPtr, fI32, fF64, fF64, fF64, fF64, fPtr, fPtr, fPtr, fPtr}
	readRegion := func(props uintptr, r *fakeRegion) {
		p := readAt(props, regionFields...)
		r.props = RegionProps{Transforms: Transforms(p.i32(1)), Start: p.f64(2), Duration: p.f64(3), At: p.f64(4),
			Length: p.f64(5), Name: p.str(8)}
		r.seq = p.ptr(7)
	}
	set(dcCreatePlaybackRegion, func(ctl, mod, host, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("createPlaybackRegion")
		r := &fakeRegion{host: host, mod: mod}
		readRegion(props, r)
		if d.seqs[r.seq] == nil {
			d.a.violate("a region on no sequence")
		}
		ref := newHandle(r)
		d.mu.Lock()
		d.regions[ref] = r
		d.mu.Unlock()
		return ref
	})
	set(dcUpdatePlaybackRegionProperties, func(ctl, ref, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updatePlaybackRegionProperties")
		d.mu.Lock()
		readRegion(props, d.regions[ref])
		d.mu.Unlock()
		return 0
	})
	set(dcDestroyPlaybackRegion, func(ctl, ref uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("destroyPlaybackRegion")
		for _, x := range d.exts {
			if slices.Contains(x.regions, ref) || slices.Contains(x.editorRegions, ref) {
				d.a.violate("a region went while a renderer held it")
			}
		}
		d.mu.Lock()
		delete(d.regions, ref)
		d.mu.Unlock()
		return 0
	})
	// Content, through a function of each kind of object's.
	type events func(d *fakeDoc, ref uintptr, k ContentType) *contentEvents
	contentFns := func(available, grade, reader int, ev events) {
		set(available, func(ctl, ref, kind uintptr) uintptr {
			d := fakeDocOf(ctl)
			d.mu.Lock()
			defer d.mu.Unlock()
			return araBool(ev(d, ref, ContentType(int32(uint32(kind)))) != nil)
		})
		set(grade, func(ctl, ref, kind uintptr) uintptr {
			d := fakeDocOf(ctl)
			d.mu.Lock()
			defer d.mu.Unlock()
			if ev(d, ref, ContentType(int32(uint32(kind)))) != nil {
				return uintptr(GradeDetected)
			}
			return uintptr(GradeInitial)
		})
		set(reader, func(ctl, ref, kind, rng uintptr) uintptr {
			d := fakeDocOf(ctl)
			d.mu.Lock()
			e := ev(d, ref, ContentType(int32(uint32(kind))))
			d.mu.Unlock()
			if e == nil {
				d.a.violate("a content reader of content the fake has none of")
				return 0
			}
			d.contentReaders++
			return newHandle(&contentReader{ev: e})
		})
	}
	contentFns(dcIsAudioSourceContentAvailable, dcGetAudioSourceContentGrade, dcCreateAudioSourceContentReader,
		func(d *fakeDoc, ref uintptr, k ContentType) *contentEvents {
			s := d.sources[ref]
			return d.sourceEvents(s, k, func() []Note { return slices.Clone(s.notes) })
		})
	contentFns(dcIsAudioModificationContentAvailable, dcGetAudioModificationContentGrade,
		dcCreateAudioModificationContentReader, func(d *fakeDoc, ref uintptr, k ContentType) *contentEvents {
			m := d.mods[ref]
			return d.sourceEvents(d.sources[m.src], k, func() []Note { return d.modNotes(m) })
		})
	contentFns(dcIsPlaybackRegionContentAvailable, dcGetPlaybackRegionContentGrade, dcCreatePlaybackRegionContentReader,
		func(d *fakeDoc, ref uintptr, k ContentType) *contentEvents {
			if k != ContentNotes {
				return nil
			}
			r := d.regions[ref]
			return d.sourceEvents(d.sources[d.mods[r.mod].src], k, func() []Note { return d.regionNotes(r) })
		})
	set(dcGetContentReaderEventCount, getContentReaderEventCount)
	set(dcGetContentReaderDataForEvent, getContentReaderDataForEvent)
	set(dcDestroyContentReader, func(ctl, reader uintptr) uintptr {
		d := fakeDocOf(ctl)
		if handle[contentReader](reader) == nil {
			d.a.violate("a content reader destroyed that is none")
			return 0
		}
		d.contentReaders--
		dropHandle(reader)
		return 0
	})
	set(dcGetProcessingAlgorithmsCount, func(ctl uintptr) uintptr { return uintptr(len(fakeAlgorithms)) })
	set(dcGetProcessingAlgorithmProperties, func(ctl, i uintptr) uintptr {
		d := fakeDocOf(ctl)
		k := int(int32(uint32(i)))
		if k < 0 || k >= len(d.a.algorithms) {
			d.a.violate("the properties of algorithm %d asked for", k)
			return 0
		}
		return d.a.algorithms[k].addr()
	})
	set(dcGetProcessingAlgorithmForAudioSource, func(ctl, ref uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mu.Lock()
		defer d.mu.Unlock()
		return uintptr(d.sources[ref].algo)
	})
	set(dcRequestProcessingAlgorithmForAudioSource, func(ctl, ref, i uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("requestProcessingAlgorithmForAudioSource")
		k := int(int32(uint32(i)))
		if k < 0 || k >= len(fakeAlgorithms) {
			d.a.violate("algorithm %d requested", k)
			return 0
		}
		d.mu.Lock()
		d.setAlgorithm(d.sources[ref], k)
		d.mu.Unlock()
		return 0
	})
	set(dcIsAudioSourceContentAnalysisIncomplete, func(ctl, ref, kind uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mu.Lock()
		defer d.mu.Unlock()
		return araBool(!d.sources[ref].analysed)
	})
	set(dcRequestAudioSourceContentAnalysis, func(ctl, ref, n, types uintptr) uintptr {
		d := fakeDocOf(ctl)
		if n == 0 || *ptr[int32](types) != int32(ContentNotes) {
			d.a.violate("an analysis asked for content the fake analyzes none of")
		}
		s := d.sources[ref]
		s.requested = true
		d.startAnalysis(s)
		return 0
	})
	seqFields := []int{fPtr, fPtr, fI32, fPtr, fPtr}
	set(dcCreateRegionSequence, func(ctl, host, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("createRegionSequence")
		r := readAt(props, seqFields...)
		q := &fakeSeq{host: host, name: r.str(1), ctx: r.ptr(3)}
		if d.contexts[q.ctx] == nil {
			d.a.violate("a sequence in no musical context")
		}
		ref := newHandle(q)
		d.mu.Lock()
		d.seqs[ref] = q
		d.mu.Unlock()
		return ref
	})
	set(dcUpdateRegionSequenceProperties, func(ctl, ref, props uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("updateRegionSequenceProperties")
		r := readAt(props, seqFields...)
		d.seqs[ref].name, d.seqs[ref].ctx = r.str(1), r.ptr(3)
		return 0
	})
	set(dcDestroyRegionSequence, func(ctl, ref uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("destroyRegionSequence")
		for _, r := range d.regions {
			if r.seq == ref {
				d.a.violate("a sequence went with a region on it")
			}
		}
		d.mu.Lock()
		delete(d.seqs, ref)
		d.mu.Unlock()
		return 0
	})
	set(dcGetPlaybackRegionHeadAndTailTime, func(ctl, ref, head, tail uintptr) uintptr {
		*ptr[float64](head), *ptr[float64](tail) = 0.01, 0.02
		return 0
	})
	set(dcRestoreObjectsFromArchive, func(ctl, reader, filter uintptr) uintptr {
		d := fakeDocOf(ctl)
		d.mustEdit("restoreObjectsFromArchive")
		return araBool(d.restore(reader, filter))
	})
	set(dcStoreObjectsToArchive, func(ctl, writer, filter uintptr) uintptr {
		d := fakeDocOf(ctl)
		if d.editing {
			d.a.violate("storeObjectsToArchive while editing")
		}
		return araBool(d.store(writer, filter))
	})
	return newTable(fns...)
})

// fakeArchive is what the fake's archives hold.
type fakeArchive struct {
	Doc     string
	Sources map[string]float64
	Mods    map[string]float64
	// Algorithms are the sources' algorithms' IDs, and Edits the spans
	// of the modifications' gains, by their objects' IDs.
	Algorithms map[string]string    `json:",omitempty"`
	Edits      map[string]TimeRange `json:",omitempty"`
}

func (d *fakeDoc) store(writer, filter uintptr) bool {
	ar := fakeArchive{Doc: d.data, Sources: map[string]float64{}, Mods: map[string]float64{},
		Algorithms: map[string]string{}, Edits: map[string]TimeRange{}}
	all := filter == 0
	var srcs, mods []uintptr
	if !all {
		f := readAt(filter, fPtr, fI32, fPtr, fPtr, fPtr, fPtr)
		if !f.boolean(1) {
			ar.Doc = ""
		}
		for i := range f.count(2) {
			srcs = append(srcs, *ptr[uintptr](f.ptr(3) + uintptr(i*ptrSize)))
		}
		for i := range f.count(4) {
			mods = append(mods, *ptr[uintptr](f.ptr(5) + uintptr(i*ptrSize)))
		}
	}
	d.mu.Lock()
	for ref, s := range d.sources {
		if !all && !slices.Contains(srcs, ref) {
			continue
		}
		if s.analysed {
			ar.Sources[s.id] = s.peak
		}
		ar.Algorithms[s.id] = fakeAlgorithms[s.algo].ID
	}
	for ref, m := range d.mods {
		if all || slices.Contains(mods, ref) {
			ar.Mods[m.id] = m.gain
			if m.edit != nil {
				ar.Edits[m.id] = *m.edit
			}
		}
	}
	d.mu.Unlock()
	b, _ := json.Marshal(ar)
	// In two writes, the second first.
	half := len(b) / 2
	w := d.host.archive
	ok := w.call(2, writer, uintptr(half), uintptr(len(b)-half), uintptr(unsafe.Pointer(&b[half])))&0xffffffff != 0
	return ok && w.call(2, writer, 0, uintptr(half), uintptr(unsafe.Pointer(&b[0])))&0xffffffff != 0
}

func (d *fakeDoc) restore(reader, filter uintptr) bool {
	ar := d.host.archive
	n := ar.call(0, reader)
	b := make([]byte, n)
	if n > 0 && ar.call(1, reader, 0, n, uintptr(unsafe.Pointer(&b[0])))&0xffffffff == 0 {
		return false
	}
	if id := cstringAt(ar.call(5, reader)); id != fakeArchiveID && id != "org.gunim.fake.old" {
		d.a.violate("an archive of the form %q", id)
	}
	var a fakeArchive
	if err := json.Unmarshal(b, &a); err != nil {
		return false
	}
	// Each object takes its state from the archive, under the name it
	// has there.
	srcNames, modNames := map[string]string{}, map[string]string{}
	doc := true
	if filter != 0 {
		f := readAt(filter, fPtr, fI32, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr)
		doc = f.boolean(1)
		names := func(n, a, c int, into map[string]string) {
			for i := range f.count(n) {
				into[cstringAt(*ptr[uintptr](f.ptr(c) + uintptr(i*ptrSize)))] = cstringAt(*ptr[uintptr](f.ptr(a) + uintptr(i*ptrSize)))
			}
		}
		names(2, 3, 4, srcNames)
		names(5, 6, 7, modNames)
	}
	name := func(names map[string]string, id string) (string, bool) {
		if filter == 0 {
			return id, true
		}
		n, ok := names[id]
		return n, ok
	}
	if doc && a.Doc != "" {
		d.data = a.Doc
	}
	d.mu.Lock()
	// The objects whose states this call restores.
	srcs, mods := map[uintptr]bool{}, map[uintptr]bool{}
	for ref, s := range d.sources {
		if n, ok := name(srcNames, s.id); ok {
			if p, ok := a.Sources[n]; ok {
				// The analysis comes back; the samples it renders from
				// are read again as the edit ends.
				s.peak, s.requested = p, true
				srcs[ref] = true
			}
			if id, ok := a.Algorithms[n]; ok {
				if k := slices.IndexFunc(fakeAlgorithms, func(al Algorithm) bool { return al.ID == id }); k >= 0 {
					d.setAlgorithm(s, k)
				}
				srcs[ref] = true
			}
		}
	}
	// A modification takes the edits archived whatever source it is of,
	// as a plugin keeps what it can of a modification of another source.
	var changed []*fakeMod
	for ref, m := range d.mods {
		if n, ok := name(modNames, m.id); ok {
			if g, ok := a.Mods[n]; ok {
				mods[ref] = true
				var edit *TimeRange
				if r, ok := a.Edits[n]; ok {
					edit = &r
				}
				if g != m.gain || (edit == nil) != (m.edit == nil) || edit != nil && *edit != *m.edit {
					m.gain, m.edit = g, edit
					changed = append(changed, m)
				}
			}
		}
	}
	d.checkRestore(srcs, mods, doc)
	d.mu.Unlock()
	// A restore tells of what it changed, as a plugin may.
	for _, m := range changed {
		d.later(func() { d.host.model.call(2, m.host, 0, 0) })
	}
	return true
}

// checkRestore checks a restore of the states of the sources srcs and
// the modifications mods, and of the document's data where doc, against
// ARA's rules of partial persistence, as ARAInterface.h gives them under
// restoreObjectsFromArchive and ARAStoreObjectsFilter::documentData:
//   - the document's data is restored last in the edit cycle;
//   - a source's state is restored with or before its modifications';
//   - a modification of a source made in the same edit cycle is
//     restored with or after its source's state, as Melodyne asserts
//     ("restoring audio modification onto a freshly created audio
//     source").
//
// d.mu is held.
func (d *fakeDoc) checkRestore(srcs, mods map[uintptr]bool, doc bool) {
	if d.docRestored && (len(srcs) > 0 || len(mods) > 0) {
		d.a.violate("a source's or modification's state restored after the document's data")
	}
	for ref := range srcs {
		for mref, m := range d.mods {
			if m.src == ref && d.modRestored[mref] {
				d.a.violate("a source's state restored after a modification of it")
			}
		}
		d.srcRestored[ref] = true
	}
	for ref := range mods {
		if src := d.mods[ref].src; d.made[src] && !d.srcRestored[src] {
			d.a.violate("a modification restored onto a source made in the same edit, before the source's state")
		}
		d.modRestored[ref] = true
	}
	if doc {
		d.docRestored = true
	}
}

// edit sets the gain of the modification of ID id, as the user does in
// the fake's editor, and tells the host so, of the range given.
func (d *fakeDoc) edit(id string, gain, from, length float64) {
	d.mu.Lock()
	var m *fakeMod
	for _, x := range d.mods {
		if x.id == id {
			m = x
		}
	}
	m.gain, m.edit = gain, &TimeRange{from, length}
	d.mu.Unlock()
	d.later(func() {
		rng := newPlain(fF64, fF64)
		rng.putF64(0, from)
		rng.putF64(1, length)
		d.host.model.call(2, m.host, rng.addr(), uintptr(NotesUnchanged))
	})
}

// The fake's extension, one an instance bound.

type fakeExt struct {
	d             *fakeDoc
	f             *fakeInstance
	roles         Roles
	inst          *cstruct
	regions       []uintptr
	editorRegions []uintptr
	editorSeqs    []uintptr
	selection     []uintptr
	selRange      *TimeRange
	hidden        []uintptr
}

var fakeARAEntryKind = &kind{iids: []uid{iidARAPlugInEntry}, other: fakeInstOther, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this uintptr) uintptr { return instOf(this).ara.factory.addr() }),
		newCallback(func(this, dc uintptr) uintptr { return 0 }),
	}
}}

var fakeARAEntry2Kind = &kind{iids: []uid{iidARAPlugInEntry2}, other: fakeInstOther, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this, dc, known, assigned uintptr) uintptr {
			f := instOf(this)
			d := fakeDocOf(dc)
			if d == nil {
				f.ara.violate("a bind to no document")
				return 0
			}
			f.mu.Lock()
			active := f.isActive
			f.mu.Unlock()
			if active || f.araExt != nil {
				f.ara.violate("a bind after the instance started, or a second bind")
			}
			if Roles(known) != allRoles {
				f.ara.violate("the host knows the roles %d", known)
			}
			x := &fakeExt{d: d, f: f, roles: Roles(assigned)}
			ref := newHandle(x)
			t := fakeExtTables()
			x.inst = newStruct(extensionFields...)
			if x.roles&PlaybackRenderer != 0 {
				x.inst.put(3, uint64(ref))
				x.inst.putPtr(4, t.pr.addr(), nil)
			}
			if x.roles&EditorRenderer != 0 {
				x.inst.put(5, uint64(ref))
				x.inst.putPtr(6, t.er.addr(), nil)
			}
			if x.roles&EditorView != 0 {
				x.inst.put(7, uint64(ref))
				x.inst.putPtr(8, t.ev.addr(), nil)
			}
			f.araExt = x
			d.exts = append(d.exts, x)
			return x.inst.addr()
		}),
	}
}}

type fakeExtTab struct{ pr, er, ev fnTable }

var fakeExtTables = sync.OnceValue(func() fakeExtTab {
	x := func(ref uintptr) *fakeExt { return handle[fakeExt](ref) }
	stopped := func(e *fakeExt, what string) {
		e.f.mu.Lock()
		active := e.f.isActive
		e.f.mu.Unlock()
		if active {
			e.d.a.violate("%s while the renderer was active", what)
		}
	}
	add := func(list *[]uintptr, r uintptr) {
		if !slices.Contains(*list, r) {
			*list = append(*list, r)
		}
	}
	del := func(list *[]uintptr, r uintptr) {
		*list = slices.DeleteFunc(*list, func(q uintptr) bool { return q == r })
	}
	return fakeExtTab{
		pr: newTable(
			newCallback(func(ref, r uintptr) uintptr {
				e := x(ref)
				stopped(e, "addPlaybackRegion")
				add(&e.regions, r)
				return 0
			}),
			newCallback(func(ref, r uintptr) uintptr {
				e := x(ref)
				stopped(e, "removePlaybackRegion")
				del(&e.regions, r)
				return 0
			}),
		),
		er: newTable(
			newCallback(func(ref, r uintptr) uintptr { add(&x(ref).editorRegions, r); return 0 }),
			newCallback(func(ref, r uintptr) uintptr { del(&x(ref).editorRegions, r); return 0 }),
			newCallback(func(ref, s uintptr) uintptr { add(&x(ref).editorSeqs, s); return 0 }),
			newCallback(func(ref, s uintptr) uintptr { del(&x(ref).editorSeqs, s); return 0 }),
		),
		ev: newTable(
			newCallback(func(ref, sel uintptr) uintptr {
				e := x(ref)
				s := readAt(sel, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr)
				e.selection = nil
				for i := range s.count(1) {
					e.selection = append(e.selection, *ptr[uintptr](s.ptr(2) + uintptr(i*ptrSize)))
				}
				e.selRange = nil
				if s.ptr(5) != 0 {
					r := readAt(s.ptr(5), fF64, fF64)
					e.selRange = &TimeRange{r.f64(0), r.f64(1)}
				}
				return 0
			}),
			newCallback(func(ref, n, seqs uintptr) uintptr {
				e := x(ref)
				e.hidden = nil
				for i := range int(n) {
					e.hidden = append(e.hidden, *ptr[uintptr](seqs + uintptr(i*ptrSize)))
				}
				return 0
			}),
		),
	}
})

// render renders the instance's regions into the main output, as a
// playback renderer, where the song plays; else it passes its input on.
func (a *fakeARAPlugin) render(f *fakeInstance, d *processData) {
	x := f.araExt
	if x == nil || x.roles&PlaybackRenderer == 0 {
		return
	}
	ctx := ptr[processContext](d.context)
	n := int(d.frames)
	bb := ptr[busBuffers](d.outputs)
	out := make([][]float32, bb.channels)
	for c := range out {
		out[c] = unsafe.Slice(*ptr[*float32](bb.buffers + uintptr(c)*unsafe.Sizeof(uintptr(0))), n)
		clear(out[c])
	}
	if ctx.state&ctxPlaying == 0 {
		return
	}
	doc := x.d
	if d.mode == modeOffline {
		// Offline, it renders whole: it waits for the samples it reads.
		doc.wg.Wait()
	}
	doc.mu.Lock()
	defer doc.mu.Unlock()
	for _, ref := range x.regions {
		r := doc.regions[ref]
		m := doc.mods[r.mod]
		s := doc.sources[m.src]
		if r == nil || m == nil || s == nil || len(s.samples) == 0 {
			continue
		}
		p := r.props
		frames := len(s.samples[0])
		// sample returns the source's frame at of channel c, or silence.
		sample := func(c int, at int64) float32 {
			if at < 0 || at >= int64(frames) {
				return 0
			}
			return s.samples[c%len(s.samples)][at]
		}
		for i := range n {
			t := float64(ctx.projectTime+int64(i)) / ctx.rate
			if t < p.At || t >= p.At+p.Length {
				continue
			}
			g := m.gain
			if p.Transforms&FadeAtHead != 0 && t-p.At < fakeFade {
				g *= (t - p.At) / fakeFade
			}
			if p.Transforms&FadeAtTail != 0 && p.At+p.Length-t < fakeFade {
				g *= (p.At + p.Length - t) / fakeFade
			}
			if p.Duration == p.Length {
				at := int64(math.Round((p.Start + t - p.At) * s.rate))
				for c := range out {
					out[c][i] += float32(g) * sample(c, at)
				}
				continue
			}
			// Stretched: Duration of the modification over Length.
			pos := (p.Start + (t-p.At)*p.Duration/p.Length) * s.rate
			at := math.Floor(pos)
			fr := float32(pos - at)
			for c := range out {
				out[c][i] += float32(g) * (sample(c, int64(at))*(1-fr) + sample(c, int64(at)+1)*fr)
			}
		}
	}
}

// fakeInstance is an instance of the fake: its VST3 component and
// audio processor, and its ARA entries, each an object of the host's
// kind, all answering for one another.
type fakeInstance struct {
	ara                       *fakeARAPlugin
	comp, proc, entry, entry2 uintptr

	mu       sync.Mutex
	isActive bool
	araExt   *fakeExt
	// activations and deactivations count its setActive calls; view is
	// its view's zoom, which its state holds.
	activations, deactivations int
	view                       float64
}

type fakeInstObject struct {
	object
	f *fakeInstance
}

func instOf(this uintptr) *fakeInstance { return self[fakeInstObject](this).f }

// fakeInstOther answers a query of one of the instance's interfaces.
func fakeInstOther(this uintptr, id uid) uintptr {
	f := instOf(this)
	var p uintptr
	switch id {
	case iidComponent:
		p = f.comp
	case iidAudioProcessor:
		p = f.proc
	case iidARAPlugInEntry:
		p = f.entry
	case iidARAPlugInEntry2:
		p = f.entry2
	}
	if p != 0 {
		selfObject(p).refs.Add(1)
	}
	return p
}

// build makes an instance of the fake for p, on the plugins' thread.
func (a *fakeARAPlugin) build(p *Plugin) error {
	f := &fakeInstance{ara: a}
	mk := func(k *kind) uintptr { return keep(&fakeInstObject{f: f}, k, false) }
	f.comp, f.proc, f.entry, f.entry2 = mk(fakeInstCompKind), mk(fakeInstProcKind), mk(fakeARAEntryKind), mk(fakeARAEntry2Kind)
	a.mu.Lock()
	a.instances = append(a.instances, f)
	a.mu.Unlock()
	return p.build(f.comp, nil)
}

// The instance's component: a stereo input and output, no controller.
var fakeInstCompKind = &kind{iids: []uid{iidComponent}, other: fakeInstOther, make: func() []uintptr {
	ok := func() uintptr { return resultOf(resultOK) }
	return []uintptr{
		newCallback(func(this, ctx uintptr) uintptr { return ok() }),
		newCallback(func(this uintptr) uintptr {
			// Terminated, it lets go of its ARA extension, as a plugin
			// destroyed does.
			if x := instOf(this).araExt; x != nil {
				x.d.mu.Lock()
				x.d.exts = slices.DeleteFunc(x.d.exts, func(e *fakeExt) bool { return e == x })
				x.d.mu.Unlock()
			}
			return ok()
		}),
		newCallback(func(this, cid uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, mode uintptr) uintptr { return ok() }),
		newCallback(func(this, media, dir uintptr) uintptr {
			if i32(media) == mediaAudio {
				return 1
			}
			return 0
		}),
		newCallback(func(this, media, dir, index, info uintptr) uintptr {
			if i32(media) != mediaAudio || i32(index) != 0 {
				return resultOf(resultFalse)
			}
			*ptr[busInfo](info) = busInfo{mediaType: mediaAudio, direction: i32(dir), channels: 2}
			return ok()
		}),
		newCallback(func(this, in, out uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, media, dir, index, state uintptr) uintptr { return ok() }),
		newCallback(func(this, state uintptr) uintptr {
			f := instOf(this)
			f.mu.Lock()
			f.isActive = state&0xff != 0
			if f.isActive {
				f.activations++
			} else {
				f.deactivations++
			}
			f.mu.Unlock()
			return ok()
		}),
		newCallback(func(this, stream uintptr) uintptr {
			f := instOf(this)
			var b [8]byte
			var n int32
			call(stream, 3, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&n)))
			if n != int32(len(b)) {
				return resultOf(resultFalse)
			}
			f.mu.Lock()
			f.view = math.Float64frombits(binary.LittleEndian.Uint64(b[:]))
			f.mu.Unlock()
			return ok()
		}),
		newCallback(func(this, stream uintptr) uintptr {
			f := instOf(this)
			var b [8]byte
			f.mu.Lock()
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(f.view))
			f.mu.Unlock()
			var n int32
			call(stream, 4, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&n)))
			return ok()
		}),
	}
}}

// The instance's audio processor: it renders as a playback renderer,
// or passes its input on.
var fakeInstProcKind = &kind{iids: []uid{iidAudioProcessor}, other: fakeInstOther, make: func() []uintptr {
	ok := func() uintptr { return resultOf(resultOK) }
	return []uintptr{
		newCallback(func(this, ins, nIn, outs, nOut uintptr) uintptr { return ok() }),
		newCallback(func(this, dir, index, arr uintptr) uintptr {
			*ptr[uint64](arr) = stereo
			return ok()
		}),
		newCallback(func(this, size uintptr) uintptr { return ok() }),
		newCallback(func(this uintptr) uintptr { return 0 }),
		newCallback(func(this, setup uintptr) uintptr { return ok() }),
		newCallback(func(this, state uintptr) uintptr { return ok() }),
		newCallback(func(this, data uintptr) uintptr {
			f := instOf(this)
			d := ptr[processData](data)
			n := int(d.frames)
			chans := func(at uintptr) [][]float32 {
				bb := ptr[busBuffers](at)
				out := make([][]float32, bb.channels)
				for c := range out {
					out[c] = unsafe.Slice(*ptr[*float32](bb.buffers + uintptr(c)*unsafe.Sizeof(uintptr(0))), n)
				}
				return out
			}
			if d.numInputs > 0 && d.numOutputs > 0 {
				in, out := chans(d.inputs), chans(d.outputs)
				for c := range out {
					copy(out[c], in[c%len(in)])
				}
			}
			f.ara.render(f, d)
			return ok()
		}),
		newCallback(func(this uintptr) uintptr { return 0 }),
	}
}}
