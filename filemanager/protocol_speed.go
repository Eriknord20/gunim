package filemanager

import "github.com/marrasen/gunim"

// OpSpeed is a patch: a sample of how fast operation ID moves bytes,
// in bytes a second, taken when it had got Done of the way.
type OpSpeed struct {
	ID   int
	Rate float64
	Done float32
}

// OpDone is a patch: operation ID has ended, well with OK, and leaves the
// corner once it has shown so.
type OpDone struct {
	ID int
	OK bool
}

func init() {
	gunim.RegisterType[OpSpeed]("files.op-speed")
	gunim.RegisterType[OpDone]("files.op-done")
}
