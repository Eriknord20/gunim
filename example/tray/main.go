// Command tray shows an icon in the tray, and a flyout beside it on a click: a card of the application's state that
// takes the keyboard and closes once the user turns to something else. The flyout opens beside the icon on Windows,
// and in the corner of the screen where the system does not say where the icon is.
//
//	go run ./example/tray
//	go run ./example/tray -open -shot flyout.png
package main

import (
	"context"
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// Quit travels when the user presses Quit in the flyout, and Dismiss when they press Escape in it.
type (
	Quit    struct{}
	Dismiss struct{}
)

func init() {
	gunim.RegisterType[Quit]("tray.quit")
	gunim.RegisterType[Dismiss]("tray.dismiss")
}

// margin is the room round the flyout's card for its shadow.
const margin = 24

func main() {
	open := flag.Bool("open", false, "open the flyout at once, as a click on the icon does")
	shot := flag.String("shot", "", "with -open, write the flyout to this PNG file after a second, and quit")
	flag.Parse()
	if err := run(*open, *shot); err != nil {
		log.Fatal(err)
	}
}

func run(open bool, shot string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return gunim.Main(ctx, func(a *gunim.App) error {
		ctx, quit := context.WithCancel(ctx)
		defer quit()
		a.StayOpen(true)
		defer a.StayOpen(false)
		var mu sync.Mutex
		var flyout *gunim.Window
		show := func() {
			mu.Lock()
			defer mu.Unlock()
			if flyout != nil {
				return
			}
			// Where the system cannot say where the icon is, the flyout goes to the corner of the screen.
			beside, _ := a.TrayRect()
			w, err := a.NewWindow(gunim.WindowOptions{Title: "Home server", Size: geom.Sz(320+2*margin, 170+2*margin),
				Kind: driver.KindFlyout, Beside: beside, Margin: margin})
			if err != nil {
				log.Printf("tray: %v", err)
				return
			}
			gunim.RegisterView(w, "flyout", func(struct{}) *card { return newCard(status()) }, nil)
			if err := w.Client().Mount(gunim.Root, "flyout", "flyout", struct{}{}); err != nil {
				log.Printf("tray: %v", err)
				w.Client().Close()
				return
			}
			flyout = w
			go func() {
				for in := range w.Client().Intents() {
					switch in.Intent.(type) {
					case Quit:
						quit()
					case Dismiss:
						w.Client().Close()
					}
				}
				mu.Lock()
				flyout = nil
				mu.Unlock()
			}()
			if shot != "" {
				go writeShot(ctx, w, shot, quit)
			}
		}
		err := a.SetTray(gunim.Tray{Icon: []image.Image{berry(32)}, Tooltip: "Home server: you're up to date",
			Items: []gunim.TrayItem{{Title: "Quit", ID: 1}}, OnPick: func(int) { quit() }, OnClick: show})
		if err != nil {
			return err
		}
		defer func() { _ = a.SetTray(gunim.Tray{}) }()
		if open {
			show()
		}
		<-ctx.Done()
		return nil
	})
}

// status is the flyout's content: how the server stands, and a button to quit.
func status() gunim.Node {
	title := widget.NewLabel("Home server")
	title.Size = widget.HeadingSize
	bar := widget.NewProgressBar()
	bar.SetValue(1, nil)
	q := widget.NewButton("Quit")
	q.OnClick = widget.Sends(Quit{})
	col := widget.Column(title, widget.NewLabel("You're up to date."), bar, widget.Row(q))
	col.Cross = widget.CrossStretch
	return widget.NewPad(col)
}

// writeShot writes w to path a second after it opens, and ends the program.
func writeShot(ctx context.Context, w *gunim.Window, path string, quit func()) {
	defer quit()
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Second):
	}
	img, err := w.Client().Shot(ctx)
	if err != nil {
		log.Printf("tray: shot: %v", err)
		return
	}
	f, err := os.Create(path)
	if err != nil {
		log.Printf("tray: shot: %v", err)
		return
	}
	if err := png.Encode(f, img); err != nil {
		log.Printf("tray: shot: %v", err)
	}
	if err := f.Close(); err != nil {
		log.Printf("tray: shot: %v", err)
	}
}

// berry draws the tray icon: a round berry in the accent's blue, size pixels across.
func berry(size int) image.Image {
	m := image.NewNRGBA(image.Rect(0, 0, size, size))
	c := float64(size) / 2
	for y := range size {
		for x := range size {
			dx, dy := float64(x)+0.5-c, float64(y)+0.5-c
			if d := dx*dx + dy*dy; d <= (c-1)*(c-1) {
				m.Set(x, y, color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff})
			}
		}
	}
	return m
}

// card draws its child on a rounded card with a shadow, margin in from the flyout's edge.
type card struct{ child gunim.Node }

func newCard(child gunim.Node) *card { return &card{child: child} }

// Children implements [gunim.Composite].
func (c *card) Children() []gunim.Node { return []gunim.Node{c.child} }

// Layout implements [gunim.Node]: the child fills the card.
func (c *card) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	in := geom.Sz(max(0, cs.Max.W-2*margin), max(0, cs.Max.H-2*margin))
	kids.At(0).Layout(gunim.Tight(in))
	kids.At(0).Place(geom.Pt(margin, margin))
	return cs.Max
}

// Handle implements [gunim.Handler]: Escape closes the flyout, which is the application's to do.
func (c *card) Handle(e input.Event, u *gunim.UI) bool {
	if k, ok := e.(input.KeyPress); ok && k.Key == input.KeyEscape {
		u.Send(c, Dismiss{})
		return true
	}
	return false
}

// Paint implements [gunim.Node].
func (c *card) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rc(margin, margin, box.W-2*margin, box.H-2*margin)
	p.ShadowRRect(r, widget.CardRadius.Get(f.Theme), paint.Solid(widget.CardFill.Get(f.Theme)),
		paint.Shadow{Offset: geom.Pt(0, 8), Blur: 16, Color: widget.MenuShadow.Get(f.Theme)})
	kids.At(0).Paint(p)
}
