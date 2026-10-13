package midi

import (
	"fmt"
	"strings"
)

// A Message is one MIDI 1.0 message: a channel message, as a note or a
// controller's move, a system message, as the clock's tick, or a
// system exclusive message.
//
// A Message is a value, made by the functions named for each kind:
// [NoteOn], [Control], [Clock], [Sysex] and the rest.
type Message struct {
	// Status is the status byte. 0x80 to 0xEF is a channel message,
	// the channel in its low four bits. 0xF0 is a system exclusive
	// message, and 0xF1 to 0xFF the other system messages.
	Status byte
	// Data1 and Data2 are the data bytes, each 0 to 127, as many as
	// Status takes: two for a note, one for a program, none for the
	// clock.
	Data1, Data2 byte
	// Sysex holds a system exclusive message's bytes after its 0xF0 and
	// before its 0xF7: the maker's ID first, then the maker's own data.
	// Each is 0 to 127.
	Sysex []byte
}

// Kind is what a message says: a note on, a controller's move, the
// clock's tick. A channel kind is the status byte's high four bits; a
// system kind is the whole status byte.
type Kind uint8

// The kinds of MIDI message.
const (
	KindNoteOff       Kind = 0x80
	KindNoteOn        Kind = 0x90
	KindKeyPressure   Kind = 0xA0 // aftertouch on one key
	KindControl       Kind = 0xB0
	KindProgram       Kind = 0xC0
	KindPressure      Kind = 0xD0 // aftertouch on the whole channel
	KindBend          Kind = 0xE0
	KindSysex         Kind = 0xF0
	KindTimeCode      Kind = 0xF1 // a quarter frame of MIDI time code
	KindSongPosition  Kind = 0xF2
	KindSongSelect    Kind = 0xF3
	KindTuneRequest   Kind = 0xF6
	KindClock         Kind = 0xF8 // 24 to the quarter note
	KindStart         Kind = 0xFA
	KindContinue      Kind = 0xFB
	KindStop          Kind = 0xFC
	KindActiveSensing Kind = 0xFE
	KindReset         Kind = 0xFF
	// KindUndefined is the kind of a message whose status byte MIDI 1.0
	// leaves free, and of a zero Message.
	KindUndefined Kind = 0
)

var kindNames = map[Kind]string{
	KindNoteOff: "NoteOff", KindNoteOn: "NoteOn", KindKeyPressure: "KeyPressure",
	KindControl: "Control", KindProgram: "Program", KindPressure: "Pressure",
	KindBend: "Bend", KindSysex: "Sysex", KindTimeCode: "TimeCode",
	KindSongPosition: "SongPosition", KindSongSelect: "SongSelect",
	KindTuneRequest: "TuneRequest", KindClock: "Clock", KindStart: "Start",
	KindContinue: "Continue", KindStop: "Stop", KindActiveSensing: "ActiveSensing",
	KindReset: "Reset", KindUndefined: "Undefined",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%#x)", uint8(k))
}

// dataLen is how many data bytes follow status: -1 for a data byte, and
// for a status byte MIDI 1.0 leaves free.
func dataLen(status byte) int {
	switch {
	case status < 0x80:
		return -1
	case status < 0xC0, status >= 0xE0 && status < 0xF0:
		return 2
	case status < 0xE0:
		return 1
	}
	switch status {
	case 0xF1, 0xF3:
		return 1
	case 0xF2:
		return 2
	case 0xF0, 0xF6, 0xF7, 0xF8, 0xFA, 0xFB, 0xFC, 0xFE, 0xFF:
		return 0
	}
	return -1
}

// Kind returns what m says. A NoteOn of velocity zero is a NoteOff,
// as MIDI means it; its Status stays as it came.
func (m Message) Kind() Kind {
	if dataLen(m.Status) < 0 || m.Status == 0xF7 {
		return KindUndefined
	}
	if m.Status < 0xF0 {
		k := Kind(m.Status & 0xF0)
		if k == KindNoteOn && m.Data2 == 0 {
			return KindNoteOff
		}
		return k
	}
	return Kind(m.Status)
}

// Channel returns the channel of a channel message, 0 to 15, as MIDI
// numbers them 1 to 16, and -1 for a system message.
func (m Message) Channel() int {
	if m.Status < 0x80 || m.Status >= 0xF0 {
		return -1
	}
	return int(m.Status & 0x0F)
}

// Key returns the note number of a NoteOn, NoteOff or KeyPressure,
// 0 to 127; 60 is middle C.
func (m Message) Key() int { return int(m.Data1) }

// Velocity returns how hard a note was struck or let go, 0 to 127.
func (m Message) Velocity() int { return int(m.Data2) }

// Controller returns the controller a Control message moves, 0 to 127:
// 1 is the mod wheel, 64 the sustain pedal.
func (m Message) Controller() int { return int(m.Data1) }

// Value returns a message's one value, 0 to 127: a controller's, a
// program's number, a pressure, a SongSelect's song, or a TimeCode's
// quarter frame.
func (m Message) Value() int {
	switch m.Kind() {
	case KindControl, KindKeyPressure:
		return int(m.Data2)
	default:
		return int(m.Data1)
	}
}

// Bend returns a pitch bend, from -8192 down to 8191 up; zero is the
// wheel at rest.
func (m Message) Bend() int { return int(m.Data1) | int(m.Data2)<<7 - 8192 }

// Beats returns a SongPosition's place, in sixteenth notes from the
// song's start.
func (m Message) Beats() int { return int(m.Data1) | int(m.Data2)<<7 }

