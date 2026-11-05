package compose

import (
	"image"
	"image/color"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/paint"
)

// fakeGL records what the compositor asks of GL, with no GL. Calls the
// compositor never makes reach the nil Context and panic.
type fakeGL struct {
	gl.Context
	names   uint32
	fbo     uint32
	viewW   int32
	rects   [][4]float32
	laid    []uint32
	texture uint32
	sized   map[uint32][2]int32
	read    []uint32
	deleted []uint32
}

func (g *fakeGL) name() uint32                                                { g.names++; return g.names }
func (g *fakeGL) CreateShader(uint32) uint32                                  { return g.name() }
func (g *fakeGL) ShaderSource(uint32, string)                                 {}
func (g *fakeGL) CompileShader(uint32)                                        {}
func (g *fakeGL) GetShaderi(uint32, uint32) int                               { return gl.TRUE }
func (g *fakeGL) CreateProgram() uint32                                       { return g.name() }
func (g *fakeGL) AttachShader(uint32, uint32)                                 {}
func (g *fakeGL) BindAttribLocation(uint32, uint32, string)                   {}
func (g *fakeGL) LinkProgram(uint32)                                          {}
func (g *fakeGL) DeleteShader(uint32)                                         {}
func (g *fakeGL) GetProgrami(uint32, uint32) int                              { return gl.TRUE }
func (g *fakeGL) GetUniformLocation(uint32, string) int32                     { return 1 }
func (g *fakeGL) UseProgram(uint32)                                           {}
func (g *fakeGL) Uniform1i(int32, int32)                                      {}
func (g *fakeGL) CreateVertexArray() uint32                                   { return g.name() }
func (g *fakeGL) BindVertexArray(uint32)                                      {}
func (g *fakeGL) CreateBuffer() uint32                                        { return g.name() }
func (g *fakeGL) BindBuffer(uint32, uint32)                                   {}
func (g *fakeGL) BufferData(uint32, []byte, uint32)                           {}
func (g *fakeGL) EnableVertexAttribArray(uint32)                              {}
func (g *fakeGL) VertexAttribPointer(uint32, int32, uint32, bool, int32, int) {}
func (g *fakeGL) CreateTexture() uint32                                       { return g.name() }
func (g *fakeGL) CreateFramebuffer() uint32                                   { return g.name() }
func (g *fakeGL) BindTexture(_, t uint32)                                     { g.texture = t }
func (g *fakeGL) TexParameteri(uint32, uint32, int32)                         {}
func (g *fakeGL) FramebufferTexture2D(uint32, uint32, uint32, uint32, int32)  {}
func (g *fakeGL) DeleteTexture(t uint32)                                      { g.deleted = append(g.deleted, t) }
func (g *fakeGL) DeleteFramebuffer(f uint32)                                  { g.deleted = append(g.deleted, f) }
func (g *fakeGL) BindFramebuffer(_, f uint32)                                 { g.fbo = f }
func (g *fakeGL) Viewport(_, _, w, _ int32)                                   { g.viewW = w }
func (g *fakeGL) Disable(uint32)                                              {}
func (g *fakeGL) Enable(uint32)                                               {}
func (g *fakeGL) ClearColor(_, _, _, _ float32)                               {}
func (g *fakeGL) Clear(uint32)                                                {}
func (g *fakeGL) BlendFuncSeparate(_, _, _, _ uint32)                         {}
func (g *fakeGL) ActiveTexture(uint32)                                        {}
func (g *fakeGL) Uniform4fv(_ int32, v []float32)                             { g.rects = append(g.rects, [4]float32(v)) }
func (g *fakeGL) DrawElements(uint32, int32, uint32, int)                     { g.laid = append(g.laid, g.texture) }
func (g *fakeGL) ReadPixels([]byte, int32, int32, int32, int32, uint32, uint32) {
	g.read = append(g.read, g.fbo)
}
func (g *fakeGL) TexImage2D(_ uint32, _, _, w, h int32, _, _ uint32, _ []byte) {
	if g.sized == nil {
		g.sized = map[uint32][2]int32{}
	}
	g.sized[g.texture] = [2]int32{w, h}
}

