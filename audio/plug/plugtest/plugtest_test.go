package plugtest

import (
	"math"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
)

// heard is an event as an instrument heard it: its frame counted from
// the start, and the block's In.
type heard struct {
	at    int
	kind  plug.EventKind
	note  int32
	hadIn bool
}

// recorder is an instrument that keeps what it hears in room made
// before it plays, and sounds the number of its notes held down.
type recorder struct {
	heard   []heard
	changes []plug.Change
	down    int
	setup   plug.Setup
}

func (r *recorder) Prepare(s plug.Setup) {
	r.setup = s
	r.heard = make([]heard, 0, 128)
	r.changes = make([]plug.Change, 0, 8)
}

func (r *recorder) Process(b *plug.Block) {
	r.changes = append(r.changes, b.Changes...)
	next := 0
	for f := range b.Frames {
		for ; next < len(b.Events) && b.Events[next].Frame <= f; next++ {
			e := b.Events[next]
			r.heard = append(r.heard, heard{int(b.Position.Frame) + e.Frame, e.Kind, e.Note, b.In != nil})
			switch e.Kind {
			case plug.NoteOn:
				r.down++
			case plug.NoteOff:
				r.down--
			case plug.Expression, plug.Pedal:
			}
		}
		for _, ch := range b.Out {
			ch[f] = float32(r.down)
		}
	}
}

func (r *recorder) Latency() int { return 0 }
func (r *recorder) Tail() int    { return 0 }
func (r *recorder) Reset()       { r.down = 0 }

func TestEventsReachAnInstrumentInFrameOrder(t *testing.T) {
	r := &recorder{}
	s := plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 2}
	params := []plug.Param{{ID: 7, Max: 1, Default: 0.25}}
	// Listed out of order, across the blocks' edges.
	out := Play(t, r, s, 256, params,
		plug.Event{Frame: 200, Kind: plug.NoteOff, Note: 2},
		plug.Event{Frame: 10, Kind: plug.NoteOn, Note: 1, Pitch: 60, Velocity: 0.8},
		plug.Event{Frame: 64, Kind: plug.NoteOn, Note: 2, Pitch: 64.5, Velocity: 0.8},
		plug.Event{Frame: 100, Kind: plug.Expression, Note: 2, Expr: plug.Bend, Value: -2},
		plug.Event{Frame: 63, Kind: plug.Pedal, Value: 1},
		plug.Event{Frame: 255, Kind: plug.NoteOff, Note: 1},
	)
	want := []heard{{10, plug.NoteOn, 1, false}, {63, plug.Pedal, 0, false}, {64, plug.NoteOn, 2, false},
		{100, plug.Expression, 2, false}, {200, plug.NoteOff, 2, false}, {255, plug.NoteOff, 1, false}}
	if len(r.heard) != len(want) {
		t.Fatalf("the instrument heard %v, want %v", r.heard, want)
	}
	for i := range want {
		if r.heard[i] != want[i] {
			t.Errorf("event %d was heard as %+v, want %+v", i, r.heard[i], want[i])
		}
	}
	if len(r.changes) != 1 || r.changes[0] != (plug.Change{ID: 7, Value: 0.25}) {
		t.Errorf("the parameters came as %v, want 7 at 0.25 at frame 0", r.changes)
	}
	for _, c := range []struct{ at, down int }{{9, 0}, {10, 1}, {63, 1}, {64, 2}, {199, 2}, {200, 1}, {255, 0}} {
		for ch := range out {
			if got := out[ch][c.at]; got != float32(c.down) {
				t.Errorf("channel %d at frame %d sounds %v, want %d", ch, c.at, got, c.down)
			}
		}
	}
}

func TestPlayingNotesAllocatesNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	events := make([]plug.Event, 0, 96)
	for i := range 32 {
		events = append(events,
			plug.Event{Frame: i * 300, Kind: plug.NoteOn, Note: int32(i), Pitch: 60, Velocity: 1},
			plug.Event{Frame: i*300 + 150, Kind: plug.Expression, Note: int32(i), Expr: plug.Pressure, Value: 0.5},
			plug.Event{Frame: i*300 + 299, Kind: plug.NoteOff, Note: int32(i)})
	}
	r := &recorder{}
	// Only what the playing makes counts, not Play's making of blocks.
	made := AllocsUnder(t, "plugtest.playBlocks", func() {
		Play(t, r, plug.Setup{Rate: 48000, MaxBlock: 128, Channels: 2}, 32*300, nil, events...)
	})
	if made != 0 {
		t.Errorf("playing made %d things over 75 blocks", made)
	}
}

// half is an instrument that writes only the first half of each block.
type half struct{ recorder }

func (h *half) Process(b *plug.Block) {
	for _, ch := range b.Out {
		for f := range b.Frames / 2 {
			ch[f] = 0
		}
	}
}

func TestPlayShowsFramesLeftUnwritten(t *testing.T) {
	out := Play(t, &half{}, plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1}, 64, nil)
	if out[0][0] != 0 || !math.IsNaN(float64(out[0][63])) {
		t.Errorf("the frames run %v to %v, want 0 written and NaN left", out[0][0], out[0][63])
	}
}
