//go:build !(windows && amd64) && !(linux && !android && (amd64 || arm64))

package driver

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

// Close stops the driver and lets it go.
func (*Device) Close() error { return nil }

func alsaDrivers() []Driver { return nil }

func alsaChannels(string) (inputs, outputs []string, err error) { return nil, nil, ErrUnsupported }
