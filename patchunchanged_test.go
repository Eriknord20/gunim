package gunim

import (
	"testing"

	"github.com/marrasen/gunim/anim"
)

// quietTick is a patch whose handler says it changed nothing laid out: it only moves a spring.
type quietTick struct{ At float32 }

// A patch builds no frame of its own where its handler says it changed nothing laid out: the spring it moved asks
// for the frames it needs. A patch whose handler says nothing builds one, as before.
func TestAPatchThatChangedNothingLaidOutBuildsNoFrame(t *testing.T) {
	w := newProbeWindow(t)
	RegisterPatch(w, "panel", func(p *probe, v quietTick, u *UI) {
		if v.At != p.at.Target() {
			p.at.Animate(v.At, anim.Snappy)
		}
		u.Unchanged()
	})
	c := w.Client()
	if err := c.Mount(Root, "panel", "panel", panelState{Label: "hello"}, "jobs"); err != nil {
		t.Fatal(err)
	}
	run(w, 60)
	u := w.ui
	p := probeOf(t, u.ids["panel"].node)

	u.apply(Patch{Key: "jobs", Data: quietTick{At: 0}})
	if u.invalid {
		t.Error("a patch that changed nothing laid out asked for a frame")
	}
	u.apply(Patch{Key: "jobs", Data: quietTick{At: 1}})
	if u.invalid {
		t.Error("a patch that only moved a spring asked for a whole frame")
	}
	if !p.at.Active() {
		t.Fatal("the patch's spring didn't start")
	}
	run(w, 120)
	if got := p.at.Value(); got != 1 {
		t.Fatalf("at = %v, want 1 once the spring settles", got)
	}

	u.apply(Patch{Key: "jobs", Data: tick{At: 0}})
	if !u.invalid {
		t.Error("a patch whose handler didn't say it changed nothing built no frame")
	}
	// Unchanged outside a patch does nothing to the next one.
	run(w, 1)
	u.Unchanged()
	u.apply(Patch{Key: "jobs", Data: tick{At: 1}})
	if !u.invalid {
		t.Error("Unchanged called before a patch kept the patch from building a frame")
	}
}

// A window woken by patches alone that changed nothing laid out builds no frame, unless a spring moves; any other
// patch, or another command, builds one.
func TestAWindowWokenByQuietPatchesBuildsNoFrame(t *testing.T) {
	w := newProbeWindow(t)
	RegisterPatch(w, "panel", func(p *probe, v quietTick, u *UI) {
		if v.At != p.at.Target() {
			p.at.Animate(v.At, anim.Snappy)
		}
		u.Unchanged()
	})
	c := w.Client()
	if err := c.Mount(Root, "panel", "panel", panelState{Label: "hello"}, "jobs"); err != nil {
		t.Fatal(err)
	}
	run(w, 60)
	queue := func(cmd Command) {
		w.inMu.Lock()
		w.pending = append(w.pending, cmd)
		w.inMu.Unlock()
	}
	frames := func() int64 { return w.Stats().Frames }

	n := frames()
	queue(Patch{Key: "jobs", Data: quietTick{At: 0}})
	w.draw()
	if frames() != n {
		t.Error("a patch that changed nothing built a frame")
	}
	queue(Patch{Key: "jobs", Data: quietTick{At: 1}})
	w.draw()
	if frames() != n+1 {
		t.Error("a patch that started a spring built no frame for it to move in")
	}
	if !w.ui.calm {
		t.Error("the frame a quiet patch's spring moves in carried no steady subtree")
	}
	run(w, 120)
	n = frames()
	queue(Patch{Key: "jobs", Data: tick{At: 0}})
	w.draw()
	if frames() != n+1 {
		t.Error("a patch that didn't say it changed nothing built no frame")
	}
	for _, cmd := range []Command{Publish{Key: "jobs", State: panelState{Label: "fresh"}}, Focus{ID: "panel"},
		SetTheme{Theme: "light"}} {
		run(w, 120)
		n = frames()
		queue(cmd)
		queue(Patch{Key: "jobs", Data: quietTick{At: p0(w)}})
		w.draw()
		if frames() != n+1 {
			t.Errorf("a %s with a quiet patch built no frame", cmd.Name())
		}
	}
}

// p0 is where the probe panel's spring stands, for a quiet patch that moves nothing.
func p0(w *Window) float32 { return w.ui.ids["panel"].node.(*probe).at.Target() }
