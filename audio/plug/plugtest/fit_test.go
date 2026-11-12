package plugtest

import (
	"slices"
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// spots lays each child out at its rectangle, and draws each of texts at its point.
type spots struct {
	kids  []gunim.Node
	at    []geom.Rect
	texts []string
	where []geom.Point
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
