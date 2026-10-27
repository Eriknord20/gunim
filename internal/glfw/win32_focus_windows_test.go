package glfw

import (
	"runtime"
	"testing"
)

// A thread where no window had the keyboard gets NULL back from
// SetFocus, and that is no error. See issue #199.
func TestSetFocusWithNoWindowBeforeIsNoError(t *testing.T) {
	err := make(chan error)
	go func() {
		// A thread of its own holds no window. It is not unlocked, so
		// it ends with the goroutine.
		runtime.LockOSThread()
		_, e := _SetFocus(0)
		err <- e
	}()
	if e := <-err; e != nil {
		t.Fatal(e)
	}
}
