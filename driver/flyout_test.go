package driver

import (
	"testing"

	"github.com/marrasen/gunim/geom"
)

// A 1920 by 1080 monitor with a taskbar 48 high at the bottom.
var (
	screen    = geom.Rc(0, 0, 1920, 1080)
	workAbove = geom.Rc(0, 0, 1920, 1032)
	card      = geom.Sz(360, 480)
)

func TestAFlyoutSitsAboveATrayIconAtTheBottom(t *testing.T) {
	icon := geom.Rc(1700, 1040, 24, 32)
	got := FlyoutPlace(icon, card, workAbove, 12)
	if want := geom.Pt(1724-360, 1032-12-480); got != want {
		t.Fatalf("the card is at %v, want its bottom right %v from the icon's top right: %v", got, 12, want)
	}
}

func TestAFlyoutMovesLeftToStayOnTheScreen(t *testing.T) {
	icon := geom.Rc(1890, 1040, 24, 32)
	got := FlyoutPlace(icon, card, workAbove, 12)
	if want := geom.Pt(1920-12-360, 1032-12-480); got != want {
		t.Fatalf("near the right edge the card is at %v, want %v, 12 in from the edge", got, want)
	}
}

func TestAFlyoutMirrorsATaskbarOnAnotherEdge(t *testing.T) {
	for _, c := range []struct {
		name       string
		icon, work geom.Rect
		want       geom.Point
	}{
		{"top", geom.Rc(1700, 8, 24, 32), geom.Rc(0, 48, 1920, 1032), geom.Pt(1724-360, 48+12)},
		{"left", geom.Rc(8, 900, 32, 24), geom.Rc(48, 0, 1872, 1080), geom.Pt(48+12, 924-480)},
		{"right", geom.Rc(1880, 900, 32, 24), geom.Rc(0, 0, 1872, 1080), geom.Pt(1872-12-360, 924-480)},
	} {
		if got := FlyoutPlace(c.icon, card, c.work, 12); got != c.want {
			t.Errorf("taskbar at the %s: the card is at %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAFlyoutBesideAHiddenTaskbarGoesToTheNearestEdge(t *testing.T) {
	// A taskbar that hides leaves the work area the whole screen, and the icon inside it.
	icon := geom.Rc(1700, 1046, 24, 32)
	got := FlyoutPlace(icon, card, screen, 12)
	if want := geom.Pt(1724-360, 1046-12-480); got != want {
		t.Fatalf("beside a hidden taskbar the card is at %v, want %v", got, want)
	}
}

func TestAFlyoutWithNoIconGoesToTheCorner(t *testing.T) {
	got := FlyoutPlace(geom.Rect{}, card, workAbove, 12)
	if want := geom.Pt(1920-12-360, 1032-12-480); got != want {
		t.Fatalf("with no icon the card is at %v, want the bottom right corner %v", got, want)
	}
}

func TestAFlyoutStaysInsideTheWorkArea(t *testing.T) {
	tall := geom.Sz(360, 2000)
	for _, icon := range []geom.Rect{geom.Rc(-50, 1040, 24, 32), geom.Rc(1700, 1040, 24, 32), geom.Rc(10, 10, 4, 4)} {
		got := FlyoutPlace(icon, card, workAbove, 12)
		if got.X < 12 || got.X+card.W > 1920-12 || got.Y < 12 || got.Y+card.H > 1032-12 {
			t.Errorf("beside %v the card is at %v, outside the work area", icon, got)
		}
		if got := FlyoutPlace(icon, tall, workAbove, 12); got.Y != 12 {
			t.Errorf("a card taller than the work area is at %v beside %v, want its top kept 12 down", got, icon)
		}
	}
}
