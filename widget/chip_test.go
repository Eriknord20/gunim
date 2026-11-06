package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
)

type removed struct{ Label string }

type picked struct{ Item int }

func TestAClickOnAChipsCrossRemovesIt(t *testing.T) {
	c := NewChip("level", "error")
	c.OnRemove = func(u *gunim.UI) gunim.Intent { return removed{"error"} }
	w, run := stage(t, &frame{child: Row(c), size: geom.Sz(400, 40)})
	click(w, 5, 12)
	run(1)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("a click on the label sent %v, want nothing", got)
	}
	click(w, c.crossX, 12)
	run(1)
	if got := sent(w); len(got) != 1 || got[0] != (removed{"error"}) {
		t.Fatalf("a click on the cross sent %v, want the chip removed", got)
	}
}

func TestALongChipInANarrowWrapKeepsItsCrossInside(t *testing.T) {
	c := NewChip("path", "/home/someone/a/folder/with/a/very/long/name/indeed/and/more/yet")
	c.Icon = icon.Folder
	c.OnRemove = func(u *gunim.UI) gunim.Intent { return removed{"path"} }
	w, run := stage(t, &frame{child: NewWrap(c), size: geom.Sz(200, 100)})
	run(1)
	if c.size.W > 200 {
		t.Fatalf("in a 200 px wrap the chip is %v wide", c.size.W)
	}
	if bad := spills(painted(c, c.size), c.size, 0); len(bad) > 0 {
		t.Fatalf("the chip drew past its %v box: %v", c.size, bad)
	}
	click(w, c.size.W-c.size.H/2, c.size.H/2)
	run(1)
	if got := sent(w); len(got) != 1 {
		t.Fatalf("a click on the cross at the chip's end sent %v, want the chip removed", got)
	}
}

func TestAWrapStartsANewRowWhereTheNextChildWouldPassItsWidth(t *testing.T) {
	a, b, c := newSpot(150, 20), newSpot(150, 30), newSpot(150, 20)
	wrap := NewWrap()
	wrap.Gap = tableNoGap
	w := gunimtest.New(t, geom.Sz(320, 200), nil)
	gunim.RegisterView(w, "wrap", func(struct{}) gunim.Node { return wrap },
		func(n gunim.Node, _ struct{}, u *gunim.UI) {
			for _, k := range []gunim.Node{a, b, c} {
				u.Insert(n, k)
			}
		})
	if err := w.Client().Mount(gunim.Root, "wrap", "wrap", nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.Frame(time.Second / 60)
	}
	at(t, a, geom.Pt(0, 0))
	at(t, b, geom.Pt(150, 0))
	// The row is as tall as its tallest child.
	at(t, c, geom.Pt(0, 30))
}

func TestAWrapCanCentreEachChildInItsRow(t *testing.T) {
	a, b, c := newSpot(150, 20), newSpot(150, 30), newSpot(150, 20)
	wrap := NewWrap()
	wrap.Gap, wrap.Cross = tableNoGap, CrossCenter
	w := gunimtest.New(t, geom.Sz(320, 200), nil)
	gunim.RegisterView(w, "wrap", func(struct{}) gunim.Node { return wrap },
		func(n gunim.Node, _ struct{}, u *gunim.UI) {
			for _, k := range []gunim.Node{a, b, c} {
				u.Insert(n, k)
			}
		})
	if err := w.Client().Mount(gunim.Root, "wrap", "wrap", nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.Frame(time.Second / 60)
	}
	at(t, a, geom.Pt(0, 5))
	at(t, b, geom.Pt(150, 0))
	at(t, c, geom.Pt(0, 30))
}

