package midi

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestPlayOnAPort plays on the output GUNIM_MIDI_OUT names: eight notes
// an eighth apart at 120 beats a minute, with the MIDI clock, all sent
// ahead at once. They should sound evenly.
func TestPlayOnAPort(t *testing.T) {
	name := os.Getenv("GUNIM_MIDI_OUT")
	if name == "" {
		t.Skip("GUNIM_MIDI_OUT names no output to play on")
	}
	o, err := OpenOut(name)
	if err != nil {
		outs, _ := Outputs()
		t.Fatalf("%v; outputs: %q", err, outs)
	}
	defer func() { _ = o.Close() }()
	const eighth = 250 * time.Millisecond
	start := time.Now().Add(200 * time.Millisecond)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(o.Send(start, Program(0, 0)))
	must(o.Send(start, Start()))
	for k := range 8 * 12 {
		must(o.Send(start.Add(time.Duration(k)*eighth/12), Clock()))
	}
	for k, key := range []int{60, 64, 67, 72, 67, 64, 60, 55} {
		at := start.Add(time.Duration(k) * eighth)
		must(o.Send(at, NoteOn(0, key, 100)))
		must(o.Send(at.Add(eighth*3/4), NoteOff(0, key, 64)))
	}
	end := start.Add(8 * eighth)
	must(o.Send(end, Stop()))
	time.Sleep(time.Until(end) + 200*time.Millisecond)
}

// TestHearAPort prints what comes in on the input GUNIM_MIDI_IN names,
// for 20 seconds, and says if the port goes away meanwhile.
func TestHearAPort(t *testing.T) {
	name := os.Getenv("GUNIM_MIDI_IN")
	if name == "" {
		t.Skip("GUNIM_MIDI_IN names no input to hear")
	}
	i, err := OpenIn(name, func(e Event) {
		if e.Kind() != KindClock && e.Kind() != KindActiveSensing {
			t.Logf("%v, stamped %v before f heard it", e.Message, time.Since(e.Time))
		}
	})
	if err != nil {
		ins, _ := Inputs()
		t.Fatalf("%v; inputs: %q", err, ins)
	}
	defer func() { _ = i.Close() }()
	t.Logf("listening to %q for 20 seconds", name)
	end := time.After(20 * time.Second)
	for {
		select {
		case <-Changed():
			ins, _ := Inputs()
			t.Logf("the ports changed: inputs %q", ins)
			if errors.Is(i.Err(), ErrGone) {
				t.Logf("%q went away", name)
				return
			}
		case <-end:
			return
		}
	}
}
