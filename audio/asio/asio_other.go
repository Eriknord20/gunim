//go:build !(windows && amd64)

package asio

// Drivers lists the ASIO drivers installed: none, on this system.
func Drivers() ([]Driver, error) { return nil, nil }

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

// Channels lists the names of the driver's inputs and outputs; on
// this system it returns [ErrUnsupported].
func Channels(string) (inputs, outputs []string, err error) { return nil, nil, ErrUnsupported }

// Close stops the driver and lets it go.
func (*Device) Close() error { return nil }
