package beat

import (
	"math"
	"testing"
	"time"
)

// follow runs a recording through a live follower in the blocks a
// sound card would hand it, and returns the beats it called, each with
// the tempo it held as it called it.
func follow(l *Live, frames []float32) (at []time.Duration, bpm []float64) {
	const block = 2 * 256
	for i := 0; i < len(frames); i += block {
		for _, b := range l.Write(frames[i:min(i+block, len(frames))]) {
			at = append(at, b)
			bpm = append(bpm, l.BPM())
		}
	}
	return at, bpm
}

// after is the beats called from the time from on.
func after(at []time.Duration, from time.Duration) []time.Duration {
	for i, b := range at {
		if b >= from {
			return at[i:]
		}
	}
	return nil
}

// TestALiveFollowerLocksToASteadyBeat: within a few seconds it holds
// the tempo, and calls each beat where it is played.
func TestALiveFollowerLocksToASteadyBeat(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		for _, bpm := range []float64{72, 90, 110, 120, 128, 140, 160, 175} {
			want := beats(500*time.Millisecond, int(bpm/60*20), func(int) float64 { return bpm })
			l := NewLive(rate)
			got, _ := follow(l, render(rate, want, 20*time.Second))
			if math.Abs(l.BPM()-bpm) > 1 {
				t.Errorf("%d Hz, %v: the tempo held is %v", rate, bpm, l.BPM())
			}
			late := after(got, 6*time.Second)
			if len(late) == 0 {
				t.Errorf("%d Hz, %v: no beat called after six seconds; %d in all", rate, bpm, len(got))
				continue
			}
			worst, _ := stray(late, want)
			if worst > 20*time.Millisecond {
				t.Errorf("%d Hz, %v: a beat called %v from the nearest played", rate, bpm, worst)
			}
			if n, played := len(late), len(after(want, 6*time.Second)); n < played-2 || n > played+2 {
				t.Errorf("%d Hz, %v: %d beats called after six seconds, %d played", rate, bpm, n, played)
			}
		}
	}
}

// TestALiveFollowerFollowsASong: kick, snare and hats, the hats off
// the beat too, and the beat called on the kick and the snare.
func TestALiveFollowerFollowsASong(t *testing.T) {
	const rate = 48000
	for _, bpm := range []float64{98, 124, 138} {
		frames, want := song(rate, bpm, 14)
		l := NewLive(rate)
		got, _ := follow(l, frames)
		if math.Abs(l.BPM()-bpm) > 1 {
			t.Errorf("%v: the tempo held is %v", bpm, l.BPM())
		}
		late := after(got, want[0]+6*time.Second)
		late = late[:max(len(late)-1, 0)]
		if worst, _ := stray(late, want); worst > 20*time.Millisecond || len(late) < 10 {
			t.Errorf("%v: %d beats called late on, the furthest %v from one played", bpm, len(late), worst)
		}
	}
}

// TestALiveFollowerLocksSoon: a beat is held within four seconds of the
// music starting.
func TestALiveFollowerLocksSoon(t *testing.T) {
	const rate = 48000
	want := beats(0, 20, func(int) float64 { return 124 })
	l := NewLive(rate)
	frames := render(rate, want, 4*time.Second)
	follow(l, frames)
	if l.BPM() == 0 {
		t.Fatal("no tempo after four seconds of a plain beat")
	}
	if next, ok := l.Next(); !ok || next < 4*time.Second-10*time.Millisecond {
		t.Errorf("the next beat is put at %v, %v", next, ok)
	}
}

// TestALiveFollowerFollowsATempoChange from 120 to 140, as a DJ's next
// track does.
func TestALiveFollowerFollowsATempoChange(t *testing.T) {
	const rate = 48000
	first := beats(0, 40, func(int) float64 { return 120 })
	end := first[len(first)-1] + 500*time.Millisecond
	second := beats(end, 47, func(int) float64 { return 140 })
	want := append(append([]time.Duration{}, first...), second...)
	l := NewLive(rate)
	got, _ := follow(l, render(rate, want, end+20*time.Second))
	if math.Abs(l.BPM()-140) > 1 {
		t.Errorf("the tempo held is %v, want 140", l.BPM())
	}
	late := after(got, end+10*time.Second)
	if worst, _ := stray(late, second); worst > 20*time.Millisecond || len(late) < 10 {
		t.Errorf("%d beats called in the new track's last ten seconds, the furthest %v from one played", len(late), worst)
	}
}

