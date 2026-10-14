package filemanager

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// othersKey is the key of the row of all the types not listed.
const othersKey = "\x00others"

// usageTypes is the list of the types of file Disk usage found, largest
// first. A click on one lights its files in the boxes, and dims the rest;
// a click on it again lights them all.
type usageTypes struct {
	pg   *usagePage
	list *widget.VirtualList
	keys []widget.Key
	at   map[string]int
}

func newUsageTypes(pg *usagePage) *usageTypes {
	t := &usageTypes{pg: pg, at: map[string]int{}}
	t.list = widget.NewVirtualList(func(k widget.Key) gunim.Node { return newTypeRow(t, string(k)) })
	t.list.Estimate = usageRowH
	t.list.Spacing = noGap
	return t
}

// keyOf is the key of type t's row.
func keyOf(t UsageType) string {
	if t.Others {
		return othersKey
	}
	return t.Ext
}

// update shows the types of the state the page took.
func (t *usageTypes) update(u *gunim.UI) {
	t.keys = t.keys[:0]
	clear(t.at)
	for i, ty := range t.pg.state.Types {
		k := keyOf(ty)
		t.at[k] = i
		t.keys = append(t.keys, widget.Key(k))
	}
	t.list.SetKeys(t.keys, u)
}

// Children implements [gunim.Composite].
func (t *usageTypes) Children() []gunim.Node { return []gunim.Node{t.list} }

// Layout implements [gunim.Node].
func (t *usageTypes) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(geom.Sz(c.Max.W, max(0, c.Max.H-usageTitlesH-4))))
	kids.At(0).Place(geom.Pt(0, usageTitlesH))
	return c.Max
}

// Paint implements [gunim.Node]: the card, the titles, and the rows.
func (t *usageTypes) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	p.RRect(geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th), paint.Solid(widget.CardFill.Get(th)))
	face := widget.Font.Get(th)
	small := SmallText.Get(th)
	ink := Caption.Get(th)
	run := t.pg.texts.run(face, "Type", small)
	y := (usageTitlesH-run.Height())/2 + 2
	t.pg.texts.draw(p, th, face, "Type", small, geom.Pt(usagePad, y), ink, box.W/2)
	words := sizeTitle(t.pg.state.Measure)
	w := t.pg.texts.run(face, words, small).Advance
	t.pg.texts.draw(p, th, face, words, small, geom.Pt(box.W-usagePad-w, y), ink, box.W/2)
	p.RRect(geom.Rc(0, usageTitlesH, box.W, 1), 0, paint.Solid(UsageTrack.Get(th)))
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, usageTitlesH, box.W, box.H-usageTitlesH), Opacity: 1, Clip: true})()
		kids.At(0).Paint(p)
	}()
}

// sizeTitle is the title of the column of sizes by m.
func sizeTitle(m Measure) string {
	switch m {
	case MeasureDevice:
		return "On this device"
	case MeasureOnline:
		return "Online only"
	case MeasureAll:
	}
	return "Size"
}

// typeRow is one row of the list of types.
type typeRow struct {
	anim.Group
	t     *usageTypes
	key   string
	hot   *anim.Float
	pick  *anim.Float
	laid  bool
	click widget.Clicker
}

func newTypeRow(t *usageTypes, key string) *typeRow {
	r := &typeRow{t: t, key: key, hot: anim.NewFloat(0), pick: anim.NewFloat(0)}
	r.Add(r.hot, r.pick)
	return r
}

// Layout implements [gunim.Node].
func (r *typeRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	picked := float32(0)
	if pg := r.t.pg; pg.picked && pg.ext == r.key {
		picked = 1
	}
	if !r.laid {
		r.pick.Jump(picked)
		r.laid = true
	}
	r.pick.Animate(picked, widget.Quick.Get(f.Theme))
	return c.Constrain(geom.Sz(c.Max.W, usageRowH))
}

// Paint implements [gunim.Node]: its colour, its name, its share of the
// whole, and its size.
func (r *typeRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	pg := r.t.pg
	i, ok := r.t.at[r.key]
	if !ok {
		return
	}
	ty := pg.state.Types[i]
	band := geom.Rc(4, 1, box.W-8, box.H-2)
	if v := min(max(r.hot.Value(), 0), 1); v > 0.01 {
		p.RRect(band, 6, paint.Solid(scaleA(widget.TreeHover.Get(th), v)))
	}
	if v := min(max(r.pick.Value(), 0), 1); v > 0.01 {
		p.RRect(band, 6, paint.Solid(scaleA(widget.TreeSelected.Get(th), v)))
	}
	face := widget.Font.Get(th)
	size := widget.TextSize.Get(th) * 0.93
	small := SmallText.Get(th)
	mid := box.H / 2
	tint := TintToken(ty.Tint).Get(th)
	d := float32(10)
	p.RRect(geom.Rc(usagePad, mid-d/2, d, d), 3, paint.Solid(tint))
	var by int64
	switch pg.state.Measure {
	case MeasureDevice:
		by = ty.Device
	case MeasureOnline:
		by = ty.Online
	case MeasureAll:
		by = ty.Size
	}
	words := humanBytes(by)
	run := pg.texts.run(face, words, small)
	right := box.W - usagePad
	pg.texts.draw(p, th, face, words, small, geom.Pt(right-run.Advance, (box.H-run.Height())/2), widget.Ink.Get(th), right)
	right -= 72
	// The share of all the files, as a thin bar.
	whole := pg.sizeOf(pg.state.Total)
	if whole > 0 && right > 140 {
		bar := geom.Rc(right-50, mid-2, 50, 4)
		p.RRect(bar, 2, paint.Solid(UsageTrack.Get(th)))
		if v := float32(float64(by) / float64(whole)); v > 0 {
			p.RRect(geom.Rc(bar.Min.X, bar.Min.Y, max(2, 50*min(v, 1)), 4), 2, paint.Solid(tint))
		}
		right -= 60
	}
	name := ty.Label
	if ty.Ext != "" && !ty.Others {
		name = "." + ty.Ext + "  " + ty.Label
	}
	files := plural(ty.Files, "file")
	left := float32(usagePad) + d + 8
	w := pg.texts.draw(p, th, face, name, size, geom.Pt(left, (box.H-pg.texts.run(face, name, size).Height())/2),
		widget.Ink.Get(th), right-left-8)
	if left+w+8 < right-40 {
		pg.texts.draw(p, th, face, files, small, geom.Pt(left+w+8, (box.H-pg.texts.run(face, files, small).Height())/2),
			Caption.Get(th), right-left-w-16)
	}
}

// Handle implements [gunim.Handler]: a click lights the type's files in
// the boxes, and a click again lights all.
func (r *typeRow) Handle(e input.Event, u *gunim.UI) bool {
	pg := r.t.pg
	switch e := e.(type) {
	case input.PointerEnter:
		r.hot.Animate(1, widget.Quick.Get(u.Theme()))
		return false
	case input.PointerLeave:
		r.hot.Animate(0, widget.Settle.Get(u.Theme()))
		return false
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		r.click.Press(e, 0)
		if pg.picked && pg.ext == r.key {
			pg.picked, pg.ext = false, ""
		} else {
			pg.picked, pg.ext = true, r.key
		}
		u.Invalidate()
		return true
	case input.PointerUp:
		r.click.Release(e, 0)
		return true
	}
	return false
}
