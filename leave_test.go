package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/paint"
)

// A window leaving draws its content in a layer that fades as it
// shrinks, takes no input meanwhile, and closes once it has gone.
func TestAWindowLeavesFadingAndThenCloses(t *testing.T) {
	w, _, r := newStage(t, paint.Identity)
	c := w.Client()
	c.Leave()
	w.Frame(time.Second / 60)
	w.Frame(LeaveTime / 2)
	ops := w.Offscreen().Ops()
	l, ok := ops[0].(*paint.LayerOp)
	if !ok || l.Opts.Opacity <= 0 || l.Opts.Opacity >= 1 {
		t.Fatalf("halfway out, the frame starts with %#v", ops[0])
	}
	if l.Transform.A >= 1 {
		t.Fatalf("halfway out, the content is drawn at scale %v", l.Transform.A)
	}
	press(w, 50, 30)
	if len(r.events) != 0 {
		t.Fatalf("leaving, the window passed on %v", r.events)
	}
	w.Frame(LeaveTime)
	select {
	case <-w.done:
	default:
		t.Fatal("gone, the window is still open")
	}
}

// A window opened to arrive grows and fades in, and is drawn plainly
// once it has.
func TestAWindowArrivesFadingIn(t *testing.T) {
	w, _, _ := newStage(t, paint.Identity)
	w.ui.arriving, w.ui.arrivedAt = true, time.Time{}
	w.Frame(time.Second / 60)
	l, ok := w.Offscreen().Ops()[0].(*paint.LayerOp)
	if !ok || l.Opts.Opacity > 0.1 || l.Transform.A >= 1 {
		t.Fatalf("arriving, the frame starts with %#v", w.Offscreen().Ops()[0])
	}
	w.Frame(ArriveTime)
	w.Frame(time.Second / 60)
	if ops := w.Offscreen().Ops(); len(ops) > 0 {
		if _, ok := ops[0].(*paint.LayerOp); ok {
			t.Fatal("arrived, the window is still drawn in a layer")
		}
	}
}

// A window that arrived animated, closed by the user with no one to
// ask, leaves animated too, and closes once it has.
func TestAWindowThatArrivedLeavesTheSameWay(t *testing.T) {
	w, _, _ := newStage(t, paint.Identity)
	w.ui.animated = true
	w.ui.AskToClose()
	w.Frame(time.Second / 60)
	w.Frame(LeaveTime / 2)
	if _, ok := w.Offscreen().Ops()[0].(*paint.LayerOp); !ok {
		t.Fatal("closed by the user, the window did not leave animated")
	}
	select {
	case <-w.done:
		t.Fatal("the window closed before it had left")
	default:
	}
	w.Frame(LeaveTime)
	select {
	case <-w.done:
	default:
		t.Fatal("left, the window is still open")
	}
}

// A window all there sets its background, fully opaque, under its
// frames, so nothing of the desktop shows through what the tree leaves
// unpainted. While it arrives, the background fades in with it.
func TestAWindowAllThereIsOpaqueUnderItsTree(t *testing.T) {
	w, _, _ := newStage(t, paint.Identity)
	w.Frame(time.Second / 60)
	want := WindowBackground.Get(w.ui.theme)
	if got := w.Offscreen().Background(); got != want || got.A != 0xff {
		t.Fatalf("the window's background is %v, want the theme's %v, opaque", got, want)
	}
	w.ui.arriving = true
	w.ui.arrivedAt = time.Time{}
	w.Frame(time.Second / 60)
	w.Frame(ArriveTime / 3)
	if a := w.Offscreen().Background().A; a == 0 || a == 0xff {
		t.Fatalf("a third of the way in, the background's alpha is %d, want it on its way in", a)
	}
	if _, ok := w.Offscreen().Ops()[0].(*paint.LayerOp); !ok {
		t.Fatalf("an arriving window's frame starts with %T, want its fading layer", w.Offscreen().Ops()[0])
	}
}

