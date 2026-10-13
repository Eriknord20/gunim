package midi

import (
	"math"
	"sync"
	"time"
)

// epoch is the time a schedule counts from.
var epoch = time.Now()

// sinceEpoch is t in nanoseconds from epoch, and the zero time the
// earliest time of all.
func sinceEpoch(t time.Time) int64 {
	if t.IsZero() {
		return math.MinInt64
	}
	return int64(t.Sub(epoch))
}

// scheduleSize is how many messages a schedule holds.
const scheduleSize = 4096

// A schedule holds messages sent ahead, for a system that sends each
// at once, until they are due. Each comes out at its time, and those of
// one time in the order they went in.
type schedule struct {
	mu  sync.Mutex
	q   []pending // a heap, earliest first
	seq uint64
}

type pending struct {
	at  int64 // nanoseconds from epoch
	seq uint64
	m   Message
}

func (p *pending) before(o *pending) bool {
	return p.at < o.at || p.at == o.at && p.seq < o.seq
}

// add takes m to leave at at, or says to send it now: where it is due
// by now, and nothing waiting is. It says too where m is now the first
// to leave, so whatever waits for the first wakes. It copies a sysex's
// bytes.
func (s *schedule) add(at, now int64, m Message) (send, first bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at <= now && (len(s.q) == 0 || s.q[0].at > now) {
		return true, false, nil
	}
	if s.q == nil {
		s.q = make([]pending, 0, scheduleSize)
	}
	if len(s.q) == cap(s.q) {
		return false, false, ErrFull
	}
	if m.Sysex != nil {
		m.Sysex = append([]byte(nil), m.Sysex...)
	}
	s.seq++
	s.q = append(s.q, pending{at: at, seq: s.seq, m: m})
	i := len(s.q) - 1
	for i > 0 {
		up := (i - 1) / 2
		if !s.q[i].before(&s.q[up]) {
			break
		}
		s.q[i], s.q[up] = s.q[up], s.q[i]
		i = up
	}
	return false, i == 0, nil
}

// due takes the first message off, where it is due by now.
func (s *schedule) due(now int64) (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.q) == 0 || s.q[0].at > now {
		return Message{}, false
	}
	m := s.q[0].m
	last := len(s.q) - 1
	s.q[0] = s.q[last]
	s.q[last] = pending{}
	s.q = s.q[:last]
	for i := 0; ; {
		least := i
		for _, c := range [2]int{2*i + 1, 2*i + 2} {
			if c < len(s.q) && s.q[c].before(&s.q[least]) {
				least = c
			}
		}
		if least == i {
			break
		}
		s.q[i], s.q[least] = s.q[least], s.q[i]
		i = least
	}
	return m, true
}

// next returns when the first message is due.
func (s *schedule) next() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.q) == 0 {
		return 0, false
	}
	return s.q[0].at, true
}

// clear lets go of every message waiting.
func (s *schedule) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.q)
	s.q = s.q[:0]
}