// TestALiveFollowerTakesHalfTimeHeardForLong: a half or double tempo
// is most often the same music, read another way, but where the music
// truly goes to half time and stays there it is followed.
func TestALiveFollowerTakesHalfTimeHeardForLong(t *testing.T) {
	const rate = 48000
	first := beats(0, 40, func(int) float64 { return 160 })
	end := first[len(first)-1] + 750*time.Millisecond
	second := beats(end, 24, func(int) float64 { return 80 })
	want := append(append([]time.Duration{}, first...), second...)
	l := NewLive(rate)
	follow(l, render(rate, want, end+18*time.Second))
	if math.Abs(l.BPM()-80) > 1 {
		t.Errorf("after eighteen seconds of half time the tempo held is %v, want 80", l.BPM())
	}
}

// TestALiveFollowerFollowsADrift: a band speeding up from 120 to 132
// over half a minute.
func TestALiveFollowerFollowsADrift(t *testing.T) {
	const rate, n = 48000, 64
	want := beats(0, n, func(k int) float64 { return 120 + 12*float64(k)/n })
	l := NewLive(rate)
	got, _ := follow(l, render(rate, want, want[n-1]+100*time.Millisecond))
	late := after(got, 8*time.Second)
	if worst, _ := stray(late, want); worst > 25*time.Millisecond || len(late) < n-20 {
		t.Errorf("%d beats called after eight seconds, the furthest %v from one played", len(late), worst)
	}
	if math.Abs(l.BPM()-132) > 3 {
		t.Errorf("the tempo held at the end is %v, want about 132", l.BPM())
	}
}

// TestALiveFollowerCountsThroughSilence: the music stops for a few bars
// and comes back on the beat, and the beat goes on through the break
// without losing it.
func TestALiveFollowerCountsThroughSilence(t *testing.T) {
	const rate = 48000
	all := beats(0, 56, func(int) float64 { return 120 })
	// Bars 7 and 8 of 14 left out.
	want := append(append([]time.Duration{}, all[:24]...), all[32:]...)
	l := NewLive(rate)
	got, _ := follow(l, render(rate, want, all[len(all)-1]+time.Second))
	if math.Abs(l.BPM()-120) > 1 {
		t.Errorf("the tempo held is %v, want 120", l.BPM())
	}
	// In the break the beats called are where the music would have
	// played them.
	gap := after(got, all[24]-50*time.Millisecond)
	gap = gap[:min(len(gap), 8)]
	if worst, _ := stray(gap, all[24:32]); worst > 20*time.Millisecond || len(gap) < 8 {
		t.Errorf("%d beats called in the break, the furthest %v from where they fell", len(gap), worst)
	}
	// Up to the last beat played: past it the beat goes on, as in the
	// break.
	back := after(got, all[32]-50*time.Millisecond)
	for len(back) > 0 && back[len(back)-1] > all[len(all)-1]+50*time.Millisecond {
		back = back[:len(back)-1]
	}
	if worst, _ := stray(back, all[32:]); worst > 20*time.Millisecond || len(back) < 23 {
		t.Errorf("%d beats called after the break, the furthest %v from one played", len(back), worst)
	}
}

// TestALiveFollowerIsLessSureInABreak: the beat goes on in a long
// break, but the follower says it hears none.
func TestALiveFollowerIsLessSureInABreak(t *testing.T) {
	const rate = 48000
	want := beats(0, 20, func(int) float64 { return 120 })
	l := NewLive(rate)
	follow(l, render(rate, want, 10*time.Second))
	sure := l.Confidence()
	follow(l, render(rate, nil, 8*time.Second))
	if l.BPM() == 0 || l.Confidence() >= sure/2 {
		t.Errorf("eight seconds into a break the tempo is %v and the follower %v sure, %v before it",
			l.BPM(), l.Confidence(), sure)
	}
}

// TestALiveFollowerHearsNoBeatInHiss: hiss with no beat has no tempo
// and calls no beats.
func TestALiveFollowerHearsNoBeatInHiss(t *testing.T) {
	const rate = 48000
	l := NewLive(rate)
	got, _ := follow(l, render(rate, nil, 12*time.Second))
	if l.BPM() != 0 || len(got) > 0 {
		t.Errorf("hiss has a tempo of %v and %d beats", l.BPM(), len(got))
	}
}

// TestALiveFollowersClockCounts: the clock counts a beat for each beat
// called, and runs between them.
func TestALiveFollowersClockCounts(t *testing.T) {
	const rate = 48000
	want := beats(0, 30, func(int) float64 { return 120 })
	frames := render(rate, want, 15*time.Second)
	l := NewLive(rate)
	var called int
	last := 0.0
	const block = 2 * 256
	for i := 0; i < len(frames); i += block {
		called += len(l.Write(frames[i:min(i+block, len(frames))]))
		b := l.Beats()
		if b < last {
			t.Fatalf("the clock ran back from %v to %v", last, b)
		}
		last = b
		if called > 0 && (b < float64(called) || b > float64(called)+1) {
			t.Fatalf("%d beats called and the clock at %v", called, b)
		}
	}
}
