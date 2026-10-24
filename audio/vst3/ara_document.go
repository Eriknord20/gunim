package vst3

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"unsafe"
)

// ARAHost is the host's half of an ARA document: it reads the audio
// sources' samples for the plugin.
type ARAHost interface {
	// ReadAudio fills dst, a slice for each of src's channels, with
	// src's frames from frame at on, silence before its first frame and
	// after its last, and reports false where reading failed, the frames
	// silent. It runs on the plugin's own threads, several at once, each
	// reading its own sources, and never on the audio thread.
	ReadAudio(src *AudioSource, at int64, dst [][]float32) bool
}

// A Document is an ARA document: the host's song, as an ARA plugin sees
// it, with the plugin's document controller, which holds the plugin's
// analysis and edits. Its methods are safe from any goroutine; each
// runs its work on the plugins' thread, and waits.
type Document struct {
	f    *ARAFactory
	host ARAHost
	// h is the document's handle, the host reference of each of its
	// controllers; inst is its half as the plugin reads it.
	h    uintptr
	inst *cstruct
	dc   iface

	// mu guards what the plugin tells, from its threads, and the
	// objects, by handle.
	mu      sync.Mutex
	updates []Update
	objects map[uintptr]any
	closed  bool
	// plugins are the plugin instances bound to it, open.
	plugins []*Plugin
}

// Color is an object's colour, each part from 0 to 1.
type Color struct{ R, G, B float32 }

// TimeRange is a span of time, in seconds.
type TimeRange struct{ Start, Duration float64 }

// The objects of a document. Each is made by an Edit, and known by a
// handle, the host's reference to it, and the plugin's reference.
type object3 struct {
	d       *Document
	h, ref  uintptr
	removed bool
}

// MusicalContext is a document's musical context: a song's tempo map,
// bars, keys and chords.
type MusicalContext struct {
	object3
	props ContextProps
	// events are the content as the plugin reads it, by kind.
	events map[ContentType]*contentEvents
}

// ContextProps are a musical context's properties and content.
type ContextProps struct {
	Name  string
	Order int
	Color *Color
	// Content is what the plugin reads of it.
	Content Content
}

// Content is a musical context's content, in the order of time: each
// kind left empty is none.
type Content struct {
	// Tempo maps seconds to quarter notes: two entries at least, one at
	// quarter 0, the tempo constant between each two.
	Tempo []TempoEntry
	// Bars are the bar signatures, each from its quarter on, the first
	// from the start.
	Bars []BarSignature
	// Keys are the key signatures, each from its quarter on.
	Keys []KeySignature
	// Chords are the chords as a lead sheet writes them, each from its
	// quarter on; a chord of no intervals marks a stretch of none.
	Chords []Chord
}

// TempoEntry is a point of a tempo map: a time in seconds and its
// quarter note.
type TempoEntry struct{ Time, Quarter float64 }

// BarSignature is a meter, as 6/8, from its quarter note on.
type BarSignature struct {
	Numerator, Denominator int
	Quarter                float64
}

// KeySignature is a key from its quarter note on: its root, on the
// circle of fifths from C (G is 1, F is -1), and the notes of its
// scale, by semitones above the root.
type KeySignature struct {
	Root    int
	Scale   [12]bool
	Name    string
	Quarter float64
}

// Chord is a chord from its quarter note on: its root and bass on the
// circle of fifths from C, and each interval above the root it holds:
// 0 for none, its degree (1, 3, 5, 7, 9, 11, 13) where known, or 0xFF.
type Chord struct {
	Root, Bass int
	Intervals  [12]byte
	Name       string
	Quarter    float64
}

// RegionSequence is a sequence of playback regions: a track.
type RegionSequence struct {
	object3
	props SequenceProps
}

// SequenceProps are a region sequence's properties.
type SequenceProps struct {
	Name    string
	Order   int
	Context *MusicalContext
	Color   *Color
}

// AudioSource is a recording the plugin reads.
type AudioSource struct {
	object3
	props   SourceProps
	enabled bool
}

