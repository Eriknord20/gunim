package filemanager

import (
	"image/color"
	"math"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// UsageInkDark is the text on a light box, as UsageInk is on a dark one.
var UsageInkDark = theme.Foreground("files.usage.ink.dark", color.NRGBA{R: 0x14, G: 0x17, B: 0x1d, A: 0xff})

// usageMapPad is the room round the boxes, inside their card.
const usageMapPad = 6

// usageMap shows the folder in focus as boxes, each as large as what it
// holds, a folder's items inside its box. The boxes glide to their new
// places as more is read, and zoom as the focus goes into a folder or out
// of it. A click selects a box, a double click goes into its folder, and
// the secondary button opens a menu.
type usageMap struct {
	anim.Group
	pg   *usagePage
	menu *widget.ContextMenu
	// boxes are where each item goes, and from where it comes from as
	// morph runs from 0 to 1; fresh marks an item that fades in.
	boxes []mapBox
	from  []geom.Rect
	fresh []bool
	morph *anim.Float
	// laid is the state, the size and the measure the boxes were laid out
	// for, and area where they go.
	laidGen  int
	laidSeq  int
	laidSize geom.Size
	laidBy   Measure
	area     geom.Rect
	// keys are the items' keys, as the boxes were laid out for them.
	keys []string
	// focus is the folder the boxes showed, which a new focus zooms from.
	focus string
	// zoom says the next layout zooms into the box of the focus (1) or out
	// of it (-1).
	zoom  int
	click widget.Clicker
	// hot is the box under the pointer, or -1, and at where the pointer is.
	hot int
	at  geom.Point
	// menuKey is the item the menu is for, and menuActs what its items do.
	menuKey  string
	menuActs []string
	texts    texts
}

func newUsageMap(pg *usagePage) *usageMap {
	m := &usageMap{pg: pg, morph: anim.NewFloat(1), hot: -1}
	m.Add(m.morph)
	m.menu = widget.NewContextMenu(m, nil)
	m.menu.Prepare = func(at geom.Point, u *gunim.UI) bool {
		i := m.boxAt(at)
		if i < 0 {
			return false
		}
		key := pg.state.Items[i].Key
		pg.pickBox(key, u)
		items, acts := pg.menuItems(key)
		m.menuKey, m.menuActs = key, acts
		m.menu.SetItems(items)
		return len(items) > 0
	}
	m.menu.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		pg.menuPicked(m.menuKey, m.menuActs, i, u)
		return nil
	}
	return m
}

// update takes the state the page took; was is the state before. A new
// focus zooms.
func (m *usageMap) update(was Usage, u *gunim.UI) {
	st := m.pg.state
	if was.Seq == st.Seq && was.Focus != st.Focus {
		m.zoom = 1
		if strings.HasPrefix(was.Focus, st.Focus) && len(was.Focus) > len(st.Focus) {
			m.zoom = -1
		}
	}
	u.Invalidate()
}

// Layout implements [gunim.Node]: the boxes are laid out again for a new
// state or a new size, and glide from where they were.
func (m *usageMap) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	box := c.Max
	st := m.pg.state
	if st.Gen != m.laidGen || st.Seq != m.laidSeq || box != m.laidSize || st.Measure != m.laidBy {
		m.relayout(box, f.Theme)
	}
	return box
}

