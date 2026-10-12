package install

import (
	"image/color"
	"math"
	"math/rand/v2"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// stage is the installer's window: a backdrop in the program's colours,
// the program's icon large on a glow that breathes, and under the icon
// the page the installer is on.
//
// The icon carries the work. It grows in as the window opens and
// floats while the user chooses; as the work starts it rises to the
// middle of the window and a ring draws itself round it; at the end the
// ring closes and flies out, the icon jumps, a tick badge pops onto its
// corner and confetti falls. A failure shakes it, and an uninstall
// fades it away.
type stage struct {
	anim.Group
	// img is the program's icon, or nil for a monogram of its name's
	// first letter in letter, on a tile in the accent.
	img    *paint.Image
	letter *widget.Label
	accent color.NRGBA
	// page is the page showing; pages leaving stay among the children
	// until they have faded.
	page *page

	// arrive runs from 0 to 1 as the window opens.
	arrive *anim.Float
	// lift moves the icon from over the choices, 0, to the middle of the
	// window, 1, for the work and its end.
	lift *anim.Float
	// ring is how much of the ring round the icon is drawn, from 0 to 1,
	// ringOn how strongly it shows, and tint its colour.
	ring, ringOn *anim.Float
	tint         *anim.Color
	// pop scales the icon at the end, kicked up and springing back.
	pop *anim.Float
	// badge grows the tick's badge in, and tick draws the tick.
	badge, tick *anim.Float
	// burst runs from 0 to 1 as the ring flies out and the confetti
	// falls.
	burst *anim.Float
	// shake swings the icon side to side after a failure, dying away.
	shake *anim.Float
	// fade takes the icon away as the program is uninstalled.
	fade *anim.Float

	// spinning turns a short arc round the icon, while the installer
	// waits for the program to close.
	spinning bool
	// born is the first frame, for the float and the breathing, and
	// still how long the page showing has been there, for them to come
	// to rest.
	born  time.Time
	still time.Duration
	// live runs to 1 as a page arrives, and back to 0 as the icon comes
	// to rest, which resting says it is doing.
	live    *anim.Float
	resting bool
	// pieces are the confetti.
	pieces []piece
	echo   widget.Echo
	// iconAt is where the icon was laid out, its centre and size.
	iconAt   geom.Point
	iconSize float32
}

// piece is one piece of confetti: where it flies, how it turns, and its
// colour and shape.
type piece struct {
	vx, vy, spin, turn float32
	w, h               float32
	c                  color.NRGBA
}

func newStage(img *paint.Image, name string, accent color.NRGBA) *stage {
	s := &stage{
		img:    img,
		accent: accent,
		arrive: anim.NewFloat(0),
		lift:   anim.NewFloat(0),
		ring:   anim.NewFloat(0),
		ringOn: anim.NewFloat(0),
		tint:   anim.NewColor(accent),
		pop:    anim.NewFloat(1),
		badge:  anim.NewFloat(0),
		tick:   anim.NewFloat(0),
		burst:  anim.NewFloat(0),
		shake:  anim.NewFloat(0),
		fade:   anim.NewFloat(0),
		live:   anim.NewFloat(0),
	}
	if img == nil {
		first := "?"
		for _, r := range name {
			first = string(r)
			break
		}
		s.letter = widget.NewLabel(first)
		s.letter.Face = widget.BoldFont
		s.letter.Size = monogramSize
		s.letter.Color = monogramInk
		s.letter.NoWrap = true
	}
	s.Add(s.arrive, s.lift, s.ring, s.ringOn, s.tint, s.pop, s.badge, s.tick, s.burst, s.shake, s.fade, s.live)
	s.arrive.Animate(1, anim.Spring{Response: 0.7, Damping: 0.55})
	return s
}

// Children implements [gunim.Composite].
func (s *stage) Children() []gunim.Node {
	if s.letter != nil {
		return []gunim.Node{s.letter, s.page}
	}
	return []gunim.Node{s.page}
}

// show puts p in place of the page showing, the two crossing over.
func (s *stage) show(p *page, u *gunim.UI) {
	if s.page != nil {
		u.Remove(s.page)
	}
	s.page = p
	u.Insert(s, p)
	s.wake()
}

// wake sets the icon floating again, as a page arrives.
func (s *stage) wake() {
	s.still, s.resting = 0, false
	s.live.Animate(1, wakeSpring)
}

// Step implements [gunim.Animator]. The icon floats and its glow
// breathes a while after each page arrives, and then rests.
func (s *stage) Step(dt time.Duration) bool {
	busy := s.Group.Step(dt)
	s.still += dt
	if s.still > restAfter && !s.resting {
		s.resting = true
		s.live.Animate(0, restSpring)
		busy = true
	}
	return busy || s.spinning || !s.resting
}

// The icon floats and its glow breathes for restAfter after a page
// arrives, and then comes to rest: a window at rest draws nothing, where
// one that moves for ever draws every frame, a page of long notes and
// all. The springs carry it into the motion and out of it.
var (
	restAfter  = 6 * time.Second
	wakeSpring = anim.Spring{Response: 0.8, Damping: 1}
	restSpring = anim.Spring{Response: 1.4, Damping: 1}
)

// lively is how much the icon floats and its glow breathes now.
func (s *stage) lively() float32 { return min(max(s.live.Value(), 0), 1) }

// The icon's size over the choices and in the middle of the window.
const (
	iconSmall = 124
	iconLarge = 152
	// iconLeast is as small as the icon gets to leave a long page room.
	iconLeast = 56
)

// Layout implements [gunim.Node].
func (s *stage) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	lift := s.lift.Value()
	size := iconSmall + (iconLarge-iconSmall)*lift
	place := func() {
		top := f.Safe.Top + 30 + size/2
		middle := box.H*0.36 + f.Safe.Top/2
		s.iconAt = geom.Pt(box.W/2, top+(middle-top)*lift)
		s.iconSize = size
		under := s.iconAt.Y + size/2 + 26 + 12*lift
		for i := range kids.Len() {
			k := kids.At(i)
			if k.Node() == s.letter {
				sz := k.Layout(gunim.Loose(box))
				k.Place(geom.Pt(-sz.W/2, -sz.H/2))
				continue
			}
			k.Layout(gunim.Tight(geom.Sz(box.W, max(box.H-under, 0))))
			k.Place(geom.Pt(0, under))
		}
	}
	place()
	// A page with more to say than the window has room for, under an
	// icon at its full size, has the icon smaller, so its last lines
	// stay clear of the buttons.
	if s.page != nil && s.page.short > 0 && size > iconLeast {
		size = max(size-s.page.short, iconLeast)
		place()
	}
	return box
}

