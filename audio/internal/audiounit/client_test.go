//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64)

package audiounit

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"unsafe"
)

// The test devices run at 50 kHz, so a frame is 20 µs, and a buffer of
// 64 frames 1.28 ms: whole nanoseconds.
const (
	testRate  = 50000
	frameNs   = 20000
	bufFrames = 64
	period    = bufFrames * frameNs
	// start is the host time of the first buffer.
	start = 1_000_000_000
)

// speakers is an output device of two channels, with 100 frames of
// latency, 20 of its stream's and 24 of safety offset.
var speakers = fakeDevice{id: 10, name: "Speakers", rate: testRate, outs: 2, buffer: bufFrames,
	lat: 100, streamLat: 20, safety: 24}

// mic is an input device of two channels, with 30 frames of latency and
// 10 of its stream's.
var mic = fakeDevice{id: 20, name: "Microphone", rate: testRate, ins: 2, buffer: bufFrames, inLat: 30, inStreamLat: 10}

func openFake(t *testing.T, f *fakeCA, cfg Config) *Client {
	t.Helper()
	f.use(t)
	cl, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

func newSystem(devs ...fakeDevice) *fakeCA {
	f := newFakeCA()
	for _, d := range devs {
		f.add(d)
	}
	var out, in uint32
	for _, d := range devs {
		if d.outs > 0 && out == 0 {
			out = d.id
		}
		if d.ins > 0 && in == 0 {
			in = d.id
		}
	}
	f.defaults(out, in)
	return f
}

// ramp returns n stereo frames, left counting up from at and right
// counting down, so a frame tells where it came from.
func ramp(at, n int) []float32 {
	b := make([]float32, 2*n)
	for i := range n {
		b[2*i] = float32(at + i)
		b[2*i+1] = -float32(at + i)
	}
	return b
}

// Each buffer the output takes plays the ring's next frames, one
// channel a buffer, and the ring's end wraps; a ring run dry plays the
// rest silent and counts the buffer short; a buffer past the ring's
// channels plays silence, and a buffer smaller than asked is filled to
// its size.
func TestRenderPlaysTheRing(t *testing.T) {
	f := newSystem(speakers)
	cl := openFake(t, f, Config{})
	if cl.Channels() != 2 {
		t.Fatalf("channels = %d, want 2", cl.Channels())
	}
	size := int(cl.size)
	next := 0
	for c := 0; next < 2*size+1000; c++ {
		if w := cl.Write(ramp(next, bufFrames)); w != bufFrames {
			t.Fatalf("cycle %d: wrote %d of %d", c, w, bufFrames)
		}
		next += bufFrames
		out := f.render(t, bufFrames, 2, 0)
		for i := range bufFrames {
			want := float32(next - bufFrames + i)
			if out[0][i] != want || out[1][i] != -want {
				t.Fatalf("cycle %d frame %d: played %v %v, want %v", c, i, out[0][i], out[1][i], want)
			}
		}
	}
	if e := cl.Poll(); e.Short != 0 {
		t.Fatalf("%d buffers short while the ring kept up", e.Short)
	}
	// Half a buffer: half plays, the rest is silence, and the buffer is
	// short. A third buffer plays silence.
	cl.Write(ramp(next, 32))
	out := f.render(t, bufFrames, 3, 0)
	if out[0][31] != float32(next+31) || out[0][32] != 0 || out[1][63] != 0 {
		t.Fatalf("a short buffer played %v", out[0])
	}
	if slices.ContainsFunc(out[2], func(v float32) bool { return v != 0 }) {
		t.Fatalf("a buffer past the ring's channels played %v", out[2])
	}
	if e := cl.Poll(); e.Short != 1 {
		t.Fatalf("short = %d, want 1", e.Short)
	}
	next += 32
	// A buffer of 16 frames' room, asked for 64: 16 play, and the ring
	// gives 64 all the same, as Core Audio asked for them.
	cl.Write(ramp(next, bufFrames))
	out = f.renderSized(t, bufFrames, 2, 16, 0)
	if out[0][15] != float32(next+15) || cl.Queued() != 0 {
		t.Fatalf("a small buffer played %v, %d left queued", out[0], cl.Queued())
	}
	next += bufFrames
	// Paused, the output plays silence and takes nothing, and no buffer
	// counts short.
	cl.Write(ramp(next, bufFrames))
	cl.Pause(true)
	out = f.render(t, bufFrames, 2, 0)
	if slices.ContainsFunc(out[0], func(v float32) bool { return v != 0 }) || cl.Queued() != bufFrames {
		t.Fatalf("paused, it played %v and left %d queued", out[0], cl.Queued())
	}
	cl.Pause(false)
	out = f.render(t, bufFrames, 2, 0)
	if out[0][0] != float32(next) {
		t.Fatalf("resumed, it played %v first, want %d", out[0][0], next)
	}
	if e := cl.Poll(); e.Short != 0 || e.Cycles == 0 {
		t.Fatalf("events %+v, want none short", e)
	}
}

// heardAtNs is the frame expected at the ear at host time t, while every
// buffer from start on was full: the frames since start, less the
// latency, within what the output took.
func heardAtNs(t, took int64) int64 {
	return min(max((t-start)/frameNs-120, 0), took)
}

// The frame heard follows the clock, frame by frame, across each
// buffer's start, not a buffer at a time; and a buffer starting moves it
// nowhere.
func TestHeardFollowsTheClock(t *testing.T) {
	f := newSystem(speakers)
	cl := openFake(t, f, Config{})
	if _, ok := cl.Heard(); ok {
		t.Fatal("heard a frame before the output took a buffer")
	}
	if got := cl.Info(); got.Latency != 120 || got.Safety != 24 || cl.Delay() != 64+24+120 {
		t.Fatalf("latency %d, safety %d, delay %d; want 120, 24 and 208", got.Latency, got.Safety, cl.Delay())
	}
	cl.Write(ramp(0, 4000))
	last := int64(-1)
	for k := range 20 {
		host := int64(start + k*period)
		// Core Audio asks for a buffer about a buffer and the safety
		// offset before its time: the clock reads that as it asks.
		ask := host - (bufFrames+24)*frameNs
		f.setClock(ask)
		before, _ := cl.Heard()
		f.render(t, bufFrames, 2, uint64(host))
		after, ok := cl.Heard()
		if !ok {
			t.Fatalf("cycle %d: heard nothing", k)
		}
		if k > 0 && after != before {
			t.Fatalf("cycle %d: the buffer starting moved the frame heard from %d to %d", k, before, after)
		}
		took := int64((k + 1) * bufFrames)
		for ns := ask; ns < ask+period; ns += 7 * frameNs {
			f.setClock(ns)
			got, _ := cl.Heard()
			if want := heardAtNs(ns, took); got != want {
				t.Fatalf("cycle %d, %d ns: heard %d, want %d", k, ns-start, got, want)
			}
			if got < last {
				t.Fatalf("cycle %d: heard went back from %d to %d", k, last, got)
			}
			last = got
		}
	}
}

// Silence the output played, as the ring ran dry or paused, counts for
// no frame: the frame heard stands while it plays, then goes on from the
// next frame the ring gave.
func TestHeardSkipsSilence(t *testing.T) {
	f := newSystem(speakers)
	cl := openFake(t, f, Config{})
	cl.Write(ramp(0, 32))
	f.render(t, bufFrames, 2, start) // 32 frames, 32 silent
	cl.Pause(true)
	f.render(t, bufFrames, 2, start+period) // silent
	cl.Pause(false)
	cl.Write(ramp(32, 3*bufFrames))
	f.render(t, bufFrames, 2, start+2*period)
	f.render(t, bufFrames, 2, start+3*period)
	at := func(frames int64) int64 {
		f.setClock(start + (frames+120)*frameNs)
		h, ok := cl.Heard()
		if !ok {
			t.Fatal("heard nothing")
		}
		return h
	}
	for _, c := range []struct{ at, want int64 }{
		{0, 0}, {31, 31}, {32, 32}, {63, 32}, {64, 32}, {127, 32},
		{128, 32}, {129, 33}, {191, 95}, {192, 96}, {255, 159}, {300, 160},
	} {
		if got := at(c.at); got != c.want {
			t.Errorf("%d frames on: heard %d, want %d", c.at, got, c.want)
		}
	}
}

// What the input records reaches Read in order, each block stamped with
// the frame heard as its first frame reached the input: its time less
// the input's latency. The unit renders through a stand-in that changes
// every register a C function may.
func TestInputIsStamped(t *testing.T) {
	f := newSystem(speakers, mic)
	cl := openFake(t, f, Config{Record: true})
	info := cl.Info()
	if info.Recorded != 2 || info.InputDevice != "Microphone" || info.InputLatency != 40 || info.InputProblem != nil {
		t.Fatalf("info %+v", info)
	}
	if len(f.made) != 2 || !f.made[1].running {
		t.Fatal("the input unit is not running")
	}
	cl.Write(ramp(0, 2000))
	for k := range 8 {
		f.render(t, bufFrames, 2, uint64(start+k*period))
	}
	in := [][]float32{make([]float32, bufFrames), make([]float32, bufFrames)}
	for i := range bufFrames {
		in[0][i], in[1][i] = float32(1000+i), float32(-1000-i)
	}
	// Its first frame came in at the hardware 5 buffers on: at the
	// input 40 frames before that, when frame 320-40-120 was heard.
	f.record(t, in, bufFrames, uint64(start+5*period), 0)
	dst := make([]float32, 2*40)
	n, at, ok := cl.Read(dst)
	if !ok || n != 40 || at != 160 {
		t.Fatalf("read %d frames at %d, ok %v; want 40 at 160", n, at, ok)
	}
	if dst[0] != 1000 || dst[1] != -1000 || dst[78] != 1039 || dst[79] != -1039 {
		t.Fatalf("read %v", dst)
	}
	n, at, ok = cl.Read(dst)
	if !ok || n != 24 || at != 200 || dst[0] != 1040 {
		t.Fatalf("read %d frames at %d, first %v; want 24 at 200, first 1040", n, at, dst[0])
	}
	if _, _, ok := cl.Read(dst); ok {
		t.Fatal("read a block that was not recorded")
	}
	// A render that fails writes nothing, and counts; a block larger
	// than the room counts lost.
	f.record(t, in, bufFrames, uint64(start+6*period), -1)
	f.record(t, in, scratchFrames+1, uint64(start+6*period), 0)
	if _, _, ok := cl.Read(dst); ok {
		t.Fatal("read a block that failed")
	}
	if e := cl.Poll(); e.Failed != 1 || e.Lost != scratchFrames+1 {
		t.Fatalf("events %+v, want 1 failed, %d lost", e, scratchFrames+1)
	}
}

// Blocks that find the input's ring full are dropped, counted, and
// what fitted reads with the stamps of its own block.
func TestInputRingFull(t *testing.T) {
	f := newSystem(speakers, mic)
	cl := openFake(t, f, Config{Record: true})
	size := int(cl.size)
	in := [][]float32{make([]float32, 4096), make([]float32, 4096)}
	blocks := size/4096 + 2
	for k := range blocks {
		for i := range in[0] {
			in[0][i] = float32(k*4096 + i)
		}
		f.record(t, in, 4096, uint64(start+k*4096*frameNs), 0)
	}
	if e := cl.Poll(); e.Lost != int64(blocks*4096-size) {
		t.Fatalf("lost %d, want %d", e.Lost, blocks*4096-size)
	}
	dst := make([]float32, 2*4096)
	got := 0
	for {
		n, _, ok := cl.Read(dst)
		if !ok {
			break
		}
		if dst[0] != float32(got) {
			t.Fatalf("frame %d read as %v", got, dst[0])
		}
		got += n
	}
	if got != size {
		t.Fatalf("read %d frames, want the ring's %d", got, size)
	}
}

// Outputs names the device's channels to play on: the unit's channel
// map plays each of the client's on the channel named, and none on the
// rest. Empty Outputs is an error; a device of one plays one channel.
func TestOutputsMapChannels(t *testing.T) {
	four := speakers
	four.outs = 4
	f := newSystem(four)
	cl := openFake(t, f, Config{Outputs: []int{3, 1, 1, 9}})
	if got := cl.Info().Played; !slices.Equal(got, []int{3, 1, -1, -1}) {
		t.Fatalf("played %v", got)
	}
	b := f.made[0].props[[3]uint32{unitChannelMap, unitInput, busOut}]
	m := make([]int32, len(b)/4)
	for i := range m {
		m[i] = int32(binary.LittleEndian.Uint32(b[4*i:]))
	}
	if !slices.Equal(m, []int32{-1, 1, -1, 0}) {
		t.Fatalf("channel map %v", m)
	}
	if fm := f.format(0, unitInput, busOut); fm.channelsPerFrame != 4 || fm.sampleRate != testRate ||
		fm.formatFlags&formatNonInterleaved == 0 {
		t.Fatalf("format %+v", fm)
	}
	f.use(t)
	if _, err := Open(Config{Outputs: []int{}}); err == nil {
		t.Fatal("opened with no outputs named")
	}

	one := speakers
	one.outs = 1
	f = newSystem(one)
	cl = openFake(t, f, Config{})
	if got := cl.Info().Played; !slices.Equal(got, []int{0}) || cl.Channels() != 1 {
		t.Fatalf("a device of one plays on %v", got)
	}
	if _, ok := f.made[0].props[[3]uint32{unitChannelMap, unitInput, busOut}]; ok {
		t.Fatal("a channel map was set where Outputs named none")
	}
}

// format returns the stream format set on the unit made i.
func (f *fakeCA) format(i int, scope, elem uint32) streamDescription {
	b := f.made[i].props[[3]uint32{unitStreamFormat, scope, elem}]
	if len(b) == 0 {
		return streamDescription{}
	}
	return *(*streamDescription)(unsafe.Pointer(&b[0]))
}

// The output plays where the input cannot record, and Info says why: no
// leave to record, no input device, an input at another rate, or a unit
// that refused. Nothing of the input is left behind.
func TestInputProblems(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(f *fakeCA)
		units int
	}{
		{"denied", func(f *fakeCA) { f.denied = true }, 1},
		{"no input", func(f *fakeCA) { f.defaults(speakers.id, 0) }, 1},
		{"other rate", func(f *fakeCA) { f.put(mic.id, propNominalSampleRate, scopeGlobal, 0, 44100.0) }, 1},
		{"refused", func(f *fakeCA) { f.refuse[unitInputCallback] = true }, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newSystem(speakers, mic)
			c.setup(f)
			cl := openFake(t, f, Config{Record: true})
			info := cl.Info()
			if info.InputProblem == nil || info.Recorded != 0 || cl.Inputs() != 0 {
				t.Fatalf("info %+v, inputs %d", info, cl.Inputs())
			}
			if c.name == "denied" && !errors.Is(info.InputProblem, ErrDenied) {
				t.Fatalf("problem %v, want ErrDenied", info.InputProblem)
			}
			if len(f.made) != c.units {
				t.Fatalf("%d units made, want %d", len(f.made), c.units)
			}
			if !f.made[0].running {
				t.Fatal("the output is not running")
			}
			if c.units == 2 && (!f.made[1].disposed || f.made[1].running) {
				t.Fatal("the input unit was left behind")
			}
			if len(cl.units) != 1 {
				t.Fatalf("the client holds %d units", len(cl.units))
			}
		})
	}
}

