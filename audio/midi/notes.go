package midi

import (
	"math"
	"sync"
)

// stillOn marks a note sounding with no NoteOff sent for it.
const stillOn = math.MaxInt64

// sounding keeps the notes an Out may have left sounding, to let them
// go as it stops or closes.
type sounding struct {
	mu sync.Mutex
	// off holds each note's state: zero for silent, stillOn, or the
	// time its NoteOff leaves, from epoch, while that is ahead.
	off [16][128]int64
	// pedal marks the channels that played a note or moved the
	// sustain pedal.
	pedal uint16
}

// sent takes note of m, sent to leave at at, as it is now.
func (s *sounding) sent(at, now int64, m Message) {
	if m.Status >= 0xF0 {
		return
	}
	ch, key := m.Status&0x0F, m.Data1&0x7F
	s.mu.Lock()
	defer s.mu.Unlock()
	switch k := m.Kind(); {
	case k == KindNoteOn:
		s.off[ch][key] = stillOn
		s.pedal |= 1 << ch
	case k == KindNoteOff && s.off[ch][key] != 0:
		if at <= now {
			s.off[ch][key] = 0
		} else {
			s.off[ch][key] = at
		}
	case k == KindControl && key == 64:
		s.pedal |= 1 << ch
	}
}

// end calls note with each note that may sound at now, and pedal with
// each channel that played, and forgets them all.
func (s *sounding) end(now int64, note func(ch, key int), pedal func(ch int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.off {
		for key, off := range &s.off[ch] {
			if off > now {
				note(ch, key)
			}
			s.off[ch][key] = 0
		}
	}
	for ch := range 16 {
		if s.pedal&(1<<ch) != 0 {
			pedal(ch)
		}
	}
	s.pedal = 0
}
