//go:build !linux || android || !(amd64 || arm64)

package pipewire

// A Client plays a stream to PipeWire's default output.
type Client struct{}

// Available reports whether PipeWire answers: never, here.
func Available() bool { return false }

// Open opens a client of the server; here it returns [ErrUnsupported].
func Open(Config) (*Client, error) { return nil, ErrUnsupported }

// Info says how the client plays.
func (*Client) Info() Info { return Info{} }

// Write adds frames to the ring the output plays from.
func (*Client) Write([]float32) int { return 0 }

// Queued returns how many frames written the output has not taken.
func (*Client) Queued() int64 { return 0 }

// Room returns how many frames Write has room for.
func (*Client) Room() int64 { return 0 }

// Taken returns how many frames written the output has taken.
func (*Client) Taken() int64 { return 0 }

// Held returns how many frames written are still to be heard.
func (*Client) Held() int64 { return 0 }

// HeardAt returns the frame written that is heard at ns.
func (*Client) HeardAt(int64) (int64, bool) { return 0, false }

// Read takes what the input recorded.
func (*Client) Read([]float32) (n int, at int64, ok bool) { return 0, 0, false }

// Poll returns what happened since it was last called.
func (*Client) Poll() Events { return Events{} }

// Pause stops the output, or starts it again.
func (*Client) Pause(bool) {}

// Err returns why the client stopped.
func (*Client) Err() error { return nil }

// Close stops the client.
func (*Client) Close() error { return nil }

// A Device is a device PipeWire plays or records on: none here.
type Device struct {
	Name, Output, Input       string
	OutPositions, InPositions []uint32
}

// Devices lists none here.
func Devices() ([]Device, error) { return nil, ErrUnsupported }
