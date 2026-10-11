package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// carrier starts a drag of its word when pressed and moved, and
// records how the drag ended.
type carrier struct {
	word  string
	ended []input.DragEnd
	ghost *recorder
}

func (c *carrier) Handle(e input.Event, u *UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		return true
	case input.PointerMove:
		c.ghost = &recorder{}
		u.StartDrag(c, c.word, &sized{recorder: c.ghost, size: geom.Sz(20, 10)}, geom.Pt(5, 5))
		return true
	case input.DragEnd:
		c.ended = append(c.ended, e)
		return true
	}
	return false
}

func (c *carrier) Layout(cs Constraints, _ Frame, _ Children) geom.Size { return cs.Max }
func (c *carrier) Paint(*paint.Painter, Frame, geom.Size, Children)     {}

// sized is a recorder of a fixed size.
type sized struct {
	*recorder
	size geom.Size
}

func (s *sized) Layout(Constraints, Frame, Children) geom.Size { return s.size }

// basket takes a drag of any string, and records what it heard.
type basket struct{ recorder }

func (b *basket) Handle(e input.Event, u *UI) bool {
	switch e := e.(type) {
	case input.DragOver:
		_, ok := e.Data.(string)
		b.events = append(b.events, e)
		return ok
	case input.Drop, input.DragLeave:
		b.events = append(b.events, e)
		return true
	}
	return false
}

// twoWindows returns two windows of one application side by side on a
// pretend screen, 800 wide each: a carrier in the first's left half,
// and a basket filling the second.
func twoWindows(t *testing.T) (a, b *Window, c *carrier, bk *basket) {
	t.Helper()
	app := &App{}
	a, b = newTestWindow(), newTestWindow()
	for i, w := range []*Window{a, b} {
		w.app = app
		app.windows.add(w)
		w.mustOffscreen(t).SetOrigin(geom.Pt(float32(i)*800, 0))
	}
	c = &carrier{word: "apple"}
	a.ui.Insert(a.ui.Root(), &stage{t: paint.Identity})
	a.ui.Insert(a.ui.root.kids[0].node, c)
	bk = &basket{}
	b.ui.Insert(b.ui.Root(), bk)
	run(a, 1)
	run(b, 1)
	return a, b, c, bk
}

