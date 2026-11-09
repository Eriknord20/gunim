//go:build js || android

package speaker

import (
	"encoding/binary"
	"io"
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/marrasen/oto/v3"

	"github.com/marrasen/gunim/audio"
)

// otoOutput plays through the system's own sound, by oto: a player
// that reads the mixer into a buffer of its own whenever the buffer
// has room, and a device that plays from it.
type otoOutput struct {
	s      *Speaker
	ctx    *oto.Context
	player *oto.Player
	hz     int
	// buffer is the player's buffer, in frames.
	buffer atomic.Int64
	// buf is Read's frames; the player reads on one goroutine.
	buf []float32
}

// openSystemSound opens the system's own sound by oto: in the browser,
// and on Android 8.0, which has no AAudio. A test puts a function of
// its own here.
var openSystemSound = func(s *Speaker, o Options) (startedOutput, error) { return openOto(s, o) }

func openOto(s *Speaker, o Options) (*otoOutput, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:      o.rate(),
		ChannelCount:    2,
		Format:          oto.FormatFloat32LE,
		BufferSize:      device,
		ApplicationName: o.Name,
	})
	if err != nil {
		return nil, err
	}
	// A browser holds its sound back until the first click, key or touch, and ready
	// waits for that. So Open returns at once there, and what plays meanwhile
	// sounds from that first gesture on: a cue set up after Open is in place for the
	// click that lets sound start.
	if runtime.GOOS != "js" {
		<-ready
		if err := ctx.Err(); err != nil {
			_ = ctx.Close()
			return nil, err
		}
	}
	out := &otoOutput{s: s, ctx: ctx, hz: o.rate()}
	out.player = ctx.NewPlayer(out)
	return out, nil
}

// start starts the player, working ahead as far as ahead.
func (o *otoOutput) start(ahead time.Duration) {
	o.setAhead(ahead)
	o.player.Play()
}

// Read hands the player the mixer's sound a chunk at a time, and
// counts the times the player ran dry.
func (o *otoOutput) Read(p []byte) (int, error) {
	if o.s.reads.Add(1) > 20 && o.player.BufferedSize() == 0 {
		o.s.ranDry()
	}
	n := min(len(p)/8, int(audio.FramesAt(chunk, o.hz)))
	if n == 0 {
		return 0, io.ErrShortBuffer
	}
	if cap(o.buf) < 2*n {
		o.buf = make([]float32, 2*n)
	}
	frames := o.buf[:2*n]
	o.s.pull(frames)
	for i, v := range frames {
		binary.LittleEndian.PutUint32(p[4*i:], math.Float32bits(v))
	}
	return 8 * n, nil
}

func (o *otoOutput) rate() int { return o.hz }

// output returns how long the sound past the player's buffer takes to
// be heard: as the system reports it, where it does, which on Android
// counts a Bluetooth headset's own delay; or device.
func (o *otoOutput) output() time.Duration {
	if d, ok := o.ctx.OutputLatency(); ok {
		return d
	}
	return device
}

func (o *otoOutput) held() int64 {
	return int64(o.player.BufferedSize()/8) + audio.FramesAt(o.output(), o.hz)
}

func (o *otoOutput) setAhead(d time.Duration) {
	frames := audio.FramesAt(max(d-o.output(), device), o.hz)
	o.buffer.Store(frames)
	o.player.SetBufferSize(int(frames) * 8)
}

func (o *otoOutput) latency() time.Duration {
	return o.output() + audio.DurationAt(o.buffer.Load(), o.hz)
}

func (o *otoOutput) fill(st *State) {
	if r, ok := o.ctx.DeviceSampleRate(); ok && r != o.hz {
		st.DeviceRate = r
	}
}

func (o *otoOutput) err() error {
	if err := o.player.Err(); err != nil {
		return err
	}
	return o.ctx.Err()
}

func (o *otoOutput) suspend() error { return o.ctx.Suspend() }
func (o *otoOutput) resume() error  { return o.ctx.Resume() }

func (o *otoOutput) close() error {
	o.player.Pause()
	if err := o.ctx.Close(); err != nil {
		// Open still, as where the system closes none: it plays on.
		o.player.Play()
		return err
	}
	return nil
}
