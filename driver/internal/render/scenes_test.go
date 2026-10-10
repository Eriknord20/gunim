//go:build linux || windows || darwin

package render

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

func TestASceneDrawsLitMeshesInDepth(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	sphere := paint.NewSphere(24, 48, red)
	box := paint.NewBox(geom.V3(0.6, 0.6, 0.6), green)
	scene := paint.Scene{
		Camera: paint.Camera{Eye: geom.V3(0, 0, 5), At: geom.V3(0, 0, 0)},
		Light:  paint.Light{Direction: geom.V3(0, 0, -1)},
		Items: []paint.SceneItem{
			{Mesh: sphere},
			// The box sits in front of the sphere's right side, though it
			// is drawn first.
			{Mesh: box, Model: geom.Move3(geom.V3(0.6, 0, 1.2))},
		},
	}
	scene.Items[0], scene.Items[1] = scene.Items[1], scene.Items[0]
	pix := drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(200, 100, 400, 400), scene) })
	// The sphere's middle faces the light head on: red, lit.
	if got := pixelAt(pix, 370, 300); got[0] < 0xe0 || got[1] > 0x20 {
		t.Errorf("the sphere's middle is %v, want lit red", got)
	}
	// Its edge turns away from the light, and is darker.
	if mid, edge := pixelAt(pix, 370, 300), pixelAt(pix, 400, 190); edge[0] >= mid[0] {
		t.Errorf("the sphere's edge is %v and its middle %v, want the edge darker", edge, mid)
	}
	// The box hides the sphere where it stands in front of it.
	at := geom.Perspective(paint.DefaultFOV, 1, 0.1, 100).Mul(geom.LookAt(geom.V3(0, 0, 5), geom.V3(0, 0, 0), geom.V3(0, 1, 0))).Apply(geom.V3(0.6, 0, 1.5))
	bx, by := int(400+at.X*200), int(300-at.Y*200)
	if got := pixelAt(pix, bx, by); got[1] < 0xa0 || got[0] > 0x20 {
		t.Errorf("at %d, %d, before the sphere, the box is %v, want green", bx, by, got)
	}
	// Outside the sphere the view is clear, showing the black behind.
	if got := pixelAt(pix, 205, 105); got != px(black) {
		t.Errorf("in the view's corner the canvas is %v, want the black behind", got)
	}
	if got := pixelAt(pix, 150, 300); got != px(black) {
		t.Errorf("outside the view the canvas is %v, want it untouched", got)
	}
}

func TestAMeshIsUploadedOnceAndLetGo(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	m := paint.NewSphere(4, 8, color.NRGBA{A: 0xff})
	s := paint.Scene{Camera: paint.Camera{Eye: geom.V3(0, 0, 3)}, Items: []paint.SceneItem{{Mesh: m}}}
	drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(0, 0, 100, 100), s) })
	mb := r.scenes.meshes[m]
	drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(0, 0, 100, 100), s) })
	if mb == nil || r.scenes.meshes[m] != mb {
		t.Fatal("the mesh was uploaded again for the second frame")
	}
	mb.used = mb.used.Add(-2 * meshIdle)
	r.evictMeshes()
	if _, ok := r.scenes.meshes[m]; ok {
		t.Error("a mesh left undrawn stayed on the GPU")
	}
}

func TestASeeThroughMeshShowsWhatIsBehindIt(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	half := color.NRGBA{0xff, 0xff, 0xff, 0x80}
	glass := paint.SceneItem{Mesh: paint.NewSphere(24, 48, white), Tint: half}
	// The box stands behind the glass's right edge, and reaches past it.
	box := paint.SceneItem{Mesh: paint.NewBox(geom.V3(1, 1, 0.2), red), Model: geom.Move3(geom.V3(1.6, 0, -2))}
	// An even white light alone shows every face its own colour, so each
	// face of the glass is half white over what is behind it: its back
	// face over the box or the black behind, and its front face over that.
	light := paint.Light{Color: color.NRGBA{A: 0xff}, Ambient: white}
	for _, items := range [][]paint.SceneItem{{glass, box}, {box, glass}} {
		first := items[0] == glass
		s := paint.Scene{Camera: paint.Camera{Eye: geom.V3(0, 0, 5)}, Light: light, Items: items}
		pix := drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(200, 100, 400, 400), s) })
		m := s.Camera.Matrix(1)
		shows := func(v geom.Vec3) (int, int) {
			at := m.Apply(v)
			return int(400 + at.X*200), int(300 - at.Y*200)
		}
		// Through the glass's middle onto the black: a quarter black
		// left, the rest white.
		if got := pixelAt(pix, 400, 300); !near(got, [4]byte{0xbf, 0xbf, 0xbf, 0xff}, 4) {
			t.Errorf("glass listed first %v: over the black it is %v, want three quarters white", first, got)
		}
		// Through the glass onto the box: the red shows a quarter.
		if x, y := shows(geom.V3(1.2, 0, -2)); !near(pixelAt(pix, x, y), [4]byte{0xff, 0xbf, 0xbf, 0xff}, 4) {
			t.Errorf("glass listed first %v: over the box, at %d, %d, it is %v, want a quarter red under white",
				first, x, y, pixelAt(pix, x, y))
		}
		// Past the glass's edge the box is plain red.
		if x, y := shows(geom.V3(1.9, 0.3, -2)); !near(pixelAt(pix, x, y), px(red), 4) {
			t.Errorf("glass listed first %v: beside the glass the box is %v, want red", first, pixelAt(pix, x, y))
		}
	}
}