// drawn is one frame a fakeDrawer was asked to draw.
type drawn struct {
	fbo   uint32
	whole bool
	w, h  int
}

// fakeDrawer stands in for a renderer.
type fakeDrawer struct {
	drew    []drawn
	sharpAt time.Time
	rebinds *int
	gone    int
}

func (d *fakeDrawer) drawRects(fbo uint32, _ color.NRGBA, _ []paint.Op, _ []geom.Rect, w, h int, _ float32) time.Time {
	d.drew = append(d.drew, drawn{fbo: fbo, w: w, h: h})
	return d.sharpAt
}

func (d *fakeDrawer) draw(fbo uint32, _ []paint.Op, w, h int, _ float32) time.Time {
	d.drew = append(d.drew, drawn{fbo: fbo, whole: true, w: w, h: h})
	return d.sharpAt
}

func (d *fakeDrawer) rebind() { *d.rebinds++ }

func (d *fakeDrawer) release() { d.gone++ }

// bench is a compositor on a fake GL, with windows of fake renderers.
type bench struct {
	*testing.T
	g       *fakeGL
	c       *Compositor
	rebinds int
}

func newBench(t *testing.T) *bench {
	t.Helper()
	g := &fakeGL{}
	c, err := New(g, "test")
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{T: t, g: g, c: c}
	c.open = func(*Pane) (drawer, error) { return &fakeDrawer{rebinds: &b.rebinds}, nil }
	return b
}

func newPane() *Pane { return &Pane{Sharp: func(time.Time) {}} }

// took returns what p was asked to draw since the last call, and forgets it.
func took(p *Pane) []drawn {
	d := p.drawer.(*fakeDrawer)
	got := d.drew
	d.drew = nil
	return got
}

var someOps = []paint.Op{&paint.RRectOp{}}

// frame runs one frame: draws, each of which is a frame waiting for the
// layer at that index, over stack on a surface of w×h.
func (b *bench) frame(stack []Layer, w, h int, draws ...*Layer) {
	b.Helper()
	b.c.Begin(stack, w, h)
	for _, l := range draws {
		if !b.c.Draw(l.Pane, someOps, nil, l.W, l.H, color.NRGBA{}, 1) {
			b.Fatalf("a frame of %dx%d did not draw", l.W, l.H)
		}
	}
	b.c.Finish(stack, w, h, 1)
}

func TestAloneOnTheSurfaceDrawsStraightOntoIt(t *testing.T) {
	b := newBench(t)
	p := newPane()
	l := Layer{Pane: p, W: 100, H: 80}
	b.frame([]Layer{l}, 100, 80, &l)
	if got, want := took(p), []drawn{{fbo: 0, w: 100, h: 80}}; !slices.Equal(got, want) {
		t.Fatalf("drew %v, want %v", got, want)
	}
	if p.OnTex || len(b.g.laid) != 0 {
		t.Fatalf("on a texture %v, %d textures laid", p.OnTex, len(b.g.laid))
	}
}

func TestSmallerThanTheSurfaceGoesThroughItsTexture(t *testing.T) {
	b := newBench(t)
	p := newPane()
	l := Layer{Pane: p, X: 10, Y: 20, W: 50, H: 40}
	b.frame([]Layer{l}, 100, 80, &l)
	if got, want := took(p), []drawn{{fbo: p.fbo, w: 50, h: 40}}; !slices.Equal(got, want) {
		t.Fatalf("drew %v, want %v", got, want)
	}
	if !p.OnTex || !slices.Equal(b.g.laid, []uint32{p.tex}) {
		t.Fatalf("on a texture %v, laid %v, want %d", p.OnTex, b.g.laid, p.tex)
	}
	// x runs -1 to 1 from the left and y 1 to -1 from the top.
	want := [4]float32{-0.8, -0.5, 0.2, 0.5}
	if len(b.g.rects) != 1 {
		t.Fatalf("laid %d rects", len(b.g.rects))
	}
	for i, v := range b.g.rects[0] {
		if math.Abs(float64(v-want[i])) > 1e-5 {
			t.Fatalf("laid at %v, want %v", b.g.rects[0], want)
		}
	}
}

