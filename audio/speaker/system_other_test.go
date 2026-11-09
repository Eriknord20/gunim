//go:build !js && !android && !(linux && (amd64 || arm64))

package speaker

import (
	"errors"
	"testing"

	"github.com/marrasen/gunim/audio"
)

// Where the system has no backend of gunim's own, Open says there is no
// sound.
func TestWithNoBackendThereIsNoSound(t *testing.T) {
	noBackend(t)
	if s, err := Open(audio.NewMixer(), Options{}); !errors.Is(err, errNoSound) {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("Open = %v", err)
	}
}
