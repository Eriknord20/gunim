package speaker

import "sync/atomic"

// ring holds frames between one goroutine that writes them and another
// that reads them, each without waiting for the other. A frame holds
// chans interleaved samples.
type ring struct {
	buf   []float32
	chans int
	// w and r count the frames written and read, ever.
	w, r atomic.Int64
}

func newRing(frames, chans int) *ring {
	return &ring{buf: make([]float32, chans*frames), chans: chans}
}

// size returns how many frames the ring holds at most.
func (q *ring) size() int64 { return int64(len(q.buf) / q.chans) }

// len returns how many frames wait to be read.
func (q *ring) len() int64 { return q.w.Load() - q.r.Load() }

// room returns how many frames can be written.
func (q *ring) room() int64 { return q.size() - q.len() }

// push writes frames, as many as there is room for, and returns how many.
func (q *ring) push(frames []float32) int {
	c, size := int64(q.chans), q.size()
	w := q.w.Load()
	n := min(int64(len(frames))/c, size-(w-q.r.Load()))
	for i := int64(0); i < n; {
		// As far as the ring's end at most, in one copy.
		at := (w + i) % size
		k := min(n-i, size-at)
		copy(q.buf[at*c:(at+k)*c], frames[i*c:(i+k)*c])
		i += k
	}
	q.w.Store(w + n)
	return int(n)
}

// pop reads frames into dst, as many as wait, and returns how many.
func (q *ring) pop(dst []float32) int {
	c, size := int64(q.chans), q.size()
	r := q.r.Load()
	n := min(int64(len(dst))/c, q.w.Load()-r)
	for i := int64(0); i < n; {
		at := (r + i) % size
		k := min(n-i, size-at)
		copy(dst[i*c:(i+k)*c], q.buf[at*c:(at+k)*c])
		i += k
	}
	q.r.Store(r + n)
	return int(n)
}
