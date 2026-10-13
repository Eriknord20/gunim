package vst3

import (
	"bytes"
	"math"
	"slices"
	"testing"
	"unsafe"

	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugtest"
)

// stereoBlock returns a block of n frames with a stereo Out, filled with
// NaN, so a frame left unwritten shows.
func stereoBlock(n int) *plug.Block {
	b := &plug.Block{Frames: n, Out: [][]float32{make([]float32, n), make([]float32, n)}}
	for _, ch := range b.Out {
		for i := range ch {
			ch[i] = float32(math.NaN())
		}
	}
	return b
}

// TestNotesReachAPluginAsItsEvents plays notes, expression and a pedal
// into a fake instrument: each note on a channel of its group's, its
// pitch a key and a tuning in cents, pressure as poly pressure, and
// bend, mod and pedal as the parameters the plugin maps those MIDI
// controllers to, at their frames. A reset ends the notes sounding and
// rests the controllers in the next block.
func TestNotesReachAPluginAsItsEvents(t *testing.T) {
	f := fakeSynth()
	p, err := f.open(t, Config{Rate: 48000, Block: 256})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if !p.Notes() || p.Bends() {
		t.Fatalf("the fake takes notes %v, and bends them %v", p.Notes(), p.Bends())
	}
	b := stereoBlock(128)
	b.Events = []plug.Event{
		{Frame: 10, Kind: plug.NoteOn, Note: 7, Group: 3, Pitch: 60.25, Velocity: 0.5},
		{Frame: 20, Kind: plug.NoteOn, Note: 8, Group: 4, Pitch: 64, Velocity: 1},
		{Frame: 30, Kind: plug.Expression, Note: 7, Expr: plug.Bend, Value: 1},
		{Frame: 40, Kind: plug.Expression, Note: 8, Expr: plug.Pressure, Value: 0.3},
		{Frame: 50, Kind: plug.Expression, Note: 8, Expr: plug.Mod, Value: 0.6},
		{Frame: 60, Kind: plug.Pedal, Group: 4, Value: 1},
		{Frame: 70, Kind: plug.NoteOff, Note: 7, Velocity: 0.25},
		{Frame: 80, Kind: plug.NoteOff, Note: 99},
		{Frame: 90, Kind: plug.Expression, Note: 99, Expr: plug.Pressure, Value: 1},
	}
	p.ProcessBlock(b)
	got := f.last(t)
	if len(got.events) != 4 {
		t.Fatalf("the fake heard %d events, want 4: %+v", len(got.events), got.events)
	}
	on, on2, press, off := got.events[0], got.events[1], got.events[2], got.events[3]
	if on.kind != evNoteOn || on.offset != 10 || on.channel() != 0 || on.pitch() != 60 || on.f32(4) != 25 ||
		on.f32(8) != 0.5 || on.i32(16) != 7 {
		t.Errorf("the first note came as kind %d at %d, channel %d, key %d, %v cents, velocity %v, ID %d",
			on.kind, on.offset, on.channel(), on.pitch(), on.f32(4), on.f32(8), on.i32(16))
	}
	if on2.kind != evNoteOn || on2.offset != 20 || on2.channel() != 1 || on2.pitch() != 64 || on2.i32(16) != 8 {
		t.Errorf("the second note, of another group, came at %d on channel %d, key %d", on2.offset, on2.channel(), on2.pitch())
	}
	if press.kind != evPolyPressure || press.offset != 40 || press.channel() != 1 || press.pitch() != 64 ||
		math.Abs(float64(press.f32(4))-0.3) > 1e-6 || press.i32(8) != 8 {
		t.Errorf("the pressure came as kind %d at %d, channel %d, key %d, %v, note %d", press.kind, press.offset,
			press.channel(), press.pitch(), press.f32(4), press.i32(8))
	}
	if off.kind != evNoteOff || off.offset != 70 || off.channel() != 0 || off.pitch() != 60 || off.f32(4) != 0.25 ||
		off.i32(8) != 7 || off.f32(12) != 25 {
		t.Errorf("the note off came as kind %d at %d, channel %d, key %d, velocity %v, note %d, %v cents",
			off.kind, off.offset, off.channel(), off.pitch(), off.f32(4), off.i32(8), off.f32(12))
	}
	want := map[uint32][]point{
		900:  {{30, 0.75}},
		2001: {{50, 0.6}},
		1001: {{60, 1}},
	}
	for id, pts := range want {
		if !slices.Equal(got.changes[id], pts) {
			t.Errorf("parameter %d changed as %v, want %v", id, got.changes[id], pts)
		}
	}
	if len(got.changes) != len(want) {
		t.Errorf("the fake heard changes of %d parameters, want %d: %v", len(got.changes), len(want), got.changes)
	}

	// A reset, on the audio's goroutine: processing stops and starts,
	// and the next block ends note 8 and rests what moved.
	p.resetNow()
	if f.processing[len(f.processing)-2] || !f.processing[len(f.processing)-1] {
		t.Fatalf("a reset set processing %v", f.processing)
	}
	p.ProcessBlock(stereoBlock(64))
	got = f.last(t)
	if len(got.events) != 1 || got.events[0].kind != evNoteOff || got.events[0].i32(8) != 8 || got.events[0].offset != 0 ||
		got.events[0].channel() != 1 {
		t.Fatalf("after a reset, the fake heard %+v, want note 8 let go at frame 0 on channel 1", got.events)
	}
	rest := map[uint32][]point{900: {{0, 0.5}}, 2001: {{0, 0}}, 1001: {{0, 0}}}
	for id, pts := range rest {
		if !slices.Equal(got.changes[id], pts) {
			t.Errorf("after a reset, parameter %d changed as %v, want %v", id, got.changes[id], pts)
		}
	}
	// And the block after holds nothing more.
	p.ProcessBlock(stereoBlock(64))
	if got = f.last(t); len(got.events) != 0 || len(got.changes) != 0 {
		t.Fatalf("two blocks after a reset, the fake heard %+v and %v", got.events, got.changes)
	}
}