// Alone and at the surface's corner is short of drawing straight onto it: a
// smaller window has the rest to clear, and one slid up from over the
// keyboard is off to one side.
func TestAloneButNotFillingGoesThroughItsTexture(t *testing.T) {
	for _, l := range []Layer{{W: 50, H: 40}, {W: 100, H: 40}, {W: 50, H: 80}, {Y: -10, W: 100, H: 80}, {X: 1, W: 100, H: 80}} {
		b := newBench(t)
		l.Pane = newPane()
		b.frame([]Layer{l}, 100, 80, &l)
		if got, want := took(l.Pane), []drawn{{fbo: l.Pane.fbo, w: l.W, h: l.H}}; !slices.Equal(got, want) {
			t.Errorf("at %d,%d, %dx%d, it drew %v, want %v", l.X, l.Y, l.W, l.H, got, want)
		}
		if !slices.Equal(b.g.laid, []uint32{l.Pane.tex}) {
			t.Errorf("at %d,%d, %dx%d, it laid %v", l.X, l.Y, l.W, l.H, b.g.laid)
		}
	}
}

func TestPopupOpeningPutsTheWindowUnderItOnItsTexture(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb := Layer{Pane: base, W: 100, H: 80}
	b.frame([]Layer{lb}, 100, 80, &lb)
	took(base)
	lp := Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	stack := []Layer{lb, lp}
	b.frame(stack, 100, 80, &lp)
	// The window under it last drew on the surface, so it draws its last frame again.
	if got, want := took(base), []drawn{{fbo: base.fbo, whole: true, w: 100, h: 80}}; !slices.Equal(got, want) {
		t.Fatalf("under the popup it drew %v, want %v", got, want)
	}
	if !slices.Equal(b.g.laid, []uint32{base.tex, popup.tex}) {
		t.Fatalf("laid %v, want the window %d then the popup %d", b.g.laid, base.tex, popup.tex)
	}
	// Laid once more with nothing new, nothing draws again.
	b.g.laid = nil
	b.frame(stack, 100, 80)
	if got := took(base); len(got) != 0 {
		t.Fatalf("it drew %v with nothing new", got)
	}
	if len(b.g.laid) != 2 {
		t.Fatalf("laid %v, want two", b.g.laid)
	}
}

func TestPopupClosingPutsTheWindowBackOnTheSurface(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	took(base)
	b.frame([]Layer{lb}, 100, 80)
	if got, want := took(base), []drawn{{fbo: 0, whole: true, w: 100, h: 80}}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
	if base.OnTex {
		t.Fatal("it says its texture holds what it drew on the surface")
	}
}

// A surface that changed size and has no frame for it yet shows the last
// frame, drawn to fill it.
func TestNewSizeFillsAtOnceWithTheLastFrame(t *testing.T) {
	b := newBench(t)
	p := newPane()
	l := Layer{Pane: p, W: 100, H: 80}
	b.frame([]Layer{l}, 100, 80, &l)
	took(p)
	l.W, l.H = 80, 100
	stack := []Layer{l}
	if !b.c.Dirty(stack) {
		t.Fatal("a new size is not dirty")
	}
	b.frame(stack, 80, 100)
	if got, want := took(p), []drawn{{fbo: 0, whole: true, w: 80, h: 100}}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
	if p.LastW != 80 || p.LastH != 100 {
		t.Fatalf("the last size is %dx%d", p.LastW, p.LastH)
	}
}

