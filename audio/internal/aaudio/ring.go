// Package aaudio plays and records through AAudio, Android's own
// sound API in C, from Android 11. Package audio/speaker plays through
// it and hands its input to package audio/capture.
//
// AAudio asks for each buffer, and hands each one recorded, on a thread
// of its own that must have its answer at once. So that thread runs C
// alone: it copies from and to rings in C memory, which goroutines fill
// and empty. [Shared] is what it tells Go besides.
//
// Streams open on Android alone; the rings build everywhere, so the
// code around them is tested anywhere.
package aaudio

import (
	"errors"
	"sync/atomic"
	"unsafe"
)

// ErrUnsupported is the error where AAudio is missing: on any system
// but Android, and on Android before 11.
var ErrUnsupported = errors.New("aaudio: AAudio plays on Android 11 and newer")

// ringHeader is a ring's counts and buffer. It mirrors gunim_ring in
// aaudio_android.h, field for field, as both sides use it.
type ringHeader struct {
	w, r  atomic.Int64
	size  int64
	chans int64
	buf   unsafe.Pointer
}

// A Ring holds frames between one thread that writes them and another
// that reads them, neither waiting for the other. A frame holds
// Chans values. It lives in memory C can read, until Free.
type Ring[T float32 | int64] struct {
	h   *ringHeader
	buf []T
	// mem is the memory, for Free.
	mem []unsafe.Pointer
}

// NewRing returns a ring that holds the given number of frames, of
// chans values each.
func NewRing[T float32 | int64](frames, chans int) *Ring[T] {
	h := (*ringHeader)(alloc(unsafe.Sizeof(ringHeader{})))
	var zero T
	buf := alloc(uintptr(frames*chans) * unsafe.Sizeof(zero))
	h.size, h.chans, h.buf = int64(frames), int64(chans), buf
	return &Ring[T]{h: h, buf: unsafe.Slice((*T)(buf), frames*chans), mem: []unsafe.Pointer{unsafe.Pointer(h), buf}}
}

// Free lets the ring's memory go. Nothing may read or write it after.
func (q *Ring[T]) Free() {
	for _, p := range q.mem {
		free(p)
	}
	q.mem, q.buf, q.h = nil, nil, nil
}

// Size returns how many frames the ring holds at most.
func (q *Ring[T]) Size() int64 { return q.h.size }

// Chans returns how many values a frame holds.
func (q *Ring[T]) Chans() int { return int(q.h.chans) }

// Len returns how many frames wait to be read.
func (q *Ring[T]) Len() int64 { return q.h.w.Load() - q.h.r.Load() }

// Room returns how many frames can be written.
func (q *Ring[T]) Room() int64 { return q.h.size - q.Len() }

// Written returns how many frames have been written, ever.
func (q *Ring[T]) Written() int64 { return q.h.w.Load() }

// Read returns how many frames have been read, ever.
func (q *Ring[T]) Read() int64 { return q.h.r.Load() }

// Push writes frames, as many as there is room for, and returns how
// many.
func (q *Ring[T]) Push(frames []T) int {
	c, size := q.h.chans, q.h.size
	w := q.h.w.Load()
	n := min(int64(len(frames))/c, size-(w-q.h.r.Load()))
	for i := int64(0); i < n; {
		at := (w + i) % size
		k := min(n-i, size-at)
		copy(q.buf[at*c:(at+k)*c], frames[i*c:(i+k)*c])
		i += k
	}
	q.h.w.Store(w + n)
	return int(n)
}

// Pop reads frames into dst, as many as wait, and returns how many.
func (q *Ring[T]) Pop(dst []T) int {
	c, size := q.h.chans, q.h.size
	r := q.h.r.Load()
	n := min(int64(len(dst))/c, q.h.w.Load()-r)
	for i := int64(0); i < n; {
		at := (r + i) % size
		k := min(n-i, size-at)
		copy(dst[i*c:(i+k)*c], q.buf[at*c:(at+k)*c])
		i += k
	}
	q.h.r.Store(r + n)
	return int(n)
}

// Drop drops the frames waiting. One side calls it while the other is
// still, as between two streams.
func (q *Ring[T]) Drop() { q.h.r.Store(q.h.w.Load()) }

// Shared is what a stream's callbacks tell Go: counts the callback
// keeps, which Go reads as they change. It lives in C memory, and
// mirrors gunim_shared in aaudio_android.h, field for field.
type Shared struct {
	// Frames counts the frames through the callback since the stream
	// opened: played, silence too, or recorded, dropped too. AAudio's
	// timestamps count the same frames.
	Frames atomic.Int64
	// Silence counts the frames an output played silent as its ring ran
	// short, and Underruns the callbacks that found it short.
	Silence, Underruns atomic.Int64
	// Dropped counts the frames an input dropped as its ring was full.
	Dropped atomic.Int64
	// Most is the most frames a callback has carried.
	Most atomic.Int64
	// Err is the error AAudio's error callback was given, as
	// AAUDIO_ERROR_DISCONNECTED, or 0.
	Err atomic.Int64
	// data and blocks are the stream's rings: an input's blocks hold
	// each block's first frame, as Frames counts, and its length.
	data   unsafe.Pointer
	blocks unsafe.Pointer
}

// NewShared returns the shared state of a stream that plays from data,
// or records into data, its blocks into blocks.
func NewShared(data *Ring[float32], blocks *Ring[int64]) *Shared {
	sh := (*Shared)(alloc(unsafe.Sizeof(Shared{})))
	sh.data = unsafe.Pointer(data.h)
	if blocks != nil {
		sh.blocks = unsafe.Pointer(blocks.h)
	}
	return sh
}

// FreeShared lets sh's memory go, once nothing calls back with it.
func FreeShared(sh *Shared) { free(unsafe.Pointer(sh)) }

// Info says how a stream plays or records.
type Info struct {
	Rate, Channels int
	// Burst is how many frames the device moves at a time, Buffer how
	// many it holds now, and Capacity how many it could hold.
	Burst, Buffer, Capacity int
	// Device is the device's ID.
	Device int
	// Exclusive says the stream has the device to itself, and
	// LowLatency that it runs in low-latency mode.
	Exclusive, LowLatency bool
}
