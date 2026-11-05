//go:build !js

package desktop

import (
	"testing"

	"github.com/marrasen/gunim/internal/glfw"
)

// A modifier key's own event counts it as held once it is down, and not
// once it is up, whatever the system said was held before it.
func TestModsAfterAModifierKey(t *testing.T) {
	for _, c := range []struct {
		name   string
		k      glfw.Key
		action glfw.Action
		before glfw.ModifierKey
		want   glfw.ModifierKey
	}{
		{"Ctrl down, as X11 sends it", glfw.KeyLeftControl, glfw.Press, 0, glfw.ModControl},
		{"Ctrl up, as X11 sends it", glfw.KeyRightControl, glfw.Release, glfw.ModControl, 0},
		{"Ctrl repeating", glfw.KeyLeftControl, glfw.Repeat, glfw.ModControl, glfw.ModControl},
		{"Shift up with Ctrl held", glfw.KeyLeftShift, glfw.Release, glfw.ModShift | glfw.ModControl, glfw.ModControl},
		{"Alt down", glfw.KeyRightAlt, glfw.Press, glfw.ModShift, glfw.ModShift | glfw.ModAlt},
		{"Super up", glfw.KeyLeftSuper, glfw.Release, glfw.ModSuper, 0},
		{"a letter keeps what was held", glfw.KeyA, glfw.Press, glfw.ModControl, glfw.ModControl},
		{"as Windows sends Ctrl up", glfw.KeyLeftControl, glfw.Release, 0, 0},
	} {
		if got := modsAfter(c.k, c.action, c.before); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
