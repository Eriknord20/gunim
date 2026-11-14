//go:build !(linux && (arm64 || amd64))

package main

import (
	"context"
	"errors"
	"os"

	"github.com/marrasen/gunim"
)

// kmsBuilt says this build can draw through KMS.
const kmsBuilt = false

// stopSignals end the panel: Ctrl+C.
var stopSignals = []os.Signal{os.Interrupt}

// kmsFlags are the flags for the screen, which this build has none of.
type kmsFlags struct{}

func (*kmsFlags) add() {}

// runKMS says this build cannot draw through KMS.
func runKMS(context.Context, kmsFlags, func(*gunim.App) error) error {
	return errors.New("kmshello: drawing without a desktop needs Linux on arm64 or amd64; run it with -window")
}
