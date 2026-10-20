package filemanager

import (
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// speedEvery is how often an operation's speed is sampled, and
// speedSpan how many samples its graph shows.
const (
	speedEvery = 200 * time.Millisecond
	speedSpan  = 50
)

// cheerFor is how long a finished operation's card stays, showing it
// went well, and stayAfter how long an operation runs before its card
// stays until the user closes it.
const (
	cheerFor  = 4 * time.Second
	stayAfter = 15 * time.Second
)

// Success is the colour of something that went well.
var Success = theme.Color("files.success", color.NRGBA{R: 0x4f, G: 0xd6, B: 0x9c, A: 0xff})

// speedometer turns the bytes an operation has moved, as it goes, into
// its speed.
type speedometer struct {
	at    time.Time
	bytes int64
	// rate is the last sample, and smooth the speed over the last few.
	rate, smooth float64
	// shown is the speed the panel says, which changes once a second at
	// most so it can be read, and shownAt when it last changed.
	shown   float64
	shownAt time.Time
	// eta is the seconds left as of etaAt.
	eta   float64
	etaAt time.Time
}

// etaDrift is how long, in seconds, the time left takes to drift most of
// the way to a new estimate, and etaSlack the share of it an estimate
// may be off by before it drifts at all.
const (
	etaDrift = 2.0
	etaSlack = 0.1
)

// add takes the bytes moved by now, and reports whether that made a new
// sample.
func (s *speedometer) add(now time.Time, bytes int64) bool {
	if s.at.IsZero() {
		s.at, s.bytes = now, bytes
		return false
	}
	dt := now.Sub(s.at).Seconds()
	if dt < speedEvery.Seconds() {
		return false
	}
	s.rate = float64(bytes-s.bytes) / dt
	if s.smooth == 0 {
		s.smooth = s.rate
	} else {
		s.smooth = 0.7*s.smooth + 0.3*s.rate
	}
	if s.shownAt.IsZero() || now.Sub(s.shownAt) >= time.Second {
		s.shown, s.shownAt = s.smooth, now
	}
	s.at, s.bytes = now, bytes
	return true
}

// left is how many seconds the rest of total takes from done, as of
// now, or 0 when that is unknown. It counts down a second a second, and
// drifts toward the estimate at the smoothed speed only when that is
// well off, rather than leaping with every swing of the speed.
func (s *speedometer) left(now time.Time, done, total int64) float64 {
	if s.smooth <= 0 || total <= done {
		return 0
	}
	guess := float64(total-done) / s.smooth
	if s.etaAt.IsZero() {
		s.eta, s.etaAt = guess, now
		return guess
	}
	if dt := now.Sub(s.etaAt).Seconds(); dt > 0 {
		s.eta = max(0, s.eta-dt)
		if math.Abs(guess-s.eta) > etaSlack*s.eta {
			s.eta += (guess - s.eta) * (1 - math.Exp(-dt/etaDrift))
		}
		s.etaAt = now
	}
	return s.eta
}

// pace waits so the bytes a copy moves keep to env.limit a second, n more
// having just gone.
func (r *runner) pace(n int) error {
	if r.env.limit <= 0 {
		return nil
	}
	if r.paceStart.IsZero() {
		r.paceStart = time.Now()
	}
	r.paced += int64(n)
	ahead := time.Duration(float64(r.paced)/r.env.limit*float64(time.Second)) - time.Since(r.paceStart)
	if ahead <= 0 {
		return nil
	}
	select {
	case <-time.After(ahead):
		return nil
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

// bigCopy copies a 2 GB file in the folder showing beside itself, at
// mbps megabytes a second or 150, making the file first when it is not
// there.
func (a *app) bigCopy(mbps string) {
	rate := 150.0
	if mbps != "" {
		v, err := strconv.ParseFloat(mbps, 64)
		if err != nil || v <= 0 {
			a.fail("big-copy takes megabytes a second, such as big-copy:80")
			return
		}
		rate = v
	}
	src := a.ps.Join(a.nav.path, "Big video.mp4")
	_, err := a.fs.Lstat(src)
	switch {
	case errors.Is(err, fs.ErrNotExist) && a.fs.ID() != "":
		a.fail("big-copy makes its big file on the computer's own disk only.")
		return
	case errors.Is(err, fs.ErrNotExist):
		if merr := makeBig(src, 2<<30); merr != nil {
			a.fail("Making the big file: " + merr.Error())
			return
		}
	case err != nil:
		a.fail("Looking for the big file: " + err.Error())
		return
	}
	a.ops.limit = rate * (1 << 20)
	a.startOp(job{kind: OpCopy, srcs: []string{src}, dest: a.nav.path}, "Copying Big video.mp4")
	a.ops.limit = 0
}

// manyCopy copies many files slowly from a folder Holiday photos to a
// folder Backup, beside the folder showing: 60 photos of a few megabytes
// at 40 MB a second, or with small 3,000 small files at 1 MB a second,
// making them first where they are not there.
func (a *app) manyCopy(small bool) {
	if a.fs.ID() != "" {
		a.fail("many-copy makes its files on the computer's own disk only.")
		return
	}
	n, rate, size := 60, 40.0, func(i int) int64 {
		if i%15 == 0 {
			return 120 << 20
		}
		return 200<<10 + int64(i%7)*3<<20
	}
	if small {
		n, rate, size = 3000, 1, func(i int) int64 { return 2<<10 + int64(i%5)*3<<10 }
	}
	from, to := a.ps.Join(a.nav.path, "Holiday photos"), a.ps.Join(a.nav.path, "Backup")
	srcs := make([]string, n)
	for i := range n {
		srcs[i] = a.ps.Join(from, fmt.Sprintf("IMG_%04d.jpg", 4100+i))
	}
	for _, dir := range []string{from, to} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.fail("Making the folders: " + err.Error())
			return
		}
	}
	for i, src := range srcs {
		if _, err := os.Stat(src); err == nil {
			continue
		}
		if err := makeBig(src, size(i)); err != nil {
			a.fail("Making the files: " + err.Error())
			return
		}
	}
	a.ops.limit = rate * (1 << 20)
	a.startOp(job{kind: OpCopy, srcs: srcs, dest: to}, "Copying "+a.what(srcs)+" to "+a.ps.placeName(to))
	a.ops.limit = 0
}

func registerSpeed(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, s OpSpeed, u *gunim.UI) { b.ops.speed(s, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, d OpDone, u *gunim.UI) { b.ops.done(d, u) })
}

