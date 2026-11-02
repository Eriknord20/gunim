//go:build !(((darwin && !ios) || (linux && !android)) && (amd64 || arm64))

package audiounit

// A Client plays through the default output device.
type Client struct{}

// Available reports whether Core Audio loads: never, here.
func Available() bool { return false }

// Ports returns the default devices' channels; here it returns
// [ErrUnsupported].
func Ports() (inputs, outputs []string, err error) { return nil, nil, ErrUnsupported }

// ControlPanel opens Audio MIDI Setup; here it returns [ErrUnsupported].
func ControlPanel() error { return ErrUnsupported }

// Playing returns the output device a client plays through: none, here.
func Playing() uint32 { return 0 }

// Recording returns the input device a client records: none, here.
func Recording() uint32 { return 0 }

// Open opens a client; here it returns [ErrUnsupported].
func Open(Config) (*Client, error) { return nil, ErrUnsupported }

// Info says how the client plays.
func (*Client) Info() Info { return Info{} }

// Rate returns the rate the client plays at.
func (*Client) Rate() int { return 0 }

// Channels returns how many channels a frame Write takes holds.
func (*Client) Channels() int { return 0 }

// Inputs returns how many channels Read's frames hold.
func (*Client) Inputs() int { return 0 }

// Buffer returns the frames the device hands the client at a time.
func (*Client) Buffer() int { return 0 }

// Delay returns the frames from a frame written to its being heard.
func (*Client) Delay() int { return 0 }

// Write adds frames to the ring the output plays from.
func (*Client) Write([]float32) int { return 0 }

// Written returns how many frames Write has written.
func (*Client) Written() int64 { return 0 }

// Queued returns how many frames written the output has not taken.
func (*Client) Queued() int64 { return 0 }

// Room returns how many frames Write has room for.
func (*Client) Room() int64 { return 0 }

// Heard returns the frame heard now.
func (*Client) Heard() (int64, bool) { return 0, false }

// Read takes what the input recorded.
func (*Client) Read([]float32) (n int, at int64, ok bool) { return 0, 0, false }

// Poll returns what happened since it was last called.
func (*Client) Poll() Events { return Events{} }

// Pause stops the output, or starts it again.
func (*Client) Pause(bool) {}

// Suspend stops the units.
func (*Client) Suspend() error { return nil }

// Resume starts them again.
func (*Client) Resume() error { return nil }

// Close stops the client.
func (*Client) Close() error { return nil }
