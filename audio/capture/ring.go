package capture

import (
	"math"
	"sync/atomic"
)

// ring holds the frames recorded between the device's thread, which
// writes them, and the goroutine that reads them, each without waiting
// for the other. Each block written leaves a mark of where it starts,
// and of its frame count and stamp.
type ring struct {
	buf []float32
	ch  int
	// size is how many frames buf holds; w and r count the frames
	// written and read, ever.
	size int64
	w, r atomic.Int64
	// marks hold a mark a block, mw counting those written and mr the
	// one the reader is in.
	marks  []mark
	mw, mr atomic.Int64
	// ratio is the speaker's frames to a frame of the stream's.
	ratio float64
}

// A mark says that the block from ring frame pos on came in as frame
// of the stream, and sounded with mixer frame heard.
type mark struct{ pos, frame, heard atomic.Int64 }

func newRing(frames, ch int, ratio float64) *ring {
	// A block of fewer than 16 frames is rare: where they come, the
	// marks run out before the frames, and the ring drops as it does
	// when full.
	return &ring{buf: make([]float32, frames*ch), ch: ch, size: int64(frames),
		marks: make([]mark, frames/16+2), ratio: ratio}
}

// push writes the channels sel of in, frames of devCh channels, and
// reports whether there was room for them all; where there was none it
// writes none.
func (q *ring) push(in []float32, devCh int, sel []int, frame, heard int64) bool {
	n := int64(len(in) / devCh)
	if n == 0 {
		return true
	}
	w, mw := q.w.Load(), q.mw.Load()
	if n > q.size-(w-q.r.Load()) || mw-q.mr.Load() >= int64(len(q.marks))-1 {
		return false
	}
	for i := range n {
		src := in[i*int64(devCh):]
		at := ((w + i) % q.size) * int64(q.ch)
		for j, c := range sel {
			q.buf[at+int64(j)] = src[c]
		}
	}
	m := &q.marks[mw%int64(len(q.marks))]
	m.pos.Store(w)
	m.frame.Store(frame)
	m.heard.Store(heard)
	q.mw.Store(mw + 1)
	q.w.Store(w + n)
	return true
}

// pop reads frames into dst up to the next mark, as many as fit, and
// returns them as a block.
func (q *ring) pop(dst []float32) Block {
	w, r := q.w.Load(), q.r.Load()
	if w == r || len(dst) < q.ch {
		return Block{Channels: q.ch, Heard: -1}
	}
	mw, mr := q.mw.Load(), q.mr.Load()
	size := int64(len(q.marks))
	for mr+1 < mw && q.marks[(mr+1)%size].pos.Load() <= r {
		mr++
	}
	q.mr.Store(mr)
	m := &q.marks[mr%size]
	end := w
	if mr+1 < mw {
		end = min(end, q.marks[(mr+1)%size].pos.Load())
	}
	n := min(end-r, int64(len(dst)/q.ch))
	for i := range n {
		at := ((r + i) % q.size) * int64(q.ch)
		copy(dst[i*int64(q.ch):(i+1)*int64(q.ch)], q.buf[at:at+int64(q.ch)])
	}
	into := r - m.pos.Load()
	heard := m.heard.Load()
	if heard >= 0 {
		heard += int64(math.Round(float64(into) * q.ratio))
	}
	b := Block{Frames: dst[:n*int64(q.ch)], Channels: q.ch, Frame: m.frame.Load() + into, Heard: heard}
	q.r.Store(r + n)
	return b
}
