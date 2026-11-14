package gunim_test

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// BeforeClose runs what it is given as the window closes, once, and not once it is stopped.
func TestBeforeCloseRunsAsTheWindowCloses(t *testing.T) {
	h := &hider{child: &drawnLeaf{}}
	w := gunimtest.New(t, geom.Sz(100, 100), nil)
	gunim.RegisterView(w, "h", func(struct{}) gunim.Node { return h }, nil)
	if err := w.Client().Mount(gunim.Root, "h", "h", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	var ran []string
	gunim.RegisterPatch(w, "h", func(_ *hider, _ bool, u *gunim.UI) {
		u.BeforeClose(func(*gunim.UI) { ran = append(ran, "kept") })
		stop := u.BeforeClose(func(*gunim.UI) { ran = append(ran, "stopped") })
		stop()
	})
	if err := w.Client().Patch("h", true); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	if len(ran) != 0 {
		t.Fatalf("before the window closed, %v ran", ran)
	}
	w.Close()
	w.Close()
	if len(ran) != 1 || ran[0] != "kept" {
		t.Errorf("as the window closed, %v ran, want [kept]", ran)
	}
	var nilUI *gunim.UI
	nilUI.BeforeClose(func(*gunim.UI) {})()
}
