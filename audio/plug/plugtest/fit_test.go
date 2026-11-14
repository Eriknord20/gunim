package plugtest

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// spots lays each child out at its rectangle, and draws each of texts at its point.
type spots struct {
	kids  []gunim.Node
	at    []geom.Rect
	texts []string
	where []geom.Point
	// ribbon is a ribbon drawn under the children.
	ribbon []paint.RibbonPoint
}

func (s *spots) Children() []gunim.Node { return s.kids }

func (s *spots) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for i := range s.kids {
		kids.At(i).Layout(gunim.Tight(s.at[i].Size()))
		kids.At(i).Place(s.at[i].Min)
	}
	return c.Max
}

func (s *spots) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	if len(s.ribbon) > 0 {
		p.Ribbon(s.ribbon)
	}
	for i, t := range s.texts {
		audioui.Shaped(t, 12, false, false).Paint(p, s.where[i], widget.Ink.Get(f.Theme))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

func knob(name string) *plugui.Knob {
	return plugui.NewKnobFunc(plug.Param{ID: 1, Name: name, Max: 1}, 0.5, func(*gunim.UI) {},
		func(float64, *gunim.UI) {}, func(*gunim.UI) {})
}

// A view laid out with room for each part passes.
func TestFitsPassesAViewWithRoom(t *testing.T) {
	v := &spots{kids: []gunim.Node{knob("Gain"), knob("Pan")},
		at:    []geom.Rect{geom.Rc(10, 30, plugui.KnobW, plugui.KnobH), geom.Rc(80, 30, plugui.KnobW, plugui.KnobH)},
		texts: []string{"INPUT"}, where: []geom.Point{geom.Pt(10, 6)}}
	if got := Misfits(t, v, geom.Sz(200, 140), Fit{}); len(got) != 0 {
		t.Errorf("a view with room for its parts misfits: %v", got)
	}
}

// Each thing wrong is named: controls overlapping, one outside the view, one squeezed, a part under the least size
// given it, texts over each other, a text outside, and a text cut short.
func TestFitsNamesWhatIsWrong(t *testing.T) {
	meter := &spots{}
	label := widget.NewLabel("A label far too long for its room")
	label.MaxLines = 1
	v := &spots{
		kids: []gunim.Node{knob("Gain"), knob("Pan"), knob("Width"), knob("Drive"), meter, label},
		at: []geom.Rect{geom.Rc(10, 30, plugui.KnobW, plugui.KnobH), geom.Rc(40, 30, plugui.KnobW, plugui.KnobH),
			geom.Rc(170, 30, plugui.KnobW, plugui.KnobH), geom.Rc(10, 110, 30, 30), geom.Rc(120, 110, 40, 20),
			geom.Rc(60, 150, 60, 16)},
		texts: []string{"LEVEL", "LEVEL", "EDGE"},
		where: []geom.Point{geom.Pt(10, 4), geom.Pt(16, 6), geom.Pt(180, 175)},
	}
	got := Misfits(t, v, geom.Sz(200, 190), Fit{Least: map[gunim.Node]geom.Size{meter: geom.Sz(80, 40)}})
	for _, want := range []string{
		`"Gain" (*plugui.Knob) at`,                         // overlaps Pan
		`"Width" (*plugui.Knob) at`,                        // outside
		`"Drive" (*plugui.Knob) is 30×30, under its least`, // squeezed
		`*plugtest.spots is 40×20, under its least size 80×40`,
		`text "LEVEL" at`,
		`text "EDGE" at`,
		`cut short`,
	} {
		if !slices.ContainsFunc(got, func(m Misfit) bool { return strings.Contains(m.Text, want) }) {
			t.Errorf("nothing says %q in %v", want, got)
		}
	}
}

// A part laid over others on purpose may overlap them.
func TestFitsLetsAPartLieOverOthers(t *testing.T) {
	card := knob("Card")
	v := &spots{kids: []gunim.Node{knob("Gain"), card},
		at: []geom.Rect{geom.Rc(10, 30, plugui.KnobW, plugui.KnobH), geom.Rc(30, 40, plugui.KnobW, plugui.KnobH)}}
	if got := Misfits(t, v, geom.Sz(200, 140), Fit{Over: []gunim.Node{card}}); slices.ContainsFunc(got,
		func(m Misfit) bool { return m.Kind == Overlaps }) {
		t.Errorf("a card laid over a knob on purpose misfits: %v", got)
	}
}

// has reports whether got holds a misfit of kind k that says want.
func has(got []Misfit, k MisfitKind, want string) bool {
	return slices.ContainsFunc(got, func(m Misfit) bool { return m.Kind == k && strings.Contains(m.Text, want) })
}

// A small control lying wholly inside a larger one, beside it in the tree, overlaps it, unless the larger one is a
// backdrop.
func TestFitsNamesASmallControlOverALargerOne(t *testing.T) {
	gain := knob("Gain")
	v := &spots{kids: []gunim.Node{gain, widget.NewButton("X")},
		at: []geom.Rect{geom.Rc(10, 30, plugui.KnobW, plugui.KnobH), geom.Rc(20, 40, 24, 20)}}
	if got := Misfits(t, v, geom.Sz(200, 140), Fit{}); !has(got, Overlaps, `"Gain" (*plugui.Knob)`) {
		t.Errorf("a button laid on a knob is not named as overlapping it: %v", got)
	}
	if got := Misfits(t, v, geom.Sz(200, 140), Fit{Backdrops: []gunim.Node{gain}}); len(got) != 0 {
		t.Errorf("a button laid on a knob named as a backdrop misfits: %v", got)
	}
}

// A part squeezed to nothing is named, unless it is hidden on purpose. A knob given no room lays out at its own size,
// as a zero bound sets no limit, so it is not squeezed.
func TestFitsNamesAPartSqueezedToNothing(t *testing.T) {
	for _, at := range []geom.Rect{geom.Rc(10, 30, 0, 0), geom.Rc(10, 30, 0, 76)} {
		meter := &spots{}
		k := knob("Knee")
		v := &spots{kids: []gunim.Node{meter, k}, at: []geom.Rect{at, geom.Rc(100, 30, 0, 0)}}
		f := Fit{Least: map[gunim.Node]geom.Size{meter: geom.Sz(30, 40)}}
		if got := Misfits(t, v, geom.Sz(200, 140), f); !has(got, Squeezed, "*plugtest.spots is squeezed") ||
			len(got) != 1 {
			t.Errorf("a meter laid out at %v×%v, and a knob given no room, misfit: %v", at.Size().W, at.Size().H, got)
		}
		f.Hidden = []gunim.Node{meter}
		if got := Misfits(t, v, geom.Sz(200, 140), f); len(got) != 0 {
			t.Errorf("a meter hidden on purpose at %v×%v misfits: %v", at.Size().W, at.Size().H, got)
		}
	}
}

// A ribbon drawn past the view's edge spills.
func TestFitsNamesARibbonOutsideTheView(t *testing.T) {
	ink := color.NRGBA{R: 0xff, A: 0xff}
	v := &spots{ribbon: []paint.RibbonPoint{{At: geom.Pt(20, 20), Width: 2, Color: ink},
		{At: geom.Pt(900, 700), Width: 2, Color: ink}}}
	if got := Misfits(t, v, geom.Sz(200, 140), Fit{}); !has(got, Spills, "drawn outside the view") {
		t.Errorf("a ribbon drawn to 900,700 in a 200×140 view is not named: %v", got)
	}
}

// squeezing lays its knob out at its box's right edge, however small the box is.
type squeezing struct {
	plug.Room
	least geom.Size
	k     *plugui.Knob
}

func (s *squeezing) Children() []gunim.Node { return []gunim.Node{s.k} }

func (s *squeezing) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	laid := s.Lay(c.Max, s.least)
	kids.At(0).Layout(gunim.Tight(geom.Sz(plugui.KnobW, plugui.KnobH)))
	kids.At(0).Place(geom.Pt(laid.W-plugui.KnobW-8, 8))
	return c.Max
}

