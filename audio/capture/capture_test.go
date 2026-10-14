package capture

import (
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio/asio"
	"github.com/marrasen/gunim/audio/internal/duplex"
	"github.com/marrasen/gunim/audio/speaker"
)

// fakeDevice stands in for a device: the test hands the stream its
// blocks, as the device's thread would.
type fakeDevice struct {
	started, closed bool
}

func (d *fakeDevice) start() error { d.started = true; return nil }
func (d *fakeDevice) close() error { d.closed = true; return nil }

// fakeClock stands in for a speaker, heard at frame now.
type fakeClock struct{ now atomic.Int64 }

func (c *fakeClock) Heard() int64           { return c.now.Load() }
func (c *fakeClock) Latency() time.Duration { return 10 * time.Millisecond }

// openFake opens a stream on a fake device of f, counting by clock
// where it is set, at mixRate.
func openFake(t *testing.T, o Options, f format, clock clock, mixRate int) (*Stream, *fakeDevice) {
	t.Helper()
	d := &fakeDevice{}
	s := &Stream{o: o, done: make(chan struct{}), ratio: 1, clock: clock, b: d}
	if err := s.ready(f, mixRate); err != nil {
		t.Fatal(err)
	}
	if !d.started {
		t.Fatal("the device never started")
	}
	return s, d
}

// frames returns n frames of ch channels, each sample from, from frame
// first on, as sample ch*1000 + frame: the channel and frame it is.
func frames(first, n, ch int) []float32 {
	out := make([]float32, n*ch)
	for i := range n {
		for c := range ch {
			out[i*ch+c] = float32(c*1000 + first + i)
		}
	}
	return out
}

// TestReadGivesThePickedChannelsStamped records three channels, keeps
// the third and the first, and reads them back in pieces smaller than
// the device's blocks: every frame comes once, in order, with the
// frame count and the stamp of its own place.
func TestReadGivesThePickedChannelsStamped(t *testing.T) {
	s, _ := openFake(t, Options{Channels: []int{2, 0}}, format{name: "Fake", channels: 3, rate: 48000, buffer: 64}, &fakeClock{}, 48000)
	if got := s.Info().Channels; got != 2 {
		t.Fatalf("the stream records %d channels, want 2", got)
	}
	for blk := range 4 {
		s.putAt(frames(blk*64, 64, 3), int64(5000+blk*64))
	}
	dst := make([]float32, 2*24)
	next := 0
	for {
		b := s.Read(dst)
		if b.Len() == 0 {
			break
		}
		if b.Frame != int64(next) || b.Heard != int64(5000+next) {
			t.Fatalf("a block at frame %d came as frame %d, heard %d", next, b.Frame, b.Heard)
		}
		for i := range b.Len() {
			if l, r := b.Frames[2*i], b.Frames[2*i+1]; l != float32(2000+next+i) || r != float32(next+i) {
				t.Fatalf("frame %d holds %v %v", next+i, l, r)
			}
		}
		next += b.Len()
	}
	if next != 256 {
		t.Fatalf("read %d frames of 256", next)
	}
}

// TestAFullRingDropsAndTheGapShows fills the ring past what it holds:
// the block that does not fit is dropped whole and counted, and the
// next block read shows the gap in its frame count and stamp.
func TestAFullRingDropsAndTheGapShows(t *testing.T) {
	s, _ := openFake(t, Options{Hold: time.Millisecond}, format{name: "Fake", channels: 1, rate: 48000, buffer: 100}, &fakeClock{}, 48000)
	size := int(s.q.size)
	block := 100
	put := 0
	for put+block <= size {
		s.putAt(frames(put, block, 1), int64(put))
		put += block
	}
	s.putAt(frames(put, block, 1), int64(put)) // no room
	if s.Overruns() != 1 {
		t.Fatalf("%d overruns, want 1", s.Overruns())
	}
	dst := make([]float32, size)
	got := 0
	for b := s.Read(dst); b.Len() > 0; b = s.Read(dst) {
		got += b.Len()
	}
	if got != put {
		t.Fatalf("read %d frames, want %d", got, put)
	}
	s.putAt(frames(put+block, block, 1), int64(put+block))
	b := s.Read(dst)
	if b.Frame != int64(put+block) || b.Heard != int64(put+block) || b.Frames[0] != float32(put+block) {
		t.Fatalf("the block after the gap came as frame %d heard %d holding %v; want frame %d", b.Frame, b.Heard, b.Frames[0], put+block)
	}
}

// TestOnBlockHandsLongBlocksInPieces hands a block longer than
// maxBlock to OnBlock, which hears it in pieces, each stamped where it
// starts, at a mixer twice the stream's rate.
func TestOnBlockHandsLongBlocksInPieces(t *testing.T) {
	var pieces []Block
	o := Options{OnBlock: func(b Block) {
		c := b
		c.Frames = append([]float32(nil), b.Frames...)
		pieces = append(pieces, c)
	}}
	s, _ := openFake(t, o, format{name: "Fake", channels: 2, rate: 24000, buffer: 64}, &fakeClock{}, 48000)
	n := maxBlock + 100
	s.putAt(frames(0, n, 2), 1000)
	if len(pieces) != 2 || pieces[0].Len() != maxBlock || pieces[1].Len() != 100 {
		t.Fatalf("OnBlock heard %d pieces", len(pieces))
	}
	if p := pieces[1]; p.Frame != maxBlock || p.Heard != 1000+2*maxBlock || p.Frames[1] != float32(1000+maxBlock) {
		t.Fatalf("the second piece came as frame %d, heard %d, holding %v", p.Frame, p.Heard, p.Frames[:2])
	}
}

