package calendar

import (
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a [MiniMonth]: its cells at most, its heading, and its week numbers' column.
const (
	miniCell  = 34
	miniHeadH = 36
	miniWeekW = 26
)

// MiniMonth is a small month for picking a day: the month's name with arrows to the months either side, the days
// in weeks with the weeks' numbers, today ringed, and the days From to To marked, such as the week a calendar shows.
// A click picks a day, and the arrow keys, Page Up and Page Down move the day picked while it has the keyboard.
type MiniMonth struct {
	anim.Group
	// From and To are the first and last days marked.
	From, To time.Time
	// FirstWeekday is the day weeks start on.
	FirstWeekday time.Weekday
	// OnPick runs on the UI goroutine with a day picked. It may act in the window through u, such as for a field to
	// take the day; a non-nil result is sent to the application as the month's intent.
	OnPick func(day time.Time, u *gunim.UI) gunim.Intent

	// month is the month showing, which the arrows move without picking anything.
	month time.Time
	hover int
	// band is the marked days' band while they lie in one week, gliding from week to week; hot is the light under
	// the pointer, gliding from day to day, and hotIn how much of it shows.
	band  *anim.Rect
	laid  bool
	hot   *anim.Rect
	hotIn *anim.Float
	// slide eases the days in from the side the month came from.
	slide *anim.Float
	// side is a day's cell's side at the last layout.
	side float32
	// tap is a press on an arrow or a day, which acts as the pointer lets go.
	tap tap
	// ring runs from 0 to 1 as the month shows that it has the keyboard.
	ring *anim.Float
}

// NewMiniMonth returns a small month showing day's month, with day marked, and weeks starting on Monday.
func NewMiniMonth(day time.Time) *MiniMonth {
	m := &MiniMonth{From: Day(day), To: Day(day), FirstWeekday: time.Monday, month: MonthStart(day), hover: -1,
		slide: anim.NewFloat(0), band: anim.NewRect(geom.Rect{}), hot: anim.NewRect(geom.Rect{}), hotIn: anim.NewFloat(0), ring: anim.NewFloat(0)}
	m.Add(m.slide, m.band, m.hot, m.hotIn, m.ring)
	return m
}

// SetMarked marks the days from and to, and shows from's month when it is not showing already.
func (m *MiniMonth) SetMarked(from, to time.Time, u *gunim.UI) {
	m.From, m.To = Day(from), Day(to)
	if !SameDay(MonthStart(from), m.month) && !SameDay(MonthStart(to), m.month) {
		m.show(MonthStart(from), u)
	}
	u.Invalidate()
}

// show turns to the month starting at month, sliding the days in from the side it lies.
func (m *MiniMonth) show(month time.Time, u *gunim.UI) {
	if month.Before(m.month) {
		m.slide.Jump(-1)
	} else if month.After(m.month) {
		m.slide.Jump(1)
	}
	m.slide.Animate(0, widget.Quick.Get(u.Theme()))
	m.month = month
	u.Invalidate()
}

func (m *MiniMonth) first() time.Time { return WeekStart(m.month, m.FirstWeekday) }
func (m *MiniMonth) day(i int) time.Time {
	return AddDays(m.first(), i)
}

// cell returns day i's box.
func (m *MiniMonth) cell(i int) geom.Rect {
	s := m.side
	return geom.Rc(miniWeekW+float32(i%7)*s, miniHeadH+22+float32(i/7)*s, s, s)
}

// arrows returns the boxes of the arrows to the month before and after, at the right, and from the left edge on
// when the month is narrower than they are.
func (m *MiniMonth) arrows() (back, next geom.Rect) {
	right := max(miniWeekW+7*m.side, 2*28)
	return geom.Rc(right-2*28, 4, 28, 28), geom.Rc(right-28, 4, 28, 28)
}

// Layout implements [gunim.Node]: cells as large as the width and the height allow, up to miniCell. The band over
// the marked days glides to them when they lie in one week.
func (m *MiniMonth) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	side := float32(miniCell)
	if c.Max.W > 0 {
		side = min(side, float32(int((c.Max.W-miniWeekW)/7)))
	}
	if c.Max.H > 0 {
		side = min(side, float32(int((c.Max.H-miniHeadH-22)/6)))
	}
	side = max(side, 0)
	resized := side != m.side
	m.side = side
	if b, ok := m.oneWeekBand(); ok {
		if !m.laid || resized || m.band.Target().Empty() {
			m.band.Jump(b)
		} else if m.band.Target() != b {
			m.band.Animate(b, widget.Settle.Get(f.Theme))
		}
	} else {
		m.band.Jump(geom.Rect{})
	}
	m.laid = true
	return c.Constrain(geom.Sz(miniWeekW+7*m.side, miniHeadH+22+6*m.side))
}