func (s *squeezing) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	defer s.Room.Paint(p, f)()
	kids.At(0).Paint(p)
}

// UnderLeast passes a view that lays out at its least size and clips, and names one that squeezes into a smaller box.
func TestUnderLeastNamesAViewThatSqueezes(t *testing.T) {
	least := geom.Sz(300, 200)
	ok := &fakeT{TB: t}
	UnderLeast(ok, func() gunim.Node { return &squeezing{least: least, k: knob("Gain")} }, geom.Sz(150, 100), least)
	if len(ok.errs) != 0 {
		t.Errorf("a view that lays out at its least size and clips is named: %v", ok.errs)
	}
	bad := &fakeT{TB: t}
	UnderLeast(bad, func() gunim.Node { return &squeezing{k: knob("Gain")} }, geom.Sz(150, 100), least)
	if len(bad.errs) == 0 {
		t.Error("a view that squeezes into a box under its least size is not named")
	}
}

// fakeT is a testing.TB that notes its errors in place of failing.
type fakeT struct {
	testing.TB
	errs []string
}

func (f *fakeT) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func (f *fakeT) Helper() {}

// skipping lays out a knob at its top left and one at its bottom right, at its least size, and paints only the knobs
// that show.
type skipping struct {
	plug.Room
	least geom.Size
	ks    [2]*plugui.Knob
	at    [2]geom.Point
}

