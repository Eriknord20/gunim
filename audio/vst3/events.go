package vst3

import (
	"encoding/binary"
	"math"
	"sync"
	"unsafe"

	"github.com/marrasen/gunim/audio/plug"
)

// vstEvent is VST3's Event, as the interfaces lay it out: 48 bytes, its
// kinds' fields in a union at 24.
type vstEvent struct {
	bus, offset int32
	ppq         float64
	flags, kind uint16
	_           [4]byte
	data        [24]byte
}

// The kinds of event.
const (
	evNoteOn         = 0
	evNoteOff        = 1
	evPolyPressure   = 3
	evNoteExpression = 4
	noteExprTuning   = 2
	maxEvents        = 512
	maxNotes         = 256
	channels         = 16
	ccModWheel       = 1
	ccSustain        = 64
	ccPitchBend      = 129
	ccMapped         = 3
	mapMod, mapPedal = 0, 1
	mapBend          = 2
	tuningSemitones  = 240.0
	unmapped         = math.MaxUint32
	defaultBendRange = 2.0
)

// noteOn returns the event that starts a note.
func noteOn(offset int32, channel, pitch int16, tuning, velocity float32, id int32) vstEvent {
	e := vstEvent{offset: offset, kind: evNoteOn}
	le := binary.LittleEndian
	le.PutUint16(e.data[0:], uint16(channel))
	le.PutUint16(e.data[2:], uint16(pitch))
	le.PutUint32(e.data[4:], math.Float32bits(tuning))
	le.PutUint32(e.data[8:], math.Float32bits(velocity))
	le.PutUint32(e.data[16:], uint32(id))
	return e
}

// noteOff returns the event that lets a note go.
func noteOff(offset int32, channel, pitch int16, velocity float32, id int32, tuning float32) vstEvent {
	e := vstEvent{offset: offset, kind: evNoteOff}
	le := binary.LittleEndian
	le.PutUint16(e.data[0:], uint16(channel))
	le.PutUint16(e.data[2:], uint16(pitch))
	le.PutUint32(e.data[4:], math.Float32bits(velocity))
	le.PutUint32(e.data[8:], uint32(id))
	le.PutUint32(e.data[12:], math.Float32bits(tuning))
	return e
}

// polyPressure returns the event that presses a sounding note's key.
func polyPressure(offset int32, channel, pitch int16, pressure float32, id int32) vstEvent {
	e := vstEvent{offset: offset, kind: evPolyPressure}
	le := binary.LittleEndian
	le.PutUint16(e.data[0:], uint16(channel))
	le.PutUint16(e.data[2:], uint16(pitch))
	le.PutUint32(e.data[4:], math.Float32bits(pressure))
	le.PutUint32(e.data[8:], uint32(id))
	return e
}

// noteExpression returns the event that changes a sounding note's
// expression typ to value, from 0 to 1.
func noteExpression(offset int32, typ uint32, id int32, value float64) vstEvent {
	e := vstEvent{offset: offset, kind: evNoteExpression}
	le := binary.LittleEndian
	le.PutUint32(e.data[0:], typ)
	le.PutUint32(e.data[4:], uint32(id))
	le.PutUint64(e.data[8:], math.Float64bits(value))
	return e
}

// eventList is a list of events going into a plugin, a block's: its
// room is made once, so a block makes nothing.
type eventList struct {
	object
	events [maxEvents]vstEvent
	n      int
}

var eventListKind = &kind{iids: []uid{iidEventList}, make: func() []uintptr {
	el := self[eventList]
	return []uintptr{
		newCallback(func(this uintptr) uintptr { return uintptr(uint32(el(this).n)) }),
		newCallback(func(this, index, e uintptr) uintptr {
			l, i := el(this), int(i32(index))
			if i < 0 || i >= l.n || e == 0 {
				return resultOf(resultInvalid)
			}
			*ptr[vstEvent](e) = l.events[i]
			return resultOf(resultOK)
		}),
		newCallback(func(this, e uintptr) uintptr { return resultOf(resultFalse) }),
	}
}}

// newEventList returns an empty list of events, and its pointer.
func newEventList() (l *eventList, p uintptr) {
	l = new(eventList)
	return l, keep(l, eventListKind, false)
}

