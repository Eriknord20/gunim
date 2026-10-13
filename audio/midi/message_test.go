package midi

import (
	"bytes"
	"testing"
)

func TestMessagesEncodeAsTheWireHasThem(t *testing.T) {
	for _, c := range []struct {
		m    Message
		want []byte
		kind Kind
		s    string
	}{
		{NoteOn(0, 60, 100), []byte{0x90, 60, 100}, KindNoteOn, "NoteOn ch 1 key 60 vel 100"},
		{NoteOn(15, 60, 0), []byte{0x9F, 60, 0}, KindNoteOff, "NoteOff ch 16 key 60 vel 0"},
		{NoteOff(2, 61, 64), []byte{0x82, 61, 64}, KindNoteOff, "NoteOff ch 3 key 61 vel 64"},
		{KeyPressure(1, 62, 30), []byte{0xA1, 62, 30}, KindKeyPressure, "KeyPressure ch 2 key 62 30"},
		{Control(3, 64, 127), []byte{0xB3, 64, 127}, KindControl, "Control ch 4 cc 64 127"},
		{Program(4, 5), []byte{0xC4, 5}, KindProgram, "Program ch 5 5"},
		{Pressure(5, 90), []byte{0xD5, 90}, KindPressure, "Pressure ch 6 90"},
		{Bend(6, 0), []byte{0xE6, 0x00, 0x40}, KindBend, "Bend ch 7 0"},
		{Bend(6, -8192), []byte{0xE6, 0, 0}, KindBend, "Bend ch 7 -8192"},
		{Bend(6, 8191), []byte{0xE6, 0x7F, 0x7F}, KindBend, "Bend ch 7 8191"},
		{Clock(), []byte{0xF8}, KindClock, "Clock"},
		{Start(), []byte{0xFA}, KindStart, "Start"},
		{Continue(), []byte{0xFB}, KindContinue, "Continue"},
		{Stop(), []byte{0xFC}, KindStop, "Stop"},
		{SongPosition(300), []byte{0xF2, 300 & 0x7F, 300 >> 7}, KindSongPosition, "SongPosition 300"},
		{Sysex([]byte{0x7E, 0x7F, 0x06, 0x01}), []byte{0xF0, 0x7E, 0x7F, 0x06, 0x01, 0xF7}, KindSysex, "Sysex 7E 7F 06 01"},
		{Message{Status: 0xF6}, []byte{0xF6}, KindTuneRequest, "TuneRequest"},
		{Message{Status: 0xF1, Data1: 0x23}, []byte{0xF1, 0x23}, KindTimeCode, "TimeCode 35"},
	} {
		got := c.m.AppendTo(nil)
		if !bytes.Equal(got, c.want) {
			t.Errorf("%v: AppendTo = % X, want % X", c.m, got, c.want)
		}
		if c.m.Len() != len(c.want) {
			t.Errorf("%v: Len = %d, want %d", c.m, c.m.Len(), len(c.want))
		}
		if c.m.Kind() != c.kind {
			t.Errorf("%v: Kind = %v, want %v", c.m, c.m.Kind(), c.kind)
		}
		if c.m.String() != c.s {
			t.Errorf("String = %q, want %q", c.m.String(), c.s)
		}
	}
}

func TestValuesPastTheirRangeStopAtItsEnds(t *testing.T) {
	m := NoteOn(17, 200, -5)
	if m.Channel() != 1 || m.Key() != 127 || m.Velocity() != 0 {
		t.Errorf("NoteOn(17, 200, -5) = ch %d key %d vel %d, want ch 1 key 127 vel 0", m.Channel(), m.Key(), m.Velocity())
	}
	if b := Bend(0, 99999).Bend(); b != 8191 {
		t.Errorf("Bend past the top = %d, want 8191", b)
	}
	if b := Bend(0, -99999).Bend(); b != -8192 {
		t.Errorf("Bend past the bottom = %d, want -8192", b)
	}
	if v := Control(0, 7, 300).Value(); v != 127 {
		t.Errorf("Control value 300 = %d, want 127", v)
	}
	if p := SongPosition(1 << 20).Beats(); p != 16383 {
		t.Errorf("SongPosition past the end = %d, want 16383", p)
	}
}

func TestUndefinedMessagesTakeNoBytes(t *testing.T) {
	for _, m := range []Message{{}, {Status: 0x40}, {Status: 0xF4}, {Status: 0xF5}, {Status: 0xF7}, {Status: 0xF9}, {Status: 0xFD}} {
		if m.Kind() != KindUndefined || m.Len() != 0 || len(m.AppendTo(nil)) != 0 {
			t.Errorf("%#x: Kind %v, Len %d, %d bytes; want Undefined and none", m.Status, m.Kind(), m.Len(), len(m.AppendTo(nil)))
		}
		if m.Channel() != -1 {
			t.Errorf("%#x: Channel = %d, want -1", m.Status, m.Channel())
		}
	}
}

func TestAppendToMakesNothingWhereTheSliceHasRoom(t *testing.T) {
	buf := make([]byte, 0, 64)
	sx := Sysex([]byte{1, 2, 3})
	if n := testing.AllocsPerRun(100, func() {
		buf = NoteOn(0, 60, 1).AppendTo(buf[:0])
		buf = sx.AppendTo(buf)
	}); n != 0 {
		t.Errorf("AppendTo made %v things a run, want none", n)
	}
}
