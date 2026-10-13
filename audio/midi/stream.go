package midi

// MaxSysex is the longest system exclusive message a [Parser] gathers,
// in bytes. The bytes of a longer one are let go, and so is the
// message.
const MaxSysex = 1 << 20

// A Parser reads messages from a stream of MIDI bytes, as a cable, a
// MIDI file's track or a system's sysex buffers carry them. A message
// may straddle the slices it is given.
//
// It follows running status, where a channel message leaves out its
// status byte when it is the one before's. A real-time message, as the
// clock's tick, comes out as it arrives, even from inside another
// message. Any status byte ends a sysex, as 0xF7 does. Data bytes with
// no status before them, and status bytes MIDI 1.0 leaves free, are
// passed over.
//
// The zero Parser is ready to use.
type Parser struct {
	status byte // the status of the message being read, or zero
	need   int  // data bytes still to come for status
	data   [2]byte
	n      int
	sysex  []byte
	in     bool // inside a sysex
	over   bool // the sysex grew past MaxSysex
}

// Parse reads b, and calls f with each message it completes, in order.
// A sysex's bytes are the Parser's own, and hold until f returns.
func (p *Parser) Parse(b []byte, f func(Message)) {
	for _, c := range b {
		p.byte(c, f)
	}
}

// Reset forgets a message half read, and the running status.
func (p *Parser) Reset() {
	p.status, p.need, p.n = 0, 0, 0
	p.sysex, p.in, p.over = p.sysex[:0], false, false
}

func (p *Parser) byte(c byte, f func(Message)) {
	switch {
	case c >= 0xF8:
		// Real time: through at once, leaving the rest as it is.
		if dataLen(c) == 0 {
			f(Message{Status: c})
		}
		return
	case c < 0x80:
		if p.in {
			if len(p.sysex) < MaxSysex {
				p.sysex = append(p.sysex, c)
			} else {
				p.over = true
			}
			return
		}
		if p.status == 0 {
			return
		}
		p.data[p.n] = c
		p.n++
		if p.n < p.need {
			return
		}
		f(Message{Status: p.status, Data1: p.data[0], Data2: p.data[1]})
		p.n, p.data = 0, [2]byte{}
		if p.status >= 0xF0 {
			// System common messages leave no running status.
			p.status = 0
		}
		return
	}
	if p.in {
		p.in = false
		if !p.over {
			f(Message{Status: 0xF0, Sysex: p.sysex})
		}
		p.sysex, p.over = p.sysex[:0], false
	}
	p.n, p.data = 0, [2]byte{}
	switch n := dataLen(c); {
	case c == 0xF0:
		p.status, p.in = 0, true
	case n < 0, c == 0xF7:
		p.status = 0
	case n == 0:
		p.status = 0
		f(Message{Status: c})
	default:
		p.status, p.need = c, n
	}
}

// An Encoder writes messages as MIDI bytes with running status: a
// channel message whose status byte is the one before's leaves it out,
// as a cable and a MIDI file carry it. A real-time message leaves the
// running status as it is; a system common message or a sysex ends it.
//
// The zero Encoder is ready to use.
type Encoder struct {
	status byte
}

// Append appends m to b, and returns the longer slice. It makes nothing
// new where b has room.
func (e *Encoder) Append(b []byte, m Message) []byte {
	switch {
	case m.Status >= 0xF8:
	case m.Status >= 0xF0:
		e.status = 0
	case m.Status >= 0x80:
		if m.Status == e.status {
			n := len(b)
			b = m.AppendTo(b)
			return append(b[:n], b[n+1:]...)
		}
		e.status = m.Status
	}
	return m.AppendTo(b)
}

// Reset makes the next channel message carry its status byte, as after
// a pause on a cable.
func (e *Encoder) Reset() { e.status = 0 }
