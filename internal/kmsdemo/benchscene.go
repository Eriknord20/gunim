//go:build linux && (arm64 || amd64)

package kmsdemo

import (
	"context"
	"fmt"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/kms"
	"github.com/marrasen/gunim/driver/kms/kmsbench"
	"github.com/marrasen/gunim/internal/gallery"
	"github.com/marrasen/gunim/widget"
)

// BenchScene is the bench's scene for screen: "demo", the test pattern,
// with a finger drawing circles, taps and two fingers dragging; or
// "widgets", the gallery of example/widgets, with drags that fling it up
// and down, started with the warm-up so its first scroll, which shapes text
// never shown before, is not measured. still keeps the demo still while
// the script plays, so only the fingers draw. logf hears the gallery's
// errors.
func BenchScene(screen string, still bool, logf func(string)) (kmsbench.Scene, error) {
	open := func(a *gunim.App) (*gunim.Window, gunim.Client, error) {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "kmsbench", Root: widget.NewSurface(), Theme: widget.Dark()})
		if err != nil {
			return nil, gunim.Client{}, err
		}
		c := w.Client()
		return w, c, nil
	}
	switch screen {
	case "", "demo":
		return kmsbench.Scene{Name: "demo",
			Script: func(_ int, d time.Duration) []byte { return kmsbench.TouchScript(d) },
			Open: func(_ context.Context, a *gunim.App, d *kms.Driver) (gunim.Client, func() error, error) {
				w, c, err := open(a)
				if err != nil {
					return c, nil, err
				}
				Register(w, Options{Scale: d.Monitors()[0].Scale, Software: d.Software(), FrameMS: func() float64 { return float64(d.Stats().Last.Microseconds()) / 1000 }})
				if err := c.TakeFingers(true); err != nil {
					return c, nil, err
				}
				if err := c.Mount(gunim.Root, "demo", View, State{Still: still}); err != nil {
					return c, nil, err
				}
				go func() {
					for range c.Intents() {
					}
				}()
				return c, func() error { return c.Update("demo", State{Still: true}) }, nil
			}}, nil
	case "widgets":
		return kmsbench.Scene{Name: "widgets", WarmTouch: true,
			Open: func(ctx context.Context, a *gunim.App, _ *kms.Driver) (gunim.Client, func() error, error) {
				w, c, err := open(a)
				if err != nil {
					return c, nil, err
				}
				gallery.Register(w)
				go func() {
					if err := gallery.Serve(ctx, c, "", 0); err != nil && logf != nil {
						logf(fmt.Sprintf("the gallery: %v", err))
					}
				}()
				return c, nil, nil
			}}, nil
	}
	return kmsbench.Scene{}, fmt.Errorf("kmsbench: no screen %q: want demo or widgets", screen)
}