// TestStampsWithoutASpeakerAreUnknown opens a stream without a
// speaker: its blocks carry -1 as their stamp.
func TestStampsWithoutASpeakerAreUnknown(t *testing.T) {
	s, _ := openFake(t, Options{}, format{name: "Fake", channels: 1, rate: 48000, buffer: 64}, nil, 0)
	s.putAgo(frames(0, 64, 1), 100)
	s.putAt(frames(64, 64, 1), 100)
	dst := make([]float32, 256)
	for b := s.Read(dst); b.Len() > 0; b = s.Read(dst) {
		if b.Heard != -1 {
			t.Fatalf("a block at frame %d is stamped %d", b.Frame, b.Heard)
		}
	}
}

// TestReckonedStampsRideOutJitter hands blocks on late by up to two
// milliseconds, as a system's thread does, and starts on a late one.
// After their first seconds the stamps sit where the soonest blocks
// put them, and step by no more than a frame or two from one block to
// the next. The two clocks then drift apart by 50 parts in a million,
// and the stamps follow within 20 frames. Then the speaker opens again
// and its frames jump: the stamps follow at once.
func TestReckonedStampsRideOutJitter(t *testing.T) {
	clock := &fakeClock{}
	s, _ := openFake(t, Options{}, format{name: "Fake", channels: 1, rate: 48000, buffer: 480}, clock, 48000)
	const latency = 960 // frames between the input and the block handed on
	in := frames(0, 480, 1)
	var last, drift int64
	jitter := []int64{61, 37, 96, 12, 0, 80, 3, 45}
	const blocks = 3000
	for blk := range blocks {
		frame := int64(blk * 480)
		if blk > 1000 {
			// 2.4 frames a second.
			drift = int64(blk-1000) * 480 * 50 / 1000000
		}
		// The speaker heard frame+100000+drift as frame reached the
		// input.
		clock.now.Store(100000 + drift + frame + latency + jitter[blk%len(jitter)])
		s.putAgo(in, latency)
		b := s.Read(make([]float32, 480))
		if b.Frame != frame {
			t.Fatalf("block %d came as frame %d", blk, b.Frame)
		}
		want := 100000 + drift + frame
		if blk > 500 && math.Abs(float64(b.Heard-want)) > 20 {
			t.Fatalf("block %d stamped %d, %d from where it sounded", blk, b.Heard, b.Heard-want)
		}
		if step := b.Heard - last - 480; blk > 500 && (step > 2 || step < -2) {
			t.Fatalf("block %d's stamp stepped %d frames", blk, step)
		}
		last = b.Heard
	}
	frame := int64(blocks * 480)
	clock.now.Store(500000 + frame + latency)
	s.putAgo(in, latency)
	if b := s.Read(make([]float32, 480)); b.Heard != 500000+frame {
		t.Fatalf("after the jump the block was stamped %d, want %d", b.Heard, 500000+frame)
	}
}

// TestRecordingAllocatesNothing records into the ring and into
// OnBlock, with reckoned stamps and exact ones.
func TestRecordingAllocatesNothing(t *testing.T) {
	clock := &fakeClock{}
	ring, _ := openFake(t, Options{Channels: []int{1, 0}}, format{name: "Fake", channels: 2, rate: 48000, buffer: 256}, clock, 44100)
	heard := 0
	call, _ := openFake(t, Options{OnBlock: func(b Block) { heard += b.Len() }}, format{name: "Fake", channels: 2, rate: 48000, buffer: 256}, clock, 48000)
	in := frames(0, 256, 2)
	dst := make([]float32, 512)
	if n := testing.AllocsPerRun(200, func() {
		clock.now.Add(256)
		ring.putAgo(in, 300)
		ring.putAt(in, clock.now.Load())
		ring.Read(dst)
		ring.Read(dst)
		call.putAgo(in, 300)
		call.putAt(in, 1)
	}); n != 0 {
		t.Errorf("recording allocates %v times a block", n)
	}
}

// TestALostDeviceEndsTheStream has the device go away: Done closes and
// Err says it was lost; Close after still lets the device go.
func TestALostDeviceEndsTheStream(t *testing.T) {
	s, d := openFake(t, Options{}, format{name: "Fake", channels: 1, rate: 48000, buffer: 64}, nil, 0)
	select {
	case <-s.Done():
		t.Fatal("Done closed before the device went")
	default:
	}
	s.lose(ErrLost)
	<-s.Done()
	if !errors.Is(s.Err(), ErrLost) {
		t.Fatalf("Err = %v, want ErrLost", s.Err())
	}
	if err := s.Close(); err != nil || !d.closed {
		t.Fatalf("Close = %v, closed %v", err, d.closed)
	}
}

