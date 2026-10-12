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
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Sizes of a [Days] grid.
const (
	gutterW   = 56
	headerH   = 52
	longRowH  = 24
	dragSlack = 5
	edgeGrab  = 7
	minEventH = 20
	dayMargin = 8
)

// Days shows days side by side, by the hour: a column for each day, under a heading with its name and date, and a
// row above the hours for the events that last whole days. Events that overlap in time share their column.
//
// Everything moves: an event glides to where it goes, fades in as it arrives and out as it leaves, lifts under the
// pointer, and morphs into place as the grid goes from one day to a week. Stepping to other days slides them in.
//
// The pointer draws out a new event on free time, or on the row of whole days, moves an event by dragging it, and
// changes its length by dragging its bottom edge. Times snap to Snap. A drag that ends where it began is a click,
// which opens the event. The grid shows a change at once, and for up to a second while the application catches up.
type Days struct {
	anim.Group
	// First is the first day shown, and Count how many days: 1 for a day, 7 for a week.
	First time.Time
	Count int
	// Snap is how finely times snap under the pointer; zero is 15 minutes.
	Snap time.Duration
	// WorkDay is the working day, from its start to its end; the hours outside it are shaded. Zero shades none.
	WorkDay [2]time.Duration
	// The callbacks below run on the UI goroutine and may act in the window through u. A non-nil result is sent to
	// the application as the view's intent.
	//
	// OnDay runs on a click on a day's heading, such as to show that day alone.
	OnDay func(day time.Time, u *gunim.UI) gunim.Intent
	// OnCreate runs with a span drawn out on free time and its box in the grid's space, to make an event there.
	// Returning an intent lets the drawn event fade; returning nil keeps it until [Days.ClearGhost], for a callback
	// that asks for a title beside it.
	OnCreate func(start, end time.Time, allDay bool, box geom.Rect, u *gunim.UI) gunim.Intent
	// OnChange runs with an event moved, or made longer or shorter.
	OnChange func(id string, start, end time.Time, u *gunim.UI) gunim.Intent
	// OnOpen runs when the user clicks an event, or presses Enter on the one chosen, with the event's box in the
	// grid's space, such as to show a card about it there.
	OnOpen func(id string, box geom.Rect, u *gunim.UI) gunim.Intent
	// OnEdit runs on a double click on an event, such as to open it in an editor.
	OnEdit func(id string, u *gunim.UI) gunim.Intent
	// OnDelete runs on Delete on the event chosen by the keys.
	OnDelete func(id string, u *gunim.UI) gunim.Intent
	// OnStep runs on scrolling sideways, or with Shift held, to step to the days either side, by -1 or 1.
	OnStep func(by int, u *gunim.UI) gunim.Intent
	// Busy, when set, is asked before a press on free time begins an event. True lets the press do nothing more,
	// such as one that only closed a card about an event.
	Busy func() bool

	events []Event
	// idx finds the events on the days shown, and vis holds them as [Days.shown] gave them at the last layout.
	idx *index
	vis []Event
	// laidTo holds where the events went at the last layout, and laidFor what it was worked out for. While that stays
	// the same, so do they.
	laidTo  []target
	laidFor daysKey
	// held are events the user changed, shown where they left them until the application agrees.
	held map[string]heldEvent
	// holds counts the changes held, so each tells the layout it has something new.
	holds int
	// sprites are the events as drawn, by key, and order the order they are drawn in.
	sprites map[string]*sprite
	order   []string
	// selected is the event marked as chosen, such as the one a card is open on.
	selected string

	scroll *anim.Float
	// slide carries the days in from the side they came from, as the grid steps to other days.
	slide *anim.Float
	// ghost is the event being drawn out, and ghostIn how much of it shows.
	ghost   *anim.Rect
	ghostIn *anim.Float
	laid    bool
	box     geom.Size
	// was is what the grid showed at the last layout, to tell a step from a change of view.
	was struct {
		box   geom.Size
		first time.Time
		count int
	}
	// hourH is how tall an hour was at the last layout.
	hourH float32
	// topHour is the hour [Days.ScrollToHour] asked for before the first layout, while asked is set.
	topHour float32
	asked   bool
	// heads fades the headings in as the grid changes how many days it shows.
	heads *anim.Float
	// th is the theme at the last layout, and pointer where the pointer last was, for a drag to go on and the event
	// under it to lift as the grid scrolls under it. over says the pointer is over the grid.
	th      *theme.Live
	pointer geom.Point
	over    bool
	// longRows is how many rows the whole-day events take on screen, long the rows of them shown, and longMore how
	// many more each day has than they show, said on a row under them. longAll is how many rows they would take, and
	// longOpen says the user opened the rows to show more of them.
	longRows int
	long     [][]longPlace
	longMore []int
	longAll  int
	longOpen bool
	hover    string
	tip      widget.PartTip
	swipe    swipe
	// tap is a press on a heading or a button over the rows of whole days, which acts as the pointer lets go.
	tap  tap
	drag *dayDrag
	// ring runs from 0 to 1 as the grid shows that it has the keyboard.
	ring *anim.Float
	// ghostHeld is the drag that drew out an event being named, whose ghost stays until ClearGhost.
	ghostHeld *dayDrag
	texts     map[textKey]text.Paragraph
}

// heldEvent is where the user left an event, and when.
type heldEvent struct {
	start, end time.Time
	at         time.Time
}

// sprite is an event as the grid draws it: its box, which glides to where the event goes, how far it has faded in
// or out, and how much it is lifted under the pointer. A timed event's box is in the hours' own space, with
// midnight at zero and no scroll, so scrolling does not set it moving.
type sprite struct {
	e           Event
	long        bool
	top, bottom bool
	rect        *anim.Rect
	in, lift    *anim.Float
	// wait runs to 1 while the event waits for the user's answer, and back to 0 once it has one.
	wait *anim.Float
	gone bool
}

func (s *sprite) step(dt time.Duration) bool {
	a, b, c, w := s.rect.Step(dt), s.in.Step(dt), s.lift.Step(dt), s.wait.Step(dt)
	return a || b || c || w
}

// newSprite returns a sprite for e at box.
func newSprite(box geom.Rect, e Event) *sprite {
	s := &sprite{rect: anim.NewRect(box), in: anim.NewFloat(0), lift: anim.NewFloat(0), wait: anim.NewFloat(0)}
	if e.Faint {
		s.wait.Jump(1)
	}
	return s
}

// show sets the event s draws, fading to or from the look of one waiting for an answer.
func (s *sprite) show(e Event, th *theme.Live) {
	s.e = e
	s.wait.Animate(map[bool]float32{false: 0, true: 1}[e.Faint], widget.Settle.Get(th))
}

// dayDrag is a drag on the grid: drawing out a new event, or moving an event or changing its length.
type dayDrag struct {
	kind  dragKind
	id    string
	fixed bool
	from  geom.Point
	moved bool
	// grab is how far into the event the pointer took hold of it; anchor is where a new event was begun.
	grab       time.Duration
	anchor     time.Time
	start, end time.Time
	allDay     bool
	box        geom.Rect
}

