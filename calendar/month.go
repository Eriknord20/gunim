package calendar

import (
	"image/color"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a [Month].
const (
	monthHeadH = 28
	chipH      = 20
	chipGap    = 2
	dayNumH    = 26
)

// Month shows a month as six weeks of days, each with its events: those lasting whole days as bars across the
// days they cover, and the rest as a line each with its time. A day with more than it has room for says how many
// more.
//
// A click on a day's number, or on how many more it has, picks the day, and a click on free room in a day begins an
// event there. Dragging an event to another day moves it there, keeping its time of day; it glides from day to day
// as it goes. Events fade in as they come and out as they go, and lift under the pointer.
type Month struct {
	anim.Group
	// Month is any day of the month shown, and FirstWeekday the day weeks start on.
	Month        time.Time
	FirstWeekday time.Weekday
	// HideWeekends leaves Saturdays and Sundays out.
	HideWeekends bool
	// The callbacks below run on the UI goroutine and may act in the window through u. A non-nil result is sent to
	// the application as the view's intent.
	//
	// OnMore, when set, runs in place of OnDay for a click on how many more events a day has, with the day and the
	// box of the line saying so, such as to list them beside it.
	OnMore func(day time.Time, box geom.Rect, u *gunim.UI) gunim.Intent
	// OnDay runs on a click on a day's number, such as to show that day alone.
	OnDay func(day time.Time, u *gunim.UI) gunim.Intent
	// OnCreate runs on a click on free room in a day, with the day and its box in the month's space, to make an
	// event that day, or to ask for a title beside it.
	OnCreate func(day time.Time, box geom.Rect, u *gunim.UI) gunim.Intent
	// OnEdit runs on a double click on an event, such as to open it in an editor.
	OnEdit func(id string, u *gunim.UI) gunim.Intent
	// OnDelete runs on Delete on the event chosen by the keys.
	OnDelete func(id string, u *gunim.UI) gunim.Intent
	// OnStep runs on scrolling, which has nothing else to move in a month, to step to the month either side, by -1
	// or 1.
	OnStep func(by int, u *gunim.UI) gunim.Intent
	// Busy, when set, is asked before a press on free room begins an event, as [Days.Busy] is.
	Busy func() bool
	// OnChange runs with an event dragged to another day.
	OnChange func(id string, start, end time.Time, u *gunim.UI) gunim.Intent
	// OnOpen runs when the user clicks an event, or presses Enter on the one chosen, with its box in the month's
	// space.
	OnOpen func(id string, box geom.Rect, u *gunim.UI) gunim.Intent

	events []Event
	// idx finds the events of each week.
	idx *index
	// laidOut holds the events as laid out at the last layout, and laidFor what they were laid out for. While that
	// stays the same, so do they.
	laidOut []chip
	laidFor monthKey
	tip     widget.PartTip
	swipe   swipe
	box     geom.Size
	// slide carries the days in from the side they came from, as the month steps.
	slide *anim.Float
	laid  bool
	// sprites are the events as drawn, by key, and order the order they are drawn in; was is the month and size
	// at the last layout.
	sprites map[string]*sprite
	order   []string
	more    []chip
	was     struct {
		month time.Time
		box   geom.Size
	}
	// days is the first day shown, kept for the month and the first day of the week it is for.
	days struct {
		month, first time.Time
		from         time.Weekday
	}
	selected string
	hover    string
	drag     *monthDrag
	// tap is a press on a day or how many more it has, which acts as the pointer lets go.
	tap tap
	// ring runs from 0 to 1 as the month shows that it has the keyboard.
	ring  *anim.Float
	texts map[textKey]text.Paragraph
}

// monthDrag is an event taken hold of: where the pointer took it, and the day it is over.
type monthDrag struct {
	id    string
	fixed bool
	from  geom.Point
	box   geom.Rect
	moved bool
	to    int
	start int
}

// NewMonth returns the month holding day, with weeks starting on Monday.
func NewMonth(day time.Time) *Month {
	m := &Month{Month: MonthStart(day), FirstWeekday: time.Monday, slide: anim.NewFloat(0), ring: anim.NewFloat(0),
		sprites: map[string]*sprite{}}
	m.Add(m.slide, m.ring)
	return m
}

// Step implements [gunim.Animator], stepping the events as drawn too.
func (m *Month) Step(dt time.Duration) bool {
	moving := m.Group.Step(dt)
	for k, s := range m.sprites {
		if s.step(dt) {
			moving = true
		}
		if s.gone && !s.in.Active() {
			delete(m.sprites, k)
		}
	}
	return moving
}

// Selected returns the ID of the event chosen, or an empty ID.
func (m *Month) Selected() string { return m.selected }

// SetSelected marks the event id as chosen, lifting it, or marks none for an empty id, and sends no intent. With a nil
// u the lifts jump.
func (m *Month) SetSelected(id string, u *gunim.UI) {
	m.selected = id
	if u == nil {
		m.aimLifts(nil)
		for _, s := range m.sprites {
			s.lift.Jump(s.lift.Target())
		}
		return
	}
	m.aimLifts(u.Theme())
	u.Invalidate()
}

// aimLifts lifts the event under the pointer, the one chosen and the one dragged, and lets the rest down.
func (m *Month) aimLifts(th *theme.Live) {
	for _, s := range m.sprites {
		up := !s.gone && (s.e.ID == m.hover || s.e.ID == m.selected || (m.drag != nil && m.drag.moved && s.e.ID == m.drag.id))
		s.lift.Animate(map[bool]float32{false: 0, true: 1}[up], widget.Quick.Get(th))
	}
}

// SetMonth shows the month holding day, sliding it in from its side.
func (m *Month) SetMonth(day time.Time, u *gunim.UI) {
	month := MonthStart(day)
	if m.laid && !month.Equal(m.Month) {
		m.slide.Jump(map[bool]float32{false: -1, true: 1}[month.After(m.Month)])
		m.slide.Animate(0, widget.Quick.Get(u.Theme()))
	}
	m.Month = month
	u.Invalidate()
}

// Focusable implements [gunim.Focusable]: the keys move between the events.
func (m *Month) Focusable() bool { return true }

// stops returns the events the keys move between, those showing.
func (m *Month) stops() []stop {
	var out []stop
	for _, k := range m.order {
		s := m.sprites[k]
		if s == nil || s.gone {
			continue
		}
		c := m.cellAt(s.rect.Target().Center())
		out = append(out, stop{id: s.e.ID, day: c, start: s.e.Start, box: s.rect.Target()})
	}
	return out
}

// EventAt returns the ID of the event at pt, in the month's space, and false where there is none.
func (m *Month) EventAt(pt geom.Point) (string, bool) {
	ch, ok := m.chipAt(pt)
	if !ok || ch.more > 0 {
		return "", false
	}
	return ch.e.ID, true
}

// EventBox returns where the event id first shows, in the month's space, and false when it does not show.
func (m *Month) EventBox(id string) (geom.Rect, bool) {
	for _, k := range m.order {
		if s := m.sprites[k]; s != nil && !s.gone && s.e.ID == id {
			return s.rect.Target(), true
		}
	}
	return geom.Rect{}, false
}

// SetEvents shows events, which may hold events outside the weeks shown.
func (m *Month) SetEvents(events []Event, u *gunim.UI) {
	m.events, m.idx = events, nil
	m.texts = nil
	u.Invalidate()
}

// first returns the first day shown: the start of the week holding the month's first day.
func (m *Month) first() time.Time {
	if !m.days.month.Equal(m.Month) || m.days.from != m.FirstWeekday || m.days.first.IsZero() {
		m.days.month, m.days.from = m.Month, m.FirstWeekday
		m.days.first = WeekStart(MonthStart(m.Month), m.FirstWeekday)
	}
	return m.days.first
}

// day returns the midnight of cell i, counting from the top left.
func (m *Month) day(i int) time.Time { return AddDays(m.first(), i) }

// shows reports whether the day dd of each week, from 0 to 6, shows.
func (m *Month) shows(dd int) bool {
	// Each week starts on FirstWeekday.
	wd := (m.FirstWeekday + time.Weekday(dd)) % 7
	return !m.HideWeekends || (wd != time.Saturday && wd != time.Sunday)
}

// cols returns how many days of each week show.
func (m *Month) cols() int {
	n := 0
	for dd := range 7 {
		if m.shows(dd) {
			n++
		}
	}
	return max(n, 1)
}

// colOf returns the column day dd of each week shows in.
func (m *Month) colOf(dd int) int {
	c := 0
	for k := range dd {
		if m.shows(k) {
			c++
		}
	}
	return c
}

func (m *Month) cellW() float32 { return m.box.W / float32(m.cols()) }
func (m *Month) cellH() float32 { return (m.box.H - monthHeadH) / 6 }

// cell returns cell i's box, which has no width for a day that does not show.
func (m *Month) cell(i int) geom.Rect {
	w := m.cellW()
	if !m.shows(i % 7) {
		w = 0
	}
	return geom.Rc(float32(m.colOf(i%7))*m.cellW(), monthHeadH+float32(i/7)*m.cellH(), w, m.cellH())
}

// cellAt returns the cell under pt, or -1.
func (m *Month) cellAt(pt geom.Point) int {
	if pt.Y < monthHeadH || pt.X < 0 || pt.X >= m.box.W || pt.Y >= m.box.H {
		return -1
	}
	col := min(int(pt.X/m.cellW()), m.cols()-1)
	row := min(int((pt.Y-monthHeadH)/m.cellH()), 5)
	for dd := range 7 {
		if m.shows(dd) && m.colOf(dd) == col {
			return dd + 7*row
		}
	}
	return -1
}

// Layout implements [gunim.Node].
func (m *Month) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	m.box = c.Max
	stepped := m.laid && !m.Month.Equal(m.was.month)
	jump := !m.laid || m.box != m.was.box || stepped
	m.place(f.Theme, jump, stepped)
	m.laid = true
	m.was.month, m.was.box = m.Month, m.box
	return m.box
}

