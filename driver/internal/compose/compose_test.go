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
	"github.com/marrasen/gunim/text"
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
	// The GL state a draw or a clear goes by, and a record of each.
	viewH   int32
	scissor bool
	clear   [4]float32
	calls   []glCall
	blend   bool
	blendFn [4]uint32
	program uint32
	vao     uint32
	active  uint32
	// attached is each framebuffer's colour texture, and params each texture's
	// settings.
	attached map[uint32]uint32
	params   map[uint32]map[uint32]int32
	// turn is the last matrix the compositor turned its layers by.
	turn [4]float32
	// fade is what the compositor's program multiplies by now, and
	// fades what each lay went by.
	fade  float32
	fades []float32
}

// glCall is the state a clear or a laid texture went by.
type glCall struct {
	kind         string
	fbo          uint32
	viewW, viewH int32
	scissor      bool
	clear        [4]float32
	blend        bool
	blendFn      [4]uint32
	program, vao uint32
	active       uint32
}

func (g *fakeGL) record(kind string) {
	g.calls = append(g.calls, glCall{
		kind: kind, fbo: g.fbo, viewW: g.viewW, viewH: g.viewH, scissor: g.scissor, clear: g.clear,
		blend: g.blend, blendFn: g.blendFn, program: g.program, vao: g.vao, active: g.active,
	})
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
func (g *fakeGL) UseProgram(p uint32)                                         { g.program = p }
func (g *fakeGL) Uniform1i(int32, int32)                                      {}
func (g *fakeGL) CreateVertexArray() uint32                                   { return g.name() }
func (g *fakeGL) BindVertexArray(a uint32)                                    { g.vao = a }
func (g *fakeGL) CreateBuffer() uint32                                        { return g.name() }
func (g *fakeGL) BindBuffer(uint32, uint32)                                   {}
func (g *fakeGL) BufferData(uint32, []byte, uint32)                           {}
func (g *fakeGL) EnableVertexAttribArray(uint32)                              {}
func (g *fakeGL) VertexAttribPointer(uint32, int32, uint32, bool, int32, int) {}
func (g *fakeGL) CreateTexture() uint32                                       { return g.name() }
func (g *fakeGL) CreateFramebuffer() uint32                                   { return g.name() }
func (g *fakeGL) BindTexture(_, t uint32)                                     { g.texture = t }
func (g *fakeGL) TexParameteri(_, name uint32, v int32) {
	if g.params == nil {
		g.params = map[uint32]map[uint32]int32{}
	}
	if g.params[g.texture] == nil {
		g.params[g.texture] = map[uint32]int32{}
	}
	g.params[g.texture][name] = v
}
func (g *fakeGL) FramebufferTexture2D(_, _, _, t uint32, _ int32) {
	if g.attached == nil {
		g.attached = map[uint32]uint32{}
	}
	g.attached[g.fbo] = t
}
func (g *fakeGL) DeleteTexture(t uint32)      { g.deleted = append(g.deleted, t) }
func (g *fakeGL) DeleteFramebuffer(f uint32)  { g.deleted = append(g.deleted, f) }
func (g *fakeGL) BindFramebuffer(_, f uint32) { g.fbo = f }
func (g *fakeGL) Viewport(_, _, w, h int32)   { g.viewW, g.viewH = w, h }
func (g *fakeGL) Disable(c uint32) {
	g.scissor = g.scissor && c != glScissorTest
	g.blend = g.blend && c != gl.BLEND
}
func (g *fakeGL) Enable(c uint32) {
	g.scissor = g.scissor || c == glScissorTest
	g.blend = g.blend || c == gl.BLEND
}
func (g *fakeGL) ClearColor(r, c, b, a float32)         { g.clear = [4]float32{r, c, b, a} }
func (g *fakeGL) Clear(uint32)                          { g.record("clear") }
func (g *fakeGL) BlendFuncSeparate(a, b, c, d uint32)   { g.blendFn = [4]uint32{a, b, c, d} }
func (g *fakeGL) ActiveTexture(t uint32)                { g.active = t }
func (g *fakeGL) Uniform4fv(_ int32, v []float32)       { g.rects = append(g.rects, [4]float32(v)) }
func (g *fakeGL) UniformMatrix2fv(_ int32, v []float32) { g.turn = [4]float32(v) }
func (g *fakeGL) Uniform1fv(_ int32, v []float32)       { g.fade = v[0] }
func (g *fakeGL) DrawElements(uint32, int32, uint32, int) {
	g.laid = append(g.laid, g.texture)
	g.fades = append(g.fades, g.fade)
	g.record("lay")
}
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
	// kept is what keep last said: the frames stay in the canvas.
	kept bool
	// g, if set, has the draws leave GL as a renderer does: its own framebuffer,
	// viewport, program, vertex array, blending and texture unit, and a scissor on.
	g *fakeGL
}