type dragKind uint8

const (
	dragCreate dragKind = iota
	dragMove
	dragResize
	dragResizeTop
)

// textKey names a paragraph laid out for an event.
type textKey struct {
	s        string
	w        float32
	lines    int
	bold     bool
	fontSize float32
}

// NewDays returns a grid of count days from first.
func NewDays(first time.Time, count int) *Days {
	d := &Days{First: Day(first), Count: count, scroll: anim.NewFloat(0), slide: anim.NewFloat(0), ring: anim.NewFloat(0),
		ghost: anim.NewRect(geom.Rect{}), ghostIn: anim.NewFloat(0), held: map[string]heldEvent{},
		sprites: map[string]*sprite{}, heads: anim.NewFloat(1)}
	d.Add(d.scroll, d.slide, d.ghost, d.ghostIn, d.heads, d.ring)
	return d
}

// Step implements [gunim.Animator], stepping the events as drawn too.
func (d *Days) Step(dt time.Duration) bool {
	moving := d.Group.Step(dt)
	if d.edgeScroll(dt) {
		moving = true
	}
	if d.scroll.Active() && d.over && d.drag == nil && d.th != nil {
		// The events move under a pointer at rest.
		d.hoverAt(d.pointer, d.th)
	}
	for k, s := range d.sprites {
		if s.step(dt) {
			moving = true
		}
		if s.gone && !s.in.Active() {
			delete(d.sprites, k)
		}
	}
	return moving
}

// edgeScroll scrolls the grid while a drag holds the pointer near the top or bottom of the hours, as
// [gunim.EdgeSpeed] says, and carries the drag along. It reports whether it scrolled.
func (d *Days) edgeScroll(dt time.Duration) bool {
	if d.drag == nil || d.drag.allDay || !d.drag.moved {
		return false
	}
	v := gunim.EdgeSpeed(d.pointer.Y, d.bodyTop(), d.box.H)
	if v == 0 {
		return false
	}
	to := d.clampScroll(d.scrollY() + v*float32(dt.Seconds()))
	if to == d.scrollY() {
		return false
	}
	d.scroll.Jump(to)
	d.dragTo(d.pointer, d.th)
	return true
}

// SetEvents shows events, which may hold events outside the days shown.
func (d *Days) SetEvents(events []Event, u *gunim.UI) {
	d.events, d.idx = events, nil
	for id, h := range d.held {
		if time.Since(h.at) > time.Second {
			delete(d.held, id)
			continue
		}
		for _, e := range events {
			if e.ID == id && e.Start.Equal(h.start) && e.End.Equal(h.end) {
				delete(d.held, id)
			}
		}
	}
	d.texts = nil
	u.Invalidate()
}

// SetDays shows count days from first. Stepping to other days of the same count slides them in from their side;
// changing the count morphs each event into its new place.
func (d *Days) SetDays(first time.Time, count int, u *gunim.UI) {
	first, count = Day(first), max(count, 1)
	if d.laid && count == d.Count && !first.Equal(d.First) {
		d.slide.Jump(map[bool]float32{false: -1, true: 1}[first.After(d.First)])
		d.slide.Animate(0, widget.Settle.Get(u.Theme()))
	}
	if d.laid && count != d.Count {
		d.heads.Jump(0)
		d.heads.Animate(1, widget.Settle.Get(u.Theme()))
	}
	d.First, d.Count = first, count
	u.Invalidate()
}

// Selected returns the ID of the event chosen, or an empty ID.
func (d *Days) Selected() string { return d.selected }

// SetSelected marks the event id as chosen, lifting it and ringing it in the accent, or marks none for an empty id,
// and sends no intent. With a nil u the lifts jump.
func (d *Days) SetSelected(id string, u *gunim.UI) {
	d.selected = id
	if u == nil {
		d.aimLifts(nil)
		for _, s := range d.sprites {
			s.lift.Jump(s.lift.Target())
		}
		return
	}
	d.aimLifts(u.Theme())
	u.Invalidate()
}

// EventBox returns where the event id shows, in the grid's space, and false when it does not show.
func (d *Days) EventBox(id string) (geom.Rect, bool) {
	for _, k := range d.order {
		if s := d.sprites[k]; s != nil && !s.gone && s.e.ID == id {
			r := s.rect.Target()
			if !s.long {
				r = r.Add(geom.Pt(0, d.bodyTop()-d.scroll.Target()))
			}
			return r, true
		}
	}
	return geom.Rect{}, false
}

// Focusable implements [gunim.Focusable]: the keys move between the events.
func (d *Days) Focusable() bool { return true }

// stops returns the events the keys move between, those showing.
func (d *Days) stops() []stop {
	var out []stop
	for _, k := range d.order {
		s := d.sprites[k]
		if s == nil || s.gone {
			continue
		}
		day := 0
		for i := range d.Count {
			if s.e.covers(d.day(i)) {
				day = i
				break
			}
		}
		out = append(out, stop{id: s.e.ID, day: day, start: s.e.Start, box: d.onScreen(s, s.rect.Target())})
	}
	return out
}

// choose chooses the event id by the keys, and scrolls it into sight.
func (d *Days) choose(id string, u *gunim.UI) {
	d.SetSelected(id, u)
	if id != "" {
		d.Reveal(id, u)
	}
}

// EventAt returns the ID of the event at pt, in the grid's space, and false where there is none.
func (d *Days) EventAt(pt geom.Point) (string, bool) {
	e, _, _, ok := d.eventUnder(pt)
	return e.ID, ok
}

// Reveal scrolls the grid so the event id shows, with an hour above it, unless it shows already.
func (d *Days) Reveal(id string, u *gunim.UI) {
	for _, e := range d.shown() {
		if e.ID != id || e.long() {
			continue
		}
		y0 := d.hourY(dayOffset(e.Start))
		y1 := d.hourY(dayOffset(e.End))
		view := d.box.H - d.bodyTop()
		if y0 >= d.scroll.Target() && y1 <= d.scroll.Target()+view {
			return
		}
		d.scroll.Animate(d.clampScroll(y0-d.hour()), widget.Settle.Get(u.Theme()))
		return
	}
}

// moving draws what draw draws slid and faded as the days step.
func moving(p *paint.Painter, box geom.Size, slide float32, draw func()) {
	if abs(slide) < 0.001 {
		draw()
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1 - min(abs(slide), 1)*0.8})()
	defer p.Push(paint.Translate(geom.Pt(slide*72, 0)))()
	draw()
}

// ScrollToHour scrolls the grid so the hour h, such as 7.5 for half past seven, is at its top. Asked before the
// grid is first laid out, the grid starts there.
func (d *Days) ScrollToHour(h float32, u *gunim.UI) {
	if !d.laid {
		d.topHour, d.asked = h, true
		u.Invalidate()
		return
	}
	d.scroll.Animate(d.clampScroll(h*d.hour()), widget.Quick.Get(u.Theme()))
}

func (d *Days) gridStep() time.Duration {
	if d.Snap <= 0 {
		return 15 * time.Minute
	}
	return d.Snap
}

