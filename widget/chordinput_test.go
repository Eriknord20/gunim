package widget

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// A chord input listening on a stage, with a button after it to go to.
func chordStage(t *testing.T) (*ChordInput, *gunim.Window, func(int)) {
	t.Helper()
	c := NewChordInput("Press the keys")
	w, run := stage(t, &frame{child: Row(c, NewButton("after")), size: geom.Sz(500, 100)})
	do(t, w, func(u *gunim.UI) { u.Focus(c) })
	run(1)
	return c, w, run
}

// Modifiers show as they are held, and the first other key ends it, with
// the whole press: Tab and the rest are not lost to the window.
func TestAChordInputShowsTheModifiersThenCommits(t *testing.T) {
	for _, k := range []input.Key{input.KeyK, input.KeyTab, input.KeyF10} {
		c, w, run := chordStage(t)
		var got []input.KeyPress
		c.OnCommit = func(p input.KeyPress, _ *gunim.UI) gunim.Intent { got = append(got, p); return nil }
		w.Input(input.KeyPress{Key: input.KeyLeftControl, Mods: input.ModControl})
		w.Input(input.KeyPress{Key: input.KeyLeftShift, Mods: input.ModControl | input.ModShift})
		run(1)
		if s := c.shown(); s != "Ctrl+Shift+…" {
			t.Fatalf("holding two modifiers it says %q", s)
		}
		w.Input(input.KeyRelease{Key: input.KeyLeftShift, Mods: input.ModControl | input.ModShift})
		run(1)
		if s := c.shown(); s != "Ctrl+…" {
			t.Fatalf("letting go of Shift it says %q", s)
		}
		w.Input(input.KeyPress{Key: k, Mods: input.ModControl})
		run(1)
		if len(got) != 1 || got[0].Key != k || got[0].Mods != input.ModControl {
			t.Fatalf("pressing %v it committed %v", k, got)
		}
	}
}

// Escape alone cancels; with a modifier it is a key like any other.
func TestAChordInputEscape(t *testing.T) {
	c, w, run := chordStage(t)
	gone, sent := 0, 0
	c.OnDismiss = func(*gunim.UI) gunim.Intent { gone++; return nil }
	c.OnCommit = func(input.KeyPress, *gunim.UI) gunim.Intent { sent++; return nil }
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	if gone != 1 || sent != 0 {
		t.Fatalf("Escape dismissed %d and committed %d", gone, sent)
	}

	c, w, run = chordStage(t)
	gone, sent = 0, 0
	c.OnDismiss = func(*gunim.UI) gunim.Intent { gone++; return nil }
	c.OnCommit = func(input.KeyPress, *gunim.UI) gunim.Intent { sent++; return nil }
	w.Input(input.KeyPress{Key: input.KeyEscape, Mods: input.ModControl})
	run(1)
	if gone != 0 || sent != 1 {
		t.Fatalf("Ctrl+Escape dismissed %d and committed %d", gone, sent)
	}
}

// The keyboard going elsewhere ends it, once, and a commit is not
// followed by a dismiss as the keyboard leaves.
func TestAChordInputEndsOnce(t *testing.T) {
	c, w, run := chordStage(t)
	gone := 0
	c.OnDismiss = func(*gunim.UI) gunim.Intent { gone++; return nil }
	do(t, w, func(u *gunim.UI) { u.Focus(nil) })
	run(1)
	if gone != 1 {
		t.Fatalf("the keyboard leaving dismissed %d times", gone)
	}

	c, w, run = chordStage(t)
	gone = 0
	c.OnDismiss = func(*gunim.UI) gunim.Intent { gone++; return nil }
	w.Input(input.KeyPress{Key: input.KeyK, Mods: input.ModControl})
	run(1)
	do(t, w, func(u *gunim.UI) { u.Focus(nil) })
	run(1)
	if gone != 0 {
		t.Fatalf("after a commit the keyboard leaving dismissed %d times", gone)
	}
}

