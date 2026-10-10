package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// wentTo is the intent an address bar sends for a path.
type wentTo struct{ path string }

func TestAnAddressBarsPlacesAreOneTabStopAndEnterGoesThere(t *testing.T) {
	a := NewAddressBar()
	a.OnGo = func(path string, u *gunim.UI) gunim.Intent { return wentTo{path} }
	after := NewButton("After")
	w, run := stage(t, &frame{child: Row(a, after).Grow(a, 1), size: geom.Sz(600, 40)})
	focused := focusProbe(t, w, run)
	type set struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ set, u *gunim.UI) {
		a.SetPath("/x/y/z", []Crumb{{"x", "/x"}, {"y", "/x/y"}, {"z", "/x/y/z"}}, u)
	})
	if err := w.Client().Patch("stage", set{}); err != nil {
		t.Fatal(err)
	}
	run(60)
	press := func(k input.Key, mods input.Mods) {
		w.Input(input.KeyPress{Key: k, Mods: mods})
		w.Input(input.KeyRelease{Key: k, Mods: mods})
		run(20)
	}
	on := func() int {
		for i, c := range a.crumbs.crumbs {
			if focused() == gunim.Node(c.btn) {
				return i
			}
		}
		return -1
	}
	press(input.KeyTab, 0)
	if on() != 0 {
		t.Fatal("Tab did not land on the first place")
	}
	if a.crumbs.ring.Value() < 0.9 {
		t.Fatal("the bar shows no ring with the keyboard on a place")
	}
	press(input.KeyTab, 0)
	if focused() != after {
		t.Fatal("a second Tab did not leave the places, which are one stop")
	}
	press(input.KeyTab, input.ModShift)
	press(input.KeyRight, 0)
	if on() != 1 {
		t.Fatal("Right did not move to the second place")
	}
	press(input.KeyEnter, 0)
	var went []string
	for _, ev := range sent(w) {
		if g, ok := ev.(wentTo); ok {
			went = append(went, g.path)
		}
	}
	if len(went) != 1 || went[0] != "/x/y" {
		t.Fatalf("Enter on the second place went to %v, want /x/y", went)
	}
}

// addressStage mounts an address bar width wide showing cs, and returns a way to press keys.
func addressStage(t *testing.T, width float32, cs []Crumb) (a *AddressBar, w *gunim.Window, run func(int), press func(input.Key)) {
	t.Helper()
	a = NewAddressBar()
	a.OnGo = func(path string, u *gunim.UI) gunim.Intent { return wentTo{path} }
	w, run = stage(t, &frame{child: Row(a).Grow(a, 1), size: geom.Sz(width, 40)})
	type set struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ set, u *gunim.UI) { a.SetPath(cs[len(cs)-1].Path, cs, u) })
	if err := w.Client().Patch("stage", set{}); err != nil {
		t.Fatal(err)
	}
	run(60)
	press = func(k input.Key) {
		w.Input(input.KeyPress{Key: k})
		w.Input(input.KeyRelease{Key: k})
	}
	return a, w, run, press
}

func TestTheAddressPlaceWithTheKeyboardSlidesIntoView(t *testing.T) {
	cs := make([]Crumb, 0, 8)
	path := ""
	for _, name := range []string{"home", "someone", "projects", "gunim", "widget", "testdata", "fonts", "noto"} {
		path += "/" + name
		cs = append(cs, Crumb{name, path})
	}
	a, w, run, press := addressStage(t, 200, cs)
	u := stageUI(t, w, run)
	focused := func() AddressPlace {
		t.Helper()
		for _, pl := range a.Places(u) {
			if pl.Focused {
				return pl
			}
		}
		t.Fatal("no place has the keyboard")
		return AddressPlace{}
	}
	inView := func(why string) {
		t.Helper()
		run(40)
		if pl := focused(); pl.Rect.Min.X < -0.5 || pl.Rect.Max.X > 200.5 {
			t.Fatalf("after %s the place with the keyboard, %s, spans %v, outside the 200 px bar", why, pl.Name, pl.Rect)
		}
	}
	press(input.KeyTab)
	inView("Tab")
	press(input.KeyEnd)
	inView("End")
	press(input.KeyHome)
	inView("Home")
	for range 3 {
		press(input.KeyRight)
		inView("Right")
	}
	press(input.KeyEnd)
	for range 7 {
		press(input.KeyLeft)
		inView("Left")
	}
}

func TestALongLastAddressPlaceShowsTheStartOfItsName(t *testing.T) {
	name := strings.Repeat("a very long folder name ", 8)
	a, w, run, _ := addressStage(t, 200, []Crumb{{name, "/" + name}})
	u := stageUI(t, w, run)
	pl := a.Places(u)[0]
	if pl.Rect.Min.X < 0 || pl.Rect.Max.X > 200 {
		t.Fatalf("the long place spans %v, outside the 200 px bar", pl.Rect)
	}
	btn := a.crumbs.crumbs[0].btn
	if bad := spills(painted(btn, btn.size), btn.size, ringReach); len(bad) > 0 {
		t.Fatalf("the long place drew past its %v box: %v", btn.size, bad)
	}
}

