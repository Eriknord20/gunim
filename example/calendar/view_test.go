package main

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/calendar"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// probe is a patch that runs fn on the window's UI goroutine, with the calendar view.
type probe struct{ fn func(v *calView, u *gunim.UI) }

// ui runs fn on the window's UI goroutine, with the calendar view.
func (h *harness) ui(fn func(v *calView, u *gunim.UI)) {
	h.t.Helper()
	if err := h.w.Client().Patch("cal", probe{fn}); err != nil {
		h.t.Fatal(err)
	}
	h.frames(1)
}

// eventAt returns where on the window the event with title shows in the days.
func (h *harness) eventAt(title string) geom.Point {
	h.t.Helper()
	id := h.events(title)[0].ID
	var at geom.Point
	h.ui(func(v *calView, u *gunim.UI) {
		b, ok1 := u.Bounds(v.days)
		box, ok2 := v.days.EventBox(id)
		if !ok1 || !ok2 {
			h.t.Fatalf("%q does not show", title)
		}
		at = b.Min.Add(box.Center())
	})
	return at
}

// click clicks at p.
func (h *harness) click(p geom.Point) {
	h.w.Input(input.PointerMove{Pos: p})
	h.w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1})
	h.w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary})
	h.frames(3)
}

// cardOpen reports whether an event's card is open, and on which event.
func (h *harness) cardOpen() (string, bool) {
	var id string
	var open bool
	h.ui(func(v *calView, u *gunim.UI) { id, open = v.cardID, v.card != nil && v.card.Open() })
	return id, open
}

func TestAClickOpensTheCardAndEscapeClosesIt(t *testing.T) {
	h := newHarness(t)
	h.click(h.eventAt("Dentist"))
	if id, open := h.cardOpen(); !open || id != h.events("Dentist")[0].ID {
		t.Fatalf("a click on the dentist opened %q, open %v", id, open)
	}
	h.w.Input(input.KeyPress{Key: input.KeyEscape})
	h.frames(20)
	if _, open := h.cardOpen(); open {
		t.Fatal("Escape left the card open")
	}
}

func TestAPressOnFreeTimeOnlyClosesTheCard(t *testing.T) {
	h := newHarness(t)
	h.click(h.eventAt("Dentist"))
	// Saturday at about noon, where nothing is.
	var free geom.Point
	h.ui(func(v *calView, u *gunim.UI) {
		b, _ := u.Bounds(v.days)
		free = geom.Pt(b.Min.X+b.Size().W-200, b.Min.Y+b.Size().H-120)
	})
	h.click(free)
	var quick bool
	h.ui(func(v *calView, u *gunim.UI) { quick = v.quick != nil })
	if _, open := h.cardOpen(); open || quick {
		t.Fatalf("a press on free time with the card open: card open %v, new event begun %v", open, quick)
	}
	// The next press there begins an event.
	h.click(free)
	h.ui(func(v *calView, u *gunim.UI) { quick = v.quick != nil })
	if !quick {
		t.Fatal("a press on free time with nothing open began no event")
	}
}

func TestTheCardClosesAsTheViewMovesOn(t *testing.T) {
	h := newHarness(t)
	h.click(h.eventAt("Dentist"))
	h.a.handle(ViewChosen{View: MonthView})
	h.frames(20)
	if _, open := h.cardOpen(); open {
		t.Fatal("the card stayed open over the month")
	}
}

func TestTheEditorTakesTheKeyboardAsItOpens(t *testing.T) {
	h := newHarness(t)
	h.a.handle(EditAsked{ID: h.events("Dentist")[0].ID})
	h.frames(10)
	h.w.Input(input.KeyPress{Key: input.KeyEscape})
	h.frames(10)
	if h.a.editing != nil {
		t.Fatal("Escape did not reach the editor as it opened")
	}
}

func TestANewEventIsNamedBesideWhereItWasDrawn(t *testing.T) {
	h := newHarness(t)
	var from, to geom.Point
	h.ui(func(v *calView, u *gunim.UI) {
		b, _ := u.Bounds(v.days)
		// Saturday, near the bottom of what shows, dragged down a little.
		from = geom.Pt(b.Min.X+b.Size().W-200, b.Min.Y+b.Size().H-150)
		to = from.Add(geom.Pt(0, 60))
	})
	h.w.Input(input.PointerMove{Pos: from})
	h.w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	for i := 1; i <= 4; i++ {
		h.w.Input(input.PointerMove{Pos: from.Add(geom.Pt(0, 15*float32(i)))})
		h.frames(1)
	}
	h.w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	h.frames(5)
	var quick bool
	h.ui(func(v *calView, u *gunim.UI) { quick = v.quick != nil })
	if !quick {
		t.Fatal("drawing out an event did not ask for its title")
	}
	h.w.Input(input.TextInput{Text: "Picnic"})
	h.frames(1)
	h.w.Input(input.KeyPress{Key: input.KeyEnter})
	h.frames(10)
	picnic := h.events("Picnic")
	if len(picnic) != 1 || picnic[0].Start.Weekday() != time.Saturday || picnic[0].End.Sub(picnic[0].Start) < time.Hour {
		t.Fatalf("the new event shows as %+v, want one on Saturday about an hour long", picnic)
	}
}

func TestTheKeysGoOnFromTheEventWhoseCardClosed(t *testing.T) {
	h := newHarness(t)
	var chosen func() string
	h.ui(func(v *calView, u *gunim.UI) {
		u.Focus(v.days)
		chosen = func() string {
			var id string
			h.ui(func(v *calView, u *gunim.UI) { id = v.days.Selected() })
			return id
		}
	})
	key := func(k input.Key) {
		h.w.Input(input.KeyPress{Key: k})
		h.frames(3)
	}
	key(input.KeyDown)
	key(input.KeyRight)
	key(input.KeyDown)
	was := chosen()
	if was == "" {
		t.Fatal("the keys chose no event")
	}
	key(input.KeyEnter)
	if id, open := h.cardOpen(); !open || id != was {
		t.Fatalf("Enter opened the card on %q, open %v, want %q", id, open, was)
	}
	key(input.KeyEscape)
	h.frames(20)
	if got := chosen(); got != was {
		t.Fatalf("after Escape closed the card, %q is chosen, want %q still", got, was)
	}
}

// TestACardSaysWhenAnEventIs, for each of the four shapes an event
// comes in: a whole day, several whole days, hours in one day, and
// hours that run over midnight.
func TestACardSaysWhenAnEventIs(t *testing.T) {
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.Local) }
	for _, c := range []struct {
		name      string
		e         calendar.Event
		day, hour string
	}{
		{"a whole day", calendar.Event{Start: day(12, 0, 0), End: day(13, 0, 0), AllDay: true},
			"Monday 12 October", "All day"},
		{"several whole days", calendar.Event{Start: day(12, 0, 0), End: day(15, 0, 0), AllDay: true},
			"Monday 12 October – Wednesday 14 October", "All day"},
		{"over midnight", calendar.Event{Start: day(12, 22, 0), End: day(14, 2, 0)},
			"Monday 12 October, 22:00 –", "Wednesday 14 October, 02:00"},
	} {
		d, h := when2(c.e)
		if d != c.day || h != c.hour {
			t.Errorf("%s reads %q, %q; want %q, %q", c.name, d, h, c.day, c.hour)
		}
	}
}
