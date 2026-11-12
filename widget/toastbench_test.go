package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// BenchmarkToastsAtRest lays out and paints a frame of a stack of three toasts at rest: one with a button, a
// warning with a body, and one with an action.
func BenchmarkToastsAtRest(b *testing.B) {
	ts := &Toasts{Life: time.Hour}
	w := gunimtest.New(b, geom.Sz(800, 600), nil)
	gunim.RegisterView(w, "stage", func(struct{}) gunim.Node { return &frame{child: ts, size: geom.Sz(340, 600)} }, nil)
	if err := w.Client().Mount(gunim.Root, "stage", "stage", nil); err != nil {
		b.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	type do struct{ fn func(u *gunim.UI) }
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, d do, u *gunim.UI) { d.fn(u) })
	on := func(fn func(u *gunim.UI)) {
		if err := w.Client().Patch("stage", do{fn}); err != nil {
			b.Fatal(err)
		}
		run(1)
	}
	on(func(u *gunim.UI) {
		ts.Show(Toast{Title: "Exported Song.wav", Body: "Loudness −19.4 LUFS · range 2.2 LU · true peak −5.7 dBTP",
			Buttons: []ToastButton{{Label: "Show"}}}, u)
		ts.Show(Toast{Title: "Recording room is running low", Body: "About 12 minutes of recording room is left on this disk.",
			Kind: ToastWarning}, u)
		ts.Show(Toast{Title: "Removed Bass", Action: "Undo", Kind: ToastInfo}, u)
	})
	run(120)
	b.ReportAllocs()
	for b.Loop() {
		on(func(u *gunim.UI) { u.Invalidate() })
	}
}
