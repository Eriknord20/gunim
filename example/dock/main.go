// Command dock shows package widget/dock: views in tabs and splits,
// over as many windows as the user drags them into.
//
// Drag a tab along its bar to move it, onto another bar to join that
// group, to the edge of a pane to split it, into another window to move
// there, or out of every window to open one of its own. The + adds a
// pane and the × closes one. Drag a divider to share the room
// differently, and double click it to even it out. Ctrl+Page Down and
// Ctrl+Page Up step through the tabs, Ctrl+Tab through the panes.
//
//	CGO_ENABLED=0 go run ./example/dock
//	CGO_ENABLED=0 go run ./example/dock -tabs
//
// By default each pane has its bar of tabs, as an editor's do. With
// -tabs the window has one bar, in its title bar, and each tab holds a
// set of panes, as a terminal's do.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/gunim/widget/dock"
)

func main() {
	runFor := flag.Duration("for", 0, "quit after this long; zero runs until the windows close")
	tabs := flag.Bool("tabs", false, "one bar of tabs in the title bar, each tab a set of panes")
	shot := flag.String("shot", "", "write the first window to this PNG file after -after, and quit")
	after := flag.Duration("after", 1500*time.Millisecond, "how long -shot waits")
	flag.Parse()
	if err := run(*runFor, *tabs, *shot, *after); err != nil {
		log.Fatal(err)
	}
}

func run(runFor time.Duration, tabs bool, shot string, after time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runFor)
		defer cancel()
	}
	err := gunim.Main(ctx, func(a *gunim.App) error {
		s := newShow(a, tabs)
		if err := s.open(0, nil, geom.Point{}, geom.Sz(1100, 700)); err != nil {
			return err
		}
		if shot != "" {
			go func() {
				select {
				case <-time.After(after):
				case <-ctx.Done():
					return
				}
				if err := writeShot(ctx, s.wins[0].c, shot); err != nil {
					log.Print(err)
				}
				s.in <- quit{}
			}()
		}
		return s.serve(ctx)
	})
	if errors.Is(err, driver.ErrNoDriver) {
		log.Print("gunim has no driver for this operating system yet")
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// writeShot writes what a window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// The application's half: the layout, and a window for each of its
// windows.

// show is the application: the layout, and its windows.
type show struct {
	a      *gunim.App
	tabs   bool
	l      dock.Layout
	wins   map[int]*win
	in     chan any
	panes  int
	closed bool
}

// win is one of the application's windows.
type win struct {
	id int
	c  gunim.Client
	gw *gunim.Window
}

// fromWin is an intent from window id, or word that it closed.
type fromWin struct {
	id     int
	in     gunim.Intent
	closed bool
}

// quit ends the application.
type quit struct{}

// The panes the show starts with.
var firstPanes = []string{"Inbox", "Calendar", "Notes", "Music", "Map", "Logs"}

func newShow(a *gunim.App, tabs bool) *show {
	s := &show{a: a, tabs: tabs, wins: map[int]*win{}, in: make(chan any, 16)}
	if tabs {
		s.l = dock.NewLayout(false, "Inbox", "Calendar", "Notes")
		s.l.Move("Calendar", dock.Target{At: "Inbox", Side: dock.Right})
		s.l.Add("Music", dock.Target{Index: -1})
		s.l.Add("Map", dock.Target{At: "Music", Side: dock.Bottom})
		s.l.Add("Logs", dock.Target{Index: -1})
		s.l.Show("Inbox")
	} else {
		s.l = dock.NewLayout(true, firstPanes...)
		s.l.Move("Notes", dock.Target{At: "Inbox", Side: dock.Right})
		s.l.Move("Music", dock.Target{At: "Notes", Index: -1})
		s.l.Move("Logs", dock.Target{At: "Inbox", Side: dock.Bottom})
		s.l.Show("Inbox")
	}
	s.panes = len(firstPanes)
	return s
}

// open opens window id, placed at the point at in parent's space, size
// large.
func (s *show) open(id int, parent *gunim.Window, at geom.Point, size geom.Size) error {
	o := gunim.WindowOptions{Title: "Dock", Size: size, Parent: parent, Anchor: at, ZoomKeys: true}
	gw, err := s.a.NewWindow(o)
	if err != nil {
		return fmt.Errorf("dock: %w", err)
	}
	registerViews(gw, id, s.tabs)
	c := gw.Client()
	if err := c.Mount(gunim.Root, "win", "win", s.l, "layout"); err != nil {
		return err
	}
	s.wins[id] = &win{id: id, c: c, gw: gw}
	go func() {
		for env := range c.Intents() {
			s.in <- fromWin{id: id, in: env.Intent}
		}
		s.in <- fromWin{id: id, closed: true}
	}()
	return nil
}

// serve carries out what the windows ask for, until the last closes.
func (s *show) serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m := <-s.in:
			switch m := m.(type) {
			case quit:
				for _, w := range s.wins {
					w.c.Close()
				}
				return nil
			case fromWin:
				if m.closed {
					if !s.closed {
						s.windowClosed(m.id)
					}
				} else {
					s.carryOut(m.in)
				}
			}
			if len(s.wins) == 0 {
				return nil
			}
		}
	}
}

