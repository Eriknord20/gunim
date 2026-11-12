package plugui

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// lateHost is a source whose Current follows a pick only once the pick lands.
type lateHost struct {
	presetHost
	waiting *plug.Preset
}

func (s *lateHost) Pick(p plug.Preset, _ *gunim.UI) {
	s.picked = append(s.picked, p.Name)
	s.waiting = &p
}

// land has the source play the preset picked last.
func (s *lateHost) land() {
	if s.waiting != nil {
		s.cur, s.waiting = *s.waiting, nil
	}
}

// Where the source's Current follows a pick late, a skip shows the group of the preset it picked, and a second skip
// before the pick lands steps on from it. The column marks what plays all the while.
func TestASkipShowsTheGroupItPickedBeforeThePickLands(t *testing.T) {
	src := &lateHost{presetHost: *kits(4)}
	src.cur = src.list[5]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) {
		b.showGroup("Factory", "Bass", u)
		b.Skip(1, u)
	})
	if b.group != "Bass" {
		t.Fatalf("with the pick of Bass 0 still to land, the browser shows %s, want Bass", b.group)
	}
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Skip(1, u) })
	if want := []string{"Bass 0", "Bass 1"}; !slices.Equal(src.picked, want) || b.group != "Bass" {
		t.Fatalf("two skips before the first landed picked %v and show %s, want %v in Bass", src.picked, b.group, want)
	}
	if got := playingRow(b); got != "" {
		t.Errorf("before the pick lands, with Kit 2 playing, the presets column marks %q as playing, want none", got)
	}
	src.land()
	if got := playingRow(b); got != "Bass 1" {
		t.Errorf("once the pick landed, the presets column marks %q as playing, want Bass 1", got)
	}
	patch(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Skip(1, u) })
	if got := src.picked[len(src.picked)-1]; got != "Bass 2" {
		t.Errorf("once the pick landed, a skip picked %s, want Bass 2", got)
	}
}

// During a search that leaves the preset playing out, next plays the first preset it finds and previous the last,
// whatever group the browser shows.
func TestASkipDuringASearchMissingThePresetPlayingStartsAtAnEnd(t *testing.T) {
	for _, c := range []struct {
		by   int
		want string
	}{{1, "Sub bass"}, {-1, "Bass piano"}} {
		src := &presetHost{list: []plug.Preset{
			{Bank: "Factory", Group: "Bass", Name: "Sub bass"},
			{Bank: "Factory", Group: "Bass", Name: "Bass two"},
			{Bank: "Factory", Group: "Keys", Name: "Piano"},
			{Bank: "Factory", Group: "Keys", Name: "Bass piano"},
		}}
		src.cur = src.list[2]
		b := NewPresetBrowser(src)
		w := mountBrowser(t, b)
		patch(t, w, func(b *PresetBrowser, u *gunim.UI) {
			b.query = "bass"
			b.fill(u)
			b.Skip(c.by, u)
		})
		if want := []string{c.want}; !slices.Equal(src.picked, want) {
			t.Errorf("a skip of %d through a search for bass, Piano playing, picked %v, want %v", c.by, src.picked,
				want)
		}
	}
}

// shutter shows its child, or stops drawing it, as a host closes a browser it keeps.
type shutter struct {
	child gunim.Node
	shut  bool
}

func (s *shutter) Children() []gunim.Node { return []gunim.Node{s.child} }

func (s *shutter) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(c.Max))
	return c.Max
}

func (s *shutter) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if !s.shut {
		kids.At(0).Paint(p)
	}
}

// With the browser closed, next steps from the preset playing, not into a group the user only looked at; open, it
// steps into the group shown.
func TestAClosedBrowserSkipsFromThePresetPlaying(t *testing.T) {
	for _, c := range []struct {
		shut bool
		want string
	}{{true, "Big hall"}, {false, "Kick"}} {
		src := &presetHost{list: []plug.Preset{
			{Bank: "Factory", Group: "Drums", Name: "Kick"},
			{Bank: "Factory", Group: "Drums", Name: "Snare"},
			{Bank: "Factory", Group: "Rooms", Name: "Small room"},
			{Bank: "Factory", Group: "Rooms", Name: "Big hall"},
		}}
		src.cur = src.list[2]
		b := NewPresetBrowser(src)
		sh := &shutter{child: b}
		w := gunimtest.New(t, geom.Sz(360, 480), nil)
		gunim.RegisterView(w, "s", func(struct{}) gunim.Node { return sh }, nil)
		if err := w.Client().Mount(gunim.Root, "s", "s", nil); err != nil {
			t.Fatal(err)
		}
		frames(w, 30)
		gunim.RegisterPatch(w, "s", func(sh *shutter, step string, u *gunim.UI) {
			switch step {
			case "drums":
				b.showGroup("Factory", "Drums", u)
			case "close":
				sh.shut = c.shut
				u.Invalidate()
			case "next":
				b.Skip(1, u)
			}
		})
		for _, step := range []string{"drums", "close", "next"} {
			if err := w.Client().Patch("s", step); err != nil {
				t.Fatal(err)
			}
			frames(w, 2)
		}
		if want := []string{c.want}; !slices.Equal(src.picked, want) {
			t.Errorf("closed %v, next after a look at Drums picked %v, want %v", c.shut, src.picked, want)
		}
	}
}