// SourceProps are an audio source's properties.
type SourceProps struct {
	// Name is shown; ID names it in archives, unique in the document.
	Name, ID string
	// Frames, Rate and Channels are its length, rate and channels.
	Frames   int64
	Rate     float64
	Channels int
	// Merits64 says its samples are worth reading as doubles.
	Merits64 bool
}

// AudioModification is the plugin's edits of an audio source.
type AudioModification struct {
	object3
	src   *AudioSource
	props ModificationProps
}

// ModificationProps are an audio modification's properties: its name,
// and the ID that names it in archives, unique in the document.
type ModificationProps struct {
	Name, ID string
}

// PlaybackRegion is a stretch of a modification, placed on the song's
// timeline.
type PlaybackRegion struct {
	object3
	mod   *AudioModification
	props RegionProps
}

// RegionProps are a playback region's properties. The region plays
// its modification from Start for Duration seconds, at playback time
// At for Length seconds; the two lengths differ only where Transforms
// stretches it.
type RegionProps struct {
	Transforms      Transforms
	Start, Duration float64
	At, Length      float64
	Sequence        *RegionSequence
	Name            string
	Color           *Color
}

// Props returns the context's properties.
func (c *MusicalContext) Props() ContextProps { return c.props }

// Props returns the sequence's properties.
func (s *RegionSequence) Props() SequenceProps { return s.props }

// Props returns the source's properties.
func (s *AudioSource) Props() SourceProps { return s.props }

// Props returns the modification's properties.
func (m *AudioModification) Props() ModificationProps { return m.props }

// Source returns the modification's source.
func (m *AudioModification) Source() *AudioSource { return m.src }

// Props returns the region's properties.
func (r *PlaybackRegion) Props() RegionProps { return r.props }

// Modification returns the region's modification.
func (r *PlaybackRegion) Modification() *AudioModification { return r.mod }

// UpdateKind is what an Update tells.
type UpdateKind int

// What the plugin tells.
const (
	// Analysis is an analysis's progress: Progress from 0 to 1, Done
	// once complete.
	Analysis UpdateKind = iota
	// SourceChanged, ModificationChanged and RegionChanged say the
	// content of Source, Modification or Region changed, over Range,
	// or all of it where Range is nil.
	SourceChanged
	ModificationChanged
	RegionChanged
	// DocumentChanged says data of the document's own changed, as a
	// host stores with it.
	DocumentChanged
	// Play, Stop, Locate, Cycle and CycleOn are the plugin's requests of
	// the host's transport: to play, to stop, to move to Time, to loop
	// from Time for Duration, and to loop or not, as On says.
	Play
	Stop
	Locate
	Cycle
	CycleOn
)

// Update is something the plugin told the host.
type Update struct {
	Kind         UpdateKind
	Source       *AudioSource
	Modification *AudioModification
	Region       *PlaybackRegion
	Range        *TimeRange
	// Flags say what stayed as it was, as ARA's content update flags.
	Flags    ContentFlags
	Progress float64
	Done     bool
	Time     float64
	Duration float64
	On       bool
}

// ContentFlags say which parts of a content change stayed as they were.
type ContentFlags int32

// The parts.
const (
	SignalUnchanged   ContentFlags = 1 << 0
	NotesUnchanged    ContentFlags = 1 << 1
	TimingUnchanged   ContentFlags = 1 << 2
	TuningUnchanged   ContentFlags = 1 << 3
	HarmonyUnchanged  ContentFlags = 1 << 4
	EverythingChanged ContentFlags = 0
)

// ErrClosed is the error of a document closed.
var ErrClosed = errors.New("vst3: the ARA document is closed")

