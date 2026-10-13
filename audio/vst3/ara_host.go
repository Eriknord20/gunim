package vst3

import (
	"log"
	"math"
	"slices"
	"sync"
	"unsafe"
)

// The host's interfaces, as an ARA plugin calls them: tables of
// functions, made once, shared by every document, each function
// finding its document by the host reference it is called with.

type araTables struct {
	audio, archive, content, model, playback fnTable
}

var hostTables = sync.OnceValue(func() *araTables {
	t := &araTables{}
	t.audio = newTable(
		newCallback(createAudioReader),
		newCallback(readAudioSamples),
		newCallback(destroyAudioReader),
	)
	t.archive = newTable(
		newCallback(getArchiveSize),
		newCallback(readBytesFromArchive),
		newCallback(writeBytesToArchive),
		newCallback(func(ctl uintptr) uintptr { return 0 }),
		newCallback(func(ctl uintptr) uintptr { return 0 }),
		newCallback(getDocumentArchiveID),
	)
	t.content = newTable(
		newCallback(isContextContentAvailable),
		newCallback(getContextContentGrade),
		newCallback(createContextContentReader),
		newCallback(func(ctl, src, kind uintptr) uintptr { return 0 }),
		newCallback(func(ctl, src, kind uintptr) uintptr { return 0 }),
		newCallback(func(ctl, src, kind, rng uintptr) uintptr { return 0 }),
		newCallback(getContentReaderEventCount),
		newCallback(getContentReaderDataForEvent),
		newCallback(destroyContentReader),
	)
	t.model = newTable(
		newProgressCallback(notifyAnalysisProgress),
		newCallback(func(ctl, src, rng, flags uintptr) uintptr {
			notifyContent(ctl, src, rng, flags, SourceChanged)
			return 0
		}),
		newCallback(func(ctl, mod, rng, flags uintptr) uintptr {
			notifyContent(ctl, mod, rng, flags, ModificationChanged)
			return 0
		}),
		newCallback(func(ctl, region, rng, flags uintptr) uintptr {
			notifyContent(ctl, region, rng, flags, RegionChanged)
			return 0
		}),
		newCallback(func(ctl uintptr) uintptr {
			if d := handle[Document](ctl); d != nil {
				d.tell(Update{Kind: DocumentChanged})
			}
			return 0
		}),
	)
	t.playback = newTable(
		newCallback(func(ctl uintptr) uintptr {
			if d := handle[Document](ctl); d != nil {
				d.tell(Update{Kind: Play})
			}
			return 0
		}),
		newCallback(func(ctl uintptr) uintptr {
			if d := handle[Document](ctl); d != nil {
				d.tell(Update{Kind: Stop})
			}
			return 0
		}),
		newPositionCallback(func(ctl uintptr, at float64) {
			if d := handle[Document](ctl); d != nil {
				d.tell(Update{Kind: Locate, Time: at})
			}
		}),
		newCycleCallback(func(ctl uintptr, at, length float64) {
			if d := handle[Document](ctl); d != nil {
				d.tell(Update{Kind: Cycle, Time: at, Duration: length})
			}
		}),
		newCallback(func(ctl, on uintptr) uintptr {
			if d := handle[Document](ctl); d != nil {
				d.tell(Update{Kind: CycleOn, On: on&0xffffffff != 0})
			}
			return 0
		}),
	)
	return t
})

// The plugin's asserts, as its debug builds make them: logged.
var araAssertFn = sync.OnceValue(func() uintptr {
	return newCallback(func(category, arg, diagnosis uintptr) uintptr {
		onARAAssert(int32(uint32(category)), cstringAt(diagnosis))
		return 0
	})
})

// onARAAssert hears an assert of a plugin's; a test counts them.
var onARAAssert = func(category int32, diagnosis string) {
	log.Printf("vst3: an ARA plugin asserts (%d): %s", category, diagnosis)
}

// Reading audio.

// audioReader is an audio reader the plugin made: one thread reads
// through it at a time.
type audioReader struct {
	d     *Document
	src   *AudioSource
	use64 bool
	// buf is room for frames read as floats, to hand on as doubles; dst
	// the channels handed to the host.
	buf [][]float32
	dst [][]float32
}

