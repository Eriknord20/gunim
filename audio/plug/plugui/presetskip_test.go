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
// before the pick lands steps on from it.
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
	if i := b.presets.current(); i < 0 || b.presets.rows[i].p.Name != "Bass 1" {
		t.Errorf("the presets' pill marks row %d, want the row of Bass 1", i)
	}
	src.land()
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
			t.Errorf("a skip of %d through a search for bass, Piano playing, picked %v, want %v", c.by, src.picked, want)
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
		t.Fatalf("pressed on Sub, the press shows %v on %s", b.presets.press.down.Value(), b.presets.rows[pressed].p.Name)
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