// The document controller's functions, by their place in its table.
const (
	dcDestroy = iota
	dcGetFactory
	dcBeginEditing
	dcEndEditing
	dcNotifyModelUpdates
	dcBeginRestoring
	dcEndRestoring
	dcStoreDocument
	dcUpdateDocumentProperties
	dcCreateMusicalContext
	dcUpdateMusicalContextProperties
	dcUpdateMusicalContextContent
	dcDestroyMusicalContext
	dcCreateAudioSource
	dcUpdateAudioSourceProperties
	dcUpdateAudioSourceContent
	dcEnableAudioSourceSamplesAccess
	dcDeactivateAudioSourceForUndoHistory
	dcDestroyAudioSource
	dcCreateAudioModification
	dcCloneAudioModification
	dcUpdateAudioModificationProperties
	dcDeactivateAudioModificationForUndoHistory
	dcDestroyAudioModification
	dcCreatePlaybackRegion
	dcUpdatePlaybackRegionProperties
	dcDestroyPlaybackRegion
	dcIsAudioSourceContentAvailable
	dcIsAudioSourceContentAnalysisIncomplete
	dcRequestAudioSourceContentAnalysis
	dcGetAudioSourceContentGrade
	dcCreateAudioSourceContentReader
	dcIsAudioModificationContentAvailable
	dcGetAudioModificationContentGrade
	dcCreateAudioModificationContentReader
	dcIsPlaybackRegionContentAvailable
	dcGetPlaybackRegionContentGrade
	dcCreatePlaybackRegionContentReader
	dcGetContentReaderEventCount
	dcGetContentReaderDataForEvent
	dcDestroyContentReader
	dcCreateRegionSequence
	dcUpdateRegionSequenceProperties
	dcDestroyRegionSequence
	dcGetPlaybackRegionHeadAndTailTime
	dcRestoreObjectsFromArchive
	dcStoreObjectsToArchive
	dcGetProcessingAlgorithmsCount
	dcGetProcessingAlgorithmProperties
	dcGetProcessingAlgorithmForAudioSource
	dcRequestProcessingAlgorithmForAudioSource
)

// NewDocument makes a document named name, with its controller, for
// host.
func (f *ARAFactory) NewDocument(name string, host ARAHost) (*Document, error) {
	d := &Document{f: f, host: host, objects: map[uintptr]any{}}
	d.h = newHandle(d)
	t := hostTables()
	inst := newStruct(fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr)
	for i, tab := range []fnTable{t.audio, t.archive, t.content, t.model, t.playback} {
		inst.put(1+2*i, uint64(d.h))
		inst.putPtr(2+2*i, tab.addr(), nil)
	}
	d.inst = inst
	var err error
	onUI(func() {
		f.open()
		props := newStruct(fPtr, fPtr)
		props.putString(1, name)
		r := readAt(f.ptr, araFactoryFields...)
		p := cfn(r.ptr(afCreate), inst.addr(), props.addr())
		runtime.KeepAlive(props)
		if p == 0 {
			err = fmt.Errorf("vst3: %s made no document controller", f.Name)
			return
		}
		ri := readAt(p, fPtr, fPtr, fPtr)
		d.dc = iface{ref: ri.ptr(1), table: ri.ptr(2)}
		if !d.dc.has(dcStoreObjectsToArchive) {
			d.dc.call(dcDestroy)
			err = fmt.Errorf("%w: %s's document controller is too old", ErrNoARA, f.Name)
		}
		if err != nil {
			f.close()
		}
	})
	if err != nil {
		dropHandle(d.h)
		return nil, err
	}
	return d, nil
}

// Factory returns the factory that made the document.
func (d *Document) Factory() *ARAFactory { return d.f }

// Edit makes the changes fn makes as one edit of the document, between
// ARA's beginEditing and endEditing, on the plugins' thread. fn calls
// e's methods alone: a Document's own would wait for the thread it
// runs on. It returns the first error of e's.
func (d *Document) Edit(fn func(e *Edit)) error {
	e := &Edit{d: d}
	onUI(func() {
		if d.closed {
			e.err = ErrClosed
			return
		}
		d.dc.call(dcBeginEditing)
		fn(e)
		d.dc.call(dcEndEditing)
		e.resume()
	})
	return e.err
}

