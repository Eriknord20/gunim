//go:build !darwin || ios

package coreaudio

// An Input is a device recording.
type Input struct{}

// Inputs lists the devices that record; on this system it returns
// [ErrUnsupported].
func Inputs() ([]InputDevice, error) { return nil, ErrUnsupported }

// OpenInput opens a device that records and starts it; on this system
// it returns [ErrUnsupported].
func OpenInput(InputConfig) (*Input, error) { return nil, ErrUnsupported }

// Info says how the input records.
func (*Input) Info() InputInfo { return InputInfo{} }

// Done returns a channel closed once the input stops on its own.
func (*Input) Done() <-chan struct{} { return nil }

// Err returns why the input stopped on its own.
func (*Input) Err() error { return nil }

// Close stops the input and lets it go.
func (*Input) Close() error { return nil }
