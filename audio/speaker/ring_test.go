package speaker

import "testing"

// TestRingKeepsOrderAcrossItsEnd writes and reads past the ring's end,
// and reads every frame back in order.
func TestRingKeepsOrderAcrossItsEnd(t *testing.T) {
	q := newRing(5, 2)
	next, want := float32(0), float32(0)
	for range 20 {
		in := make([]float32, 2*3)
		for i := range 3 {
			in[2*i], in[2*i+1] = next, -next
			next++
		}
		if n := q.push(in); n != 3 {
			t.Fatalf("pushed %d frames, want 3", n)
		}
		out := make([]float32, 2*4)
		n := q.pop(out)
		for i := range n {
			if out[2*i] != want || out[2*i+1] != -want {
				t.Fatalf("read %v, want %v", out[2*i], want)
			}
			want++
		}
		if q.len() > 5 {
			t.Fatalf("holds %d frames, more than its 5", q.len())
		}
		// Drain what is left so the next push fits.
		rest := make([]float32, 2*5)
		for i := range q.pop(rest) {
			if rest[2*i] != want {
				t.Fatalf("read %v, want %v", rest[2*i], want)
			}
			want++
		}
	}
	if q.push(make([]float32, 2*7)) != 5 {
		t.Error("a ring of 5 took more than 5 frames")
	}
}