// place sends each event's sprite to where it goes, as [Days.place] does.
func (m *Month) place(th *theme.Live, jump, drop bool) {
	seen := map[string]bool{}
	m.order, m.more = m.order[:0], m.more[:0]
	count := map[string]int{}
	if k := m.key(); k != m.laidFor || m.laidOut == nil {
		m.laidOut, m.laidFor = m.chips(), k
	}
	for _, ch := range m.laidOut {
		if ch.more > 0 {
			m.more = append(m.more, ch)
			continue
		}
		// An event keeps its key as it moves from day to day, so it glides there.
		key := ch.e.ID
		if ch.bar {
			key += "#bar"
		}
		count[key]++
		key += "@" + strconv.Itoa(count[key])
		seen[key] = true
		m.order = append(m.order, key)
		s, ok := m.sprites[key]
		switch {
		case !ok:
			s = newSprite(ch.box, ch.e)
			if jump {
				s.in.Jump(1)
			} else {
				s.in.Animate(1, widget.Bounce.Get(th))
			}
			m.sprites[key] = s
		case s.gone:
			s.gone = false
			s.in.Animate(1, widget.Bounce.Get(th))
			s.rect.Jump(ch.box)
		case jump:
			s.rect.Jump(ch.box)
		case s.rect.Target() != ch.box:
			motion := widget.Settle.Get(th)
			if m.drag != nil && m.drag.id == ch.e.ID {
				motion = widget.Quick.Get(th)
			}
			s.rect.Animate(ch.box, motion)
		}
		s.show(ch.e, th)
		s.long = ch.bar
	}
	for k, s := range m.sprites {
		if seen[k] {
			continue
		}
		if drop {
			delete(m.sprites, k)
			continue
		}
		if !s.gone {
			s.gone = true
			s.in.Animate(0, widget.Quick.Get(th))
		}
		m.order = append(m.order, k)
	}
	m.aimLifts(th)
}