// A unit that refuses a setting fails Open, and what was made is let go:
// no unit, no listener.
func TestOpenFailsCleanly(t *testing.T) {
	f := newSystem(speakers)
	f.refuse[unitRenderCallback] = true
	f.use(t)
	if _, err := Open(Config{}); err == nil {
		t.Fatal("opened with the callback refused")
	}
	if len(f.made) != 1 || !f.made[0].disposed {
		t.Fatal("the unit was left behind")
	}
	for k, n := range f.listeners {
		if n != 0 {
			t.Fatalf("%d listeners left on %+v", n, k)
		}
	}
	f = newSystem(speakers)
	f.defaults(0, 0)
	f.use(t)
	if _, err := Open(Config{}); !errors.Is(err, ErrNoOutput) {
		t.Fatalf("Open with no output device: %v", err)
	}
}

// A change Core Audio tells of has Poll read the devices again: one that
// changes the device, its rate or its being there asks to open again;
// one of its latency or buffer is read in place.
func TestPollHearsChanges(t *testing.T) {
	f := newSystem(speakers, mic)
	cl := openFake(t, f, Config{Record: true})
	if e := cl.Poll(); e.Changed || e.Reopen != "" {
		t.Fatalf("events %+v before any change", e)
	}
	f.put(speakers.id, propLatency, scopeOutput, 0, uint32(500))
	f.put(speakers.id, propBufferFrameSize, scopeGlobal, 0, uint32(128))
	f.tell(speakers.id, propLatency)
	if e := cl.Poll(); !e.Changed || e.Reopen != "" {
		t.Fatalf("events %+v after the latency changed", e)
	}
	if i := cl.Info(); i.Latency != 520 || i.Buffer != 128 || cl.Delay() != 128+24+520 {
		t.Fatalf("latency %d, buffer %d, delay %d after the change", i.Latency, i.Buffer, cl.Delay())
	}
	for _, c := range []struct {
		name   string
		change func()
		undo   func()
	}{
		{"output", func() { f.defaults(99, mic.id) }, func() { f.defaults(speakers.id, mic.id) }},
		{"input", func() { f.defaults(speakers.id, 99) }, func() { f.defaults(speakers.id, mic.id) }},
		{"gone", func() { f.put(speakers.id, propDeviceIsAlive, scopeGlobal, 0, uint32(0)) },
			func() { f.put(speakers.id, propDeviceIsAlive, scopeGlobal, 0, uint32(1)) }},
		{"rate", func() { f.put(speakers.id, propNominalSampleRate, scopeGlobal, 0, 48000.0) },
			func() { f.put(speakers.id, propNominalSampleRate, scopeGlobal, 0, float64(testRate)) }},
		{"input rate", func() { f.put(mic.id, propNominalSampleRate, scopeGlobal, 0, 48000.0) },
			func() { f.put(mic.id, propNominalSampleRate, scopeGlobal, 0, float64(testRate)) }},
		{"channels", func() { f.put(speakers.id, propStreamConfiguration, scopeOutput, 0, bufferList(6)) },
			func() { f.put(speakers.id, propStreamConfiguration, scopeOutput, 0, bufferList(2)) }},
	} {
		c.change()
		f.tell(systemObject, propDefaultOutputDevice)
		if e := cl.Poll(); e.Reopen == "" {
			t.Errorf("%s: no reopen asked", c.name)
		}
		c.undo()
		f.tell(systemObject, propDefaultOutputDevice)
		if e := cl.Poll(); e.Reopen != "" {
			t.Errorf("%s undone: reopen asked: %s", c.name, e.Reopen)
		}
	}
}

