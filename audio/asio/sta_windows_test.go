package asio

import (
	"testing"
	"time"
)

// TestACallThatDoesNotReturnIsWaitedForAWhile has the driver's thread
// call a function that holds it: the wait for it gives up in time, and
// the call still ends once it may.
func TestACallThatDoesNotReturnIsWaitedForAWhile(t *testing.T) {
	s, err := newSTA()
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	done := s.start(func() { <-held })
	if waitFor(done, 50*time.Millisecond) {
		t.Fatal("a call that holds the thread returned")
	}
	close(held)
	if !waitFor(done, time.Second) {
		t.Fatal("the call never ended once let go")
	}
	s.stop()
	if s.start(func() {}) != nil {
		t.Fatal("a call was taken after the thread stopped")
	}
}