// On every frame the width moves by small steps to fit what it says, and
// the ring pulses without resting while it listens.
func TestAChordInputGlidesAndPulses(t *testing.T) {
	c, w, run := chordStage(t)
	run(30)
	start := c.width.Value()
	w.Input(input.KeyPress{Key: input.KeyLeftControl, Mods: input.ModControl})
	last := start
	moved := false
	lows, highs := float32(2), float32(-1)
	for range 90 {
		run(1)
		v := c.width.Value()
		if d := v - last; d > 40 || d < -40 {
			t.Fatalf("the width jumped from %v to %v", last, v)
		}
		if v != start {
			moved = true
		}
		last = v
		p := c.pulse()
		lows, highs = min(lows, p), max(highs, p)
	}
	if !moved {
		t.Fatal("the width never moved")
	}
	if highs-lows < 0.3 {
		t.Fatalf("the ring stays between %v and %v", lows, highs)
	}
}

// A fold that Comes grows sideways as it arrives and shrinks as it goes,
// and what follows it in a row glides.
func TestAFoldThatComesGrowsAcrossAndShrinks(t *testing.T) {
	after := newSpot(20, 20)
	row := Row(after)
	w, run := stage(t, &frame{child: row, size: geom.Sz(400, 100)})
	f := NewFold(newSpot(100, 20), false)
	f.Axis, f.Comes = Horizontal, true
	do(t, w, func(u *gunim.UI) { u.InsertAt(row, 0, f) })
	run(2)
	if b := f.box.W; b <= 0 || b >= 100 {
		t.Fatalf("two frames in, the fold is %v wide", b)
	}
	run(120)
	if f.box.W < 99.5 {
		t.Fatalf("arrived, the fold is %v wide", f.box.W)
	}
	do(t, w, func(u *gunim.UI) { u.Remove(f) })
	run(3)
	if b := f.box.W; b <= 0 || b >= 100 {
		t.Fatalf("three frames into leaving, the fold is %v wide", b)
	}
	run(120)
	do(t, w, func(u *gunim.UI) {
		if u.Presence(f) == gunim.Exiting {
			t.Fatal("the fold never left")
		}
	})
}

// What it says is painted, at full strength once the fade is done.
func TestAChordInputPaintsWhatItSays(t *testing.T) {
	c, _, run := chordStage(t)
	run(60)
	ops := painted(c, geom.Sz(200, 24))
	layers := 0
	for _, op := range ops {
		if l, ok := op.(*paint.LayerOp); ok {
			layers++
			if l.Opts.Opacity != 1 {
				t.Fatalf("the words are drawn in a layer at %v", l.Opts.Opacity)
			}
		}
	}
	if layers == 0 {
		t.Fatal("the words are drawn in no clipped layer")
	}
}

// Taking the keyboard again, it listens again, and Escape can still leave.
func TestAChordInputListensAgainWhenRefocused(t *testing.T) {
	c, w, run := chordStage(t)
	gone := 0
	c.OnDismiss = func(*gunim.UI) gunim.Intent { gone++; return nil }
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	do(t, w, func(u *gunim.UI) { u.Focus(nil) })
	run(1)
	do(t, w, func(u *gunim.UI) { u.Focus(c) })
	run(1)
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(1)
	if gone != 2 {
		t.Fatalf("dismissed %d times", gone)
	}
}

// On every frame a key already held stays lit as another comes, and each
// lets go by fading, not all at once.
func TestAChordInputsKeysDoNotPop(t *testing.T) {
	c, w, run := chordStage(t)
	w.Input(input.KeyPress{Key: input.KeyLeftControl, Mods: input.ModControl})
	run(40)
	w.Input(input.KeyPress{Key: input.KeyLeftShift, Mods: input.ModControl | input.ModShift})
	for range 30 {
		run(1)
		if v := c.capIn[0].Value(); v < 0.99 {
			t.Fatalf("Ctrl dimmed to %v as Shift came", v)
		}
	}
	w.Input(input.KeyRelease{Key: input.KeyLeftShift, Mods: input.ModControl | input.ModShift})
	w.Input(input.KeyRelease{Key: input.KeyLeftControl, Mods: input.ModControl})
	run(1)
	if v := c.capIn[0].Value(); v < 0.3 {
		t.Fatalf("let go, Ctrl is at %v after one frame", v)
	}
	run(60)
	if v := c.capIn[0].Value(); v > 0.01 {
		t.Fatalf("Ctrl never faded: %v", v)
	}
}
