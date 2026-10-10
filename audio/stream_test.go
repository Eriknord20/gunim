package audio

import (
	"errors"
	"io"
	"math/rand/v2"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// ramp is a sound whose every frame says which it is: frame f is f on
// the left and -f on the right. slow, where set, has each read wait,
// as a disk might; short ends it early, though Len says otherwise.
type ramp struct {
	n, at int64
	short int64
	slow  time.Duration
	reads atomic.Int64
}

func (r *ramp) Len() int64 { return r.n }

func (r *ramp) SeekFrame(f int64) error {
	r.at = f
	return nil
}

func (r *ramp) Read(dst []float32) (int, error) {
	r.reads.Add(1)
	if r.slow > 0 {
		time.Sleep(r.slow)
	}
	end := r.n
	if r.short > 0 {
		end = r.short
	}
	n := min(int64(len(dst)/2), end-r.at)
	for i := range n {
		dst[2*i], dst[2*i+1] = float32(r.at+i), -float32(r.at+i)
	}
	r.at += n
	if r.at >= end {
		return int(n), io.EOF
	}
	return int(n), nil
}

// readAt reads frames from c, waiting while none are decoded, until it
// has n or the sound ends.
func readAt(t *testing.T, c *Cursor, n int) []float32 {
	t.Helper()
	out := make([]float32, 0, 2*n)
	buf := make([]float32, 2*1000)
	for deadline := time.Now().Add(5 * time.Second); len(out) < 2*n; {
		got, err := c.Read(buf[:2*min(1000, n-len(out)/2)])
		out = append(out, buf[:2*got]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if got == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the stream decoded nothing in five seconds")
			}
			time.Sleep(time.Millisecond)
		}
	}
	return out
}

// checkRamp fails where frames are not the ramp's from frame from.
func checkRamp(t *testing.T, frames []float32, from int64) {
	t.Helper()
	for i := range len(frames) / 2 {
		want := float32(from + int64(i))
		if frames[2*i] != want || frames[2*i+1] != -want {
			t.Fatalf("frame %d reads %v, %v; want %v, %v", from+int64(i), frames[2*i], frames[2*i+1], want, -want)
		}
	}
}

func TestAStreamReadsTheSoundAsItIsAndEnds(t *testing.T) {
	src := &ramp{n: 50000}
	closed := false
	s, err := NewStream(src, StreamOptions{Rate: 48000, Close: func() { closed = true }})
	if err != nil {
		t.Fatal(err)
	}
	c := s.Cursor()
	got := readAt(t, c, 60000)
	if len(got)/2 != 50000 {
		t.Fatalf("read %d frames of 50000", len(got)/2)
	}
	checkRamp(t, got, 0)
	if n, err := c.Read(make([]float32, 2)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("past the end a read gives %d, %v", n, err)
	}
	// A seek back reads from there.
	_ = c.SeekFrame(12345)
	checkRamp(t, readAt(t, c, 3000), 12345)
	s.Close()
	if !closed {
		t.Fatal("closing the stream left the source open")
	}
}

