// Command graph shows package widget/graph: modules of a light show,
// joined by wires.
//
// Drag a box to move it, or empty room to pan; the wheel pans too, and
// with Ctrl zooms. Drag from a port to join it to another of the same
// kind: sound, beats, levels or light. Drag from an input that has a
// wire to take its end elsewhere, or let it go on nothing to pull it
// out. Click a box or a wire to pick it, and press Delete to take it
// out; Shift and a drag on empty room picks boxes with a band. Home
// fits every box in view.
//
//	CGO_ENABLED=0 go run ./example/graph
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
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/gunim/widget/graph"
)

func main() {
	runFor := flag.Duration("for", 0, "quit after this long; zero runs until the window closes")
	shot := flag.String("shot", "", "write the window to this PNG file after -after, and quit")
	after := flag.Duration("after", 1500*time.Millisecond, "how long -shot waits")
	flag.Parse()
	if err := run(*runFor, *shot, *after); err != nil {
		log.Fatal(err)
	}
}

func run(runFor time.Duration, shot string, after time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runFor)
		defer cancel()
	}
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: "Graph", Size: geom.Sz(1200, 720), SystemFrame: true})
		if err != nil {
			return fmt.Errorf("graph: %w", err)
		}
		gunim.RegisterView(w, "graph", func(graph.Graph) *graph.View { return newView() },
			func(v *graph.View, g graph.Graph, u *gunim.UI) { v.SetGraph(g, u) })
		c := w.Client()
		g := show()
		if err := c.Mount(gunim.Root, "graph", "graph", g, "graph"); err != nil {
			return err
		}
		if shot != "" {
			go func() {
				select {
				case <-time.After(after):
				case <-ctx.Done():
					return
				}
				if err := writeShot(ctx, c, shot); err != nil {
					log.Print(err)
				}
				c.Close()
			}()
		}
		for env := range c.Intents() {
			if ch, ok := env.Intent.(graph.Change); ok && g.Apply(ch) {
				_ = c.Publish("graph", g.Clone())
			}
		}
		return nil
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

// writeShot writes what the window shows to a PNG file.
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

// The kinds of port, and their looks.
var (
	sound = color.NRGBA{R: 0x4f, G: 0xd6, B: 0xc0, A: 0xff}
	beats = color.NRGBA{R: 0xff, G: 0xc8, B: 0x4a, A: 0xff}
	level = color.NRGBA{R: 0xc7, G: 0x7d, B: 0xff, A: 0xff}
	light = color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0xff}
)

func soundPort(id, name string) graph.Port {
	return graph.Port{ID: id, Name: name, Kind: "sound", Shape: graph.Ring, Color: sound}
}

func beatPort(id, name string) graph.Port {
	return graph.Port{ID: id, Name: name, Kind: "beat", Shape: graph.Bolt, Color: beats}
}

func levelPort(id, name string) graph.Port {
	return graph.Port{ID: id, Name: name, Kind: "level", Shape: graph.Diamond, Color: level}
}

func lightPort(id, name string) graph.Port {
	return graph.Port{ID: id, Name: name, Kind: "light", Shape: graph.Ring, Color: light}
}

// show is the graph the window starts with: a light show driven by
// the beat of what the microphone hears, and by a fader.
func show() graph.Graph {
	return graph.Graph{
		Nodes: []graph.Node{
			{ID: "mic", Title: "Audio in", Sub: "USB microphone", Color: sound, At: geom.Pt(0, 40),
				Outs: []graph.Port{soundPort("out", "Sound")}, Glow: 0.5},
			{ID: "player", Title: "Music player", Sub: "Set list · 12 songs", Color: sound, At: geom.Pt(0, 220),
				Outs: []graph.Port{soundPort("out", "Sound"), beatPort("beat", "Beat")}},
			{ID: "beat", Title: "Beat detector", Sub: "128 BPM", Color: beats, At: geom.Pt(300, 40),
				Ins: []graph.Port{soundPort("in", "Sound")}, Outs: []graph.Port{beatPort("beat", "Beat"), beatPort("bar", "Bar")}},
			{ID: "fader", Title: "Fader", Sub: "X-Touch Mini · 1", Color: level, At: geom.Pt(300, 260), Body: 40,
				Outs: []graph.Port{levelPort("out", "Level")}},
			{ID: "chase", Title: "Chaser", Sub: "Wash · 4 steps", Color: light, At: geom.Pt(600, 60),
				Ins:  []graph.Port{beatPort("step", "Step"), levelPort("dim", "Dimmer")},
				Outs: []graph.Port{lightPort("out", "Light")}},
			{ID: "dmx", Title: "DMX out", Sub: "ENTTEC Pro · universe 1", Color: light, At: geom.Pt(880, 140),
				Ins: []graph.Port{lightPort("in", "Light")}},
		},
		Wires: []graph.Wire{
			{ID: "w1", From: graph.End{Node: "mic", Port: "out"}, To: graph.End{Node: "beat", Port: "in"}, Flow: 0.7},
			{ID: "w2", From: graph.End{Node: "beat", Port: "beat"}, To: graph.End{Node: "chase", Port: "step"}, Thin: true},
			{ID: "w3", From: graph.End{Node: "fader", Port: "out"}, To: graph.End{Node: "chase", Port: "dim"}, Thin: true, Dashed: true},
			{ID: "w4", From: graph.End{Node: "chase", Port: "out"}, To: graph.End{Node: "dmx", Port: "in"}, Flow: 0.4},
		},
	}
}

// faderPad is the room round the fader's slider in its box.
var faderPad = theme.Insets("example.graph.fader.pad", geom.Insets{Left: 12, Right: 12, Top: 4, Bottom: 4})

// newView returns the graph's view: the fader's box holds a slider.
func newView() *graph.View {
	v := graph.NewView()
	v.Content = func(node string) gunim.Node {
		if node != "fader" {
			return nil
		}
		s := widget.NewSlider(0, 1)
		s.SetValue(0.7, nil)
		pad := widget.NewPad(s)
		pad.Padding = faderPad
		return pad
	}
	return v
}