// relayout lays the boxes out for the state showing in a map of size box.
func (m *usageMap) relayout(box geom.Size, th *theme.Live) {
	st := m.pg.state
	// Where each item is drawn now, by key, which it glides from.
	drawn := make(map[string]geom.Rect, len(m.boxes))
	if st.Seq == m.laidSeq {
		for i, key := range m.keys {
			if i < len(m.boxes) {
				drawn[key] = m.drawnRect(i)
			}
		}
	}
	resized := box != m.laidSize && m.laidSize != (geom.Size{})
	m.laidGen, m.laidSeq, m.laidSize, m.laidBy = st.Gen, st.Seq, box, st.Measure
	m.area = geom.Rc(usageMapPad, usageMapPad, max(0, box.W-2*usageMapPad), max(0, box.H-2*usageMapPad))
	m.boxes = layoutMap(st.Items, st.Measure, m.area)
	m.from = make([]geom.Rect, len(m.boxes))
	m.fresh = make([]bool, len(m.boxes))
	var zoomFrom, zoomTo geom.Rect
	switch {
	case m.zoom > 0:
		zoomFrom = drawn[st.Focus]
	case m.zoom < 0:
		if i, ok := m.pg.index[m.focus]; ok {
			zoomTo = m.boxes[i].r
		}
	}
	for i, b := range m.boxes {
		key := st.Items[i].Key
		switch {
		case m.zoom > 0 && !zoomFrom.Empty():
			m.from[i] = mapRect(b.r, m.area, zoomFrom)
		case m.zoom < 0 && !zoomTo.Empty():
			m.from[i] = mapRect(b.r, zoomTo, m.area)
		default:
			r, ok := drawn[key]
			m.from[i], m.fresh[i] = r, !ok
		}
	}
	m.focus = st.Focus
	m.hot = -1
	m.morph.Jump(0)
	m.morph.Animate(1, Page.Get(th))
	if resized {
		// A drag of the divider moves the boxes with it, at once.
		m.morph.Jump(1)
	}
	m.zoom = 0
	m.keys = m.keys[:0]
	for _, it := range st.Items {
		m.keys = append(m.keys, it.Key)
	}
}

// mapRect is r, in the space of from, mapped to the same place in to.
func mapRect(r, from, to geom.Rect) geom.Rect {
	fs, ts := from.Size(), to.Size()
	if fs.W <= 0 || fs.H <= 0 {
		return to
	}
	sx, sy := ts.W/fs.W, ts.H/fs.H
	return geom.Rect{
		Min: geom.Pt(to.Min.X+(r.Min.X-from.Min.X)*sx, to.Min.Y+(r.Min.Y-from.Min.Y)*sy),
		Max: geom.Pt(to.Min.X+(r.Max.X-from.Min.X)*sx, to.Min.Y+(r.Max.Y-from.Min.Y)*sy),
	}
}

// drawnRect is where box i is drawn as the boxes glide.
func (m *usageMap) drawnRect(i int) geom.Rect {
	b := m.boxes[i].r
	v := min(max(m.morph.Value(), 0), 1)
	if v >= 1 || i >= len(m.from) || m.fresh[i] || m.from[i].Empty() {
		return b
	}
	f := m.from[i]
	lerp := func(a, b float32) float32 { return a + (b-a)*v }
	return geom.Rect{Min: geom.Pt(lerp(f.Min.X, b.Min.X), lerp(f.Min.Y, b.Min.Y)),
		Max: geom.Pt(lerp(f.Max.X, b.Max.X), lerp(f.Max.Y, b.Max.Y))}
}

// boxAt is the deepest box at pt, or -1.
func (m *usageMap) boxAt(pt geom.Point) int {
	for i := len(m.boxes) - 1; i >= 0; i-- {
		if r := m.drawnRect(i); !r.Empty() && r.Contains(pt) {
			return i
		}
	}
	return -1
}

// lit reports whether item it shows lit, with a type picked in the list of
// types: a file of that type.
func (m *usageMap) lit(it UsageItem) bool {
	pg := m.pg
	if !pg.picked {
		return true
	}
	if it.Dir || it.Rest > 0 {
		return false
	}
	ext := typeExt(it.Name)
	if pg.ext == othersKey {
		return !pg.others[ext]
	}
	return ext == pg.ext
}