// carryOut carries out intent in, from one of the windows.
func (s *show) carryOut(in gunim.Intent) {
	switch in := in.(type) {
	case dock.Detach:
		from := s.wins[in.From]
		if from == nil {
			return
		}
		next := s.l.Clone()
		wid, ok := next.ToWindow(in.ID, nil)
		if !ok {
			return
		}
		s.l = next
		if err := s.open(wid, from.gw, in.At, in.Size); err != nil {
			log.Print(err)
		}
	case dock.New:
		s.panes++
		name := "Pane " + strconv.Itoa(s.panes)
		to := dock.Target{At: in.Group, Win: in.Win, Index: -1}
		if in.Group == "" {
			to = dock.Target{Win: in.Win, Index: -1}
		}
		s.l.Add(name, to)
	case dock.Change:
		if !s.l.Apply(in) {
			return
		}
	default:
		return
	}
	s.publish()
}

// windowClosed puts the panes of a window the user closed back in the
// first window, or ends the show when it was the first.
func (s *show) windowClosed(id int) {
	delete(s.wins, id)
	if id == 0 {
		s.closed = true
		for _, w := range s.wins {
			w.c.Close()
		}
		return
	}
	for _, it := range s.l.CloseWindow(id) {
		s.l.Add(it, dock.Target{Index: -1})
	}
	s.publish()
}

// publish sends the layout to every window, and closes those it has no
// more.
func (s *show) publish() {
	for id, w := range s.wins {
		if s.l.Window(id) == nil {
			delete(s.wins, id)
			w.c.Close()
			continue
		}
		_ = w.c.Publish("layout", s.l)
	}
}

// The windows' half: a dock filling the window, its own title bar over
// it.

func registerViews(w *gunim.Window, id int, tabs bool) {
	gunim.RegisterView(w, "win", func(dock.Layout) *frame { return newFrame(id, tabs) },
		func(f *frame, l dock.Layout, u *gunim.UI) { f.d.SetLayout(l, u) })
}

// frame is a window: its title row and the dock.
type frame struct {
	d        *dock.Dock
	controls *widget.WindowControls
	title    *widget.WindowTitle
	tabs     bool
}

func newFrame(id int, tabs bool) *frame {
	f := &frame{d: dock.NewDock(id), controls: widget.NewWindowControls(), tabs: tabs}
	f.d.Content = func(item string) gunim.Node { return newCard(item) }
	f.d.Face = face
	f.d.Plus, f.d.Keys = true, true
	if tabs {
		f.d.TitleBar = true
		f.d.TopBar().Caption = true
	} else {
		f.title = widget.NewWindowTitle("Dock")
	}
	return f
}

// face is how a pane's tab looks: an icon and the pane's name, or for a
// tab of several panes, the first one's and how many more.
func face(n *dock.Node) dock.Face {
	items := n.Items()
	if len(items) == 0 {
		return dock.Face{Title: n.ID}
	}
	f := dock.Face{Title: items[0], Icon: iconOf(items[0]), Closable: true}
	if len(items) > 1 {
		f.More = "+" + strconv.Itoa(len(items)-1)
		f.Icon = icon.Columns2
	}
	return f
}

