//go:build linux

package desktop

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/internal/icc"
	"github.com/marrasen/gunim/paint"
)

// TestAWindowConvertsToTheX11ScreensProfile sets a Display P3 profile on
// the root window, as colord would, and checks a window converts sRGB
// red to P3's red and leaves grey grey, then that it goes back to sRGB
// once the profile is gone. It leaves a display that has a profile of
// its own alone.
func TestAWindowConvertsToTheX11ScreensProfile(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	if waylandSession {
		t.Skip("a Wayland session")
	}
	var had []byte
	_ = display.call(func() error {
		had, _ = glfw.ICCProfile(0)
		return nil
	})
	if had != nil {
		t.Skip("the display has a profile of its own")
	}
	setProfile := func(b []byte) {
		_ = display.call(func() error { return glfw.SetICCProfile(0, b) })
	}
	defer setProfile(nil)

	dw, err := display.NewWindow(driver.Options{Title: "gunim colour test", Size: geom.Sz(200, 120)})
	if err != nil {
		t.Fatal(err)
	}
	w := dw.(*Window)
	defer func() { _ = w.Close() }()

	ops := []paint.Op{
		&paint.RRectOp{Rect: geom.Rc(0, 0, 200, 120), Fill: paint.Solid(color.NRGBA{A: 0xff}), Transform: paint.Identity},
		&paint.RRectOp{Rect: geom.Rc(10, 10, 80, 60), Fill: paint.Solid(color.NRGBA{R: 0xff, A: 0xff}), Transform: paint.Identity},
		&paint.RRectOp{Rect: geom.Rc(110, 10, 80, 60), Fill: paint.Solid(color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}), Transform: paint.Identity},
	}
	// shot draws a frame and returns its pixels as the window shows them.
	shot := func() *image.RGBA {
		got := make(chan *image.RGBA, 1)
		w.Shoot(func(m *image.RGBA) { got <- m })
		if err := w.Present(ops, paint.Everything); err != nil {
			t.Fatal(err)
		}
		<-w.Presented()
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no shot")
			return nil
		}
	}
	// waitFor waits for the window's colour state to become s.
	waitFor := func(s driver.ColorState) {
		deadline := time.Now().Add(5 * time.Second)
		for w.ColorProfile().State != s {
			if time.Now().After(deadline) {
				t.Fatalf("colour state is %v, want %v", w.ColorProfile(), s)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	at := func(m *image.RGBA, x, y int) [4]byte {
		s := w.Scale()
		return [4]byte(m.Pix[m.PixOffset(int(float32(x)*s), int(float32(y)*s)):][:4])
	}

	waitFor(driver.ColorNoProfile)
	if got := at(shot(), 50, 40); got != [4]byte{0xff, 0, 0, 0xff} {
		t.Fatalf("red with no profile is %v", got)
	}

	p3 := icc.DisplayP3()
	p3.Description = "gunim test P3"
	setProfile(icc.Write(p3))
	waitFor(driver.ColorConverted)
	if got := w.ColorProfile().Profile; got != "gunim test P3" {
		t.Errorf("profile is %q", got)
	}
	m := shot()
	if got := at(m, 50, 40); !near(got, [4]byte{234, 51, 35, 0xff}, 1) {
		t.Errorf("sRGB red on a Display P3 screen is %v, want 234,51,35", got)
	}
	if got := at(m, 150, 40); !near(got, [4]byte{0x80, 0x80, 0x80, 0xff}, 1) {
		t.Errorf("grey on a Display P3 screen is %v, want it grey", got)
	}

	setProfile(icc.Write(icc.SRGB()))
	waitFor(driver.ColorSRGB)

	setProfile(nil)
	waitFor(driver.ColorNoProfile)
	if got := at(shot(), 50, 40); got != [4]byte{0xff, 0, 0, 0xff} {
		t.Errorf("red once the profile is gone is %v", got)
	}
}

func TestNoColorManagementLeavesColoursAlone(t *testing.T) {
	if display == nil {
		t.Skip("no display")
	}
	dw, err := display.NewWindow(driver.Options{Title: "gunim colour off", Size: geom.Sz(100, 100), NoColorManagement: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dw.Close() }()
	if got := dw.(*Window).ColorProfile().State; got != driver.ColorOff {
		t.Errorf("state is %v, want off", got)
	}
}

func near(a, b [4]byte, tol int) bool {
	for i := range a {
		if d := int(a[i]) - int(b[i]); d > tol || d < -tol {
			return false
		}
	}
	return true
}
