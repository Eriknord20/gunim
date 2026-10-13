// Package duplex hands the inputs of the ASIO driver the speakers play
// through from package audio/speaker, which opens the driver, to
// package audio/capture, which records them.
package duplex

import (
	"sync"
	"sync/atomic"
)

// A Sink hears a driver's inputs: in holds frames of channels
// interleaved samples, and heard is the mixer frame at the speakers as
// the first of them reached the inputs. It runs on the driver's
// thread.
type Sink func(in []float32, channels int, heard int64)

// A Tap is the inputs of the driver the speakers play through, while
// they play through it.
type Tap struct {
	// Driver is the driver's name; Rate, Buffer and Latency its rate,
	// buffer size and input latency in frames; Names its inputs'.
	Driver                string
	Rate, Buffer, Latency int
	Names                 []string
	sink                  atomic.Pointer[Sink]
	gone                  chan struct{}
	closing               sync.Once
}

// NewTap returns a tap of the inputs info names.
func NewTap(driver string, rate, buffer, latency int, names []string) *Tap {
	return &Tap{Driver: driver, Rate: rate, Buffer: buffer, Latency: latency, Names: names, gone: make(chan struct{})}
}

// Attach has f hear the inputs from the next buffer on. It reports
// false where another sink hears them already.
func (t *Tap) Attach(f Sink) bool { return t.sink.CompareAndSwap(nil, &f) }

// Detach stops any sink hearing the inputs.
func (t *Tap) Detach() { t.sink.Store(nil) }

// Deliver hands the inputs to the sink, if any. The speaker calls it
// on the driver's thread.
func (t *Tap) Deliver(in []float32, channels int, heard int64) {
	if f := t.sink.Load(); f != nil {
		(*f)(in, channels, heard)
	}
}

// Gone returns a channel closed once the driver closes.
func (t *Tap) Gone() <-chan struct{} { return t.gone }

// Close tells of the driver closed.
func (t *Tap) Close() { t.closing.Do(func() { close(t.gone) }) }

var current atomic.Pointer[Tap]

// Current returns the tap of the driver the speakers play through, or
// nil.
func Current() *Tap { return current.Load() }

// Publish makes t the current tap.
func Publish(t *Tap) { current.Store(t) }

// Withdraw closes t, and drops it as the current tap where it is.
func Withdraw(t *Tap) {
	current.CompareAndSwap(t, nil)
	t.Close()
}
