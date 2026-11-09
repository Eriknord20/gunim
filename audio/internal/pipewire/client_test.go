//go:build linux && !android && (amd64 || arm64)

package pipewire

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func openFake(t *testing.T, f *fakeServer, cfg Config) *Client {
	t.Helper()
	f.use(t)
	if cfg.Hold == 0 {
		cfg.Hold = 100 * time.Millisecond
	}
	if cfg.Rate == 0 {
		cfg.Rate = 48000
	}
	cl, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
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

// at48k is a time report at 48 kHz, at now, with no delay.
func at48k(now int64) pwTime { return pwTime{now: now, rateNum: 1, rateDenom: 48000} }

// setClock puts a clock of the test's in place of CLOCK_MONOTONIC.
func setClock(t *testing.T, now *atomic.Int64) {
	t.Helper()
	old := monotonic
	monotonic = now.Load
	t.Cleanup(func() { monotonic = old })
}

// Each cycle plays the ring's next frames, as many as the stream asks
// for, and the ring's end wraps; a ring run dry plays the rest silent
// and counts the cycle short.
func TestProcessPlaysTheRing(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{})
	size := int(cl.size)
	next := 0
	for c := 0; next < 2*size+1000; c++ {
		if w := cl.Write(ramp(next, 100)); w != 100 {
			t.Fatalf("cycle %d: wrote %d of 100", c, w)
		}
		next += 100
		out := f.play(t, 100, at48k(int64(c)))
		if len(out) != 200 {
			t.Fatalf("cycle %d: played %d samples, want 200", c, len(out))
		}
		for i := range 100 {
			want := float32(next - 100 + i)
			if out[2*i] != want || out[2*i+1] != -want {
				t.Fatalf("cycle %d frame %d: played %v %v, want %v", c, i, out[2*i], out[2*i+1], want)
			}
		}
	}
	if e := cl.Poll(); e.Short != 0 || e.Cycles == 0 {
		t.Fatalf("short %d of %d cycles while the ring kept up", e.Short, e.Cycles)
	}
	// Half a cycle: half plays, the rest is silence, and the cycle is
	// short.
	cl.Write(ramp(next, 50))
	out := f.play(t, 100, at48k(1))
	if out[2*49] != float32(next+49) || out[2*50] != 0 || out[199] != 0 {
		t.Fatalf("a short cycle played %v", out)
	}
	if e := cl.Poll(); e.Short != 1 {
		t.Fatalf("short = %d, want 1", e.Short)
	}
	if got := cl.Taken(); got != int64(next+50) {
		t.Fatalf("taken = %d, want %d", got, next+50)
	}
}

// A buffer that asks for nothing plays the quantum the client asked
// for, and one too small for what it asks plays what it holds.
func TestProcessSizesTheCycle(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Quantum: 128})
	cl.Write(ramp(0, 1000))
	if out := f.play(t, 0, at48k(0)); len(out) != 2*128 {
		t.Fatalf("a buffer that asks for nothing played %d frames, want 128", len(out)/2)
	}
	if out := f.playInto(t, newFakeBuffer(64, 512), at48k(0)); len(out) != 2*64 || out[0] != 128 {
		t.Fatalf("a buffer of 64 frames played %d frames from %v", len(out)/2, out[0])
	}
	// No buffer to fill counts, and takes nothing.
	p := f.stream(t, directionOutput)
	if q := f.run(p, at48k(0)); q != 0 {
		t.Fatalf("queued %d buffers with none to fill", q)
	}
	if e := cl.Poll(); e.Empty != 1 || cl.Taken() != 192 {
		t.Fatalf("empty %d, taken %d; want 1 and 192", e.Empty, cl.Taken())
	}
}

func TestPausePlaysSilence(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{})
	cl.Write(ramp(1, 300))
	cl.Pause(true)
	out := f.play(t, 100, at48k(0))
	for _, v := range out {
		if v != 0 {
			t.Fatalf("paused, played %v", out)
		}
	}
	if e := cl.Poll(); e.Short != 0 || cl.Taken() != 0 {
		t.Fatalf("paused: short %d, taken %d", e.Short, cl.Taken())
	}
	cl.Pause(false)
	if out := f.play(t, 100, at48k(0)); out[0] != 1 {
		t.Fatalf("resumed, played %v first", out[0])
	}
}