// day returns the midnight starting day i of those shown.
func (d *Days) day(i int) time.Time { return AddDays(d.First, i) }

// geometry, all in the grid's space.
func (d *Days) colW() float32 { return (d.box.W - gutterW) / float32(max(d.Count, 1)) }
func (d *Days) colX(i int) float32 {
	return gutterW + float32(i)*d.colW()
}
func (d *Days) longTop() float32 { return headerH }
func (d *Days) bodyTop() float32 { return headerH + float32(max(d.longRows, 1))*longRowH + 6 }

// hour returns how tall an hour is.
func (d *Days) hour() float32 {
	if d.hourH <= 0 {
		return 48
	}
	return d.hourH
}

// hourY returns where a time offset into a day is in the hours' own space.
func (d *Days) hourY(offset time.Duration) float32 { return float32(offset.Hours()) * d.hour() }

// timeY returns where a time offset into a day is down the grid, scrolled.
func (d *Days) timeY(offset time.Duration) float32 {
	return d.bodyTop() + d.hourY(offset) - d.scrollY()
}

// onScreen turns a sprite's box into the grid's space.
func (d *Days) onScreen(s *sprite, r geom.Rect) geom.Rect {
	if s.long {
		return r
	}
	return r.Add(geom.Pt(0, d.bodyTop()-d.scrollY()))
}

// scrollY returns how far the hours are scrolled as drawn, kept within them, so no frame shows past either end of
// the day.
func (d *Days) scrollY() float32 { return d.clampScroll(d.scroll.Value()) }

func (d *Days) clampScroll(v float32) float32 {
	room := 24*d.hour() - (d.box.H - d.bodyTop())
	return min(max(v, 0), max(room, 0))
}

// Layout implements [gunim.Node]: it sends each event gliding to its place.
func (d *Days) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	was := d.hourH
	d.box, d.hourH, d.th = c.Max, HourHeight.Get(f.Theme), f.Theme
	if k := d.key(); k != d.laidFor || d.laidTo == nil {
		d.vis = d.shown()
		d.layLong()
		d.laidTo, d.laidFor = d.targets(), k
	}
	switch to := d.scroll.Target(); {
	case !d.laid:
		h := float32(7.5)
		if d.asked {
			h, d.asked = d.topHour, false
		}
		d.scroll.Jump(d.clampScroll(h * d.hour()))
	case was > 0 && was != d.hourH:
		// The hour at the top stays there as the hours grow or shrink.
		d.scroll.Jump(d.clampScroll(d.scroll.Value() * d.hourH / was))
	case d.clampScroll(to) != to:
		// The grid grew, or the row of whole days shrank, and has less to scroll.
		d.scroll.Animate(d.clampScroll(to), widget.Quick.Get(f.Theme))
	}
	stepped := d.laid && d.Count == d.was.count && !d.First.Equal(d.was.first)
	jump := !d.laid || d.box != d.was.box
	d.place(f.Theme, jump || stepped, stepped)
	d.laid = true
	d.was.box, d.was.first, d.was.count = d.box, d.First, d.Count
	return d.box
}

// daysKey is what a grid's places are worked out for: the events, the days, the size, the drag and the changes
// held.
type daysKey struct {
	idx         *index
	first       time.Time
	count       int
	box         geom.Size
	hourH       float32
	open        bool
	drag        dayDrag
	dragging    bool
	held, holds int
}

// key returns what the grid's places would be worked out for now.
func (d *Days) key() daysKey {
	d.idx = indexOf(d.idx, d.events)
	k := daysKey{idx: d.idx, first: d.First, count: d.Count, box: d.box, hourH: d.hourH, open: d.longOpen,
		holds: d.holds}
	if d.drag != nil {
		k.drag, k.dragging = *d.drag, true
	}
	for _, h := range d.held {
		if time.Since(h.at) <= time.Second {
			k.held++
		}
	}
	return k
}

// target is where an event goes.
type target struct {
	key         string
	e           Event
	long        bool
	box         geom.Rect
	top, bottom bool
}

// place sends each event's sprite to where it goes: at once with jump, as when the window changes size, and
// gliding otherwise. Sprites of events no longer shown fade out, or go at once with drop, as the grid steps and
// slides the new days in.
func (d *Days) place(th *theme.Live, jump, drop bool) {
	seen := map[string]bool{}
	d.order = d.order[:0]
	for _, t := range d.laidTo {
		seen[t.key] = true
		d.order = append(d.order, t.key)
		s, ok := d.sprites[t.key]
		switch {
		case !ok:
			s = newSprite(t.box, t.e)
			if jump {
				s.in.Jump(1)
			} else {
				s.in.Animate(1, widget.Bounce.Get(th))
			}
			d.sprites[t.key] = s
		case s.gone:
			s.gone = false
			s.in.Animate(1, widget.Bounce.Get(th))
			s.rect.Jump(t.box)
		case jump:
			s.rect.Jump(t.box)
		case s.rect.Target() != t.box:
			motion := widget.Settle.Get(th)
			if d.drag != nil && d.drag.id == t.e.ID {
				// Under the pointer it follows closely, snapping from step to step.
				motion = widget.Quick.Get(th)
			}
			s.rect.Animate(t.box, motion)
		}
		s.show(t.e, th)
		s.long, s.top, s.bottom = t.long, t.top, t.bottom
	}
	for k, s := range d.sprites {
		if seen[k] {
			continue
		}
		if drop {
			delete(d.sprites, k)
			continue
		}
		if !s.gone {
			s.gone = true
			s.in.Animate(0, widget.Quick.Get(th))
		}
		d.order = append(d.order, k)
	}
	d.aimLifts(th)
}

// aimLifts lifts the event under the pointer and the one chosen, and lets the rest down.
func (d *Days) aimLifts(th *theme.Live) {
	for _, s := range d.sprites {
		up := !s.gone && (s.e.ID == d.hover || s.e.ID == d.selected || (d.drag != nil && d.drag.moved && s.e.ID == d.drag.id))
		s.lift.Animate(map[bool]float32{false: 0, true: 1}[up], widget.Quick.Get(th))
	}
}

// targets returns where every event shown goes: the whole-day ones on their rows, then each day's timed ones.
func (d *Days) targets() []target {
	var out []target
	for _, row := range d.long {
		for _, lp := range row {
			out = append(out, target{key: lp.e.ID + "#long", e: lp.e, long: true, box: d.longBox(lp)})
		}
	}
	for i := range d.Count {
		key := "@" + d.day(i).Format(time.DateOnly)
		for _, b := range d.timedBoxes(i) {
			out = append(out, target{key: b.e.ID + key, e: b.e, box: b.box, top: b.top, bottom: b.bottom})
		}
	}
	return out
}

// longCap returns how many rows the events of whole days may take: up to three, and no more than a quarter of the
// grid's height, or with open up to half of it.
func (d *Days) longCap(open bool) int {
	room := d.box.H - headerH
	n := min(3, int(room/4/longRowH))
	if open {
		n = max(n, int(room/2/longRowH))
	}
	return max(n, 1)
}

