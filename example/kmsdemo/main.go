//go:build linux && (arm64 || amd64)

// Command kmsdemo shows gunim's KMS driver at work on a screen with no
// window system, as a Raspberry Pi with a Touch Display 2: an animated
// test pattern, a dot under each finger, and the frame rate, frame times
// and touch latency on the screen and on stdout once a second. A
// three-finger hold of three seconds, or Ctrl+C, ends it and gives the
// screen back to the text console.
//
// Build it on any machine, copy it over, and run it from SSH:
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./example/kmsdemo
//	sudo ./kmsdemo -rotate 90 -vt free
//
// It needs Mesa's libraries on the Pi: apt install libgbm1 libegl1
// libegl-mesa0 libgles2.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/internal/kmsdemo"
	"github.com/marrasen/gunim/widget"
)

func main() {
	var o kms.Options
	flag.IntVar(&o.Rotate, "rotate", 0, "turn the picture clockwise: 0, 90, 180 or 270")
	flag.StringVar(&o.VT, "vt", "", `the VT to draw on: empty for the one it runs on, "free", or a number`)
	flag.StringVar(&o.Connector, "connector", "", `the screen, as "DSI-2" or "HDMI-A-1"; empty finds it`)
	var scale, brightness float64
	flag.Float64Var(&scale, "scale", 0, "device pixels per logical pixel; 0 works it out")
	flag.Float64Var(&brightness, "brightness", 0, "the backlight from 0 to 1 while it runs; 0 keeps it")
	flag.BoolVar(&o.Software, "software", false, "draw on the CPU even where there is a GPU")
	flag.BoolVar(&o.VTSwitch, "vtswitch", false, "take Ctrl+Alt+Fn from a keyboard to switch VT")
	flag.StringVar(&o.Record, "record", "", "write the touch screen's events to this file")
	replay := flag.String("replay", "", "play a recording through the driver in place of the touch screen")
	still := flag.Bool("still", false, "start with nothing moving")
	flag.Parse()
	o.Scale, o.Brightness = float32(scale), float32(brightness)
	if *replay != "" {
		f, err := os.Open(*replay)
		if err != nil {
			log.Fatal(err)
		}
		o.Replay = f
	}
	if err := run(o, *still); err != nil {
		log.Fatal(err)
	}
}

func run(o kms.Options, still bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	d, err := kms.Open(o)
	if err != nil {
		return err
	}
	figures := newFigures(d)
	err = gunim.MainWith(ctx, d, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "kmsdemo", Root: widget.NewSurface(), Theme: widget.Dark()})
		if err != nil {
			return err
		}
		kmsdemo.Register(w, kmsdemo.Options{
			Scale:    d.Monitors()[0].Scale,
			Figures:  figures.line,
			FrameMS:  func() float64 { return float64(d.Stats().Last.Microseconds()) / 1000 },
			Software: d.Software(),
			Exit:     stop,
		})
		c := w.Client()
		if err := c.TakeFingers(true); err != nil {
			return err
		}
		if err := c.Mount(gunim.Root, "demo", kmsdemo.View, kmsdemo.State{Still: still}); err != nil {
			return err
		}
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					// The window closes, which ends its intents and the app.
					c.Close()
					return
				case <-t.C:
					fmt.Println(figures.line())
				}
			}
		}()
		for range c.Intents() {
		}
		return c.Err()
	})
	<-d.Closed()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// figures makes the line of figures for the last second.
type figures struct {
	d    *kms.Driver
	prev kms.Stats
	at   time.Time
	last string
}

func newFigures(d *kms.Driver) *figures { return &figures{d: d, at: time.Now()} }

// line returns the figures since it was last asked, at most once a second;
// asked sooner, it returns the line before.
func (f *figures) line() string {
	now := time.Now()
	if now.Sub(f.at) < 900*time.Millisecond && f.last != "" {
		return f.last
	}
	s := f.d.Stats()
	fps := float64(s.Frames-f.prev.Frames) / now.Sub(f.at).Seconds()
	// Slow frames count: their missed vblanks are missed flips, and their
	// times are frame times.
	f.last = fmt.Sprintf("%.1f fps · %d missed, %d slow · frame p50 %.1f ms p99 %.1f ms · touch to flip p50 %.1f ms p99 %.1f ms",
		fps, s.Missed+s.LateMissed, s.Late, ms(s.All.P50), ms(s.All.P99), ms(s.Touch.P50), ms(s.Touch.P99))
	f.prev, f.at = s, now
	return f.last
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
