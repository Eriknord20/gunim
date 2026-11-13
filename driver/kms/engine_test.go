//go:build linux && (arm64 || amd64) && !gunimkms

package kms

import (
	"context"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// onEngine runs the engine on the driver's windows and their input, with
// no screen behind them: Run waits for the end, and the monitor is the
// panel's.
type onEngine struct{ *Driver }

func (e onEngine) Run(ctx context.Context, ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}

func (e onEngine) Monitors() []driver.Monitor {
	return []driver.Monitor{{Bounds: geom.Rect{Max: geom.Pt(720, 1280)}, Scale: 1, RefreshRate: 60}}
}

// heard is a view that tells which of hidden and shown it hears.
type heard struct{ got chan bool }

func (h *heard) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(10, 10))
}

func (h *heard) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

func (h *heard) Handle(e input.Event, _ *gunim.UI) bool {
	switch e.(type) {
	case input.WindowHidden:
		h.got <- false
	case input.WindowShown:
		h.got <- true
	}
	return false
}

// A view in a real window on the driver hears the screen going off, the VT
// switching away, and both coming back, once each and in order, as a view
// does on the other platforms.
func TestAViewHearsTheScreenHiddenAndShown(t *testing.T) {
	d := testDriver(0, 1)
	d.scr, _ = testScreen()
	d.blank = newBlanker(Blank{Dim: time.Minute, Off: 5 * time.Minute}, 1, bt0)
	h := &heard{got: make(chan bool, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	up := make(chan error, 1)
	go func() {
		_ = gunim.MainWith(ctx, onEngine{d}, func(a *gunim.App) error {
			w, err := a.NewWindow(gunim.WindowOptions{Title: "heard"})
			if err == nil {
				gunim.RegisterView(w, "heard", func(struct{}) gunim.Node { return h }, nil)
				err = w.Client().Mount(gunim.Root, "heard", "heard", struct{}{})
			}
			up <- err
			<-ctx.Done()
			return nil
		})
	}()
	if err := <-up; err != nil {
		t.Fatal(err)
	}
	next := func(what string) bool {
		t.Helper()
		select {
		case v := <-h.got:
			return v
		case <-time.After(2 * time.Second):
			t.Fatalf("the view heard nothing as %s", what)
			return false
		}
	}
	none := func(what string) {
		t.Helper()
		select {
		case v := <-h.got:
			t.Fatalf("the view heard shown %v as %s", v, what)
		case <-time.After(100 * time.Millisecond):
		}
	}
	r := &renderThread{d: d}
	d.blank.step(after(5 * time.Minute))
	d.blank.step(after(6 * time.Minute))
	r.powerOff()
	if next("the screen went off") {
		t.Fatal("the screen off reads as shown")
	}
	r.left()
	none("the VT switched away with the screen off")
	r.returned(after(7 * time.Minute))
	if !next("the VT came back") {
		t.Fatal("the return reads as hidden")
	}
	r.left()
	if next("the VT switched away") {
		t.Fatal("switching away reads as shown")
	}
	r.returned(after(8 * time.Minute))
	if !next("the VT came back again") {
		t.Fatal("the second return reads as hidden")
	}
	none("nothing more happened")
}