func createAudioReader(ctl, src, use64 uintptr) uintptr {
	d, s := handle[Document](ctl), handle[AudioSource](src)
	if d == nil || s == nil {
		return 0
	}
	n := s.props.Channels
	return newHandle(&audioReader{d: d, src: s, use64: use64&0xffffffff != 0, buf: make([][]float32, n),
		dst: make([][]float32, n)})
}

func readAudioSamples(ctl, reader, at, count, buffers uintptr) uintptr {
	r := handle[audioReader](reader)
	n := int(int64(count))
	if r == nil || n < 0 {
		return 0
	}
	bufs := unsafe.Slice(ptr[uintptr](buffers), len(r.dst))
	for c := range r.dst {
		if r.use64 {
			if cap(r.buf[c]) < n {
				r.buf[c] = make([]float32, n)
			}
			r.dst[c] = r.buf[c][:n]
		} else {
			r.dst[c] = unsafe.Slice(ptr[float32](bufs[c]), n)
		}
	}
	ok := r.d.host.ReadAudio(r.src, int64(at), r.dst)
	if r.use64 {
		for c := range r.dst {
			out := unsafe.Slice(ptr[float64](bufs[c]), n)
			for i, v := range r.dst[c] {
				out[i] = float64(v)
			}
		}
	}
	return araBool(ok)
}

func destroyAudioReader(ctl, reader uintptr) uintptr {
	dropHandle(reader)
	return 0
}

// Archives.

// archiveReader is an archive being restored, and the form it is in.
type archiveReader struct {
	b  []byte
	id []byte
}

// archiveWriter is an archive being stored.
type archiveWriter struct {
	b      []byte
	failed bool
}

// maxArchive is the largest archive kept: far past any plugin's.
const maxArchive = 1 << 32

func getArchiveSize(ctl, reader uintptr) uintptr {
	if r := handle[archiveReader](reader); r != nil {
		return uintptr(len(r.b))
	}
	return 0
}

func readBytesFromArchive(ctl, reader, pos, n, buf uintptr) uintptr {
	r := handle[archiveReader](reader)
	if r == nil || pos > uintptr(len(r.b)) || n > uintptr(len(r.b))-pos {
		return 0
	}
	if n > 0 {
		copy(unsafe.Slice(ptr[byte](buf), n), r.b[pos:pos+n])
	}
	return 1
}

func writeBytesToArchive(ctl, writer, pos, n, buf uintptr) uintptr {
	w := handle[archiveWriter](writer)
	if w == nil || uint64(pos)+uint64(n) > maxArchive {
		if w != nil {
			w.failed = true
		}
		return 0
	}
	if end := int(pos + n); end > len(w.b) {
		w.b = slices.Grow(w.b, end-len(w.b))[:end]
	}
	if n > 0 {
		copy(w.b[pos:pos+n], unsafe.Slice(ptr[byte](buf), n))
	}
	return 1
}

func getDocumentArchiveID(ctl, reader uintptr) uintptr {
	if r := handle[archiveReader](reader); r != nil {
		return uintptr(unsafe.Pointer(&r.id[0]))
	}
	return 0
}

// The musical context's content.

// contentEvents are content events, laid out as the plugin reads them,
// one after another, with what they point at.
type contentEvents struct {
	b    []byte
	size int
	n    int
	pins []any
}

// The grade of the host's content: approved, as the user wrote it.
const gradeApproved = 3

// contentOf lays c out as the plugin reads it, by kind.
func contentOf(c Content) map[ContentType]*contentEvents {
	out := map[ContentType]*contentEvents{}
	add := func(t ContentType, n int, fields []int, fill func(i int, s *cstruct)) {
		if n == 0 {
			return
		}
		_, size := layout(fields...)
		ev := &contentEvents{b: make([]byte, size*n), size: size, n: n}
		for i := range n {
			s := newPlain(fields...)
			fill(i, s)
			copy(ev.b[i*size:], s.b)
			ev.pins = append(ev.pins, s.pins...)
		}
		out[t] = ev
	}
	if len(c.Tempo) >= 2 {
		add(ContentTempo, len(c.Tempo), []int{fF64, fF64}, func(i int, s *cstruct) {
			s.putF64(0, c.Tempo[i].Time)
			s.putF64(1, c.Tempo[i].Quarter)
		})
	}
	add(ContentBars, len(c.Bars), []int{fI32, fI32, fF64}, func(i int, s *cstruct) {
		b := c.Bars[i]
		s.put(0, uint64(int64(b.Numerator)))
		s.put(1, uint64(int64(b.Denominator)))
		s.putF64(2, b.Quarter)
	})
	add(ContentKeys, len(c.Keys), []int{fI32, bytesField(12), fPtr, fF64}, func(i int, s *cstruct) {
		k := c.Keys[i]
		s.put(0, uint64(int64(k.Root)))
		var iv [12]byte
		for j, on := range k.Scale {
			if on {
				iv[j] = 0xFF
			}
		}
		s.putBytes(1, iv[:])
		s.putString(2, k.Name)
		s.putF64(3, k.Quarter)
	})
	add(ContentSheetChords, len(c.Chords), []int{fI32, fI32, bytesField(12), fPtr, fF64}, func(i int, s *cstruct) {
		ch := c.Chords[i]
		s.put(0, uint64(int64(ch.Root)))
		s.put(1, uint64(int64(ch.Bass)))
		s.putBytes(2, ch.Intervals[:])
		s.putString(3, ch.Name)
		s.putF64(4, ch.Quarter)
	})
	return out
}

