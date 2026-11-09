//go:build darwin && !ios

package capture

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/coreaudio"
)

// The system's inputs are Core Audio's devices that record, by their
// UIDs. Each records by an IOProc of its own, through package
// audio/coreaudio, which hands the stream each buffer on the device's
// thread as it fills. The device is set to the rate asked for, where it
// is not the device the speakers play through; where it then runs at
// another, the stream converts to the rate asked for.

func systemDevices() ([]Device, error) {
	ds, err := coreaudio.Inputs()
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(ds))
	for _, d := range ds {
		out = append(out, Device{ID: d.UID, Name: d.Name, Channels: d.Channels, ChannelNames: d.ChannelNames, Rate: d.Rate,
			Default: d.Default})
	}
	return out, nil
}

// coreIn records a Core Audio device.
type coreIn struct {
	s  *Stream
	in *coreaudio.Input
	// conv converts to the rate asked for, nil where the device runs at
	// it; started says the stream takes the blocks.
	conv    *converter
	started atomic.Bool
	quit    chan struct{}
	done    chan struct{}
}

func openSystem(s *Stream, id string, rate int, buffer time.Duration) (format, error) {
	b := &coreIn{s: s, quit: make(chan struct{}), done: make(chan struct{})}
	frames := int(audio.FramesAt(buffer, max(rate, 48000)))
	in, err := coreaudio.OpenInput(coreaudio.InputConfig{Device: id, Rate: rate, Buffer: frames, Record: b.record})
	if err != nil {
		if errors.Is(err, coreaudio.ErrDenied) {
			return format{}, ErrDenied
		}
		return format{}, err
	}
	b.in = in
	info := in.Info()
	if rate <= 0 {
		rate = info.Rate
	}
	scale := func(n int) int { return int(int64(n) * int64(rate) / int64(max(info.Rate, 1))) }
	problem := info.Problem
	if rate != info.Rate {
		b.conv = newConverter(info.Channels, info.Rate, rate, maxBlock)
		problem = errors.Join(fmt.Errorf("capture: %s records at %d Hz, converted to %d Hz", info.Device, info.Rate,
			rate), problem)
	}
	s.b = b
	return format{name: info.Device, channels: info.Channels, rate: rate, buffer: max(1, scale(info.Buffer)),
		latency: scale(info.Latency), problem: problem}, nil
}

func (b *coreIn) start() error {
	b.started.Store(true)
	go b.watch()
	return nil
}

// record takes a buffer from the device, its first frame having
// reached the input ago of the device's frames before now. It runs on
// the device's thread.
func (b *coreIn) record(frames []float32, ago int) {
	if !b.started.Load() {
		return
	}
	if b.conv == nil {
		b.s.putAgo(frames, int64(ago))
		return
	}
	b.conv.convert(frames, ago, b.s.putAgo)
}

// watch ends the stream as the device goes away.
func (b *coreIn) watch() {
	defer close(b.done)
	select {
	case <-b.in.Done():
		if err := b.in.Err(); err != nil {
			b.s.lose(fmt.Errorf("%w: %w", ErrLost, err))
		}
	case <-b.quit:
	}
}

// close runs once, from Open or Close.
func (b *coreIn) close() error {
	if b.started.Load() {
		close(b.quit)
		<-b.done
	}
	return b.in.Close()
}