// Paint implements [gunim.Node].
func (s *stage) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if s.born.IsZero() {
		s.born = f.Now
	}
	t := float32(f.Now.Sub(s.born).Seconds())
	arrive := max(s.arrive.Value(), 0)
	fade := min(max(s.fade.Value(), 0), 1)
	accent := s.accent
	base := backdrop.Get(f.Theme)

	// The backdrop: the accent at the top, sinking into the window's
	// dark.
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(0, 0), To: geom.Pt(0, box.H),
		Start: mix(base, accent, 0.22*(1-0.6*fade)), End: base,
	}})

	c := s.iconAt
	size := s.iconSize
	live := s.lively()
	breath := 0.5 + 0.5*float32(math.Sin(float64(t)*2*math.Pi/3.6))*live
	bob := 4 * float32(math.Sin(float64(t)*2*math.Pi/4.2)) * (1 - s.lift.Value()) * live
	c.Y += bob

	// The glow under the icon, blurred far, breathing.
	glow := arrive * (0.55 + 0.25*breath) * (1 - 0.85*fade)
	if glow > 0.01 {
		r := size * (0.62 + 0.08*breath)
		bounds := geom.Rc(c.X-r*2.2, c.Y-r*2.2, r*4.4, r*4.4)
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: bounds, Blur: size * 0.32, Opacity: min(glow, 1)})()
			p.RRect(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Solid(withAlpha(accent, 0xd0)))
		}()
	}

	// The ring round the icon, its track and its progress.
	ringR := size/2 + 22
	if on := min(max(s.ringOn.Value(), 0), 1); on > 0.01 {
		rr := geom.Rc(c.X-ringR, c.Y-ringR, 2*ringR, 2*ringR)
		width := int16(6 / (2 * ringR) * 1000)
		tint := s.tint.Value()
		p.Mask(arc{sweep: 3600, width: width}, rr, withAlpha(tint, uint8(48*on)))
		sweep := min(max(s.ring.Value(), 0), 1)
		if s.spinning {
			// A quarter of the ring, turning a little over once a second.
			turn := math.Mod(float64(t)*1.15, 1)
			p.Mask(arc{start: int16(turn * 3600), sweep: 900, width: width}, rr, withAlpha(tint, uint8(255*on)))
		} else if sweep > 0.001 {
			p.Mask(arc{sweep: int16(sweep * 3600), width: width}, rr, withAlpha(tint, uint8(255*on)))
			// A bright head where the ring is drawing.
			a := float64(sweep) * 2 * math.Pi
			mid := ringR - 3 - 1
			hx := c.X + mid*float32(math.Sin(a))
			hy := c.Y - mid*float32(math.Cos(a))
			p.ShadowRRect(geom.Rc(hx-4, hy-4, 8, 8), 4, paint.Solid(withAlpha(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, uint8(230*on))),
				paint.Shadow{Blur: 10, Spread: 2, Color: withAlpha(tint, uint8(220*on))})
		}
	}

	// The ring flying out, twice, as the work ends.
	if b := s.burst.Value(); b > 0 && b < 1 {
		for i, lag := range []float32{0, 0.18} {
			k := min(max((b-lag)/(1-lag), 0), 1)
			if k <= 0 {
				continue
			}
			r := ringR + 150*anim.EaseOut(k)
			alpha := uint8(200 * (1 - k) * (1 - float32(i)*0.35))
			p.RRectStroke(geom.Rc(c.X-r, c.Y-r, 2*r, 2*r), r, paint.Fill{}, paint.Stroke{Width: 3*(1-k) + 1, Color: withAlpha(s.tint.Value(), alpha)})
		}
	}

	// The icon: growing in, jumping at the end, shaking at a failure,
	// fading at an uninstall.
	scale := (0.55 + 0.45*arrive) * s.pop.Value() * (1 - 0.3*fade)
	shake := s.shake.Value() * float32(math.Sin(float64(t)*38))
	opacity := min(arrive, 1) * (1 - 0.8*fade)
	func() {
		defer p.Push(paint.Translate(geom.Pt(c.X+shake, c.Y+14*fade)))()
		defer p.Push(paint.Scale(scale, geom.Point{}))()
		half := size / 2
		r := geom.Rc(-half, -half, size, size)
		if opacity < 0.999 {
			defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-40)), Opacity: max(opacity, 0)})()
		}
		if s.img != nil {
			// A soft shadow where the icon would sit on the backdrop.
			p.ShadowRRect(geom.Rc(-half*0.62, half*0.62, half*0.62*2, size*0.1), size*0.05, paint.Solid(color.NRGBA{}),
				paint.Shadow{Offset: geom.Pt(0, 6), Blur: 16, Color: color.NRGBA{A: 0x60}})
			p.Image(s.img, r, paint.ImageOpts{Opacity: 1})
		} else {
			p.ShadowRRect(r, size*0.24, paint.Fill{Gradient: &paint.Gradient{
				From: geom.Pt(0, -half), To: geom.Pt(0, half), Start: lighten(accent, 0.15), End: darken(accent, 0.35),
			}}, paint.Shadow{Offset: geom.Pt(0, 10), Blur: 26, Color: color.NRGBA{A: 0x80}})
			for i := range kids.Len() {
				if k := kids.At(i); k.Node() == s.letter {
					k.Paint(p)
				}
			}
		}
		// The tick's badge on the icon's corner.
		if b := s.badge.Value(); b > 0.01 {
			bc := geom.Pt(half*0.72, half*0.72)
			br := float32(22) * b
			ok := doneInk.Get(f.Theme)
			p.ShadowRRect(geom.Rc(bc.X-br, bc.Y-br, 2*br, 2*br), br, paint.Solid(ok),
				paint.Shadow{Blur: 14, Color: withAlpha(ok, 0x90)})
			if tk := s.tick.Value(); tk > 0.01 {
				ir := br * 0.62
				p.Mask(icon.Stroke{Icon: icon.Check, Width: 3.2, Progress: min(tk, 1)}, geom.Rc(bc.X-ir, bc.Y-ir, 2*ir, 2*ir),
					color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
			}
		}
	}()

	// The confetti.
	if b := s.burst.Value(); b > 0 && b < 1 && len(s.pieces) > 0 {
		secs := b * 1.6
		for _, pc := range s.pieces {
			x := c.X + pc.vx*secs
			y := c.Y + pc.vy*secs + 0.5*520*secs*secs
			alpha := uint8(255 * min(1, (1-b)*3))
			func() {
				defer p.Push(paint.Translate(geom.Pt(x, y)))()
				defer p.Push(paint.Rotate(pc.turn+pc.spin*secs, geom.Point{}))()
				p.RRect(geom.Rc(-pc.w/2, -pc.h/2, pc.w, pc.h), 1.5, paint.Solid(withAlpha(pc.c, alpha)))
			}()
		}
	}

	for i := range kids.Len() {
		if k := kids.At(i); k.Node() != s.letter {
			k.Paint(p)
		}
	}
}

