package duplex

import "testing"

// Two sinks hear each buffer; one detached, the other hears on, and the
// tap counts as heard until the last goes.
func TestSinksHearTheInputsSideBySide(t *testing.T) {
	tap := NewTap("Fake", 48000, 64, 0, []string{"In 1"})
	var a, b int
	stopA := tap.Attach(func([]float32, int, int64) { a++ })
	stopB := tap.Attach(func([]float32, int, int64) { b++ })
	tap.Deliver(make([]float32, 64), 1, 0)
	if a != 1 || b != 1 || !tap.Attached() {
		t.Fatalf("heard %d and %d, attached %v", a, b, tap.Attached())
	}
	stopA()
	stopA()
	tap.Deliver(make([]float32, 64), 1, 64)
	if a != 1 || b != 2 || !tap.Attached() {
		t.Fatalf("with one detached, heard %d and %d, attached %v", a, b, tap.Attached())
	}
	stopB()
	tap.Deliver(make([]float32, 64), 1, 128)
	if a != 1 || b != 2 || tap.Attached() {
		t.Fatalf("with both detached, heard %d and %d, attached %v", a, b, tap.Attached())
	}
}
