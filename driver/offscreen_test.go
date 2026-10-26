package driver

import (
	"image/color"
	"reflect"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// dot is a mask shape that fills its box.
type dot struct{}

func (dot) Coverage(w, h int) []byte {
	b := make([]byte, w*h)
	for i := range b {
		b[i] = 0xff
	}
	return b
}

func (dot) Settled() bool { return true }

// game records frames as a game does, from slices of its own that it
// fills again each frame.
type game struct {
	mesh   *paint.Mesh
	glyphs []paint.Glyph
	items  []paint.SceneItem
	cells  []paint.CellPaint
	ribbon []paint.RibbonPoint
}

func newGame(mesh *paint.Mesh) *game {
	return &game{mesh: mesh, glyphs: make([]paint.Glyph, 1), items: make([]paint.SceneItem, 1), cells: make([]paint.CellPaint, 1),
		ribbon: make([]paint.RibbonPoint, 2)}
}

// frame records frame n into p: a shape, text, a mask, a scene, a row
// of cells and a ribbon, each placed or coloured by n.
func (g *game) frame(p *paint.Painter, n int) {
	at := float32(n)
	g.glyphs[0] = paint.Glyph{ID: uint32(n), At: geom.Pt(at, 5)}
	g.items[0] = paint.SceneItem{Mesh: g.mesh, Model: geom.Move3(geom.V3(at, 0, 0))}
	g.cells[0] = paint.CellPaint{BG: color.NRGBA{R: uint8(n), A: 0xff}}
	for i := range g.ribbon {
		g.ribbon[i] = paint.RibbonPoint{At: geom.Pt(at+float32(i)*20, 180), Width: 4, Color: color.NRGBA{G: uint8(n), A: 0xff}}
	}
	p.Reset()
	p.RRect(geom.Rc(at, 0, 10, 10), 2, paint.Solid(color.NRGBA{R: uint8(n), A: 0xff}))
	p.Text(g.glyphs, 12, color.NRGBA{G: uint8(n), A: 0xff}, geom.Rc(at, 0, 20, 12))
	p.Mask(dot{}, geom.Rc(at, 20, 16, 16), color.NRGBA{B: uint8(n), A: 0xff})
	p.Scene(geom.Rc(0, 40, 100, 100), paint.Scene{Items: g.items})
	p.Cells(geom.Pt(0, 150), geom.Sz(8, 16), g.cells, nil, 1, n, 12, 12)
	p.Ribbon(g.ribbon)
}

func TestAnOffscreenWindowKeepsAFrameAsItWasPresented(t *testing.T) {
	w := Offscreen(geom.Sz(200, 200))
	mesh := paint.NewBox(geom.V3(1, 1, 1), color.NRGBA{A: 0xff})
	var p, alone paint.Painter
	g := newGame(mesh)
	g.frame(&p, 1)
	if err := w.Present(p.Ops(), paint.Everything); err != nil {
		t.Fatal(err)
	}
	kept := w.Ops()
	// The same frame, recorded by a painter that records nothing after.
	newGame(mesh).frame(&alone, 1)
	if !reflect.DeepEqual(kept, alone.Ops()) {
		t.Fatalf("the kept frame is not the frame presented:\n%#v\n%#v", kept, alone.Ops())
	}
	// Later frames reuse every buffer the first one was recorded into,
	// the painter's and the game's.
	for n := 2; n < 6; n++ {
		g.frame(&p, n)
		if err := w.Present(p.Ops(), paint.Everything); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(kept, alone.Ops()) {
			t.Fatalf("frame %d changed the first frame's ops, kept from Ops", n)
		}
	}
}

func TestAnOffscreenWindowKeepsAPictureOnItsClipboard(t *testing.T) {
	w := Offscreen(geom.Sz(10, 10))
	var ic ImageClipboard = w
	png := []byte("\x89PNG one")
	if err := ic.SetClipboardImage(png); err != nil {
		t.Fatal(err)
	}
	png[1] = 'X' // the window keeps its own copy
	if b, err := ic.ClipboardImage(); err != nil || string(b) != "\x89PNG one" {
		t.Fatalf("the clipboard reads %q, %v", b, err)
	}
	if err := w.SetClipboard("words"); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetClipboardImage(nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := ic.ClipboardImage(); b != nil {
		t.Fatalf("taken off, the clipboard reads %q", b)
	}
	if s, _ := w.Clipboard(); s != "words" {
		t.Fatalf("the text reads %q", s)
	}
}

// A popup attached to a window goes where a display puts it: below its
// anchor from the anchor's left edge, on the anchor's corner laid over
// it, or above it, all in the screen's space round the parent.
func TestAnAttachedPopupGoesWhereADisplayPutsIt(t *testing.T) {
	parent := Offscreen(geom.Sz(800, 600))
	parent.SetOrigin(geom.Pt(100, 50))
	anchor := geom.Rect{Min: geom.Pt(20, 30), Max: geom.Pt(80, 40)}
	for _, c := range []struct {
		name        string
		over, above bool
		want        geom.Point
	}{
		{"below", false, false, geom.Pt(120, 90)},
		{"over", true, false, geom.Pt(120, 80)},
		{"above", false, true, geom.Pt(120, 0)},
	} {
		pop := Offscreen(geom.Sz(200, 80))
		pop.Attach(parent, c.over, c.above)
		if err := pop.Place(anchor, geom.Sz(200, 80)); err != nil {
			t.Fatal(err)
		}
		if got := pop.ToScreen(geom.Point{}); got != c.want {
			t.Errorf("%s: the popup sits at %v, want %v", c.name, got, c.want)
		}
	}
	// One attached to nothing stays where it was.
	alone := Offscreen(geom.Sz(200, 80))
	_ = alone.Place(anchor, geom.Sz(200, 80))
	if got := alone.ToScreen(geom.Point{}); got != (geom.Point{}) {
		t.Errorf("a window attached to nothing moved to %v", got)
	}
}
