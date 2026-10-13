package midi

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"testing"
)

// parseAll parses each of chunks in turn, and returns what came out,
// each sysex copied.
func parseAll(p *Parser, chunks ...[]byte) []Message {
	var got []Message
	for _, c := range chunks {
		p.Parse(c, func(m Message) {
			if m.Status == 0xF0 {
				m.Sysex = append([]byte{}, m.Sysex...)
			}
			got = append(got, m)
		})
	}
	return got
}

func TestParserFollowsRunningStatus(t *testing.T) {
	var p Parser
	got := parseAll(&p, []byte{0x90, 60, 100, 62, 90, 64}, []byte{0, 0xB0, 7, 100, 10, 64})
	want := []Message{NoteOn(0, 60, 100), NoteOn(0, 62, 90), NoteOn(0, 64, 0), Control(0, 7, 100), Control(0, 10, 64)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParserLetsRealTimeThroughFromInsideAMessage(t *testing.T) {
	var p Parser
	got := parseAll(&p, []byte{0x90, 60, 0xF8, 100, 0xF0, 0x7D, 0xFA, 0x01, 0xF7, 0xFE})
	want := []Message{Clock(), NoteOn(0, 60, 100), Start(), Sysex([]byte{0x7D, 0x01}), {Status: 0xFE}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParserGathersASysexOverManySlices(t *testing.T) {
	var p Parser
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 128)
	}
	wire := Sysex(data).AppendTo(nil)
	var chunks [][]byte
	for len(wire) > 0 {
		n := min(len(wire), 7)
		chunks, wire = append(chunks, wire[:n]), wire[n:]
	}
	got := parseAll(&p, chunks...)
	if len(got) != 1 || !bytes.Equal(got[0].Sysex, data) {
		t.Fatalf("got %d messages, want one sysex of the %d bytes", len(got), len(data))
	}
}

func TestAStatusByteEndsASysexAndStartsItsOwn(t *testing.T) {
	var p Parser
	got := parseAll(&p, []byte{0xF0, 0x7D, 0x05, 0x91, 60, 100, 0xF7})
	want := []Message{Sysex([]byte{0x7D, 0x05}), NoteOn(1, 60, 100)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParserPassesOverStrayAndUndefinedBytes(t *testing.T) {
	var p Parser
	// Data before any status; an undefined status and its data; a
	// lone 0xF7; a system common message, after which running status
	// is gone.
	got := parseAll(&p, []byte{5, 6, 0xF4, 7, 0xF7, 0x80, 60, 0, 0xF3, 4, 61, 0, 0xF9, 0xFD, 0xC0, 9})
	want := []Message{NoteOff(0, 60, 0), {Status: 0xF3, Data1: 4}, Program(0, 9)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParserLetsGoOfASysexPastMaxSysex(t *testing.T) {
	var p Parser
	big := make([]byte, MaxSysex+1)
	got := parseAll(&p, []byte{0xF0}, big, []byte{0xF7, 0xF0, 1, 0xF7})
	if len(got) != 1 || !bytes.Equal(got[0].Sysex, []byte{1}) {
		t.Errorf("got %v, want the small sysex after alone", got)
	}
}

func TestParserResetForgetsHalfAMessage(t *testing.T) {
	var p Parser
	parseAll(&p, []byte{0x90, 60})
	p.Reset()
	if got := parseAll(&p, []byte{100, 0xF0, 1}); len(got) != 0 {
		t.Errorf("after Reset got %v, want nothing", got)
	}
	p.Reset()
	if got := parseAll(&p, []byte{2, 0xF7}); len(got) != 0 {
		t.Errorf("a sysex's tail after Reset gave %v, want nothing", got)
	}
}

func TestEncoderLeavesOutARepeatedStatus(t *testing.T) {
	var e Encoder
	var b []byte
	for _, m := range []Message{NoteOn(0, 60, 1), NoteOn(0, 61, 2), Clock(), NoteOn(0, 62, 3), NoteOn(1, 63, 4), Start(), NoteOn(1, 64, 5),
		SongPosition(3), NoteOn(1, 65, 6)} {
		b = e.Append(b, m)
	}
	want := []byte{0x90, 60, 1, 61, 2, 0xF8, 62, 3, 0x91, 63, 4, 0xFA, 64, 5, 0xF2, 3, 0, 0x91, 65, 6}
	if !bytes.Equal(b, want) {
		t.Errorf("got % X\nwant % X", b, want)
	}
	e.Reset()
	if b = e.Append(nil, NoteOn(1, 66, 7)); b[0] != 0x91 {
		t.Errorf("after Reset got % X, want the status byte first", b)
	}
}

// TestEncodedStreamsParseBack writes random messages with running
// status, cuts the bytes anywhere, and reads them back.
func TestEncodedStreamsParseBack(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		want := make([]Message, 0, 40)
		var e Encoder
		var wire []byte
		for range 1 + r.IntN(40) {
			ch, a, b := r.IntN(16), r.IntN(128), r.IntN(128)
			var m Message
			switch r.IntN(12) {
			case 0:
				m = NoteOn(ch, a, b)
			case 1:
				m = NoteOff(ch, a, b)
			case 2:
				m = Control(ch, a, b)
			case 3:
				m = Program(ch, a)
			case 4:
				m = Pressure(ch, a)
			case 5:
				m = KeyPressure(ch, a, b)
			case 6:
				m = Bend(ch, r.IntN(16384)-8192)
			case 7:
				m = Clock()
			case 8:
				m = SongPosition(r.IntN(16384))
			case 9:
				m = Message{Status: 0xF3, Data1: byte(a)}
			case 10:
				d := make([]byte, r.IntN(50))
				for i := range d {
					d[i] = byte(r.IntN(128))
				}
				m = Sysex(d)
			default:
				m = Start()
			}
			want = append(want, m)
			wire = e.Append(wire, m)
		}
		var chunks [][]byte
		for len(wire) > 0 {
			n := 1 + r.IntN(len(wire))
			chunks, wire = append(chunks, wire[:n]), wire[n:]
		}
		var p Parser
		got := parseAll(&p, chunks...)
		for i := range want {
			if want[i].Status == 0xF0 && len(want[i].Sysex) == 0 {
				want[i].Sysex = []byte{}
			}
		}
		if !reflect.DeepEqual(got, want) {
			for i := range min(len(got), len(want)) {
				if !reflect.DeepEqual(got[i], want[i]) {
					t.Fatalf("message %d: got %#v, want %#v", i, got[i], want[i])
				}
			}
			t.Fatalf("got %d messages, want %d", len(got), len(want))
		}
	}
}

func TestParsingMakesNothingOnceWarm(t *testing.T) {
	var p Parser
	wire := []byte{0x90, 60, 100, 61, 0, 0xF8, 0xF0, 1, 2, 3, 0xF7, 0xE0, 0, 64}
	n := 0
	f := func(Message) { n++ }
	p.Parse(wire, f)
	if a := testing.AllocsPerRun(100, func() { p.Parse(wire, f) }); a != 0 {
		t.Errorf("Parse made %v things a run, want none", a)
	}
}

func FuzzParser(f *testing.F) {
	f.Add([]byte{0x90, 60, 100, 61, 0, 0xF0, 1, 0xF8, 2, 0xF7})
	f.Fuzz(func(t *testing.T, b []byte) {
		var p Parser
		p.Parse(b, func(m Message) {
			if m.Kind() == KindUndefined {
				t.Fatalf("parsed an undefined message %#x from % X", m.Status, b)
			}
			for _, c := range append(m.AppendTo(nil)[1:], m.Sysex...) {
				if c >= 0x80 && c != 0xF7 {
					t.Fatalf("a data byte %#x in %v from % X", c, m, b)
				}
			}
		})
	})
}