// Held counts the ring, the cycle's delay to the device at the graph's
// rate, the frames queued in the stream and those its resampler holds,
// less the frames played since the cycle began, as far as a cycle's.
// HeardAt places a frame on the same line.
func TestHeldCountsTheDelay(t *testing.T) {
	var now atomic.Int64
	setClock(t, &now)
	f := newFakeServer()
	cl := openFake(t, f, Config{})
	if h := cl.Held(); h != 0 {
		t.Fatalf("held %d before anything", h)
	}
	cl.Write(ramp(0, 1000))
	if h := cl.Held(); h != 1000 {
		t.Fatalf("held %d before the first cycle, want the ring's 1000", h)
	}
	if _, ok := cl.HeardAt(0); ok {
		t.Fatal("HeardAt is ok before the first cycle")
	}
	const start = int64(5 * time.Second)
	// The graph runs at 44100 Hz, 10 ms behind the stream; 256 frames
	// are queued and the resampler holds 3.
	tm := pwTime{now: start, rateNum: 1, rateDenom: 44100, delay: 441, queued: 256, buffered: 3}
	f.play(t, 256, tm)
	now.Store(start)
	// 744 in the ring, 480 of delay, 256 queued, 3 resampling.
	if h := cl.Held(); h != 744+480+256+3 {
		t.Fatalf("held %d at the cycle's start, want %d", h, 744+480+256+3)
	}
	now.Store(start + int64(time.Millisecond))
	if h := cl.Held(); h != 744+480+256+3-48 {
		t.Fatalf("held %d a millisecond in, want %d", h, 744+480+256+3-48)
	}
	now.Store(start + int64(time.Second))
	if h := cl.Held(); h != 744+480+3 {
		t.Fatalf("held %d with the next cycle late, want %d: a cycle's frames gone at most", h, 744+480+3)
	}
	heard, ok := cl.HeardAt(start)
	if !ok || heard != 256-739 {
		t.Fatalf("heard at the cycle's start: %d, %v; want %d", heard, ok, 256-739)
	}
	if heard, _ := cl.HeardAt(start + int64(time.Millisecond)); heard != 256-739+48 {
		t.Fatalf("heard 1 ms on: %d, want %d", heard, 256-739+48)
	}
	// 10 ms on, the next cycle is late: the frame heard waits a cycle on.
	if heard, _ := cl.HeardAt(start + int64(10*time.Millisecond)); heard != 256-739+256 {
		t.Fatalf("heard 10 ms on: %d, want %d", heard, 256-739+256)
	}
	if e := cl.Poll(); e.Quantum != 256 || e.DeviceRate != 44100 || e.Delay != 483 {
		t.Fatalf("poll: quantum %d, device rate %d, delay %d; want 256, 44100, 483", e.Quantum, e.DeviceRate, e.Delay)
	}
}

