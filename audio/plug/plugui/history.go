package plugui

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The history's colours: the sound in, and its loudness. The sound out
// is the theme's accent.
var (
	HistoryIn   = theme.Color("plug.history.in", color.NRGBA{R: 0x9a, G: 0xa3, B: 0xb6, A: 0xc0})
	HistoryLoud = theme.Color("plug.history.loud", color.NRGBA{R: 0xff, G: 0xc8, B: 0x5a, A: 0xff})
)

// The history: a column for each historyFrames frames, historyCols of
// them kept and shownCols across; levels from historyTop down to
// historyBottom.
const (
	historyFrames             = 2 * plug.TraceFrames
	historyCols               = 1024
	shownCols                 = 420
	historyTop, historyBottom = 0, -48
	historyCatchUp            = 8
	maxReductions             = 4
	historyListen             = historyFrames * 16
)

// Reduction is a turning down a History draws, hanging from its top: the
// sum of what the processor keeps by frame of the IDs, as [plug.Tracer]
// traces, in Color; Line draws its edge bright over the dark.
type Reduction struct {
	IDs   []uint32
	Color theme.Token[color.NRGBA]
	Line  bool
}

// Mark is a level a History draws a dashed line at, named: a threshold,
// a ceiling, a target. At returns where, in decibels, and whether it
// shows.
type Mark struct {
	Name  string
	Color theme.Token[color.NRGBA]
	At    func() (db float64, on bool)
}

// historyColumn is a stretch of the sound: its peaks in and out, as
// ratios; how far each reduction turned it down, in decibels; and its
// loudness, the last three seconds', in LUFS.
type historyColumn struct {
	in, out float32
	down    [maxReductions]float32
	loud    float32
}

// History draws a plugin's sound in and out as it plays, scrolling from
// the right, the louder higher, with what the plugin turns down hanging
// from the top, a dashed line at each of its marks, and, measuring
// loudness, the sound out's loudness as a line. It is darkened as the
// [Look] says, for controls over it on glass, its lines bright over the
// dark.
type History struct {
	h          plug.Host
	in, out    plug.Listener
	Reductions []Reduction
	Marks      []Mark
	// meter, measuring loudness, measures the sound out as it is heard,
	// from where it last jumped.
	meter     *audio.LoudnessMeter
	measuring bool
	cols      [historyCols]historyColumn
	// n is how many columns have been made, cur the one being made, of
	// curN frames from curAt, and end the frame after the last heard.
	n          int64
	cur        historyColumn
	curN       int
	curAt, end int64
	// shown is how far the scroll has come, in columns: it moves on
	// with time, between the chunks the sound comes in, so it scrolls
	// smoothly, and runs no further than the columns made.
	shown float64
	rate  int
	size  geom.Size
}

// NewHistory returns the history of the sound h gives, drawing reds and
// marks; loudness measures the sound out's loudness.
func NewHistory(h plug.Host, reds []Reduction, marks []Mark, loudness bool) *History {
	in, out := h.Sound()
	return &History{h: h, in: plug.Listener{Sound: in}, out: plug.Listener{Sound: out}, Reductions: reds[:min(len(reds),
		maxReductions)], Marks: marks, measuring: loudness}
}

// Loudness returns the loudness of the sound out: of the last three
// seconds heard, and of all heard since playing last jumped, in LUFS,
// with whether there is enough of it to tell.
func (hs *History) Loudness() (short, integrated float64, ok bool) {
	if hs.meter == nil {
		return 0, 0, false
	}
	integrated, ok = hs.meter.Integrated()
	return hs.meter.ShortTerm(), integrated, ok
}

