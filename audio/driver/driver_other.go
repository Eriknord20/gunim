//go:build !(windows && amd64)

package driver

import "github.com/marrasen/gunim/audio/internal/jack"

// Drivers lists the ASIO drivers installed: none, on this system; and
// [JACK], where a JACK server answers.
func Drivers() ([]Driver, error) {
	if jack.Available() {
		return []Driver{{Name: JACK}}, nil
	}
	return nil, nil
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
// [JACK], the system's ports, as the server names them. For another
// driver it returns [ErrUnsupported] on this system.
func Channels(name string) (inputs, outputs []string, err error) {
	if name == JACK {
		return jack.Ports()
	}
	return nil, nil, ErrUnsupported
}

// Close stops the driver and lets it go.
func (*Device) Close() error { return nil }