// TestABendIsANotesOwnWherePluginsTakeIt bends a note of a plugin that
// takes VST3's tuning expression: the note alone bends, from its key,
// so the cents its key was tuned by count in. 60.5 plays as key 61
// tuned 50 cents down, so a bend of 12 tunes it 11.5 semitones up.
func TestABendIsANotesOwnWherePluginsTakeIt(t *testing.T) {
	f := fakeSynth()
	f.tuning = true
	p, err := f.open(t, Config{Rate: 48000, Block: 256})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if !p.Bends() {
		t.Fatal("the fake bends notes of its own, and the plugin says it does not")
	}
	b := stereoBlock(64)
	b.Events = []plug.Event{
		{Frame: 0, Kind: plug.NoteOn, Note: 1, Pitch: 60.5, Velocity: 1},
		{Frame: 5, Kind: plug.Expression, Note: 1, Expr: plug.Bend, Value: 12},
	}
	p.ProcessBlock(b)
	got := f.last(t)
	if len(got.events) != 2 || len(got.changes) != 0 {
		t.Fatalf("the fake heard %+v and changes %v", got.events, got.changes)
	}
	e := got.events[1]
	v := math.Float64frombits(uint64(uint32(e.i32(8))) | uint64(uint32(e.i32(12)))<<32)
	if e.kind != evNoteExpression || e.offset != 5 || uint32(e.i32(0)) != noteExprTuning || e.i32(4) != 1 ||
		math.Abs(v-(0.5+11.5/240)) > 1e-9 {
		t.Fatalf("the bend came as kind %d at %d, type %d, note %d, value %v", e.kind, e.offset, e.i32(0), e.i32(4), v)
	}
}