// work raises the icon to the middle and draws the ring in.
func (s *stage) work(u *gunim.UI) {
	s.spinning = false
	th := u.Theme()
	s.lift.Animate(1, widget.Settle.Get(th))
	s.ringOn.Animate(1, widget.Settle.Get(th))
	s.ring.Jump(0)
	s.tint.Animate(s.accent, widget.Settle.Get(th))
	s.badge.Animate(0, widget.Quick.Get(th))
	s.tick.Jump(0)
	s.fade.Animate(0, widget.Settle.Get(th))
}

// spin raises the icon to the middle and turns a short arc round it, or
// stops the arc, while the installer waits for the program to close.
func (s *stage) spin(on bool, u *gunim.UI) {
	th := u.Theme()
	s.spinning = on
	if on {
		// In the middle, as for the work, with room for the ring.
		s.lift.Animate(1, widget.Settle.Get(th))
		s.fade.Animate(0, widget.Settle.Get(th))
		s.badge.Animate(0, widget.Quick.Get(th))
		s.tint.Animate(s.accent, widget.Settle.Get(th))
		s.ringOn.Animate(1, widget.Settle.Get(th))
		return
	}
	s.ringOn.Animate(0, widget.Quick.Get(th))
}

// progress moves the ring on to done, from 0 to 1.
func (s *stage) progress(done float32) {
	s.ring.Animate(max(done, s.ring.Target()), anim.Spring{Response: 0.5, Damping: 1})
}

