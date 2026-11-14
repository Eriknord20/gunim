package main

import (
	"crypto/subtle"
	"fmt"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/widget"
)

// pin is the PIN that opens the settings. A real panel keeps it out of the program, and checks it in a process of
// its own.
const pin = "2468"

// panel is the view: a stack of screens over a keyboard on the screen. The first screen shows the beds.
type panel struct {
	*widget.Keyboard
	stack  *widget.Stack
	cards  []*bedCard
	screen *bedScreen
	beds   []Bed
	opened bool
}

// newPanel builds the panel showing s.
func newPanel(s Plants) *panel {
	p := &panel{beds: s.Beds}
	title := heading("Plants")
	settings := widget.NewButton("Settings")
	settings.Icon = icon.Settings
	settings.OnClick = func(u *gunim.UI) gunim.Intent {
		p.stack.Push(p.pinScreen(), u)
		return nil
	}
	top := widget.Row(title, widget.NewSpacer(), settings).Grow(title, 1)
	top.Cross = widget.CrossCenter
	kids := make([]gunim.Node, 0, len(s.Beds))
	for i, b := range s.Beds {
		c := newBedCard(p, i, b)
		p.cards = append(p.cards, c)
		kids = append(kids, c)
	}
	cards := widget.Row(kids...)
	cards.Cross = widget.CrossStretch
	for _, c := range kids {
		cards.Grow(c, 1)
	}
	home := widget.Column(top, cards).Grow(cards, 1)
	home.Cross = widget.CrossStretch
	p.stack = widget.NewStack(widget.NewPad(home))
	switch s.Show {
	case "bed", "name":
		p.open(0, nil)
	case "pin":
		p.stack.Push(p.pinScreen(), nil)
	case "settings", "hold":
		p.stack.Push(p.settingsScreen(), nil)
	}
	p.Keyboard = widget.NewKeyboard(p.stack)
	return p
}

// show brings the panel up to date with s.
func (p *panel) show(s Plants, u *gunim.UI) {
	p.beds = s.Beds
	for i, c := range p.cards {
		if i < len(s.Beds) {
			c.show(s.Beds[i], u)
		}
	}
	if p.screen != nil && p.screen.bed < len(s.Beds) {
		p.screen.show(s.Beds[p.screen.bed], u)
	}
	if !p.opened && p.screen != nil && s.Show == "name" {
		p.opened = true
		u.Focus(p.screen.name)
	}
	if !p.opened && s.Show == "tour" {
		p.opened = true
		p.tour(0, u)
	}
	u.Invalidate()
}

// tour walks the screens, one step a second, for measuring how the panel draws while it moves: a bed's screen
// slides in, its keyboard comes up and goes down, the screen slides out, and the PIN pad slides in, gives way to
// the settings as a right PIN does, and they slide out.
func (p *panel) tour(step int, u *gunim.UI) {
	switch step % 7 {
	case 0:
		p.open(step/7%len(p.beds), u)
	case 1:
		u.Focus(p.screen.name)
	case 2:
		u.Focus(nil)
	case 3, 6:
		p.stack.Pop(u)
	case 4:
		p.stack.Push(p.pinScreen(), u)
	case 5:
		p.stack.Pop(u)
		p.stack.Push(p.settingsScreen(), u)
	}
	u.After(time.Second, func(u *gunim.UI) { p.tour(step+1, u) })
}

// open pushes bed i's own screen.
func (p *panel) open(i int, u *gunim.UI) {
	p.screen = newBedScreen(p, i, p.beds[i])
	p.stack.Push(p.screen.root, u)
}

// heading is a title in the touch kit's size for titles, semibold.
func heading(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Size, l.Face = widget.HeadingSize, widget.BoldFont
	return l
}

// caption is a line of quiet text in the size for captions.
func caption(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Size, l.Color = widget.CaptionSize, widget.CaptionInk
	return l
}

// topBar is a screen's top: a back button saying where it goes, and the screen's title, 16 apart.
func topBar(to string, title *widget.Label, back func(u *gunim.UI)) *widget.Flex {
	b := widget.NewButton(to)
	b.Icon = icon.ChevronLeft
	b.OnClick = func(u *gunim.UI) gunim.Intent {
		back(u)
		return nil
	}
	top := widget.Row(b, title).Grow(title, 1)
	top.Cross, top.Gap = widget.CrossCenter, widget.ControlGap
	return top
}

// bedCard is a bed on the first screen: its name and status, its moisture as one big figure, and its buttons.
type bedCard struct {
	*widget.Card
	name, figure, word *widget.Label
	mark               *widget.Icon
	water              *widget.Button
}

func newBedCard(p *panel, i int, b Bed) *bedCard {
	c := &bedCard{
		name:   widget.NewLabel(""),
		figure: widget.NewLabel(""),
		word:   widget.NewLabel(""),
		mark:   widget.NewIcon(icon.CircleCheck, ""),
		water:  widget.NewButton("Water now"),
	}
	c.name.Face = widget.BoldFont
	c.figure.Size, c.figure.Face = widget.FigureSize, widget.BoldFont
	c.water.Icon, c.water.Kind = icon.Droplets, widget.ButtonPrimary
	c.water.OnClick = widget.Sends(WaterNow{Bed: i})
	more := widget.NewButton("Schedule")
	more.OnClick = func(u *gunim.UI) gunim.Intent {
		p.open(i, u)
		return nil
	}
	state := widget.Row(c.mark, c.word)
	state.Cross = widget.CrossCenter
	head := widget.Row(c.name, widget.NewSpacer(), state).Grow(c.name, 1)
	head.Cross = widget.CrossCenter
	buttons := widget.Row(c.water, more)
	gap := widget.NewSpacer()
	body := widget.Column(head, c.figure, caption("Soil moisture"), gap, buttons).Grow(gap, 1)
	body.Cross = widget.CrossStretch
	c.Card = widget.NewCard(body)
	c.show(b, nil)
	return c
}

