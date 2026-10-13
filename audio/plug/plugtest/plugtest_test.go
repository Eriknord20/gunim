package plugtest

import (
	"math"
	"testing"
	"time"

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
	// AllocsUnder counts what playBlocks makes.
	made := AllocsUnder(t, "plugtest.playBlocks", func() {
		Play(t, r, plug.Setup{Rate: 48000, MaxBlock: 128, Channels: 2}, 32*300, nil, events...)
	})
	if made != 0 {
		t.Errorf("playing made %d things over 75 blocks", made)
	}
}

// half is an instrument that writes every frame of its first block, and
// the first half of each block after.
type half struct {
	recorder
	blocks int
}

func (h *half) Process(b *plug.Block) {
	n := b.Frames
	if h.blocks > 0 {
		n /= 2
	}
	h.blocks++
	for _, ch := range b.Out {
		for f := range n {
			ch[f] = 1
		}
	}
}

func TestPlayShowsFramesLeftUnwritten(t *testing.T) {
	out := Play(t, &half{}, plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1}, 192, nil)
	for f, v := range out[0] {
		// The second and third blocks leave their last 32 frames.
		left := f >= 64 && f%64 >= 32
		if math.IsNaN(float64(v)) != left {
			t.Errorf("frame %d is %v, want NaN %v", f, v, left)
		}
	}
}

// patched is a plugin with a port of each kind, which shows what reaches
// them: it turns its sound by its depth, a control input, and puts the
// depth out as it heard it on a control output; its key, an aux input of
// one channel, comes out twice on an aux output of as many channels as
// the main sound.
var patched = plug.Descriptor{
	ID: "test.patched", Name: "Patched",
	AuxIn:      []plug.AudioPort{{Name: "Key", Channels: 1}},
	AuxOut:     []plug.AudioPort{{Name: "Twice"}},
	ControlIn:  []plug.ControlPort{{Name: "Depth", Max: 1, Default: 0.5}},
	ControlOut: []plug.ControlPort{{Name: "Depth heard", Max: 1}},
	New:        func(plug.Host) plug.Plugin { return nil },
}

type patch struct{ recorder }

func (p *patch) Process(b *plug.Block) {
	depth, key := b.ControlInput(0), b.AuxInput(0)
	heard, twice := b.ControlOutput(0), b.AuxOutput(0)
	for f := range b.Frames {
		d := float32(patched.ControlIn[0].Default)
		if depth != nil {
			d = depth[f]
		}
		for c := range b.Out {
			b.Out[c][f] = b.In[c][f] * d
		}
		if heard != nil {
			heard[f] = d
		}
		for _, ch := range twice {
			ch[f] = 0
			if key != nil {
				ch[f] = 2 * key[0][f]
			}
		}
	}
}

func TestControlReachesAPluginEveryFrame(t *testing.T) {
	const frames = 300 // four blocks of 64 and one of 44
	in, depth, key := make([]float32, frames), make([]float32, frames), make([]float32, frames)
	for f := range frames {
		in[f] = 1 + float32(f%7)
		depth[f] = float32(f) / frames
		key[f] = float32(f%5) / 10
	}
	s := plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 2}
	r := Drive(t, &patched, &patch{}, s, frames, Ports{In: [][]float32{in, in}, AuxIn: [][][]float32{{key}},
		ControlIn: [][]float32{depth}})
	for f := range frames {
		if got := r.ControlOut[0][f]; got != depth[f] {
			t.Fatalf("at frame %d the plugin heard a depth of %v, want %v", f, got, depth[f])
		}
		for c := range 2 {
			if got, want := r.Out[c][f], in[f]*depth[f]; got != want {
				t.Fatalf("at frame %d channel %d comes out %v, want %v", f, c, got, want)
			}
			if got := r.AuxOut[0][c][f]; got != 2*key[f] {
				t.Fatalf("at frame %d the key comes out on channel %d as %v, want %v", f, c, got, 2*key[f])
			}
		}
	}
}

