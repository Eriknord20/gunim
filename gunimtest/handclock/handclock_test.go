package handclock

import (
	"slices"
	"testing"
	"time"
)

func TestTimersFireInTheirOrderAsTheClockPassesThem(t *testing.T) {
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	c := New(start)
	var fired []string
	var at []time.Duration
	note := func(name string) func() {
		return func() { fired = append(fired, name); at = append(at, c.Now().Sub(start)) }
	}
	c.AfterFunc(300*time.Millisecond, note("late"))
	c.AfterFunc(100*time.Millisecond, note("early"))
	c.AfterFunc(100*time.Millisecond, note("early too"))
	c.Advance(99 * time.Millisecond)
	if len(fired) != 0 {
		t.Fatalf("before their time, %v fired", fired)
	}
	c.Advance(time.Second)
	if want := []string{"early", "early too", "late"}; !slices.Equal(fired, want) {
		t.Fatalf("fired %v, want %v", fired, want)
	}
	if want := []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 300 * time.Millisecond}; !slices.Equal(at, want) {
		t.Fatalf("they fired at %v, want %v", at, want)
	}
	if got := c.Now().Sub(start); got != 1099*time.Millisecond {
		t.Fatalf("the clock reads %v after moving 1.099 s", got)
	}
}

func TestAfterWaitsForTheClockToMove(t *testing.T) {
	c := New(time.Now())
	ch := c.After(time.Second)
	c.Advance(999 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("After came before its time")
	default:
	}
	c.Sleep(time.Millisecond)
	select {
	case <-ch:
	default:
		t.Fatal("After never came, the clock moved past it")
	}
}

func TestARoundRunsEachPeriodAndWhenWokenUntilItQuits(t *testing.T) {
	c := New(time.Now())
	wake, quit := make(chan struct{}, 1), make(chan struct{})
	runs := 0
	done := c.Every(100*time.Millisecond, wake, quit, func() { runs++ })
	c.Advance(350 * time.Millisecond)
	if runs != 3 {
		t.Fatalf("in 350 ms of 100 ms rounds, %d ran", runs)
	}
	wake <- struct{}{}
	c.Advance(0)
	if runs != 4 {
		t.Fatalf("woken, the round ran %d times in all, want 4", runs)
	}
	close(quit)
	<-done
	c.Advance(time.Second)
	if runs != 4 {
		t.Fatalf("after quitting, the round ran %d times in all", runs)
	}
}
