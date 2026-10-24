package vst3

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// Algorithm is one of the plugin's processing algorithms, as an
// analysis uses: its ID, which stays the same in each version of the
// plugin, and its name.
type Algorithm struct{ ID, Name string }

// algorithmFields are an ARAProcessingAlgorithmProperties' fields: its
// size, ID and name.
var algorithmFields = []int{fPtr, fPtr, fPtr}

// algorithmCount returns how many processing algorithms the plugin offers.
// It runs on the plugins' thread.
func (d *Document) algorithmCount() int {
	if d.closed || !d.dc.has(dcRequestProcessingAlgorithmForAudioSource) {
		return 0
	}
	return max(int(int32(uint32(d.dc.call(dcGetProcessingAlgorithmsCount)))), 0)
}

// Algorithms returns the plugin's processing algorithms, in the order it
// shows them, or nil where it offers none.
func (d *Document) Algorithms() []Algorithm {
	var out []Algorithm
	onUI(func() {
		for i := range d.algorithmCount() {
			r := readAt(d.dc.call(dcGetProcessingAlgorithmProperties, uintptr(i)), algorithmFields...)
			if !r.has(2) {
				out = append(out, Algorithm{})
				continue
			}
			out = append(out, Algorithm{ID: r.str(1), Name: r.str(2)})
		}
	})
	return out
}

// AlgorithmOf returns the index in Algorithms of the algorithm the
// plugin uses for src, or -1 where it is unknown. Until src's first
// analysis ends, it may be a default of the plugin's.
func (d *Document) AlgorithmOf(src *AudioSource) int {
	out := -1
	onUI(func() {
		n := d.algorithmCount()
		if n == 0 || src == nil || src.removed {
			return
		}
		if i := int(int32(uint32(d.dc.call(dcGetProcessingAlgorithmForAudioSource, src.ref)))); i >= 0 && i < n {
			out = i
		}
	})
	return out
}

// RequestAlgorithm asks the plugin to analyse src with algorithm i of
// Algorithms from now on. The plugin may decline.
func (e *Edit) RequestAlgorithm(src *AudioSource, i int) error {
	d := e.d
	var err error
	switch n := d.algorithmCount(); {
	case src == nil || src.removed:
		err = errors.New("vst3: an algorithm requested for no audio source")
	case n == 0:
		err = fmt.Errorf("vst3: %s offers no processing algorithms", d.f.Name)
	case i < 0 || i >= n:
		err = fmt.Errorf("vst3: %s has no processing algorithm %d of %d", d.f.Name, i, n)
	}
	if err != nil {
		e.fail(err)
		return err
	}
	d.dc.call(dcRequestProcessingAlgorithmForAudioSource, src.ref, uintptr(i))
	return nil
}

// Note is a note the plugin found in the audio.
type Note struct {
	// Frequency is in Hz, 0 for a note of no pitch.
	Frequency float64
	// Pitch is the MIDI note number, -1 for none.
	Pitch int
	// Volume is from 0 to 1, scaled as the ear hears it.
	Volume float64
	// Start is when it starts, in seconds of the object's own time: a
	// source's or a modification's time, or the song's for a region.
	// Attack, Duration and Signal are from Start to its musical anchor,
	// to its note off, and to the end of its sound.
	Start, Attack, Duration, Signal float64
}

// noteFields are an ARAContentNote's fields: frequency, pitch, volume,
// start, attack, duration and signal.
var noteFields = []int{fF32, fI32, fF32, fF64, fF64, fF64, fF64}

// Grade is how far a plugin's content can be trusted.
type Grade int

// The grades.
const (
	// GradeInitial is a default, as before an analysis ends.
	GradeInitial Grade = iota
	// GradeDetected is found by an analysis, unseen by the user.
	GradeDetected
	// GradeAdjusted is edited by the user.
	GradeAdjusted
	// GradeApproved is approved by the user as right.
	GradeApproved
)

// ObjectContent is the content the plugin gives of a source, a
// modification or a region, each kind in the order of time.
type ObjectContent struct {
	Notes  []Note
	Tempo  []TempoEntry
	Bars   []BarSignature
	Keys   []KeySignature
	Chords []Chord
	// Grades holds each kind the plugin had, and its grade.
	Grades map[ContentType]Grade
}

// readableKinds are the kinds of content an ObjectContent holds.
var readableKinds = []ContentType{ContentNotes, ContentTempo, ContentBars, ContentKeys, ContentSheetChords}

// contentCalls are the document controller's functions that read an
// object's content: whether a kind is there, its grade, and a reader.
type contentCalls struct{ available, grade, reader int }

// SourceContent returns the content of kinds the plugin has of s, over
// the whole source, in its time; no kinds asks for every kind an
// ObjectContent holds.
func (d *Document) SourceContent(s *AudioSource, kinds ...ContentType) (ObjectContent, error) {
	if s == nil {
		return ObjectContent{}, errors.New("vst3: the content of no audio source")
	}
	return d.objectContent(&s.object3, contentCalls{dcIsAudioSourceContentAvailable, dcGetAudioSourceContentGrade,
		dcCreateAudioSourceContentReader}, kinds)
}

// ModificationContent returns the content of kinds the plugin has of m,
// over the whole modification, in its source's time; no kinds asks for
// every kind an ObjectContent holds.
func (d *Document) ModificationContent(m *AudioModification, kinds ...ContentType) (ObjectContent, error) {
	if m == nil {
		return ObjectContent{}, errors.New("vst3: the content of no audio modification")
	}
	return d.objectContent(&m.object3, contentCalls{dcIsAudioModificationContentAvailable,
		dcGetAudioModificationContentGrade, dcCreateAudioModificationContentReader}, kinds)
}

