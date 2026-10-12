package main

import (
	"image/color"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/calendar"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Theme tokens of the calendar's window.
var (
	SidebarFill = theme.Color("cal.sidebar", color.NRGBA{R: 0x15, G: 0x17, B: 0x1d, A: 0xff})
	PaneFill    = theme.Color("cal.pane", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	TitleSize   = theme.Length("cal.title.size", 20)
	sidebarW    = theme.Length("cal.sidebar.width", 276)
	CardTitle   = theme.Length("cal.card.title", 17)
	noGap       = theme.Length("cal.gap.none", 0)
)

func darkTheme() theme.Theme { return widget.Dark() }

func lightTheme() theme.Theme {
	return widget.Light().With(append([]theme.Entry{
		theme.Set(SidebarFill, color.NRGBA{R: 0xec, G: 0xef, B: 0xf4, A: 0xff}),
		theme.Set(PaneFill, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
	}, calendar.Light...)...)
}

// registerViews is the window half: the calendar, its dialogs and the themes.
func registerViews(w *gunim.Window) {
	w.RegisterTheme(darkTheme())
	w.RegisterTheme(lightTheme())
	gunim.RegisterView(w, "cal", buildCal, (*calView).set)
	gunim.RegisterPatch(w, "cal", func(v *calView, n Notice, u *gunim.UI) { v.notice(n, u) })
	gunim.RegisterPatch(w, "cal", func(v *calView, f Found, u *gunim.UI) { v.setFound(f.Items, u) })
	gunim.RegisterPatch(w, "cal", func(v *calView, r Reveal, u *gunim.UI) { v.reveal(r.ID, u) })
	gunim.RegisterView(w, "editor", newEditor, nil)
	gunim.RegisterView(w, "delete", newDeleteDialog, nil)
	gunim.RegisterView(w, "change", newChangeDialog, nil)
}

// calView is the window: a toolbar, a sidebar with a small month and the calendars, and the days.
type calView struct {
	root *widget.Flex

	title    *fadeLabel
	views    *widget.Segmented
	weekends *widget.Checkbox
	invites  *widget.Link
	mini     *calendar.MiniMonth
	cals     *widget.List
	days     *calendar.Days
	month    *calendar.Month
	area     *switcher
	toasts   *widget.Toasts
	palette  *widget.Palette
	// found are the events the search last found, in the palette's order.
	found []FoundEvent

	state Cal
	// card is the card about the event cardID, while it is open; quick is the popover naming a new event.
	card   *gunim.Popup
	cardID string
	quick  *gunim.Popup
	more   *gunim.Popup
	// ghostKept says the event drawn out stays shown while the full editor opened on it is open.
	ghostKept bool
	// swallow is set for the rest of a press that closed a popup, so the press begins nothing else.
	swallow bool
}

func buildCal(s Cal) *calView {
	v := &calView{state: s}
	today := widget.NewButton("Today")
	today.OnClick = widget.Sends(TodayAsked{})
	back := widget.NewIconButton(icon.ChevronLeft, "Back (Page Up)")
	back.OnClick = widget.Sends(Stepped{By: -1})
	next := widget.NewIconButton(icon.ChevronRight, "Next (Page Down)")
	next.OnClick = widget.Sends(Stepped{By: 1})
	v.title = newFadeLabel(s.Title)
	v.views = widget.NewSegmented("Day", "Week", "Month")
	v.views.OnChange = func(i int, u *gunim.UI) gunim.Intent { return ViewChosen{View: View(i)} }
	themeButton := widget.NewIconButton(icon.SunMoon, "Switch theme")
	themeButton.OnClick = widget.Sends(ThemeToggled{})
	find := widget.NewIconButton(icon.Search, "Find an event (Ctrl+F)")
	find.OnClick = func(u *gunim.UI) gunim.Intent {
		v.openSearch(u)
		return nil
	}
	spacer := widget.NewSpacer()
	bar := widget.Row(today, back, next, v.title, spacer, find, v.views, themeButton).Grow(spacer, 1)
	bar.Cross = widget.CrossCenter

	add := widget.NewButton("New event")
	add.Icon, add.Kind = icon.Plus, widget.ButtonPrimary
	add.OnClick = widget.Sends(NewAsked{})
	v.mini = calendar.NewMiniMonth(s.Day)
	v.mini.OnPick = func(day time.Time, u *gunim.UI) gunim.Intent { return DayPicked{Day: day} }
	caption := widget.NewLabel("Calendars")
	caption.Face, caption.Color = widget.BoldFont, widget.PaletteHint
	v.cals = widget.NewList()
	v.cals.OnActivate = func(k widget.Key, u *gunim.UI) gunim.Intent { return CalendarToggled{ID: string(k)} }
	v.invites = widget.NewLink("")
	v.invites.Icon = icon.Mail
	v.invites.OnClick = widget.Sends(InvitesAsked{})
	v.weekends = widget.NewCheckbox("Show weekends")
	v.weekends.SetChecked(!s.HideWeekends, nil)
	v.weekends.OnChange = func(bool, *gunim.UI) gunim.Intent { return WeekendsToggled{} }
	side := widget.Column(add, v.mini, caption, v.cals, v.weekends, v.invites)
	side.Cross = widget.CrossStretch
	sidebar := &panel{child: widget.NewPad(side), fill: SidebarFill, width: sidebarW}

	busy := func() bool { return v.swallow }
	v.days = calendar.NewDays(s.Day, 7)
	v.days.WorkDay = [2]time.Duration{8 * time.Hour, 17 * time.Hour}
	v.days.OnDay = func(day time.Time, u *gunim.UI) gunim.Intent { return DayOpened{Day: day} }
	v.days.OnCreate = func(start, end time.Time, allDay bool, box geom.Rect, u *gunim.UI) gunim.Intent {
		v.openQuick(v.days, Draft{Start: start, End: end, AllDay: allDay}, box, u)
		return nil
	}
	v.days.OnChange = func(id string, start, end time.Time, u *gunim.UI) gunim.Intent {
		return EventChanged{ID: id, Start: start, End: end}
	}
	v.days.OnOpen = func(id string, box geom.Rect, u *gunim.UI) gunim.Intent {
		v.openCard(v.days, id, box, u)
		return nil
	}
	v.days.OnEdit = func(id string, u *gunim.UI) gunim.Intent { return EditAsked{ID: id} }
	v.days.OnDelete = func(id string, u *gunim.UI) gunim.Intent { return DeleteAsked{ID: id} }
	v.days.OnStep = func(by int, u *gunim.UI) gunim.Intent { return Stepped{By: by} }
	v.days.Busy = busy
	v.month = calendar.NewMonth(s.Day)
	v.month.OnDay = func(day time.Time, u *gunim.UI) gunim.Intent { return DayOpened{Day: day} }
	v.month.OnCreate = func(day time.Time, box geom.Rect, u *gunim.UI) gunim.Intent {
		v.openQuick(v.month, Draft{Start: day, End: calendar.AddDays(day, 1), AllDay: true}, box, u)
		return nil
	}
	v.month.OnChange = func(id string, start, end time.Time, u *gunim.UI) gunim.Intent {
		return EventChanged{ID: id, Start: start, End: end}
	}
	v.month.OnOpen = func(id string, box geom.Rect, u *gunim.UI) gunim.Intent {
		v.openCard(v.month, id, box, u)
		return nil
	}
	v.month.OnEdit = func(id string, u *gunim.UI) gunim.Intent { return EditAsked{ID: id} }
	v.month.OnDelete = func(id string, u *gunim.UI) gunim.Intent { return DeleteAsked{ID: id} }
	v.month.OnStep = func(by int, u *gunim.UI) gunim.Intent { return Stepped{By: by} }
	v.month.OnMore = func(day time.Time, box geom.Rect, u *gunim.UI) gunim.Intent {
		v.openMore(day, box, u)
		return nil
	}
	v.month.Busy = busy
	v.area = newSwitcher(v.eventMenu(v.days, v.days.EventAt), v.eventMenu(v.month, v.month.EventAt))

	main := widget.Column(widget.NewPad(bar), v.area).Grow(v.area, 1)
	main.Cross = widget.CrossStretch
	pane := &panel{child: main, fill: PaneFill}
	v.root = widget.Row(sidebar, pane).Grow(pane, 1)
	v.root.Cross, v.root.Gap = widget.CrossStretch, noGap
	v.toasts = widget.NewToasts()
	v.palette = widget.NewPalette()
	v.palette.Placeholder = "Find an event by its title, place or notes"
	// The palette opens from the view, and its intents come from there.
	v.palette.OnSearch = func(q string, _ *gunim.UI) gunim.Intent { return SearchAsked{Query: q} }
	v.palette.OnPick = func(i int, _ *gunim.UI) gunim.Intent {
		if i < len(v.found) {
			return EventShown{ID: v.found[i].ID}
		}
		return nil
	}
	return v
}

// set shows s. A card or a quick popover closes as the view moves on, or a dialog opens.
func (v *calView) set(s Cal, u *gunim.UI) {
	was := v.state
	v.state = s
	moved := s.View != was.View || !s.Day.Equal(was.Day)
	if moved || s.Busy {
		v.closeCard(u)
		v.closeQuick(u, s.Busy && v.quick != nil)
		v.closeMore()
	} else if v.card != nil {
		if _, ok := v.event(v.cardID); !ok {
			v.closeCard(u)
		}
	}
	if v.ghostKept && !s.Busy {
		v.ghostKept = false
		v.days.ClearGhost(u)
	}
	v.title.set(s.Title, u)
	v.weekends.SetChecked(!s.HideWeekends, u)
	v.month.HideWeekends = s.HideWeekends
	v.views.SetSelected(int(s.View), u)
	from, to := shown(s)
	v.mini.SetMarked(from, calendar.AddDays(to, -1), u)
	widget.Sync(v.cals, u, s.Calendars,
		func(c Calendar) widget.Key { return widget.Key(c.ID) },
		newCalRow,
		func(r *calRow, c Calendar, u *gunim.UI) { r.set(c, u) })
	switch s.Invites {
	case 0:
		v.invites.Text = ""
	case 1:
		v.invites.Text = "1 invitation waits for an answer"
	default:
		v.invites.Text = strconv.Itoa(s.Invites) + " invitations wait for an answer"
	}
	if s.View == MonthView {
		v.month.SetMonth(s.Day, u)
		v.month.SetEvents(s.Events, u)
		v.area.show(1, u)
	} else {
		v.days.SetDays(from, int(to.Sub(from).Hours()/24+0.5), u)
		v.days.SetEvents(s.Events, u)
		v.area.show(0, u)
	}
	u.Invalidate()
}

// shown returns the first day the view shows, and the day after the last.
func shown(s Cal) (from, to time.Time) {
	switch s.View {
	case DayView:
		return s.Day, calendar.AddDays(s.Day, 1)
	case WeekView:
		f := calendar.WeekStart(s.Day, time.Monday)
		if s.HideWeekends {
			return f, calendar.AddDays(f, 5)
		}
		return f, calendar.AddDays(f, 7)
	default:
		// The month view draws its own, below.
	}
	return calendar.MonthStart(s.Day), calendar.MonthStart(s.Day).AddDate(0, 1, 0)
}

// notice shows a notice at the corner: with Undo for a change, or Show for an event.
func (v *calView) notice(n Notice, u *gunim.UI) {
	t := widget.Toast{Title: n.Title, Body: n.Body, Kind: widget.ToastInfo}
	switch {
	case n.Undo:
		t.Action, t.Icon = "Undo", icon.Check
		t.OnClick = widget.Sends(UndoAsked{})
	case n.ID != "":
		t.Action, t.Icon = "Show", icon.Mail
		t.OnClick = widget.Sends(EventShown{ID: n.ID})
	}
	v.toasts.Show(t, u)
}

// openSearch opens the search at the top of the window.
func (v *calView) openSearch(u *gunim.UI) {
	if v.palette.IsOpen() {
		return
	}
	b, ok := u.Bounds(v)
	if !ok {
		return
	}
	w := float32(560)
	v.palette.Open(v, geom.Rc((b.Size().W-w)/2, 60, w, 0), u)
}

// setFound shows the events a search found.
func (v *calView) setFound(found []FoundEvent, u *gunim.UI) {
	v.found = found
	items := make([]widget.PaletteItem, len(found))
	for i, f := range found {
		items[i] = widget.PaletteItem{Title: f.Title, Detail: f.When, Icon: icon.Calendar, Key: widget.Key(f.ID)}
	}
	v.palette.SetItems(items, u)
}

// reveal brings the event id into sight and opens its card.
func (v *calView) reveal(id string, u *gunim.UI) {
	if v.state.View == MonthView {
		if b, ok := v.month.EventBox(id); ok {
			v.openCard(v.month, id, b, u)
		}
		return
	}
	v.days.Reveal(id, u)
	if b, ok := v.days.EventBox(id); ok {
		v.openCard(v.days, id, b, u)
	}
}

// Children implements [gunim.Composite].
func (v *calView) Children() []gunim.Node { return []gunim.Node{v.root, v.toasts} }

// Layout implements [gunim.Node]: the notices sit at the bottom right, over the days.
func (v *calView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	root, toasts := kids.At(0), kids.At(1)
	root.Layout(gunim.Tight(c.Max))
	root.Place(geom.Point{})
	ts := toasts.Layout(gunim.Loose(geom.Sz(c.Max.W-32, c.Max.H)))
	toasts.Place(geom.Pt(c.Max.W-ts.W-20, c.Max.H-ts.H-20))
	return c.Max
}

// Paint implements [gunim.Node].
func (v *calView) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}

// Handle implements [gunim.Handler]: the keys that move about in time, pick the view, begin an event, find one,
// and undo.
func (v *calView) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	var intent gunim.Intent
	switch {
	case k.Mods&input.ModControl != 0 && (k.Key == input.KeyF || k.Key == input.KeyK):
		v.openSearch(u)
		return true
	case k.Mods&input.ModControl != 0 && k.Key == input.KeyZ:
		intent = UndoAsked{}
	case k.Mods&(input.ModControl|input.ModAlt) != 0:
		return false
	case k.Key == input.KeyT:
		intent = TodayAsked{}
	case k.Key == input.KeyD:
		intent = ViewChosen{View: DayView}
	case k.Key == input.KeyW:
		intent = ViewChosen{View: WeekView}
	case k.Key == input.KeyM:
		intent = ViewChosen{View: MonthView}
	case k.Key == input.KeyN:
		intent = NewAsked{}
	case k.Key == input.KeyPageUp || k.Key == input.KeyLeft:
		intent = Stepped{By: -1}
	case k.Key == input.KeyPageDown || k.Key == input.KeyRight:
		intent = Stepped{By: 1}
	default:
		return false
	}
	u.Send(v, intent)
	return true
}

// beside returns where a popup w wide goes beside box in from: to its right, top edges level, or to its left where
// the window runs out on the right. It reports whether the popup goes left.
func (v *calView) beside(from gunim.Node, box geom.Rect, w float32, u *gunim.UI) (geom.Rect, bool) {
	win, ok1 := u.Bounds(v)
	at, ok2 := u.Bounds(from)
	const gap = 6
	margin := widget.MenuMargin.Get(u.Theme())
	if ok1 && ok2 && at.Min.X+box.Max.X+gap+w+margin > win.Max.X {
		x := box.Min.X - gap - w - margin
		return geom.Rc(x, box.Min.Y, 1, 0), true
	}
	return geom.Rc(box.Max.X+gap-margin, box.Min.Y, 1, 0), false
}

// openCard shows the card about the event id beside its box in from, or closes it when it shows already.
func (v *calView) openCard(from gunim.Node, id string, box geom.Rect, u *gunim.UI) {
	if v.card != nil && v.card.Open() {
		open := v.cardID
		v.closeCard(u)
		if open == id {
			return
		}
	}
	v.closeQuick(u, false)
	v.closeMore()
	ev, ok := v.event(id)
	if !ok {
		return
	}
	v.cardID = id
	anchor, left := v.beside(from, box, cardW, u)
	c := newEventCard(ev, v.state.Details[id], left, func(u *gunim.UI) { v.backFromCard(u) })
	v.card = u.OpenPopup(from, c, gunim.PopupOptions{Anchor: anchor, Max: geom.Sz(cardW+80, 700),
		Dismiss: v.dismiss(func(u *gunim.UI) { v.closeCard(u) })})
	u.Focus(c)
	v.days.SetSelected(id, u)
	v.month.SetSelected(id, u)
}

// eventMenu wraps view in the menu a right click on one of its events opens: edit it, make a copy of it, delete
// it, or move it to another calendar.
func (v *calView) eventMenu(view gunim.Node, at func(geom.Point) (string, bool)) *widget.ContextMenu {
	c := widget.NewContextMenu(view, nil)
	var id string
	var cals []Calendar
	c.Prepare = func(pt geom.Point, u *gunim.UI) bool {
		ev, ok := at(pt)
		e, shown := v.event(ev)
		if !ok || !shown || e.Fixed {
			return false
		}
		v.closeCard(u)
		v.closeQuick(u, false)
		v.closeMore()
		id, cals = ev, v.state.Calendars
		items := []widget.MenuItem{{Label: "Edit", Icon: icon.Pencil}, {Label: "Make a copy", Icon: icon.Copy},
			{Label: "Delete", Icon: icon.Trash2}}
		for i, cal := range cals {
			items = append(items, widget.MenuItem{Label: "In " + cal.Name, Checked: cal.Name == v.state.Details[ev].Calendar,
				Break: i == 0})
		}
		c.SetItems(items)
		return true
	}
	c.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		switch {
		case i == 0:
			return EditAsked{ID: id}
		case i == 1:
			return DuplicateAsked{ID: id}
		case i == 2:
			return DeleteAsked{ID: id}
		case i-3 < len(cals):
			return CalendarSet{ID: id, Calendar: cals[i-3].ID}
		}
		return nil
	}
	return c
}

