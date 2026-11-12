package widget

import (
	"image/color"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/paint"
)

// spot places its child at a fixed place and size.
type spot2 struct {
	at    geom.Rect
	child gunim.Node
}

func (s *spot2) Children() []gunim.Node { return []gunim.Node{s.child} }

func (s *spot2) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	k.Layout(gunim.Tight(s.at.Size()))
	k.Place(s.at.Min)
	return c.Max
}

func (s *spot2) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// heroStage has a thumbnail hero in view "grid", and a view "detail"
// with a big hero of the same tag, mounted and unmounted on demand.
type heroStage struct {
	w          *gunim.Window
	run        func(int)
	thumb, big *Hero
	thumbPic   *paint.Image
	bigPic     *paint.Image
}

func newHeroStage(t *testing.T) *heroStage {
	t.Helper()
	s := &heroStage{thumbPic: picture(10, 10), bigPic: picture(10, 10)}
	thumb := NewImage(s.thumbPic)
	thumb.Fit = FitFill
	s.thumb = NewHero("pic", thumb)
	s.w = gunimtest.New(t, geom.Sz(400, 400), nil)
	s.w.PaintWhenLooked(false)
	gunim.RegisterView(s.w, "grid", func(struct{}) gunim.Node {
		return &spot2{at: geom.Rc(10, 10, 40, 30), child: s.thumb}
	}, nil)
	gunim.RegisterView(s.w, "detail", func(struct{}) gunim.Node {
		big := NewImage(s.bigPic)
		big.Fit = FitFill
		s.big = NewHero("pic", big)
		return &spot2{at: geom.Rc(100, 100, 200, 150), child: s.big}
	}, nil)
	if err := s.w.Client().Mount(gunim.Root, "grid", "grid", nil); err != nil {
		t.Fatal(err)
	}
	s.run = func(n int) {
		for range n {
			s.w.Frame(time.Second / 60)
		}
	}
	s.run(60)
	return s
}

// drawn returns where each picture was drawn in the last frame.
func (s *heroStage) drawn() map[*paint.Image]geom.Rect {
	out := map[*paint.Image]geom.Rect{}
	for _, op := range s.w.Offscreen().Ops() {
		if im, ok := op.(*paint.ImageOp); ok && im.Opacity > 0 {
			t := im.Transform
			out[im.Image] = geom.Rect{Min: t.Apply(im.Rect.Min), Max: t.Apply(im.Rect.Max)}
		}
	}
	return out
}

func TestHeroFliesFromItsCounterpartAndBack(t *testing.T) {
	s := newHeroStage(t)
	if err := s.w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	d := s.drawn()
	if _, ok := d[s.thumbPic]; ok {
		t.Fatal("the thumbnail still shows while its hero flies")
	}
	r, ok := d[s.bigPic]
	if !ok {
		t.Fatal("the big picture is not drawn in flight")
	}
	// Two frames in, it is between the thumbnail and its own place.
	if r.Min.X <= 10 || r.Min.X >= 100 || r.Size().W <= 40 || r.Size().W >= 200 {
		t.Fatalf("in flight at %v, want between the thumbnail and its place", r)
	}
	s.run(120)
	if landed := s.drawn(); landed[s.bigPic] != geom.Rc(100, 100, 200, 150) || landed[s.thumbPic] != geom.Rc(10, 10, 40, 30) {
		t.Fatalf("landed with %v; want both pictures in their places", landed)
	}

	// Leaving, the big hero hides, and the thumbnail flies back from it.
	if err := s.w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	d = s.drawn()
	if _, ok := d[s.bigPic]; ok {
		t.Fatal("the big picture still shows once its hero has left")
	}
	r = d[s.thumbPic]
	if r.Min.X <= 10 || r.Min.X >= 100 {
		t.Fatalf("the thumbnail flying back is at %v, want between", r)
	}
	s.run(120)
	if d := s.drawn(); d[s.thumbPic] != geom.Rc(10, 10, 40, 30) {
		t.Fatalf("the thumbnail landed at %v", d[s.thumbPic])
	}
}

