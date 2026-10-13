package vocal

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
}

func (h *host) Param(id uint32) float64 {
	if v, ok := h.values[id]; ok {
		return v
	}
	return param(id).Default
}
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)                { h.log = append(h.log, "end") }
func (h *host) BeginEdit(string, *gunim.UI)          {}
func (h *host) Changed(*gunim.UI)                    {}
func (h *host) EndEdit(*gunim.UI)                    {}
func (h *host) Sound() (in, out plug.Sound)          { return nil, nil }
func (h *host) Traced(uint32, int64) (float32, bool) { return 0, false }

// stage shows n in a window of size, and returns it with a way to run
// frames.
func stage(t *testing.T, size geom.Size, n gunim.Node) (w *gunim.Window, run func(int)) {
	t.Helper()
	w = gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "vocal", func(struct{}) gunim.Node { return n }, nil)
	if err := w.Client().Mount(gunim.Root, "vocal", "vocal", nil); err != nil {
		t.Fatal(err)
	}
	run = func(k int) {
		for range k {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, run
}

// boundsIn returns where n is in w.
func boundsIn(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) geom.Rect {
	t.Helper()
	var b geom.Rect
	gunim.RegisterPatch(w, "vocal", func(_ gunim.Node, _ struct{}, u *gunim.UI) { b, _ = u.Bounds(n) })
	if err := w.Client().Patch("vocal", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	return b
}

func TestLearnSetsTheChainAsOneGestureOverTheLoop(t *testing.T) {
	p := Descriptor.New(nil).(*plugin)
	h := &host{values: map[uint32]float64{}}
	src := &memSource{frames: makeVoice(6, voiceOpts{level: 0.1, hiss: 0.1})}
	p.SetDocument(memDoc{src: src, loop: plug.Loop{From: rate, To: 5 * rate}, on: true})
	pn := p.View(plug.Panel, h).(*panel)
	w, run := stage(t, geom.Sz(760, 360), pn)
	if pn.learn.Value != "Loop" || pn.learn.Disabled {
		t.Fatalf("Learn reads %q, disabled %v, with the host looping; want Loop", pn.learn.Value, pn.learn.Disabled)
	}
	at := boundsIn(t, w, run, pn.learn).Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	for deadline := time.Now().Add(2 * time.Minute); pn.learning; {
		if time.Now().After(deadline) {
			t.Fatal("Learn ran on for 2 minutes")
		}
		run(1)
		time.Sleep(time.Millisecond)
	}
	n := len(learnedIDs)
	if len(h.log) != 3*n {
		t.Fatalf("Learn sent %d steps, want %d: %v", len(h.log), 3*n, h.log)
	}
	for i, s := range h.log {
		if want := []string{"begin", "set", "end"}[i/n]; s != want {
			t.Fatalf("step %d is %q, want every begin, then every set, then every end: %v", i, s, h.log)
		}
	}
	if !p.learnt.ok || !p.learnt.loop {
		t.Errorf("what Learn learned: %+v, want it over the loop", p.learnt)
	}
	if pn.values[InputID] != h.values[InputID] || h.values[InputID] == 0 {
		t.Errorf("Input is %+.1f dB in the panel and %+.1f in the host", pn.values[InputID], h.values[InputID])
	}
}

func TestEverySectionShowsInFullAndNoneOverlaps(t *testing.T) {
	p := Descriptor.New(nil).(*plugin)
	h := &host{values: map[uint32]float64{}}
	pn := p.View(plug.Full, h).(*panel)
	size := geom.Sz(1180, 620)
	w, run := stage(t, size, pn)
	if len(pn.sections) != 8 {
		t.Fatalf("%d sections in full, want the macros, Learn and six parts of the chain", len(pn.sections))
	}
	seen := make([]geom.Rect, 0, len(pn.sections))
	for _, s := range pn.sections {
		b := boundsIn(t, w, run, s)
		if b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > size.W || b.Max.Y > size.H {
			t.Errorf("%s stands at %v, outside the panel", s.Name, b)
		}
		for _, o := range seen {
			if b.Min.X < o.Max.X && o.Min.X < b.Max.X && b.Min.Y < o.Max.Y && o.Min.Y < b.Max.Y {
				t.Errorf("%s at %v overlaps a section at %v", s.Name, b, o)
			}
		}
		seen = append(seen, b)
	}
	if pn.learn.Value != "No track" || !pn.learn.Disabled {
		t.Errorf("with no track, Learn reads %q, disabled %v", pn.learn.Value, pn.learn.Disabled)
	}
}