// An input that failed to open waits for the default input to change
// before it asks to open again, so a refused microphone asks no reopen
// on each change elsewhere.
func TestFailedInputWaitsForAnotherDevice(t *testing.T) {
	f := newSystem(speakers, mic)
	f.denied = true
	cl := openFake(t, f, Config{Record: true})
	f.tell(speakers.id, propLatency)
	if e := cl.Poll(); e.Reopen != "" {
		t.Fatalf("reopen asked with the same input: %s", e.Reopen)
	}
	f.defaults(speakers.id, 0)
	f.tell(systemObject, propDefaultInputDevice)
	if e := cl.Poll(); e.Reopen == "" {
		t.Fatal("no reopen asked as the input changed")
	}
}

// Suspend stops both units and Resume starts them; Close stops and lets
// go of each, and removes the listeners, once; and each call that starts
// a unit runs with signals masked.
func TestSuspendAndClose(t *testing.T) {
	f := newSystem(speakers, mic)
	cl := openFake(t, f, Config{Record: true})
	if err := cl.Suspend(); err != nil {
		t.Fatal(err)
	}
	for i, u := range f.made {
		if u.running {
			t.Fatalf("unit %d runs suspended", i)
		}
	}
	if err := cl.Resume(); err != nil {
		t.Fatal(err)
	}
	for i, u := range f.made {
		if !u.running || u.starts != 2 {
			t.Fatalf("unit %d: running %v, started %d times", i, u.running, u.starts)
		}
	}
	added := 0
	for _, n := range f.listeners {
		added += n
	}
	if added == 0 {
		t.Fatal("no listener added")
	}
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cl.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := cl.Resume(); err != nil {
		t.Fatal(err)
	}
	for i, u := range f.made {
		if u.running || u.ready || !u.disposed || u.stops != 2 {
			t.Fatalf("unit %d after close: %+v", i, u)
		}
	}
	for k, n := range f.listeners {
		if n != 0 {
			t.Fatalf("%d listeners left on %+v", n, k)
		}
	}
	if f.unmasked != 0 {
		t.Fatalf("%d units made or started with signals unmasked", f.unmasked)
	}
}
