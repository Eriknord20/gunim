package driver

import (
	"testing"

	"github.com/marrasen/gunim/geom"
)

// Two monitors side by side: a primary one at 100% with a task bar
// along its bottom, and one at 200% to its right.
var twoMonitors = []Monitor{
	{
		Name: "left", Primary: true, Scale: 1, CoordsPerLogical: 1,
		Bounds: geom.Rc(0, 0, 1920, 1080), WorkArea: geom.Rc(0, 0, 1920, 1040),
	},
	{
		Name: "right", Scale: 2, CoordsPerLogical: 2,
		Bounds: geom.Rc(1920, 0, 3840, 2160), WorkArea: geom.Rc(1920, 0, 3840, 2160),
	},
}

func TestFitPlacement(t *testing.T) {
	tests := []struct {
		name     string
		in       Placement
		monitors []Monitor
		frame    geom.Insets
		want     Placement
		ok       bool
	}{
		{
			name:     "saved at 100%, opened where the monitor is at 200%: twice the size, as far from the corner in logical pixels",
			in:       Placement{Bounds: geom.Rc(160, 90, 780, 660), CoordsPerLogical: 1},
			monitors: []Monitor{{Primary: true, Scale: 2, CoordsPerLogical: 2, Bounds: geom.Rc(0, 0, 4096, 2560), WorkArea: geom.Rc(0, 64, 4096, 2496)}},
			want:     Placement{Bounds: geom.Rc(320, 180, 1560, 1320), CoordsPerLogical: 2}, ok: true,
		},
		{
			name: "saved at 200%, opened at 100%: half the size",
			in:   Placement{Bounds: geom.Rc(320, 180, 1560, 1320), CoordsPerLogical: 2},
			want: Placement{Bounds: geom.Rc(160, 90, 780, 660), CoordsPerLogical: 1}, ok: true,
		},
		{
			name:     "saved at 100% on the right monitor, now at 200%: measured from that monitor's corner",
			in:       Placement{Bounds: geom.Rc(2020, 100, 800, 600), CoordsPerLogical: 1},
			monitors: twoMonitors[1:],
			want:     Placement{Bounds: geom.Rc(2120, 200, 1600, 1200), CoordsPerLogical: 2}, ok: true,
		},
		{
			name:     "grown past the far edge by the new scale, moved back onto the work area",
			in:       Placement{Bounds: geom.Rc(1000, 500, 800, 500), CoordsPerLogical: 1},
			monitors: []Monitor{{Primary: true, Scale: 1.5, CoordsPerLogical: 1.5, Bounds: geom.Rc(0, 0, 1920, 1080)}},
			want:     Placement{Bounds: geom.Rc(720, 330, 1200, 750), CoordsPerLogical: 1.5}, ok: true,
		},
		{
			name:     "one monitor, its coordinates halved with the scale: found where it now is, not centred",
			in:       Placement{Bounds: geom.Rc(2500, 1500, 800, 400), CoordsPerLogical: 2},
			monitors: []Monitor{{Primary: true, Scale: 1, Bounds: geom.Rc(0, 0, 1920, 1080)}},
			want:     Placement{Bounds: geom.Rc(1250, 750, 400, 200), CoordsPerLogical: 1}, ok: true,
		},
		{
			name: "two monitors, the screen's coordinates doubled: stays on the right monitor",
			in:   Placement{Bounds: geom.Rc(2020, 100, 800, 600), CoordsPerLogical: 1},
			monitors: []Monitor{{Primary: true, Scale: 2, CoordsPerLogical: 2, Bounds: geom.Rc(0, 0, 3840, 2160)},
				{Scale: 2, CoordsPerLogical: 2, Bounds: geom.Rc(3840, 0, 3840, 2160)}},
			want: Placement{Bounds: geom.Rc(4040, 200, 1600, 1200), CoordsPerLogical: 2}, ok: true,
		},
		{
			name: "two monitors, the screen's coordinates halved: stays on the left monitor",
			in:   Placement{Bounds: geom.Rc(3000, 100, 800, 600), CoordsPerLogical: 2},
			monitors: []Monitor{{Primary: true, Scale: 1, Bounds: geom.Rc(0, 0, 1920, 1080)},
				{Scale: 1, Bounds: geom.Rc(1920, 0, 1920, 1080)}},
			want: Placement{Bounds: geom.Rc(1500, 50, 400, 300), CoordsPerLogical: 1}, ok: true,
		},
		{
			name: "two monitors, the screen's coordinates halved: stays on the right monitor",
			in:   Placement{Bounds: geom.Rc(4000, 100, 800, 600), CoordsPerLogical: 2},
			monitors: []Monitor{{Primary: true, Scale: 1, Bounds: geom.Rc(0, 0, 1920, 1080)},
				{Scale: 1, Bounds: geom.Rc(1920, 0, 1920, 1080)}},
			want: Placement{Bounds: geom.Rc(2000, 50, 400, 300), CoordsPerLogical: 1}, ok: true,
		},
		{
			name:     "a scale no system reports, as in a damaged file, is ignored",
			in:       Placement{Bounds: geom.Rc(160, 90, 780, 660), CoordsPerLogical: 1e-30},
			monitors: []Monitor{{Primary: true, Scale: 2, CoordsPerLogical: 2, Bounds: geom.Rc(0, 0, 4096, 2560)}},
			want:     Placement{Bounds: geom.Rc(160, 90, 780, 660)}, ok: true,
		},
		{
			name: "saved at its own scale, kept as it was",
			in:   Placement{Bounds: geom.Rc(100, 100, 800, 600), CoordsPerLogical: 1},
			want: Placement{Bounds: geom.Rc(100, 100, 800, 600), CoordsPerLogical: 1}, ok: true,
		},
		{
			name:     "saved before the scale was kept: the bounds as they are",
			in:       Placement{Bounds: geom.Rc(160, 90, 780, 660)},
			monitors: []Monitor{{Primary: true, Scale: 2, CoordsPerLogical: 2, Bounds: geom.Rc(0, 0, 4096, 2560)}},
			want:     Placement{Bounds: geom.Rc(160, 90, 780, 660)}, ok: true,
		},
		{
			name: "kept where it was",
			in:   Placement{Bounds: geom.Rc(100, 100, 800, 600)},
			want: Placement{Bounds: geom.Rc(100, 100, 800, 600)}, ok: true,
		},
		{
			name: "kept on the second monitor, maximized",
			in:   Placement{Bounds: geom.Rc(2500, 300, 1600, 1200), Maximized: true},
			want: Placement{Bounds: geom.Rc(2500, 300, 1600, 1200), Maximized: true}, ok: true,
		},
		{
			name:     "kept with most of it off the right edge, the title bar still in reach",
			in:       Placement{Bounds: geom.Rc(1850, 100, 800, 600)},
			monitors: twoMonitors[:1],
			want:     Placement{Bounds: geom.Rc(1850, 100, 800, 600)}, ok: true,
		},
		{
			name:     "centred on the primary once its monitor has gone",
			in:       Placement{Bounds: geom.Rc(2500, 300, 800, 600), Maximized: true},
			monitors: twoMonitors[:1],
			want:     Placement{Bounds: geom.Rc(560, 220, 800, 600), Maximized: true}, ok: true,
		},
		{
			name: "centred when the title bar is under the task bar",
			in:   Placement{Bounds: geom.Rc(100, 1045, 800, 600)},
			want: Placement{Bounds: geom.Rc(560, 220, 800, 600)}, ok: true,
		},
		{
			name: "centred when the title bar is above the top of every screen",
			in:   Placement{Bounds: geom.Rc(100, -300, 800, 600)},
			want: Placement{Bounds: geom.Rc(560, 220, 800, 600)}, ok: true,
		},
		{
			name:     "centred when only a sliver of the title bar shows",
			in:       Placement{Bounds: geom.Rc(1900, 100, 800, 600)},
			monitors: twoMonitors[:1],
			want:     Placement{Bounds: geom.Rc(560, 220, 800, 600)}, ok: true,
		},
		{
			// 40 logical pixels are 80 device pixels at 200%, and only 60 show; on the first monitor 60 would do
			name:     "the grip is in logical pixels",
			in:       Placement{Bounds: geom.Rc(1000, 100, 980, 600)},
			monitors: twoMonitors[1:],
			want:     Placement{Bounds: geom.Rc(3350, 780, 980, 600)}, ok: true,
		},
		{
			name:     "shrunk to the work area it is on",
			in:       Placement{Bounds: geom.Rc(50, 20, 2500, 1500)},
			monitors: twoMonitors[:1],
			want:     Placement{Bounds: geom.Rc(0, 0, 1920, 1040)}, ok: true,
		},
		{
			name:     "shrunk only along the side that is too long",
			in:       Placement{Bounds: geom.Rc(50, 20, 800, 1500)},
			monitors: twoMonitors[:1],
			want:     Placement{Bounds: geom.Rc(50, 0, 800, 1040)}, ok: true,
		},
		{
			name: "shrunk and centred on the primary",
			in:   Placement{Bounds: geom.Rc(-5000, 0, 3000, 900)},
			want: Placement{Bounds: geom.Rc(0, 70, 1920, 900)}, ok: true,
		},
		{
			name:  "the system's title bar counts",
			in:    Placement{Bounds: geom.Rc(100, 5, 800, 600)},
			frame: geom.Insets{Top: 30, Left: 1, Right: 1, Bottom: 1},
			want:  Placement{Bounds: geom.Rc(560, 234, 800, 600)}, ok: true,
		},
		{
			name:     "the frame fits in the work area too",
			in:       Placement{Bounds: geom.Rc(10, 40, 1920, 1040)},
			monitors: twoMonitors[:1],
			frame:    geom.Insets{Top: 30, Left: 1, Right: 1, Bottom: 1},
			want:     Placement{Bounds: geom.Rc(1, 30, 1918, 1009)}, ok: true,
		},
		{
			name:     "a monitor with no work area uses its bounds",
			in:       Placement{Bounds: geom.Rc(100, 1045, 800, 600)},
			monitors: []Monitor{{Bounds: geom.Rc(0, 0, 1920, 1080)}},
			want:     Placement{Bounds: geom.Rc(100, 1045, 800, 600)}, ok: true,
		},
		{
			name: "no primary centres on the first",
			in:   Placement{Bounds: geom.Rc(-3000, 0, 800, 600)},
			monitors: []Monitor{
				{Bounds: geom.Rc(0, 0, 1000, 1000)},
				{Bounds: geom.Rc(1000, 0, 1000, 1000)},
			},
			want: Placement{Bounds: geom.Rc(100, 200, 800, 600)}, ok: true,
		},
		{
			name: "empty bounds",
			in:   Placement{Maximized: true},
			want: Placement{Maximized: true},
		},
		{
			name:     "no monitors",
			in:       Placement{Bounds: geom.Rc(1, 2, 3, 4)},
			monitors: []Monitor{},
			want:     Placement{Bounds: geom.Rc(1, 2, 3, 4)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := tt.monitors
			if ms == nil {
				ms = twoMonitors
			}
			got, ok := FitPlacement(tt.in, ms, tt.frame)
			if got != tt.want || ok != tt.ok {
				t.Errorf("FitPlacement(%v) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
