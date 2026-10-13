package plug

import (
	"math"
	"sync/atomic"
)

// Keys is a set of MIDI keys, from 0 to 127: key n is bit n%64 of word
// n/64.
type Keys [2]uint64

// With returns the set with key n in it. A key outside 0 to 127 stays
// out.
func (k Keys) With(n int) Keys {
	if n >= 0 && n < 128 {
		k[n/64] |= 1 << (n % 64)
	}
	return k
}

// Has reports whether key n is in the set.
func (k Keys) Has(n int) bool {
	return n >= 0 && n < 128 && k[n/64]&(1<<(n%64)) != 0
}

// KeysHost is a [Host] that says which keys the plugin playing holds
// down, as heard: the song's notes as it plays, a MIDI keyboard's, and
// the notes a view auditions. A keyboard on screen lights them. A view
// asks for it by a type assertion on its Host, and asks it each frame.
type KeysHost interface {
	Host
	// Keys returns the keys held down in the sound heard now: each
	// note's from its NoteOn to its NoteOff, its pitch rounded to the
	// nearest key.
	Keys() Keys
}

// keyRuns is how many runs of TraceFrames frames a [KeyTrace] keeps:
// over two seconds at 48 kHz, more than any speaker holds between
// playing and hearing. keyNotes is how many notes it follows at once.
const (
	keyRuns  = 512
	keyNotes = 256
)

// A KeyTrace keeps which keys an instrument's notes hold down, by
// frame, as its host plays it, for a [KeysHost] to say which keys are
// heard. Each run of TraceFrames frames keeps every key down at any
// time in it, so a note shorter than a run still shows. The frames
// count as [Position.Frame] does. The host plays, on the audio
// thread, and views get, on theirs, neither waiting for the other;
// playing allocates nothing.
type KeyTrace struct {
	runs [keyRuns]keyRun
	// The audio thread's: the notes sounding, by ID, with their keys;
	// how many notes hold each key; the keys down; and the run being
	// kept, with the keys down at any time in it so far.
	notes [keyNotes]keyNote
	n     int
	count [128]uint16
	down  Keys
	run   int64
	open  bool
	seen  Keys
}

// keyRun is one run's keys. tag holds the run's number, plus one, and
// is 0 while the keys are written, so a reader that sees the same tag
// before and after reading the keys read the run's. Within a run the
// keys only gain, so a read across two writes of one run reads keys
// that run held.
type keyRun struct{ tag, lo, hi atomic.Uint64 }

type keyNote struct {
	id  int32
	key uint8
}

// Play takes a block from frame at, as long as frames, as the host gives
// it to the instrument with events: each NoteOn presses its key, and
// each NoteOff lets go of the key its note pressed. The events come in
// frame order, as a [Block]'s do.
func (t *KeyTrace) Play(at int64, frames int, events []Event) {
	ei := 0
	end := at + int64(frames)
	for f := at; f < end; {
		r := f >> traceShift
		next := min((r+1)<<traceShift, end)
		if !t.open || r != t.run {
			t.run, t.open, t.seen = r, true, t.down
		}
		for ei < len(events) && at+int64(events[ei].Frame) < next {
			t.event(events[ei])
			ei++
		}
		t.keep(r)
		f = next
	}
	for ; ei < len(events); ei++ {
		t.event(events[ei])
	}
}

// event takes one event.
func (t *KeyTrace) event(e Event) {
	switch e.Kind {
	case NoteOn:
		key := int(math.Round(e.Pitch))
		if key < 0 || key > 127 || t.n == keyNotes || t.find(e.Note) >= 0 {
			return
		}
		t.notes[t.n] = keyNote{id: e.Note, key: uint8(key)}
		t.n++
		t.count[key]++
		t.down = t.down.With(key)
		t.seen = t.seen.With(key)
	case NoteOff:
		i := t.find(e.Note)
		if i < 0 {
			return
		}
		key := t.notes[i].key
		t.n--
		t.notes[i] = t.notes[t.n]
		t.count[key]--
		if t.count[key] == 0 {
			t.down[key/64] &^= 1 << (key % 64)
		}
	case Expression, Pedal:
	}
}

func (t *KeyTrace) find(id int32) int {
	for i := range t.n {
		if t.notes[i].id == id {
			return i
		}
	}
	return -1
}

// keep stores the keys seen in run r.
func (t *KeyTrace) keep(r int64) {
	s := &t.runs[r%keyRuns]
	s.tag.Store(0)
	s.lo.Store(t.seen[0])
	s.hi.Store(t.seen[1])
	s.tag.Store(uint64(r) + 1)
}

// Reset lets go of every key, as the instrument resets and its notes
// end with no NoteOff: the audio thread's. The runs played keep their
// keys.
func (t *KeyTrace) Reset() {
	t.n = 0
	t.count = [128]uint16{}
	t.down = Keys{}
}

// Get returns the keys down at any time in the run of frame f, and
// whether the run was played: none for frames not played, or played
// too long ago.
func (t *KeyTrace) Get(f int64) (Keys, bool) {
	if f < 0 {
		return Keys{}, false
	}
	r := f >> traceShift
	s := &t.runs[r%keyRuns]
	tag := s.tag.Load()
	if tag != uint64(r)+1 {
		return Keys{}, false
	}
	k := Keys{s.lo.Load(), s.hi.Load()}
	if s.tag.Load() != tag {
		return Keys{}, false
	}
	return k, true
}