// layLong sets the events of whole days in the rows they may take. When they need more, the last row says how many
// more each day has.
func (d *Days) layLong() {
	rows := d.longPlaces()
	d.longAll = len(rows)
	bars := len(rows)
	if limit := d.longCap(d.longOpen); bars > limit {
		bars = limit - 1
	}
	d.long, d.longRows = rows[:bars], bars
	d.longMore = make([]int, d.Count)
	for _, row := range rows[bars:] {
		for _, lp := range row {
			for i := lp.first; i <= lp.last; i++ {
				d.longMore[i]++
			}
		}
	}
	if bars < len(rows) {
		d.longRows++
	}
}

// moreRow returns the box of the row saying how many more events of whole days there are, and false when there is
// none.
func (d *Days) moreRow() (geom.Rect, bool) {
	if len(d.long) == d.longAll {
		return geom.Rect{}, false
	}
	return geom.Rc(gutterW, d.longTop()+float32(len(d.long))*longRowH, d.box.W-gutterW, longRowH), true
}

// longToggle returns the box of the button in the gutter that opens and closes the rows of whole days, and false
// when they all fit without it.
func (d *Days) longToggle() (geom.Rect, bool) {
	if d.longAll <= d.longCap(false) {
		return geom.Rect{}, false
	}
	return geom.Rc(gutterW/2-12, d.longTop()+float32(d.longRows-1)*longRowH, 24, longRowH), true
}

// openLong opens the rows of whole days to show more of them, or closes them back.
func (d *Days) openLong(open bool, u *gunim.UI) {
	d.longOpen = open
	u.Invalidate()
}

// longPlace is a whole-day event on the row above the hours: its row, and the days it covers, from first to last.
type longPlace struct {
	e           Event
	row         int
	first, last int
}

// longPlaces sets the events that last whole days in rows, each in the first row free across its days. It returns
// the rows.
func (d *Days) longPlaces() [][]longPlace {
	var rows [][]longPlace
	for _, e := range d.vis {
		if !e.long() {
			continue
		}
		first, last := -1, -1
		for i := range d.Count {
			if e.covers(d.day(i)) {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first < 0 {
			continue
		}
		row := 0
		for ; row < len(rows); row++ {
			free := true
			for _, p := range rows[row] {
				if p.first <= last && p.last >= first {
					free = false
					break
				}
			}
			if free {
				break
			}
		}
		if row == len(rows) {
			rows = append(rows, nil)
		}
		rows[row] = append(rows[row], longPlace{e: e, row: row, first: first, last: last})
	}
	return rows
}

// shown returns the events on the days shown, in the order given, with any the user changed where they left them,
// and the one being dragged where the pointer has it.
func (d *Days) shown() []Event {
	d.idx = indexOf(d.idx, d.events)
	from, to := d.First, d.day(d.Count)
	at := d.idx.within(from, to)
	// The events the user changed, which may have come from days not shown.
	for id := range d.held {
		if i, ok := d.idx.find(id); ok {
			at = append(at, i)
		}
	}
	if g := d.drag; g != nil && g.kind != dragCreate {
		if i, ok := d.idx.find(g.id); ok {
			at = append(at, i)
		}
	}
	slices.Sort(at)
	out := make([]Event, 0, len(at))
	for _, i := range slices.Compact(at) {
		e := d.events[i]
		if h, ok := d.held[e.ID]; ok && time.Since(h.at) <= time.Second {
			e.Start, e.End = h.start, h.end
		}
		if d.drag != nil && d.drag.kind != dragCreate && d.drag.moved && d.drag.id == e.ID {
			e.Start, e.End = d.drag.start, d.drag.end
		}
		if overlaps(e, from, to) {
			out = append(out, e)
		}
	}
	return out
}

// timedBox is an event laid out in a day's column.
type timedBox struct {
	e   Event
	box geom.Rect
	// clipped says the event goes on before the day starts, or after it ends.
	top, bottom bool
}

// timedBoxes lays out the events of day i by the hour, in the hours' own space, in the order to draw them.
func (d *Days) timedBoxes(i int) []timedBox {
	day, next := d.day(i), d.day(i+1)
	var evs []Event
	var starts, ends []time.Time
	for _, e := range d.vis {
		if e.long() || !overlaps(e, day, next) {
			continue
		}
		s, en := e.Start, e.End
		if s.Before(day) {
			s = day
		}
		if en.After(next) {
			en = next
		}
		evs, starts, ends = append(evs, e), append(starts, s), append(ends, en)
	}
	places, order := lanes(starts, ends, d.leastSpan())
	w := d.colW() - dayMargin
	// Each step of indent moves an event over by a fifth of the column, up to 28 pixels.
	step := min(w/5, 28)
	out := make([]timedBox, 0, len(evs))
	for _, k := range order {
		p := places[k]
		inW := max(w-float32(p.indent)*step, w/3)
		laneW := inW / float32(max(p.lanes, 1))
		x := d.colX(i) + (w - inW) + float32(p.lane)*laneW
		y0 := d.hourY(starts[k].Sub(day))
		y1 := max(d.hourY(ends[k].Sub(day)), y0+minEventH)
		out = append(out, timedBox{e: evs[k], box: geom.Rc(x+1, y0+1, float32(p.span)*laneW-2, y1-y0-2),
			top: evs[k].Start.Before(day), bottom: evs[k].End.After(next)})
	}
	return out
}

// leastSpan returns how long a time the smallest box of an event covers, minEventH tall.
func (d *Days) leastSpan() time.Duration {
	return time.Duration(float64(minEventH) / float64(d.hour()) * float64(time.Hour))
}

// longBox returns where a whole-day event sits on the row above the hours.
func (d *Days) longBox(p longPlace) geom.Rect {
	x0, x1 := d.colX(p.first)+2, d.colX(p.last+1)-2
	return geom.Rc(x0, d.longTop()+float32(p.row)*longRowH+2, x1-x0, longRowH-4)
}

// Paint implements [gunim.Node].
func (d *Days) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	// The ring is drawn last, over the rest.
	defer widget.GroupRing(p, geom.Rect{Max: box.Point()}, 0, d.ring.Value(), th)
	line := widget.MenuBorder.Get(th)
	faint := widget.PaletteHint.Get(th)
	hourH := d.hour()
	now := f.Now.In(d.First.Location())
	body := geom.Rect{Min: geom.Pt(0, d.bodyTop()), Max: box.Point()}

	// The hours, under everything else, clipped to the body.
	func() {
		defer p.Layer(paint.LayerOpts{Bounds: body, Opacity: 1, Clip: true})()
		for i := range d.Count {
			day := d.day(i)
			col := geom.Rc(d.colX(i), body.Min.Y, d.colW(), body.Size().H)
			switch {
			case SameDay(day, now):
				p.RRect(col, 0, paint.Solid(TodayFill.Get(th)))
			case day.Weekday() == time.Saturday || day.Weekday() == time.Sunday:
				p.RRect(col, 0, paint.Solid(WeekendFill.Get(th)))
			}
		}
		if w := d.WorkDay; w[1] > w[0] {
			off := OffHoursFill.Get(th)
			y0, y1 := d.timeY(w[0]), d.timeY(w[1])
			p.RRect(geom.Rect{Min: geom.Pt(gutterW, body.Min.Y), Max: geom.Pt(box.W, y0)}, 0, paint.Solid(off))
			p.RRect(geom.Rect{Min: geom.Pt(gutterW, y1), Max: geom.Pt(box.W, body.Max.Y)}, 0, paint.Solid(off))
		}
		small := widget.Font.Get(th)
		for h := range 25 {
			y := d.timeY(time.Duration(h) * time.Hour)
			if y < body.Min.Y-hourH || y > box.H+hourH {
				continue
			}
			p.RRect(geom.Rc(gutterW, float32(math.Round(float64(y))), box.W-gutterW, 1), 0, paint.Solid(line))
			if h > 0 && h < 24 {
				lbl := small.Shape(clock(h, 0), EventText.Get(th))
				lbl.Paint(p, geom.Pt(gutterW-8-lbl.Advance, y-lbl.Height()/2), faint)
			}
		}
		for i := 1; i < d.Count; i++ {
			p.RRect(geom.Rc(d.colX(i), body.Min.Y, 1, body.Size().H), 0, paint.Solid(line))
		}
		moving(p, box, d.slide.Value(), func() {
			for _, k := range d.order {
				if s := d.sprites[k]; s != nil && !s.long {
					d.paintSprite(p, th, s)
				}
			}
			if !d.ghost.Target().Empty() && !d.ghostAllDay() {
				d.paintGhost(p, th, d.ghost.Value().Add(geom.Pt(0, d.bodyTop()-d.scrollY())))
			}
		})
		// Now, faint across the days shown and bold across today.
		for i := range d.Count {
			if !SameDay(d.day(i), now) {
				continue
			}
			faintNow := NowInk.Get(th)
			faintNow.A = 0x50
			p.RRect(geom.Rc(gutterW, d.timeY(dayOffset(now))-0.5, box.W-gutterW, 1), 0, paint.Solid(faintNow))
		}
		for i := range d.Count {
			if !SameDay(d.day(i), now) {
				continue
			}
			y := d.timeY(dayOffset(now))
			c := NowInk.Get(th)
			p.RRect(geom.Rc(d.colX(i), y-1, d.colW(), 2), 1, paint.Solid(c))
			p.RRect(geom.Rc(d.colX(i)-5, y-5, 10, 10), 5, paint.Solid(c))
			f.RedrawAt(now.Truncate(time.Minute).Add(time.Minute))
		}
	}()

	// The headings and the row of whole days, over the hours.
	for i := 1; i < d.Count; i++ {
		p.RRect(geom.Rc(d.colX(i), headerH-10, 1, d.bodyTop()-headerH+10), 0, paint.Solid(line))
	}
	p.RRect(geom.Rc(0, d.bodyTop()-1, box.W, 1), 0, paint.Solid(line))
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, box.W, d.bodyTop()), Opacity: 1, Clip: true})()
	if r, ok := d.longToggle(); ok {
		chevron := icon.ChevronDown
		if d.longOpen {
			chevron = icon.ChevronUp
		}
		widget.PaintIcon(p, th, chevron, geom.Rc(r.Center().X-8, r.Center().Y-8, 16, 16), faint)
	}
	moving(p, box, d.slide.Value(), func() {
		if r, ok := d.moreRow(); ok {
			for i, n := range d.longMore {
				if n == 0 {
					continue
				}
				t := d.paragraph(th, strconv.Itoa(n)+" more", d.colW()-14, 1, true, EventText.Get(th))
				t.Paint(p, geom.Pt(d.colX(i)+9, r.Center().Y-t.Size.H/2), faint)
			}
		}
		if h := min(max(d.heads.Value(), 0), 1); h < 0.99 {
			func() {
				defer p.Layer(paint.LayerOpts{Bounds: geom.Rc(0, 0, box.W, headerH), Opacity: h})()
				d.paintHeads(p, th, now)
			}()
		} else {
			d.paintHeads(p, th, now)
		}
		for _, k := range d.order {
			if s := d.sprites[k]; s != nil && s.long {
				d.paintSprite(p, th, s)
			}
		}
		if !d.ghost.Target().Empty() && d.ghostAllDay() {
			d.paintGhost(p, th, d.ghost.Value())
		}
	})
}

