package plug

import "testing"

// playing is a sound that has played to frame at: frame f is f.
type playing struct{ at int64 }

func (p *playing) Heard(dst []float32) (frames int, at int64, rate int) {
	n := min(int64(len(dst)/2), p.at)
	for i := range n {
		f := float32(p.at - n + i)
		dst[2*i], dst[2*i+1] = f, f
	}
	return int(n), p.at, 48000
}

func TestAListenerReadsEachFrameOnce(t *testing.T) {
	p := &playing{at: 1000}
	l := Listener{Sound: p}
	// The first time, the last as many as asked for.
	if got, rate := l.Listen(256); len(got) != 2*256 || got[0] != 744 || rate != 48000 {
		t.Fatalf("first, %d frames from %v", len(got)/2, got[0])
	}
	// Then what was heard since, and nothing while it stands still.
	p.at = 1100
	if got, _ := l.Listen(256); len(got) != 2*100 || got[0] != 1000 {
		t.Fatalf("after 100 more, %d frames from %v", len(got)/2, got[0])
	}
	if got, _ := l.Listen(256); got != nil {
		t.Fatalf("standing still, %d frames", len(got)/2)
	}
	// After a jump back, the last as many as asked for again.
	p.at = 500
	if got, _ := l.Listen(256); len(got) != 2*256 || got[0] != 244 {
		t.Fatalf("after a jump back, %d frames from %v", len(got)/2, got[0])
	}
	if got, _ := (&Listener{}).Listen(256); got != nil {
		t.Fatal("a Listener of no sound heard something")
	}
}

func TestAListenerHearsPlayingBetweenChunksUntilStill(t *testing.T) {
	p := &playing{at: 1000}
	l := Listener{Sound: p}
	frame := StillAfter / 10
	if got, _, on := l.Hear(256, frame); len(got) == 0 || !on {
		t.Fatal("a sound playing was not heard playing")
	}
	// The frames of the window between chunks hear nothing new, and the
	// sound plays on through them.
	for i := range 9 {
		if got, _, on := l.Hear(256, frame); got != nil || !on {
			t.Fatalf("%d frames between chunks, the sound stopped", i+1)
		}
	}
	p.at += 100
	if got, _, on := l.Hear(256, frame); len(got) != 2*100 || !on {
		t.Fatal("the next chunk was not heard")
	}
	// Nothing new for StillAfter: it stopped.
	for range 9 {
		l.Hear(256, frame)
	}
	if _, _, on := l.Hear(256, frame); on {
		t.Fatal("a sound still for StillAfter plays on")
	}
	if _, _, on := (&Listener{}).Hear(256, frame); on {
		t.Fatal("a Listener of no sound hears it playing")
	}
}

// quiet is a sound that has played to frame at, every frame of it v.
type quiet struct {
	at int64
	v  float32
}

func (q *quiet) Heard(dst []float32) (frames int, at int64, rate int) {
	n := min(int64(len(dst)/2), q.at)
	for i := range 2 * n {
		dst[i] = q.v
	}
	return int(n), q.at, 48000
}

// Silence moving on is a sound standing still: an engine running on
// with the song stopped. A sound heard again plays at once.
func TestAListenerHearsSilenceAsStill(t *testing.T) {
	q := &quiet{at: 1000}
	l := Listener{Sound: q}
	frame := StillAfter / 10
	for i := range 11 {
		q.at += 100
		if got, _, _ := l.Hear(256, frame); got != nil {
			t.Fatalf("frame %d: silence heard as %d frames", i, len(got)/2)
		}
	}
	if _, _, on := l.Hear(256, frame); on {
		t.Fatal("silence for StillAfter plays on")
	}
	q.at, q.v = q.at+100, Silence*2
	if got, _, on := l.Hear(256, frame); len(got) != 2*100 || !on {
		t.Fatal("the sound coming back was not heard playing")
	}
}