func (d *fakeDrawer) leaveState(fbo uint32, w, h int) {
	if d.g != nil {
		d.g.BindFramebuffer(gl.FRAMEBUFFER, fbo)
		d.g.Viewport(0, 0, int32(w), int32(h))
		d.g.Enable(glScissorTest)
		d.g.Disable(gl.BLEND)
		d.g.BlendFuncSeparate(0, 0, 0, 0)
		d.g.UseProgram(9001)
		d.g.BindVertexArray(9002)
		d.g.ActiveTexture(gl.TEXTURE0 + 5)
	}
}

func (d *fakeDrawer) drawRects(fbo uint32, _ color.NRGBA, _ []paint.Op, _ []geom.Rect, w, h int, _ float32) time.Time {
	d.leaveState(fbo, w, h)
	d.drew = append(d.drew, drawn{fbo: fbo, w: w, h: h})
	return d.sharpAt
}

func (d *fakeDrawer) draw(fbo uint32, _ []paint.Op, w, h int, _ float32) time.Time {
	d.leaveState(fbo, w, h)
	d.drew = append(d.drew, drawn{fbo: fbo, whole: true, w: w, h: h})
	return d.sharpAt
}

func (d *fakeDrawer) rebind() { *d.rebinds++ }

func (d *fakeDrawer) release() { d.gone++ }

func (d *fakeDrawer) keep(on bool) { d.kept = on }

// canvas is the fake renderer's canvas: names well apart from the fake GL's.
func (d *fakeDrawer) canvas() (tex, fbo uint32) { return 7000, 7001 }

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
	c.open = func(*Pane) (drawer, error) { return &fakeDrawer{rebinds: &b.rebinds, g: g}, nil }
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