func TestNewSizeFillsAtOnceUnderAPopup(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	took(base)
	took(popup)
	lb.W, lb.H = 80, 100
	b.frame([]Layer{lb, lp}, 80, 100)
	if got, want := took(base), []drawn{{fbo: base.fbo, whole: true, w: 80, h: 100}}; !slices.Equal(got, want) {
		t.Fatalf("under the popup it drew %v, want %v", got, want)
	}
	if got := b.g.sized[base.tex]; got != [2]int32{80, 100} {
		t.Fatalf("its texture is %v", got)
	}
	if got := took(popup); len(got) != 0 {
		t.Fatalf("the popup, the same size, drew %v", got)
	}
	// The next frame finds nothing behind.
	b.frame([]Layer{lb, lp}, 80, 100)
	if got := took(base); len(got) != 0 {
		t.Fatalf("it drew %v again", got)
	}
}

func TestAFrameAtTheNewSizeDrawsOnce(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	took(base)
	lb.W, lb.H = 80, 100
	b.frame([]Layer{lb, lp}, 80, 100, &lb)
	if got, want := took(base), []drawn{{fbo: base.fbo, w: 80, h: 100}}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
}

func TestAWindowWithNoFrameYetIsLeftOut(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb)
	if !slices.Equal(b.g.laid, []uint32{base.tex}) {
		t.Fatalf("laid %v, want only the window %d", b.g.laid, base.tex)
	}
	if popup.LastW != 0 || popup.Last != nil {
		t.Fatalf("the popup's last frame is %v at %dx%d", popup.Last, popup.LastW, popup.LastH)
	}
}

func TestANoSizeFrameDrawsNothing(t *testing.T) {
	b := newBench(t)
	p := newPane()
	b.c.Begin(nil, 100, 80)
	if b.c.Draw(p, someOps, nil, 0, 80, color.NRGBA{}, 1) || b.c.Draw(p, someOps, nil, 80, -1, color.NRGBA{}, 1) {
		t.Fatal("a frame with no size drew")
	}
	if p.drawer != nil || p.Last != nil {
		t.Fatal("a frame with no size left its mark")
	}
}

func TestSharpHearsOfEveryDraw(t *testing.T) {
	b := newBench(t)
	var heard []time.Time
	p := &Pane{Sharp: func(at time.Time) { heard = append(heard, at) }}
	at := time.Unix(100, 0)
	l := Layer{Pane: p, W: 100, H: 80}
	b.c.open = func(*Pane) (drawer, error) { return &fakeDrawer{rebinds: &b.rebinds, sharpAt: at}, nil }
	b.frame([]Layer{l}, 100, 80, &l)
	// A draw that was not the engine's frame can stretch a mask too.
	other := newPane()
	lo := Layer{Pane: other, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{l, lo}, 100, 80)
	if !slices.Equal(heard, []time.Time{at, at}) {
		t.Fatalf("heard %v", heard)
	}
}

func TestRebindsOnceAFrame(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	if b.rebinds != 1 {
		t.Fatalf("%d rebinds", b.rebinds)
	}
}

func TestShootReadsWhereTheFrameWent(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	stack := []Layer{lb}
	got := make(chan *image.RGBA, 2)
	shoot := func(m *image.RGBA) { got <- m }
	b.frame(stack, 100, 80, &lb)
	b.c.Shoot(base, 100, 80, shoot)
	if m := <-got; m.Bounds() != image.Rect(0, 0, 100, 80) {
		t.Fatalf("the shot is %v", m.Bounds())
	}
	stack = []Layer{lb, lp}
	b.frame(stack, 100, 80, &lb)
	b.c.Shoot(base, 100, 80, shoot)
	<-got
	if !slices.Equal(b.g.read, []uint32{0, base.fbo}) {
		t.Fatalf("read %v, want the surface and then %d", b.g.read, base.fbo)
	}
}