// monthKey is what a month's events are laid out for: the events, the weeks, the size and the drag.
type monthKey struct {
	idx      *index
	first    time.Time
	hide     bool
	box      geom.Size
	drag     monthDrag
	dragging bool
}

// key returns what the month's events would be laid out for now.
func (m *Month) key() monthKey {
	m.idx = indexOf(m.idx, m.events)
	k := monthKey{idx: m.idx, first: m.first(), hide: m.HideWeekends, box: m.box}
	if m.drag != nil {
		k.drag, k.dragging = *m.drag, true
	}
	return k
}

// chip is an event laid out in the month: its box, and whether it is a bar across whole days.
type chip struct {
	e   Event
	box geom.Rect
	bar bool
	// more is set on the line that says how many more a day has, for the day in cell.
	more, cell int
}

// chips lays out the month's events. Each week's bars take rows from the top of its days, each in the first row
// free across its days, and each day's timed events go in the rows left under them. A day with more than fits keeps
// its last row to say how many more; where a cell has no room for a row, its number's line says so.
func (m *Month) chips() []chip {
	var out []chip
	rows := max(int((m.cellH()-dayNumH-4)/(chipH+chipGap)), 0)
	for week := range 6 {
		evs := weekOrder(m.shown(m.day(week*7), m.day(week*7+7)))
		// keep[dd] says day dd keeps its last row to say how many more. A day that turns out to have more than fits
		// keeps it, and the week is laid out again, until each day with more has its last row free.
		var keep [7]bool
		var placed []chip
		var hidden [7]int
		for {
			placed, hidden = m.weekChips(week, evs, rows, keep)
			again := false
			for dd := range 7 {
				if hidden[dd] > 0 && !keep[dd] && rows > 0 {
					keep[dd], again = true, true
				}
			}
			if !again {
				break
			}
		}
		out = append(out, placed...)
		for dd := range 7 {
			if hidden[dd] == 0 || !m.shows(dd) {
				continue
			}
			cell := week*7 + dd
			c := m.cell(cell)
			box := geom.Rc(c.Min.X+3, c.Min.Y+dayNumH+float32(rows-1)*(chipH+chipGap), c.Size().W-6, chipH)
			if rows == 0 {
				// No room for a row: the right half of the number's line.
				box = geom.Rc(c.Center().X, c.Min.Y, c.Size().W/2-3, min(dayNumH, c.Size().H))
			}
			out = append(out, chip{more: hidden[dd], cell: cell, box: box})
		}
	}
	return out
}

