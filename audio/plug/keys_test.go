package plug

import (
	"sync"
	"testing"
)

func TestAKeyTraceKeepsTheKeysDownInEachRun(t *testing.T) {
	var kt KeyTrace
	if _, ok := kt.Get(0); ok {
		t.Fatal("a trace of nothing played has keys")
	}
	at := int64(1000 * TraceFrames)
	// Block one: C4 from frame 10, and E4 pressed and let go inside the
	// second run.
	kt.Play(at, 4*TraceFrames, []Event{
		{Frame: 10, Kind: NoteOn, Note: 1, Pitch: 60},
		{Frame: TraceFrames + 3, Kind: NoteOn, Note: 2, Pitch: 63.6},
		{Frame: TraceFrames + 9, Kind: NoteOff, Note: 2},
		{Frame: 3*TraceFrames + 1, Kind: NoteOff, Note: 1},
	})
	want := func(f int64, keys ...int) {
		t.Helper()
		got, ok := kt.Get(f)
		if !ok {
			t.Fatalf("frame %d reads as not played", f)
		}
		var k Keys
		for _, n := range keys {
			k = k.With(n)
		}
		if got != k {
			t.Errorf("frame %d reads %v, want keys %v", f, got, keys)
		}
	}
	want(at, 60)
	want(at+TraceFrames+200, 60, 64)
	want(at+2*TraceFrames, 60)
	want(at+3*TraceFrames+100, 60)
	// A run of its own after the note ended has no keys.
	kt.Play(at+4*TraceFrames, TraceFrames, nil)
	want(at + 4*TraceFrames)
	if _, ok := kt.Get(at + 5*TraceFrames); ok {
		t.Error("a run not yet played has keys")
	}
	if _, ok := kt.Get(-1); ok {
		t.Error("a frame before the first has keys")
	}
}

func TestAKeyTraceLetsAKeyGoOnceEveryNoteOnItEnds(t *testing.T) {
	var kt KeyTrace
	// Two notes on G4, from two lanes, and a block that starts mid-run.
	kt.Play(5, 20, []Event{
		{Frame: 0, Kind: NoteOn, Note: 7, Pitch: 67},
		{Frame: 1, Kind: NoteOn, Note: 8, Pitch: 67},
	})
	kt.Play(25, 2*TraceFrames, []Event{{Frame: 0, Kind: NoteOff, Note: 7}})
	if k, _ := kt.Get(TraceFrames + 30); !k.Has(67) {
		t.Error("G4 let go while a second note held it")
	}
	kt.Play(25+2*TraceFrames, 2*TraceFrames, []Event{{Frame: 0, Kind: NoteOff, Note: 8}})
	if k, _ := kt.Get(4*TraceFrames + 30); k.Has(67) {
		t.Error("G4 is held after both its notes ended")
	}
	// A NoteOff for a note never started, and a key off the keyboard,
	// change nothing.
	kt.Play(25+4*TraceFrames, TraceFrames, []Event{
		{Frame: 0, Kind: NoteOff, Note: 99},
		{Frame: 1, Kind: NoteOn, Note: 3, Pitch: 140},
		{Frame: 2, Kind: NoteOff, Note: 3},
	})
	if k, ok := kt.Get(25 + 4*TraceFrames); !ok || k != (Keys{}) {
		t.Errorf("stray events read as keys %v", k)
	}
}

func TestAKeyTraceResetLetsGoOfEveryKey(t *testing.T) {
	var kt KeyTrace
	kt.Play(0, TraceFrames, []Event{{Kind: NoteOn, Note: 1, Pitch: 48}})
	kt.Reset()
	kt.Play(TraceFrames, TraceFrames, nil)
	if k, _ := kt.Get(TraceFrames); k.Has(48) {
		t.Error("a key stays down after a reset")
	}
	if k, _ := kt.Get(0); !k.Has(48) {
		t.Error("the run played before the reset lost its key")
	}
	// A note started again after the reset sounds again.
	kt.Play(2*TraceFrames, TraceFrames, []Event{{Kind: NoteOn, Note: 1, Pitch: 48}})
	if k, _ := kt.Get(2 * TraceFrames); !k.Has(48) {
		t.Error("a note started after the reset reads as up")
	}
}

func TestAKeyTraceForgetsARunARingAgo(t *testing.T) {
	var kt KeyTrace
	kt.Play(0, TraceFrames, []Event{{Kind: NoteOn, Note: 1, Pitch: 50}})
	kt.Play(keyRuns*TraceFrames, TraceFrames, nil)
	if _, ok := kt.Get(0); ok {
		t.Error("a run a ring ago reads the keys of the run now in its place")
	}
}

func TestAKeyTracePlaysWithoutAllocating(t *testing.T) {
	var kt KeyTrace
	ev := []Event{{Frame: 3, Kind: NoteOn, Note: 1, Pitch: 60}, {Frame: 90, Kind: NoteOff, Note: 1}}
	at := int64(0)
	if n := testing.AllocsPerRun(100, func() {
		kt.Play(at, 512, ev)
		at += 512
	}); n != 0 {
		t.Errorf("Play allocates %v times a block", n)
	}
}

// TestAKeyTraceReadsWhilePlaying runs a reader beside the player, for
// the race detector, and checks each read is a set the player kept.
func TestAKeyTraceReadsWhilePlaying(t *testing.T) {
	var kt KeyTrace
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for f := int64(0); f < 64*TraceFrames; f += TraceFrames {
				if k, ok := kt.Get(f); ok && k != (Keys{}) && !k.Has(60) && !k.Has(100) {
					t.Errorf("frame %d reads keys %v, which no note pressed", f, k)
					return
				}
			}
		}
	}()
	for i := range 2000 {
		at := int64(i%64) * TraceFrames
		key := 60.0
		if i%2 == 1 {
			key = 100
		}
		kt.Play(at, TraceFrames, []Event{{Kind: NoteOn, Note: int32(i), Pitch: key}, {Frame: 5, Kind: NoteOff, Note: int32(i)}})
	}
	close(stop)
	wg.Wait()
}
