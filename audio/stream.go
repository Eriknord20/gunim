package audio

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// A Stream decodes a sound ahead of where it is read, on a goroutine
// of its own, so reading it waits for no disk and no decoder, takes no
// lock and makes nothing: the work a sound's mixing must not do.
//
// It holds the sound as pages of decoded frames, around each of its
// [Cursor]s: as far ahead of each as [StreamOptions.Ahead] says, and
// as far behind as Behind, so a cursor that seeks a little way, or
// jumps back to where another stands, reads at once. A cursor that
// reads where no page is decoded yet reads nothing, and no error: the
// mix plays silence there, and the frames come a moment later.
//
// One goroutine at a time reads its cursors, as a [Mixer] mixes on
// one: the decoder reuses a page once the reads that might copy it
// have finished, counting them in order.
type Stream struct {
	o      StreamOptions
	length int64
	// end is where the sound ends, as the decoder found it, where the
	// source said it was longer.
	end atomic.Int64
	err atomic.Pointer[error]
	// table holds the page of each pageFrames frames, or nil where
	// none is decoded. Readers take pages from it; the decoder alone
	// puts them there and takes them away.
	table []atomic.Pointer[page]
	// entered and left count the reads started and finished, so the
	// decoder reuses a page taken away only once no read can be
	// copying it.
	entered, left atomic.Int64
	// pages counts the pages made, for a test to see they are reused.
	pages atomic.Int64

	// mu guards cursors, and closed: the decoder's and the
	// application's, never a reader's.
	mu      sync.Mutex
	cursors []*Cursor
	closed  bool
	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
}

// StreamOptions says how a [Stream] decodes.
type StreamOptions struct {
	// Ahead is how far ahead of each cursor the stream decodes, and
	// Behind how far behind it keeps what it decoded, in frames. Zero
	// is two seconds ahead and half a second behind, at Rate.
	Ahead, Behind int64
	// Rate is the sound's rate, for the defaults.
	Rate int
	// Close, where set, closes what the source reads, as its file, once
	// the stream is done with it.
	Close func()
}

// pageFrames is how many frames a page holds.
const pageFrames = 4096

// page is pageFrames frames of the sound, decoded, from frame at;
// frames says how many it holds, fewer at the sound's end. It is not
// changed while in the table.
type page struct {
	at     int64
	frames int
	data   []float32
}

// retired is a page taken out of the table, to reuse once the reads
// counted by entered have left.
type retired struct {
	p       *page
	entered int64
}

// ErrStreamClosed is returned by a read of a stream closed.
var ErrStreamClosed = errors.New("audio: the stream is closed")

// NewStream starts decoding src, a sound of known length, ahead of its
// cursors. The stream owns src from then: only its goroutine reads and
// seeks it, until [Stream.Close].
func NewStream(src Seeker, o StreamOptions) (*Stream, error) {
	length := src.Len()
	if length < 0 {
		return nil, errors.New("audio: a stream needs a sound of known length")
	}
	rate := max(o.Rate, 1)
	if o.Ahead <= 0 {
		o.Ahead = 2 * int64(rate)
	}
	if o.Behind <= 0 {
		o.Behind = int64(rate) / 2
	}
	s := &Stream{o: o, length: length, table: make([]atomic.Pointer[page], (length+pageFrames-1)/pageFrames),
		wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	s.end.Store(length)
	go s.decode(src)
	return s, nil
}

// Cursor returns a reader of the stream of its own, at frame 0.
func (s *Stream) Cursor() *Cursor {
	c := &Cursor{s: s}
	s.mu.Lock()
	s.cursors = append(s.cursors, c)
	s.mu.Unlock()
	s.Wake()
	return c
}

// Wake has the decoder look at the cursors at once, as after a seek:
// otherwise it looks every few milliseconds while they move, and less
// often while they rest. It is for the application's side; a reader
// need not call it.
func (s *Stream) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Close stops the decoder and closes the source. Reads after it read
// nothing: nothing may read the stream as it closes.
func (s *Stream) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.quit)
	<-s.done
}

// Err returns the error the decoder met, if any.
func (s *Stream) Err() error {
	if e := s.err.Load(); e != nil {
		return *e
	}
	return nil
}

// A Cursor reads a [Stream], from a place of its own. It is a [Seeker]:
// it reads and seeks without waiting or making anything, and one
// goroutine at a time may read and seek it.
type Cursor struct {
	s *Stream
	// at is the frame read next: the reader's, which the decoder reads
	// to know what to decode.
	at atomic.Int64
}

// Len implements [Seeker].
func (c *Cursor) Len() int64 { return c.s.length }

// SeekFrame implements [Seeker]: the cursor reads from f next. Frames
// not decoded there yet come a moment later.
func (c *Cursor) SeekFrame(f int64) error {
	c.at.Store(max(0, min(f, c.s.length)))
	return nil
}

// Read implements [Source]: as many frames as are decoded from where
// the cursor is, none where none are yet, and [io.EOF] at the sound's
// end.
func (c *Cursor) Read(dst []float32) (int, error) {
	s := c.s
	s.entered.Add(1)
	defer s.left.Add(1)
	at, end := c.at.Load(), s.end.Load()
	want := int64(len(dst) / 2)
	n := int64(0)
	for n < want && at < end {
		k := at / pageFrames
		if k >= int64(len(s.table)) {
			break
		}
		p := s.table[k].Load()
		if p == nil {
			break
		}
		off := at - p.at
		got := min(want-n, int64(p.frames)-off)
		if got <= 0 {
			break
		}
		copy(dst[2*n:2*(n+got)], p.data[2*off:2*(off+got)])
		n += got
		at += got
	}
	c.at.Store(at)
	if n == 0 {
		if at >= end {
			return 0, io.EOF
		}
		if err := s.err.Load(); err != nil {
			return 0, *err
		}
	}
	return int(n), nil
}