// weekOrder returns evs in the order a week takes them in: those of whole days first, then by when they start, and
// otherwise in the order given.
func weekOrder(evs []Event) []Event {
	long := make([]bool, len(evs))
	at := make([]int, len(evs))
	for i, e := range evs {
		long[i], at[i] = e.long(), i
	}
	slices.SortFunc(at, func(a, b int) int {
		if long[a] != long[b] {
			if long[a] {
				return -1
			}
			return 1
		}
		if c := evs[a].Start.Compare(evs[b].Start); c != 0 {
			return c
		}
		return a - b
	})
	out := make([]Event, len(evs))
	for k, i := range at {
		out[k] = evs[i]
	}
	return out
}

// weekChips lays out the events evs in week, given rows to a day, each in the first row free across its days. A
// day in keep holds its last row free. It returns the chips, and how many events of each day found no row.
func (m *Month) weekChips(week int, evs []Event, rows int, keep [7]bool) (out []chip, hidden [7]int) {
	// taken[row][day] says the row is used on that day.
	taken := make([][7]bool, rows)
	place := func(e Event, first, last int, bar bool) {
		for r := range rows {
			free := true
			for dd := first; dd <= last; dd++ {
				if taken[r][dd] || (keep[dd] && r == rows-1) {
					free = false
				}
			}
			if !free {
				continue
			}
			for dd := first; dd <= last; dd++ {
				taken[r][dd] = true
			}
			// A bar runs from the first day of its days that shows to the last.
			for first < last && !m.shows(first) {
				first++
			}
			for last > first && !m.shows(last) {
				last--
			}
			if !m.shows(first) {
				return
			}
			c0, c1 := m.cell(week*7+first), m.cell(week*7+last)
			y := c0.Min.Y + dayNumH + float32(r)*(chipH+chipGap)
			out = append(out, chip{e: e, bar: bar, box: geom.Rc(c0.Min.X+3, y, c1.Max.X-c0.Min.X-6, chipH)})
			return
		}
		for dd := first; dd <= last; dd++ {
			hidden[dd]++
		}
	}
	var days [8]time.Time
	for dd := range days {
		days[dd] = m.day(week*7 + dd)
	}
	for _, e := range evs {
		first, last := -1, -1
		for dd := range 7 {
			if overlaps(e, days[dd], days[dd+1]) {
				if first < 0 {
					first = dd
				}
				last = dd
			}
		}
		if first < 0 {
			continue
		}
		if e.long() {
			place(e, first, last, true)
			continue
		}
		for dd := first; dd <= last; dd++ {
			if m.shows(dd) {
				place(e, dd, dd, false)
			}
		}
	}
	return out, hidden
}

