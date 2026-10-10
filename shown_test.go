package gunim

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// hearsShown records the window's hidden and shown events.
type hearsShown struct{ got []input.Event }

func (*hearsShown) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (*hearsShown) Paint(*paint.Painter, Frame, geom.Size, Children)    {}
func (*hearsShown) Focusable() bool                                     { return true }
func (h *hearsShown) Handle(e input.Event, _ *UI) bool {
	switch e.(type) {
	case input.WindowHidden, input.WindowShown:
		h.got = append(h.got, e)
		return true
	}
	return false
}

func TestAWindowHiddenAndShownTellsTheFocusedNode(t *testing.T) {
	h := &hearsShown{}
	w := NewOffscreen(geom.Sz(200, 200), h)
	w.Frame(time.Second / 60)
	w.Input(driver.WindowShown{Shown: false})
	w.Input(driver.WindowShown{Shown: true})
	w.Frame(time.Second / 60)
	if len(h.got) != 2 {
		t.Fatalf("heard %v, want hidden then shown", h.got)
	}
	if _, ok := h.got[0].(input.WindowHidden); !ok {
		t.Fatalf("first heard %T, want WindowHidden", h.got[0])
	}
	if _, ok := h.got[1].(input.WindowShown); !ok {
		t.Fatalf("then heard %T, want WindowShown", h.got[1])
	}
}

// modalShown is hearsShown holding the keyboard, as a dialog or an
// intro over a game does.
type modalShown struct{ hearsShown }

func (*modalShown) Modal() bool { return true }

func TestEveryViewHearsTheWindowHiddenAndShownOnce(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 200), nil)
	type game struct{}
	type intro struct{}
	g, m := &hearsShown{}, &modalShown{}
	RegisterView(w, "game", func(game) *hearsShown { return g }, func(*hearsShown, game, *UI) {})
	RegisterView(w, "intro", func(intro) *modalShown { return m }, func(*modalShown, intro, *UI) {})
	c := w.Client()
	if err := c.Mount(Root, "game", "game", game{}); err != nil {
		t.Fatal(err)
	}
	_ = c.Focus("game")
	w.Frame(time.Second / 60)
	// The game alone, with the keyboard: it hears each once.
	w.Input(driver.WindowShown{Shown: false})
	w.Input(driver.WindowShown{Shown: true})
	if len(g.got) != 2 {
		t.Fatalf("the focused game heard %v, want hidden and shown once each", g.got)
	}
	// The intro over it holds the keyboard and takes what it hears; the
	// game under it hears the window go and come back all the same.
	if err := c.Mount(Root, "intro", "intro", intro{}); err != nil {
		t.Fatal(err)
	}
	_ = c.Focus("intro")
	w.Frame(time.Second / 60)
	g.got = nil
	w.Input(driver.WindowShown{Shown: false})
	if len(m.got) != 1 || len(g.got) != 1 {
		t.Fatalf("hidden under the intro: the intro heard %v, the game %v; want one each", m.got, g.got)
	}
	if _, ok := g.got[0].(input.WindowHidden); !ok {
		t.Fatalf("the game heard %T, want WindowHidden", g.got[0])
	}
	w.Input(driver.WindowShown{Shown: true})
	if len(m.got) != 2 || len(g.got) != 2 {
		t.Fatalf("shown again: the intro heard %v, the game %v; want two each", m.got, g.got)
	}
	if _, ok := g.got[1].(input.WindowShown); !ok {
		t.Fatalf("the game heard %T, want WindowShown", g.got[1])
	}
}

func TestAHiddenWindowDrawsNothingUntilShownOrLeaving(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 200), &hearsShown{})
	w.Frame(time.Second / 60)
	if !w.draws() {
		t.Fatal("a window shown does not draw")
	}
	w.Input(driver.WindowShown{Shown: false})
	if w.draws() {
		t.Fatal("a hidden window draws")
	}
	w.ui.startLeaving()
	if !w.draws() {
		t.Fatal("a hidden window leaving does not draw its way out")
	}
	w.ui.goingAway = false
	w.Input(driver.WindowShown{Shown: true})
	if !w.draws() || !w.resumed {
		t.Fatalf("shown again: draws %v, resumed %v; want it drawing, from where it held", w.draws(), w.resumed)
	}
}