// A mesh mirrored by its model, as by a scale of -1 on one axis, is lit
// as the same mesh unmirrored, and seen through the same: a sphere is
// the same sphere mirrored.
func TestAMirroredMeshIsLitAsItIs(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	sphere := paint.NewSphere(24, 48, red)
	half := color.NRGBA{0xff, 0xff, 0xff, 0x80}
	box := paint.SceneItem{Mesh: paint.NewBox(geom.V3(1, 1, 0.2), green), Model: geom.Move3(geom.V3(0.6, 0, -2))}
	for _, c := range []struct {
		name string
		item paint.SceneItem
		at   [][2]int
	}{
		{"solid", paint.SceneItem{Mesh: sphere}, [][2]int{{370, 300}, {400, 190}, {300, 300}}},
		{"see-through", paint.SceneItem{Mesh: sphere, Tint: half}, [][2]int{{400, 300}, {430, 300}, {300, 300}}},
	} {
		plain := paint.Scene{
			Camera: paint.Camera{Eye: geom.V3(0, 0, 5)},
			Light:  paint.Light{Direction: geom.V3(-0.3, 0, -1)},
			Items:  []paint.SceneItem{box, c.item},
		}
		mirror := plain
		m := c.item
		m.Model = geom.Scale3(geom.V3(-1, 1, 1))
		mirror.Items = []paint.SceneItem{box, m}
		want := drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(200, 100, 400, 400), plain) })
		got := drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(200, 100, 400, 400), mirror) })
		for _, xy := range c.at {
			if g, w := pixelAt(got, xy[0], xy[1]), pixelAt(want, xy[0], xy[1]); !near(g, w, 6) {
				t.Errorf("%s: at %v the mirrored sphere is %v, unmirrored %v", c.name, xy, g, w)
			}
		}
	}
}

// A shape that changes, a new mesh each frame, keeps the meshes on the
// GPU within the budget, the frame's own drawn whole.
func TestMeshesStayWithinTheBudget(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	was := meshBudget
	meshBudget = 3 * meshSize(paint.NewSphere(16, 32, red))
	t.Cleanup(func() { meshBudget = was })
	for frame := range 20 {
		m := paint.NewSphere(16, 32, red)
		s := paint.Scene{Camera: paint.Camera{Eye: geom.V3(0, 0, 3)}, Items: []paint.SceneItem{{Mesh: m}}}
		pix := drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(0, 0, 100, 100), s) })
		if r.scenes.meshBytes > meshBudget {
			t.Fatalf("frame %d: the meshes take %d bytes, past the budget of %d", frame, r.scenes.meshBytes, meshBudget)
		}
		if _, ok := r.scenes.meshes[m]; !ok {
			t.Fatalf("frame %d: the frame's own mesh was let go", frame)
		}
		if got := pixelAt(pix, 50, 50); got[0] < 0x80 {
			t.Fatalf("frame %d: the sphere's middle is %v, want it drawn", frame, got)
		}
	}
}

// meshSize is how many bytes m takes on the GPU.
func meshSize(m *paint.Mesh) int {
	return len(m.Vertices())*meshVertexFloats*4 + len(m.Indices())*4
}

