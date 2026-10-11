package filemanager

import (
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// fakeIcons has the window show icons of the system's, each a square of
// one colour, as Windows' would.
func fakeIcons(t *testing.T) {
	reads := 0
	wasHere, wasOf := iconsHere, systemIconOf
	iconsHere = true
	systemIconOf = func(path, ext string, dir bool) (image.Image, image.Image, error) {
		reads++
		square := func(n int) image.Image {
			img := image.NewNRGBA(image.Rect(0, 0, n, n))
			for i := range img.Pix {
				img.Pix[i] = 0xff
			}
			img.SetNRGBA(0, 0, color.NRGBA{R: uint8(len(ext)), A: 0xff})
			return img
		}
		return square(64), square(32), nil
	}
	t.Cleanup(func() { iconsHere, systemIconOf = wasHere, wasOf })
}

// The window shows the icon the system does for each kind of item, read
// once for every window, in the details and on the tiles, and the View
// menu turns them off for the window's own, and on again.
func TestSystemIconsShowAndTurnOff(t *testing.T) {
	fakeIcons(t)
	h := newHarness(t, "a.txt", "b.txt", "c.md", "sub/x")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 4 })
	keys := make([]string, 0, len(h.a.nav.rows))
	for _, e := range h.a.nav.rows {
		keys = append(keys, e.iconKey)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"dir", "ext:.md", "ext:.txt", "ext:.txt"}) {
		t.Fatalf("the icon keys are %q", keys)
	}
	h.until("the icons arrive", func() bool {
		return h.b.icons["dir"].Small != nil && h.b.icons["ext:.txt"].Large != nil && h.b.icons["ext:.md"].Small != nil
	})
	pg := h.b.listing.cur
	for i := range len(h.a.nav.rows) {
		r, ok := pg.row(i)
		if !ok || r.Cells[0][0].Image == nil {
			t.Fatalf("row %d draws no icon: %+v", i, r.Cells[0])
		}
	}
	if !h.b.shell.SystemIcons {
		t.Fatal("the menu does not tick Windows icons")
	}

	h.do(Command{Name: CmdSystemIcons})
	h.until("the window's own icons", func() bool {
		r, ok := h.b.listing.cur.row(0)
		return ok && r.Cells[0][0].Image == nil && !h.b.shell.SystemIcons
	})
	if h.a.prefs.SystemIcons == nil || *h.a.prefs.SystemIcons {
		t.Fatal("turning them off was not kept")
	}
	h.do(Command{Name: CmdSystemIcons})
	h.until("Windows' icons again", func() bool {
		r, ok := h.b.listing.cur.row(0)
		return ok && r.Cells[0][0].Image != nil
	})
}

// A system's small icon is drawn no larger than its pixels.
func TestSystemIconRect(t *testing.T) {
	img := paintSquare(48)
	if r := systemIconRect(img, sz(200, 160)); r.Size().W != 48 || r.Min.X != 76 {
		t.Fatalf("a 48 pixel icon in a 200 by 160 box is at %v", r)
	}
	if r := systemIconRect(paintSquare(256), sz(100, 120)); r.Size().W != 100 {
		t.Fatalf("a 256 pixel icon in a 100 by 120 box is at %v", r)
	}
}

func paintSquare(n int) *paint.Image { return paint.NewImage(image.NewNRGBA(image.Rect(0, 0, n, n))) }

func sz(w, h float32) geom.Size { return geom.Sz(w, h) }
