package gunim

import (
	"context"
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/theme"
)

// firstBackground is a window that notes the background of its first
// frame.
type firstBackground struct {
	*driver.OffscreenWindow
	got chan color.NRGBA
}

func (w *firstBackground) SetBackground(c color.NRGBA) {
	select {
	case w.got <- c:
	default:
	}
	w.OffscreenWindow.SetBackground(c)
}

type firstBackgroundDriver struct {
	pumpDriver
	w *firstBackground
}

func (d *firstBackgroundDriver) NewWindow(driver.Options) (driver.Window, error) {
	return d.w, nil
}

// A window opened in a theme draws its first frame in it, before the
// application has named one: a see-through window is never drawn dark
// for a frame first.
func TestAWindowOpensInItsTheme(t *testing.T) {
	d := &firstBackgroundDriver{w: &firstBackground{OffscreenWindow: driver.Offscreen(geom.Sz(80, 60)), got: make(chan color.NRGBA, 1)}}
	seeThrough := theme.Make("clear", theme.Set(WindowBackground, color.NRGBA{}))
	err := runApp(context.Background(), d, func(a *App) error {
		w, err := a.NewWindow(WindowOptions{Title: "clear", Theme: seeThrough})
		if err != nil {
			return err
		}
		defer w.Close()
		// Anything sent draws a frame.
		_ = w.Client().Focus("")
		select {
		case c := <-d.w.got:
			if c.A != 0 {
				t.Errorf("the first frame's background is %v, want the theme's, see-through", c)
			}
		case <-time.After(5 * time.Second):
			t.Error("no frame was drawn")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