// show brings the card up to date with b.
func (c *bedCard) show(b Bed, u *gunim.UI) {
	word, ic, ink := status(b)
	c.name.Text = b.Name
	c.figure.Text = percent(b.Moisture)
	c.word.Text, c.word.Color = word, ink
	c.mark.Icon, c.mark.Color = ic, ink
	c.water.Disabled = b.Watering
	u.Invalidate()
}

// bedScreen is a bed's own screen: its schedule and its name.
type bedScreen struct {
	bed     int
	root    gunim.Node
	title   *widget.Label
	morning *widget.Switch
	minutes *widget.Slider
	value   *widget.Label
	name    *widget.TextField
}

func newBedScreen(p *panel, i int, b Bed) *bedScreen {
	s := &bedScreen{
		bed:     i,
		title:   heading(""),
		morning: widget.NewSwitch("Water each morning"),
		minutes: widget.NewSlider(1, 20),
		value:   widget.NewLabel(""),
		name:    widget.NewTextField(),
	}
	top := topBar("Plants", s.title, func(u *gunim.UI) { p.stack.Pop(u) })
	s.morning.OnChange = func(on bool, _ *gunim.UI) gunim.Intent { return SetMorning{Bed: i, On: on} }
	s.minutes.Label, s.minutes.Snap = "Minutes of water", 1
	s.minutes.OnChange = func(v float32, u *gunim.UI) gunim.Intent {
		s.value.Text = minutes(int(math.Round(float64(v))))
		u.Invalidate()
		return nil
	}
	s.minutes.OnCommit = func(v float32, _ *gunim.UI) gunim.Intent {
		return SetMinutes{Bed: i, Minutes: int(math.Round(float64(v)))}
	}
	length := widget.Row(widget.NewLabel("Water for"), s.minutes, s.value).Grow(s.minutes, 1)
	length.Cross = widget.CrossCenter
	s.name.Placeholder = "The bed's name"
	s.name.OnCommit = func(text string, _ *gunim.UI) gunim.Intent { return Rename{Bed: i, Name: text} }
	named := widget.Row(widget.NewLabel("Name"), s.name).Grow(s.name, 1)
	named.Cross = widget.CrossCenter
	// The name comes first: with the keyboard up for it, nothing lies half scrolled away above it.
	body := widget.Column(named, s.morning, length)
	body.Cross = widget.CrossStretch
	scroll := widget.NewScroll(body)
	page := widget.Column(top, scroll).Grow(scroll, 1)
	page.Cross = widget.CrossStretch
	s.root = widget.NewPad(page)
	s.show(b, nil)
	s.name.SetText(b.Name, nil)
	return s
}

// show brings the screen up to date with b.
func (s *bedScreen) show(b Bed, u *gunim.UI) {
	s.title.Text = b.Name
	s.morning.SetChecked(b.Morning, u)
	s.minutes.SetValue(float32(b.Minutes), u)
	s.value.Text = minutes(b.Minutes)
}

// minutes says a number of minutes.
func minutes(n int) string {
	if n == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", n)
}

// pinScreen is the PIN pad that opens the settings. A wrong PIN holds the pad for three seconds.
func (p *panel) pinScreen() gunim.Node {
	pad := widget.NewPinPad(len(pin), "Unlock")
	pad.Label = "PIN for the settings"
	pad.OnCommit = func(typed []byte, u *gunim.UI) gunim.Intent {
		// The pad wipes typed as this returns: it is checked here, and kept nowhere.
		if subtle.ConstantTimeCompare(typed, []byte(pin)) != 1 {
			pad.SetWaiting(3*time.Second, u)
			return nil
		}
		// The PIN pad gives way to the settings: they replace it, so going back from them, by the button or the
		// swipe, lands on the beds.
		p.stack.Pop(u)
		p.stack.Push(p.settingsScreen(), u)
		return nil
	}
	top := topBar("Plants", heading("Enter the PIN"), func(u *gunim.UI) { p.stack.Pop(u) })
	side := widget.Column(top, caption("The PIN is 2468 in this example."))
	side.Cross, side.Gap = widget.CrossStretch, widget.ControlGap
	page := widget.Row(side, widget.NewSpacer(), pad).Grow(side, 1)
	return widget.NewPad(page)
}

// settingsScreen is what the PIN opens: a reset of every bed, which takes a hold to confirm.
func (p *panel) settingsScreen() gunim.Node {
	reset := widget.NewHoldButton("Reset all beds")
	reset.Icon = icon.RotateCcw
	reset.OnClick = widget.Sends(ResetAll{})
	// Back goes to the beds, and locks the settings again.
	top := topBar("Plants", heading("Settings"), func(u *gunim.UI) { p.stack.Pop(u) })
	words := widget.NewLabel("Resetting puts both beds back as they came: their names, their schedules and their " +
		"moisture. Hold the button down until it fills.")
	page := widget.Column(top, words, widget.Row(reset))
	page.Cross, page.Gap = widget.CrossStretch, widget.ControlGap
	return widget.NewPad(page)
}