// TestBusesCarryABlocksPorts runs a fake instrument's buses through a
// block's ports: its aux input from the block's, or silence where
// unconnected, as an instrument has no sound of its own going in; its
// main output into Out and its aux output into the block's, a mono bus
// into both of two channels; and silence past the frames it processes.
func TestBusesCarryABlocksPorts(t *testing.T) {
	f := fakeSynth()
	p, err := f.open(t, Config{Rate: 48000, Block: 64})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	in, out := p.Buses()
	if len(in) != 1 || in[0].Main || in[0].Name != "Sidechain" || in[0].Channels != 2 ||
		len(out) != 2 || !out[0].Main || out[0].Channels != 2 || out[1].Name != "Kick" || out[1].Channels != 1 {
		t.Fatalf("the fake's buses are %+v in and %+v out", in, out)
	}
	for _, k := range [][3]int{{mediaAudio, dirInput, 0}, {mediaAudio, dirOutput, 0}, {mediaAudio, dirOutput, 1},
		{mediaEvent, dirInput, 0}} {
		if !f.active[k] {
			t.Errorf("bus %v is off", k)
		}
	}
	side := [][]float32{make([]float32, 100), make([]float32, 100)}
	for i := range side[0] {
		side[0][i], side[1][i] = 0.25, -0.5
	}
	b := stereoBlock(100)
	b.AuxIn = [][][]float32{side}
	b.AuxOut = [][][]float32{{make([]float32, 100), make([]float32, 100)}}
	p.ProcessBlock(b)
	got := f.last(t)
	if got.frames != 64 || !slices.Equal(got.ins[0], []float32{0.25, -0.5}) {
		t.Fatalf("the fake processed %d frames, with %v going in", got.frames, got.ins)
	}
	for i := range 100 {
		want := [2]float32{1, 2}
		aux := float32(11)
		if i >= 64 {
			want, aux = [2]float32{}, 0
		}
		if b.Out[0][i] != want[0] || b.Out[1][i] != want[1] || b.AuxOut[0][0][i] != aux || b.AuxOut[0][1][i] != aux {
			t.Fatalf("frame %d came out as %v %v, aux %v %v", i, b.Out[0][i], b.Out[1][i], b.AuxOut[0][0][i], b.AuxOut[0][1][i])
		}
	}
	// Unconnected, the sidechain hears silence.
	p.ProcessBlock(stereoBlock(32))
	if got = f.last(t); !slices.Equal(got.ins[0], []float32{0, 0}) {
		t.Fatalf("the unconnected sidechain heard %v", got.ins[0])
	}
}