// A popup that closed frees its renderer, texture and framebuffer, each
// once, and the window under it goes on with its own.
func TestReleaseFreesWhatTheWindowHolds(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	d := popup.drawer.(*fakeDrawer)
	tex, fbo := popup.tex, popup.fbo
	b.c.Release(popup)
	if d.gone != 1 {
		t.Fatalf("the renderer was let go %d times", d.gone)
	}
	if !slices.Equal(b.g.deleted, []uint32{fbo, tex}) {
		t.Fatalf("deleted %v, want the framebuffer %d and the texture %d", b.g.deleted, fbo, tex)
	}
	if popup.R != nil || popup.drawer != nil || popup.tex != 0 || popup.fbo != 0 || popup.Last != nil {
		t.Fatalf("after Release: %+v", popup)
	}
	// Again, it has nothing left to free.
	b.c.Release(popup)
	if d.gone != 1 || len(b.g.deleted) != 2 {
		t.Fatalf("a second Release freed more: %d, %v", d.gone, b.g.deleted)
	}
	b.g.laid = nil
	took(base)
	b.frame([]Layer{lb}, 100, 80, &lb)
	if len(b.g.laid) != 0 || base.drawer.(*fakeDrawer).gone != 0 {
		t.Fatalf("the window left laid %v, and was let go %d times", b.g.laid, base.drawer.(*fakeDrawer).gone)
	}
}

// A window can close with a frame waiting that never drew, or before it ever
// drew at all.
func TestReleaseOfAWindowThatNeverDrew(t *testing.T) {
	b := newBench(t)
	p := newPane()
	b.c.Release(p)
	if len(b.g.deleted) != 0 {
		t.Fatalf("deleted %v", b.g.deleted)
	}
}

// Alone on the surface, a window has no use for a texture, and a popup
// opening later has one made.
func TestAloneOnTheSurfaceMakesNoTexture(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb := Layer{Pane: base, W: 100, H: 80}
	for _, sz := range [][2]int{{100, 80}, {120, 90}, {80, 100}} {
		lb.W, lb.H = sz[0], sz[1]
		b.frame([]Layer{lb}, lb.W, lb.H, &lb)
		b.frame([]Layer{{Pane: base, W: lb.W, H: lb.H}}, lb.W, lb.H)
	}
	if base.tex != 0 || base.fbo != 0 || len(b.g.sized) != 0 {
		t.Fatalf("alone, it made a texture: %d, sized %v", base.tex, b.g.sized)
	}
	took(base)
	lp := Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, lb.W, lb.H, &lp)
	if base.tex == 0 || b.g.sized[base.tex] != [2]int32{int32(lb.W), int32(lb.H)} {
		t.Fatalf("with a popup open its texture is %d, %v", base.tex, b.g.sized[base.tex])
	}
	if got, want := took(base), []drawn{{fbo: base.fbo, whole: true, w: lb.W, h: lb.H}}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
	if !slices.Equal(b.g.laid, []uint32{base.tex, popup.tex}) {
		t.Fatalf("laid %v", b.g.laid)
	}
}

// A popup that opens in the same frame as a new frame of the window under it
// has the texture made for that frame.
func TestPopupOpeningWithAFrameOfTheWindow(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb}, 100, 80, &lb)
	took(base)
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	if got, want := took(base), []drawn{{fbo: base.fbo, w: 100, h: 80}}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
	if base.tex == 0 || !slices.Equal(b.g.laid, []uint32{base.tex, popup.tex}) {
		t.Fatalf("laid %v", b.g.laid)
	}
}