// TestAChannelPastTheDeviceIsRefused asks for a channel the device
// lacks, and for none.
func TestAChannelPastTheDeviceIsRefused(t *testing.T) {
	for _, chans := range [][]int{{0, 2}, {-1}, {}} {
		d := &fakeDevice{}
		s := &Stream{o: Options{Channels: chans}, done: make(chan struct{}), ratio: 1, b: d}
		if err := s.ready(format{name: "Fake", channels: 2, rate: 48000, buffer: 64}, 0); err == nil || !d.closed {
			t.Errorf("channels %v: err %v, device closed %v", chans, err, d.closed)
		}
	}
}

// TestASIOInputsRecordThroughTheTap publishes a driver's inputs, as the
// speaker does: Devices lists them, a stream records them with the
// stamps the driver's buffers give, a second stream records them too,
// on after the first closes, and the driver closing ends it.
func TestASIOInputsRecordThroughTheTap(t *testing.T) {
	tap := duplex.NewTap("Fake ASIO", 48000, 128, 200, []string{"In 1", "In 2", "In 3"})
	duplex.Publish(tap)
	defer duplex.Withdraw(tap)
	devs, _ := Devices()
	if len(devs) == 0 || devs[0].ID != "asio:Fake ASIO" || devs[0].Channels != 3 || devs[0].ChannelNames[2] != "In 3" {
		t.Fatalf("Devices = %+v", devs)
	}
	clock := &fakeClock{}
	s := &Stream{o: Options{Channels: []int{1}}, done: make(chan struct{}), ratio: 1, clock: clock}
	f, err := openTap(s, "Fake ASIO")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ready(f, 48000); err != nil {
		t.Fatal(err)
	}
	if !s.Info().Exact || s.Info().Latency != 200*time.Second/48000 {
		t.Fatalf("Info = %+v", s.Info())
	}
	tap.Deliver(frames(0, 128, 3), 3, 7777)
	b := s.Read(make([]float32, 256))
	if b.Len() != 128 || b.Heard != 7777 || b.Frames[5] != 1005 {
		t.Fatalf("read %d frames heard %d holding %v", b.Len(), b.Heard, b.Frames[5])
	}
	other := &Stream{o: Options{Channels: []int{2}}, done: make(chan struct{}), ratio: 1, clock: clock}
	f, err = openTap(other, "Fake ASIO")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.ready(f, 48000); err != nil {
		t.Fatalf("a second stream could not record the inputs: %v", err)
	}
	tap.Deliver(frames(128, 128, 3), 3, 8888)
	if b := s.Read(make([]float32, 256)); b.Len() != 128 || b.Heard != 8888 {
		t.Fatalf("beside a second stream, the first read %d frames heard %d", b.Len(), b.Heard)
	}
	if b := other.Read(make([]float32, 256)); b.Len() != 128 || b.Heard != 8888 || b.Frames[5] != 2133 {
		t.Fatalf("the second stream read %d frames heard %d holding %v", b.Len(), b.Heard, b.Frames[5])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	tap.Deliver(frames(256, 128, 3), 3, 9999)
	if b := other.Read(make([]float32, 256)); b.Len() != 128 || b.Heard != 9999 {
		t.Fatalf("with the first closed, the second read %d frames heard %d", b.Len(), b.Heard)
	}
	duplex.Withdraw(tap)
	select {
	case <-other.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the stream recorded on after the driver closed")
	}
	if !errors.Is(other.Err(), ErrLost) {
		t.Fatalf("Err = %v", other.Err())
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDriverInputsListsTheDriversInputsBeforeTheyOpen(t *testing.T) {
	if _, ok := driverDevice(speaker.State{Rate: 48000}, nil); ok {
		t.Fatal("the system's own sound lists driver inputs")
	}
	st := speaker.State{Driver: "MOTU", Rate: 48000, ASIO: &asio.Info{Driver: "MOTU", Inputs: 4}}
	d, ok := driverDevice(st, nil)
	if !ok || d.ID != asioPrefix+"MOTU" || d.Channels != 4 || d.Driver != "MOTU" || len(d.ChannelNames) != 0 {
		t.Fatalf("before the inputs open: %+v, %v", d, ok)
	}
	// Open, the tap names them.
	tap := duplex.NewTap("MOTU", 48000, 64, 32, []string{"In 1", "In 2"})
	d, ok = driverDevice(st, tap)
	if !ok || d.Channels != 2 || d.ChannelNames[1] != "In 2" {
		t.Fatalf("with the inputs open: %+v, %v", d, ok)
	}
	// Another driver's tap, from before the speaker changed, names none.
	d, _ = driverDevice(st, duplex.NewTap("Other", 44100, 64, 32, []string{"A"}))
	if d.Channels != 4 {
		t.Fatalf("another driver's tap: %+v", d)
	}
	if _, ok := driverDevice(speaker.State{Driver: "Out only", ASIO: &asio.Info{}}, nil); ok {
		t.Fatal("a driver with no inputs lists them")
	}
}