// finish closes the ring, then sends it flying with the confetti, pops
// a tick onto the icon and sends a ping past the window's edges.
func (s *stage) finish(u *gunim.UI) {
	s.lift.Animate(1, widget.Settle.Get(u.Theme()))
	s.ringOn.Animate(1, widget.Quick.Get(u.Theme()))
	s.ring.Animate(1, anim.Tween{Duration: 380 * time.Millisecond, Ease: anim.EaseInOut})
	u.After(400*time.Millisecond, func(u *gunim.UI) {
		s.ringOn.Animate(0, anim.Tween{Duration: 500 * time.Millisecond, Ease: anim.EaseOut})
		s.pop.Jump(1.16)
		s.pop.Animate(1, anim.Spring{Response: 0.45, Damping: 0.42})
		s.badge.Animate(1, anim.Spring{Response: 0.4, Damping: 0.5})
		s.tick.Jump(0)
		s.tick.Animate(1, anim.Tween{Duration: 420 * time.Millisecond, Ease: anim.EaseOut})
		s.confetti()
		s.burst.Jump(0)
		s.burst.Animate(1, anim.Tween{Duration: 1600 * time.Millisecond, Ease: anim.Linear})
		s.echo.Ping(u, widget.EchoDone)
	})
}

// updateBegun lights the window's edges in the program's accent as the
// user starts an update, from was, the page before.
func (s *stage) updateBegun(was string, u *gunim.UI) {
	if was == pageUpdate {
		s.echo.Glow(u, widget.Accent)
	}
}