// An Edit is the work of Document.Edit, valid while its function runs.
type Edit struct {
	d   *Document
	err error
	// paused are the renderers the edit stopped to take regions out of,
	// started again once it ends.
	paused []*Plugin
}

// pause stops p, a renderer, until the edit ends, once.
func (e *Edit) pause(p *Plugin) {
	if p.active && !p.closed {
		p.deactivate()
		e.paused = append(e.paused, p)
	}
}

// resume starts the renderers the edit stopped again.
func (e *Edit) resume() {
	for _, p := range e.paused {
		if !p.closed {
			_ = p.activate()
		}
	}
	e.paused = nil
}

func (e *Edit) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// track keeps o as one of the document's objects.
func (d *Document) track(h uintptr, o any) {
	d.mu.Lock()
	d.objects[h] = o
	d.mu.Unlock()
}

func (d *Document) untrack(h uintptr) {
	d.mu.Lock()
	delete(d.objects, h)
	d.mu.Unlock()
	dropHandle(h)
}

// colorOf returns c as ARA's colour, or none.
func colorOf(c *Color, s *cstruct, i int) {
	if c == nil {
		return
	}
	col := newPlain(fF32, fF32, fF32)
	col.putF32(0, c.R)
	col.putF32(1, c.G)
	col.putF32(2, c.B)
	s.putPtr(i, col.addr(), col)
}

// SetName names the document.
func (e *Edit) SetName(name string) {
	props := newStruct(fPtr, fPtr)
	props.putString(1, name)
	e.d.dc.call(dcUpdateDocumentProperties, props.addr())
	runtime.KeepAlive(props)
}

func contextProps(p ContextProps) *cstruct {
	s := newStruct(fPtr, fPtr, fI32, fPtr)
	s.putString(1, p.Name)
	s.put(2, uint64(int64(p.Order)))
	colorOf(p.Color, s, 3)
	return s
}

// AddContext adds a musical context.
func (e *Edit) AddContext(p ContextProps) *MusicalContext {
	d := e.d
	c := &MusicalContext{props: p}
	c.d = d
	c.events = contentOf(p.Content)
	c.h = newHandle(c)
	props := contextProps(p)
	c.ref = d.dc.call(dcCreateMusicalContext, c.h, props.addr())
	runtime.KeepAlive(props)
	if c.ref == 0 {
		dropHandle(c.h)
		e.fail(errors.New("vst3: the plugin made no musical context"))
		return nil
	}
	d.track(c.h, c)
	return c
}

// UpdateContext sets a musical context's properties and content.
func (e *Edit) UpdateContext(c *MusicalContext, p ContextProps) {
	if c == nil || c.removed {
		return
	}
	old := c.props
	c.props = p
	props := contextProps(p)
	e.d.dc.call(dcUpdateMusicalContextProperties, c.ref, props.addr())
	runtime.KeepAlive(props)
	if !sameContent(old.Content, p.Content) {
		c.events = contentOf(p.Content)
		e.d.dc.call(dcUpdateMusicalContextContent, c.ref, 0, uintptr(EverythingChanged))
	}
}

// RemoveContext takes a musical context out: no sequence may name it
// then.
func (e *Edit) RemoveContext(c *MusicalContext) {
	if c == nil || c.removed {
		return
	}
	e.d.dc.call(dcDestroyMusicalContext, c.ref)
	c.removed = true
	e.d.untrack(c.h)
}

func sequenceProps(p SequenceProps) *cstruct {
	s := newStruct(fPtr, fPtr, fI32, fPtr, fPtr)
	s.putString(1, p.Name)
	s.put(2, uint64(int64(p.Order)))
	if p.Context != nil {
		s.put(3, uint64(p.Context.ref))
	}
	colorOf(p.Color, s, 4)
	return s
}