func TestADragCarriesItsDataToAnotherWindow(t *testing.T) {
	a, b, c, bk := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	run(a, 1)
	if len(a.ui.popups) != 1 {
		t.Fatal("no picture follows the drag")
	}
	// Out of window a, over window b: 1000 on the screen is 200 in b.
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(b, 1)
	if !bk.got(input.DragOver{}) {
		t.Fatal("the basket in the other window heard no DragOver")
	}
	for _, e := range bk.events {
		if o, ok := e.(input.DragOver); ok && !near(o.Pos, geom.Pt(200, 50)) {
			t.Fatalf("DragOver at %v in the basket's space, want (200, 50)", o.Pos)
		}
	}
	a.Input(input.PointerUp{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(b, 1)
	run(a, 1)
	var drop input.Drop
	for _, e := range bk.events {
		if d, ok := e.(input.Drop); ok {
			drop = d
		}
	}
	if drop.Data != "apple" {
		t.Fatalf("the basket got a drop of %v, want apple", drop.Data)
	}
	if len(c.ended) != 1 || !c.ended[0].Taken {
		t.Fatalf("the carrier heard %v, want one DragEnd, taken", c.ended)
	}
	run(a, 120)
	if len(a.ui.popups) != 0 {
		t.Fatal("the picture under the pointer stayed after the drop")
	}
}

func TestADragLetGoOverNothingEndsUntaken(t *testing.T) {
	a, b, c, bk := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(b, 1)
	// Off both windows: the basket hears it leave.
	a.Input(input.PointerMove{Pos: geom.Pt(3000, 50), Time: time.Now()})
	run(b, 1)
	if !bk.got(input.DragLeave{}) {
		t.Fatal("the basket heard no DragLeave")
	}
	a.Input(input.PointerUp{Pos: geom.Pt(3000, 50), Time: time.Now()})
	run(a, 1)
	if len(c.ended) != 1 || c.ended[0].Taken {
		t.Fatalf("the carrier heard %v, want one DragEnd, untaken", c.ended)
	}
	if e := c.ended[0]; !e.Out || !near(e.At, geom.Pt(3000, 50)) {
		t.Fatalf("the carrier heard %v, want it out of every window, at (3000, 50)", e)
	}
}

func TestADragLetGoOverAWindowIsNotOut(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	// Over its own window, where nothing takes it.
	a.Input(input.PointerUp{Pos: geom.Pt(600, 50), Time: time.Now()})
	run(a, 1)
	if len(c.ended) != 1 || c.ended[0].Taken || c.ended[0].Out {
		t.Fatalf("the carrier heard %v, want one DragEnd, untaken and not out", c.ended)
	}
}

func TestFilesDroppedOnAWindowReachTheNodeUnderThem(t *testing.T) {
	w := newTestWindow()
	bk := &basket{}
	w.ui.Insert(w.ui.Root(), bk)
	run(w, 1)
	w.Input(input.Drop{Pos: geom.Pt(10, 10), Paths: []string{"/tmp/a.png"}})
	var got input.Drop
	for _, e := range bk.events {
		if d, ok := e.(input.Drop); ok {
			got = d
		}
	}
	if len(got.Paths) != 1 || got.Paths[0] != "/tmp/a.png" {
		t.Fatalf("the basket got %v, want the dropped file", got)
	}
}

// exportable is drag data that leaves the application as a file.
type exportable struct{ path string }

func (e exportable) ExportFiles() ([]string, error) { return []string{e.path}, nil }

func TestADragLeavingEveryWindowGoesToOtherProgramsAsFiles(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	c.word = ""
	// A carrier of exportable data.
	x := &exporter{carrier: c, data: exportable{"/tmp/apple.png"}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	run(a, 1)
	// Over the other window it is still a drag inside the application.
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	if out := a.mustOffscreen(t).DraggedOut(); len(out) != 0 {
		t.Fatalf("handed out over a window of the application: %v", out)
	}
	// Past every window, it goes out as the file.
	a.Input(input.PointerMove{Pos: geom.Pt(3000, 50), Time: time.Now()})
	out := a.mustOffscreen(t).DraggedOut()
	if len(out) != 1 || len(out[0]) != 1 || out[0][0] != "/tmp/apple.png" {
		t.Fatalf("handed out %v, want the file once", out)
	}
	if len(a.ui.popups) != 0 {
		t.Fatal("the picture under the pointer stayed once the drag went out")
	}
	a.Input(driver.DragOutEnded{Taken: true})
	if len(x.ended) != 1 || !x.ended[0].Taken {
		t.Fatalf("the source heard %v, want one DragEnd, taken", x.ended)
	}
}

// exporter is a carrier whose drag carries data of its own.
type exporter struct {
	*carrier
	data any
}

func (e *exporter) Handle(ev input.Event, u *UI) bool {
	if _, ok := ev.(input.PointerMove); ok {
		u.StartDrag(e, e.data, &sized{recorder: &recorder{}, size: geom.Sz(20, 10)}, geom.Pt(5, 5))
		return true
	}
	return e.carrier.Handle(ev, u)
}

func TestADragCrossingTheGapBetweenWindowsStaysInside(t *testing.T) {
	a, b, c, bk := twoWindows(t)
	// A gap of 20 between the windows.
	b.mustOffscreen(t).SetOrigin(geom.Pt(820, 0))
	x := &exporter{carrier: c, data: exportable{"/tmp/apple.png"}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(810, 50), Time: time.Now()})
	run(a, 2)
	if out := a.mustOffscreen(t).DraggedOut(); len(out) != 0 {
		t.Fatalf("crossing the gap handed the drag out: %v", out)
	}
	if len(a.ui.popups) != 1 {
		t.Fatal("the picture under the pointer went in the gap")
	}
	a.Input(input.PointerMove{Pos: geom.Pt(900, 50), Time: time.Now()})
	a.Input(input.PointerUp{Pos: geom.Pt(900, 50), Time: time.Now()})
	run(b, 1)
	run(a, 1)
	var dropped bool
	for _, e := range bk.events {
		if _, ok := e.(input.Drop); ok {
			dropped = true
		}
	}
	if !dropped || len(a.mustOffscreen(t).DraggedOut()) != 0 {
		t.Fatal("the drop across the gap did not land inside the application")
	}
}

func TestADragLeavesOnlyOnceFarFromEveryWindow(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	x := &exporter{carrier: c, data: exportable{"/tmp/apple.png"}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	// Just past the right window's edge, and resting there a while: a
	// slow crossing looks the same.
	a.Input(input.PointerMove{Pos: geom.Pt(1620, 50), Time: time.Now()})
	run(a, 60)
	if len(a.mustOffscreen(t).DraggedOut()) != 0 {
		t.Fatal("handed out close to a window")
	}
	a.Input(input.PointerMove{Pos: geom.Pt(1700, 50), Time: time.Now()})
	if len(a.mustOffscreen(t).DraggedOut()) != 1 {
		t.Fatal("a drag far from every window was not handed out")
	}
}

// fakeCoverer says another program's window lies in front of the
// application's windows within the screen rectangle over.
type fakeCoverer struct{ over geom.Rect }

func (f fakeCoverer) Covered(_ []driver.Window, p geom.Point) (covered, known bool) {
	return f.over.Contains(p), true
}

func TestADragOverAnotherProgramsWindowInFrontLeavesAtOnce(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	// Another program's window lies over the right one, from 1000 to
	// 1200 across.
	a.app.windows.coverer = fakeCoverer{over: geom.Rc(1000, 0, 200, 600)}
	x := &exporter{carrier: c, data: exportable{"/tmp/apple.png"}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(900, 50), Time: time.Now()})
	if len(a.mustOffscreen(t).DraggedOut()) != 0 {
		t.Fatal("handed out over a window of the application in front")
	}
	a.Input(input.PointerMove{Pos: geom.Pt(1100, 50), Time: time.Now()})
	if out := a.mustOffscreen(t).DraggedOut(); len(out) != 1 {
		t.Fatalf("over another program's window in front, handed out %v, want the file once", out)
	}
}

// answerer is a basket that says what a drop on it would do.
type answerer struct {
	basket
	answer string
}

func (a *answerer) Handle(e input.Event, u *UI) bool {
	if _, ok := e.(input.DragOver); ok {
		u.AnswerDrag(a.answer)
	}
	return a.basket.Handle(e, u)
}

// ghostHeard returns the events of the kind of want the carrier's ghost heard.
func ghostHeard[E input.Event](c *carrier) []E {
	var out []E
	for _, e := range c.ghost.events {
		if v, ok := e.(E); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestTheAnswerOfTheNodeUnderADragReachesItsPicture(t *testing.T) {
	a, b, c, bk := twoWindows(t)
	ans := &answerer{answer: "Move here"}
	b.ui.Remove(bk)
	b.ui.Insert(b.ui.Root(), ans)
	run(b, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(b, 1)
	run(a, 1)
	answers := ghostHeard[input.DragAnswer](c)
	if len(answers) == 0 || answers[len(answers)-1].Answer != "Move here" {
		t.Fatalf("the picture heard %v, want the answer from the other window", answers)
	}
	if moves := ghostHeard[input.DragMove](c); len(moves) < 2 || !near(moves[len(moves)-1].At, geom.Pt(1000, 50)) {
		t.Fatalf("the picture heard moves %v, want the last at (1000, 50) on the screen", moves)
	}
	// Off every window, nothing answers.
	a.Input(input.PointerMove{Pos: geom.Pt(3000, 50), Time: time.Now()})
	answers = ghostHeard[input.DragAnswer](c)
	if answers[len(answers)-1].Answer != nil {
		t.Fatalf("off the windows the picture still heard %v", answers[len(answers)-1].Answer)
	}
}

func TestModifierKeysReachTheNodeUnderADrag(t *testing.T) {
	w := newTestWindow()
	c := &carrier{word: "pear"}
	bk := &basket{}
	p := &apart{}
	w.ui.Insert(w.ui.Root(), p)
	w.ui.Insert(p, c)
	w.ui.Insert(p, bk)
	run(w, 1)
	w.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	w.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	w.Input(input.PointerMove{Pos: geom.Pt(250, 250), Mods: input.ModShift, Time: time.Now()})
	w.Input(input.KeyPress{Key: input.KeyLeftControl, Mods: input.ModShift, Time: time.Now()})
	w.Input(input.PointerUp{Pos: geom.Pt(250, 250), Mods: input.ModShift | input.ModControl, Time: time.Now()})
	var over []input.Mods
	var drop input.Drop
	for _, e := range bk.events {
		switch e := e.(type) {
		case input.DragOver:
			over = append(over, e.Mods)
		case input.Drop:
			drop = e
		}
	}
	if len(over) < 2 || over[0] != input.ModShift || over[1] != input.ModShift|input.ModControl {
		t.Fatalf("DragOver carried %v, want Shift and then Shift with Ctrl", over)
	}
	if drop.Mods != input.ModShift|input.ModControl {
		t.Fatalf("the drop carried %v, want Shift with Ctrl", drop.Mods)
	}
}

func TestEscapeGivesADragUp(t *testing.T) {
	a, b, c, bk := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(b, 1)
	a.Input(input.KeyPress{Key: input.KeyEscape, Time: time.Now()})
	run(b, 1)
	if !bk.got(input.DragLeave{}) {
		t.Fatal("the basket did not hear the drag leave")
	}
	if len(c.ended) != 1 || c.ended[0].Taken {
		t.Fatalf("the carrier heard %v, want one DragEnd, untaken", c.ended)
	}
	a.Input(input.PointerUp{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(b, 1)
	if bk.got(input.Drop{}) {
		t.Fatal("a drag given up still dropped")
	}
}

func TestThePictureOfADragStaysUntilTheDropIsTaken(t *testing.T) {
	a, b, c, _ := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	a.Input(input.PointerUp{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(a, 1)
	if len(a.ui.popups) != 1 || len(ghostHeard[input.DragEnd](c)) != 0 {
		t.Fatal("the picture left before the other window said whether it took the drop")
	}
	run(b, 1)
	run(a, 1)
	ends := ghostHeard[input.DragEnd](c)
	if len(ends) != 1 || !ends[0].Taken {
		t.Fatalf("the picture heard %v, want one DragEnd, taken", ends)
	}
	run(a, 120)
	if len(a.ui.popups) != 0 {
		t.Fatal("the picture stayed after the drop was taken")
	}
}

// apart puts its first child at (10, 10) and its second at (200, 200),
// each 100 square.
type apart struct{ _ byte }

func (p *apart) Layout(c Constraints, _ Frame, kids Children) geom.Size {
	for i, at := range []geom.Point{{X: 10, Y: 10}, {X: 200, Y: 200}}[:kids.Len()] {
		k := kids.At(i)
		k.Layout(Tight(geom.Sz(100, 100)))
		k.Place(at)
	}
	return c.Max
}

func (p *apart) Paint(pt *paint.Painter, _ Frame, _ geom.Size, kids Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

func TestThePictureOfADragOutlivesItsSource(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	run(a, 5)
	a.ui.Remove(c)
	run(a, 120)
	if len(a.ui.popups) != 1 || !a.ui.drag.ghost.Open() {
		t.Fatal("the picture went with the node the drag started from")
	}
	a.Input(input.PointerUp{Pos: geom.Pt(40, 30), Time: time.Now()})
	run(a, 120)
	if len(a.ui.popups) != 0 {
		t.Fatal("the picture stayed after the drag ended")
	}
}

// A drop whose answer is slow reaches its own drag: a second drag, let
// go before the first's answer came, neither hears that answer nor
// takes the first's picture with it.
func TestASlowAnswerReachesItsOwnDrop(t *testing.T) {
	a, b, c, _ := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	run(a, 1)
	first := c.ghost
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	a.Input(input.PointerUp{Pos: geom.Pt(1000, 50), Time: time.Now()})
	run(a, 1)
	// Window b has not drawn: its answer waits. A second drag is let go
	// over window a, where nothing takes it.
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(600, 30), Time: time.Now()})
	a.Input(input.PointerUp{Pos: geom.Pt(600, 30), Time: time.Now()})
	run(a, 1)
	if len(c.ended) != 1 || c.ended[0].Taken {
		t.Fatalf("after the second drop the carrier heard %v, want one DragEnd, untaken", c.ended)
	}
	if first.got(input.DragEnd{}) {
		t.Fatal("the first drag's picture heard an end before the first drop's answer")
	}
	run(b, 1)
	run(a, 1)
	if len(c.ended) != 2 || !c.ended[1].Taken {
		t.Fatalf("after b answered the carrier heard %v, want the first drop's end, taken", c.ended)
	}
	for _, e := range first.events {
		if end, ok := e.(input.DragEnd); ok && !end.Taken {
			t.Fatal("the first drag's picture heard it was not taken")
		}
	}
	if !first.got(input.DragEnd{}) {
		t.Fatal("the first drag's picture never heard its drop was taken")
	}
}

// A drop over a window that never answers, as one that closed with the
// drop unread, ends untaken once dropWait has passed.
func TestADropNobodyAnswersEndsUntaken(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(1000, 50), Time: time.Now()})
	a.Input(input.PointerUp{Pos: geom.Pt(1000, 50), Time: time.Now()})
	// Window b never draws, so never answers.
	run(a, int(dropWait/(time.Second/60))+10)
	if len(c.ended) != 1 || c.ended[0].Taken {
		t.Fatalf("the carrier heard %v, want one DragEnd, untaken", c.ended)
	}
	if len(a.ui.drops) != 0 {
		t.Fatalf("%d drops are still waiting", len(a.ui.drops))
	}
}

// Two drags handed to other programs each hear their own end, in the
// order the platform says them.
func TestDragsHandedOutHearTheirEndsInOrder(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	c.word = ""
	x := &exporter{carrier: c, data: exportable{"/tmp/apple.png"}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	for range 2 {
		a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
		a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
		run(a, 1)
		a.Input(input.PointerMove{Pos: geom.Pt(3000, 50), Time: time.Now()})
		a.Input(input.PointerUp{Pos: geom.Pt(3000, 50), Time: time.Now()})
		run(a, 1)
	}
	if out := a.mustOffscreen(t).DraggedOut(); len(out) != 2 {
		t.Fatalf("handed out %v, want two drags", out)
	}
	a.Input(driver.DragOutEnded{Taken: true})
	a.Input(driver.DragOutEnded{Taken: false})
	if len(x.ended) != 2 || !x.ended[0].Taken || x.ended[1].Taken {
		t.Fatalf("the source heard %v, want the first taken and the second not", x.ended)
	}
	if len(a.ui.drops) != 0 {
		t.Fatalf("%d drops are still waiting", len(a.ui.drops))
	}
}

func TestADragThatWentOutCarriesOnWhenThePointerComesBack(t *testing.T) {
	a, b, c, bk := twoWindows(t)
	x := &exporter{carrier: c, data: exportable{"/tmp/apple.png"}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(3000, 50), Time: time.Now()})
	if len(a.mustOffscreen(t).DraggedOut()) != 1 {
		t.Fatal("the drag far from every window was not handed out")
	}
	// The platform gives the drag back as the pointer comes over the other window
	a.Input(driver.DragOutEnded{Back: true, At: geom.Pt(900, 50)})
	if len(x.ended) != 0 {
		t.Fatalf("the source heard %v as the drag came back, want nothing yet", x.ended)
	}
	a.Input(input.PointerMove{Pos: geom.Pt(900, 50), Time: time.Now()})
	run(a, 1)
	if len(a.ui.popups) != 1 {
		t.Fatal("the picture under the pointer did not come back with the drag")
	}
	a.Input(input.PointerUp{Pos: geom.Pt(900, 50), Time: time.Now()})
	run(b, 1)
	run(a, 1)
	var dropped bool
	for _, e := range bk.events {
		if _, ok := e.(input.Drop); ok {
			dropped = true
		}
	}
	if !dropped {
		t.Fatal("the drag that came back did not drop in the other window")
	}
	if len(x.ended) != 1 || !x.ended[0].Taken {
		t.Fatalf("the source heard %v, want one DragEnd, taken", x.ended)
	}
}

// lateExport is drag data whose files are on their way until ready is
// set, and which says so meanwhile.
type lateExport struct{ ready *bool }

func (e lateExport) ExportFiles() ([]string, error) {
	if !*e.ready {
		return nil, &ExportError{Hint: "fetching", Wait: true}
	}
	return []string{"/tmp/late.png"}, nil
}

func TestADragOfFilesOnTheirWayLeavesOnceTheyArrive(t *testing.T) {
	a, _, c, _ := twoWindows(t)
	ready := false
	x := &exporter{carrier: c, data: lateExport{&ready}}
	a.ui.Remove(c)
	a.ui.Insert(a.ui.root.kids[0].node, x)
	run(a, 60)
	a.Input(input.PointerDown{Pos: geom.Pt(30, 30), Time: time.Now()})
	a.Input(input.PointerMove{Pos: geom.Pt(40, 30), Time: time.Now()})
	run(a, 1)
	a.Input(input.PointerMove{Pos: geom.Pt(3000, 50), Time: time.Now()})
	run(a, 10)
	if out := a.mustOffscreen(t).DraggedOut(); len(out) != 0 {
		t.Fatalf("handed out files not there yet: %v", out)
	}
	if a.ui.drag == nil {
		t.Fatal("the drag ended while its files were on their way")
	}
	ghost, ok := a.ui.drag.picture.(*sized)
	if !ok {
		t.Fatal("the drag carries another picture")
	}
	var hint any
	for _, e := range ghost.events {
		if da, ok := e.(input.DragAnswer); ok {
			hint = da.Answer
		}
	}
	if hint != "fetching" {
		t.Fatalf("the picture says %v, want the hint of the files on their way", hint)
	}
	// The files arrive with the pointer still: the drag asks again by
	// itself.
	ready = true
	time.Sleep(2 * exportRetry)
	run(a, 30)
	out := a.mustOffscreen(t).DraggedOut()
	if len(out) != 1 || len(out[0]) != 1 || out[0][0] != "/tmp/late.png" {
		t.Fatalf("handed out %v, want the file once it arrived", out)
	}
}