// Ready says the next frames of the cursor, as many as frames, or to
// the sound's end, are decoded: the application's side waits for it
// before playing from a place, so the sound starts whole.
func (c *Cursor) Ready(frames int64) bool {
	s := c.s
	at, end := c.at.Load(), s.end.Load()
	for f := at; f < min(at+frames, end); f = (f/pageFrames + 1) * pageFrames {
		if s.table[f/pageFrames].Load() == nil {
			return false
		}
	}
	return true
}

// WaitReady waits up to wait for Ready(frames), and says whether it came.
func (c *Cursor) WaitReady(frames int64, wait time.Duration) bool {
	c.s.Wake()
	for end := time.Now().Add(wait); !c.Ready(frames); time.Sleep(time.Millisecond) {
		if time.Now().After(end) || c.s.Err() != nil {
			return false
		}
	}
	return true
}

// Close lets the cursor go: the stream keeps nothing for it.
func (c *Cursor) Close() {
	s := c.s
	s.mu.Lock()
	for i, x := range s.cursors {
		if x == c {
			s.cursors = append(s.cursors[:i], s.cursors[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

// The decoder's pace: how often it looks while the cursors move, and
// while they rest.
const (
	streamMoving  = 5 * time.Millisecond
	streamResting = 100 * time.Millisecond
)

// decode decodes the pages the cursors want, the nearest ahead of a
// cursor first, takes away those no cursor wants, and rests while
// there is nothing to do, until the stream closes.
func (s *Stream) decode(src Seeker) {
	defer close(s.done)
	if s.o.Close != nil {
		defer s.o.Close()
	}
	var (
		at       []int64
		was      []int64
		live     []int64 // the pages in the table, by index
		retiring []retired
		srcAt    = int64(-1)
		timer    = time.NewTimer(time.Hour)
		rested   time.Duration
	)
	defer timer.Stop()
	for {
		s.mu.Lock()
		at = at[:0]
		for _, c := range s.cursors {
			at = append(at, c.at.Load())
		}
		s.mu.Unlock()
		// The pages no cursor wants go first, for their room to be
		// used again before any page is made.
		kept := live[:0]
		for _, k := range live {
			if s.wanted(k, at) {
				kept = append(kept, k)
				continue
			}
			p := s.table[k].Swap(nil)
			retiring = append(retiring, retired{p: p, entered: s.entered.Load()})
		}
		live = kept
		// The nearest page ahead of a cursor not yet decoded.
		want, best := int64(-1), int64(-1)
		end := s.end.Load()
		for _, f := range at {
			for k := f / pageFrames; k*pageFrames < min(f+s.o.Ahead, end); k++ {
				if s.table[k].Load() != nil {
					continue
				}
				if d := max(0, k*pageFrames-f); best < 0 || d < best {
					want, best = k, d
				}
				break
			}
		}
		if want >= 0 && s.Err() == nil {
			p := s.pageFor(&retiring)
			p.at = want * pageFrames
			if srcAt != p.at {
				if err := src.SeekFrame(p.at); err != nil {
					s.fail(err)
					continue
				}
			}
			n := 0
			for n < pageFrames {
				got, err := src.Read(p.data[2*n:])
				n += got
				if err != nil {
					if !errors.Is(err, io.EOF) {
						s.fail(err)
					}
					// A sound shorter than it said ends where it stops.
					s.end.Store(min(s.end.Load(), p.at+int64(n)))
					break
				}
				if got == 0 {
					break
				}
			}
			p.frames, srcAt = n, p.at+int64(n)
			s.table[want].Store(p)
			live = append(live, want)
			select {
			case <-s.quit:
				return
			default:
			}
			continue
		}
		// Nothing to decode. Rest: briefly while the cursors move,
		// longer while they do not.
		moved := len(at) != len(was)
		for i := range at {
			moved = moved || i >= len(was) || at[i] != was[i]
		}
		was = append(was[:0], at...)
		rest := streamMoving
		if !moved {
			rested += streamMoving
			if rested > 250*time.Millisecond {
				rest = streamResting
			}
		} else {
			rested = 0
		}
		timer.Reset(rest)
		select {
		case <-s.quit:
			return
		case <-s.wake:
			rested = 0
		case <-timer.C:
		}
	}
}

// wanted says some cursor wants page k: it lies from Behind before a
// cursor to Ahead after it.
func (s *Stream) wanted(k int64, at []int64) bool {
	lo, hi := k*pageFrames, (k+1)*pageFrames
	for _, f := range at {
		if hi > f-s.o.Behind && lo < f+s.o.Ahead {
			return true
		}
	}
	return false
}

// pageFor returns a page to decode into: one retired that no read can
// be copying now, or a new one.
func (s *Stream) pageFor(retiring *[]retired) *page {
	left := s.left.Load()
	for i, r := range *retiring {
		if left >= r.entered {
			*retiring = append((*retiring)[:i], (*retiring)[i+1:]...)
			return r.p
		}
	}
	s.pages.Add(1)
	return &page{data: make([]float32, 2*pageFrames)}
}

// fail records err, the decoder's, for the readers.
func (s *Stream) fail(err error) {
	s.err.CompareAndSwap(nil, &err)
}