func (s *skipping) Children() []gunim.Node { return []gunim.Node{s.ks[0], s.ks[1]} }

func (s *skipping) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	laid := s.Lay(c.Max, s.least)
	s.at = [2]geom.Point{geom.Pt(8, 8), geom.Pt(laid.W-plugui.KnobW-8, laid.H-plugui.KnobH-8)}
	for i := range s.ks {
		kids.At(i).Layout(gunim.Tight(geom.Sz(plugui.KnobW, plugui.KnobH)))
		kids.At(i).Place(s.at[i])
	}
	return c.Max
}

func (s *skipping) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	defer s.Room.Paint(p, f)()
	vis, clipped := p.Visible()
	for i := range s.ks {
		if !clipped || !intersect(vis, geom.Rect{Min: s.at[i], Max: s.at[i].Add(geom.Pt(plugui.KnobW,
			plugui.KnobH))}).Empty() {
			kids.At(i).Paint(p)
		}
	}
}

// UnderLeast passes a view that leaves out the parts it scrolled out of sight, as a view that draws only what shows.
func TestUnderLeastPassesAViewThatDrawsOnlyWhatShows(t *testing.T) {
	least := geom.Sz(300, 200)
	ft := &fakeT{TB: t}
	UnderLeast(ft, func() gunim.Node { return &skipping{least: least, ks: [2]*plugui.Knob{knob("A"), knob("B")}} },
		geom.Sz(150, 100), least)
	if len(ft.errs) != 0 {
		t.Errorf("a view that draws only the knob that shows is named: %v", ft.errs)
	}
}

// unpainting lays out its children and paints none of them.
type unpainting struct{ kids []gunim.Node }

func (u *unpainting) Children() []gunim.Node { return u.kids }

func (u *unpainting) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for i := range u.kids {
		kids.At(i).Layout(gunim.Tight(geom.Sz(plugui.KnobW, plugui.KnobH)))
		kids.At(i).Place(geom.Pt(10+float32(i)*80, 10))
	}
	return c.Max
}

func (u *unpainting) Paint(*paint.Painter, gunim.Frame, geom.Size, gunim.Children) {}

// A control or a part in f.Least that is never painted is named, unless it is hidden on purpose.
func TestFitsNamesAPartNeverPainted(t *testing.T) {
	meter, gain := &spots{}, knob("Gain")
	v := &unpainting{kids: []gunim.Node{meter, gain}}
	f := Fit{Least: map[gunim.Node]geom.Size{meter: geom.Sz(30, 40)}}
	got := Misfits(t, v, geom.Sz(200, 140), f)
	if !has(got, Unpainted, "*plugtest.spots") || !has(got, Unpainted, `"Gain" (*plugui.Knob)`) {
		t.Errorf("a meter and a knob never painted are not both named: %v", got)
	}
	f.Hidden = []gunim.Node{meter, gain}
	if got := Misfits(t, v, geom.Sz(200, 140), f); len(got) != 0 {
		t.Errorf("a meter and a knob hidden on purpose misfit: %v", got)
	}
}

// stuck is a squeezing view whose own Handle, Step and Reveal hide its Room's.
type stuck struct{ squeezing }

func (s *stuck) Handle(input.Event, *gunim.UI) bool { return false }
func (s *stuck) Step(time.Duration) bool            { return false }
func (s *stuck) Reveal(geom.Rect, *gunim.UI)        {}
func (s *stuck) Children() []gunim.Node             { return s.squeezing.Children() }

// UnderLeast names a view that lays out at its least size and does not scroll, as one that hides its Room's methods.
func TestUnderLeastNamesAViewThatDoesNotScroll(t *testing.T) {
	least := geom.Sz(300, 200)
	ft := &fakeT{TB: t}
	UnderLeast(ft, func() gunim.Node { return &stuck{squeezing{least: least, k: knob("Gain")}} }, geom.Sz(150, 100),
		least)
	for _, want := range []string{"the wheel scrolls the view nowhere", "a drag on empty ground scrolls the view nowhere",
		"out of sight"} {
		if !slices.ContainsFunc(ft.errs, func(e string) bool { return strings.Contains(e, want) }) {
			t.Errorf("nothing says %q in %v", want, ft.errs)
		}
	}
}
