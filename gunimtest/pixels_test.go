package gunimtest_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/widget"
)

// A label's text draws where the label stands, and the picture writes as a PNG.
func TestPixelsDrawsAWindowsText(t *testing.T) {
	w := gunimtest.NewWith(t, gunim.WindowOptions{Size: geom.Sz(240, 60), Root: widget.NewLabel("Hej, pixels!")})
	w.Frame(time.Second / 60)
	img := gunimtest.Pixels(t, w)
	bg := img.RGBAAt(img.Bounds().Max.X-1, img.Bounds().Max.Y-1)
	// The label stands at the window's top left, so its ink is all in the top half.
	inked, low := 0, 0
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			if img.RGBAAt(x, y) != bg {
				inked++
				if y >= img.Bounds().Dy()/2 {
					low++
				}
			}
		}
	}
	if inked < 50 || low > 0 {
		t.Fatalf("the label inks %d pixels, %d of them in the bottom half", inked, low)
	}
	path := filepath.Join(t.TempDir(), "label.png")
	if dir := os.Getenv("GUNIM_PIXELS_DIR"); dir != "" {
		path = filepath.Join(dir, "label.png")
	}
	gunimtest.WritePNG(t, path, img)
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("the PNG is not written: %v", err)
	}
}
