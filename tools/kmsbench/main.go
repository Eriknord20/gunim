//go:build linux && (arm64 || amd64)

// Command kmsbench measures gunim's KMS driver on the screen it runs on,
// such as a Raspberry Pi with a Touch Display 2, against the goals of
// docs/plans/kms-touch.md: frames and missed flips while the test pattern
// moves with a touch script playing, frame times, touch to flip, the time
// to the first frame, and CPU use while moving and at rest. It prints a
// table, and exits non-zero if any goal is missed.
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./tools/kmsbench
//	sudo ./kmsbench -rotate 90 -vt free
//
// The touch script plays through the driver's own input path, so it needs
// no uinput and no finger. Without -script it is a made-up one: a finger
// drawing circles, taps, and two fingers dragging. A script recorded on
// the screen with kmsdemo -record plays as made.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/marrasen/gunim/internal/kmsdemo"
)

func main() { os.Exit(run()) }

// run runs the bench, and returns the exit code: 1 where a goal was
// missed or the bench failed.
func run() int {
	var o kmsdemo.BenchOptions
	flag.IntVar(&o.KMS.Rotate, "rotate", 0, "turn the picture clockwise: 0, 90, 180 or 270")
	flag.StringVar(&o.KMS.VT, "vt", "", `the VT to draw on: empty for the one it runs on, "free", or a number`)
	flag.StringVar(&o.KMS.Connector, "connector", "", "the screen; empty finds it")
	scale := flag.Float64("scale", 0, "device pixels per logical pixel; 0 works it out")
	flag.BoolVar(&o.KMS.Software, "software", false, "draw on the CPU even where there is a GPU")
	script := flag.String("script", "", "a touch recording to play; empty plays a made-up one")
	flag.DurationVar(&o.Warm, "warm", 3*time.Second, "how long the scene moves before the figures start")
	flag.DurationVar(&o.Moving, "moving", 60*time.Second, "how long to measure the scene moving")
	flag.DurationVar(&o.Rest, "rest", 60*time.Second, "how long to measure the scene still")
	flag.BoolVar(&o.Still, "still", false, "keep the scene still while the touch script plays: only the fingers draw")
	flag.Parse()
	o.KMS.Scale = float32(*scale)
	if *script != "" {
		f, err := os.Open(*script)
		if err != nil {
			log.Print(err)
			return 1
		}
		o.KMS.Replay = f
	}
	o.Log = func(s string) { log.Print("kmsbench: ", s) }
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	r, err := kmsdemo.Bench(ctx, o)
	if err != nil {
		log.Print(err)
		return 1
	}
	if ctx.Err() != nil {
		log.Print("kmsbench: stopped before the end")
		return 1
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Goal\tTarget\tMeasured\t\n")
	failed := 0
	for _, g := range r.Goals() {
		mark := "ok"
		if !g.OK {
			mark = "MISSED"
			failed++
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", g.Name, g.Target, g.Got, mark)
	}
	if err := tw.Flush(); err != nil {
		log.Print(err)
		return 1
	}
	fmt.Printf("\n%.2f Hz; %d frames in %.1f s while moving; draw p50 %.1f ms p99 %.1f ms; frame max %.1f ms; %d touch reports answered",
		r.Rate, r.Moving.Frames, r.MovingFor.Seconds(), ms(r.Moving.Draw.P50), ms(r.Moving.Draw.P99), ms(r.Moving.All.Max), r.Moving.Touch.N)
	if r.Software {
		fmt.Print("; drawn by the CPU")
	}
	fmt.Println()
	if failed > 0 {
		fmt.Printf("%d goals missed\n", failed)
		return 1
	}
	return 0
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
