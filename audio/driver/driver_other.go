//go:build !(windows && amd64)

package driver

import (
	"github.com/marrasen/gunim/audio/internal/audiounit"
	"github.com/marrasen/gunim/audio/internal/jack"
)

// Drivers lists the ASIO drivers installed: none, on this system; and
// [JACK], where a JACK server answers; and [CoreAudio] on macOS.
func Drivers() ([]Driver, error) {
	var list []Driver
	if jack.Available() {
		list = append(list, Driver{Name: JACK})
	}
	if audiounit.Available() {
		list = append(list, Driver{Name: CoreAudio})
	}
	return list, nil
}

// A Device is an open ASIO driver, playing.
type Device struct{}

// Open opens the driver c names and starts it playing; on this system
// it returns [ErrUnsupported].
func Open(Config) (*Device, error) { return nil, ErrUnsupported }

// Info says how the device plays.
func (*Device) Info() Info { return Info{} }

// ControlPanel opens the driver's own settings.
func (*Device) ControlPanel() error { return ErrUnsupported }

// ControlPanel opens the settings of the driver named; on this system
// it returns [ErrUnsupported].
func ControlPanel(string) error { return ErrUnsupported }

// Channels lists the names of the driver's inputs and outputs: for
// [JACK], the system's ports, as the server names them; for
// [CoreAudio], the channels of the default input and output devices.
// For another driver it returns [ErrUnsupported] on this system.
func Channels(name string) (inputs, outputs []string, err error) {
	switch name {
	case JACK:
		return jack.Ports()
	case CoreAudio:
		return audiounit.Ports()
	}
	return nil, nil, ErrUnsupported
}

// Close stops the driver and lets it go.
func (*Device) Close() error { return nil }