// What the input records comes back in order, stamped with the frame
// heard as it reached the input: the cycle's last frame came the
// input's delay before the cycle began, and each frame before it a
// frame earlier.
func TestReadStampsInputs(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Record: true})
	in := f.stream(t, directionInput)
	if !in.has("media.category=Capture") {
		t.Fatalf("the input's properties: %s", in.props)
	}
	const start = int64(5 * time.Second)
	dst := make([]float32, 2*16)
	// Before the output's first cycle, nothing places what the input
	// records: it goes.
	f.record(t, at48k(start), ramp(1000, 64))
	if _, _, ok := cl.Read(dst); ok {
		t.Fatal("read what came before the output played")
	}
	cl.Write(ramp(0, 1000))
	f.play(t, 256, pwTime{now: start, rateNum: 1, rateDenom: 48000, delay: 480, queued: 256})
	// Frame 0 of the ring is heard at start less 256 frames, and 256 at
	// start plus 736.
	ms := int64(time.Millisecond)
	f.record(t, pwTime{now: start + 2*ms, rateNum: 1, rateDenom: 48000, delay: 96}, ramp(0, 40), ramp(40, 24))
	// The last of 64 frames reached the input at start; the first 64
	// frames before.
	first := int64(256 - 736 - 64)
	for k := range 4 {
		n, at, ok := cl.Read(dst)
		if !ok || n != 16 {
			t.Fatalf("read %d: n %d, ok %v", k, n, ok)
		}
		if dst[0] != float32(16*k) || dst[31] != -float32(16*k+15) {
			t.Fatalf("read %d: %v", k, dst)
		}
		if want := first + int64(16*k); at != want {
			t.Fatalf("read %d: stamped %d, want %d", k, at, want)
		}
	}
	if _, _, ok := cl.Read(dst); ok {
		t.Fatal("read more than recorded")
	}
	// A cycle with no frames notes nothing.
	f.record(t, at48k(start))
	if _, _, ok := cl.Read(dst); ok {
		t.Fatal("read a cycle that brought nothing")
	}
}

// While the output's cycles stall, as through an xrun or a client
// joining the graph, and the input's go on, no stamp passes the frames
// the output took: the frame heard moves on a cycle at most past the
// output's last cycle.
func TestStampsWaitForAStalledOutput(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Record: true})
	const start = int64(5 * time.Second)
	cl.Write(ramp(0, 4000))
	f.play(t, 256, pwTime{now: start, rateNum: 1, rateDenom: 48000, delay: 480, queued: 256})
	dst := make([]float32, 2*256)
	for k := range 20 {
		// The input runs on, a cycle each 5.33 ms, for 100 ms.
		now := start + int64(k)*int64(256*time.Second/48000)
		f.record(t, pwTime{now: now, rateNum: 1, rateDenom: 48000, delay: 96}, ramp(256*k, 256))
		for {
			n, at, ok := cl.Read(dst)
			if !ok {
				break
			}
			if at+int64(n) > cl.Taken() {
				t.Fatalf("input cycle %d stamped %d to %d, past the %d frames taken", k, at, at+int64(n), cl.Taken())
			}
		}
	}
	// Earlier than the output's last cycle, a stamp moves back freely.
	heard, _ := cl.HeardAt(start - int64(time.Second))
	if want := int64(256 - 736 - 48000); heard != want {
		t.Fatalf("heard a second before the last cycle: %d, want %d", heard, want)
	}
}

// Frames that find the ring full are lost, counted, and the frames kept
// keep their stamps.
func TestInputsLostAndFoundAgain(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Record: true, Hold: time.Millisecond})
	f.play(t, 256, at48k(0))
	size := int(cl.size)
	for total := 0; total < size+512; {
		n := min(1024, size+512-total)
		f.record(t, at48k(0), ramp(total, n))
		total += n
	}
	e := cl.Poll()
	if e.Lost != 512 {
		t.Fatalf("lost %d, want 512", e.Lost)
	}
	dst := make([]float32, 2*size)
	read := 0
	for {
		n, _, ok := cl.Read(dst[2*read:])
		if !ok {
			break
		}
		read += n
	}
	if read != size || dst[2*(size-1)] != float32(size-1) {
		t.Fatalf("read %d frames, the last %v", read, dst[2*(read-1)])
	}
	// Room again: the next cycle comes whole.
	f.record(t, at48k(0), ramp(7, 10))
	if n, _, ok := cl.Read(dst); !ok || n != 10 || dst[0] != 7 {
		t.Fatalf("after the loss: n %d, ok %v, %v", n, ok, dst[0])
	}
}