// sameContent reports whether a and b are the same content.
func sameContent(a, b Content) bool {
	return slices.Equal(a.Tempo, b.Tempo) && slices.Equal(a.Bars, b.Bars) && slices.Equal(a.Keys, b.Keys) &&
		slices.Equal(a.Chords, b.Chords)
}

// contextOf returns the musical context of handle h of document ctl.
func contextOf(ctl, h uintptr) *MusicalContext {
	if handle[Document](ctl) == nil {
		return nil
	}
	return handle[MusicalContext](h)
}

func isContextContentAvailable(ctl, mc, kind uintptr) uintptr {
	c := contextOf(ctl, mc)
	return araBool(c != nil && c.events[ContentType(int32(uint32(kind)))] != nil)
}

func getContextContentGrade(ctl, mc, kind uintptr) uintptr {
	if isContextContentAvailable(ctl, mc, kind) != 0 {
		return gradeApproved
	}
	return 0
}

// contentReader is a content reader the plugin made: the events it
// reads.
type contentReader struct{ ev *contentEvents }

func createContextContentReader(ctl, mc, kind, rng uintptr) uintptr {
	c := contextOf(ctl, mc)
	if c == nil {
		return 0
	}
	ev := c.events[ContentType(int32(uint32(kind)))]
	if ev == nil {
		return 0
	}
	// The whole content, whatever the range: a reader may give more
	// than it was asked for.
	return newHandle(&contentReader{ev: ev})
}

func getContentReaderEventCount(ctl, reader uintptr) uintptr {
	if r := handle[contentReader](reader); r != nil {
		return uintptr(r.ev.n)
	}
	return 0
}

func getContentReaderDataForEvent(ctl, reader, index uintptr) uintptr {
	r := handle[contentReader](reader)
	i := int(int32(uint32(index)))
	if r == nil || i < 0 || i >= r.ev.n {
		return 0
	}
	return uintptr(unsafe.Pointer(&r.ev.b[i*r.ev.size]))
}

func destroyContentReader(ctl, reader uintptr) uintptr {
	dropHandle(reader)
	return 0
}

// The plugin's news.

func notifyAnalysisProgress(ctl, src uintptr, state int32, v float32) {
	d, s := handle[Document](ctl), handle[AudioSource](src)
	if d == nil || s == nil {
		return
	}
	p := float64(v)
	if math.IsNaN(p) {
		p = 0
	}
	d.tell(Update{Kind: Analysis, Source: s, Progress: min(max(p, 0), 1), Done: state == 2})
}

// notifyContent queues news of an object's content changed.
func notifyContent(ctl, obj, rng, flags uintptr, kind UpdateKind) {
	d := handle[Document](ctl)
	if d == nil {
		return
	}
	u := Update{Kind: kind, Flags: ContentFlags(int32(uint32(flags)))}
	if rng != 0 {
		r := readAt(rng, fF64, fF64)
		u.Range = &TimeRange{Start: r.f64(0), Duration: r.f64(1)}
	}
	switch kind { //nolint:exhaustive // the news of content alone
	case SourceChanged:
		u.Source = handle[AudioSource](obj)
	case ModificationChanged:
		u.Modification = handle[AudioModification](obj)
	case RegionChanged:
		u.Region = handle[PlaybackRegion](obj)
	}
	d.tell(u)
}