// Step implements [gunim.Animator]: the columns of the sound heard
// since, and the scroll moving on.
func (hs *History) Step(dt time.Duration) bool {
	out, rate, playing := hs.out.Hear(historyListen, dt)
	in, _, _ := hs.in.Hear(historyListen, dt)
	if len(out) > 0 {
		end := hs.out.At()
		start := end - int64(len(out)/2)
		switch {
		case start == hs.end && rate == hs.rate:
		case start > hs.end && hs.n > 0 && start-hs.end < 2*int64(rate) && rate == hs.rate:
			// Frames missed, as the window stalled: the history goes on
			// past them.
			hs.skip(start - hs.end)
		default:
			// A jump: the history, and its loudness, start again here.
			hs.n, hs.curN, hs.shown = 0, 0, 0
			hs.curAt = start
			if hs.measuring {
				hs.meter = audio.NewLoudnessMeter(rate)
			}
		}
		hs.end, hs.rate = end, rate
		hs.take(in, out)
	}
	if hs.rate > 0 && playing {
		hs.shown += dt.Seconds() * float64(hs.rate) / historyFrames
	}
	made := float64(hs.n)
	hs.shown = max(min(hs.shown, made), made-historyCatchUp)
	if !playing {
		hs.shown = made
	}
	return playing
}

// take adds the frames heard, in and out, to the columns, and measures
// the sound out's loudness: up to each column's end before the column
// ends, so each column has the loudness to it.
func (hs *History) take(in, out []float32) {
	from := 0
	for i := 0; i+1 < len(out); i += 2 {
		if hs.curN == 0 {
			hs.cur = historyColumn{}
		}
		hs.cur.out = max(hs.cur.out, abs32(out[i]), abs32(out[i+1]))
		if i+1 < len(in) {
			hs.cur.in = max(hs.cur.in, abs32(in[i]), abs32(in[i+1]))
		}
		hs.curN++
		if hs.curN == historyFrames {
			if hs.meter != nil {
				hs.meter.Write(out[from : i+2])
				from = i + 2
			}
			hs.endColumn()
		}
	}
	if hs.meter != nil && from < len(out) {
		hs.meter.Write(out[from:])
	}
}

// endColumn ends the column being made, with how far each reduction
// turned it down, and the loudness so far.
func (hs *History) endColumn() {
	for r, red := range hs.Reductions {
		for _, id := range red.IDs {
			hs.cur.down[r] += hs.traced(id)
		}
	}
	if hs.meter != nil {
		hs.cur.loud = float32(hs.meter.ShortTerm())
	}
	hs.cols[hs.n%historyCols] = hs.cur
	hs.n++
	hs.curN = 0
	hs.curAt += historyFrames
}

// skip moves the history on over n frames missed, as silence.
func (hs *History) skip(n int64) {
	for ; n > 0; n-- {
		if hs.curN == 0 {
			hs.cur = historyColumn{}
		}
		hs.curN++
		if hs.curN == historyFrames {
			hs.endColumn()
		}
	}
}

// traced is how far id turned the column being made down, the most over
// its frames, or 0 where the plugin kept none.
func (hs *History) traced(id uint32) float32 {
	least := float32(0)
	for f := hs.curAt; f < hs.curAt+historyFrames; f += plug.TraceFrames {
		if v, ok := hs.h.Traced(id, f); ok {
			least = min(least, v)
		}
	}
	return least
}

// Columns returns how many columns have been made.
func (hs *History) Columns() int64 { return hs.n }

// Column returns column k's peaks in and out, how far each reduction
// turned it down, and its loudness.
func (hs *History) Column(k int64) (in, out float32, down []float32, loud float32) {
	c := hs.cols[k%historyCols]
	return c.in, c.out, c.down[:len(hs.Reductions)], c.loud
}

// firstShown is the first column drawn: the one at the left edge, or
// before it, as far as the scroll has come, which lags the columns made
// by as many as historyCatchUp.
func (hs *History) firstShown() int64 {
	return max(0, int64(math.Floor(hs.shown))-shownCols-1)
}

// columnAt returns where column k runs across a history w wide, in
// whole pixels, each column starting where the last ended.
func (hs *History) columnAt(k int64, w float32) (x0, x1 float32) {
	colW := w / shownCols
	x0 = float32(math.Round(float64(w - float32(hs.shown-float64(k))*colW)))
	x1 = float32(math.Round(float64(w - float32(hs.shown-float64(k+1))*colW)))
	return x0, x1
}

// Scrolled returns how far the scroll has come, in columns.
func (hs *History) Scrolled() float64 { return hs.shown }

// Layout implements [gunim.Node].
func (hs *History) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	hs.size = c.Max
	return c.Max
}