// The streams ask for the format, the quantum and the flags; Close
// destroys them and stops the loop.
func TestOpenAsksForTheStream(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Name: `My "app"`, Rate: 44100, Quantum: 512, Record: true})
	out, in := f.stream(t, directionOutput), f.stream(t, directionInput)
	for _, p := range []*fakePW{out, in} {
		if !bytes.Equal(p.params, formatPod(44100, stereoPositions)) {
			t.Fatalf("format %v", p.params)
		}
		if p.flags != flagAutoconnect|flagMapBuffers|flagRTProcess {
			t.Fatalf("flags %#x", p.flags)
		}
		for _, prop := range []string{`node.latency="512/44100"`, `node.name="My \"app\""`, "media.type=Audio"} {
			if !p.has(prop) {
				t.Fatalf("properties %s lack %s", p.props, prop)
			}
		}
	}
	if !out.has("media.category=Playback") {
		t.Fatalf("output properties %s", out.props)
	}
	if i := cl.Info(); i.Rate != 44100 || i.Quantum != 512 || !i.Recording || i.Library != "1.0.5-fake" {
		t.Fatalf("info %+v", i)
	}
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	if !out.destroyed || !in.destroyed || f.stopped != 1 {
		t.Fatalf("closed: destroyed %v %v, loop stopped %d", out.destroyed, in.destroyed, f.stopped)
	}
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
}

// A stream the server refuses makes Open fail, with what the server
// said.
func TestOpenFailsWithTheServersError(t *testing.T) {
	f := newFakeServer()
	f.connectTo, f.connectErr = StateError, "no target node available"
	f.use(t)
	_, err := Open(Config{Rate: 48000})
	if !errors.Is(err, ErrStopped) || !strings.Contains(err.Error(), "no target node available") {
		t.Fatalf("err = %v", err)
	}
	if !f.streams[0].destroyed {
		t.Fatal("the failed stream was left")
	}
}

// The states the server moves a stream through reach Poll, and an
// error reaches Err.
func TestStatesReachPoll(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Record: true})
	if e := cl.Poll(); e.Out != StateStreaming || e.In != StateStreaming || !e.Moved {
		t.Fatalf("first poll %+v", e)
	}
	if e := cl.Poll(); e.Moved {
		t.Fatal("moved with nothing told")
	}
	out := f.stream(t, directionOutput)
	f.setState(out, StatePaused, "")
	f.setState(out, StateStreaming, "")
	if e := cl.Poll(); !e.Moved || e.Out != StateStreaming || cl.Err() != nil {
		t.Fatalf("after a move: %+v, %v", e, cl.Err())
	}
	f.setState(f.stream(t, directionInput), StateError, "connection closed")
	e := cl.Poll()
	if e.In != StateError || e.Error != "connection closed" {
		t.Fatalf("after an error: %+v", e)
	}
	if err := cl.Err(); !errors.Is(err, ErrStopped) || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("err = %v", err)
	}
}

// The ring carries every frame in order from a writer to the process
// function, and from the input's process function to a reader, each on
// a thread of its own.
func TestRingsAcrossThreads(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Record: true, Hold: 5 * time.Millisecond})
	const total = 200000
	var wg sync.WaitGroup
	var bad atomic.Value
	stop := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		next := 0
		for next < total {
			select {
			case <-stop:
				return
			default:
			}
			// From 1, so silence tells itself apart.
			next += cl.Write(ramp(next+1, min(300, total-next)))
		}
	}()
	go func() {
		defer wg.Done()
		dst := make([]float32, 2*500)
		want := 0
		for want < total {
			n, _, ok := cl.Read(dst)
			if !ok {
				select {
				case <-stop:
					return
				default:
				}
				continue
			}
			for i := range n {
				if dst[2*i] != float32(want+1) {
					bad.Store(dst[2*i])
					return
				}
				want++
			}
		}
	}()
	out := f.stream(t, directionOutput)
	played, cycle := 0, 0
	for played < total {
		b := newFakeBuffer(256, 256)
		f.run(out, at48k(0), b)
		var frames []float32
		for i := range int(b.chunk.size / 8) {
			if v := b.samples[2*i]; v != 0 {
				if v != float32(played+1) {
					t.Fatalf("played %v, want %d", v, played)
				}
				played++
				frames = append(frames, b.samples[2*i], b.samples[2*i+1])
			}
		}
		if len(frames) > 0 {
			f.record(t, at48k(0), frames)
		}
		if bad.Load() != nil {
			break
		}
		// Let the reader catch up now and then, so neither the input's
		// ring nor its log of cycles fills.
		cycle++
		if cycle%32 == 0 {
			for atomic.LoadUint64(&cl.st.inW) != atomic.LoadUint64(&cl.st.inR) && bad.Load() == nil {
				time.Sleep(10 * time.Microsecond)
			}
		}
	}
	close(stop)
	wg.Wait()
	if v := bad.Load(); v != nil {
		t.Fatalf("the reader read %v out of order", v)
	}
}