// A window that cuts its corners and can show what is behind it leaves whole: its background is drawn inside the
// shrinking layer, clipped to its shape, and none is left under it at full size.
func TestAWindowWithRoundCornersLeavesWhole(t *testing.T) {
	w, _, _ := newStage(t, paint.Identity)
	w.Offscreen().SetOutline(8, 1, true)
	w.Client().Leave()
	w.Frame(time.Second / 60)
	w.Frame(LeaveTime / 2)
	ops := w.Offscreen().Ops()
	l, ok := ops[0].(*paint.LayerOp)
	if !ok || !l.Opts.Clip || l.Opts.Radius != 7 || l.Opts.Bounds.Min.X != 1 || l.Transform.A >= 1 {
		t.Fatalf("halfway out, the frame starts with %#v, want a shrinking layer clipped to the window's shape", ops[0])
	}
	if _, ok := ops[1].(*paint.RRectOp); !ok {
		t.Fatalf("halfway out, the layer starts with %#v, want the window's background", ops[1])
	}
	if a := w.Offscreen().Background().A; a != 0 {
		t.Fatalf("halfway out, the background left under the window is %v opaque", a)
	}
}

// A window faded out fades as it would leaving, takes no input, and is
// taken off the screen once it has gone, staying open; faded back in, it
// is on the screen again and fades in, and takes input once more.
func TestAWindowFadesOutAndBackInStayingOpen(t *testing.T) {
	w, _, r := newStage(t, paint.Identity)
	c := w.Client()
	c.Fade(true)
	w.Frame(time.Second / 60)
	w.Frame(LeaveTime / 2)
	l, ok := w.Offscreen().Ops()[0].(*paint.LayerOp)
	if !ok || l.Opts.Opacity <= 0 || l.Opts.Opacity >= 1 {
		t.Fatalf("halfway out, the frame starts with %#v", w.Offscreen().Ops()[0])
	}
	press(w, 50, 30)
	if len(r.events) != 0 {
		t.Fatalf("fading out, the window passed on %v", r.events)
	}
	if w.Offscreen().Cloaked() {
		t.Fatal("halfway out, the window is off the screen")
	}
	w.Frame(LeaveTime)
	if !w.Offscreen().Cloaked() {
		t.Fatal("faded out, the window is still on the screen")
	}
	if w.draws() {
		t.Fatal("faded out, the window still draws")
	}
	select {
	case <-w.done:
		t.Fatal("faded out, the window closed")
	default:
	}

	c.Fade(false)
	w.Frame(time.Second / 60)
	if w.Offscreen().Cloaked() {
		t.Fatal("fading in, the window is still off the screen")
	}
	l, ok = w.Offscreen().Ops()[0].(*paint.LayerOp)
	if !ok || l.Opts.Opacity >= 0.5 {
		t.Fatalf("starting in, the frame starts with %#v", w.Offscreen().Ops()[0])
	}
	w.Frame(LeaveTime)
	w.Frame(time.Second / 60)
	if ops := w.Offscreen().Ops(); len(ops) > 0 {
		if _, ok := ops[0].(*paint.LayerOp); ok {
			t.Fatal("faded in, the window is still drawn in a layer")
		}
	}
	press(w, 50, 30)
	if len(r.events) == 0 {
		t.Fatal("faded in, the window takes no input")
	}
}

// Fading back in before it has gone whole, the window comes back from
// as far as it had gone.
func TestAWindowFadingOutTurnsBackWhereItIs(t *testing.T) {
	w, _, _ := newStage(t, paint.Identity)
	c := w.Client()
	c.Fade(true)
	w.Frame(time.Second / 60)
	w.Frame(LeaveTime / 2)
	was := w.ui.faded
	c.Fade(false)
	w.Frame(time.Second / 60)
	w.Frame(time.Second / 60)
	if got := w.ui.faded; got >= was || got < was-0.3 {
		t.Fatalf("turned back at %v, the window is at %v", was, got)
	}
	if w.Offscreen().Cloaked() {
		t.Fatal("turned back, the window went off the screen")
	}
}

// A window fading out or in fades where it stands, at its own size: it
// comes back exactly where what stood in for it lands.
func TestAFadingWindowKeepsItsSize(t *testing.T) {
	w, _, _ := newStage(t, paint.Identity)
	w.Client().Fade(true)
	w.Frame(time.Second / 60)
	w.Frame(LeaveTime / 2)
	l, ok := w.Offscreen().Ops()[0].(*paint.LayerOp)
	if !ok || l.Opts.Opacity >= 1 {
		t.Fatalf("halfway out, the frame starts with %#v", w.Offscreen().Ops()[0])
	}
	if l.Transform.A != 1 {
		t.Fatalf("halfway out, the window is drawn at scale %v, want its own size", l.Transform.A)
	}
}