// AddSequence adds a region sequence, in p's musical context.
func (e *Edit) AddSequence(p SequenceProps) *RegionSequence {
	d := e.d
	if p.Context == nil {
		e.fail(errors.New("vst3: a region sequence needs a musical context"))
		return nil
	}
	s := &RegionSequence{props: p}
	s.d = d
	s.h = newHandle(s)
	props := sequenceProps(p)
	s.ref = d.dc.call(dcCreateRegionSequence, s.h, props.addr())
	runtime.KeepAlive(props)
	if s.ref == 0 {
		dropHandle(s.h)
		e.fail(errors.New("vst3: the plugin made no region sequence"))
		return nil
	}
	d.track(s.h, s)
	return s
}

// UpdateSequence sets a region sequence's properties.
func (e *Edit) UpdateSequence(s *RegionSequence, p SequenceProps) {
	if s == nil || s.removed || p.Context == nil {
		return
	}
	s.props = p
	props := sequenceProps(p)
	e.d.dc.call(dcUpdateRegionSequenceProperties, s.ref, props.addr())
	runtime.KeepAlive(props)
}

// RemoveSequence takes a region sequence out: no region may be on it
// then.
func (e *Edit) RemoveSequence(s *RegionSequence) {
	if s == nil || s.removed {
		return
	}
	e.d.dc.call(dcDestroyRegionSequence, s.ref)
	s.removed = true
	e.d.untrack(s.h)
}

func sourceProps(p SourceProps) *cstruct {
	s := newStruct(fPtr, fPtr, fPtr, fI64, fF64, fI32, fI32, fI32, fPtr)
	s.putString(1, p.Name)
	s.putString(2, p.ID)
	s.put(3, uint64(p.Frames))
	s.putF64(4, p.Rate)
	s.put(5, uint64(int64(p.Channels)))
	s.put(6, uint64(araBool(p.Merits64)))
	return s
}

// AddSource adds an audio source, its samples out of reach until
// EnableAudio.
func (e *Edit) AddSource(p SourceProps) *AudioSource {
	d := e.d
	if p.ID == "" || p.Channels < 1 || p.Rate <= 0 {
		e.fail(fmt.Errorf("vst3: an audio source needs an ID, channels and a rate: %+v", p))
		return nil
	}
	s := &AudioSource{props: p}
	s.d = d
	s.h = newHandle(s)
	props := sourceProps(p)
	s.ref = d.dc.call(dcCreateAudioSource, s.h, props.addr())
	runtime.KeepAlive(props)
	if s.ref == 0 {
		dropHandle(s.h)
		e.fail(errors.New("vst3: the plugin made no audio source"))
		return nil
	}
	d.track(s.h, s)
	return s
}

// UpdateSource sets an audio source's properties. Its samples go out
// of reach while it changes, and come back after.
func (e *Edit) UpdateSource(s *AudioSource, p SourceProps) {
	if s == nil || s.removed {
		return
	}
	on := s.enabled
	if on {
		e.EnableAudio(s, false)
	}
	s.props = p
	props := sourceProps(p)
	e.d.dc.call(dcUpdateAudioSourceProperties, s.ref, props.addr())
	runtime.KeepAlive(props)
	if on {
		e.EnableAudio(s, true)
	}
}

// EnableAudio lets the plugin read the source's samples, or stops it.
func (e *Edit) EnableAudio(s *AudioSource, on bool) {
	if s == nil || s.removed || s.enabled == on {
		return
	}
	s.enabled = on
	e.d.dc.call(dcEnableAudioSourceSamplesAccess, s.ref, araBool(on))
}

// RemoveSource takes an audio source out: no modification of it may
// remain.
func (e *Edit) RemoveSource(s *AudioSource) {
	if s == nil || s.removed {
		return
	}
	e.EnableAudio(s, false)
	e.d.dc.call(dcDestroyAudioSource, s.ref)
	s.removed = true
	e.d.untrack(s.h)
}

func modificationProps(p ModificationProps) *cstruct {
	s := newStruct(fPtr, fPtr, fPtr)
	s.putString(1, p.Name)
	s.putString(2, p.ID)
	return s
}