// y is where on the history a level is, in decibels.
func (hs *History) y(db float32) float32 {
	t := (db - historyBottom) / (historyTop - historyBottom)
	return hs.size.H * (1 - max(0, min(t, 1)))
}

func dbOf(v float64) float64 { return 20 * math.Log10(max(v, 1e-9)) }

// Paint implements [gunim.Node].
func (hs *History) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	radius := widget.CardRadius.Get(th)
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, radius, paint.Solid(audioui.Ground.Get(th)))
	end := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: 1, Clip: true, Radius: radius})
	defer end()
	for db := float32(historyTop - 6); db > historyBottom; db -= 6 {
		y := float32(math.Round(float64(hs.y(db))))
		p.RRect(geom.Rc(0, y, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.06)))
		label := audioui.Shaped(fmt.Sprintf("%.0f", db), 9, false, true)
		label.Paint(p, geom.Pt(box.W-label.Advance-6, y+2), audioui.Faded(ink, 0.35))
	}
	in := HistoryIn.Get(th)
	// each runs fn over the columns shown, with where each is.
	each := func(fn func(c *historyColumn, x0, w float32)) {
		for k := hs.firstShown(); k < hs.n; k++ {
			x0, x1 := hs.columnAt(k, box.W)
			if x0 >= box.W || x1 <= 0 || x1 <= x0 {
				continue
			}
			fn(&hs.cols[k%historyCols], x0, x1-x0)
		}
	}
	// The sound in, faint, and out over it; what the plugin turned down,
	// hanging from the top.
	each(func(c *historyColumn, x0, w float32) {
		if y := hs.y(float32(dbOf(float64(c.in)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, box.H-y), 0, paint.Solid(audioui.Faded(in, 0.35)))
		}
		if y := hs.y(float32(dbOf(float64(c.out)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, box.H-y), 0, paint.Solid(audioui.Faded(accent, 0.22)))
		}
		for r, red := range hs.Reductions {
			if c.down[r] < -0.1 {
				p.RRect(geom.Rc(x0, 0, w, hs.y(c.down[r])), 0, paint.Solid(audioui.Faded(red.Color.Get(th), 0.27)))
			}
		}
	})
	// Darkened under the controls over it, and the lines over the dark:
	// the bright top edges, in and out, between them what the plugin
	// took away; the edges of the reductions; and the loudness.
	Darken(p, whole, 1)
	lines := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: Lines(1)})
	defer lines()
	var loudPrev geom.Point
	loudHas := false
	each(func(c *historyColumn, x0, w float32) {
		if y := hs.y(float32(dbOf(float64(c.in)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, 1.5), 0, paint.Solid(in))
		}
		if y := hs.y(float32(dbOf(float64(c.out)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, 1.5), 0, paint.Solid(audioui.Faded(accent, 0.9)))
		}
		for r, red := range hs.Reductions {
			if red.Line && c.down[r] < -0.1 {
				p.RRect(geom.Rc(x0, hs.y(c.down[r])-1, w, 2), 0, paint.Solid(red.Color.Get(th)))
			}
		}
		if hs.meter != nil && c.loud > historyBottom {
			pt := geom.Pt(x0+w/2, hs.y(c.loud))
			if loudHas {
				audioui.Segment(p, loudPrev, pt, 2, HistoryLoud.Get(th))
			}
			loudPrev, loudHas = pt, true
		} else {
			loudHas = false
		}
	})
	for _, m := range hs.Marks {
		db, on := m.At()
		if !on {
			continue
		}
		c := m.Color.Get(th)
		y := hs.y(float32(db))
		for x := float32(0); x < box.W-40; x += 8 {
			p.RRect(geom.Rc(x, y, 4, 1), 0, paint.Solid(audioui.Faded(c, 0.6)))
		}
		label := audioui.Shaped(m.Name, 9, true, false)
		// Over its line, or under it where there is no room over.
		ly := y - label.Height() - 1
		if ly < 2 {
			ly = y + 3
		}
		label.Paint(p, geom.Pt(6, ly), audioui.Faded(c, 0.8))
	}
}
