package anim

import (
	"testing"
	"time"
)

// The count changes in every frame the value does, the last one included, and holds on every frame after.
func TestMovesCountsEveryFrameTheValueChanges(t *testing.T) {
	var g Group
	var m Moves
	v := NewFloat(0)
	g.Add(v)
	v.Animate(1, Gentle)
	frames := 0
	for rested := 0; rested < 10; {
		was, counted := v.Value(), m.Count()
		moving := m.Note(g.Step(time.Second / 60))
		if v.Value() != was && m.Count() == counted {
			t.Fatalf("frame %d: the value went from %v to %v and the count held at %d", frames, was, v.Value(), counted)
		}
		if !moving {
			rested++
			if rested > 1 && m.Count() != counted {
				t.Fatalf("frame %d: at rest, the count went from %d to %d", frames, counted, m.Count())
			}
		}
		frames++
		if frames > 1000 {
			t.Fatal("the spring never came to rest")
		}
	}
	if v.Value() != 1 {
		t.Fatalf("the value rested at %v, want 1", v.Value())
	}
}

// Note hands back what it was given, so a Step can end in it.
func TestMovesNoteReturnsWhatItTakes(t *testing.T) {
	var m Moves
	for _, moving := range []bool{false, true, true, false, false} {
		if got := m.Note(moving); got != moving {
			t.Fatalf("Note(%v) = %v", moving, got)
		}
	}
	if m.Count() != 3 {
		t.Fatalf("two moving steps and the one they rest in counted %d, want 3", m.Count())
	}
}