// A surface with nothing to show yet, as a canvas that has just stopped being
// hidden, shows the window's background and not whatever a new surface holds.
func TestAWindowWithNoFrameYetShowsItsBackground(t *testing.T) {
	for _, c := range []struct {
		name  string
		under color.NRGBA
		want  [4]float32
	}{
		{"solid", color.NRGBA{R: 255, G: 128, A: 255}, [4]float32{1, 128.0 / 255, 0, 1}},
		{"half see-through", color.NRGBA{R: 255, A: 128}, [4]float32{128.0 / 255, 0, 0, 1}},
		{"none told yet", color.NRGBA{}, [4]float32{0, 0, 0, 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBench(t)
			p := newPane()
			// As a renderer left them, from a window closed before.
			b.g.BindFramebuffer(gl.FRAMEBUFFER, 7)
			b.g.Viewport(0, 0, 3, 3)
			b.g.Enable(glScissorTest)
			b.g.ClearColor(1, 1, 1, 1)
			l := Layer{Pane: p, W: 100, H: 80, Under: c.under}
			b.frame([]Layer{l}, 100, 80)
			if len(b.g.calls) != 1 {
				t.Fatalf("calls %+v, want one clear", b.g.calls)
			}
			got := b.g.calls[0]
			if got.kind != "clear" || got.fbo != 0 || got.viewW != 100 || got.viewH != 80 || got.scissor {
				t.Fatalf("the clear went by %+v", got)
			}
			for i := range got.clear {
				if math.Abs(float64(got.clear[i]-c.want[i])) > 1e-5 {
					t.Fatalf("cleared to %v, want %v", got.clear, c.want)
				}
			}
			if b.g.clear != [4]float32{0, 0, 0, 0} {
				t.Fatalf("left the clear colour at %v", b.g.clear)
			}
			if p.LastW != 0 || p.Last != nil {
				t.Fatalf("it holds a last frame: %+v", p)
			}
			// Its first frame draws over it as any frame does.
			b.frame([]Layer{l}, 100, 80, &l)
			if got, want := took(p), []drawn{{fbo: 0, w: 100, h: 80}}; !slices.Equal(got, want) {
				t.Fatalf("it drew %v, want %v", got, want)
			}
		})
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

// Renderers leave their own viewport and a scissor on, and laying must not go by
// them: one popup's viewport would cut every texture to its size, and a scissor
// would leave part of the surface as the last frame left it.
// A window at the bottom that shows what is under it, or leaves part of the
// surface bare, has the surface cleared first, to opaque black.
func TestLayingClearsUnderAWindowThatDoesNotHideIt(t *testing.T) {
	for _, c := range []struct {
		name string
		l    Layer
	}{
		{"transparent", Layer{Pane: &Pane{Transparent: true, Sharp: func(time.Time) {}}, W: 100, H: 80}},
		{"smaller", Layer{Pane: newPane(), X: 10, W: 90, H: 80}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBench(t)
			pop := Layer{Pane: newPane(), X: 5, Y: 5, W: 20, H: 20}
			b.frame([]Layer{c.l, pop}, 100, 80, &c.l, &pop)
			var cleared *glCall
			for i := range b.g.calls {
				if b.g.calls[i].kind == "clear" && b.g.calls[i].fbo == 0 {
					cleared = &b.g.calls[i]
				}
			}
			if cleared == nil || cleared.clear != [4]float32{0, 0, 0, 1} {
				t.Fatalf("calls %+v, want the surface cleared to black", b.g.calls)
			}
			for _, call := range b.g.calls {
				if call.kind == "lay" && !call.blend {
					t.Fatalf("a layer laid unblended over a cleared surface: %+v", call)
				}
			}
		})
	}
}

func TestLayingSetsTheGLStateItGoesBy(t *testing.T) {
	for _, awkward := range []struct {
		name string
		move func(b *bench, lb, lp *Layer)
	}{
		{"a popup drew last", func(*bench, *Layer, *Layer) {}},
		{"the surface changed size", func(_ *bench, lb, _ *Layer) { lb.W, lb.H = 80, 100 }},
		{"the popup moved", func(_ *bench, _, lp *Layer) { lp.X, lp.Y = 30, 30 }},
	} {
		t.Run(awkward.name, func(t *testing.T) {
			b := newBench(t)
			lb := Layer{Pane: newPane(), W: 100, H: 80}
			lp := Layer{Pane: newPane(), X: 5, Y: 5, W: 20, H: 20}
			b.frame([]Layer{lb, lp}, 100, 80, &lb, &lp)
			awkward.move(b, &lb, &lp)
			b.g.calls = nil
			// The popup draws last, and its renderer leaves its viewport and scissor.
			b.frame([]Layer{lb, lp}, lb.W, lb.H, &lp)
			// The window at the bottom is opaque and covers the surface, so
			// there is no clear under it, and it is laid unblended.
			if len(b.g.calls) != 2 {
				t.Fatalf("calls %+v, want two layers", b.g.calls)
			}
			for i, c := range b.g.calls {
				if c.fbo != 0 {
					t.Errorf("call %d (%s) went to framebuffer %d", i, c.kind, c.fbo)
				}
				if c.viewW != int32(lb.W) || c.viewH != int32(lb.H) {
					t.Errorf("call %d (%s) had a viewport of %dx%d on a surface of %dx%d", i, c.kind, c.viewW, c.viewH, lb.W, lb.H)
				}
				if c.scissor {
					t.Errorf("call %d (%s) had a scissor on", i, c.kind)
				}
				if c.kind != "lay" {
					continue
				}
				if c.blend != (i == 1) || c.blendFn != [4]uint32{gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA} {
					t.Errorf("layer %d blended %v with %v, want the popup alone blended, premultiplied", i, c.blend, c.blendFn)
				}
				if c.program != b.c.prog || c.vao != b.c.vao || c.active != gl.TEXTURE0 {
					t.Errorf("a layer drew with program %d, vertex array %d, unit %#x", c.program, c.vao, c.active)
				}
			}
			if b.g.vao != 0 {
				t.Errorf("laying left vertex array %d bound", b.g.vao)
			}
			// A renderer clears with what it finds, for the corners of a popup.
			if b.g.clear != [4]float32{0, 0, 0, 0} {
				t.Errorf("laying left the clear colour at %v, where a renderer wants nothing", b.g.clear)
			}
		})
	}
}

// A window's texture is ready to draw into and to lay, and making it leaves no
// framebuffer bound.
func TestATextureIsReadyToDrawInto(t *testing.T) {
	b := newBench(t)
	p := newPane()
	p.LastW, p.LastH = 20, 10
	p.onTexture(b.g)
	if b.g.fbo != 0 {
		t.Errorf("making a texture left framebuffer %d bound", b.g.fbo)
	}
	if b.g.attached[p.fbo] != p.tex {
		t.Errorf("framebuffer %d draws into %d, want %d", p.fbo, b.g.attached[p.fbo], p.tex)
	}
	if got := b.g.sized[p.tex]; got != [2]int32{20, 10} {
		t.Errorf("the texture is %v", got)
	}
	want := map[uint32]int32{
		gl.TEXTURE_MIN_FILTER: glNearest, gl.TEXTURE_MAG_FILTER: glNearest,
		gl.TEXTURE_WRAP_S: gl.CLAMP_TO_EDGE, gl.TEXTURE_WRAP_T: gl.CLAMP_TO_EDGE,
	}
	for name, v := range want {
		if got := b.g.params[p.tex][name]; got != v {
			t.Errorf("texture setting %#x is %#x, want %#x", name, got, v)
		}
	}
	if p.target != p.fbo || !p.OnTex {
		t.Errorf("the next draw goes to %d, on a texture %v", p.target, p.OnTex)
	}
	// A size it has already, it keeps.
	was := p.tex
	b.g.sized = nil
	p.onTexture(b.g)
	if p.tex != was || b.g.sized != nil {
		t.Errorf("at the same size the texture went from %d to %d, sized %v", was, p.tex, b.g.sized)
	}
}

// fakeStyler records what a window's renderer is told about its text.
type fakeStyler struct {
	told        int
	transparent bool
}

func (f *fakeStyler) SetText(_ text.Rendering, transparent bool) {
	f.told++
	f.transparent = transparent
}

// A window that blends with what is under it has no subpixel text, which only
// comes out right over a surface that holds its colour.
func TestARendererHearsWhetherItsWindowIsTransparent(t *testing.T) {
	for _, want := range []bool{true, false} {
		var f fakeStyler
		(&Pane{Transparent: want}).styleText(&f)
		if f.told != 1 || f.transparent != want {
			t.Errorf("a pane Transparent %v told it %v, %d times", want, f.transparent, f.told)
		}
	}
}

// A frame drawn straight onto the surface leaves nothing dirty, as a frame laid
// from textures does.
func TestAloneIsNotDirtyOnceDrawn(t *testing.T) {
	b := newBench(t)
	l := Layer{Pane: newPane(), W: 100, H: 80}
	stack := []Layer{l}
	if !b.c.Dirty(stack) {
		t.Fatal("a stack never laid is not dirty")
	}
	b.frame(stack, 100, 80, &l)
	if b.c.Dirty(stack) {
		t.Fatal("the stack just drawn is dirty")
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

// A shade lies over the whole surface, blended, after every window, and
// a new shade alone makes a frame.
func TestAShadeLiesOverTheWholeSurface(t *testing.T) {
	for _, turn := range []int{0, 90} {
		b := newBench(t)
		b.c.SetTurn(turn)
		p := newPane()
		l := Layer{Pane: p, W: 100, H: 80}
		stack := []Layer{l}
		b.frame(stack, 100, 80, &l)
		laid := len(b.g.laid)
		b.c.SetShade(0.25)
		if !b.c.Dirty(stack) {
			t.Fatalf("turned %d: a new shade is not dirty", turn)
		}
		b.frame(stack, 100, 80)
		if len(b.g.laid) <= laid {
			t.Fatalf("turned %d: nothing laid for the shade", turn)
		}
		last := b.g.calls[len(b.g.calls)-1]
		if b.g.laid[len(b.g.laid)-1] != b.c.black || !last.blend || last.fbo != 0 || b.g.fades[len(b.g.fades)-1] != 0.25 {
			t.Fatalf("turned %d: the last lay is texture %d, blended %v, on %d, at %v; want the black one, blended, on the surface, at 0.25",
				turn, b.g.laid[len(b.g.laid)-1], last.blend, last.fbo, b.g.fades[len(b.g.fades)-1])
		}
		if got := b.g.rects[len(b.g.rects)-1]; got != [4]float32{-1, -1, 1, 1} {
			t.Fatalf("turned %d: the shade covers %v, want the whole surface", turn, got)
		}
		if b.g.fade != 1 {
			t.Fatalf("turned %d: the program is left fading by %v", turn, b.g.fade)
		}
		if b.c.Dirty(stack) {
			t.Fatalf("turned %d: dirty once the shade is laid", turn)
		}
		b.c.SetShade(0)
		if !b.c.Dirty(stack) {
			t.Fatalf("turned %d: taking the shade off is not dirty", turn)
		}
		laid = len(b.g.laid)
		b.frame(stack, 100, 80)
		for _, tex := range b.g.laid[laid:] {
			if tex == b.c.black {
				t.Fatalf("turned %d: the shade is laid at 0", turn)
			}
		}
	}
}

func TestUpright(t *testing.T) {
	img := Upright([]byte{1, 0, 0, 255, 2, 0, 0, 255}, 1, 2)
	if img.RGBAAt(0, 0).R != 2 || img.RGBAAt(0, 1).R != 1 {
		t.Fatalf("got %v", img.Pix)
	}
}

// A screen turned sideways, as a portrait panel shown in landscape, keeps a
// window alone on it in its renderer's canvas, and lays the canvas turned,
// with the viewport the panel's own way up: one copy onto the surface, as
// unturned, and no blending under an opaque window.
func TestTurnedLaysTheCanvasTurnedOnTheSurface(t *testing.T) {
	b := newBench(t)
	b.c.SetTurn(90)
	p := newPane()
	l := Layer{Pane: p, W: 1280, H: 720}
	b.frame([]Layer{l}, 1280, 720, &l)
	if got, want := took(p), []drawn{{fbo: 0, w: 1280, h: 720}}; !slices.Equal(got, want) {
		t.Fatalf("drew %v, want %v", got, want)
	}
	if d := p.drawer.(*fakeDrawer); !d.kept || !p.InCanvas || p.tex != 0 {
		t.Fatalf("kept in the canvas %v (%v), texture %d", d.kept, p.InCanvas, p.tex)
	}
	if !slices.Equal(b.g.laid, []uint32{7000}) {
		t.Fatalf("laid %v, want the canvas, 7000", b.g.laid)
	}
	if want := [4]float32{0, -1, 1, 0}; b.g.turn != want {
		t.Fatalf("turned by %v, want %v", b.g.turn, want)
	}
	lay := b.g.calls[len(b.g.calls)-1]
	if lay.kind != "lay" || lay.viewW != 720 || lay.viewH != 1280 || lay.blend || lay.fbo != 0 {
		t.Fatalf("laid with %+v, want onto the surface, 720x1280, unblended", lay)
	}
	for _, c := range b.g.calls {
		if c.kind == "clear" && c.fbo == 0 {
			t.Fatalf("cleared the surface under an opaque window: %+v", c)
		}
	}

	// A frame with nothing new, as a popup closing, lays the canvas again.
	b.g.laid = nil
	b.frame([]Layer{l}, 1280, 720)
	if got := took(p); len(got) != 0 || !slices.Equal(b.g.laid, []uint32{7000}) {
		t.Fatalf("drew %v, laid %v; want the canvas laid again, nothing drawn", got, b.g.laid)
	}

	// A popup over it: the window goes through its texture, drawn again.
	q := newPane()
	q.Transparent = true
	pop := Layer{Pane: q, X: 100, Y: 100, W: 200, H: 100}
	b.frame([]Layer{l, pop}, 1280, 720, &pop)
	if got := took(p); len(got) != 1 || got[0].fbo != p.fbo || !got[0].whole {
		t.Fatalf("under a popup the window drew %v, want its last frame into its texture", got)
	}
	if d := p.drawer.(*fakeDrawer); d.kept || p.InCanvas {
		t.Fatal("under a popup the window still keeps its frames in the canvas")
	}

	// Alone again: back in the canvas, its last frame drawn there.
	b.frame([]Layer{l}, 1280, 720)
	if got := took(p); len(got) != 1 || got[0].fbo != 0 || !p.InCanvas {
		t.Fatalf("alone again the window drew %v, in the canvas %v", got, p.InCanvas)
	}

	b.c.SetTurn(0)
	if want := [4]float32{1, 0, 0, 1}; b.g.turn != want {
		t.Fatalf("turned back by %v, want %v", b.g.turn, want)
	}
	l.W, l.H = 720, 1280
	b.frame([]Layer{l}, 720, 1280, &l)
	if got := took(p); len(got) != 1 || got[0].fbo != 0 || p.InCanvas || p.drawer.(*fakeDrawer).kept {
		t.Fatalf("drew %v, want straight onto the surface again", got)
	}
}

// A shot of a window kept in its canvas reads the canvas.
func TestShootReadsTheCanvasOfATurnedWindow(t *testing.T) {
	b := newBench(t)
	b.c.SetTurn(90)
	p := newPane()
	l := Layer{Pane: p, W: 40, H: 20}
	b.c.Begin([]Layer{l}, 40, 20)
	b.c.Draw(p, someOps, nil, 40, 20, color.NRGBA{}, 1)
	done := make(chan struct{})
	b.c.Shoot(p, 40, 20, func(*image.RGBA) { close(done) })
	<-done
	if !slices.Equal(b.g.read, []uint32{7001}) {
		t.Fatalf("read %v, want the canvas, 7001", b.g.read)
	}
}
