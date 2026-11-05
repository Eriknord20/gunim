// Package inbox queues a window's input for the engine.
package inbox

import (
	"sync"

	"github.com/marrasen/gunim/input"
)

// An Inbox carries input from the thread a platform delivers it on to
// the engine.
//
// It queues without bound, and a goroutine of its own feeds the
// channel. So the platform's thread, which pumps events for every
// window, always hands input over at once, and a window slow to read
// its input leaves the others running. Pointer moves queued back to back
// with the same modifiers arrive as the last of them.
type Inbox struct {
	out  chan any
	quit <-chan struct{}
	wake chan struct{}

	mu     sync.Mutex
	items  []any
	closed bool
	// sending says an item is on its way out, taken from items but not yet read.
	sending bool
}

// New returns an inbox that stops feeding when quit closes.
func New(quit <-chan struct{}) *Inbox {
	q := &Inbox{out: make(chan any), quit: quit, wake: make(chan struct{}, 1)}
	go q.feed()
	return q
}

// Push queues ev.
func (q *Inbox) Push(ev any) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, ev)
	q.mu.Unlock()
	q.nudge()
}

// Close ends the stream once what is queued has been delivered.
func (q *Inbox) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.nudge()
}

// Waiting reports whether anything pushed has yet to be read from Out.
func (q *Inbox) Waiting() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sending || len(q.items) > 0
}

func (q *Inbox) nudge() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Inbox) feed() {
	for {
		q.mu.Lock()
		if len(q.items) == 0 {
			closed := q.closed
			q.mu.Unlock()
			if closed {
				close(q.out)
				return
			}
			select {
			case <-q.wake:
			case <-q.quit:
				return
			}
			continue
		}
		ev := q.items[0]
		q.items = q.items[1:]
		ev = q.lastMove(ev)
		q.sending = true
		q.mu.Unlock()
		if !q.send(ev) {
			return
		}
		q.mu.Lock()
		q.sending = false
		q.mu.Unlock()
	}
}

// send delivers ev, swapping a pointer move for a newer one queued while it waits, and reports false on quit.
func (q *Inbox) send(ev any) bool {
	for {
		select {
		case q.out <- ev:
			return true
		case <-q.quit:
			return false
		case <-q.wake:
			q.mu.Lock()
			ev = q.lastMove(ev)
			q.mu.Unlock()
		}
	}
}

// lastMove returns the newest of the pointer moves queued right after ev, dropping the ones before it, or ev itself
// when ev is not a pointer move. It runs with q.mu held.
func (q *Inbox) lastMove(ev any) any {
	m, ok := ev.(input.PointerMove)
	if !ok {
		return ev
	}
	for len(q.items) > 0 {
		next, ok := q.items[0].(input.PointerMove)
		if !ok || next.Mods != m.Mods || next.Touch != m.Touch {
			break
		}
		m = next
		q.items = q.items[1:]
	}
	return m
}

// Out is the channel the input arrives on, closed after Close once
// what was queued has been delivered.
func (q *Inbox) Out() <-chan any { return q.out }