// AddModification adds an audio modification of src.
func (e *Edit) AddModification(src *AudioSource, p ModificationProps) *AudioModification {
	d := e.d
	if src == nil || src.removed || p.ID == "" {
		e.fail(errors.New("vst3: an audio modification needs a source and an ID"))
		return nil
	}
	m := &AudioModification{src: src, props: p}
	m.d = d
	m.h = newHandle(m)
	props := modificationProps(p)
	m.ref = d.dc.call(dcCreateAudioModification, src.ref, m.h, props.addr())
	runtime.KeepAlive(props)
	if m.ref == 0 {
		dropHandle(m.h)
		e.fail(errors.New("vst3: the plugin made no audio modification"))
		return nil
	}
	d.track(m.h, m)
	return m
}

// UpdateModification sets an audio modification's properties.
func (e *Edit) UpdateModification(m *AudioModification, p ModificationProps) {
	if m == nil || m.removed {
		return
	}
	m.props = p
	props := modificationProps(p)
	e.d.dc.call(dcUpdateAudioModificationProperties, m.ref, props.addr())
	runtime.KeepAlive(props)
}

// RemoveModification takes an audio modification out: no region of it
// may remain.
func (e *Edit) RemoveModification(m *AudioModification) {
	if m == nil || m.removed {
		return
	}
	e.d.dc.call(dcDestroyAudioModification, m.ref)
	m.removed = true
	e.d.untrack(m.h)
}

func regionProps(p RegionProps) *cstruct {
	s := newStruct(fPtr, fI32, fF64, fF64, fF64, fF64, fPtr, fPtr, fPtr, fPtr)
	s.put(1, uint64(p.Transforms))
	s.putF64(2, p.Start)
	s.putF64(3, p.Duration)
	s.putF64(4, p.At)
	s.putF64(5, p.Length)
	if p.Sequence != nil {
		// The musical context is ARA 1's, the sequence's since.
		if c := p.Sequence.props.Context; c != nil {
			s.put(6, uint64(c.ref))
		}
		s.put(7, uint64(p.Sequence.ref))
	}
	s.putString(8, p.Name)
	colorOf(p.Color, s, 9)
	return s
}

// AddRegion adds a playback region of m, on p's sequence.
func (e *Edit) AddRegion(m *AudioModification, p RegionProps) *PlaybackRegion {
	d := e.d
	if m == nil || m.removed || p.Sequence == nil {
		e.fail(errors.New("vst3: a playback region needs a modification and a sequence"))
		return nil
	}
	r := &PlaybackRegion{mod: m, props: p}
	r.d = d
	r.h = newHandle(r)
	props := regionProps(p)
	r.ref = d.dc.call(dcCreatePlaybackRegion, m.ref, r.h, props.addr())
	runtime.KeepAlive(props)
	if r.ref == 0 {
		dropHandle(r.h)
		e.fail(errors.New("vst3: the plugin made no playback region"))
		return nil
	}
	d.track(r.h, r)
	return r
}

// UpdateRegion sets a playback region's properties.
func (e *Edit) UpdateRegion(r *PlaybackRegion, p RegionProps) {
	if r == nil || r.removed || p.Sequence == nil {
		return
	}
	r.props = p
	props := regionProps(p)
	e.d.dc.call(dcUpdatePlaybackRegionProperties, r.ref, props.addr())
	runtime.KeepAlive(props)
}

// RemoveRegion takes a playback region out. The renderers playing it
// let it go first, stopped until the edit ends.
func (e *Edit) RemoveRegion(r *PlaybackRegion) {
	if r == nil || r.removed {
		return
	}
	for _, p := range e.d.livePlugins() {
		if x := p.ara; x != nil {
			x.forget(r, e)
		}
	}
	e.d.dc.call(dcDestroyPlaybackRegion, r.ref)
	r.removed = true
	e.d.untrack(r.h)
}

// livePlugins returns the plugins bound to the document, open.
func (d *Document) livePlugins() []*Plugin {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.plugins)
}