// fail turns the ring red and shakes the icon.
func (s *stage) fail(u *gunim.UI) {
	s.tint.Animate(failInk.Get(u.Theme()), widget.Quick.Get(u.Theme()))
	s.shake.Jump(9)
	s.shake.Animate(0, anim.Tween{Duration: 650 * time.Millisecond, Ease: anim.EaseOut})
	s.ringOn.Animate(0.6, widget.Settle.Get(u.Theme()))
	s.badge.Animate(0, widget.Quick.Get(u.Theme()))
	s.echo.Ping(u, widget.EchoProblem)
}

// back lowers the icon over the choices again, the ring gone.
func (s *stage) back(u *gunim.UI) {
	th := u.Theme()
	s.lift.Animate(0, widget.Settle.Get(th))
	s.ringOn.Animate(0, widget.Settle.Get(th))
	s.tint.Animate(s.accent, widget.Settle.Get(th))
	s.badge.Animate(0, widget.Quick.Get(th))
	s.fade.Animate(0, widget.Settle.Get(th))
}

// removed fades the icon away, its glow going out.
func (s *stage) removed(u *gunim.UI) {
	s.ring.Animate(1, anim.Tween{Duration: 300 * time.Millisecond, Ease: anim.EaseInOut})
	u.After(320*time.Millisecond, func(u *gunim.UI) {
		s.ringOn.Animate(0, anim.Tween{Duration: 600 * time.Millisecond, Ease: anim.EaseOut})
		s.fade.Animate(1, anim.Spring{Response: 1.1, Damping: 1})
	})
}

// confetti throws a new handful of pieces, in the accent and its
// neighbours round the colour wheel.
func (s *stage) confetti() {
	h, sat, _ := hsv(s.accent.R, s.accent.G, s.accent.B)
	sat = max(sat, 0.55)
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7))
	s.pieces = s.pieces[:0]
	for range 46 {
		angle := rng.Float64()*math.Pi*1.6 - math.Pi*1.3 // mostly upwards
		speed := 260 + rng.Float64()*360
		hue := math.Mod(h+[]float64{0, 0, 40, -40, 180}[rng.IntN(5)]+360, 360)
		s.pieces = append(s.pieces, piece{
			vx:   float32(math.Cos(angle) * speed),
			vy:   float32(math.Sin(angle) * speed),
			spin: float32(rng.Float64()*14 - 7),
			turn: float32(rng.Float64() * math.Pi),
			w:    float32(5 + rng.Float64()*5),
			h:    float32(3 + rng.Float64()*3),
			c:    fromHSV(hue, sat, 0.95),
		})
	}
}

// mix is a blended toward b by t.
func mix(a, b color.NRGBA, t float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

func lighten(c color.NRGBA, t float32) color.NRGBA {
	return mix(c, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: c.A}, t)
}

func darken(c color.NRGBA, t float32) color.NRGBA {
	return mix(c, color.NRGBA{A: c.A}, t)
}