// add adds e, where there is room: past it, a block's events are let
// go.
func (l *eventList) add(e vstEvent) {
	if l.n == len(l.events) {
		return
	}
	l.events[l.n] = e
	l.n++
}

// free lets go of the list.
func (l *eventList) free() { drop(uintptr(unsafe.Pointer(&l.object))) } //nolint:gosec // its own address

// An event list for what a plugin sends, which the host lets go: it
// counts none.

type dropList struct{ object }

var eventsKind = &kind{iids: []uid{iidEventList}, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this uintptr) uintptr { return 0 }),
		newCallback(func(this, index, e uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, e uintptr) uintptr { return resultOf(resultOK) }),
	}
}}

var theEvents = sync.OnceValue(func() uintptr {
	return keep(new(dropList), eventsKind, false)
})

// sounding is a note the plugin plays, as the keyboard remembers it.
type sounding struct {
	id      int32
	channel int16
	pitch   int16
	tuning  float32
	used    bool
}

// keyboard turns package plug's notes into a plugin's events: each
// note's ID, a channel for each group of notes, its pitch as a key and
// a tuning, and expression and pedals as the plugin takes them. Its
// state belongs to the audio's goroutine, but for what learn finds,
// which holds while the plugin processes.
type keyboard struct {
	notes [maxNotes]sounding
	// groups holds the group each channel plays, and claimed whether
	// one does; next is the channel claimed next.
	groups  [channels]int32
	claimed [channels]bool
	next    int
	// cc holds the parameters the plugin moves by a MIDI controller,
	// on each channel: the mod wheel, the pedal and the pitch bend.
	cc [channels][ccMapped]uint32
	// moved holds each mapped controller moved from its rest.
	moved [channels][ccMapped]bool
	// tuning says the plugin bends a note of its own, by note
	// expression.
	tuning bool
	// lifting says the notes sounding end and the controllers rest at
	// the next block's start, after a reset.
	lifting bool
}

// learn finds what the plugin takes: the parameters it moves by MIDI
// controllers, and whether it bends notes of their own. It runs on
// the plugins' thread.
func (k *keyboard) learn(p *Plugin) {
	for c := range channels {
		for i := range ccMapped {
			k.cc[c][i] = unmapped
		}
	}
	if !p.notes || p.ctrl == 0 {
		return
	}
	if mm := query(p.ctrl, iidMidiMapping); mm != 0 {
		ccs := [ccMapped]uintptr{ccModWheel, ccSustain, ccPitchBend}
		for c := range channels {
			for i, cc := range ccs {
				var id uint32
				if result(call(mm, mGetMidiControllerAssignment, 0, uintptr(c), cc, uintptr(unsafe.Pointer(&id)))) == resultOK {
					k.cc[c][i] = id
				}
			}
		}
		release(mm)
	}
	if ne := query(p.ctrl, iidNoteExpression); ne != 0 {
		n := int(i32(call(ne, mGetNoteExpressionCount, 0, 0)))
		for i := range n {
			var info noteExpressionInfo
			if result(call(ne, mGetNoteExpressionInfo, 0, 0, uintptr(i), uintptr(unsafe.Pointer(&info)))) == resultOK &&
				info.typeID == noteExprTuning {
				k.tuning = true
			}
		}
		release(ne)
	}
}

// noteExpressionInfo is VST3's NoteExpressionTypeInfo, as the
// interfaces lay it out.
type noteExpressionInfo struct {
	typeID              uint32
	title, short, units [128]uint16
	unitID              int32
	def, min, max       float64
	steps               int32
	_                   [4]byte
	paramID             uint32
	flags               int32
}

// Bends says the plugin bends each note of its own, by VST3's note
// expression; else a bend moves the pitch bend of the note's group,
// where the plugin maps one, by [BendRange] semitones.
func (p *Plugin) Bends() bool { return p.keys.tuning }

// BendRange is how far a plugin's pitch bend reaches, in semitones
// each way, as a bend moves it for a plugin that bends no note of its
// own: MIDI's, as most synths start.
const BendRange = defaultBendRange

