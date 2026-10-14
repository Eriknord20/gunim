package capture

import (
	"fmt"

	"github.com/marrasen/gunim/audio/internal/duplex"
)

// tapIn records the inputs of the ASIO driver the speakers play
// through, in the driver's own buffers, as they play.
type tapIn struct {
	s *Stream
	t *duplex.Tap
	// detach stops the stream hearing the inputs, nil until it does.
	detach func()
	quit   chan struct{}
	done   chan struct{}
}

func openTap(s *Stream, driver string) (format, error) {
	t := duplex.Current()
	if t == nil || t.Driver != driver {
		return format{}, fmt.Errorf("capture: the speakers play through no ASIO driver called %q", driver)
	}
	if len(t.Names) == 0 {
		return format{}, fmt.Errorf("capture: %s records none of its inputs", driver)
	}
	s.b = &tapIn{s: s, t: t, quit: make(chan struct{}), done: make(chan struct{})}
	s.info.Exact = true
	return format{name: driver, channels: len(t.Names), rate: t.Rate, buffer: t.Buffer, latency: t.Latency}, nil
}

func (b *tapIn) start() error {
	b.detach = b.t.Attach(func(in []float32, _ int, heard int64) { b.s.putAt(in, heard) })
	go b.watch()
	return nil
}

// watch ends the stream as the driver closes.
func (b *tapIn) watch() {
	defer close(b.done)
	select {
	case <-b.t.Gone():
		b.s.lose(ErrLost)
	case <-b.quit:
	}
}

// close runs once, from Open or Close.
func (b *tapIn) close() error {
	if b.detach == nil {
		return nil
	}
	b.detach()
	close(b.quit)
	<-b.done
	return nil
}