// Rename maps an object's ID in an archive to the one it has now.
type Rename struct{ Archived, Current string }

// RestoreFilter names what a restore takes from an archive: the
// document's own data, and the sources' and modifications' states, by
// their IDs in the archive and now.
type RestoreFilter struct {
	Document      bool
	Sources       []Rename
	Modifications []Rename
}

// Restore restores the objects an archive holds, written in the form
// format, into the document's objects of the same IDs: those f names,
// or, where f is nil, all of them.
func (e *Edit) Restore(archive []byte, format string, f *RestoreFilter) error {
	d := e.d
	if !d.f.Reads(format) {
		err := fmt.Errorf("vst3: %s reads no archive of the form %q", d.f.Name, format)
		e.fail(err)
		return err
	}
	rd := &archiveReader{b: archive, id: cstr(format)}
	h := newHandle(rd)
	defer dropHandle(h)
	var filter uintptr
	var fs *cstruct
	if f != nil {
		fs = newStruct(fPtr, fI32, fPtr, fPtr, fPtr, fPtr, fPtr, fPtr)
		fs.put(1, uint64(araBool(f.Document)))
		ids := func(n, a, c int, rs []Rename) {
			fs.put(n, uint64(len(rs)))
			if len(rs) == 0 {
				return
			}
			arch, cur := make([]uintptr, len(rs)), make([]uintptr, len(rs))
			for i, r := range rs {
				ab, cb := cstr(r.Archived), cstr(r.Current)
				fs.pins = append(fs.pins, ab, cb)
				arch[i], cur[i] = uintptr(unsafe.Pointer(&ab[0])), uintptr(unsafe.Pointer(&cb[0]))
			}
			fs.putPtr(a, uintptr(unsafe.Pointer(&arch[0])), arch)
			fs.putPtr(c, uintptr(unsafe.Pointer(&cur[0])), cur)
		}
		ids(2, 3, 4, f.Sources)
		ids(5, 6, 7, f.Modifications)
		filter = fs.addr()
	}
	ok := d.dc.call(dcRestoreObjectsFromArchive, h, filter)&0xffffffff != 0
	runtime.KeepAlive(fs)
	runtime.KeepAlive(rd)
	if !ok {
		err := fmt.Errorf("vst3: %s could not restore its archive", d.f.Name)
		e.fail(err)
		return err
	}
	return nil
}

// StoreFilter names what an archive holds: the document's own data,
// and the states of the sources and modifications listed.
type StoreFilter struct {
	Document      bool
	Sources       []*AudioSource
	Modifications []*AudioModification
}

// Store writes an archive of the objects f names, or, where f is nil,
// of the whole document, in the form the factory's ArchiveID names.
func (d *Document) Store(f *StoreFilter) ([]byte, error) {
	w := &archiveWriter{}
	h := newHandle(w)
	defer dropHandle(h)
	var ok bool
	var err error
	onUI(func() {
		if d.closed {
			err = ErrClosed
			return
		}
		var filter uintptr
		var fs *cstruct
		if f != nil {
			fs = newStruct(fPtr, fI32, fPtr, fPtr, fPtr, fPtr)
			fs.put(1, uint64(araBool(f.Document)))
			if n := len(f.Sources); n > 0 {
				refs := make([]uintptr, n)
				for i, s := range f.Sources {
					refs[i] = s.ref
				}
				fs.put(2, uint64(n))
				fs.putPtr(3, uintptr(unsafe.Pointer(&refs[0])), refs)
			}
			if n := len(f.Modifications); n > 0 {
				refs := make([]uintptr, n)
				for i, m := range f.Modifications {
					refs[i] = m.ref
				}
				fs.put(4, uint64(n))
				fs.putPtr(5, uintptr(unsafe.Pointer(&refs[0])), refs)
			}
			filter = fs.addr()
		}
		ok = d.dc.call(dcStoreObjectsToArchive, h, filter)&0xffffffff != 0
		runtime.KeepAlive(fs)
	})
	if err != nil {
		return nil, err
	}
	if !ok || w.failed {
		return nil, fmt.Errorf("vst3: %s could not store its archive", d.f.Name)
	}
	return w.b, nil
}