// oneWeekBand returns the band over the marked days, and false unless they show and lie in one week.
func (m *MiniMonth) oneWeekBand() (geom.Rect, bool) {
	first, last := -1, -1
	for i := range 42 {
		if day := m.day(i); !day.Before(m.From) && !day.After(m.To) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || first/7 != last/7 {
		return geom.Rect{}, false
	}
	return m.bandOf(first, last), true
}

// bandOf returns the band over cells first to last, in one week.
func (m *MiniMonth) bandOf(first, last int) geom.Rect {
	a, b := m.cell(first), m.cell(last)
	return geom.Rect{Min: geom.Pt(a.Min.X+2, a.Min.Y+3), Max: geom.Pt(b.Max.X-2, b.Max.Y-3)}
}

// Paint implements [gunim.Node].
func (m *MiniMonth) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	// The ring is drawn last, over the rest.
	defer widget.GroupRing(p, geom.Rect{Max: box.Point()}, 0, m.ring.Value(), th)
	ink, faint := widget.Ink.Get(th), widget.PaletteHint.Get(th)
	regular, bold := widget.Font.Get(th), widget.BoldFont.Get(th)
	now := f.Now.In(m.month.Location())
	back, next := m.arrows()
	// The month's name keeps to one line left of the arrows, cut short with an ellipsis.
	if room := back.Min.X - 4 - 8; room > 0 {
		title := bold.Layout(m.month.Format("January 2006"), text.Style{Size: widget.TextSize.Get(th), MaxLines: 1}, room)
		title.Paint(p, geom.Pt(8, 4+(28-title.Size.H)/2), ink)
	}
	widget.PaintIcon(p, th, icon.ChevronLeft, back.Inset(geom.Uniform(6)), ink)
	widget.PaintIcon(p, th, icon.ChevronRight, next.Inset(geom.Uniform(6)), ink)
	size := EventText.Get(th)
	for dd := range 7 {
		n := regular.Shape(m.day(dd).Format("Mon")[:2], size)
		c := m.cell(dd)
		n.Paint(p, geom.Pt(c.Center().X-n.Advance/2, miniHeadH), faint)
	}
	grid := geom.Rc(0, miniHeadH+20, box.W, 6*m.side+4)
	defer p.Layer(paint.LayerOpts{Bounds: grid, Opacity: 1, Clip: true})()
	defer p.Push(paint.Translate(geom.Pt(m.slide.Value()*40, 0)))()
	for w := range 6 {
		_, wk := m.day(w * 7).ISOWeek()
		n := regular.Shape(strconv.Itoa(wk), 11)
		c := m.cell(w * 7)
		n.Paint(p, geom.Pt(miniWeekW-6-n.Advance, c.Center().Y-n.Height()/2), faint)
	}
	// The marked days join into one band along each week.
	marked := hotFill(th)
	if b := m.band.Value(); !b.Empty() {
		p.RRect(b, b.Size().H/2, paint.Solid(marked))
	}
	if h := min(max(m.hotIn.Value(), 0), 1); h > 0.01 {
		hot := widget.WindowButtonHot.Get(th)
		hot.A = uint8(float32(hot.A) * h)
		r := m.hot.Value()
		p.RRect(r, r.Size().H/2, paint.Solid(hot))
	}
	for w := range 6 {
		if !m.band.Target().Empty() {
			break
		}
		first, last := -1, -1
		for dd := range 7 {
			if day := m.day(w*7 + dd); !day.Before(m.From) && !day.After(m.To) {
				if first < 0 {
					first = dd
				}
				last = dd
			}
		}
		if first >= 0 {
			band := m.bandOf(w*7+first, w*7+last)
			p.RRect(band, band.Size().H/2, paint.Solid(marked))
		}
	}
	for i := range 42 {
		day, c := m.day(i), m.cell(i)
		dayInk := ink
		if day.Month() != m.month.Month() {
			dayInk = faint
		}
		n := regular.Shape(strconv.Itoa(day.Day()), size)
		if SameDay(day, now) {
			p.RRect(c.Inset(geom.Uniform(3)), 13, paint.Solid(widget.Accent.Get(th)))
			dayInk = widget.ButtonStrongInk.Get(th)
		}
		n.Paint(p, geom.Pt(c.Center().X-n.Advance/2, c.Center().Y-n.Height()/2), dayInk)
	}
}