// doneBadge holds an operation's icon, and once the operation has gone
// well, a check mark that draws itself in its place.
type doneBadge struct {
	anim.Group
	child gunim.Node
	// pop grows the disc behind the check, stroke draws the check, and
	// since counts the seconds from the cheer.
	pop, stroke *anim.Float
	since       float64
	cheering    bool
}

func newDoneBadge(child gunim.Node) *doneBadge {
	b := &doneBadge{child: child, pop: anim.NewFloat(0), stroke: anim.NewFloat(0)}
	b.Add(b.pop, b.stroke)
	return b
}

// cheer starts the check mark.
func (b *doneBadge) cheer(u *gunim.UI) {
	if b.cheering {
		return
	}
	b.cheering = true
	b.pop.Animate(1, widget.Bounce.Get(u.Theme()))
	b.stroke.Animate(1, anim.Tween{Duration: 420 * time.Millisecond, Ease: anim.EaseInOut})
	u.Invalidate()
}

// glow is how bright the cheer is now, rising and falling once.
func (b *doneBadge) glow() float32 {
	if !b.cheering {
		return 0
	}
	t := min(b.since/0.9, 1)
	return float32(math.Sin(math.Pi * t))
}

// Step implements [gunim.Animator].
func (b *doneBadge) Step(dt time.Duration) bool {
	moving := b.Group.Step(dt)
	if b.cheering && b.since < 1 {
		b.since += dt.Seconds()
		moving = true
	}
	return moving
}

// Children implements [gunim.Composite].
func (b *doneBadge) Children() []gunim.Node { return []gunim.Node{b.child} }

// Layout implements [gunim.Node].
func (b *doneBadge) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	s := kid.Layout(c)
	kid.Place(geom.Point{})
	return s
}

// Paint implements [gunim.Node]: the icon shrinks away as the disc pops
// up in its place and the check draws itself on it.
func (b *doneBadge) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pop := b.pop.Value()
	if gone := min(max(pop*1.6, 0), 1); gone < 1 {
		func() {
			mid := geom.Pt(box.W/2, box.H/2)
			defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1 - gone})()
			defer p.Push(paint.Scale(1-0.3*gone, mid))()
			kids.At(0).Paint(p)
		}()
	}
	if pop <= 0.01 {
		return
	}
	th := f.Theme
	d := badgeDisc * max(pop, 0)
	mid := geom.Pt(box.W/2, box.H/2)
	disc := geom.Rc(mid.X-d/2, mid.Y-d/2, d, d)
	green := Success.Get(th)
	glow := green
	glow.A = uint8(0x90 * b.glow())
	p.ShadowRRect(disc, d/2, paint.Solid(green), paint.Shadow{Blur: 14 * b.glow(), Color: glow})
	// The check: a short stroke down, then a long one up, drawn in turn.
	s := d / 28
	pts := []geom.Point{mid.Add(geom.Pt(-6.5*s, 0.5*s)), mid.Add(geom.Pt(-2*s, 5*s)), mid.Add(geom.Pt(7*s, -5*s))}
	drawTrail(p, pts, min(max(b.stroke.Value(), 0), 1), 2.6*s, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
}

// badgeDisc is the width of the disc a check mark is drawn on.
const badgeDisc = 22

// drawTrail draws the line through pts, as far as share of its length.
func drawTrail(p *paint.Painter, pts []geom.Point, share, width float32, c color.NRGBA) {
	total := float32(0)
	for i := 1; i < len(pts); i++ {
		total += dist(pts[i-1], pts[i])
	}
	left := total * share
	for i := 1; i < len(pts) && left > 0; i++ {
		a, b := pts[i-1], pts[i]
		l := dist(a, b)
		n := min(l, left)
		left -= n
		angle := float32(math.Atan2(float64(b.Y-a.Y), float64(b.X-a.X)))
		func() {
			defer p.Push(paint.Rotate(angle, a))()
			p.RRect(geom.Rc(a.X-width/2, a.Y-width/2, n+width, width), width/2, paint.Solid(c))
		}()
	}
}

func dist(a, b geom.Point) float32 {
	d := b.Sub(a)
	return float32(math.Hypot(float64(d.X), float64(d.Y)))
}

// makeBig makes a file of size bytes at path, without writing them.
func makeBig(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Truncate(size), f.Close())
}