// RegionContent returns the content of kinds the plugin has of r, over
// the whole region with its head and tail, in the song's time; no kinds
// asks for every kind an ObjectContent holds.
func (d *Document) RegionContent(r *PlaybackRegion, kinds ...ContentType) (ObjectContent, error) {
	if r == nil {
		return ObjectContent{}, errors.New("vst3: the content of no playback region")
	}
	return d.objectContent(&r.object3, contentCalls{dcIsPlaybackRegionContentAvailable, dcGetPlaybackRegionContentGrade,
		dcCreatePlaybackRegionContentReader}, kinds)
}

// objectContent reads the content of kinds of o through calls.
func (d *Document) objectContent(o *object3, calls contentCalls, kinds []ContentType) (ObjectContent, error) {
	if len(kinds) == 0 {
		kinds = readableKinds
	}
	for _, k := range kinds {
		if !slices.Contains(readableKinds, k) {
			return ObjectContent{}, fmt.Errorf("vst3: content of the kind %d reads into no ObjectContent", k)
		}
	}
	c := ObjectContent{Grades: map[ContentType]Grade{}}
	var err error
	onUI(func() {
		if d.closed {
			err = ErrClosed
			return
		}
		if o.removed {
			err = errors.New("vst3: the content of an object taken out")
			return
		}
		for _, k := range kinds {
			if _, done := c.Grades[k]; done || d.dc.call(calls.available, o.ref, uintptr(k))&0xffffffff == 0 {
				continue
			}
			c.Grades[k] = Grade(int32(uint32(d.dc.call(calls.grade, o.ref, uintptr(k)))))
			if err = d.readEvents(d.dc.call(calls.reader, o.ref, uintptr(k), 0), k, &c); err != nil {
				return
			}
		}
	})
	if err != nil {
		return ObjectContent{}, err
	}
	return c, nil
}

// readEvents reads the events of kind k of the plugin's content reader
// rd into c, and destroys it.
func (d *Document) readEvents(rd uintptr, k ContentType, c *ObjectContent) error {
	if rd == 0 {
		return fmt.Errorf("vst3: %s made no reader of content it has of the kind %d", d.f.Name, k)
	}
	defer d.dc.call(dcDestroyContentReader, rd)
	n := int(int32(uint32(d.dc.call(dcGetContentReaderEventCount, rd))))
	for i := range n {
		p := d.dc.call(dcGetContentReaderDataForEvent, rd, uintptr(i))
		if p == 0 {
			return fmt.Errorf("vst3: %s gave no event %d of %d of the kind %d", d.f.Name, i, n, k)
		}
		decodeEvent(k, p, c)
	}
	return nil
}

// decodeEvent appends the event of kind k at p to c.
func decodeEvent(k ContentType, p uintptr, c *ObjectContent) {
	switch k { //nolint:exhaustive // the kinds an ObjectContent holds
	case ContentNotes:
		r := readAt(p, noteFields...)
		n := Note{Frequency: float64(r.f32(0)), Pitch: int(r.i32(1)), Volume: float64(r.f32(2)), Start: r.f64(3),
			Attack: r.f64(4), Duration: r.f64(5), Signal: r.f64(6)}
		if r.i32(1) == math.MinInt32 {
			n.Pitch = -1
		}
		c.Notes = append(c.Notes, n)
	case ContentTempo:
		r := readAt(p, fF64, fF64)
		c.Tempo = append(c.Tempo, TempoEntry{r.f64(0), r.f64(1)})
	case ContentBars:
		r := readAt(p, fI32, fI32, fF64)
		c.Bars = append(c.Bars, BarSignature{int(r.i32(0)), int(r.i32(1)), r.f64(2)})
	case ContentKeys:
		r := readAt(p, fI32, bytesField(12), fPtr, fF64)
		k := KeySignature{Root: int(r.i32(0)), Name: r.str(2), Quarter: r.f64(3)}
		for j, b := range r.bytes(1) {
			k.Scale[j] = b != 0
		}
		c.Keys = append(c.Keys, k)
	case ContentSheetChords:
		r := readAt(p, fI32, fI32, bytesField(12), fPtr, fF64)
		ch := Chord{Root: int(r.i32(0)), Bass: int(r.i32(1)), Name: r.str(3), Quarter: r.f64(4)}
		copy(ch.Intervals[:], r.bytes(2))
		c.Chords = append(c.Chords, ch)
	}
}

// noteEvents lays notes out as a plugin's content reader gives them.
func noteEvents(notes []Note) *contentEvents {
	_, size := layout(noteFields...)
	ev := &contentEvents{b: make([]byte, max(size*len(notes), 1)), size: size, n: len(notes)}
	for i, n := range notes {
		s := newPlain(noteFields...)
		s.putF32(0, float32(n.Frequency))
		pitch := int64(n.Pitch)
		if n.Pitch < 0 {
			pitch = math.MinInt32
		}
		s.put(1, uint64(pitch))
		s.putF32(2, float32(n.Volume))
		s.putF64(3, n.Start)
		s.putF64(4, n.Attack)
		s.putF64(5, n.Duration)
		s.putF64(6, n.Signal)
		copy(ev.b[i*size:], s.b)
	}
	return ev
}