func TestHeroClosedMidFlightFliesBackVisibly(t *testing.T) {
	s := newHeroStage(t)
	if err := s.w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	s.run(4)
	if err := s.w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	if _, ok := s.drawn()[s.thumbPic]; !ok {
		t.Fatal("the thumbnail is hidden while it flies back")
	}
	s.run(120)
	if d := s.drawn(); d[s.thumbPic] != geom.Rc(10, 10, 40, 30) {
		t.Fatalf("the thumbnail landed at %v", d[s.thumbPic])
	}
}

// lingering holds its view on screen for a while as it leaves, as a view
// with an exit of its own does.
type lingering struct {
	anim.Group
	spot2
	out *anim.Float
}

func (l *lingering) Transition(p gunim.Presence, _ gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		l.out.Animate(1, anim.Tween{Duration: 300 * time.Millisecond})
	case gunim.Exiting:
		l.out.Animate(0, anim.Tween{Duration: 300 * time.Millisecond})
	case gunim.Present:
	}
	return !l.out.Active()
}

func TestHeroBroughtBackMidExitShowsAndFliesOutAgain(t *testing.T) {
	s := newHeroStage(t)
	var built int
	gunim.RegisterView(s.w, "lingering", func(struct{}) gunim.Node {
		built++
		big := NewImage(s.bigPic)
		big.Fit = FitFill
		s.big = NewHero("pic", big)
		l := &lingering{spot2: spot2{at: geom.Rc(100, 100, 200, 150), child: s.big}, out: anim.NewFloat(0)}
		l.Add(l.out)
		return l
	}, nil)
	if err := s.w.Client().Mount(gunim.Root, "detail", "lingering", nil); err != nil {
		t.Fatal(err)
	}
	s.run(120)
	if err := s.w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	s.run(3)
	back := s.drawn()[s.thumbPic]
	// Mounted again while its exit is under way, the view comes back as it
	// was, and so does its hero: from where the thumbnail has got to.
	if err := s.w.Client().Mount(gunim.Root, "detail", "lingering", nil); err != nil {
		t.Fatal(err)
	}
	s.run(1)
	if built != 1 {
		t.Fatalf("the view was built %d times; want it brought back", built)
	}
	d := s.drawn()
	if _, ok := d[s.thumbPic]; ok {
		t.Fatal("the thumbnail still shows while the hero brought back flies")
	}
	r, ok := d[s.bigPic]
	if !ok {
		t.Fatal("the hero brought back is hidden")
	}
	// Within a frame's travel of the thumbnail on its way back.
	if dx := r.Min.X - back.Min.X; dx < -15 || dx > 15 {
		t.Fatalf("the hero brought back is at %v; want on its way out from %v", r, back)
	}
	s.run(120)
	if d := s.drawn(); d[s.bigPic] != geom.Rc(100, 100, 200, 150) {
		t.Fatalf("the hero brought back landed at %v", d[s.bigPic])
	}
	// And it leaves again as any hero does.
	if err := s.w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	s.run(120)
	d = s.drawn()
	if _, ok := d[s.bigPic]; ok {
		t.Fatal("the big picture still shows once its hero has left again")
	}
	if d[s.thumbPic] != geom.Rc(10, 10, 40, 30) {
		t.Fatalf("the thumbnail landed at %v", d[s.thumbPic])
	}
}

func TestHeroReopenedMidReturnFliesFromWhereTheThumbnailIs(t *testing.T) {
	s := newHeroStage(t)
	if err := s.w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	s.run(120)
	if err := s.w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	s.run(3)
	back := s.drawn()[s.thumbPic]
	if err := s.w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	s.run(1)
	if r := s.drawn()[s.bigPic]; r.Min.X-back.Min.X < -15 || r.Min.X-back.Min.X > 15 {
		t.Fatalf("the new hero starts at %v; want within a frame's travel of the thumbnail on its way back at %v", r, back)
	}
}

// zoomed draws its child scaled by z about the child's top left corner,
// as a photo viewer zooms.
type zoomed struct {
	spot2
	z float32
}