func TestACoveredWindowRestsAndTheApplicationHearsNothing(t *testing.T) {
	h := &hearsShown{}
	w := NewOffscreen(geom.Sz(200, 200), h)
	w.Frame(time.Second / 60)
	w.Input(driver.WindowCovered{Covered: true})
	if w.draws() {
		t.Fatal("a covered window draws")
	}
	// Minimized and restored while covered, it stays at rest.
	w.Input(driver.WindowShown{Shown: false})
	w.Input(driver.WindowShown{Shown: true})
	if w.draws() {
		t.Fatal("a covered window restored draws while still covered")
	}
	w.Input(driver.WindowCovered{Covered: false})
	if !w.draws() || !w.resumed {
		t.Fatalf("uncovered: draws %v, resumed %v; want it drawing, from where it held", w.draws(), w.resumed)
	}
	if len(h.got) != 2 {
		t.Fatalf("heard %v, want only the hide and the show, none of the covering", h.got)
	}
}

// hearsSeek records the media seeks it hears.
type hearsSeek struct{ at []time.Duration }

func (*hearsSeek) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (*hearsSeek) Paint(*paint.Painter, Frame, geom.Size, Children)    {}
func (*hearsSeek) Focusable() bool                                     { return true }
func (h *hearsSeek) Handle(e input.Event, _ *UI) bool {
	if s, ok := e.(input.MediaSeek); ok {
		h.at = append(h.at, s.At)
		return true
	}
	return false
}

func TestAMediaSeekGoesToTheFocusedNode(t *testing.T) {
	h := &hearsSeek{}
	w := NewOffscreen(geom.Sz(200, 200), h)
	w.Frame(time.Second / 60)
	w.Input(input.MediaSeek{At: 42 * time.Second})
	if len(h.at) != 1 || h.at[0] != 42*time.Second {
		t.Fatalf("heard seeks %v, want 42s", h.at)
	}
}

// playKeys records, for each media key it hears, whether the state it
// was last given says the music plays.
type playKeys struct {
	playing bool
	heard   []bool
}

func (*playKeys) Layout(c Constraints, _ Frame, _ Children) geom.Size { return c.Max }
func (*playKeys) Paint(*paint.Painter, Frame, geom.Size, Children)    {}
func (*playKeys) Focusable() bool                                     { return true }
func (p *playKeys) Handle(e input.Event, _ *UI) bool {
	if k, ok := e.(input.KeyPress); ok && k.Key == input.KeyMediaPlay {
		p.heard = append(p.heard, p.playing)
		return true
	}
	return false
}

func TestAHiddenWindowsInputSeesTheStateSentWhileHidden(t *testing.T) {
	w := NewOffscreen(geom.Sz(200, 200), nil)
	type player struct{ Playing bool }
	keys := &playKeys{}
	RegisterView(w, "player", func(player) *playKeys { return keys }, func(k *playKeys, s player, u *UI) {
		k.playing = s.Playing
		u.Focus(k)
	})
	c := w.Client()
	if err := c.Mount(Root, "player", "player", player{Playing: true}, "player"); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	// The screen goes off; the music pauses from the lock screen, and the
	// application sends the state; then play is pressed there.
	w.Input(driver.WindowShown{Shown: false})
	if err := c.Publish("player", player{Playing: false}); err != nil {
		t.Fatal(err)
	}
	w.Input(input.KeyPress{Key: input.KeyMediaPlay})
	if len(keys.heard) != 1 || keys.heard[0] {
		t.Fatalf("play, pressed while hidden, saw playing %v, want the paused state sent while hidden", keys.heard)
	}
}
