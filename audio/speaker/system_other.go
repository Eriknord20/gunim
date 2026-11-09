//go:build !js && !android && !(linux && (amd64 || arm64))

package speaker

import "errors"

// errNoSound is openSystemSound's error on a system where gunim has
// no sound to fall back on: Windows and macOS, whose own backend
// failed to open, Linux on a processor other than amd64 or arm64, and
// any other system, such as a BSD.
var errNoSound = errors.New("speaker: no sound on this system")

// openSystemSound returns errNoSound. A test puts a function of its
// own here.
var openSystemSound = func(*Speaker, Options) (startedOutput, error) { return nil, errNoSound }