func TestAtLeast(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"1.0.5", true}, {"0.3.50", true}, {"0.3.49", false}, {"0.3.65", true},
		{"0.2.99", false}, {"1.2.0-rc1", true}, {"0.3.50git", true}, {"", false},
	} {
		if got := atLeast(c.v, minVersion); got != c.want {
			t.Errorf("atLeast(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := quote("a \"b\" \\c\n"); got != `"a \"b\" \\c "` {
		t.Fatalf("quote = %s", got)
	}
}

// framesOf returns n frames of four channels, each frame's channels telling
// which frame and channel it is.
func framesOf(at, n, ch int) []float32 {
	b := make([]float32, ch*n)
	for i := range n {
		for c := range ch {
			b[ch*i+c] = float32(1000*(at+i) + c + 1)
		}
	}
	return b
}

var fourPositions = []uint32{channelFL, channelFR, 12, 13}

func TestOutputsOfAnyChannelsPlayFrameByFrame(t *testing.T) {
	for _, ch := range []int{1, 3, 4, 6, 18} {
		positions := make([]uint32, ch)
		for i := range positions {
			positions[i] = channelAux0 + uint32(i)
		}
		f := newFakeServer()
		cl := openFake(t, f, Config{OutPositions: positions})
		if got := cl.Info().OutChannels; got != ch {
			t.Fatalf("%d channels: Info says %d", ch, got)
		}
		cl.Write(framesOf(0, 100, ch))
		p := f.stream(t, directionOutput)
		// Room for 64 frames in the buffer, of ch channels, and a guard.
		b := &fakeBuffer{samples: make([]float32, 64*ch+1)}
		b.data.maxSize = uint32(64 * ch * 4)
		b.data.data = uintptr(unsafe.Pointer(&b.samples[0]))
		b.data.chunk = uintptr(unsafe.Pointer(&b.chunk))
		b.buf.nDatas = 1
		b.buf.datas = uintptr(unsafe.Pointer(&b.data))
		b.pw.buffer = uintptr(unsafe.Pointer(&b.buf))
		b.pw.requested = 40
		if q := f.run(p, at48k(0), b); q != 1 {
			t.Fatalf("%d channels: queued %d", ch, q)
		}
		if b.chunk.stride != int32(4*ch) || b.chunk.size != uint32(40*4*ch) || b.pw.size != 40 {
			t.Fatalf("%d channels: chunk %+v, size %d", ch, b.chunk, b.pw.size)
		}
		if got, want := b.samples[:40*ch], framesOf(0, 40, ch); !slices.Equal(got, want) {
			t.Fatalf("%d channels: played %v, want %v", ch, got[:ch*2], want[:ch*2])
		}
		if cl.Taken() != 40 {
			t.Fatalf("%d channels: took %d", ch, cl.Taken())
		}
		// The ring run dry, the rest of a buffer is silent, whole frames.
		b.pw.requested = 64
		if q := f.run(p, at48k(1), b); q != 1 {
			t.Fatalf("%d channels: queued %d", ch, q)
		}
		if got, want := b.samples[:60*ch], framesOf(40, 60, ch)[:60*ch]; !slices.Equal(got[:60*ch], want) {
			t.Fatalf("%d channels: the ring's end played %v", ch, got[:ch])
		}
		for _, v := range b.samples[60*ch : 64*ch] {
			if v != 0 {
				t.Fatalf("%d channels: past the ring's end, %v", ch, v)
			}
		}
		if b.samples[64*ch] != 0 {
			t.Fatalf("%d channels: it wrote past the buffer", ch)
		}
	}
}

func TestABufferTooSmallForWhatItAsksPlaysWholeFrames(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{OutPositions: []uint32{channelFL, channelFR, 12}})
	cl.Write(framesOf(0, 100, 3))
	p := f.stream(t, directionOutput)
	b := &fakeBuffer{samples: make([]float32, 3*10+2+1)}
	b.data.maxSize = uint32((3*10 + 2) * 4) // ten frames, and part of another
	b.data.data = uintptr(unsafe.Pointer(&b.samples[0]))
	b.data.chunk = uintptr(unsafe.Pointer(&b.chunk))
	b.buf.nDatas = 1
	b.buf.datas = uintptr(unsafe.Pointer(&b.data))
	b.pw.buffer = uintptr(unsafe.Pointer(&b.buf))
	b.pw.requested = 64
	f.run(p, at48k(0), b)
	if b.pw.size != 10 || b.samples[30] != 0 || b.samples[32] != 0 {
		t.Fatalf("size %d, samples past it %v", b.pw.size, b.samples[30:])
	}
}