// reset forgets the notes and groups, after the plugin's processing is
// reset: they end at the next block's start, and the pedals lift.
func (k *keyboard) reset() {
	k.lifting = true
	k.claimed = [channels]bool{}
	k.next = 0
}

// lift ends the notes sounding, and rests the controllers moved, at the
// first frame of the block to come, where a reset asked.
func (k *keyboard) lift(p *Plugin) {
	if !k.lifting {
		return
	}
	k.lifting = false
	for i := range k.notes {
		n := &k.notes[i]
		if n.used {
			p.events.add(noteOff(0, n.channel, n.pitch, 0, n.id, n.tuning))
			n.used = false
		}
	}
	rest := [ccMapped]float64{mapMod: 0, mapPedal: 0, mapBend: 0.5}
	for c := range channels {
		for i := range ccMapped {
			if k.moved[c][i] {
				k.moved[c][i] = false
				p.change(k.cc[c][i], 0, rest[i])
			}
		}
	}
}

// channel returns the channel of group g, claiming one for it, in turn,
// where it has none.
func (k *keyboard) channel(g int32) int16 {
	for c := range channels {
		if k.claimed[c] && k.groups[c] == g {
			return int16(c)
		}
	}
	c := k.next
	k.next = (k.next + 1) % channels
	k.groups[c], k.claimed[c] = g, true
	return int16(c)
}

// find returns the note of ID id sounding, or nil.
func (k *keyboard) find(id int32) *sounding {
	for i := range k.notes {
		if k.notes[i].used && k.notes[i].id == id {
			return &k.notes[i]
		}
	}
	return nil
}

// play turns e into the plugin's events and changes, at its frame.
func (k *keyboard) play(p *Plugin, e *plug.Event) {
	f := int32(e.Frame)
	switch e.Kind {
	case plug.NoteOn:
		key := math.Round(e.Pitch)
		pitch := int16(min(max(key, 0), 127))
		tuning := float32((e.Pitch - float64(pitch)) * 100)
		ch := k.channel(e.Group)
		n := k.find(e.Note)
		for i := range k.notes {
			if n != nil {
				break
			}
			if !k.notes[i].used {
				n = &k.notes[i]
			}
		}
		if n == nil {
			// Every place taken: the oldest kept goes.
			n = &k.notes[0]
		}
		*n = sounding{id: e.Note, channel: ch, pitch: pitch, tuning: tuning, used: true}
		p.events.add(noteOn(f, ch, pitch, tuning, float32(e.Velocity), e.Note))
	case plug.NoteOff:
		n := k.find(e.Note)
		if n == nil {
			return
		}
		n.used = false
		p.events.add(noteOff(f, n.channel, n.pitch, float32(e.Velocity), n.id, n.tuning))
	case plug.Expression:
		n := k.find(e.Note)
		if n == nil {
			return
		}
		switch e.Expr {
		case plug.Bend:
			if k.tuning {
				// The note's own tuning counts in, as the note's start.
				semis := e.Value + float64(n.tuning)/100
				p.events.add(noteExpression(f, noteExprTuning, n.id, min(max(0.5+semis/tuningSemitones, 0), 1)))
				return
			}
			k.move(p, n.channel, mapBend, f, min(max(0.5+e.Value/(2*BendRange), 0), 1))
		case plug.Pressure:
			p.events.add(polyPressure(f, n.channel, n.pitch, float32(min(max(e.Value, 0), 1)), n.id))
		case plug.Mod:
			k.move(p, n.channel, mapMod, f, min(max(e.Value, 0), 1))
		}
	case plug.Pedal:
		k.move(p, k.channel(e.Group), mapPedal, f, min(max(e.Value, 0), 1))
	}
}

// move moves controller i of channel ch to v, from frame f, where the
// plugin maps it to a parameter.
func (k *keyboard) move(p *Plugin, ch int16, i int, f int32, v float64) {
	id := k.cc[ch][i]
	if id == unmapped {
		return
	}
	k.moved[ch][i] = true
	p.change(id, f, v)
}

// change adds a change of parameter id to v at frame f to the block.
func (p *Plugin) change(id uint32, f int32, v float64) {
	if q := p.inChanges.queue(id); q >= 0 {
		p.inChanges.queues[q].add(f, v)
	}
}