// Poll has the plugin tell what changed since it last told, and
// returns that, and its requests of the transport made since. A host
// polls every few tens of milliseconds.
func (d *Document) Poll() []Update {
	onUI(func() {
		if !d.closed {
			d.dc.call(dcNotifyModelUpdates)
		}
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	u := d.updates
	d.updates = nil
	return u
}

// tell queues an update.
func (d *Document) tell(u Update) {
	d.mu.Lock()
	d.updates = append(d.updates, u)
	d.mu.Unlock()
}

// Analysing reports whether the plugin's analysis of src is under way
// or still to come, for any kind of content the factory analyzes.
func (d *Document) Analysing(src *AudioSource) bool {
	var out bool
	onUI(func() {
		if d.closed || src == nil || src.removed {
			return
		}
		for _, t := range d.f.Analyzes {
			if d.dc.call(dcIsAudioSourceContentAnalysisIncomplete, src.ref, uintptr(t))&0xffffffff != 0 {
				out = true
				return
			}
		}
	})
	return out
}

// Analyse asks the plugin to analyze src for each kind of content it
// analyzes.
func (d *Document) Analyse(src *AudioSource) {
	onUI(func() {
		if d.closed || src == nil || src.removed || len(d.f.Analyzes) == 0 {
			return
		}
		types := make([]int32, len(d.f.Analyzes))
		for i, t := range d.f.Analyzes {
			types[i] = int32(t)
		}
		// Pinned, so on the heap: on the stack, a callback of the
		// plugin's could move it before the plugin reads it.
		var pin runtime.Pinner
		pin.Pin(&types[0])
		defer pin.Unpin()
		d.dc.call(dcRequestAudioSourceContentAnalysis, src.ref, uintptr(len(types)), uintptr(unsafe.Pointer(&types[0])))
	})
}

// HeadAndTail returns how long before its start and after its end the
// region sounds, in seconds.
func (d *Document) HeadAndTail(r *PlaybackRegion) (head, tail float64) {
	onUI(func() {
		if d.closed || r == nil || r.removed {
			return
		}
		d.dc.call(dcGetPlaybackRegionHeadAndTailTime, r.ref, uintptr(unsafe.Pointer(&head)), uintptr(unsafe.Pointer(&tail)))
	})
	return head, tail
}

// Close takes every object out of the document, and lets its
// controller go. The plugins bound to it close first.
func (d *Document) Close() {
	for _, p := range d.livePlugins() {
		p.Close()
	}
	onUI(func() {
		if d.closed {
			return
		}
		d.mu.Lock()
		objs := make([]any, 0, len(d.objects))
		for _, o := range d.objects {
			objs = append(objs, o)
		}
		d.mu.Unlock()
		e := &Edit{d: d}
		for _, o := range objs {
			if s, ok := o.(*AudioSource); ok {
				e.EnableAudio(s, false)
			}
		}
		d.dc.call(dcBeginEditing)
		// Regions, then modifications, sources, sequences and contexts,
		// as each names the one after.
		for pass := range 5 {
			for _, o := range objs {
				switch o := o.(type) {
				case *PlaybackRegion:
					if pass == 0 {
						e.RemoveRegion(o)
					}
				case *AudioModification:
					if pass == 1 {
						e.RemoveModification(o)
					}
				case *AudioSource:
					if pass == 2 {
						e.RemoveSource(o)
					}
				case *RegionSequence:
					if pass == 3 {
						e.RemoveSequence(o)
					}
				case *MusicalContext:
					if pass == 4 {
						e.RemoveContext(o)
					}
				}
			}
		}
		d.dc.call(dcEndEditing)
		e.resume()
		d.dc.call(dcDestroy)
		d.closed = true
		d.f.close()
	})
	dropHandle(d.h)
}