// shown returns the events that take up some of the days between from and to, in the order given, with the one
// being dragged on the day it is over.
func (m *Month) shown(from, to time.Time) []Event {
	m.idx = indexOf(m.idx, m.events)
	at := m.idx.within(from, to)
	g := m.drag
	dragging := g != nil && g.moved && !g.fixed
	if dragging {
		if i, ok := m.idx.find(g.id); ok {
			// It may have come from another week.
			at = append(at, i)
			slices.Sort(at)
			at = slices.Compact(at)
		}
	}
	out := make([]Event, 0, len(at))
	for _, i := range at {
		e := m.events[i]
		if dragging && e.ID == g.id {
			e.Start, e.End = m.moved(e, g)
		}
		if overlaps(e, from, to) {
			out = append(out, e)
		}
	}
	return out
}

// moved returns where e goes when g drags it from its day to the day under the pointer.
func (m *Month) moved(e Event, g *monthDrag) (start, end time.Time) {
	days := g.to - g.start
	s := AddDays(Day(e.Start), days).Add(dayOffset(e.Start))
	return s, s.Add(e.End.Sub(e.Start))
}

// Paint implements [gunim.Node].
func (m *Month) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	// The ring is drawn last, over the rest.
	defer widget.GroupRing(p, geom.Rect{Max: box.Point()}, 0, m.ring.Value(), th)
	line := widget.MenuBorder.Get(th)
	faint := widget.PaletteHint.Get(th)
	ink := widget.Ink.Get(th)
	now := f.Now.In(m.Month.Location())
	regular, bold := widget.Font.Get(th), widget.BoldFont.Get(th)
	size := EventText.Get(th)
	for dd := range 7 {
		if !m.shows(dd) {
			continue
		}
		name := regular.Shape(m.day(dd).Format("Mon"), size)
		name.Paint(p, geom.Pt(m.cell(dd).Min.X+8, (monthHeadH-name.Height())/2), faint)
	}
	for i := range 42 {
		if !m.shows(i % 7) {
			continue
		}
		c := m.cell(i)
		day := m.day(i)
		if day.Month() != m.Month.Month() {
			p.RRect(c, 0, paint.Solid(OtherMonthFill.Get(th)))
		} else if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			p.RRect(c, 0, paint.Solid(WeekendFill.Get(th)))
		}
		p.RRect(geom.Rc(c.Min.X, c.Min.Y, c.Size().W, 1), 0, paint.Solid(line))
		if m.colOf(i%7) > 0 {
			p.RRect(geom.Rc(c.Min.X, c.Min.Y, 1, c.Size().H), 0, paint.Solid(line))
		}
		label := strconv.Itoa(day.Day())
		if day.Day() == 1 {
			label = day.Format("2 Jan")
		}
		num := bold.Shape(label, size)
		numInk := ink
		if day.Month() != m.Month.Month() {
			numInk = faint
		}
		at := geom.Pt(c.Min.X+8, c.Min.Y+(dayNumH-num.Height())/2)
		if SameDay(day, now) {
			pill := geom.Rc(at.X-5, c.Min.Y+3, max(num.Advance+10, dayNumH-6), dayNumH-6)
			p.RRect(pill, (dayNumH-6)/2, paint.Solid(widget.Accent.Get(th)))
			numInk = widget.ButtonStrongInk.Get(th)
			at.X = pill.Center().X - num.Advance/2
		}
		num.Paint(p, at, numInk)
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
	moving(p, box, m.slide.Value(), func() {
		for _, k := range m.order {
			if s := m.sprites[k]; s != nil {
				m.paintSprite(p, th, s)
			}
		}
		for _, ch := range m.more {
			m.paintChip(p, th, ch)
		}
	})
	f.RedrawAt(AddDays(Day(now), 1))
}