func (z *zoomed) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	defer p.Push(paint.Scale(z.z, z.at.Min))()
	kids.At(0).Paint(p)
}

func TestHeroFliesToWhereAScaledPlaceShows(t *testing.T) {
	s := newHeroStage(t)
	gunim.RegisterView(s.w, "zoomed", func(struct{}) gunim.Node {
		big := NewImage(s.bigPic)
		big.Fit = FitFill
		s.big = NewHero("pic", big)
		return &zoomed{spot2: spot2{at: geom.Rc(100, 100, 100, 75), child: s.big}, z: 2}
	}, nil)
	if err := s.w.Client().Mount(gunim.Root, "detail", "zoomed", nil); err != nil {
		t.Fatal(err)
	}
	// Its place shows at 200 by 150 from (100, 100): it grows there
	// smoothly, its corner and its size both, with no jump as it lands.
	prev := geom.Rc(10, 10, 40, 30)
	for i := range 120 {
		s.run(1)
		r, ok := s.drawn()[s.bigPic]
		if !ok {
			t.Fatalf("frame %d: the flying picture is missing", i)
		}
		if r.Min.X < 9 || r.Min.X > 101 || r.Min.Y < 9 || r.Min.Y > 101 {
			t.Fatalf("frame %d: the corner flew off to %v", i, r.Min)
		}
		if dw := r.Size().W - prev.Size().W; dw < -1 || dw > 40 {
			t.Fatalf("frame %d: width went from %v to %v", i, prev.Size().W, r.Size().W)
		}
		prev = r
	}
	if prev != geom.Rc(100, 100, 200, 150) {
		t.Fatalf("landed at %v; want where its place shows", prev)
	}
}

// clipped draws its child inside a clip of its own.
type clipped struct {
	spot2
	clip geom.Rect
}

func (c *clipped) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	defer p.Layer(paint.LayerOpts{Bounds: c.clip, Opacity: 1, Clip: true})()
	kids.At(0).Paint(p)
}

func TestHeroLandsInsideWhatClipsItsPlace(t *testing.T) {
	s := newHeroStage(t)
	place := geom.Rc(100, 100, 200, 100)
	gunim.RegisterView(s.w, "clipped", func(struct{}) gunim.Node {
		big := NewImage(s.bigPic)
		big.Fit = FitFill
		s.big = NewHero("pic", big)
		return &clipped{spot2: spot2{at: geom.Rc(100, 100, 200, 150), child: s.big}, clip: place}
	}, nil)
	if err := s.w.Client().Mount(gunim.Root, "detail", "clipped", nil); err != nil {
		t.Fatal(err)
	}
	// The flight's own clip starts around the thumbnail, outside its
	// place's, and narrows to its place's as it lands.
	prev := float32(-1)
	for i := range 120 {
		s.run(1)
		var flight geom.Rect
		for _, op := range s.w.Offscreen().Ops() {
			if l, ok := op.(*paint.LayerOp); ok && l.Opts.Clip && l.Opts.Bounds != place {
				flight = l.Opts.Bounds
			}
		}
		if flight.Empty() {
			if i == 0 {
				t.Fatal("the flight is not clipped")
			}
			break
		}
		if i == 0 && (flight.Min.X > 10 || flight.Min.Y > 10) {
			t.Fatalf("the flight's clip starts at %v; want around the thumbnail", flight)
		}
		// The flight's spring may overshoot its place a little.
		if flight.Min.X < prev-1 {
			t.Fatalf("frame %d: the flight's clip widened, to %v", i, flight)
		}
		prev = flight.Min.X
	}
	if prev < 95 {
		t.Fatalf("the flight's clip ended at x %v; want near its place's, 100", prev)
	}
}

// captioned draws a caption over its child, as a viewer draws a file's
// name over the photo.
type captioned struct {
	zoomed
	caption geom.Rect
}

func (c *captioned) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	c.zoomed.Paint(p, f, box, kids)
	p.RRect(c.caption, 0, paint.Solid(color.NRGBA{R: 1, A: 0xff}))
}

