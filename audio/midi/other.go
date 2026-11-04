//go:build !(linux && !android) && !windows && !(darwin && !ios)

package midi

import "time"

type portAddr struct{}

func sysList(bool) ([]sysPort, error) { return nil, ErrUnsupported }

func sysWatch() {}

type outSys struct{}

func (outSys) open(*Out) error                     { return ErrUnsupported }
func (outSys) send(*Out, time.Time, Message) error { return ErrUnsupported }
func (outSys) cancel(*Out) error                   { return ErrUnsupported }
func (outSys) close(*Out) error                    { return nil }
func (outSys) gone(*Out)                           {} //nolint:unused // Named as on systems with MIDI.

type inSys struct{}

func (inSys) open(*In) error  { return ErrUnsupported }
func (inSys) close(*In) error { return nil }
func (inSys) gone(*In)        {} //nolint:unused // Named as on systems with MIDI.