// paintChip draws how many more a day has.
func (m *Month) paintChip(p *paint.Painter, th *theme.Live, ch chip) {
	size := EventText.Get(th)
	r := ch.box
	label := strconv.Itoa(ch.more) + " more"
	if r.Size().H < chipH {
		// On the line of the day's number.
		label = "+" + strconv.Itoa(ch.more)
	}
	t := m.paragraph(th, label, r.Size().W-8, true, size)
	t.Paint(p, geom.Pt(r.Min.X+6, r.Center().Y-t.Size.H/2), widget.PaletteHint.Get(th))
}

// paintSprite draws an event as it moves: a bar filled with its colour, or a line with a dot of it and its time,
// faded while it comes in or goes out, and lifted under the pointer, while dragged, or chosen.
func (m *Month) paintSprite(p *paint.Painter, th *theme.Live, s *sprite) {
	in := min(max(s.in.Value(), 0), 1)
	if in <= 0.01 {
		return
	}
	lift := min(max(s.lift.Value(), 0), 1)
	r := s.rect.Value()
	if in < 0.99 {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-10)), Opacity: in})()
	}
	e := s.e
	size := EventText.Get(th)
	wait := min(max(s.wait.Value(), 0), 1)
	fill, bar, ink, _ := eventColors(th, e, lift, wait)
	round := MonthEventRadius.Get(th)
	if s.long {
		shadow := paint.Shadow{}
		if lift > 0.01 {
			shadow = paint.Shadow{Color: color.NRGBA{A: uint8(0x50 * lift)}, Blur: 8 * lift, Offset: geom.Pt(0, 2*lift)}
		}
		p.ShadowRRect(r, round, paint.Solid(fill), shadow)
		if wait > 0.01 {
			stripes(p, r, round, bar, wait)
		}
		t := m.paragraph(th, e.Title, r.Size().W-12, true, size)
		t.Paint(p, geom.Pt(r.Min.X+7, r.Center().Y-t.Size.H/2), ink)
	} else {
		if lift > 0.01 {
			hot := widget.MenuHot.Get(th)
			hot.A = uint8(float32(hot.A) * lift)
			p.ShadowRRect(r, round, paint.Solid(hot), paint.Shadow{Color: color.NRGBA{A: uint8(0x40 * lift)}, Blur: 8 * lift,
				Offset: geom.Pt(0, 2*lift)})
		}
		dot := geom.Rc(r.Min.X+5, r.Center().Y-4, 8, 8)
		// A ring while it waits for an answer, filling in once it has one.
		solid := bar
		solid.A = uint8(float32(solid.A) * (1 - wait))
		p.RRectStroke(dot, 4, paint.Solid(solid), paint.Stroke{Width: 1.5, Color: bar})
		t := m.paragraph(th, clock(e.Start.Hour(), e.Start.Minute())+" "+e.Title, r.Size().W-20, false, size)
		t.Paint(p, geom.Pt(r.Min.X+18, r.Center().Y-t.Size.H/2), ink)
	}
	if e.ID == m.selected {
		p.RRectStroke(r.Inset(geom.Uniform(-1.5)), round+1.5, paint.Fill{}, paint.Stroke{Width: 2, Color: widget.Accent.Get(th)})
	}
}

