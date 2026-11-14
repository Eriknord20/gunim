//go:build linux && (arm64 || amd64)

package main

import (
	"context"
	"flag"
	"os"
	"syscall"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver/kms"
)

// kmsBuilt says this build can draw through KMS.
const kmsBuilt = true

// stopSignals end the panel, and give the screen back: Ctrl+C, systemd stopping it, and the SSH session it runs
// from ending.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// kmsFlags are the flags for the screen, as kmsdemo has them.
type kmsFlags struct {
	o     kms.Options
	scale float64
}

func (k *kmsFlags) add() {
	flag.IntVar(&k.o.Rotate, "rotate", 0, "turn the picture clockwise: 0, 90, 180 or 270")
	flag.StringVar(&k.o.VT, "vt", "", `the VT to draw on: empty for the one it runs on, "free", or a number`)
	flag.StringVar(&k.o.Connector, "connector", "", `the screen, as "DSI-2" or "HDMI-A-1"; empty finds it`)
	flag.Float64Var(&k.scale, "scale", 0, "device pixels per logical pixel; 0 works it out")
	flag.BoolVar(&k.o.Software, "software", false, "draw on the CPU even where there is a GPU")
	flag.DurationVar(&k.o.Blank.Dim, "dim", 0, "dim the screen after this long untouched; 0 never does")
	flag.DurationVar(&k.o.Blank.Off, "off", 0, "turn the screen off after this long untouched; 0 never does")
}

// runKMS opens the screen and runs fn on it, and gives the screen back once it ends.
func runKMS(ctx context.Context, k kmsFlags, fn func(*gunim.App) error) error {
	k.o.Scale = float32(k.scale)
	d, err := kms.Open(k.o)
	if err != nil {
		return err
	}
	err = gunim.MainWith(ctx, d, fn)
	<-d.Closed()
	return err
}
