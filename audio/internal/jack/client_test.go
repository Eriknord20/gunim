//go:build linux && !android && (amd64 || arm64)

package jack

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openFake(t *testing.T, f *fakeServer, cfg Config) *Client {
	t.Helper()
	f.use(t)
	if cfg.Hold == 0 {
		cfg.Hold = 100 * time.Millisecond
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

// Each buffer the server asks for plays the ring's next frames on the
// ports, one channel each, and the ring's end wraps; a ring run dry
// plays the rest silent and counts the buffer short.
func TestProcessPlaysTheRing(t *testing.T) {
	f := newFakeServer(48000, 64, []string{"system:playback_1", "system:playback_2"}, nil)
	cl := openFake(t, f, Config{})
	if cl.Channels() != 2 {
		t.Fatalf("channels = %d, want 2", cl.Channels())
	}
	size := int(cl.size)
	// Round the ring more than twice, a buffer ahead each time, so each
	// buffer finds the frames before it taken.
	next := 0
	for c := 0; next < 2*size+1000; c++ {
		if w := cl.Write(ramp(next, 64)); w != 64 {
			t.Fatalf("cycle %d: wrote %d of 64", c, w)
		}
		next += 64
		out := f.cycle(t, 64)
		for i := range 64 {
			want := float32(next - 64 + i)
			if out[0][i] != want || out[1][i] != -want {
				t.Fatalf("cycle %d frame %d: played %v %v, want %v", c, i, out[0][i], out[1][i], want)
			}
		}
		if q := cl.Queued(); q != 0 {
			t.Fatalf("cycle %d: %d frames left queued", c, q)
		}
	}
	if e := cl.Poll(); e.Short != 0 {
		t.Fatalf("%d buffers short while the ring kept up", e.Short)
	}
	// Half a buffer: half plays, the rest is silence, and the buffer is
	// short.
	cl.Write(ramp(next, 32))
	out := f.cycle(t, 64)
	if out[0][31] != float32(next+31) || out[0][32] != 0 || out[1][63] != 0 {
		t.Fatalf("a short buffer played %v", out[0])
	}
	e := cl.Poll()
	if e.Short != 1 {
		t.Fatalf("short = %d, want 1", e.Short)
	}
	if got := cl.Taken(); got != int64(next+32) {
		t.Fatalf("taken = %d, want %d", got, next+32)
	}
}

// The ring takes no more than it holds, and gives the frames back in
// order as the server takes them.
func TestWriteStopsAtTheRingsSize(t *testing.T) {
	f := newFakeServer(48000, 256, []string{"a", "b"}, nil)
	cl := openFake(t, f, Config{})
	big := ramp(0, int(cl.size)+500)
	if n := cl.Write(big); n != int(cl.size) {
		t.Fatalf("wrote %d, want the ring's %d", n, cl.size)
	}
	if cl.Room() != 0 {
		t.Fatalf("room = %d, want 0", cl.Room())
	}
	f.cycle(t, 256)
	if cl.Room() != 256 {
		t.Fatalf("room = %d after a buffer, want 256", cl.Room())
	}
}

// Paused, the ports play silence, the ring keeps its frames, and no
// buffer counts short.
func TestPausePlaysSilence(t *testing.T) {
	f := newFakeServer(48000, 64, []string{"a", "b"}, nil)
	cl := openFake(t, f, Config{})
	cl.Write(ramp(1, 128))
	cl.Pause(true)
	out := f.cycle(t, 64)
	if slices.ContainsFunc(out[0], func(v float32) bool { return v != 0 }) {
		t.Fatal("paused, the ports played sound")
	}
	if cl.Queued() != 128 {
		t.Fatalf("paused, the ring gave frames: %d queued", cl.Queued())
	}
	cl.Pause(false)
	out = f.cycle(t, 64)
	if out[0][0] != 1 {
		t.Fatalf("resumed, played %v first, want 1", out[0][0])
	}
	if e := cl.Poll(); e.Short != 0 {
		t.Fatalf("short = %d", e.Short)
	}
}

// What the inputs record comes back in order, interleaved, each piece
// with the output frame taken at the same moment.
func TestReadStampsInputs(t *testing.T) {
	f := newFakeServer(48000, 64, []string{"out1", "out2"}, []string{"cap1", "cap2", "cap3"})
	cl := openFake(t, f, Config{Record: true})
	if cl.Inputs() != 3 {
		t.Fatalf("inputs = %d, want 3", cl.Inputs())
	}
	buf := make([]float32, 3*40)
	frame := 0
	for c := range 20 {
		// The system's inputs hold frame numbers, channel c times 1000.
		f.mu.Lock()
		for k, name := range f.physIn {
			_, p := f.byName(name)
			for i := range 64 {
				p.buf[i] = float32((k+1)*1000000 + frame + i)
			}
		}
		f.mu.Unlock()
		taken := cl.Taken()
		cl.Write(ramp(0, 64))
		f.cycle(t, 64)
		// Read in pieces of 40 frames: 40, then 24.
		got := 0
		for {
			n, at, ok := cl.Read(buf)
			if !ok {
				break
			}
			if at != taken+int64(got) {
				t.Fatalf("cycle %d: stamped %d, want %d", c, at, taken+int64(got))
			}
			for i := range n {
				for k := range 3 {
					if want := float32((k+1)*1000000 + frame + got + i); buf[3*i+k] != want {
						t.Fatalf("cycle %d frame %d channel %d: %v, want %v", c, got+i, k, buf[3*i+k], want)
					}
				}
			}
			got += n
		}
		if got != 64 {
			t.Fatalf("cycle %d: read %d frames, want 64", c, got)
		}
		frame += 64
	}
	if e := cl.Poll(); e.Lost != 0 {
		t.Fatalf("lost %d", e.Lost)
	}
}

// Inputs nobody reads fill the ring, and the rest are counted lost; a
// reader that falls behind the log picks up again from what it holds.
func TestInputsLostAndFoundAgain(t *testing.T) {
	f := newFakeServer(48000, 1024, []string{"o"}, []string{"cap"})
	cl := openFake(t, f, Config{Record: true})
	cycles := int(cl.size)/1024 + logSize + 5
	for range cycles {
		f.cycle(t, 1024)
	}
	e := cl.Poll()
	if want := int64(cycles*1024) - int64(cl.size); e.Lost != want {
		t.Fatalf("lost %d, want %d", e.Lost, want)
	}
	buf := make([]float32, 4096)
	var last int64 = -1
	total := 0
	for {
		n, at, ok := cl.Read(buf)
		if !ok {
			break
		}
		if at <= last {
			t.Fatalf("stamps went back: %d after %d", at, last)
		}
		last = at
		total += n
	}
	if total > int(cl.size) {
		t.Fatalf("read %d frames, more than the ring holds", total)
	}
	// Now caught up, the next buffer reads whole.
	f.cycle(t, 1024)
	got := 0
	for {
		n, _, ok := cl.Read(buf)
		if !ok {
			break
		}
		got += n
	}
	if got != 1024 {
		t.Fatalf("read %d after catching up, want 1024", got)
	}
}

// The server's other callbacks reach Poll: xruns counted, the buffer
// size and rate told, the graph changing, and the server shutting down.
func TestCallbacksReachPoll(t *testing.T) {
	f := newFakeServer(48000, 256, []string{"out1", "out2"}, nil)
	f.lat["out1"] = latencyRange{256, 512}
	cl := openFake(t, f, Config{})
	if out, _ := cl.Latency(); out != 512 {
		t.Fatalf("latency = %d, want 512", out)
	}
	f.call(t, "xrun")
	f.call(t, "xrun")
	f.mu.Lock()
	f.lat["out1"] = latencyRange{128, 128}
	f.mu.Unlock()
	f.call(t, "bufsize", 128)
	e := cl.Poll()
	if e.Xruns != 2 || e.Buffer != 128 || !e.Graph {
		t.Fatalf("events = %+v, want 2 xruns, a buffer of 128, the graph read again", e)
	}
	if cl.Buffer() != 128 || cl.Info().Buffer != 128 {
		t.Fatalf("buffer = %d, info %d, want 128", cl.Buffer(), cl.Info().Buffer)
	}
	if out, _ := cl.Latency(); out != 128 {
		t.Fatalf("latency = %d after the buffer changed, want 128", out)
	}
	if e := cl.Poll(); e.Xruns != 0 || e.Graph {
		t.Fatalf("a second poll told again: %+v", e)
	}
	f.mu.Lock()
	f.lat["out2"] = latencyRange{900, 1000}
	f.mu.Unlock()
	f.call(t, "graph")
	if e := cl.Poll(); !e.Graph {
		t.Fatal("the graph's change went untold")
	}
	if out, _ := cl.Latency(); out != 1000 {
		t.Fatalf("latency = %d after the graph changed, want 1000", out)
	}
	f.call(t, "rate", 44100)
	if e := cl.Poll(); e.Rate != 44100 {
		t.Fatalf("rate = %d, want 44100", e.Rate)
	}
	f.mu.Lock()
	f.since = 17
	f.mu.Unlock()
	if s := cl.SinceCycle(); s != 17 {
		t.Fatalf("since = %d, want 17", s)
	}
	f.call(t, "shutdown")
	if e := cl.Poll(); !e.Shutdown {
		t.Fatal("the shutdown went untold")
	}
	if !errors.Is(cl.Err(), ErrShutdown) {
		t.Fatalf("err = %v, want ErrShutdown", cl.Err())
	}
	if s := cl.SinceCycle(); s != 0 {
		t.Fatalf("since = %d after shutdown, want 0", s)
	}
}

// Ports connect to the system's ports Outputs names, and inputs to
// every system input; the latencies are the largest of those connected.
func TestConnectsToTheSystemsPorts(t *testing.T) {
	f := newFakeServer(48000, 128, []string{"p1", "p2", "p3", "p4"}, []string{"c1", "c2"})
	f.lat["p3"] = latencyRange{100, 300}
	f.lat["p4"] = latencyRange{100, 200}
	f.lat["c2/capture"] = latencyRange{150, 160}
	f.refuseLink = map[string]bool{"p2": true}
	cl := openFake(t, f, Config{Name: "my:app", Outputs: []int{2, 3, 9, 2, 1}, Record: true})
	info := cl.Info()
	if want := []int{2, 3, -1, -1, -1}; !slices.Equal(info.Played, want) {
		t.Fatalf("played = %v, want %v", info.Played, want)
	}
	if info.Latency != 300 || info.InputLatency != 160 {
		t.Fatalf("latencies = %d, %d, want 300, 160", info.Latency, info.InputLatency)
	}
	if info.Recorded != 2 || cl.Channels() != 5 {
		t.Fatalf("recorded %d, channels %d, want 2, 5", info.Recorded, cl.Channels())
	}
	want := [][2]string{{"app:out_1", "p3"}, {"app:out_2", "p4"}, {"c1", "app:in_1"}, {"c2", "app:in_2"}}
	if !slices.Equal(f.links, want) {
		t.Fatalf("links = %v, want %v", f.links, want)
	}
}

func TestLayout(t *testing.T) {
	for _, c := range []struct {
		want []int
		outs int
		got  []int
	}{
		{nil, 0, []int{-1, -1}},
		{nil, 1, []int{0}},
		{nil, 8, []int{0, 1}},
		{[]int{1}, 2, []int{1}},
		{[]int{1, 1, -1, 5}, 4, []int{1, -1, -1, -1}},
	} {
		if got := layout(c.want, c.outs); !slices.Equal(got, c.got) {
			t.Errorf("layout(%v, %d) = %v, want %v", c.want, c.outs, got, c.got)
		}
	}
}

func TestClientName(t *testing.T) {
	if got := clientName("a:b"); got != "a-b" {
		t.Errorf("clientName(a:b) = %q", got)
	}
	long := strings.Repeat("å", 40)
	got := clientName(long)
	if len(got) > nameSize || !strings.HasPrefix(long, got) {
		t.Errorf("clientName cut %q to %q", long, got)
	}
	if clientName("") == "" {
		t.Error("an empty name stayed empty")
	}
}

// Without a server, Open fails, and Available says so.
func TestNoServer(t *testing.T) {
	f := newFakeServer(48000, 128, nil, nil)
	f.refuse = true
	f.use(t)
	if Available() {
		t.Fatal("available with no server")
	}
	if _, err := Open(Config{}); !errors.Is(err, errNoServer) {
		t.Fatalf("err = %v, want errNoServer", err)
	}
}

// Close deactivates and closes the client once.
func TestClose(t *testing.T) {
	f := newFakeServer(48000, 128, []string{"a", "b"}, nil)
	cl := openFake(t, f, Config{})
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	_ = cl.Close()
	if f.closes != 1 || f.active {
		t.Fatalf("closes = %d, active = %v", f.closes, f.active)
	}
}

// The server's thread and the goroutine that fills and empties the
// rings run at once: every frame written plays once, in order, and
// every frame recorded comes back once, in order, with its stamp.
func TestRingsAcrossThreads(t *testing.T) {
	f := newFakeServer(48000, 64, []string{"o1", "o2"}, []string{"c1"})
	cl := openFake(t, f, Config{Record: true})
	const cycles = 3000
	done := make(chan []float32)
	go func() {
		// The server: the input counts frames, the output is kept.
		played := make([]float32, 0, 64*cycles)
		for c := range cycles {
			f.mu.Lock()
			_, p := f.byName("c1")
			for i := range 64 {
				p.buf[i] = float32(c*64 + i)
			}
			f.mu.Unlock()
			out, err := f.run(64)
			if err != nil {
				t.Error(err)
				break
			}
			played = append(played, out[0]...)
			if c%16 == 0 {
				time.Sleep(50 * time.Microsecond)
			}
		}
		done <- played
	}()
	next, read := 0, 0
	buf := make([]float32, 100)
	var played []float32
	for played == nil {
		select {
		case played = <-done:
		default:
		}
		if cl.Queued() < 4096 {
			next += cl.Write(ramp(next+1, 256))
		}
		for {
			n, at, ok := cl.Read(buf)
			if !ok {
				break
			}
			for i := range n {
				if buf[i] != float32(read+i) {
					t.Fatalf("input frame %d read as %v", read+i, buf[i])
				}
			}
			// The output frame taken as frame read came in is never
			// ahead of what was taken.
			if at > cl.Taken() {
				t.Fatalf("stamped %d, ahead of the %d taken", at, cl.Taken())
			}
			read += n
		}
	}
	// Played frames count up from 1, with silence only where the ring
	// ran dry, never out of order.
	last := float32(0)
	for i, v := range played {
		if v == 0 {
			continue
		}
		if v != last+1 {
			t.Fatalf("frame %d played %v after %v", i, v, last)
		}
		last = v
	}
	if last == 0 || read == 0 {
		t.Fatalf("played up to %v, read %d", last, read)
	}
}

// Outputs naming none fails to open, and lets the client go.
func TestNoOutputsFails(t *testing.T) {
	f := newFakeServer(48000, 128, []string{"a", "b"}, nil)
	f.use(t)
	if _, err := Open(Config{Outputs: []int{}}); err == nil {
		t.Fatal("opened with no outputs")
	}
	if f.closes != 1 {
		t.Fatalf("closes = %d, want 1", f.closes)
	}
}

// SinceCycle, called from any goroutine, never reaches libjack once the
// client is closed: Close waits out a call in libjack before it lets the
// client go, and calls after return 0 at once.
func TestSinceCycleNeverOutlivesTheClient(t *testing.T) {
	f := newFakeServer(48000, 128, []string{"a", "b"}, nil)
	cl := openFake(t, f, Config{})
	var gone atomic.Bool
	inside := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	cl.a.sinceCycle = func(uintptr) uint32 {
		if gone.Load() {
			t.Error("jack_frames_since_cycle_start called on a closed client")
		}
		once.Do(func() {
			close(inside)
			<-release
		})
		return 7
	}
	close0 := cl.a.clientClose
	cl.a.clientClose = func(c uintptr) int32 {
		gone.Store(true)
		return close0(c)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					cl.SinceCycle()
				}
			}
		}()
	}
	<-inside
	closed := make(chan error)
	go func() { closed <- cl.Close() }()
	select {
	case <-closed:
		t.Fatal("Close let the client go while SinceCycle was in libjack")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if got := cl.SinceCycle(); got != 0 {
		t.Errorf("SinceCycle = %d after Close, want 0", got)
	}
	close(stop)
	wg.Wait()
}