func iconOf(item string) *icon.Icon {
	switch item {
	case "Inbox":
		return icon.Inbox
	case "Calendar":
		return icon.Calendar
	case "Notes":
		return icon.NotebookPen
	case "Music":
		return icon.Music
	case "Map":
		return icon.Map
	case "Logs":
		return icon.ScrollText
	}
	return icon.SquareTerminal
}

// Children implements [gunim.Composite].
func (f *frame) Children() []gunim.Node {
	kids := []gunim.Node{f.d}
	if f.tabs {
		kids = append(kids, f.d.TopBar())
	} else {
		kids = append(kids, f.title)
	}
	return append(kids, f.controls)
}

// titleH is how tall the title row is.
const titleH = 38

// Layout implements [gunim.Node].
func (f *frame) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	d, head, controls := kids.At(0), kids.At(1), kids.At(2)
	cs := controls.Layout(gunim.Constraints{Max: geom.Sz(size.W, titleH)})
	controls.Place(geom.Pt(size.W-cs.W, 0))
	head.Layout(gunim.Tight(geom.Sz(max(size.W-cs.W-12, 0), titleH)))
	head.Place(geom.Pt(8, 0))
	d.Layout(gunim.Tight(geom.Sz(max(size.W-16, 0), max(size.H-titleH-8, 0))))
	d.Place(geom.Pt(8, titleH))
	return size
}

// Paint implements [gunim.Node].
func (f *frame) Paint(p *paint.Painter, fr gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.MenubarFill.Get(fr.Theme)))
	for k := range kids.All {
		k.Paint(p)
	}
}

// card is a pane: its name on a tinted card.
type card struct {
	name  string
	title *widget.Label
	hint  *widget.Label
}

func newCard(name string) *card {
	c := &card{name: name, title: widget.NewLabel(name), hint: widget.NewLabel("Drag my tab to move me")}
	c.title.Size = widget.HeadingSize
	c.hint.Color = widget.Placeholder
	return c
}

// Children implements [gunim.Composite].
func (c *card) Children() []gunim.Node { return []gunim.Node{c.title, c.hint} }

// Layout implements [gunim.Node].
func (c *card) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := cs.Max
	t := kids.At(0).Layout(gunim.Constraints{Max: size})
	h := kids.At(1).Layout(gunim.Constraints{Max: size})
	y := (size.H - t.H - h.H - 6) / 2
	kids.At(0).Place(geom.Pt((size.W-t.W)/2, y))
	kids.At(1).Place(geom.Pt((size.W-h.W)/2, y+t.H+6))
	return size
}

// Paint implements [gunim.Node].
func (c *card) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	hue := hueOf(c.name)
	top, bottom := hue, hue
	top.A, bottom.A = 0x44, 0x14
	p.RRect(r, 12, paint.Solid(widget.CardFill.Get(f.Theme)))
	p.RRect(r, 12, paint.Fill{Gradient: &paint.Gradient{From: r.Min, To: geom.Pt(r.Min.X, r.Max.Y), Start: top, End: bottom}})
	for k := range kids.All {
		k.Paint(p)
	}
}

// hueOf gives each pane a colour of its own.
func hueOf(name string) color.NRGBA {
	hues := []color.NRGBA{
		{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff},
		{R: 0xff, G: 0x8a, B: 0x5e, A: 0xff},
		{R: 0x6c, G: 0xd4, B: 0x8a, A: 0xff},
		{R: 0xc7, G: 0x7d, B: 0xff, A: 0xff},
		{R: 0xff, G: 0xc8, B: 0x4a, A: 0xff},
		{R: 0x4a, G: 0xd6, B: 0xd0, A: 0xff},
	}
	sum := 0
	for _, r := range name {
		sum = sum*31 + int(r)
	}
	sum = max(sum, -sum)
	return hues[sum%len(hues)]
}