func TestAMenuButtonThatStaysOpenTicksWhatIsPicked(t *testing.T) {
	b := NewMenuButton("Files", Labels("app.log", "app.log.1", "app.log.2"))
	b.StayOpen = true
	b.OnPick = func(i int, u *gunim.UI) gunim.Intent { return picked{i} }
	w, run := stage(t, &frame{child: Row(b), size: geom.Sz(400, 300)})
	click(w, 10, 10)
	run(10)
	if !b.IsOpen() {
		t.Fatal("a click did not open the menu")
	}
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if !b.IsOpen() {
		t.Fatal("a pick closed a menu that stays open")
	}
	if !b.Items()[1].Checked || !b.menu.Items()[1].Checked {
		t.Fatalf("after picking the second item, ticks are %v, want it ticked", checkedOf(b.Items()))
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if b.Items()[1].Checked {
		t.Fatal("picking a ticked item left it ticked")
	}
	if got := sent(w); len(got) != 2 || got[0] != (picked{1}) || got[1] != (picked{1}) {
		t.Fatalf("intents %v, want two picks of item 1", got)
	}
}

func TestAPaletteOffersTypedItemsFirst(t *testing.T) {
	w, o, run := newPaletteStage(t)
	o.p.Typed = func(q string) []PaletteItem {
		if q == "" {
			return nil
		}
		return []PaletteItem{{Title: "path = " + q}}
	}
	focusOpener(w, run)
	w.Input(input.KeyPress{Key: input.KeyF1})
	run(20)
	w.Input(input.TextInput{Text: "sp"})
	run(20)
	f := o.p.card.found
	if len(f) < 2 || f[0].Index != len(o.p.Items) {
		t.Fatalf("typing sp found %v, want the typed item first", f)
	}
	if got := o.p.item(f[0].Index).Title; got != "path = sp" {
		t.Fatalf("the typed item is %q, want path = sp", got)
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(20)
	if len(o.picked) != 1 || o.picked[0] != len(o.p.Items) {
		t.Fatalf("picked %v, want the typed item at %d", o.picked, len(o.p.Items))
	}
}

// OnPick hears a pick on the UI goroutine, for a pick that works in the
// window, and an OnPick that returns nil sends nothing.
func TestAMenuButtonsOnPickHearsThePick(t *testing.T) {
	b := NewMenuButton("Add", Labels("Server", "Window"))
	got := -1
	b.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		got = i
		return nil
	}
	w, run := stage(t, &frame{child: Row(b), size: geom.Sz(400, 300)})
	click(w, 10, 10)
	run(10)
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if got != 1 || b.IsOpen() {
		t.Fatalf("picked %d, open %v; want the second, closed", got, b.IsOpen())
	}
	quiet(t, w, "an OnPick that returns nil")
}

// A chip takes the keyboard by Tab, rings its cross, and Delete or Backspace removes it.
func TestAChipTakesTheKeyboardAndDeleteRemovesIt(t *testing.T) {
	for _, key := range []input.Key{input.KeyDelete, input.KeyBackspace} {
		c := NewChip("level", "error")
		c.OnRemove = func(u *gunim.UI) gunim.Intent { return removed{"error"} }
		w, run := stage(t, &frame{child: Row(c), size: geom.Sz(400, 40)})
		focused := focusProbe(t, w, run)
		tab(w, run, 0)
		if f := focused(); f != c {
			t.Fatalf("Tab put the keyboard on %T, want the chip", f)
		}
		last := float32(0)
		for k := range 30 {
			run(1)
			r := c.ring.Value()
			if r < last-0.02 {
				t.Fatalf("frame %d after Tab, the chip's ring went back from %v to %v", k+1, last, r)
			}
			last = r
		}
		if last < 0.99 {
			t.Fatalf("Tab left the chip's ring at %v", last)
		}
		w.Input(input.KeyPress{Key: key})
		run(1)
		if got := sent(w); len(got) != 1 || got[0] != (removed{"error"}) {
			t.Fatalf("%v on the chip sent %v, want it removed", key, got)
		}
	}
}

// A chip hiding its cross narrows over frames, with no jump, and a click
// where the cross was removes nothing.
func TestAChipHidesItsCrossOverFrames(t *testing.T) {
	c := NewChip("", "Ctrl+K")
	gone := 0
	c.OnRemove = func(*gunim.UI) gunim.Intent { gone++; return nil }
	w, run := stage(t, &frame{child: Row(c), size: geom.Sz(300, 100)})
	run(1)
	width := c.size.W
	do(t, w, func(u *gunim.UI) { c.SetCross(false, u) })
	last := c.cross.Value()
	moved := 0
	for range 40 {
		run(1)
		if v := c.cross.Value(); v != last {
			moved++
			if d := last - v; d > 0.5 {
				t.Fatalf("the cross jumped from %v to %v", last, v)
			}
			last = v
		}
		if d := width - c.size.W; d > 8 || d < -8 {
			t.Fatalf("the chip jumped from %v wide to %v", width, c.size.W)
		}
		width = c.size.W
	}
	if moved < 3 || last > 0.01 {
		t.Fatalf("the cross moved on %d frames and ended at %v", moved, last)
	}
	click(w, c.crossX, c.size.H/2)
	run(1)
	if gone != 0 {
		t.Fatal("a click where the hidden cross was removed the chip")
	}
}

// A chip of keys keeps its width as its cross comes and goes, and draws
// inside its box.
func TestAChipOfKeysHoldsStill(t *testing.T) {
	c := NewChip("", "Ctrl+K")
	c.Keys = []string{"Ctrl", "K"}
	w, run := stage(t, &frame{child: Row(c), size: geom.Sz(300, 100)})
	run(1)
	width := c.size.W
	do(t, w, func(u *gunim.UI) { c.SetCross(false, u) })
	for range 30 {
		run(1)
		if c.size.W != width {
			t.Fatalf("the chip went from %v wide to %v", width, c.size.W)
		}
	}
	box := geom.Sz(width, c.size.H)
	if bad := spills(painted(c, box), box, 3); len(bad) > 0 {
		t.Fatalf("it paints outside its box: %v", bad)
	}
}