// The scene's target shrinks when the scenes of a while took less than
// half of it, and goes when no scene has drawn for a while.
func TestTheScenesTargetShrinksAndGoes(t *testing.T) {
	r, done := hiddenGL(t)
	defer done()
	s := paint.Scene{Camera: paint.Camera{Eye: geom.V3(0, 0, 3)}, Items: []paint.SceneItem{{Mesh: paint.NewSphere(8, 16, red)}}}
	drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(0, 0, 400, 400), s) })
	st := &r.scenes
	if st.outW < 400 || st.outH < 400 {
		t.Fatalf("a 400 by 400 scene has a target %d by %d", st.outW, st.outH)
	}
	if st.most <= 0 || st.most > sceneMost {
		t.Fatalf("the target may be %d a side", st.most)
	}
	// A while of small scenes, since the large one.
	st.peakW, st.peakH, st.peakSince = 0, 0, st.peakSince.Add(-2*meshIdle)
	drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(0, 0, 100, 100), s) })
	pix := drawn(r, func(p *paint.Painter) { p.Scene(geom.Rc(0, 0, 100, 100), s) })
	if st.outW > 100 || st.outH > 100 {
		t.Fatalf("after a while of 100 by 100 scenes the target is %d by %d", st.outW, st.outH)
	}
	if got := pixelAt(pix, 50, 50); got[0] < 0x80 {
		t.Fatalf("in the smaller target the sphere's middle is %v, want it drawn", got)
	}
	// A while of none.
	st.drawn = st.drawn.Add(-2 * meshIdle)
	st.peakSince = st.peakSince.Add(-2 * meshIdle)
	drawn(r, func(*paint.Painter) {})
	if st.ms != 0 {
		t.Error("with no scene for a while, the target stayed")
	}
}

// sceneOps records a frame holding a scene of n items, the odd ones see
// through and every third mirrored, so a frame takes both of a scene's
// passes.
func sceneOps(n int) []paint.Op {
	ball := paint.NewSphere(6, 12, red)
	items := make([]paint.SceneItem, n)
	for i := range items {
		x := float32(i%25)/12 - 1
		y := float32(i/25)/10 - 1
		items[i] = paint.SceneItem{Mesh: ball, Model: geom.Move3(geom.V3(x, y, 0)).Mul(geom.Scale3(geom.V3(0.05, 0.05, 0.05)))}
		if i%2 == 1 {
			items[i].Tint = color.NRGBA{0xff, 0xff, 0xff, 0x80}
		}
		if i%3 == 0 {
			items[i].Model = items[i].Model.Mul(geom.Scale3(geom.V3(-1, 1, 1)))
		}
	}
	var p paint.Painter
	p.RRect(geom.Rect{Max: benchSize.Point()}, 0, paint.Solid(black))
	p.Scene(geom.Rc(100, 100, 600, 400), paint.Scene{Camera: paint.Camera{Eye: geom.V3(0, 0, 3)}, Items: items})
	return p.Ops()
}

// sceneFrameAllocs is how many allocations drawing a frame of
// sceneOps(n) takes, once the meshes are on the GPU.
func sceneFrameAllocs(r *Renderer, n int) float64 {
	ops := sceneOps(n)
	w, h := int(benchSize.W), int(benchSize.H)
	draw := func() { r.Draw(ops, paint.Everything, w, h, 1) }
	for range 3 {
		draw()
	}
	return testing.AllocsPerRun(10, draw)
}

// Drawing a scene's items allocates nothing for each: a game drawing
// thousands of them a frame would otherwise make garbage by the
// megabyte each second.
func TestASceneItemDrawsWithoutAllocating(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	r, done := hiddenGL(t)
	defer done()
	one, many := sceneFrameAllocs(r, 1), sceneFrameAllocs(r, 500)
	t.Logf("a frame allocates %v times with a scene of 1 item, %v with 500", one, many)
	// A few to spare, for what the runtime may allocate now and then,
	// as a pool emptied by a collection; an allocation for each item
	// would be hundreds.
	if many > one+4 {
		t.Errorf("a frame with a scene of 500 items allocates %v times, and of 1 item %v; want no more", many, one)
	}
}

// BenchmarkRenderScene draws a frame holding a scene of 500 items.
func BenchmarkRenderScene(b *testing.B) {
	r, done := hiddenGL(b)
	defer done()
	ops := sceneOps(500)
	w, h := int(benchSize.W), int(benchSize.H)
	r.Draw(ops, paint.Everything, w, h, 1)
	r.GL.Finish()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Draw(ops, paint.Everything, w, h, 1)
		r.GL.Finish()
	}
}
