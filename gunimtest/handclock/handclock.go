// Package handclock is a clock a test moves by hand, for code that times
// itself on a clock it is given: its timers and its rounds run as the test
// moves the clock, and the wall clock plays no part.
package handclock

import (
	"slices"
	"sync"
	"time"
)

// Clock is a clock the test moves by hand. Nothing on it happens until
// Advance passes its time: then the timers fire, on the goroutine that
// advances, and the rounds run, each on its own goroutine as on the wall
// clock, and Advance returns once they are done. A round woken runs at
// the next Advance, Advance(0) too.
//
// Sleep is Advance, for code that sleeps on the test's goroutine. A goroutine of its own waits on After instead, until the
// test moves the clock.
type Clock struct {
	// adv keeps Advance to one goroutine at a time.
	adv    sync.Mutex
	mu     sync.Mutex
	now    time.Time
	seq    int
	rounds []*round
	timers []*timer
}

// round is a function the clock runs every so often, as Every asks.
type round struct {
	every      time.Duration
	next       time.Time
	wake, quit <-chan struct{}
	run        chan chan struct{}
	done       chan struct{}
}

// timer is a function the clock calls at a time.
type timer struct {
	at  time.Time
	seq int
	f   func()
}

// New returns a clock that reads start.
func New(start time.Time) *Clock { return &Clock{now: start} }

// Now returns the time on the clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep moves the clock on d.
func (c *Clock) Sleep(d time.Duration) { c.Advance(d) }

// After returns a channel that receives once the clock has moved on d.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.AfterFunc(d, func() { ch <- c.Now() })
	return ch
}

// AfterFunc calls f once the clock has moved on d, on the goroutine
// that moves it.
func (c *Clock) AfterFunc(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.timers = append(c.timers, &timer{at: c.now.Add(d), seq: c.seq, f: f})
}

// Every runs f on a goroutine of its own each time the clock moves on
// d, and at the next Advance after wake receives, until quit closes;
// done closes as it stops.
func (c *Clock) Every(d time.Duration, wake, quit <-chan struct{}, f func()) <-chan struct{} {
	r := &round{every: d, wake: wake, quit: quit, run: make(chan chan struct{}), done: make(chan struct{})}
	c.mu.Lock()
	r.next = c.now.Add(d)
	c.rounds = append(c.rounds, r)
	c.mu.Unlock()
	go func() {
		defer close(r.done)
		for {
			select {
			case <-quit:
				c.mu.Lock()
				c.rounds = slices.DeleteFunc(c.rounds, func(x *round) bool { return x == r })
				c.mu.Unlock()
				return
			case ran := <-r.run:
				f()
				close(ran)
			}
		}
	}()
	return r.done
}

// runRound runs r once, on its goroutine, and waits for it: not at all
// where it has stopped.
func runRound(r *round) {
	ran := make(chan struct{})
	select {
	case r.run <- ran:
		<-ran
	case <-r.done:
	}
}

// Advance moves the clock on d, firing the timers and running the
// rounds due on the way, in the order of their times, and the rounds
// woken, and returns once all are done.
func (c *Clock) Advance(d time.Duration) {
	c.adv.Lock()
	defer c.adv.Unlock()
	c.mu.Lock()
	end := c.now.Add(d)
	c.mu.Unlock()
	for n := 0; c.step(end); n++ {
		if n > 1e6 {
			panic("handclock: the rounds wake each other without end")
		}
	}
	c.mu.Lock()
	if end.After(c.now) {
		c.now = end
	}
	c.mu.Unlock()
}

// step runs a round woken, or else what is due first by end, and
// reports whether there was one.
func (c *Clock) step(end time.Time) bool {
	c.mu.Lock()
	for _, r := range c.rounds {
		select {
		case <-r.wake:
			c.mu.Unlock()
			runRound(r)
			return true
		default:
		}
	}
	var t *timer
	for _, x := range c.timers {
		if !x.at.After(end) && (t == nil || x.at.Before(t.at) || x.at.Equal(t.at) && x.seq < t.seq) {
			t = x
		}
	}
	var r *round
	for _, x := range c.rounds {
		if !x.next.After(end) && (r == nil || x.next.Before(r.next)) {
			r = x
		}
	}
	switch {
	case t != nil && (r == nil || !r.next.Before(t.at)):
		c.timers = slices.DeleteFunc(c.timers, func(x *timer) bool { return x == t })
		if t.at.After(c.now) {
			c.now = t.at
		}
		c.mu.Unlock()
		t.f()
	case r != nil:
		if r.next.After(c.now) {
			c.now = r.next
		}
		r.next = r.next.Add(r.every)
		c.mu.Unlock()
		runRound(r)
	default:
		c.mu.Unlock()
		return false
	}
	return true
}