// ghostAllDay reports whether the event being drawn out, or held while it is named, lasts whole days.
func (d *Days) ghostAllDay() bool {
	g := d.drag
	if g == nil || g.kind != dragCreate {
		g = d.ghostHeld
	}
	return g != nil && g.allDay
}

// ghostOnScreen returns the drawn event's box in the grid's space.
func (d *Days) ghostOnScreen() geom.Rect {
	if d.ghostAllDay() {
		return d.ghost.Target()
	}
	return d.ghost.Target().Add(geom.Pt(0, d.bodyTop()-d.scroll.Target()))
}

// ClearGhost lets the event drawn out and held by [Days.OnCreate] fade, once it is named or given up.
func (d *Days) ClearGhost(u *gunim.UI) {
	d.ghostHeld = nil
	d.ghostIn.Animate(0, widget.Settle.Get(u.Theme()))
	u.Invalidate()
}

// paintHeads draws the days' headings.
func (d *Days) paintHeads(p *paint.Painter, th *theme.Live, now time.Time) {
	ink, faint := widget.Ink.Get(th), widget.PaletteHint.Get(th)
	bold, regular := widget.BoldFont.Get(th), widget.Font.Get(th)
	for i := range d.Count {
		day := d.day(i)
		x := d.colX(i)
		name := regular.Shape(day.Format("Mon"), EventText.Get(th))
		num := bold.Shape(strconv.Itoa(day.Day()), 20)
		c := x + d.colW()/2
		if d.Count == 1 {
			c = x + 28
		}
		name.Paint(p, geom.Pt(c-name.Advance/2, 6), faint)
		numInk := ink
		if SameDay(day, now) {
			r := geom.Rc(c-17, 22, 34, 28)
			p.RRect(r, 14, paint.Solid(widget.Accent.Get(th)))
			numInk = widget.ButtonStrongInk.Get(th)
		}
		num.Paint(p, geom.Pt(c-num.Advance/2, 36-num.Height()/2), numInk)
	}
}

// eventColors returns an event's fill, the bar down its side, and its text's ink, lit by lift and waiting for an
// answer by wait, each from 0 to 1.
func eventColors(th *theme.Live, e Event, lift, wait float32) (fill, bar, ink, sub color.NRGBA) {
	c := e.Color
	a := (0.42+0.18*lift)*(1-wait) + (0.10+0.12*lift)*wait
	fill = color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(255 * a)}
	bar = c
	ink = widget.Ink.Get(th)
	sub = ink
	sub.A = 0xc8
	return fill, bar, ink, sub
}