func TestInputsOfAnyChannelsRecordFrameByFrame(t *testing.T) {
	for _, ch := range []int{1, 3, 4, 6} {
		positions := make([]uint32, ch)
		for i := range positions {
			positions[i] = channelAux0 + uint32(i)
		}
		f := newFakeServer()
		cl := openFake(t, f, Config{Record: true, InPositions: positions})
		// The input's stamps stand on the output's time line.
		cl.Write(make([]float32, 2*100))
		f.play(t, 10, at48k(0))
		p := f.stream(t, directionInput)
		fb := &fakeBuffer{samples: make([]float32, (20+7)*ch)}
		fb.data.maxSize = uint32((20 + 7) * ch * 4)
		fb.data.data = uintptr(unsafe.Pointer(&fb.samples[0]))
		fb.data.chunk = uintptr(unsafe.Pointer(&fb.chunk))
		fb.buf.nDatas = 1
		fb.buf.datas = uintptr(unsafe.Pointer(&fb.data))
		fb.pw.buffer = uintptr(unsafe.Pointer(&fb.buf))
		fb.chunk.offset = uint32(7 * ch * 4)
		fb.chunk.size = uint32(20 * ch * 4)
		copy(fb.samples[7*ch:], framesOf(0, 20, ch))
		if q := f.run(p, at48k(0), fb); q != 1 {
			t.Fatalf("%d channels: queued %d", ch, q)
		}
		dst := make([]float32, 64*ch)
		n, _, ok := cl.Read(dst)
		if !ok || n != 20 {
			t.Fatalf("%d channels: read %d, %v", ch, n, ok)
		}
		if got, want := dst[:20*ch], framesOf(0, 20, ch); !slices.Equal(got, want) {
			t.Fatalf("%d channels: read %v, want %v", ch, got[:2*ch], want[:2*ch])
		}
	}
}

func TestADeviceNamedIsTargetedAndNotRemixed(t *testing.T) {
	f := newFakeServer()
	cl := openFake(t, f, Config{Record: true, Output: "the.sink", Input: "the.source", OutPositions: fourPositions, InPositions: fourPositions})
	_ = cl
	out, in := f.stream(t, directionOutput), f.stream(t, directionInput)
	if !out.has(`target.object="the.sink"`) || !out.has("stream.dont-remix=true") {
		t.Errorf("the output's properties: %s", out.props)
	}
	if !in.has(`target.object="the.source"`) || !in.has("stream.dont-remix=true") {
		t.Errorf("the input's properties: %s", in.props)
	}
	if !bytes.Equal(out.params, formatPod(48000, fourPositions)) {
		t.Errorf("the output offers %v", out.params)
	}
}

func TestTheDefaultDeviceIsNotTargeted(t *testing.T) {
	f := newFakeServer()
	openFake(t, f, Config{})
	if out := f.stream(t, directionOutput); out.has("target.object") || out.has("stream.dont-remix") {
		t.Errorf("the output's properties: %s", out.props)
	}
}