// dismiss wraps what closes a popup on a press outside it, so the rest of that press begins nothing.
func (v *calView) dismiss(shut func(*gunim.UI)) func(*gunim.UI) {
	return func(u *gunim.UI) {
		v.swallow = true
		u.After(0, func(*gunim.UI) { v.swallow = false })
		shut(u)
	}
}

// closeCard takes the card away, and lets its event go.
func (v *calView) closeCard(u *gunim.UI) {
	if v.card == nil {
		return
	}
	v.card.Close()
	v.card, v.cardID = nil, ""
	v.days.SetSelected("", u)
	v.month.SetSelected("", u)
}

// backFromCard takes the card away from inside it, as Escape or its close button does. The keyboard goes back to
// the days with the event still chosen, for the keys to move on from it.
func (v *calView) backFromCard(u *gunim.UI) {
	if v.card == nil {
		return
	}
	v.card.Close()
	v.card, v.cardID = nil, ""
	if v.state.View == MonthView {
		u.Focus(v.month)
	} else {
		u.Focus(v.days)
	}
}

// openMore opens the list of the events of day beside box in the month.
func (v *calView) openMore(day time.Time, box geom.Rect, u *gunim.UI) {
	v.closeCard(u)
	v.closeQuick(u, false)
	v.closeMore()
	var evs []calendar.Event
	for _, e := range v.state.Events {
		if e.Start.Before(calendar.AddDays(day, 1)) && e.End.After(day) {
			evs = append(evs, e)
		}
	}
	anchor, left := v.beside(v.month, box, moreW, u)
	m := newMoreCard(day, evs, left, func(id string, u *gunim.UI) {
		v.closeMore()
		v.openCard(v.month, id, box, u)
	}, func(*gunim.UI) { v.closeMore() })
	v.more = u.OpenPopup(v.month, m, gunim.PopupOptions{Anchor: anchor, Max: geom.Sz(moreW+80, 700),
		Dismiss: v.dismiss(func(*gunim.UI) { v.closeMore() })})
	u.Focus(m)
}

