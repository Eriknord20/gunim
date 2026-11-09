//go:build linux && !android && (amd64 || arm64)

package speaker

import (
	"errors"
	"strings"

	"github.com/marrasen/gunim/audio/driver"
)

// openSystemSound opens the first sound card directly, where PipeWire
// doesn't play, as on a system with PulseAudio alone or none, which then
// lets go of the card as a program does on another driver. A test puts a
// function of its own here.
var openSystemSound = func(s *Speaker, o Options) (startedOutput, error) {
	ds, err := driver.Drivers()
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if strings.HasPrefix(d.Name, driver.ALSAPrefix) {
			// Through a ring, as the system's own sound always plays: a
			// program that asks for the mix on the card's own thread asks
			// that of a card it chose.
			o.Driver, o.Direct = d.Name, false
			return openRing(s, o)
		}
	}
	return nil, errors.New("speaker: no sound server plays, and there is no sound card")
}