// TestAnEffectsSidechainKeysOnWhatItChanges runs a fake effect in mono:
// its main buses take one channel, its sound in goes out on both of the
// block's, and its sidechain, unconnected, hears the main input.
func TestAnEffectsSidechainKeysOnWhatItChanges(t *testing.T) {
	f := &fakePlugin{ins: []fakeBus{{name: "In", channels: 2}, {name: "Key", channels: 1, aux: true}},
		outs: []fakeBus{{name: "Out", channels: 2}}}
	p, err := f.open(t, Config{Rate: 48000, Block: 64, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	in, out := p.Buses()
	if in[0].Channels != 1 || out[0].Channels != 1 || !in[0].Main || in[1].Main {
		t.Fatalf("in mono, the fake's buses are %+v in and %+v out", in, out)
	}
	b := stereoBlock(16)
	b.In = [][]float32{slices.Repeat([]float32{0.125}, 16)}
	p.ProcessBlock(b)
	got := f.last(t)
	if !slices.Equal(got.ins[0], []float32{0.125}) || !slices.Equal(got.ins[1], []float32{0.125}) {
		t.Fatalf("the fake heard %v", got.ins)
	}
	if b.Out[0][3] != 0.25 || b.Out[1][3] != 0.25 {
		t.Fatalf("the sound came out as %v and %v", b.Out[0][3], b.Out[1][3])
	}
	b.AuxIn = [][][]float32{{slices.Repeat([]float32{-1}, 16)}}
	p.ProcessBlock(b)
	if got = f.last(t); got.ins[1][0] != -1 {
		t.Fatalf("the connected sidechain heard %v", got.ins[1])
	}
}

// TestAVST3PluginRunsBehindPlugsInterfaces runs the fake as package plug
// runs a plugin: described, made for views without loading, made to
// sound as it is prepared, of the state given before, through a plug
// host's blocks. A state handed back as the plugin gave it sets
// nothing; another sets it.
func TestAVST3PluginRunsBehindPlugsInterfaces(t *testing.T) {
	f := fakeSynth()
	f.latency, f.tail = 64, 1000
	f.state = []byte("first")
	opened := 0
	c := Class{ID: uid{1, 2, 3}, Name: "Fake", Category: "Audio Module Class", SubCategories: "Instrument|Synth"}
	open := func(cfg Config) (*Plugin, error) {
		opened++
		return f.open(t, cfg)
	}
	d, err := describe(c, open)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != PlugID(c) || !d.Instrument() || !d.Notes || len(d.AuxIn) != 1 || d.AuxIn[0] != (plug.AudioPort{Name: "Sidechain", Channels: 2}) ||
		len(d.AuxOut) != 1 || d.AuxOut[0] != (plug.AudioPort{Name: "Kick", Channels: 1}) {
		t.Fatalf("the fake is described as %+v", d)
	}
	if id, ok := ClassID(d.ID); !ok || id != c.IDString() {
		t.Fatalf("the class of %q is %q, %v", d.ID, id, ok)
	}
	opened = 0

	// One for views: it loads nothing, and keeps the state it is given.
	views := d.New(nil)
	if err := views.SetState([]byte("kept")); err != nil {
		t.Fatal(err)
	}
	if string(views.State()) != "kept" || opened != 0 {
		t.Fatalf("one made for views has state %q, and loaded %d plugins", views.State(), opened)
	}
	views.Close()

	pl := d.New(nil)
	t.Cleanup(pl.Close)
	f.state = nil
	st := append([]byte(stateMagic), 5, 0, 0, 0)
	st = append(append(st, "saved"...), 0, 0, 0, 0)
	if err := pl.SetState(st); err != nil {
		t.Fatal(err)
	}
	pr := pl.Processor()
	res := plugtest.Drive(t, &d, pr, plug.Setup{Rate: 48000, MaxBlock: 128, Channels: 2}, 300,
		plugtest.Ports{Events: []plug.Event{{Frame: 130, Kind: plug.NoteOn, Note: 1, Pitch: 60, Velocity: 1}}})
	if opened != 1 || string(f.state) != "saved" || f.setStates != 1 {
		t.Fatalf("preparing loaded %d plugins, of state %q, set %d times", opened, f.state, f.setStates)
	}
	for i := range 300 {
		if res.Out[0][i] != 1 || res.Out[1][i] != 2 || res.AuxOut[0][0][i] != 11 {
			t.Fatalf("frame %d came out as %v %v, aux %v", i, res.Out[0][i], res.Out[1][i], res.AuxOut[0][0][i])
		}
	}
	if blk := f.blocks[len(f.blocks)-2]; len(blk.events) != 1 || blk.events[0].offset != 2 {
		t.Fatalf("the note landed as %+v in the block of frames 128 on", blk.events)
	}
	if pr.Latency() != 64 || pr.Tail() != 1000 {
		t.Fatalf("latency %d and tail %d, want 64 and 1000", pr.Latency(), pr.Tail())
	}

	// The state it gives, handed back, sets nothing; another sets it.
	got := pl.State()
	if !bytes.Contains(got, []byte("saved")) {
		t.Fatalf("the state is %q", got)
	}
	_ = pl.SetState(got)
	if f.setStates != 1 {
		t.Fatalf("its own state handed back set the state again: %d sets", f.setStates)
	}
	other := bytes.Replace(got, []byte("saved"), []byte("other"), 1)
	if err := pl.SetState(other); err != nil || f.setStates != 2 || string(f.state) != "other" {
		t.Fatalf("another state set %d times, to %q: %v", f.setStates, f.state, err)
	}

	// Prepared again at another rate, it plays on, the same plugin.
	pr.Prepare(plug.Setup{Rate: 44100, MaxBlock: 64, Channels: 2})
	if opened != 1 {
		t.Fatalf("preparing again loaded %d plugins", opened)
	}
	b := stereoBlock(64)
	pr.Process(b)
	if b.Out[1][63] != 2 {
		t.Fatalf("prepared again, it played %v", b.Out[1][63])
	}
}

// TestAnEditorsChangesAreCounted makes changes in a fake's editor, as a
// plugin calls its handler: each change counts, a gesture shows while
// it lasts, and a plugin saying its state changed counts too; what it
// tells as the host sets its state counts not.
func TestAnEditorsChangesAreCounted(t *testing.T) {
	f := fakeSynth()
	p, err := f.open(t, Config{Rate: 48000, Block: 64})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	h := f.handler
	if h == 0 {
		t.Fatal("the fake was given no handler")
	}
	call(h, 3, 5)
	if !p.Editing() {
		t.Fatal("a gesture under way shows none")
	}
	callFloat(h, 4, 5, 0.75)
	call(h, 5, 5)
	if p.Editing() || p.Edits() != 1 {
		t.Fatalf("after one change, editing %v, %d edits", p.Editing(), p.Edits())
	}
	h2 := query(h, iidComponentHandler2)
	if h2 == 0 {
		t.Fatal("the handler has no second half")
	}
	defer release(h2)
	call(h2, 3, 1)
	call(h2, 5)
	if p.Edits() != 2 || !p.Editing() {
		t.Fatalf("after the state was said dirty and a group edit started, %d edits, editing %v", p.Edits(), p.Editing())
	}
	call(h2, 6)
	call(h, 6, restartParamValues)
	if p.Edits() != 3 || p.Editing() {
		t.Fatalf("after the values were said changed, %d edits, editing %v", p.Edits(), p.Editing())
	}
	// What it tells as the host sets its state is no edit.
	f.tells = true
	st, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetState(st); err != nil || p.Edits() != 3 || f.setStates != 1 {
		t.Fatalf("setting its state made %d edits, of %d states set: %v", p.Edits(), f.setStates, err)
	}
}

// TestEventsAreLaidOutAsVST3s checks the event's layout against the
// interfaces': 48 bytes, the union at 24.
func TestEventsAreLaidOutAsVST3s(t *testing.T) {
	var e vstEvent
	if unsafe.Sizeof(e) != 48 || unsafe.Offsetof(e.data) != 24 || unsafe.Offsetof(e.kind) != 18 {
		t.Fatalf("an event is %d bytes, its data at %d", unsafe.Sizeof(e), unsafe.Offsetof(e.data))
	}
	var info noteExpressionInfo
	if unsafe.Sizeof(info) != 816 || unsafe.Offsetof(info.def) != 776 || unsafe.Offsetof(info.paramID) != 808 {
		t.Fatalf("a note expression's info is %d bytes, its value at %d", unsafe.Sizeof(info), unsafe.Offsetof(info.def))
	}
}

// TestNoStateIsTheStateAPluginStartedWith edits a plugin whose state
// holds more than its parameters, then sets no state, as an undo past
// its first edit does: it is back at the state it started with.
func TestNoStateIsTheStateAPluginStartedWith(t *testing.T) {
	f := fakeSynth()
	f.state = []byte("as made")
	c := Class{ID: uid{9}, Name: "Fake", Category: "Audio Module Class", SubCategories: "Instrument|Synth"}
	d, err := describe(c, func(cfg Config) (*Plugin, error) { return f.open(t, cfg) })
	if err != nil {
		t.Fatal(err)
	}
	pl := d.New(nil)
	t.Cleanup(pl.Close)
	pl.Processor().Prepare(plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 2})
	f.mu.Lock()
	f.state = []byte("edited")
	f.mu.Unlock()
	edited := pl.State()
	if err := pl.SetState(nil); err != nil || string(f.state) != "as made" {
		t.Fatalf("no state left the plugin at %q: %v", f.state, err)
	}
	if err := pl.SetState(edited); err != nil || string(f.state) != "edited" {
		t.Fatalf("redone, the plugin is at %q: %v", f.state, err)
	}
}
