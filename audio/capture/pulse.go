//go:build (linux && !android) || freebsd || netbsd || openbsd

package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"

	"github.com/marrasen/gunim/audio"
)

// The system's inputs are PulseAudio's sources, or PipeWire's through
// its PulseAudio server. The package records them with the client oto
// plays through, github.com/jfreymuth/pulse, which speaks PulseAudio's
// protocol in Go over the server's socket: one path for both servers,
// with no C library loaded, and oto unchanged. oto's other Linux
// driver, ALSA, serves a system with neither server; recording there
// waits for a need.

// pulseIn records a PulseAudio source.
type pulseIn struct {
	s      *Stream
	c      *pulse.Client
	st     *pulse.RecordStream
	ch     int
	source uint32
	// started says watch runs, until done closes.
	started bool
	// last is when a block last came in, in nanoseconds of the Unix
	// clock; lat the source's latency in frames.
	last atomic.Int64
	lat  atomic.Int64
	quit chan struct{}
	done chan struct{}
}

// newPulseClient connects to the server, under the application's name.
func newPulseClient(name string) (*pulse.Client, error) {
	if name == "" {
		if exe, err := os.Executable(); err == nil {
			name = filepath.Base(exe)
		} else {
			name = "gunim"
		}
	}
	c, err := pulse.NewClient(pulse.ClientApplicationName(name), pulse.ClientTimeout(2*time.Second))
	if err != nil {
		return nil, fmt.Errorf("capture: connecting to PulseAudio: %w", err)
	}
	return c, nil
}

func systemDevices() ([]Device, error) {
	c, err := newPulseClient("")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var server proto.GetServerInfoReply
	if err := c.RawRequest(&proto.GetServerInfo{}, &server); err != nil {
		return nil, fmt.Errorf("capture: asking PulseAudio for its default input: %w", err)
	}
	var list proto.GetSourceInfoListReply
	if err := c.RawRequest(&proto.GetSourceInfoList{}, &list); err != nil {
		return nil, fmt.Errorf("capture: listing PulseAudio's inputs: %w", err)
	}
	out := make([]Device, 0, len(list))
	for _, src := range list {
		out = append(out, Device{ID: src.SourceName, Name: src.Device, Channels: len(src.ChannelMap), Rate: int(src.Rate),
			Default: src.SourceName == server.DefaultSourceName, Monitor: src.MonitorSourceIndex != proto.Undefined})
	}
	return out, nil
}

func openSystem(s *Stream, id string, rate int, buffer time.Duration) (format, error) {
	c, err := newPulseClient(s.o.Name)
	if err != nil {
		return format{}, err
	}
	name := id
	if name == "" {
		name = "@DEFAULT_SOURCE@"
	}
	var src proto.GetSourceInfoReply
	if err := c.RawRequest(&proto.GetSourceInfo{SourceIndex: proto.Undefined, SourceName: name}, &src); err != nil {
		c.Close()
		return format{}, fmt.Errorf("capture: finding the input %q: %w", name, err)
	}
	if rate <= 0 {
		rate = int(src.Rate)
	}
	ch := len(src.ChannelMap)
	frames := max(1, int(audio.FramesAt(buffer, rate)))
	b := &pulseIn{s: s, c: c, ch: ch, source: src.SourceIndex, quit: make(chan struct{}), done: make(chan struct{})}
	b.lat.Store(audio.FramesAt(time.Duration(src.Latency)*time.Microsecond, rate))
	st, err := c.NewRecord(pulse.Float32Writer(b.write), pulse.RecordChannels(src.ChannelMap), pulse.RecordSampleRate(rate),
		pulse.RecordMediaName(s.o.Name), pulse.RecordRawOption(func(r *proto.CreateRecordStream) {
			r.SourceIndex = src.SourceIndex
			// A source unplugged ends the stream, where PulseAudio
			// would move it to another input unasked.
			r.NoMove = true
			r.BufferFragSize = uint32(frames * ch * 4)
			r.BufferMaxLength = proto.Undefined
			r.AdjustLatency = true
		}))
	if err != nil {
		c.Close()
		return format{}, fmt.Errorf("capture: opening the input %s: %w", src.Device, err)
	}
	b.st = st
	s.b = b
	return format{name: src.Device, channels: ch, rate: st.SampleRate(), buffer: frames,
		latency: int(b.lat.Load()) + frames}, nil
}

func (b *pulseIn) start() error {
	b.last.Store(time.Now().UnixNano())
	b.st.Start()
	b.started = true
	go b.watch()
	return nil
}

// write takes a block from the server. It runs on the client's
// goroutine, which reads what the server sends.
func (b *pulseIn) write(buf []float32) (int, error) {
	n := len(buf) / b.ch
	if n == 0 {
		return len(buf), nil
	}
	b.last.Store(time.Now().UnixNano())
	// The block's last frame reached the input the source's latency
	// ago, its first n frames before.
	b.s.putAgo(buf[:n*b.ch], b.lat.Load()+int64(n))
	return len(buf), nil
}

// watch keeps the source's latency up to date, and ends the stream as
// the source goes away or the server does.
func (b *pulseIn) watch() {
	defer close(b.done)
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-b.quit:
			return
		case <-t.C:
		}
		if b.st.Closed() {
			b.s.lose(ErrLost)
			return
		}
		if err := b.st.Error(); err != nil {
			b.s.lose(err)
			return
		}
		if time.Since(time.Unix(0, b.last.Load())) > lost {
			b.s.lose(ErrLost)
			return
		}
		var src proto.GetSourceInfoReply
		if err := b.c.RawRequest(&proto.GetSourceInfo{SourceIndex: b.source}, &src); err != nil {
			continue
		}
		lat := audio.FramesAt(time.Duration(src.Latency)*time.Microsecond, b.s.info.Rate)
		b.lat.Store(lat)
		b.s.latency.Store(lat + int64(b.s.info.Buffer))
	}
}

// close runs once, from Open or Close.
func (b *pulseIn) close() error {
	if b.started {
		close(b.quit)
		<-b.done
	}
	if !b.st.Closed() {
		b.st.Stop()
		b.st.Close()
	}
	b.c.Close()
	return nil
}