// paragraph lays s out on one line w wide, keeping it for the next frame.
func (m *Month) paragraph(th *theme.Live, s string, w float32, bold bool, size float32) text.Paragraph {
	k := textKey{s: s, w: w, lines: 1, bold: bold, fontSize: size}
	if t, ok := m.texts[k]; ok {
		return t
	}
	face := widget.Font.Get(th)
	if bold {
		face = widget.BoldFont.Get(th)
	}
	t := face.Layout(s, text.Style{Size: size, MaxLines: 1}, max(w, 1))
	if m.texts == nil {
		m.texts = map[textKey]text.Paragraph{}
	}
	m.texts[k] = t
	return t
}

// chipAt returns the chip under pt: an event, or how many more a day has.
func (m *Month) chipAt(pt geom.Point) (chip, bool) {
	for _, ch := range m.more {
		if ch.box.Contains(pt) {
			return ch, true
		}
	}
	for k := len(m.order) - 1; k >= 0; k-- {
		if s := m.sprites[m.order[k]]; s != nil && !s.gone && s.rect.Value().Contains(pt) {
			return chip{e: s.e, bar: s.long, box: s.rect.Value()}, true
		}
	}
	return chip{}, false
}

// Handle implements [gunim.Handler].
func (m *Month) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		tip := ""
		if ch, ok := m.chipAt(e.Pos); ok && ch.more == 0 && m.drag == nil {
			line := clock(ch.e.Start.Hour(), ch.e.Start.Minute()) + " " + ch.e.Title
			if widget.Font.Get(u.Theme()).Shape(line, EventText.Get(u.Theme())).Advance > ch.box.Size().W-20 {
				tip = eventTip(ch.e)
			}
		}
		m.tip.Handle(e, u, m, tip)
	case input.PointerLeave, input.PointerDown:
		m.tip.Handle(e, u, m, "")
	case input.Scroll:
		m.tip.Handle(e, u, m, "")
		if m.OnStep == nil {
			return false
		}
		if by := m.swipe.step(e, true); by != 0 {
			send(u, m, m.OnStep(by, u))
		}
		return true
	}
	switch e := e.(type) {
	case input.PointerMove:
		if g := m.drag; g != nil {
			if !g.moved && math.Hypot(float64(e.Pos.X-g.from.X), float64(e.Pos.Y-g.from.Y)) < dragSlack {
				return true
			}
			if !g.moved {
				g.moved = true
				m.aimLifts(u.Theme())
			}
			if c := m.cellAt(e.Pos); c >= 0 {
				g.to = c
			}
			u.Invalidate()
			return true
		}
		id := ""
		if ch, ok := m.chipAt(e.Pos); ok && ch.more == 0 {
			id = ch.e.ID
		}
		if id != m.hover {
			m.hover = id
			m.aimLifts(u.Theme())
		}
	case input.PointerLeave:
		if m.hover != "" && m.drag == nil {
			m.hover = ""
			m.aimLifts(u.Theme())
		}
	case input.KeyPress:
		if e.Key == input.KeyEscape && m.drag != nil {
			m.drag = nil
			m.aimLifts(u.Theme())
			return true
		}
		return eventKeys(e, u, m, m.stops(), m.selected, func(id string) { m.SetSelected(id, u) }, m.OnOpen, m.OnDelete)
	case input.FocusRing:
		to := float32(0)
		if ringShown(e) {
			to = 1
		}
		m.ring.Animate(to, widget.RingFade.Get(u.Theme()))
	case input.FocusLost:
		m.ring.Animate(0, widget.RingFade.Get(u.Theme()))
	case input.FocusGained:
		if e.Keyed && m.selected == "" {
			if s, ok := nextStop(m.stops(), "", input.KeyDown); ok {
				m.SetSelected(s.id, u)
			}
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		m.press(e, u)
		return true
	case input.PointerUp:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if m.tap.release(e.Pos) {
			return true
		}
		g := m.drag
		if g == nil {
			return false
		}
		m.drag = nil
		m.aimLifts(u.Theme())
		u.Invalidate()
		if !g.moved || g.to == g.start {
			if m.OnOpen != nil {
				send(u, m, m.OnOpen(g.id, g.box, u))
			}
			return true
		}
		if g.fixed || g.to == g.start || m.OnChange == nil {
			return true
		}
		for _, ev := range m.events {
			if ev.ID == g.id {
				s, en := m.moved(ev, g)
				send(u, m, m.OnChange(g.id, s, en, u))
			}
		}
		return true
	}
	return false
}