func TestAHeroInPlaceFliesUnderWhatItsScreenDrawsOverIt(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		s := newHeroStage(t)
		gunim.RegisterView(s.w, "captioned", func(struct{}) gunim.Node {
			big := NewImage(s.bigPic)
			big.Fit = FitFill
			s.big = NewHero("pic", big)
			s.big.InPlace = inPlace
			return &captioned{zoomed: zoomed{spot2: spot2{at: geom.Rc(100, 100, 100, 75), child: s.big}, z: 2},
				caption: geom.Rc(100, 200, 80, 20)}
		}, nil)
		if err := s.w.Client().Mount(gunim.Root, "detail", "captioned", nil); err != nil {
			t.Fatal(err)
		}
		prev := geom.Rc(10, 10, 40, 30)
		for i := range 120 {
			s.run(1)
			img, caption := -1, -1
			for j, op := range s.w.Offscreen().Ops() {
				switch op := op.(type) {
				case *paint.ImageOp:
					if op.Image == s.bigPic {
						img = j
					}
				case *paint.RRectOp:
					if op.Fill.Solid == (color.NRGBA{R: 1, A: 0xff}) {
						caption = j
					}
				}
			}
			if img < 0 || caption < 0 {
				t.Fatalf("frame %d: the picture or the caption is missing", i)
			}
			if over := img > caption; over != !inPlace && i < 10 {
				t.Fatalf("InPlace %v, frame %d: the flying picture is drawn over the caption: %v", inPlace, i, over)
			}
			// In place or not, it flies to where its place shows.
			r := s.drawn()[s.bigPic]
			if dw := r.Size().W - prev.Size().W; dw < -1 || dw > 40 {
				t.Fatalf("InPlace %v, frame %d: width went from %v to %v", inPlace, i, prev.Size().W, r.Size().W)
			}
			prev = r
		}
		if prev != geom.Rc(100, 100, 200, 150) {
			t.Fatalf("InPlace %v: landed at %v", inPlace, prev)
		}
	}
}

func TestAHeroInPlaceFliesBackUnderWhatItsScreenDrawsOverIt(t *testing.T) {
	s := newHeroStage(t)
	gunim.RegisterView(s.w, "captioned", func(struct{}) gunim.Node {
		big := NewImage(s.bigPic)
		big.Fit = FitFill
		s.big = NewHero("pic", big)
		s.big.InPlace = true
		return &captioned{zoomed: zoomed{spot2: spot2{at: geom.Rc(100, 100, 100, 75), child: s.big}, z: 2},
			caption: geom.Rc(100, 200, 80, 20)}
	}, nil)
	if err := s.w.Client().Mount(gunim.Root, "detail", "captioned", nil); err != nil {
		t.Fatal(err)
	}
	s.run(120)
	if err := s.w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	// The big picture flies back to the thumbnail, under the caption, the
	// thumbnail hidden until it lands, and the screen stays till then.
	prev := geom.Rc(100, 100, 200, 150)
	landed := false
	for i := range 120 {
		s.run(1)
		img, caption := -1, -1
		for j, op := range s.w.Offscreen().Ops() {
			switch op := op.(type) {
			case *paint.ImageOp:
				if op.Image == s.bigPic && op.Opacity > 0 {
					img = j
				}
			case *paint.RRectOp:
				if op.Fill.Solid == (color.NRGBA{R: 1, A: 0xff}) {
					caption = j
				}
			}
		}
		d := s.drawn()
		if img < 0 {
			// Landed: the thumbnail in its place, the screen gone.
			if r := d[s.thumbPic]; r != geom.Rc(10, 10, 40, 30) {
				t.Fatalf("frame %d: after the flight back the thumbnail is at %v", i, r)
			}
			landed = true
			break
		}
		if caption < 0 || img > caption {
			t.Fatalf("frame %d: the picture flying back is not under the caption (picture %d, caption %d)", i, img, caption)
		}
		if _, ok := d[s.thumbPic]; ok {
			t.Fatalf("frame %d: the thumbnail shows while the picture flies back to it", i)
		}
		r := d[s.bigPic]
		if dw := r.Size().W - prev.Size().W; dw > 1 || dw < -40 {
			t.Fatalf("frame %d: width went from %v to %v", i, prev.Size().W, r.Size().W)
		}
		prev = r
	}
	if !landed {
		t.Fatal("the picture never landed")
	}
	if prev.Size().W > 45 {
		t.Fatalf("the last frame of the flight was %v; want at the thumbnail", prev)
	}
}

