package mainthread

import "testing"

// Work posted runs where a driver takes it, and is refused where none
// does.
func TestPostRunsWhereADriverTakesIt(t *testing.T) {
	t.Cleanup(func() { Set(nil) })
	if Available() || Post(func() {}) {
		t.Fatal("work taken with no driver")
	}
	var queued []func()
	Set(func(f func()) bool {
		queued = append(queued, f)
		return true
	})
	ran := false
	if !Available() || !Post(func() { ran = true }) {
		t.Fatal("work refused with a driver")
	}
	for _, f := range queued {
		f()
	}
	if !ran {
		t.Error("work posted never ran")
	}
	Set(nil)
	if Available() {
		t.Error("available after the driver let go")
	}
}