// paintSprite draws an event as it moves: faded and a little shrunk while it comes in or goes out, and lifted with
// a shadow under the pointer, while dragged, or chosen.
func (d *Days) paintSprite(p *paint.Painter, th *theme.Live, s *sprite) {
	in := min(max(s.in.Value(), 0), 1)
	if in <= 0.01 {
		return
	}
	lift := min(max(s.lift.Value(), 0), 1)
	r := d.onScreen(s, s.rect.Value())
	shrink := (1 - in) * 6
	r = r.Inset(geom.Uniform(min(shrink, r.Size().H/3)))
	if in < 0.99 {
		defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-12)), Opacity: in})()
	}
	e := s.e
	wait := min(max(s.wait.Value(), 0), 1)
	fill, bar, ink, sub := eventColors(th, e, lift, wait)
	shadow := paint.Shadow{}
	if lift > 0.01 {
		shadow = paint.Shadow{Color: color.NRGBA{A: uint8(0x60 * lift)}, Blur: 12 * lift, Offset: geom.Pt(0, 3*lift)}
	}
	// A solid base under the tint, edged in the base's colour, keeps an event on top of another readable.
	base, round := EventBase.Get(th), EventRadius.Get(th)
	p.ShadowRRect(r.Inset(geom.Uniform(-1)), round+1, paint.Solid(base), shadow)
	p.RRect(r, round, paint.Solid(fill))
	if wait > 0.01 {
		stripes(p, r, round, bar, wait)
	}
	if hover := e.ID == d.hover && d.drag == nil; hover && s.resizable() && r.Size().H >= 44 {
		// A grip at the bottom says the event's length can be pulled.
		g := bar
		g.A = uint8(float32(g.A) * lift)
		p.RRect(geom.Rc(r.Center().X-10, r.Max.Y-4, 20, 2.5), 1.25, paint.Solid(g))
	}
	if e.ID == d.selected {
		p.RRectStroke(r.Inset(geom.Uniform(-1.5)), round+1.5, paint.Fill{}, paint.Stroke{Width: 2, Color: widget.Accent.Get(th)})
	}
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: round})()
	p.RRect(geom.Rc(r.Min.X, r.Min.Y, 4, r.Size().H), 0, paint.Solid(bar))
	size := EventText.Get(th)
	w := r.Size().W - 12
	if s.long {
		t := d.paragraph(th, e.Title, w, 1, true, size)
		t.Paint(p, geom.Pt(r.Min.X+9, r.Center().Y-t.Size.H/2), ink)
		return
	}
	times := clock(e.Start.Hour(), e.Start.Minute()) + "–" + clock(e.End.Hour(), e.End.Minute())
	line := d.paragraph(th, "Ag", w, 1, false, size).Size.H
	if r.Size().H < 2*line+4 {
		// Title and times on one line.
		t := d.paragraph(th, e.Title, w, 1, true, size)
		t.Paint(p, geom.Pt(r.Min.X+9, r.Min.Y+max((r.Size().H-t.Size.H)/2, 1)), ink)
		if rest := w - t.Size.W - 8; rest > 40 {
			tt := d.paragraph(th, times, rest, 1, false, size)
			tt.Paint(p, geom.Pt(r.Min.X+9+t.Size.W+8, r.Min.Y+max((r.Size().H-tt.Size.H)/2, 1)), sub)
		}
		return
	}
	lines := max(int((r.Size().H-6)/line)-1, 1)
	if w < 90 {
		// Too narrow to wrap words: one line, cut short.
		lines = 1
	}
	t := d.paragraph(th, e.Title, w, lines, true, size)
	t.Paint(p, geom.Pt(r.Min.X+9, r.Min.Y+4), ink)
	subText := times
	if e.Location != "" {
		subText += " · " + e.Location
	}
	st := d.paragraph(th, subText, w, 1, false, size)
	st.Paint(p, geom.Pt(r.Min.X+9, r.Min.Y+4+t.Size.H), sub)
}

// stripes shades r, rounded by radius, with thin diagonal stripes of c at strength from 0 to 1, for an event waiting
// for an answer.
func stripes(p *paint.Painter, r geom.Rect, radius float32, c color.NRGBA, strength float32) {
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: radius})()
	c.A = uint8(0x55 * strength)
	diag := r.Size().W + r.Size().H
	defer p.Push(paint.Rotate(-math.Pi/4, r.Center()))()
	for x := r.Center().X - diag/2; x < r.Center().X+diag/2; x += 9 {
		p.RRect(geom.Rc(x, r.Center().Y-diag/2, 3, diag), 0, paint.Solid(c))
	}
}

// paintGhost draws the event being drawn out, in r.
func (d *Days) paintGhost(p *paint.Painter, th *theme.Live, r geom.Rect) {
	in := min(max(d.ghostIn.Value(), 0), 1)
	if in <= 0.01 {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Uniform(-8)), Opacity: in})()
	a := widget.Accent.Get(th)
	round := EventRadius.Get(th)
	p.ShadowRRect(r, round, paint.Solid(color.NRGBA{R: a.R, G: a.G, B: a.B, A: 0x70}),
		paint.Shadow{Color: color.NRGBA{A: 0x50}, Blur: 10, Offset: geom.Pt(0, 3)})
	p.RRectStroke(r, round, paint.Fill{}, paint.Stroke{Width: 1.5, Color: a})
	g := d.drag
	if g == nil || g.kind != dragCreate {
		g = d.ghostHeld
	}
	if g == nil || g.allDay {
		return
	}
	t := clock(g.start.Hour(), g.start.Minute()) + "–" + clock(g.end.Hour(), g.end.Minute())
	s := d.paragraph(th, t, r.Size().W-12, 1, true, EventText.Get(th))
	s.Paint(p, geom.Pt(r.Min.X+9, r.Min.Y+4), widget.Ink.Get(th))
}

// paragraph lays s out, bold or not, w wide in up to lines lines, keeping it for the next frame.
func (d *Days) paragraph(th *theme.Live, s string, w float32, lines int, bold bool, size float32) text.Paragraph {
	k := textKey{s: s, w: w, lines: lines, bold: bold, fontSize: size}
	if t, ok := d.texts[k]; ok {
		return t
	}
	face := widget.Font.Get(th)
	if bold {
		face = widget.BoldFont.Get(th)
	}
	t := face.Layout(s, text.Style{Size: size, MaxLines: lines}, max(w, 1))
	if d.texts == nil {
		d.texts = map[textKey]text.Paragraph{}
	}
	d.texts[k] = t
	return t
}

// clock writes a time of day as 09:30.
func clock(h, m int) string {
	return strconv.Itoa(h/10) + strconv.Itoa(h%10) + ":" + strconv.Itoa(m/10) + strconv.Itoa(m%10)
}

// dayAt returns the day under x, clamped to those shown.
func (d *Days) dayAt(x float32) int {
	return min(max(int((x-gutterW)/d.colW()), 0), d.Count-1)
}

