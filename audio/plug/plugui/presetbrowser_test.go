package plugui

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// presetHost is a source of presets that plays the one picked.
type presetHost struct {
	list   []plug.Preset
	cur    plug.Preset
	picked []string
}

func (s *presetHost) Presets() []plug.Preset { return s.list }

func (s *presetHost) Current() (plug.Preset, bool) { return s.cur, s.cur.Name != "" }

func (s *presetHost) Pick(p plug.Preset, _ *gunim.UI) {
	s.cur = p
	s.picked = append(s.picked, p.Name)
}

func newPresetHost() *presetHost {
	ps := []plug.Preset{
		{Bank: "User", Group: "Mine", Name: "Warm"},
		{Bank: "Factory", Group: "Bass", Name: "Sub"},
		{Bank: "Factory", Group: "Bass", Name: "Growl", About: "a dirty saw"},
		{Bank: "Factory", Group: "Pads", Name: "Glass"},
	}
	return &presetHost{list: ps, cur: ps[2]}
}

var _ gunim.Animator = (*PresetBrowser)(nil)

// mountBrowser shows b alone in a window, and returns the window.
func mountBrowser(t *testing.T, b *PresetBrowser) *gunim.Window {
	t.Helper()
	w := gunimtest.New(t, geom.Sz(360, 480), nil)
	gunim.RegisterView(w, "browser", func(struct{}) gunim.Node { return b }, nil)
	if err := w.Client().Mount(gunim.Root, "browser", "browser", nil); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		w.Frame(time.Second / 60)
	}
	return w
}

func TestTheBrowserShowsTheGroupOfThePresetPlaying(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	mountBrowser(t, b)
	if b.bank != "Factory" || b.group != "Bass" {
		t.Fatalf("the browser shows %s/%s, want the group of the preset playing", b.bank, b.group)
	}
	i := b.groups.index("Factory", "Bass")
	if got := b.groups.pill.Value(); got != float32(i) {
		t.Fatalf("the groups' pill stands on row %v, want %d", got, i)
	}
	if got := b.presets.pill.Value(); got != float32(b.presets.current()) {
		t.Fatalf("the presets' pill stands on row %v, want %d", got, b.presets.current())
	}
}

func TestSkipsGoRoundTheGroup(t *testing.T) {
	src := newPresetHost()
	b := NewPresetBrowser(src)
	w := mountBrowser(t, b)
	gunim.RegisterPatch(w, "browser", func(b *PresetBrowser, by int, u *gunim.UI) { b.Skip(by, u) })
	for _, by := range []int{1, 1, -1} {
		if err := w.Client().Patch("browser", by); err != nil {
			t.Fatal(err)
		}
		w.Frame(time.Second / 60)
	}
	want := []string{"Sub", "Growl", "Sub"}
	if len(src.picked) != 3 || src.picked[0] != want[0] || src.picked[1] != want[1] || src.picked[2] != want[2] {
		t.Fatalf("the skips picked %v, want %v", src.picked, want)
	}
}

func TestASearchFindsWhatAPresetIs(t *testing.T) {
	b := NewPresetBrowser(newPresetHost())
	b.query = "dirty"
	b.fill(nil)
	var names []string
	for _, r := range b.presets.rows {
		if !r.header {
			names = append(names, r.p.Name)
		}
	}
	if len(names) != 1 || names[0] != "Growl" {
		t.Fatalf("a search for what it sounds like finds %v", names)
	}
	b.query = "factory"
	b.fill(nil)
	if len(b.presets.rows) != 0 {
		t.Fatal("a search finds presets by their bank's name")
	}
}

func TestABrowserOfNoPresetsShowsNoGroup(t *testing.T) {
	b := NewPresetBrowser(&presetHost{})
	mountBrowser(t, b)
	if len(b.groups.rows) != 0 || len(b.presets.rows) != 0 || b.groups.index(b.bank, b.group) >= 0 {
		t.Fatal("a browser of no presets lists rows")
	}
}
