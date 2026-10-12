package eq

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// bandList is every band's row, one under the other, each with all its
// knobs, its dynamics' too, so a band's compressor is set without
// picking it first; and under them a button to add a band. A row comes
// in as its band does and goes out as it goes, the rows under it
// gliding up into its place.
type bandList struct {
	anim.Group
	g *graph
	// rows are the rows, each with where it is, gliding; a row keeps its
	// band while it leaves.
	rows []*bandRow
	at   []*anim.Float
	add  *widget.Button
	// addAt is where the add button is, gliding.
	addAt *anim.Float
	laid  bool
	size  geom.Size
}

// listRow is how far apart the rows are, and listInset how far in from
// the list's edges they stand.
const (
	listRow   = plugui.KnobH + 10
	listInset = 12
)

func newBandList(g *graph) *bandList {
	l := &bandList{g: g, addAt: anim.NewFloat(0)}
	l.Add(l.addAt)
	// Room for every band, and as many again leaving.
	for range 2 * maxBands {
		r := newBandRow(g, true)
		r.glass = true
		l.rows = append(l.rows, r)
		a := anim.NewFloat(0)
		l.at = append(l.at, a)
		l.Add(a)
	}
	l.add = widget.NewButton("Add band")
	l.add.Icon = icon.Plus
	l.add.Tooltip = "Add a bell where the bands leave the most room"
	l.add.OnClick = func(u *gunim.UI) gunim.Intent {
		g.addBand(bellAt(g.freeFreq()), u)
		return nil
	}
	return l
}

// show shows the bands: each band's row, where it has one, at its place,
// a new band a row of its own, and the rows of bands gone leaving.
func (l *bandList) show(u *gunim.UI) {
	sp := widget.Settle.Get(u.Theme())
	used := map[*bandRow]bool{}
	for i := range l.g.bands {
		b := &l.g.bands[i]
		k := l.rowOf(b.ID, used)
		if k < 0 {
			continue
		}
		r := l.rows[k]
		used[r] = true
		fresh := r.id == 0
		r.show(b, i, u)
		y := float32(i * listRow)
		if fresh || !l.laid {
			l.at[k].Jump(y)
		}
		l.at[k].Animate(y, sp)
	}
	for _, r := range l.rows {
		if !used[r] && r.id != 0 {
			// Its band gone: it leaves where it is.
			r.show(nil, r.place, u)
		}
	}
	y := float32(len(l.g.bands) * listRow)
	if !l.laid {
		l.addAt.Jump(y)
	}
	l.addAt.Animate(y, sp)
	l.add.Disabled = len(l.g.bands) >= maxBands
	l.add.Tooltip = "Add a bell where the bands leave the most room"
	if l.add.Disabled {
		l.add.Tooltip = "An EQ has at most 8 bands"
	}
	l.laid = true
	u.Invalidate()
}

// rowOf returns the place of band id's row: its own, or a row free,
// none of used; -1 where every row is in use.
func (l *bandList) rowOf(id int, used map[*bandRow]bool) int {
	for k, r := range l.rows {
		if r.id == id && !used[r] {
			return k
		}
	}
	for k, r := range l.rows {
		if r.id == 0 && r.in.Value() < 0.01 && !used[r] {
			return k
		}
	}
	return -1
}

// height is how tall the list is: its rows and the add button.
func (l *bandList) height() float32 { return float32(len(l.g.bands)*listRow) + 44 + 2*listInset }

// Children implements [gunim.Composite].
func (l *bandList) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, len(l.rows)+1)
	for _, r := range l.rows {
		kids = append(kids, r)
	}
	return append(kids, l.add)
}

// Layout implements [gunim.Node]: each row where it glides to, the add
// button under them.
func (l *bandList) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	l.size = geom.Sz(c.Max.W, max(l.height(), c.Min.H))
	// Centred, in from the edges at least as far as the rows' cards
	// reach past them; the add button under the rows' numbers.
	w := min(c.Max.W-2*listInset, l.rows[0].width())
	x := (c.Max.W - w) / 2
	for k := range l.rows {
		kids.At(k).Layout(gunim.Tight(geom.Sz(w, plugui.KnobH)))
		kids.At(k).Place(geom.Pt(x, l.at[k].Value()+listInset))
	}
	n := len(l.rows)
	kids.At(n).Layout(gunim.Constraints{Max: geom.Sz(200, 36)})
	kids.At(n).Place(geom.Pt(x, l.addAt.Value()+listInset+4))
	return l.size
}

// Paint implements [gunim.Node].
func (l *bandList) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// bellAt returns a bell at hz, flat, on.
func bellAt(hz float32) audio.Band { return audio.Band{Kind: audio.Bell, Freq: hz, Q: 1, On: true} }