// timeAt returns the time under y on day i, snapped down to the step.
func (d *Days) timeAt(i int, y float32) time.Time {
	hours := (y - d.bodyTop() + d.scrollY()) / d.hour()
	off := time.Duration(float64(hours) * float64(time.Hour))
	off = min(max(off, 0), 24*time.Hour)
	return d.day(i).Add(off.Truncate(d.gridStep()))
}

// resizable reports whether the event's length can be pulled by its edges: a timed one, tall enough to hold a click
// apart from its edges, that the user may change.
func (s *sprite) resizable() bool {
	return !s.long && !s.e.Fixed && s.rect.Value().Size().H >= 30
}

// edge is the edge of an event the pointer is on.
type edge uint8

const (
	noEdge edge = iota
	topEdge
	bottomEdge
)

// eventUnder returns the event under pt, its box on screen, and the edge pt is on.
func (d *Days) eventUnder(pt geom.Point) (Event, geom.Rect, edge, bool) {
	inHours := pt.Y >= d.bodyTop()
	for k := len(d.order) - 1; k >= 0; k-- {
		s := d.sprites[d.order[k]]
		if s == nil || s.gone || s.long == inHours {
			continue
		}
		r := d.onScreen(s, s.rect.Value())
		if !r.Contains(pt) {
			continue
		}
		on := noEdge
		switch {
		case !s.resizable():
		case pt.Y > r.Max.Y-edgeGrab && !s.bottom:
			on = bottomEdge
		case pt.Y < r.Min.Y+edgeGrab && !s.top:
			on = topEdge
		}
		return s.e, r, on, true
	}
	return Event{}, geom.Rect{}, noEdge, false
}

// tipText returns the tooltip for the event at pt: its title, time and place in full, when its box cuts them short,
// and nothing otherwise.
func (d *Days) tipText(pt geom.Point, th *theme.Live) string {
	if d.drag != nil || d.th == nil {
		return ""
	}
	ev, r, _, ok := d.eventUnder(pt)
	if !ok {
		return ""
	}
	title := widget.BoldFont.Get(th).Shape(ev.Title, EventText.Get(th))
	if title.Advance <= r.Size().W-12 && (ev.long() || r.Size().H >= 36) {
		return ""
	}
	return eventTip(ev)
}

// eventTip is the tooltip for e: its title, its time and its place.
func eventTip(e Event) string {
	s := e.Title
	if !e.long() {
		s += " · " + clock(e.Start.Hour(), e.Start.Minute()) + "–" + clock(e.End.Hour(), e.End.Minute())
	}
	if e.Location != "" {
		s += " · " + e.Location
	}
	return s
}

// Handle implements [gunim.Handler].
func (d *Days) Handle(e input.Event, u *gunim.UI) bool {
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerMove:
		d.tip.Handle(e, u, d, d.tipText(e.Pos, th))
	case input.PointerLeave, input.PointerDown, input.Scroll:
		d.tip.Handle(e, u, d, "")
	}
	switch e := e.(type) {
	case input.Scroll:
		if sideways := e.Mods.Has(input.ModShift) || abs(e.Delta.X) > abs(e.Delta.Y); sideways && d.OnStep != nil {
			if by := d.swipe.step(e, false); by != 0 {
				send(u, d, d.OnStep(by, u))
			}
			return true
		}
		if e.Pos.Y < d.bodyTop() {
			return false
		}
		d.pointer, d.over = e.Pos, true
		d.scroll.Animate(d.clampScroll(d.scroll.Target()-e.Delta.Y), widget.Quick.Get(th))
		return true
	case input.PointerMove:
		d.over = true
		if d.drag != nil {
			d.pointer = e.Pos
			d.dragTo(e.Pos, th)
			u.Invalidate()
			return true
		}
		d.pointer = e.Pos
		d.hoverAt(e.Pos, th)
	case input.PointerLeave:
		d.over = false
		if d.drag == nil && d.hover != "" {
			d.hover = ""
			d.aimLifts(th)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		d.pointer = e.Pos
		d.press(e, th, u)
		return true
	case input.PointerUp:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if d.tap.release(e.Pos) {
			return true
		}
		if d.drag == nil {
			return false
		}
		d.release(u)
		return true
	case input.KeyPress:
		if e.Key == input.KeyEscape && d.drag != nil {
			d.drag = nil
			d.ghostIn.Animate(0, widget.Quick.Get(th))
			d.aimLifts(th)
			u.Invalidate()
			return true
		}
		return eventKeys(e, u, d, d.stops(), d.selected, func(id string) { d.choose(id, u) }, d.OnOpen, d.OnDelete)
	case input.FocusRing:
		to := float32(0)
		if ringShown(e) {
			to = 1
		}
		d.ring.Animate(to, widget.RingFade.Get(u.Theme()))
	case input.FocusLost:
		d.ring.Animate(0, widget.RingFade.Get(u.Theme()))
	case input.FocusGained:
		// Tabbing in chooses the first event, for the keys to move on from.
		if e.Keyed && d.selected == "" {
			if s, ok := nextStop(d.stops(), "", input.KeyDown); ok {
				d.choose(s.id, u)
			}
		}
	}
	return false
}

// hoverAt lifts the event under pt.
func (d *Days) hoverAt(pt geom.Point, th *theme.Live) {
	id := ""
	if ev, _, _, ok := d.eventUnder(pt); ok {
		id = ev.ID
	}
	if id != d.hover {
		d.hover = id
		d.aimLifts(th)
	}
}

// press starts what a press at pt starts: a day's heading picks the day as the pointer lets go, an event is taken
// hold of, and free time begins a new event. The second press of a double click edits an event, and starts nothing
// the first one started already.
func (d *Days) press(e input.PointerDown, th *theme.Live, u *gunim.UI) {
	pt := e.Pos
	if r, ok := d.longToggle(); ok && r.Contains(pt) {
		d.tap.press(r, func() { d.openLong(!d.longOpen, u) })
		return
	}
	if r, ok := d.moreRow(); ok && r.Contains(pt) && d.longMore[d.dayAt(pt.X)] > 0 {
		i := d.dayAt(pt.X)
		d.tap.press(geom.Rc(d.colX(i), r.Min.Y, d.colW(), r.Size().H), func() { d.openLong(true, u) })
		return
	}
	if pt.X < gutterW {
		return
	}
	if pt.Y < headerH {
		if i := d.dayAt(pt.X); e.Clicks <= 1 && d.OnDay != nil {
			d.tap.press(geom.Rc(d.colX(i), 0, d.colW(), headerH), func() { pickDay(u, d, d.OnDay, d.day(i)) })
		}
		return
	}
	if ev, b, on, ok := d.eventUnder(pt); ok {
		if e.Clicks > 1 {
			if d.OnEdit != nil && !ev.Fixed {
				send(u, d, d.OnEdit(ev.ID, u))
			}
			return
		}
		kind := dragMove
		switch on {
		case bottomEdge:
			kind = dragResize
		case topEdge:
			kind = dragResizeTop
		default:
			// A pointer on neither edge drags the event itself, set above.
		}
		d.drag = &dayDrag{kind: kind, id: ev.ID, fixed: ev.Fixed, from: pt, start: ev.Start, end: ev.End, box: b,
			allDay: ev.long(), grab: d.timeAt(d.dayAt(pt.X), pt.Y).Sub(ev.Start)}
		return
	}
	if e.Clicks > 1 || (d.Busy != nil && d.Busy()) {
		return
	}
	i := d.dayAt(pt.X)
	if pt.Y < d.bodyTop() {
		day := d.day(i)
		d.drag = &dayDrag{kind: dragCreate, from: pt, anchor: day, start: day, end: AddDays(day, 1), allDay: true}
	} else {
		t := d.timeAt(i, pt.Y)
		d.drag = &dayDrag{kind: dragCreate, from: pt, anchor: t, start: t, end: t.Add(d.gridStep())}
	}
	d.ghost.Jump(d.ghostBoxOf(d.drag))
	d.ghostIn.Animate(1, widget.Quick.Get(th))
	u.Invalidate()
}

