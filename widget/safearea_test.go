package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
)

func TestASurfaceKeepsItsViewsClearOfAPhonesBars(t *testing.T) {
	s := NewSurface()
	w := gunimtest.New(t, geom.Sz(400, 800), s)
	w.PaintWhenLooked(false)
	w.Offscreen().SetSafeArea(geom.Insets{Top: 48, Bottom: 24, Left: 10})
	spot := &probe{}
	gunim.RegisterView(w, "view", func(struct{}) gunim.Node { return spot }, nil)
	if err := w.Client().Mount(gunim.Root, "v", "view", struct{}{}); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	w.Frame(time.Second / 60)
	if spot.at != geom.Pt(10, 48) {
		t.Fatalf("the view lies at %v, want clear of the bars, at 10, 48", spot.at)
	}
	if want := geom.Sz(390, 728); spot.got != want {
		t.Fatalf("the view is %v, want the window less the bars, %v", spot.got, want)
	}
}

// probe records the size it is given and where it is painted.
type probe struct {
	got geom.Size
	at  geom.Point
}

func (p *probe) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	p.got = c.Max
	return c.Max
}

func (p *probe) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, _ gunim.Children) {
	p.at = pt.Transform().Apply(geom.Point{})
}