func TestTheAddressBarEditsOnAClickBesideThePlaces(t *testing.T) {
	a, w, run, _ := addressStage(t, 400, []Crumb{{"x", "/x"}})
	w.Input(input.PointerDown{Pos: geom.Pt(380, 20), Clicks: 1, Time: time.Now()})
	run(1)
	if a.Editing() {
		t.Fatal("a press beside the places edits the path before it is let go")
	}
	w.Input(input.PointerUp{Pos: geom.Pt(380, 60), Time: time.Now()})
	run(1)
	if a.Editing() {
		t.Fatal("a press let go off the bar edits the path")
	}
	click(w, 380, 20)
	run(1)
	if !a.Editing() {
		t.Fatal("a click beside the places does not edit the path")
	}
}

// longCrumbs are the places of a path too long for a narrow bar.
func longCrumbs() []Crumb {
	cs := make([]Crumb, 0, 8)
	path := ""
	for _, name := range []string{"home", "someone", "projects", "gunim", "widget", "testdata", "fonts", "noto"} {
		path += "/" + name
		cs = append(cs, Crumb{name, path})
	}
	return cs
}

// The wheel scrolls places that overflow back to the first and on to the
// last again, and they fade at an edge only while more lie past it.
func TestTheWheelScrollsAddressPlacesThatOverflow(t *testing.T) {
	a, w, run, _ := addressStage(t, 200, longCrumbs())
	u := stageUI(t, w, run)
	ps := a.Places(u)
	if first := ps[0]; first.Rect.Min.X >= 0 {
		t.Fatalf("the first place shows at %v before any scroll", first.Rect)
	}
	fade := func() geom.Insets {
		t.Helper()
		f, ok := fadeIn(painted(a.crumbs, a.crumbs.size))
		if !ok {
			t.Fatal("the places paint in no layer")
		}
		return f
	}
	if f := fade(); f.Left == 0 || f.Right != 0 {
		t.Fatalf("at the end the places fade by %v, want only at the left", f)
	}
	w.Input(input.Scroll{Pos: geom.Pt(100, 20), Delta: geom.Pt(0, 5000)})
	run(40)
	if first := a.Places(u)[0]; first.Rect.Min.X < 0 {
		t.Fatalf("the wheel back left the first place at %v", first.Rect)
	}
	if f := fade(); f.Left != 0 || f.Right == 0 {
		t.Fatalf("at the start the places fade by %v, want only at the right", f)
	}
	w.Input(input.Scroll{Pos: geom.Pt(100, 20), Delta: geom.Pt(0, -5000)})
	run(40)
	if last := a.Places(u)[len(ps)-1]; last.Rect.Max.X > 200.5 {
		t.Fatalf("the wheel on left the last place at %v", last.Rect)
	}
}

// Places that fit do not scroll.
func TestAddressPlacesThatFitDoNotScroll(t *testing.T) {
	a, w, run, _ := addressStage(t, 400, []Crumb{{"x", "/x"}})
	w.Input(input.Scroll{Pos: geom.Pt(100, 20), Delta: geom.Pt(0, 300)})
	run(20)
	if at := a.crumbs.shift.Value(); at != 0 {
		t.Fatalf("places that fit scrolled to %v", at)
	}
}

// The lead shows before the places, which start past it, and a click on
// it runs OnLead.
func TestTheAddressLeadShowsBeforeThePlaces(t *testing.T) {
	type leadClicked struct{}
	a, w, run, _ := addressStage(t, 400, []Crumb{{"x", "/x"}, {"y", "/x/y"}})
	u := stageUI(t, w, run)
	before := a.Places(u)[0].Rect.Min.X
	a.OnLead = func(*gunim.UI) gunim.Intent { return leadClicked{} }
	type set struct{}
	gunim.RegisterPatch(w, "stage", func(_ gunim.Node, _ set, u *gunim.UI) { a.SetLead(AddressLead{Name: "web"}, u) })
	if err := w.Client().Patch("stage", set{}); err != nil {
		t.Fatal(err)
	}
	run(20)
	lead, ok := u.Bounds(a.crumbs.lead)
	if !ok || lead.Size().W <= 0 {
		t.Fatal("the lead does not show")
	}
	if first := a.Places(u)[0].Rect; first.Min.X <= lead.Max.X || first.Min.X <= before {
		t.Fatalf("the first place is at %v, over the lead at %v", first, lead)
	}
	c := lead.Center()
	click(w, c.X, c.Y)
	run(5)
	got := false
	for _, ev := range sent(w) {
		if _, ok := ev.(leadClicked); ok {
			got = true
		}
	}
	if !got {
		t.Fatal("a click on the lead ran nothing")
	}
}