// dragTo follows the pointer to pt.
func (d *Days) dragTo(pt geom.Point, th *theme.Live) {
	g := d.drag
	if !g.moved && math.Hypot(float64(pt.X-g.from.X), float64(pt.Y-g.from.Y)) < dragSlack {
		return
	}
	if !g.moved {
		g.moved = true
		d.aimLifts(th)
	}
	i := d.dayAt(pt.X)
	switch g.kind {
	case dragCreate:
		if g.allDay {
			day := d.day(i)
			g.start, g.end = minTime(g.anchor, day), AddDays(maxTime(g.anchor, day), 1)
		} else {
			// Drawn out on the day it began, whichever way the pointer goes.
			t := d.timeAt(d.dayAt(g.from.X), pt.Y)
			if t.Before(g.anchor) {
				g.start, g.end = t, g.anchor.Add(d.gridStep())
			} else {
				g.start, g.end = g.anchor, t.Add(d.gridStep())
			}
			g.end = minTime(g.end, AddDays(Day(g.anchor), 1))
		}
		d.ghost.Animate(d.ghostBoxOf(d.drag), widget.Quick.Get(th))
	case dragMove:
		if g.fixed {
			return
		}
		ev, ok := d.event(g.id)
		if !ok {
			return
		}
		length := ev.End.Sub(ev.Start)
		if ev.long() {
			days := i - d.dayAt(g.from.X)
			g.start = AddDays(Day(ev.Start), days)
			if !ev.AllDay {
				g.start = g.start.Add(dayOffset(ev.Start))
			}
			g.end = g.start.Add(length)
			return
		}
		t := d.timeAt(i, pt.Y).Add(-g.grab.Truncate(d.gridStep()))
		g.start, g.end = t, t.Add(length)
	case dragResize:
		if g.fixed {
			return
		}
		ev, ok := d.event(g.id)
		if !ok {
			return
		}
		t := d.timeAt(d.dayAt(g.from.X), pt.Y).Add(d.gridStep())
		g.start, g.end = ev.Start, maxTime(t, ev.Start.Add(d.gridStep()))
	case dragResizeTop:
		if g.fixed {
			return
		}
		ev, ok := d.event(g.id)
		if !ok {
			return
		}
		t := d.timeAt(d.dayAt(g.from.X), pt.Y)
		g.start, g.end = minTime(t, ev.End.Add(-d.gridStep())), ev.End
	}
}

// ghostBoxOf returns where the event being drawn out goes: in the grid's space for one of whole days, and in the
// hours' own space otherwise.
func (d *Days) ghostBoxOf(g *dayDrag) geom.Rect {
	if g.allDay {
		first, last := -1, 0
		for i := range d.Count {
			if (Event{Start: g.start, End: g.end}).covers(d.day(i)) {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		return d.longBox(longPlace{row: 0, first: max(first, 0), last: last})
	}
	i := d.dayAt(g.from.X)
	day := d.day(i)
	y0, y1 := d.hourY(g.start.Sub(day)), d.hourY(g.end.Sub(day))
	return geom.Rc(d.colX(i)+1, y0+1, d.colW()-dayMargin-2, y1-y0-2)
}

// release ends a drag: a new event, a moved or longer one, or a click on an event, which a drag that ends where
// it began counts as.
func (d *Days) release(u *gunim.UI) {
	g := d.drag
	d.drag = nil
	th := u.Theme()
	d.aimLifts(th)
	u.Invalidate()
	if g.kind == dragCreate {
		if !g.moved && !g.allDay {
			// A click, not a drag, draws out an hour.
			g.end = minTime(g.start.Add(time.Hour), AddDays(Day(g.start), 1))
			d.ghost.Animate(d.ghostBoxOf(g), widget.Bounce.Get(th))
		}
		if d.OnCreate != nil {
			in := d.OnCreate(g.start, g.end, g.allDay, d.ghostOnScreen(), u)
			if in == nil {
				// The drawn event stays while it is being named.
				d.ghostHeld = g
				return
			}
			u.Send(d, in)
		}
		d.ghostIn.Animate(0, widget.Settle.Get(th))
		return
	}
	ev, ok := d.event(g.id)
	if !ok {
		return
	}
	if !g.moved || g.fixed || (ev.Start.Equal(g.start) && ev.End.Equal(g.end)) {
		if d.OnOpen != nil {
			if b, ok := d.EventBox(g.id); ok {
				g.box = b
			}
			send(u, d, d.OnOpen(g.id, g.box, u))
		}
		return
	}
	d.held[g.id] = heldEvent{start: g.start, end: g.end, at: time.Now()}
	d.holds++
	if d.OnChange != nil {
		send(u, d, d.OnChange(g.id, g.start, g.end, u))
	}
}

// event returns the event with id, as the application last gave it, or as the user left it.
func (d *Days) event(id string) (Event, bool) {
	d.idx = indexOf(d.idx, d.events)
	i, ok := d.idx.find(id)
	if !ok {
		return Event{}, false
	}
	e := d.events[i]
	if h, ok := d.held[id]; ok {
		e.Start, e.End = h.start, h.end
	}
	return e, true
}

// Cursor implements [gunim.CursorShaper].
func (d *Days) Cursor(pt geom.Point) input.Cursor {
	if d.drag != nil {
		switch d.drag.kind {
		case dragResize, dragResizeTop:
			return input.CursorResizeV
		case dragMove:
			if d.drag.moved && !d.drag.fixed {
				return input.CursorMove
			}
		default:
			// A drag that is only being made has the pointer it started with.
		}
		return input.CursorArrow
	}
	if r, ok := d.longToggle(); ok && r.Contains(pt) {
		return input.CursorHand
	}
	if r, ok := d.moreRow(); ok && r.Contains(pt) && d.longMore[d.dayAt(pt.X)] > 0 {
		return input.CursorHand
	}
	if pt.X < gutterW {
		return input.CursorArrow
	}
	if pt.Y < headerH {
		return input.CursorHand
	}
	if _, _, on, ok := d.eventUnder(pt); ok {
		if on != noEdge {
			return input.CursorResizeV
		}
		return input.CursorHand
	}
	return input.CursorCrosshair
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