// press takes hold of a chip, or picks a day by its number or its count of more, or begins an event on a day, as the
// pointer lets go. The second press of a double click edits an event, and starts nothing the first one started
// already.
func (m *Month) press(e input.PointerDown, u *gunim.UI) {
	if ch, ok := m.chipAt(e.Pos); ok {
		if e.Clicks > 1 {
			if ch.more == 0 && m.OnEdit != nil && !ch.e.Fixed {
				send(u, m, m.OnEdit(ch.e.ID, u))
			}
			return
		}
		if ch.more > 0 {
			m.tap.press(ch.box, func() {
				switch {
				case m.OnMore != nil:
					send(u, m, m.OnMore(m.day(ch.cell), ch.box, u))
				case m.OnDay != nil:
					pickDay(u, m, m.OnDay, m.day(ch.cell))
				}
			})
			return
		}
		c := m.cellAt(e.Pos)
		m.drag = &monthDrag{id: ch.e.ID, fixed: ch.e.Fixed, from: e.Pos, box: ch.box, start: c, to: c}
		return
	}
	c := m.cellAt(e.Pos)
	if c < 0 || e.Clicks > 1 {
		return
	}
	box := m.cell(c)
	number := geom.Rc(box.Min.X, box.Min.Y, box.Size().W, min(dayNumH, box.Size().H))
	switch {
	case number.Contains(e.Pos) && m.OnDay != nil:
		m.tap.press(number, func() { pickDay(u, m, m.OnDay, m.day(c)) })
	case m.Busy != nil && m.Busy():
	case m.OnCreate != nil:
		m.tap.press(box, func() { send(u, m, m.OnCreate(m.day(c), box, u)) })
	}
}

// Cursor implements [gunim.CursorShaper].
func (m *Month) Cursor(pt geom.Point) input.Cursor {
	if m.drag != nil && m.drag.moved && !m.drag.fixed {
		return input.CursorMove
	}
	if _, ok := m.chipAt(pt); ok {
		return input.CursorHand
	}
	if c := m.cellAt(pt); c >= 0 && pt.Y < m.cell(c).Min.Y+dayNumH {
		return input.CursorHand
	}
	return input.CursorArrow
}