// A texture left from a popup stays for the next popup at the same size, and
// goes at any other.
func TestAloneAgainKeepsATextureOfTheRightSize(t *testing.T) {
	b := newBench(t)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
	tex, fbo := base.tex, base.fbo
	b.frame([]Layer{lb}, 100, 80)
	b.frame([]Layer{lb}, 100, 80, &lb)
	if base.tex != tex || len(b.g.deleted) != 0 {
		t.Fatalf("at the same size the texture is %d, was %d, deleted %v", base.tex, tex, b.g.deleted)
	}
	lb.W, lb.H = 120, 90
	b.frame([]Layer{lb}, 120, 90, &lb)
	if base.tex != 0 || !slices.Equal(b.g.deleted, []uint32{fbo, tex}) {
		t.Fatalf("at a new size the texture is %d, deleted %v", base.tex, b.g.deleted)
	}
	// The same goes for the last frame drawn again at the new size, with no
	// frame of its own.
	b.frame([]Layer{lb, lp}, 120, 90)
	if base.tex == 0 {
		t.Fatal("a popup opened and the window under it has no texture")
	}
	b.g.deleted = nil
	lb.W, lb.H = 100, 80
	b.frame([]Layer{lb}, 100, 80)
	if base.tex != 0 || len(b.g.deleted) != 2 {
		t.Fatalf("at a new size with no frame the texture is %d, deleted %v", base.tex, b.g.deleted)
	}
}

func TestForgetKeepsTheLastFrame(t *testing.T) {
	b := newBench(t)
	p := newPane()
	l := Layer{Pane: p, W: 100, H: 80}
	o := Layer{Pane: newPane(), X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{l, o}, 100, 80, &l)
	was := p.tex
	p.Forget()
	if p.tex != 0 || p.fbo != 0 || p.texW != 0 || p.drawer != nil || p.Last == nil || p.LastW != 100 {
		t.Fatalf("after Forget: %+v", p)
	}
	b.frame([]Layer{l, o}, 100, 80, &l)
	if p.tex == 0 || p.tex == was {
		t.Fatalf("the new context's texture is %d, the old one was %d", p.tex, was)
	}
}

func TestDirty(t *testing.T) {
	b := newBench(t)
	p, q := newPane(), newPane()
	l := Layer{Pane: p, X: 1, Y: 2, W: 100, H: 80}
	m := Layer{Pane: q, X: 5, Y: 5, W: 20, H: 20}
	b.frame([]Layer{l, m}, 200, 200, &l, &m)
	for _, c := range []struct {
		name  string
		stack []Layer
		want  bool
	}{
		{"the same", []Layer{l, m}, false},
		{"hidden", []Layer{l}, true},
		{"reordered", []Layer{m, l}, true},
		{"moved", []Layer{l, {Pane: q, X: 6, Y: 5, W: 20, H: 20}}, true},
		{"resized", []Layer{l, {Pane: q, X: 5, Y: 5, W: 21, H: 20}}, true},
	} {
		if got := b.c.Dirty(c.stack); got != c.want {
			t.Errorf("%s: dirty %v, want %v", c.name, got, c.want)
		}
	}
}

// Frames stay at the monitor's rate, so laying them costs no garbage.
func TestAFrameAllocatesNothing(t *testing.T) {
	b := newBench(t)
	b.g.rects = make([][4]float32, 0, 4096)
	b.g.laid = make([]uint32, 0, 4096)
	base, popup := newPane(), newPane()
	lb, lp := Layer{Pane: base, W: 100, H: 80}, Layer{Pane: popup, X: 5, Y: 5, W: 20, H: 20}
	stack := []Layer{lb, lp}
	b.frame(stack, 100, 80, &lb, &lp)
	d := base.drawer.(*fakeDrawer)
	d.drew = make([]drawn, 0, 4096)
	if n := testing.AllocsPerRun(100, func() {
		if b.c.Dirty(stack) {
			t.Fatal("dirty")
		}
		b.c.Begin(stack, 100, 80)
		b.c.Draw(base, someOps, nil, 100, 80, color.NRGBA{}, 1)
		b.c.Finish(stack, 100, 80, 1)
	}); n != 0 {
		t.Fatalf("%v allocations a frame", n)
	}
}

func TestUpright(t *testing.T) {
	img := Upright([]byte{1, 0, 0, 255, 2, 0, 0, 255}, 1, 2)
	if img.RGBAAt(0, 0).R != 2 || img.RGBAAt(0, 1).R != 1 {
		t.Fatalf("got %v", img.Pix)
	}
}