func (v *calView) closeMore() {
	if v.more != nil {
		v.more.Close()
		v.more = nil
	}
}

// openQuick opens the popover that names a new event drawn out in from, beside its box.
func (v *calView) openQuick(from gunim.Node, d Draft, box geom.Rect, u *gunim.UI) {
	v.closeCard(u)
	v.closeQuick(u, false)
	v.closeMore()
	d.Calendars, d.Calendar = v.state.Calendars, v.state.LastCalendar
	anchor, left := v.beside(from, box, quickW, u)
	q := newQuickCard(d, left, func(u *gunim.UI) { v.closeQuick(u, false) }, func(u *gunim.UI) { v.closeQuick(u, true) })
	v.quick = u.OpenPopup(from, q, gunim.PopupOptions{Anchor: anchor, Max: geom.Sz(quickW+80, 500),
		Dismiss: v.dismiss(func(u *gunim.UI) { v.closeQuick(u, false) })})
	u.Focus(q.name)
}

// closeQuick takes the popover naming a new event away, and the event drawn out with it, unless keep keeps that
// showing behind the full editor, until the editor closes.
func (v *calView) closeQuick(u *gunim.UI, keep bool) {
	if v.quick == nil {
		return
	}
	v.quick.Close()
	v.quick = nil
	if keep {
		v.ghostKept = true
		return
	}
	v.days.ClearGhost(u)
}