func TestAnInputLeftUnconnectedReadsAsNil(t *testing.T) {
	in := level(128, 1)
	// The control input stands at its Default, and the key is silent.
	r := Drive(t, &patched, &patch{}, plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1}, 128, Ports{In: [][]float32{in}})
	for f := range 128 {
		if r.Out[0][f] != 0.5 || r.ControlOut[0][f] != 0.5 || r.AuxOut[0][0][f] != 0 {
			t.Fatalf("unconnected, at frame %d the plugin puts out %v, %v and %v, want 0.5, 0.5 and 0",
				f, r.Out[0][f], r.ControlOut[0][f], r.AuxOut[0][0][f])
		}
	}
	// A nil port in the list leaves it unconnected too.
	r = Drive(t, &patched, &patch{}, plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1}, 128,
		Ports{In: [][]float32{in}, AuxIn: [][][]float32{nil}, ControlIn: [][]float32{nil}})
	if r.Out[0][100] != 0.5 || r.AuxOut[0][0][100] != 0 {
		t.Errorf("with nil ports, the plugin puts out %v and %v, want 0.5 and 0", r.Out[0][100], r.AuxOut[0][0][100])
	}
}

func TestDrivingPortsAllocatesNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	in := level(4800, 1)
	made := AllocsUnder(t, "plugtest.playBlocks", func() {
		Drive(t, &patched, &patch{}, plug.Setup{Rate: 48000, MaxBlock: 128, Channels: 2}, 4800,
			Ports{In: [][]float32{in, in}, AuxIn: [][][]float32{{in}}, ControlIn: [][]float32{in}})
	})
	if made != 0 {
		t.Errorf("driving the ports made %d things over 38 blocks", made)
	}
}

// level returns n frames at v.
func level(n int, v float32) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// news is news of a note as an instrument heard it: the block's first
// frame, and the note's landing counted from the start.
type news struct {
	block int
	kind  plug.UpcomingKind
	note  int32
	lands int
	pitch float64
}

// listener is an instrument that keeps the news it hears, and the frame
// each NoteOn lands on, in room made before it plays.
type listener struct {
	recorder
	news []news
	ons  []heard
}

func (l *listener) Prepare(s plug.Setup) {
	l.recorder.Prepare(s)
	l.news = make([]news, 0, 64)
	l.ons = make([]heard, 0, 64)
}

func (l *listener) Process(b *plug.Block) {
	at := int(b.Position.Frame)
	for _, u := range b.Upcoming {
		lands := at + u.Frame
		if u.Kind == plug.Removed {
			lands = -1
		}
		l.news = append(l.news, news{at, u.Kind, u.Note, lands, u.Pitch})
	}
	for _, e := range b.Events {
		if e.Kind == plug.NoteOn {
			l.ons = append(l.ons, heard{at: at + e.Frame, kind: e.Kind, note: e.Note})
		}
	}
	for _, ch := range b.Out {
		clear(ch[:b.Frames])
	}
}

func TestNewsReachesTheBlockItIsSentInAheadOfItsNote(t *testing.T) {
	s := plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1}
	notes := []plug.Event{
		{Frame: 300, Kind: plug.NoteOn, Note: 1, Pitch: 60, Velocity: 0.5},
		// Within the window of the start, so its news comes in the first
		// block.
		{Frame: 70, Kind: plug.NoteOn, Note: 2, Pitch: 62, Velocity: 0.5},
		// In the first block itself: no block before it tells of it.
		{Frame: 30, Kind: plug.NoteOn, Note: 3, Pitch: 64, Velocity: 0.5},
		{Frame: 400, Kind: plug.NoteOff, Note: 1},
	}
	ahead := Announce(s, 100, notes...)
	if len(ahead) != 2 || ahead[0].At != 200 || ahead[1].At != 0 {
		t.Fatalf("Announce gave %+v, want news of note 1 at 200 and note 2 at 0", ahead)
	}
	// An edit at frame 250 moves note 1 to 320 and a fifth up; another,
	// at 260, takes back note 4, told of at 100.
	ahead = append(ahead,
		Ahead{At: 250, Upcoming: plug.Upcoming{Kind: plug.Moved, Frame: 320, Note: 1, Pitch: 67, Velocity: 0.5}},
		Ahead{At: 100, Upcoming: plug.Upcoming{Kind: plug.Coming, Frame: 450, Note: 4, Pitch: 48, Velocity: 1}},
		Ahead{At: 260, Upcoming: plug.Upcoming{Kind: plug.Removed, Frame: 450, Note: 4}})
	notes[0].Frame = 320
	l := &listener{}
	Drive(t, &plug.Descriptor{Notes: true, LookAhead: time.Second}, l, s, 512, Ports{Events: notes, Upcoming: ahead})
	want := []news{
		{0, plug.Coming, 2, 70, 62},
		{64, plug.Coming, 4, 450, 48},
		{192, plug.Coming, 1, 300, 60},
		{192, plug.Moved, 1, 320, 67},
		{256, plug.Removed, 4, -1, 0},
	}
	if len(l.news) != len(want) {
		t.Fatalf("the instrument heard %+v, want %+v", l.news, want)
	}
	for i := range want {
		if l.news[i] != want[i] {
			t.Errorf("news %d was heard as %+v, want %+v", i, l.news[i], want[i])
		}
	}
	// Each note lands where its news said it would, last.
	for _, on := range l.ons {
		for i := len(l.news) - 1; i >= 0; i-- {
			if n := l.news[i]; n.note == on.note {
				if n.lands != on.at || n.block >= on.at {
					t.Errorf("note %d landed at %d, as news heard in the block at %d put it at %d", on.note, on.at, n.block, n.lands)
				}
				break
			}
		}
	}
}

func TestHearingNewsAllocatesNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	s := plug.Setup{Rate: 48000, MaxBlock: 128, Channels: 2}
	events := make([]plug.Event, 0, 64)
	for i := range 32 {
		events = append(events,
			plug.Event{Frame: 200 + i*300, Kind: plug.NoteOn, Note: int32(i + 1), Pitch: 60, Velocity: 1},
			plug.Event{Frame: 200 + i*300 + 299, Kind: plug.NoteOff, Note: int32(i + 1)})
	}
	ahead := Announce(s, 480, events...)
	made := AllocsUnder(t, "plugtest.playBlocks", func() {
		Drive(t, &plug.Descriptor{Notes: true, LookAhead: 10 * time.Millisecond}, &listener{}, s, 32*300+200,
			Ports{Events: events, Upcoming: ahead})
	})
	if made != 0 {
		t.Errorf("hearing news made %d things", made)
	}
}

// ramp is a generator whose one control output counts the frames from
// the start, whose second holds the tempo it was given, and whose third
// the beat its block starts on.
var ramp = plug.Descriptor{
	ID: "test.ramp", Name: "Ramp", Kind: "Generator|LFO",
	ControlOut: []plug.ControlPort{{Name: "Frame", Max: 1e6}, {Name: "Tempo", Max: 999}, {Name: "Beat", Max: 999}},
	New:        func(plug.Host) plug.Plugin { return nil },
}

type ramper struct {
	recorder
	sawSound bool
}

func (r *ramper) Process(b *plug.Block) {
	if b.In != nil || b.Out != nil {
		r.sawSound = true
	}
	frame, tempo, beat := b.ControlOutput(0), b.ControlOutput(1), b.ControlOutput(2)
	for f := range b.Frames {
		frame[f] = float32(b.Position.Frame + int64(f))
		tempo[f] = float32(b.Position.Tempo)
		beat[f] = float32(b.Position.Beat)
	}
}

func TestAGeneratorRunsWithoutSound(t *testing.T) {
	r := &ramper{}
	res := Drive(t, &ramp, r, plug.Setup{Rate: 48000, MaxBlock: 64}, 200, Ports{Tempo: 120})
	if r.sawSound || res.Out != nil {
		t.Errorf("the generator was given sound, or gave some: %v", res.Out)
	}
	for f := range 200 {
		if res.ControlOut[0][f] != float32(f) || res.ControlOut[1][f] != 120 {
			t.Fatalf("at frame %d the generator put out %v and %v, want %d and 120",
				f, res.ControlOut[0][f], res.ControlOut[1][f], f)
		}
		// Two beats a second: the block at frame 192 starts on beat 0.008.
		if want := float32(f/64*64) / 24000; res.ControlOut[2][f] != want {
			t.Fatalf("at frame %d the block starts on beat %v, want %v", f, res.ControlOut[2][f], want)
		}
	}
}
