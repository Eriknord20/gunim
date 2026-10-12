//go:build linux || windows || darwin

package desktop

import (
	"context"
	"image"
	"image/color"
	"os"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/paint"
)

// display is the driver the tests share, or nil when no display is
// attached. GLFW has to run on the main thread, so TestMain keeps the
// main thread for the event loop and runs the tests beside it.
var display *Driver

func TestMain(m *testing.M) {
	d, err := Open()
	if err != nil {
		os.Exit(m.Run())
	}
	display = d
	d.stayOpen = true
	ctx, cancel := context.WithCancel(context.Background())
	code := make(chan int, 1)
	_ = d.Run(ctx, func() {
		go func() {
			// A machine with a display but no OpenGL, as a CI runner on
			// Windows is, opens no window at all: its tests skip, as
			// they do with no display.
			if w, err := d.NewWindow(driver.Options{Title: "gunim probe", Size: geom.Sz(1, 1)}); err != nil {
				display = nil
			} else {
				_ = w.Close()
			}
			code <- m.Run()
			cancel()
		}()
	})
	os.Exit(<-code)
}

func TestWindowPresentsAndReportsTheFrame(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	w, err := display.NewWindow(driver.Options{Title: "gunim test", Size: geom.Sz(200, 120)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	}()

	if s := w.Size(); s.W <= 0 || s.H <= 0 {
		t.Fatalf("Size = %v, want the window's size", s)
	}
	ops := []paint.Op{&paint.RRectOp{
		Rect:      geom.Rc(10, 10, 100, 60),
		Radius:    8,
		Fill:      paint.Solid(color.NRGBA{R: 0xff, A: 0xff}),
		Transform: paint.Identity,
	}}
	for i := range 3 {
		if err := w.Present(ops, geom.Rect{}); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		select {
		case f := <-w.Presented():
			if f.Shown.IsZero() {
				t.Fatalf("frame %d reported with no time", i)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("frame %d was never reported shown", i)
		}
	}
}

func TestPaceHoldsAnUnpacedSwapToTheRefreshRate(t *testing.T) {
	const rate = 50
	interval := time.Second / rate
	last := time.Now()
	shown := pace(last, rate)
	if want := last.Add(interval); !shown.Equal(want) {
		t.Fatalf("shown %v after the last frame, want %v", shown.Sub(last), interval)
	}
	if time.Now().Before(shown) {
		t.Fatal("pace returned before the refresh it reported")
	}
}

func TestPaceLeavesAPacedSwapAlone(t *testing.T) {
	last := time.Now().Add(-time.Second / 50)
	before := time.Now()
	shown := pace(last, 50)
	if shown.Before(before) || time.Since(before) > 5*time.Millisecond {
		t.Fatalf("pace slept after a swap that had already waited a refresh")
	}
}

// TestBackdropFramesStayUpright draws a frame that is white on top and
// black below, under a full-window backdrop layer followed by an
// unclipped layer, the way the confirm dialog paints. Under one software
// driver, the copy of such a frame to the window came out upside down
// when its shader read gl_FragCoord.
func TestBackdropFramesStayUpright(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	dw, err := display.NewWindow(driver.Options{Title: "gunim backdrop", Size: geom.Sz(160, 120)})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := dw.(*Window)
	if !ok {
		t.Fatalf("NewWindow returned a %T", dw)
	}
	defer func() { _ = w.Close() }()

	type frame struct {
		pix  []byte
		w, h int
	}
	got := make(chan frame, 1)
	w.mu.Lock()
	w.readback = func(pix []byte, fw, fh int) {
		select {
		case got <- frame{pix, fw, fh}:
		default:
		}
	}
	w.mu.Unlock()

	size := w.Size()
	full := geom.Rect{Max: size.Point()}
	top := geom.Rect{Max: geom.Pt(size.W, size.H/2)}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	black := color.NRGBA{A: 0xff}

	// 4, 12 and 30 pixels run the blur at full, half and quarter
	// resolution.
	for _, sigma := range []float32{4, 12, 30} {
		ops := []paint.Op{
			&paint.RRectOp{Rect: full, Fill: paint.Solid(black), Transform: paint.Identity},
			&paint.RRectOp{Rect: top, Fill: paint.Solid(white), Transform: paint.Identity},
			&paint.LayerOp{Opts: paint.LayerOpts{Bounds: full, Opacity: 1, Backdrop: sigma}, Transform: paint.Identity},
			&paint.LayerEndOp{},
			&paint.LayerOp{Opts: paint.LayerOpts{Bounds: full, Opacity: 1}, Transform: paint.Identity},
			&paint.LayerEndOp{},
		}
		// Several frames, since the flip came and went from frame to
		// frame.
		for i := range 4 {
			if err := w.Present(ops, geom.Rect{}); err != nil {
				t.Fatal(err)
			}
			var f frame
			select {
			case f = <-got:
			case <-time.After(5 * time.Second):
				t.Fatal("no frame read back")
			}
			<-w.Presented()
			// Rows arrive bottom up: the first row is the window's bottom.
			bottom := f.pix[(f.w/2)*4]
			topRow := f.pix[((f.h-1)*f.w+f.w/2)*4]
			if topRow < 200 || bottom > 55 {
				t.Fatalf("sigma %v, frame %d: top %d and bottom %d, want white on top of black", sigma, i, topRow, bottom)
			}
		}
	}
}

// TestEachWindowTakesItsMonitorsRate needs two monitors with different
// refresh rates; tools/multimon/start.sh makes a virtual pair.
func TestEachWindowTakesItsMonitorsRate(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	ms := display.Monitors()
	if len(ms) < 2 || ms[0].RefreshRate == ms[1].RefreshRate {
		t.Skip("needs two monitors at different refresh rates; see tools/multimon")
	}
	windows := make([]driver.Window, 2)
	for i := range windows {
		w, err := display.NewWindow(driver.Options{Title: "gunim rate", Size: geom.Sz(200, 120), Monitor: &ms[i]})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close() })
		windows[i] = w
		if got := w.RefreshRate(); got != ms[i].RefreshRate {
			t.Fatalf("window on %s reports %v Hz, want its monitor's %v", ms[i].Name, got, ms[i].RefreshRate)
		}
	}

	// Present as fast as each window takes frames, both at once, for
	// a second, and count.
	counts := make([]int, len(windows))
	done := make(chan struct{})
	for i, w := range windows {
		go func() {
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if err := w.Present(nil, geom.Rect{}); err != nil {
					break
				}
				<-w.Presented()
				counts[i]++
			}
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	for i, n := range counts {
		// Software GL on a virtual display falls a little short of a
		// fast rate, so allow some room below.
		rate := ms[i].RefreshRate
		if f := float64(n); f < 0.8*rate || f > 1.1*rate {
			t.Errorf("window on %s at %v Hz drew %d frames in a second", ms[i].Name, rate, n)
		}
	}
}

// A shot is the frame the right way up: white on top of black.
func TestAShotIsTheFrameTheRightWayUp(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	dw, err := display.NewWindow(driver.Options{Title: "gunim shot", Size: geom.Sz(160, 120)})
	if err != nil {
		t.Fatal(err)
	}
	w, ok := dw.(*Window)
	if !ok {
		t.Fatalf("NewWindow returned a %T", dw)
	}
	defer func() { _ = w.Close() }()
	got := make(chan *image.RGBA, 1)
	w.Shoot(func(img *image.RGBA) { got <- img })
	size := w.Size()
	ops := []paint.Op{
		&paint.RRectOp{Rect: geom.Rect{Max: size.Point()}, Fill: paint.Solid(color.NRGBA{A: 0xff}), Transform: paint.Identity},
		&paint.RRectOp{Rect: geom.Rect{Max: geom.Pt(size.W, size.H/2)}, Fill: paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}), Transform: paint.Identity},
	}
	if err := w.Present(ops, geom.Rect{}); err != nil {
		t.Fatal(err)
	}
	var img *image.RGBA
	select {
	case img = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no shot")
	}
	<-w.Presented()
	b := img.Bounds()
	top, bottom := img.RGBAAt(b.Dx()/2, 2), img.RGBAAt(b.Dx()/2, b.Dy()-3)
	if top.R < 200 || bottom.R > 55 {
		t.Fatalf("top %v and bottom %v, want white on top of black", top, bottom)
	}
}

// A fixed window is not resizable, so the system neither sizes,
// maximizes nor snaps it, and one without the option is.
func TestAFixedWindowIsNotResizable(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	for _, fixed := range []bool{false, true} {
		dw, err := display.NewWindow(driver.Options{Title: "gunim fixed", Size: geom.Sz(200, 120), Fixed: fixed})
		if err != nil {
			t.Fatal(err)
		}
		w, ok := dw.(*Window)
		if !ok {
			t.Fatalf("NewWindow made a %T", dw)
		}
		var resizable int
		if err := display.call(func() error {
			var err error
			resizable, err = w.gw.GetAttrib(glfw.Resizable)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		want := glfw.True
		if fixed {
			want = glfw.False
		}
		if resizable != want {
			t.Errorf("with Fixed %v the window has Resizable %d, want %d", fixed, resizable, want)
		}
		if err := dw.Close(); err != nil {
			t.Error(err)
		}
	}
}
