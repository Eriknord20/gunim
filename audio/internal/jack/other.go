//go:build !linux || android || !(amd64 || arm64)

package jack

// A Client is a client of the JACK server.
type Client struct{}

// Available reports whether a JACK server answers: never, here.
func Available() bool { return false }

// Ports returns the system's inputs and outputs; here it returns
// [ErrUnsupported].
func Ports() (inputs, outputs []string, err error) { return nil, nil, ErrUnsupported }

// Open opens a client of the server; here it returns [ErrUnsupported].
func Open(Config) (*Client, error) { return nil, ErrUnsupported }

// Info says how the client plays.
func (*Client) Info() Info { return Info{} }

// Channels returns how many channels a frame Write takes holds.
func (*Client) Channels() int { return 0 }

// Buffer returns the frames the server hands the client at a time.
func (*Client) Buffer() int { return 0 }

// Latency returns the output and input latencies, in frames.
func (*Client) Latency() (out, in int) { return 0, 0 }

// Write adds frames to the ring the outputs play from.
func (*Client) Write([]float32) int { return 0 }

// Queued returns how many frames written the server has not taken.
func (*Client) Queued() int64 { return 0 }

// Room returns how many frames Write has room for.
func (*Client) Room() int64 { return 0 }

// Taken returns how many frames written the server has taken.
func (*Client) Taken() int64 { return 0 }

// Inputs returns how many channels Read's frames hold.
func (*Client) Inputs() int { return 0 }

// Read takes what the inputs recorded.
func (*Client) Read([]float32) (n int, at int64, ok bool) { return 0, 0, false }

// SinceCycle returns the frames since the server's buffer started.
func (*Client) SinceCycle() int { return 0 }

// Poll returns what happened since it was last called.
func (*Client) Poll() Events { return Events{} }

// Pause stops the outputs, or starts them again.
func (*Client) Pause(bool) {}

// Err returns why the client stopped.
func (*Client) Err() error { return nil }

// Close stops the client.
func (*Client) Close() error { return nil }
