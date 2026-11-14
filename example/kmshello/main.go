// Command kmshello is a plant-watering panel built with gunim's touch kit: the pattern a project on a Raspberry Pi
// with a Touch Display 2 copies. It shows two beds on cards, each with its moisture, a status in words and colour,
// and a button that waters it now. A bed's own screen has a switch, a slider, a field for its name that brings up
// the keyboard on the screen, and goes back by a swipe from the left edge. The settings sit behind a PIN pad
// (2468), and resetting every bed takes a hold to confirm.
//
// On a Pi it draws full screen through driver/kms. Build it on any machine, copy it over, and run it from SSH:
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o kmshello ./example/kmshello
//	./kmshello -rotate 90 -vt free
//
// On a desktop it opens a window the size of the 5" Touch Display 2 in landscape, 731 by 411, worked with a
// mouse:
//
//	go run ./example/kmshello
//
// -light draws it in the light theme, and -shot file.png saves a picture of it -wait after it opens, and ends, for
// documentation. -show opens it on a screen: "bed", "name" (with the keyboard up), "pin" or "settings"; "hold"
// opens the settings and lays a finger on the reset button, which -wait 2.5s shows done. -show tour walks the
// screens by itself, a step a second, for measuring frames on the hardware with GUNIM_DEBUG_FRAMES=1.
package main

import (
	"context"
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The vocabulary the two halves share.
type (
	// Plants is what the panel shows.
	Plants struct {
		Beds []Bed
		// Show is the screen to open on, for a picture of it: "", "bed", "name", "pin" or "settings"; "hold" opens
		// the settings and holds the reset button down. "tour" walks the screens one step a second, for measuring
		// how the panel draws while it moves.
		Show string
	}
	// Bed is one bed: its name, how moist its soil is in per cent, whether it is being watered, whether it is
	// watered each morning, and for how many minutes.
	Bed struct {
		Name     string
		Moisture int
		Watering bool
		Morning  bool
		Minutes  int
	}
	// WaterNow travels when a bed's Water now is pressed.
	WaterNow struct{ Bed int }
	// SetMorning travels when a bed's morning switch flips.
	SetMorning struct {
		Bed int
		On  bool
	}
	// SetMinutes travels when a bed's slider is let go.
	SetMinutes struct{ Bed, Minutes int }
	// Rename travels when a bed's name is committed.
	Rename struct {
		Bed  int
		Name string
	}
	// ResetAll travels when the hold to reset every bed completes.
	ResetAll struct{}
)

func init() {
	gunim.RegisterType[Plants]("kmshello.plants")
	gunim.RegisterType[WaterNow]("kmshello.water")
	gunim.RegisterType[SetMorning]("kmshello.morning")
	gunim.RegisterType[SetMinutes]("kmshello.minutes")
	gunim.RegisterType[Rename]("kmshello.rename")
	gunim.RegisterType[ResetAll]("kmshello.reset")
}

// options are the command's flags that do not belong to the screen.
type options struct {
	light bool
	shot  string
	show  string
	wait  time.Duration
	size  geom.Size
}

func main() {
	var o options
	var k kmsFlags
	flag.BoolVar(&o.light, "light", false, "draw in the light theme")
	flag.StringVar(&o.shot, "shot", "", "save a picture of the panel to this PNG file, and end")
	flag.StringVar(&o.show, "show", "", `open on a screen, for a picture: "bed", "name", "pin" or "settings"; `+
		`"hold" holds the reset button down; "tour" walks the screens`)
	flag.DurationVar(&o.wait, "wait", 1500*time.Millisecond, "how long after it opens -shot takes its picture")
	desktop := flag.Bool("window", !kmsDefault(), "open a window on the desktop, not the full screen through KMS")
	k.add()
	flag.Parse()
	o.size = geom.Sz(731, 411)
	if err := start(o, k, *desktop); err != nil {
		log.Fatal(err)
	}
}

// start runs the panel in a window on the desktop, or full screen through KMS, until it is closed or stopped.
func start(o options, k kmsFlags, desktop bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	fn := func(a *gunim.App) error { return run(ctx, a, o) }
	var err error
	if desktop {
		err = gunim.Main(ctx, fn)
	} else {
		err = runKMS(ctx, k, fn)
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// kmsDefault reports whether the panel draws through KMS unless told otherwise: on Linux with no desktop to open a
// window on.
func kmsDefault() bool {
	return kmsBuilt && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
}

// run opens the panel's window, and works the beds until ctx ends or the window closes.
func run(ctx context.Context, a *gunim.App, o options) error {
	th := widget.Dark()
	if o.light {
		th = widget.Light()
	}
	w, err := a.NewWindow(gunim.WindowOptions{
		Title: "Plants",
		Size:  o.size,
		Root:  widget.NewSurface(),
		Theme: th.With(widget.Touch()...),
	})
	if err != nil {
		return err
	}
	gunim.RegisterView(w, "plants", newPanel, (*panel).show)
	c := w.Client()
	g := newGarden()
	if err := c.Mount(gunim.Root, "plants", "plants", Plants{Beds: g.beds(), Show: o.show}, "plants"); err != nil {
		return err
	}
	if o.shot != "" {
		go shoot(ctx, c, o.shot, o.wait)
	}
	if o.show == "hold" {
		go holdReset(ctx, c)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Close()
			return nil
		case e, ok := <-c.Intents():
			if !ok {
				return c.Err()
			}
			g.handle(e.Intent)
		case now := <-tick.C:
			if !g.step(now) {
				continue
			}
		}
		if err := c.Publish("plants", Plants{Beds: g.beds()}); err != nil {
			return err
		}
	}
}

// shoot saves a picture of the window to path after wait, once its screens have settled, and closes it.
func shoot(ctx context.Context, c gunim.Client, path string, wait time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	defer c.Close()
	img, err := c.Shot(ctx)
	if err != nil {
		log.Print(err)
		return
	}
	f, err := os.Create(path)
	if err != nil {
		log.Print(err)
		return
	}
	err = png.Encode(f, img)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Print(err)
	}
}

// holdReset lays a finger on the reset button half a second after the panel opens, and keeps it there, for a
// picture of the button filling, or of it done with a long enough -wait.
func holdReset(ctx context.Context, c gunim.Client) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(500 * time.Millisecond):
	}
	tree, err := c.Access(ctx)
	if err != nil {
		log.Print(err)
		return
	}
	var find func(n *access.Node) *access.Node
	find = func(n *access.Node) *access.Node {
		if n.Role == access.RoleButton && n.Name == "Reset all beds" {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	b := find(tree.Root)
	if b == nil {
		log.Print("kmshello: no reset button to hold")
		return
	}
	at, now := b.Bounds.Center(), time.Now()
	for _, e := range []input.Event{
		input.PointerMove{Pos: at, Touch: true, Time: now},
		input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Touch: true, Time: now},
	} {
		if err := c.Input(ctx, e); err != nil {
			log.Print(err)
		}
	}
}