func TestHeroFlightMovesSmoothlyEveryFrame(t *testing.T) {
	s := newHeroStage(t)
	if err := s.w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	prev := geom.Rc(10, 10, 40, 30)
	for i := range 90 {
		s.run(1)
		r, ok := s.drawn()[s.bigPic]
		if !ok {
			t.Fatalf("frame %d: the flying picture is missing", i)
		}
		// Growing toward 200 wide, never shrinking by more than a spring's
		// settling, and never leaping.
		if dw := r.Size().W - prev.Size().W; dw < -1 || dw > 60 {
			t.Fatalf("frame %d: width went from %v to %v", i, prev.Size().W, r.Size().W)
		}
		if dx := r.Min.X - prev.Min.X; dx < -1 || dx > 40 {
			t.Fatalf("frame %d: left edge went from %v to %v", i, prev.Min.X, r.Min.X)
		}
		prev = r
	}
}

func TestAnAnchorHeroStaysPutAndTakesItsCounterpartBack(t *testing.T) {
	thumbPic, bigPic := picture(10, 10), picture(10, 10)
	var thumb *Hero
	w := gunimtest.New(t, geom.Sz(400, 400), nil)
	w.PaintWhenLooked(false)
	gunim.RegisterView(w, "grid", func(struct{}) gunim.Node {
		img := NewImage(thumbPic)
		img.Fit = FitFill
		thumb = NewHero("pic", img)
		thumb.Anchor = true
		return &spot2{at: geom.Rc(10, 10, 40, 30), child: thumb}
	}, nil)
	gunim.RegisterView(w, "detail", func(struct{}) gunim.Node {
		img := NewImage(bigPic)
		img.Fit = FitFill
		return &spot2{at: geom.Rc(100, 100, 200, 150), child: NewHero("pic", img)}
	}, nil)
	s := &heroStage{w: w, run: func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}}
	if err := w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	s.run(30)
	// The anchor arrives where it belongs, and the big picture stays.
	if err := w.Client().Mount(gunim.Root, "grid", "grid", nil); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	if d := s.drawn(); d[thumbPic] != geom.Rc(10, 10, 40, 30) || d[bigPic] != geom.Rc(100, 100, 200, 150) {
		t.Fatalf("an anchor arriving drew %v; want both pictures in their places", d)
	}
	// The anchor leaving sends nothing flying.
	if err := w.Client().Unmount("grid"); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	if d := s.drawn(); d[bigPic] != geom.Rc(100, 100, 200, 150) {
		t.Fatalf("an anchor leaving moved the big picture to %v", d[bigPic])
	}
	// Back again, it takes the big picture home as it leaves.
	if err := w.Client().Mount(gunim.Root, "grid", "grid", nil); err != nil {
		t.Fatal(err)
	}
	s.run(30)
	if err := w.Client().Unmount("detail"); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	r := s.drawn()[thumbPic]
	if r.Min.X <= 10 || r.Min.X >= 100 {
		t.Fatalf("the anchor flying back is at %v, want between", r)
	}
	if !thumb.Anchor {
		t.Fatal("the grid was built again")
	}
}

func TestAHeroWithANewTagLeavesItsOldPartnerAlone(t *testing.T) {
	s := newHeroStage(t)
	if err := s.w.Client().Mount(gunim.Root, "detail", "detail", nil); err != nil {
		t.Fatal(err)
	}
	s.run(120)
	s.big.Tag = "other"
	s.run(2)
	if err := s.w.Client().Unmount("grid"); err != nil {
		t.Fatal(err)
	}
	s.run(2)
	if r := s.drawn()[s.bigPic]; r != geom.Rc(100, 100, 200, 150) {
		t.Fatalf("the big picture flew to %v for a hero of the tag it left", r)
	}
}