// Paint implements [gunim.Node]: the card, then each box over the box of
// its folder.
func (m *usageMap) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	pg := m.pg
	st := pg.state
	card := geom.Rect{Max: box.Point()}
	p.RRect(card, widget.CardRadius.Get(th), paint.Solid(widget.CardFill.Get(th)))
	if len(m.boxes) == 0 || len(st.Items) != len(m.boxes) {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: m.area, Opacity: 1, Clip: true})()
	v := min(max(m.morph.Value(), 0), 1)
	bold := widget.BoldFont.Get(th)
	small := SmallText.Get(th)
	edge := UsageEdge.Get(th)
	sel := -1
	if i, ok := pg.index[pg.sel]; ok {
		sel = i
	}
	hot := m.hot
	if i, ok := pg.index[pg.hot]; ok && hot < 0 {
		hot = i
	}
	for i, b := range m.boxes {
		r := m.drawnRect(i)
		if r.Empty() || r.Size().W < 1 || r.Size().H < 1 {
			continue
		}
		it := st.Items[i]
		opacity := float32(1)
		if m.fresh[i] {
			opacity = v
		}
		if opacity <= 0.01 {
			continue
		}
		inner := m.pg.kids[i]
		radius := min(UsageRadius.Get(th), r.Size().W/4, r.Size().H/4)
		if b.depth == 0 {
			continue
		}
		func() {
			if opacity < 1 {
				defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: opacity})()
			}
			if len(inner) > 0 && !it.More {
				// A folder: its box, and its name along the top.
				p.RRect(r, radius, paint.Solid(UsageBox.Get(th)))
				p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: edge})
				if b.head {
					words := it.Name + "  " + humanBytes(pg.sizeOf(it))
					run := m.texts.run(bold, words, small)
					m.texts.draw(p, th, bold, words, small, geom.Pt(r.Min.X+6, r.Min.Y+(mapHead-run.Height())/2+1),
						Faint.Get(th), r.Size().W-12)
				}
				return
			}
			m.paintLeaf(p, th, r, radius, it, small)
		}()
	}
	// The box under the pointer, and the one selected, over the rest.
	if hot >= 0 && hot < len(m.boxes) && m.boxes[hot].depth > 0 {
		r := m.drawnRect(hot)
		p.RRect(r, min(UsageRadius.Get(th), r.Size().W/4, r.Size().H/4), paint.Solid(UsageHot.Get(th)))
	}
	if sel >= 0 && sel < len(m.boxes) && m.boxes[sel].depth > 0 {
		r := m.drawnRect(sel)
		p.RRectStroke(r.Inset(geom.Uniform(-1)), min(UsageRadius.Get(th)+1, r.Size().W/4+1, r.Size().H/4+1), paint.Fill{},
			paint.Stroke{Width: 2, Color: widget.Accent.Get(th)})
	}
}

// paintLeaf draws the box of an item shown whole: a file, a folder whose
// items were not sent, or the small items of a folder taken together.
func (m *usageMap) paintLeaf(p *paint.Painter, th *theme.Live, r geom.Rect, radius float32, it UsageItem, small float32) {
	pg := m.pg
	fills := m.fills(th, it)
	lit := m.lit(it)
	inset := r.Inset(geom.Uniform(0.5))
	if len(fills) == 1 {
		p.RRect(inset, radius, cushion(inset, fills[0].c, lit))
	} else {
		// How a cloud provider keeps what it holds, in bands along its
		// longer side.
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: inset, Opacity: 1, Clip: true})()
			whole := int64(0)
			for _, f := range fills {
				whole += f.n
			}
			at := float32(0)
			wide := inset.Size().W >= inset.Size().H
			long := inset.Size().H
			if wide {
				long = inset.Size().W
			}
			for _, f := range fills {
				part := long * float32(float64(f.n)/float64(max(whole, 1)))
				var band geom.Rect
				if wide {
					band = geom.Rc(inset.Min.X+at, inset.Min.Y, part, inset.Size().H)
				} else {
					band = geom.Rc(inset.Min.X, inset.Min.Y+at, inset.Size().W, part)
				}
				p.RRect(band, 0, cushion(band, f.c, lit))
				at += part
			}
		}()
	}
	p.RRectStroke(r, radius, paint.Fill{}, paint.Stroke{Width: 1, Color: UsageEdge.Get(th)})
	// Its name and size, where there is room.
	if r.Size().W < 56 || r.Size().H < 22 {
		return
	}
	ink := UsageInk.Get(th)
	if dark := UsageInkDark.Get(th); contrast(fills[0].c, dark) > contrast(fills[0].c, ink) {
		ink = dark
	}
	if !lit {
		ink.A /= 3
	}
	fc := widget.Font.Get(th)
	name := it.Name
	run := m.texts.run(fc, name, small)
	y := r.Min.Y + 5
	m.texts.draw(p, th, fc, name, small, geom.Pt(r.Min.X+6, y), ink, r.Size().W-12)
	if r.Size().H >= 40 {
		ink.A = uint8(int(ink.A) * 3 / 4)
		m.texts.draw(p, th, fc, humanBytes(pg.sizeOf(it)), small, geom.Pt(r.Min.X+6, y+run.Height()+1), ink, r.Size().W-12)
	}
}