// at returns the day under pt, or -1.
func (m *MiniMonth) at(pt geom.Point) int {
	for i := range 42 {
		if m.cell(i).Contains(pt) {
			return i
		}
	}
	return -1
}

// lightCell moves the light under the pointer to cell i, gliding from the one before, or fades it for -1.
func (m *MiniMonth) lightCell(i int, u *gunim.UI) {
	th := u.Theme()
	if i >= 0 {
		r := m.cell(i).Inset(geom.Uniform(3))
		if m.hover < 0 || m.hotIn.Target() == 0 {
			m.hot.Jump(r)
		} else {
			m.hot.Animate(r, widget.Quick.Get(th))
		}
		m.hotIn.Animate(1, widget.Quick.Get(th))
	} else {
		m.hotIn.Animate(0, widget.Settle.Get(th))
	}
	m.hover = i
	u.Invalidate()
}

// pick picks day, turning to its month.
func (m *MiniMonth) pick(day time.Time, u *gunim.UI) {
	if !SameDay(MonthStart(day), m.month) {
		m.show(MonthStart(day), u)
	}
	u.Cue(gunim.CueSelect, m)
	if m.OnPick != nil {
		send(u, m, m.OnPick(day, u))
	}
}

// Handle implements [gunim.Handler].
func (m *MiniMonth) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if i := m.at(e.Pos); i != m.hover {
			m.lightCell(i, u)
		}
	case input.PointerLeave:
		m.lightCell(-1, u)
	case input.FocusRing:
		to := float32(0)
		if ringShown(e) {
			to = 1
		}
		m.ring.Animate(to, widget.RingFade.Get(u.Theme()))
	case input.FocusLost:
		m.ring.Animate(0, widget.RingFade.Get(u.Theme()))
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		// Each click on an arrow turns a month; a day is picked once, by the first click of a double click.
		back, next := m.arrows()
		switch i := m.at(e.Pos); {
		case back.Contains(e.Pos):
			m.tap.press(back, func() { m.show(m.month.AddDate(0, -1, 0), u) })
		case next.Contains(e.Pos):
			m.tap.press(next, func() { m.show(m.month.AddDate(0, 1, 0), u) })
		case i >= 0 && e.Clicks <= 1:
			day := m.day(i)
			m.tap.press(m.cell(i), func() { m.pick(day, u) })
		}
		return true
	case input.PointerUp:
		return e.Button == input.ButtonPrimary && m.tap.release(e.Pos)
	case input.KeyPress:
		by := map[input.Key]int{input.KeyLeft: -1, input.KeyRight: 1, input.KeyUp: -7, input.KeyDown: 7}[e.Key]
		switch {
		case by != 0:
			m.pick(AddDays(m.From, by), u)
		case e.Key == input.KeyPageUp:
			m.pick(m.From.AddDate(0, -1, 0), u)
		case e.Key == input.KeyPageDown:
			m.pick(m.From.AddDate(0, 1, 0), u)
		case e.Key == input.KeyEnter:
			m.pick(m.From, u)
		default:
			return false
		}
		return true
	}
	return false
}

// Focusable implements [gunim.Focusable].
func (m *MiniMonth) Focusable() bool { return true }

// Cursor implements [gunim.CursorShaper].
func (m *MiniMonth) Cursor(pt geom.Point) input.Cursor {
	back, next := m.arrows()
	if m.at(pt) >= 0 || back.Contains(pt) || next.Contains(pt) {
		return input.CursorHand
	}
	return input.CursorArrow
}
