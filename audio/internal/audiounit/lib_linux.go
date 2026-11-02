//go:build linux && !android && (amd64 || arm64)

package audiounit

// systemAPI returns ErrUnsupported: Core Audio is macOS's. The client
// builds here so its tests run it, the callbacks too, against a stand-in.
func systemAPI() (*api, error) { return nil, ErrUnsupported }

// Available reports whether Core Audio loads: never, here.
func Available() bool { return false }

// ControlPanel opens Audio MIDI Setup; here it returns [ErrUnsupported].
func ControlPanel() error { return ErrUnsupported }