// usageFill is a colour, and the bytes it stands for.
type usageFill struct {
	c color.NRGBA
	n int64
}

// fills are the colours of the box of it: one, or with the boxes coloured
// by cloud, for what it holds, one for each way a cloud provider keeps
// some of it.
func (m *usageMap) fills(th *theme.Live, it UsageItem) []usageFill {
	pg := m.pg
	if pg.state.Colour == ColourCloud && (it.Dir || it.Rest > 0) {
		var out []usageFill
		plain := it.Size - it.Online - it.Local - it.Pinned
		for _, f := range []struct {
			n   int64
			ink theme.Token[color.NRGBA]
		}{{plain, UsagePlain}, {it.Local, UsageLocal}, {it.Pinned, CloudPinnedInk}, {it.Online, UsageOnline}} {
			if pg.state.Measure == MeasureDevice && f.ink.Key() == UsageOnline.Key() {
				continue
			}
			if pg.state.Measure == MeasureOnline && f.ink.Key() != UsageOnline.Key() {
				continue
			}
			if f.n > 0 {
				out = append(out, usageFill{f.ink.Get(th), f.n})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	c := pg.markInk(th, it)
	if it.Rest > 0 && pg.state.Colour == ColourType {
		c = TintToken(TintOther).Get(th)
	}
	return []usageFill{{c, 1}}
}

// cushion is the fill of a box of colour c: lighter at its top left, as a
// cushion lit from there, and faint where it is not lit.
func cushion(r geom.Rect, c color.NRGBA, lit bool) paint.Fill {
	if !lit {
		c.A /= 4
	}
	top := mix(c, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: c.A}, 0.22)
	bottom := mix(c, color.NRGBA{A: c.A}, 0.12)
	return paint.Fill{Gradient: &paint.Gradient{From: r.Min, To: r.Max, Start: top, End: bottom}}
}

// mix is a blended toward b by t.
func mix(a, b color.NRGBA, t float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

// contrast is how far apart a and b stand, as the ratio of their
// luminances that WCAG reads.
func contrast(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// luminance is the relative luminance of c, as WCAG defines it.
func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// Handle implements [gunim.Handler]: the pointer lights the box under it,
// a click selects it, and a double click goes into its folder.
func (m *usageMap) Handle(e input.Event, u *gunim.UI) bool {
	pg := m.pg
	switch e := e.(type) {
	case input.PointerMove:
		m.at = e.Pos
		m.setHot(m.boxAt(e.Pos), u)
	case input.PointerLeave:
		m.setHot(-1, u)
		// A new layout forgets the box under the pointer, not the item.
		m.pg.setHot("", u)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		m.click.Press(e, 0)
		i := m.boxAt(e.Pos)
		if i < 0 {
			return true
		}
		it := pg.state.Items[i]
		if e.Clicks == 2 {
			// Into the deepest folder under the pointer.
			for j := i; j > 0; j = int(pg.state.Items[j].Parent) {
				if d := pg.state.Items[j]; d.Dir && d.Rest == 0 {
					pg.focusOn(d.Key, u)
					break
				}
			}
			return true
		}
		if m.boxes[i].depth > 0 {
			pg.pickBox(it.Key, u)
		}
		return true
	case input.PointerUp:
		m.click.Release(e, 0)
		return true
	}
	return false
}

// setHot lights box i, or with -1 none.
func (m *usageMap) setHot(i int, u *gunim.UI) {
	if i == m.hot {
		return
	}
	m.hot = i
	key := ""
	if i >= 0 && i < len(m.pg.state.Items) && m.boxes[i].depth > 0 {
		key = m.pg.state.Items[i].Key
	}
	m.pg.setHot(key, u)
	u.Invalidate()
}
