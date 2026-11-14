package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

func progressStage(t *testing.T) (b *ProgressBar, set func(float32), run func(int)) {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(300, 40), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "bar", func(float32) *ProgressBar {
		b = NewProgressBar()
		return b
	}, func(b *ProgressBar, v float32, u *gunim.UI) { b.SetValue(v, u) })
	c := w.Client()
	if err := c.Mount(gunim.Root, "bar", "bar", float32(0), "bar"); err != nil {
		t.Fatal(err)
	}
	run = func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	set = func(v float32) {
		if err := c.Publish("bar", v); err != nil {
			t.Fatal(err)
		}
	}
	return b, set, run
}

func TestAProgressBarGlidesToEachValue(t *testing.T) {
	b, set, run := progressStage(t)
	set(0.5)
	last := b.Shown()
	for i := range 60 {
		run(1)
		v := b.Shown()
		if v < last-0.001 || v > 0.51 {
			t.Fatalf("frame %d: going to a half, the fill went from %v to %v", i, last, v)
		}
		last = v
	}
	if last < 0.49 {
		t.Fatalf("settled, the fill is at %v, want a half", last)
	}
}

func TestAnIndeterminateBarKeepsDrawing(t *testing.T) {
	b, _, run := progressStage(t)
	b.Indeterminate = true
	run(120)
	if !b.Step(time.Second / 60) {
		t.Fatal("two seconds on, an indeterminate bar stopped asking for frames")
	}
	b.Indeterminate = false
	run(120)
	if b.Step(time.Second / 60) {
		t.Fatal("at rest, a bar still asks for frames")
	}
}