// Len returns how many bytes m takes on the wire, its status byte with
// them, and a sysex's 0xF0 and 0xF7.
func (m Message) Len() int {
	if m.Status == 0xF0 {
		return len(m.Sysex) + 2
	}
	n := dataLen(m.Status)
	if n < 0 || m.Status == 0xF7 {
		return 0
	}
	return 1 + n
}

// AppendTo appends m as MIDI bytes to b, status byte first, and returns
// the longer slice. It makes nothing new where b has room. A message of
// kind [KindUndefined] appends nothing.
func (m Message) AppendTo(b []byte) []byte {
	if m.Status == 0xF0 {
		b = append(b, 0xF0)
		b = append(b, m.Sysex...)
		return append(b, 0xF7)
	}
	if m.Status == 0xF7 {
		return b
	}
	switch dataLen(m.Status) {
	case 0:
		return append(b, m.Status)
	case 1:
		return append(b, m.Status, m.Data1&0x7F)
	case 2:
		return append(b, m.Status, m.Data1&0x7F, m.Data2&0x7F)
	}
	return b
}

func (m Message) String() string {
	k := m.Kind()
	switch k {
	case KindNoteOn, KindNoteOff:
		return fmt.Sprintf("%v ch %d key %d vel %d", k, m.Channel()+1, m.Key(), m.Velocity())
	case KindKeyPressure:
		return fmt.Sprintf("%v ch %d key %d %d", k, m.Channel()+1, m.Key(), m.Value())
	case KindControl:
		return fmt.Sprintf("%v ch %d cc %d %d", k, m.Channel()+1, m.Controller(), m.Value())
	case KindProgram, KindPressure:
		return fmt.Sprintf("%v ch %d %d", k, m.Channel()+1, m.Value())
	case KindBend:
		return fmt.Sprintf("%v ch %d %d", k, m.Channel()+1, m.Bend())
	case KindSongPosition:
		return fmt.Sprintf("%v %d", k, m.Beats())
	case KindTimeCode, KindSongSelect:
		return fmt.Sprintf("%v %d", k, m.Value())
	case KindSysex:
		var s strings.Builder
		s.WriteString("Sysex")
		for _, c := range m.Sysex {
			fmt.Fprintf(&s, " %02X", c)
		}
		return s.String()
	case KindUndefined:
		return fmt.Sprintf("Undefined %#x", m.Status)
	default:
		return k.String()
	}
}

func channel(ch int) byte { return byte(ch) & 0x0F }

func data(v int) byte { return byte(min(max(v, 0), 127)) }

// NoteOn strikes key, 0 to 127, on channel ch, 0 to 15, at velocity
// 1 to 127. Velocity 0 lets the key go, as a NoteOff.
func NoteOn(ch, key, velocity int) Message {
	return Message{Status: 0x90 | channel(ch), Data1: data(key), Data2: data(velocity)}
}

// NoteOff lets key go on channel ch, at velocity 0 to 127; 64 is the
// usual one from a keyboard that senses none.
func NoteOff(ch, key, velocity int) Message {
	return Message{Status: 0x80 | channel(ch), Data1: data(key), Data2: data(velocity)}
}

// Control moves controller cc on channel ch to value, each 0 to 127.
func Control(ch, cc, value int) Message {
	return Message{Status: 0xB0 | channel(ch), Data1: data(cc), Data2: data(value)}
}

// Program picks program p, 0 to 127, on channel ch.
func Program(ch, p int) Message {
	return Message{Status: 0xC0 | channel(ch), Data1: data(p)}
}

// Pressure is aftertouch on the whole of channel ch, 0 to 127.
func Pressure(ch, pressure int) Message {
	return Message{Status: 0xD0 | channel(ch), Data1: data(pressure)}
}

// KeyPressure is aftertouch on key alone, on channel ch, 0 to 127.
func KeyPressure(ch, key, pressure int) Message {
	return Message{Status: 0xA0 | channel(ch), Data1: data(key), Data2: data(pressure)}
}

// Bend bends channel ch's pitch by bend, from -8192 down to 8191 up;
// zero is the wheel at rest. A bend past either end stops there.
func Bend(ch, bend int) Message {
	v := min(max(bend+8192, 0), 16383)
	return Message{Status: 0xE0 | channel(ch), Data1: byte(v & 0x7F), Data2: byte(v >> 7)}
}

// Clock is the MIDI clock's tick, sent 24 times to the quarter note.
func Clock() Message { return Message{Status: 0xF8} }

// Start starts the song from its start, at the next [Clock].
func Start() Message { return Message{Status: 0xFA} }

// Continue starts the song from where it stopped, or from the last
// [SongPosition], at the next [Clock].
func Continue() Message { return Message{Status: 0xFB} }

// Stop stops the song where it is.
func Stop() Message { return Message{Status: 0xFC} }

// SongPosition moves the song to beats sixteenth notes from its start,
// 0 to 16383, for the next [Continue].
func SongPosition(beats int) Message {
	v := min(max(beats, 0), 16383)
	return Message{Status: 0xF2, Data1: byte(v & 0x7F), Data2: byte(v >> 7)}
}

// Sysex is a system exclusive message of data: the maker's ID first,
// then the maker's own bytes, each 0 to 127, without the 0xF0 and 0xF7
// around them. The message holds data itself, so data must stay as it
// is until the message is sent.
func Sysex(data []byte) Message { return Message{Status: 0xF0, Sysex: data} }
