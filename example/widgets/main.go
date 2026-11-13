// Command widgets is a gallery of gunim's widgets: a header row with a
// search field, and a scrolling list of cards, each a row with a label
// that wraps and a button. Typing in the field filters the cards, which
// collapse and grow back as they leave and return. The toggle switches
// between the dark and light themes, and every size, colour and motion
// animates to the new one. A row of icons shows them at 16 and 48 pixels,
// drawing themselves on, spinning, and in buttons, a link and a menu.
// Another card shows icons in tabs, a chip, a drop-down and rich text, a
// button opens a danger dialog, and a toast of each kind arrives at the
// start and when asked.
//
//	CGO_ENABLED=0 go run ./example/widgets
//	CGO_ENABLED=0 go run ./example/widgets -shot widgets.png
//
// Built with the gunimkms tag for 64-bit Linux, it draws full screen through
// driver/kms, as on a Raspberry Pi with a touch screen:
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags gunimkms ./example/widgets
//
// The gallery itself is internal/gallery, which tools/kmsbench measures too.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/cues"
	"github.com/marrasen/gunim/audio/speaker"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gallery"
	"github.com/marrasen/gunim/widget"
)

func main() {
	runFor := flag.Duration("for", 0, "quit after this long; zero runs until the window closes")
	shot := flag.String("shot", "", "write the window to this PNG file after -after, and quit")
	after := flag.Duration("after", 2*time.Second, "how long -shot waits")
	zoom := flag.Float64("zoom", 1, "zoom the window, as Ctrl with + and - does")
	sound := flag.Bool("sound", true, "play a sound as each widget is worked")
	flag.Parse()
	if err := run(*runFor, *shot, *after, float32(*zoom), *sound); err != nil {
		log.Fatal(err)
	}
}

func run(runFor time.Duration, shot string, after time.Duration, zoom float32, sound bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runFor)
		defer cancel()
	}
	return gunim.Main(ctx, func(a *gunim.App) error {
		if sound {
			// Opening a sound device can take a moment, so this doesn't hold the
			// window back with it.
			go func() {
				mix := audio.NewMixer()
				if _, err := speaker.Open(mix, speaker.Options{Name: "gunim widgets"}); err != nil {
					log.Printf("widgets: no sound: %v", err)
				} else {
					a.SetCues(cues.New(mix))
				}
			}()
		}
		w, err := a.NewWindow(gunim.WindowOptions{
			Title: "gunim widgets",
			Size:  geom.Sz(720, 720),
			Root:  widget.NewSurface(),
		})
		if err != nil {
			return err
		}
		gallery.Register(w)
		if zoom != 1 {
			if err := w.Client().SetZoom(zoom); err != nil {
				return err
			}
		}
		return gallery.Serve(ctx, w.Client(), shot, after)
	})
}
