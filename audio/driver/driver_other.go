//go:build !(windows && amd64)

package driver

import (
	"fmt"
	"strings"

	"github.com/marrasen/gunim/audio/internal/audiounit"
	"github.com/marrasen/gunim/audio/internal/jack"
	"github.com/marrasen/gunim/audio/internal/pipewire"
)

// Drivers lists the ASIO drivers installed: none, on this system; and
// [JACK], where a JACK server answers; [PipeWire], where a PipeWire
// server listens; [CoreAudio] on macOS; and on Linux the sound cards,
// each as "ALSA: " and its name, played directly.
func Drivers() ([]Driver, error) {
	var list []Driver
	if jack.Available() {
		list = append(list, Driver{Name: JACK})
	}
	if pipewire.Available() {
		list = append(list, Driver{Name: PipeWire})
		if ds, err := pipewire.Devices(); err == nil {
			for _, d := range ds {
				list = append(list, Driver{Name: PipeWirePrefix + d.Name})
			}
		}
	}
	if audiounit.Available() {
		list = append(list, Driver{Name: CoreAudio})
	}
	return append(list, alsaDrivers()...), nil
}

// Channels lists the names of the driver's inputs and outputs: for
// [JACK], the system's ports, as the server names them; for
// [PipeWire], the left and right of its streams, as PipeWire converts
// to the default devices' channels; for [CoreAudio], the channels of
// the default input and output devices; for a sound card of Linux, its
// inputs and outputs.
// For another driver it returns [ErrUnsupported] on this system.
func Channels(name string) (inputs, outputs []string, err error) {
	switch name {
	case JACK:
		return jack.Ports()
	case PipeWire:
		if !pipewire.Available() {
			return nil, nil, ErrUnsupported
		}
		return []string{"left", "right"}, []string{"left", "right"}, nil
	case CoreAudio:
		return audiounit.Ports()
	}
	if d, ok := strings.CutPrefix(name, PipeWirePrefix); ok {
		ds, err := pipewire.Devices()
		if err != nil {
			return nil, nil, err
		}
		for _, x := range ds {
			if x.Name == d {
				return numbered("Input", len(x.InPositions)), numbered("Output", len(x.OutPositions)), nil
			}
		}
		return nil, nil, fmt.Errorf("pipewire: there is no device %q now", d)
	}
	return alsaChannels(name)
}

// numbered names n channels.
func numbered(word string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s %d", word, i+1)
	}
	return out
}