// garden is the app's side: the beds, drying and being watered as time passes.
type garden struct {
	bed     []Bed
	waterTo []time.Time
}

func newGarden() *garden {
	g := &garden{}
	g.reset()
	return g
}

// reset puts every bed back as it came.
func (g *garden) reset() {
	g.bed = []Bed{
		{Name: "Herbs", Moisture: 46, Morning: true, Minutes: 4},
		{Name: "Tomatoes", Moisture: 22, Minutes: 8},
	}
	g.waterTo = make([]time.Time, len(g.bed))
}

// beds returns a copy of the beds, to hand to the window.
func (g *garden) beds() []Bed { return append([]Bed(nil), g.bed...) }

// handle acts on an intent from the window.
func (g *garden) handle(in gunim.Intent) {
	switch in := in.(type) {
	case WaterNow:
		g.bed[in.Bed].Watering = true
		g.waterTo[in.Bed] = time.Now().Add(6 * time.Second)
	case SetMorning:
		g.bed[in.Bed].Morning = in.On
	case SetMinutes:
		g.bed[in.Bed].Minutes = in.Minutes
	case Rename:
		if in.Name != "" {
			g.bed[in.Bed].Name = in.Name
		}
	case ResetAll:
		g.reset()
	}
}

// step moves the beds on to now: a bed being watered grows moister, and one that is done stops. It reports
// whether anything changed.
func (g *garden) step(now time.Time) bool {
	changed := false
	for i := range g.bed {
		b := &g.bed[i]
		if !b.Watering {
			continue
		}
		b.Moisture = min(b.Moisture+4, 80)
		if now.After(g.waterTo[i]) {
			b.Watering = false
		}
		changed = true
	}
	return changed
}

// status is how a bed is: a word, an icon and a colour, the three together, so colour is never the only sign.
func status(b Bed) (word string, ic *icon.Icon, ink theme.Token[color.NRGBA]) {
	switch {
	case b.Watering:
		return "Watering", icon.Droplets, widget.Accent
	case b.Moisture >= 40:
		return "Fine", icon.CircleCheck, widget.ToastSuccessInk
	case b.Moisture >= 25:
		return "Water soon", icon.TriangleAlert, widget.ToastWarningInk
	}
	return "Dry", icon.CircleAlert, widget.ToastErrorInk
}

// percent is a moisture as the panel shows it.
func percent(m int) string { return fmt.Sprintf("%d %%", m) }