// Two presets of one name in a group are steps of their own: next goes from the first to the second, and on.
func TestSkipsStepPastTwoPresetsOfOneName(t *testing.T) {
	src := &presetHost{list: []plug.Preset{
		{Bank: "Factory", Group: "Pads", Name: "Pad"},
		{Bank: "Factory", Group: "Pads", Name: "Pad"},
		{Bank: "Factory", Group: "Pads", Name: "Lead"},
	}}
	src.cur = src.list[0]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	gunim.RegisterPatch(w, "browser", func(b *PresetBrowser, by int, u *gunim.UI) { b.Skip(by, u) })
	for _, by := range []int{1, 1, -1, -1} {
		if err := w.Client().Patch("browser", by); err != nil {
			t.Fatal(err)
		}
		frames(w, 2)
	}
	if want := []string{"Pad", "Lead", "Pad", "Pad"}; !slices.Equal(src.picked, want) {
		t.Fatalf("the skips picked %v, want %v", src.picked, want)
	}
	if i := b.presets.current(); i < 0 || b.presets.rows[i].at != 0 {
		t.Errorf("back at the first Pad, the pill marks row %d, want the row of the first Pad", i)
	}
}

// The pressed look stays on the row pressed: it lets go as the pointer leaves that row, and comes back as it returns.
// A second button clicked while the first is held leaves the click to land.
func TestAPressShowsOnTheRowPressedAlone(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	width := float32(360)
	gw := float32(int(width * groupsW))
	top := float32(fieldH + colGap)
	rowAt := func(name string) geom.Point {
		i := slices.IndexFunc(b.presets.rows, func(r listRow) bool { return !r.header && r.p.Name == name })
		return geom.Pt(gw+colGap+20, top+b.presets.rows[i].y+rowH/2)
	}
	sub, growl := rowAt("Sub"), rowAt("Growl")
	w.Input(input.PointerMove{Pos: sub})
	w.Input(input.PointerDown{Pos: sub, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	frames(w, 30)
	pressed := b.presets.press.row
	if b.presets.rows[pressed].p.Name != "Sub" || b.presets.press.down.Value() != 1 {
		t.Fatalf("pressed on Sub, the press shows %v on %s", b.presets.press.down.Value(),
			b.presets.rows[pressed].p.Name)
	}
	w.Input(input.PointerMove{Pos: growl})
	frames(w, 30)
	if v := b.presets.press.down.Value(); v != 0 {
		t.Errorf("with the pointer moved on to Growl, the press still shows %v", v)
	}
	w.Input(input.PointerMove{Pos: sub})
	frames(w, 30)
	if v := b.presets.press.down.Value(); v != 1 {
		t.Errorf("back on Sub, the press shows %v, want 1", v)
	}
	w.Input(input.PointerUp{Pos: sub, Button: input.ButtonPrimary, Time: time.Now()})
	frames(w, 30)
	w.Input(input.PointerMove{Pos: growl})
	w.Input(input.PointerDown{Pos: growl, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerDown{Pos: growl, Button: input.ButtonSecondary, Clicks: 1, Time: time.Now()})
	w.Input(input.PointerUp{Pos: growl, Button: input.ButtonSecondary, Time: time.Now()})
	w.Input(input.PointerUp{Pos: growl, Button: input.ButtonPrimary, Time: time.Now()})
	frames(w, 2)
	if want := []string{"Sub", "Growl"}; !slices.Equal(src.picked, want) {
		t.Errorf("the clicks picked %v, want %v", src.picked, want)
	}
}

// deafHost is a source whose Pick fails: the preset playing stays as it was.
type deafHost struct{ presetHost }

func (s *deafHost) Pick(p plug.Preset, _ *gunim.UI) { s.picked = append(s.picked, p.Name) }

// briefly runs fn in the browser's window and two frames after it, on the window's frame clock.
func briefly(t *testing.T, w *gunim.Window, fn func(b *PresetBrowser, u *gunim.UI)) {
	t.Helper()
	gunim.RegisterPatch(w, "browser", func(b *PresetBrowser, _ struct{}, u *gunim.UI) { fn(b, u) })
	if err := w.Client().Patch("browser", struct{}{}); err != nil {
		t.Fatal(err)
	}
	frames(w, 2)
}

// playingRow is the name of the preset the presets column marks as playing, or "".
func playingRow(b *PresetBrowser) string {
	if i := b.presets.current(); i >= 0 {
		return b.presets.rows[i].p.Name
	}
	return ""
}

// A pick that fails leaves the browser showing the preset that plays, and once a moment has passed, a skip steps from
// that preset, not from the one the browser asked for.
func TestAPickThatFailsShowsWhatPlays(t *testing.T) {
	src := &deafHost{presetHost: *kits(4)}
	src.cur = src.list[5]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	w.TimeByFrames(true)
	briefly(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Skip(1, u) })
	if got := playingRow(b); got != "Kit 2" {
		t.Fatalf("with the pick of Kit 3 failed, the column marks %q as playing, want Kit 2", got)
	}
	frames(w, 90)
	if got := playingRow(b); got != "Kit 2" {
		t.Errorf("a while after the pick failed, the column marks %q as playing, want Kit 2", got)
	}
	briefly(t, w, func(b *PresetBrowser, u *gunim.UI) { b.Skip(-1, u) })
	if want := []string{"Kit 3", "Kit 1"}; !slices.Equal(src.picked, want) {
		t.Errorf("next, a failed pick, and previous a while later picked %v, want %v", src.picked, want)
	}
}

// With the browser shut and the source late, a pick that landed and was undone no longer steers the next skip, once
// the host has the browser follow, or once a moment has passed.
func TestAPickLandedAndUndoneNoLongerSteersASkip(t *testing.T) {
	for _, follows := range []bool{true, false} {
		src := &lateHost{presetHost: *kits(4)}
		src.cur = src.list[5]
		b := NewPresetBrowser(src)
		sh := &shutter{child: b, shut: true}
		w := gunimtest.New(t, geom.Sz(360, 480), nil)
		w.TimeByFrames(true)
		gunim.RegisterView(w, "browser", func(struct{}) gunim.Node { return sh }, nil)
		if err := w.Client().Mount(gunim.Root, "browser", "browser", nil); err != nil {
			t.Fatal(err)
		}
		frames(w, 30)
		gunim.RegisterPatch(w, "browser", func(_ *shutter, step string, u *gunim.UI) {
			switch step {
			case "next":
				b.Skip(1, u)
			case "land":
				src.land()
			case "undo":
				src.cur = src.list[5]
			case "follow":
				if follows {
					b.Follow(u)
				}
			}
		})
		for _, step := range []string{"next", "land", "follow", "undo", "follow"} {
			if err := w.Client().Patch("browser", step); err != nil {
				t.Fatal(err)
			}
			frames(w, 2)
		}
		if !follows {
			frames(w, 90)
		}
		if err := w.Client().Patch("browser", "next"); err != nil {
			t.Fatal(err)
		}
		frames(w, 2)
		if want := []string{"Kit 3", "Kit 3"}; !slices.Equal(src.picked, want) {
			t.Errorf("with the browser following %v, next, undone, and next again picked %v, want %v", follows,
				src.picked, want)
		}
	}
}

// The browser works out where the preset playing stands once, and again only as the source's Current, the list or a
// pick changes, not on every painted frame.
func TestTheBrowserFindsThePresetPlayingOnce(t *testing.T) {
	src := kits(4)
	src.cur = src.list[5]
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	if b.playing() != 5 {
		t.Fatalf("Kit 2 playing, the browser finds it at %d, want 5", b.playing())
	}
	// Renamed behind the browser's back, the list would be scanned afresh only by a browser that scans every frame
	b.list[5].Name = "Renamed"
	frames(w, 2)
	if got := b.playing(); got != 5 {
		t.Errorf("with the source's Current and the list as they were, the browser scanned the list again, to %d", got)
	}
	b.list[5].Name = "Kit 2"
	src.cur = src.list[6]
	if got := b.playing(); got != 6 {
		t.Errorf("with Kit 3 playing, the browser finds it at %d, want 6", got)
	}
}