// event returns the event with id in the state shown.
func (v *calView) event(id string) (calendar.Event, bool) {
	for _, e := range v.state.Events {
		if e.ID == id {
			return e, true
		}
	}
	return calendar.Event{}, false
}

// calRow is one of the calendars in the sidebar: a box in its colour, ticked while it shows, and its name.
type calRow struct {
	anim.Group
	c         Calendar
	on, hover *anim.Float
	name      text.Run
}

func newCalRow(c Calendar) *calRow {
	r := &calRow{c: c, on: anim.NewFloat(0), hover: anim.NewFloat(0)}
	r.Add(r.on, r.hover)
	if c.Shown {
		r.on.Jump(1)
	}
	return r
}

func (r *calRow) set(c Calendar, u *gunim.UI) {
	r.c = c
	r.on.Animate(map[bool]float32{false: 0, true: 1}[c.Shown], widget.Bounce.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (r *calRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	r.name = widget.Font.Get(f.Theme).Shape(r.c.Name, widget.TextSize.Get(f.Theme))
	return geom.Sz(c.Max.W, 32)
}

// Paint implements [gunim.Node]: the box fills with the calendar's colour and the tick draws itself in as it turns on.
func (r *calRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	if h := min(max(r.hover.Value(), 0), 1); h > 0.01 {
		hot := widget.MenuHot.Get(th)
		hot.A = uint8(float32(hot.A) * h * 0.6)
		p.RRect(geom.Rect{Max: box.Point()}, 7, paint.Solid(hot))
	}
	on := min(max(r.on.Value(), 0), 1.2)
	b := geom.Rc(10, box.H/2-8, 16, 16)
	c := r.c.Color
	fill := color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(255 * min(on, 1))}
	grow := (on - min(on, 1)) * 4
	p.RRectStroke(b.Inset(geom.Uniform(-grow)), 4, paint.Solid(fill), paint.Stroke{Width: 1.5, Color: c})
	if on > 0.01 {
		p.Mask(icon.Stroke{Icon: icon.Check, Width: 2.5, Progress: min(on, 1)}, b.Inset(geom.Uniform(2)),
			color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	}
	ink := widget.Ink.Get(th)
	if on < 0.5 {
		ink = widget.PaletteHint.Get(th)
	}
	r.name.Paint(p, geom.Pt(36, (box.H-r.name.Height())/2), ink)
}

// Handle implements [gunim.Handler]: the row lights under the pointer, and leaves clicks to the list.
func (r *calRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e.(type) {
	case input.PointerEnter:
		r.hover.Animate(1, widget.Quick.Get(u.Theme()))
	case input.PointerLeave:
		r.hover.Animate(0, widget.Settle.Get(u.Theme()))
	}
	return false
}

// Cursor implements [gunim.CursorShaper].
func (r *calRow) Cursor(geom.Point) input.Cursor { return input.CursorHand }

// panel fills its box and holds its child, at a width from a token when it has one.
type panel struct {
	child gunim.Node
	fill  theme.Token[color.NRGBA]
	width theme.Token[float32]
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node { return []gunim.Node{p.child} }

// Layout implements [gunim.Node].
func (p *panel) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	if p.width.Key() != "" {
		size.W = p.width.Get(f.Theme)
	}
	kid := kids.At(0)
	kid.Layout(gunim.Tight(size))
	kid.Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pt.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(p.fill.Get(f.Theme)))
	kids.At(0).Paint(pt)
}

// switcher shows one of its children, crossfading and a little scaling as the choice changes. The others stay built.
type switcher struct {
	anim.Group
	kids  []gunim.Node
	shown int
	vis   []*anim.Float
}

func newSwitcher(kids ...gunim.Node) *switcher {
	s := &switcher{kids: kids}
	for i := range kids {
		f := anim.NewFloat(0)
		if i == 0 {
			f.Jump(1)
		}
		s.vis = append(s.vis, f)
		s.Add(f)
	}
	return s
}

// show crossfades to child i.
func (s *switcher) show(i int, u *gunim.UI) {
	if i == s.shown {
		return
	}
	s.shown = i
	for k, f := range s.vis {
		f.Animate(map[bool]float32{false: 0, true: 1}[k == i], widget.Settle.Get(u.Theme()))
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (s *switcher) Children() []gunim.Node { return s.kids }

// Layout implements [gunim.Node].
func (s *switcher) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	for kid := range kids.All {
		kid.Layout(gunim.Tight(c.Max))
		kid.Place(geom.Point{})
	}
	return c.Max
}

// Paint implements [gunim.Node]: a child out of sight is not drawn, so it takes no clicks. One coming in grows
// into place from a little smaller, and one going out grows a little as it fades.
func (s *switcher) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	for i := range s.kids {
		o := min(max(s.vis[i].Value(), 0), 1)
		switch {
		case o <= 0.01:
		case o >= 0.99:
			kids.At(i).Paint(p)
		default:
			func() {
				defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: o})()
				scale := 0.97 + 0.03*o
				if i != s.shown {
					scale = 1.02 - 0.02*o
				}
				defer p.Push(paint.Scale(scale, geom.Pt(box.W/2, box.H/3)))()
				kids.At(i).Paint(p)
			}()
		}
	}
}

