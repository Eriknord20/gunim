package midi

import (
	"errors"
	"testing"
	"time"
)

func TestScheduleLetsMessagesOutInTimeOrderAndTiesInTheOrderSent(t *testing.T) {
	var s schedule
	// The first is due later than now, so all wait.
	for i, at := range []int64{50, 10, 30, 10, 50, 20, 10} {
		if send, _, err := s.add(at, 0, NoteOn(0, i, 1)); send || err != nil {
			t.Fatalf("add %d: send %v, err %v; want it to wait", i, send, err)
		}
	}
	if _, ok := s.due(9); ok {
		t.Fatal("a message came out before its time")
	}
	var keys []int
	for {
		m, ok := s.due(100)
		if !ok {
			break
		}
		keys = append(keys, m.Key())
	}
	want := []int{1, 3, 6, 5, 2, 0, 4}
	if len(keys) != len(want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("got %v, want %v", keys, want)
		}
	}
}

func TestScheduleSendsAtOnceOnlyWhileNothingDueWaits(t *testing.T) {
	var s schedule
	if send, _, _ := s.add(5, 10, Clock()); !send {
		t.Error("a message due, with nothing waiting, waits; want it sent at once")
	}
	if _, first, _ := s.add(20, 10, Clock()); !first {
		t.Error("the only message waiting is not first")
	}
	if send, _, _ := s.add(5, 10, Clock()); !send {
		t.Error("a message due, with only a later one waiting, waits; want it sent at once")
	}
	// Now one waiting is due, as the sender is late: the next goes
	// after it.
	if send, first, _ := s.add(22, 25, Stop()); send || first {
		t.Errorf("behind a due message: send %v first %v, want it to wait behind", send, first)
	}
	if m, _ := s.due(25); m.Kind() != KindClock {
		t.Errorf("first out %v, want the Clock sent first", m)
	}
	if m, _ := s.due(25); m.Kind() != KindStop {
		t.Errorf("second out %v, want Stop", m)
	}
	if send, _, _ := s.add(sinceEpoch(time.Time{}), 25, Start()); !send {
		t.Error("the zero time waits; want it sent at once")
	}
}

func TestScheduleTellsWhenAMessageGoesFirst(t *testing.T) {
	var s schedule
	for _, c := range []struct {
		at    int64
		first bool
	}{{100, true}, {200, false}, {50, true}, {50, false}, {49, true}} {
		if _, first, _ := s.add(c.at, 0, Clock()); first != c.first {
			t.Errorf("add at %d: first %v, want %v", c.at, first, c.first)
		}
	}
	if at, _ := s.next(); at != 49 {
		t.Errorf("next = %d, want 49", at)
	}
}

func TestScheduleIsFullAtItsSizeAndClearEmptiesIt(t *testing.T) {
	var s schedule
	for i := range scheduleSize {
		if _, _, err := s.add(int64(100+i), 0, Clock()); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, _, err := s.add(1000, 0, Clock()); !errors.Is(err, ErrFull) {
		t.Errorf("add past the size: %v, want ErrFull", err)
	}
	s.clear()
	if _, ok := s.next(); ok {
		t.Error("a message waits after clear")
	}
	if _, _, err := s.add(1000, 0, Clock()); err != nil {
		t.Errorf("add after clear: %v", err)
	}
}

func TestScheduleCopiesASysex(t *testing.T) {
	var s schedule
	data := []byte{1, 2, 3}
	_, _, _ = s.add(10, 0, Sysex(data))
	data[0] = 99
	if m, _ := s.due(10); m.Sysex[0] != 1 {
		t.Errorf("the sysex waiting changed with the caller's bytes: % X", m.Sysex)
	}
}

func TestSchedulingMakesNothing(t *testing.T) {
	var s schedule
	_, _, _ = s.add(1, 0, Clock())
	s.due(1)
	at := int64(0)
	if n := testing.AllocsPerRun(1000, func() {
		at++
		_, _, _ = s.add(at, 0, NoteOn(0, 60, 1))
		if at%2 == 0 {
			s.due(at)
		}
	}); n != 0 {
		t.Errorf("add and due made %v things a run, want none", n)
	}
}