func TestAStreamEndsWhereItsSoundStopsShort(t *testing.T) {
	s, err := NewStream(&ramp{n: 50000, short: 30000}, StreamOptions{Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := readAt(t, s.Cursor(), 50000)
	if len(got)/2 != 30000 {
		t.Fatalf("a sound that stops at 30000 frames read %d", len(got)/2)
	}
	checkRamp(t, got, 0)
}

func TestAStreamNotYetDecodedReadsNothingAndThenCatchesUp(t *testing.T) {
	src := &ramp{n: 1 << 20, slow: 20 * time.Millisecond}
	s, err := NewStream(src, StreamOptions{Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := s.Cursor()
	_ = c.SeekFrame(500000)
	if n, err := c.Read(make([]float32, 2*100)); n != 0 || err != nil {
		t.Fatalf("not yet decoded, a read gives %d, %v; want nothing, and no error", n, err)
	}
	if !c.WaitReady(100, 2*time.Second) {
		t.Fatal("the frames never came")
	}
	checkRamp(t, readAt(t, c, 100), 500000)
}

func TestAStreamReadsMakeNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	s, err := NewStream(&ramp{n: 1 << 20}, StreamOptions{Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := s.Cursor()
	if !c.WaitReady(48000, 2*time.Second) {
		t.Fatal("the frames never came")
	}
	dst := make([]float32, 2*1024)
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 40 {
		if i == 20 {
			_ = c.SeekFrame(100)
		}
		_, _ = c.Read(dst)
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("reading made %d things", n)
	}
}

// TestAStreamGivesTheRightFramesWhileItsPagesAreReused reads on one
// goroutine, as a mixer would, jumping each time to where a cursor
// just stood and closed, as the decoder takes those pages away and
// reuses them: every frame read must be the frame it is, and the pages
// made few.
func TestAStreamGivesTheRightFramesWhileItsPagesAreReused(t *testing.T) {
	s, err := NewStream(&ramp{n: 1 << 22}, StreamOptions{Rate: 48000, Ahead: 3 * pageFrames, Behind: pageFrames})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := s.Cursor()
	var hint atomic.Int64
	hint.Store(-1)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		dst := make([]float32, 2*pageFrames)
		read, last := 0, int64(-1)
		for {
			select {
			case <-stop:
				if read == 0 {
					done <- errors.New("nothing was read")
					return
				}
				done <- nil
				return
			default:
			}
			if h := hint.Load(); h != last {
				last = h
				_ = c.SeekFrame(h)
			}
			from := c.at.Load()
			n, err := c.Read(dst)
			if err != nil && !errors.Is(err, io.EOF) {
				done <- err
				return
			}
			for i := range n {
				want := float32(from + int64(i))
				if dst[2*i] != want || dst[2*i+1] != -want {
					done <- errors.New("a frame read was not the frame it is")
					return
				}
			}
			read += n
		}
	}()
	rng := rand.New(rand.NewPCG(1, 2))
	for range 400 {
		x := s.Cursor()
		at := rng.Int64N(1<<22-4*pageFrames) / pageFrames * pageFrames
		_ = x.SeekFrame(at)
		if !x.WaitReady(3*pageFrames, 2*time.Second) {
			t.Fatal("the frames never came")
		}
		x.Close()
		hint.Store(at)
		s.Wake()
		time.Sleep(200 * time.Microsecond)
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Pages are reused: far fewer made than the sound has.
	if n := s.pages.Load(); n > 200 {
		t.Fatalf("the stream made %d pages", n)
	}
}

// stalling has every other read give nothing, as a stream not yet
// decoded that far.
type stalling struct {
	Seeker
	n int
}

func (s *stalling) Read(dst []float32) (int, error) {
	s.n++
	if s.n%2 == 0 {
		return 0, nil
	}
	return s.Seeker.Read(dst[:min(len(dst), 2*37)])
}

func TestAResampledSoundWaitsForASourceWithNothingYet(t *testing.T) {
	src := func() Seeker { return &ramp{n: 20000} }
	want := readResampled(t, Resample(src(), 44100, 48000))
	got := readResampled(t, Resample(&stalling{Seeker: src()}, 44100, 48000))
	if len(got) != len(want) {
		t.Fatalf("waiting, the sound is %d frames; without, %d", len(got)/2, len(want)/2)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waiting, sample %d is %v; without, %v", i, got[i], want[i])
		}
	}
}

// readResampled reads s to its end, a little at a time, as a mixer.
func readResampled(t *testing.T, s Seeker) []float32 {
	t.Helper()
	var out []float32
	buf := make([]float32, 2*128)
	for range 100000 {
		n, err := s.Read(buf)
		out = append(out, buf[:2*n]...)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("the sound never ended")
	return nil
}

// TestAPageTakenAwayIsReusedOnlyOnceNoReadCanCopyIt plays a read and
// the decoder step by step: a page the decoder takes away while a read
// holds it must not be decoded into until that read has finished.
func TestAPageTakenAwayIsReusedOnlyOnceNoReadCanCopyIt(t *testing.T) {
	s := &Stream{table: make([]atomic.Pointer[page], 1)}
	held := &page{data: make([]float32, 2*pageFrames)}
	s.table[0].Store(held)
	// A read starts, and takes the page.
	s.entered.Add(1)
	if s.table[0].Load() != held {
		t.Fatal("the read found no page")
	}
	// The decoder takes it away, and wants a page to decode into.
	retiring := []retired{{p: s.table[0].Swap(nil), entered: s.entered.Load()}}
	if p := s.pageFor(&retiring); p == held {
		t.Fatal("the page was reused while a read held it")
	}
	// The read finishes: now the page is free to reuse.
	s.left.Add(1)
	if p := s.pageFor(&retiring); p != held {
		t.Fatal("the page was not reused once the read finished")
	}
}