// fadeLabel is a title that crossfades to new text, the old sliding up and away as the new slides in under it.
type fadeLabel struct {
	anim.Group
	now, was string
	t        *anim.Float
}

func newFadeLabel(s string) *fadeLabel {
	l := &fadeLabel{now: s, t: anim.NewFloat(1)}
	l.Add(l.t)
	return l
}

func (l *fadeLabel) set(s string, u *gunim.UI) {
	if s == l.now {
		return
	}
	l.was, l.now = l.now, s
	l.t.Jump(0)
	l.t.Animate(1, widget.Settle.Get(u.Theme()))
	u.Invalidate()
}

// Layout implements [gunim.Node].
func (l *fadeLabel) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	face := widget.BoldFont.Get(f.Theme)
	size := TitleSize.Get(f.Theme)
	w := max(face.Shape(l.now, size).Advance, face.Shape(l.was, size).Advance*(1-min(max(l.t.Value(), 0), 1)))
	return c.Constrain(geom.Sz(w+8, face.Shape("Ag", size).Height()))
}

// Paint implements [gunim.Node].
func (l *fadeLabel) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	face, size, ink := widget.BoldFont.Get(th), TitleSize.Get(th), widget.Ink.Get(th)
	t := min(max(l.t.Value(), 0), 1)
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
	draw := func(s string, y, a float32) {
		if a <= 0.01 {
			return
		}
		c := ink
		c.A = uint8(float32(c.A) * a)
		r := face.Shape(s, size)
		r.Paint(p, geom.Pt(0, y), c)
	}
	draw(l.was, -10*t, 1-t)
	draw(l.now, 10*(1-t), t)
}
