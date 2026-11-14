//go:build linux && (arm64 || amd64)

// Command kmsbench measures gunim's KMS driver on the screen it runs on,
// such as a Raspberry Pi with a Touch Display 2, against the goals of
// docs/plans/kms-touch.md: frames and missed flips while the screen moves
// with a touch script playing, frame times, touch to flip, the time to the
// first frame, memory, CPU use while moving and at rest, and the screen
// blanked and woken by a tap that reaches nothing. It prints a table, and
// exits non-zero if any goal is missed.
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./tools/kmsbench
//	./kmsbench -rotate 90 -vt free
//	./kmsbench -rotate 90 -vt free -screen widgets
//
// -screen picks what it shows: the demo's test pattern, or the gallery of
// example/widgets. It needs no root: a user in the groups video, render and
// input, with the backlight's udev rule in driver/kms's README, will do.
//
// The touch script plays through the driver's own input path, so it needs
// no uinput and no finger. Without -script it is a made-up one: on the
// demo a finger drawing circles, taps, and two fingers dragging; on the
// gallery drags up and down that fling. A script recorded on the screen
// with kmsdemo -record plays as made, and then the bench checks the
// blanking but not the wake.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marrasen/gunim/driver/kms/kmsbench"
	"github.com/marrasen/gunim/internal/kmsdemo"
)

// started is when the program started: the bench's start goal counts from
// it, as the plan's section 1 does.
var started = time.Now()

func main() { os.Exit(run()) }

// run runs the bench, and returns the exit code: 1 where a goal was
// missed or the bench failed.
func run() int {
	o := kmsbench.Options{Started: started}
	var screen string
	var still bool
	flag.IntVar(&o.KMS.Rotate, "rotate", 0, "turn the picture clockwise: 0, 90, 180 or 270")
	flag.StringVar(&o.KMS.VT, "vt", "", `the VT to draw on: empty for the one it runs on, "free", or a number`)
	flag.StringVar(&o.KMS.Connector, "connector", "", "the screen; empty finds it")
	flag.StringVar(&screen, "screen", "demo", "what to show: demo or widgets")
	flag.BoolVar(&o.NoBlank, "noblank", false, "leave out the blanking at the end")
	flag.StringVar(&o.Shot, "shot", "", "write the screen's window to this PNG file as the warm-up ends")
	scale := flag.Float64("scale", 0, "device pixels per logical pixel; 0 works it out")
	flag.BoolVar(&o.KMS.Software, "software", false, "draw on the CPU even where there is a GPU")
	script := flag.String("script", "", "a touch recording to play; empty plays a made-up one")
	flag.DurationVar(&o.Warm, "warm", 3*time.Second, "how long the scene moves before the figures start; 6s on the gallery")
	flag.DurationVar(&o.Moving, "moving", 60*time.Second, "how long to measure the scene moving")
	flag.DurationVar(&o.Rest, "rest", 60*time.Second, "how long to measure the scene still")
	flag.BoolVar(&still, "still", false, "keep the scene still while the touch script plays: only the fingers draw")
	flag.Parse()
	warmSet := false
	flag.Visit(func(f *flag.Flag) { warmSet = warmSet || f.Name == "warm" })
	if screen == "widgets" && !warmSet {
		// The gallery's start-up toasts leave 5 s in, fading as layers of
		// their own, and its first scroll shows text never drawn before:
		// both are over by 6 s, so the goals hold the scrolling. What the
		// start-up costs shows on its own line, "missed flips while warming
		// up"; -warm 3s measures it with the scrolling, as before.
		o.Warm = 6 * time.Second
	}
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
	scene, err := kmsdemo.BenchScene(screen, still, o.Log)
	if err != nil {
		log.Print(err)
		return 1
	}
	o.Scene = scene
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	r, err := kmsbench.Bench(ctx, o)
	if err != nil {
		log.Print(err)
		return 1
	}
	if ctx.Err() != nil {
		log.Print("kmsbench: stopped before the end")
		return 1
	}
	missed, err := kmsbench.Report(os.Stdout, r)
	if err != nil {
		log.Print(err)
		return 1
	}
	if missed > 0 {
		return 1
	}
	return 0
}
