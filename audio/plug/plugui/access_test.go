package plugui

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
)

// Every control reads aloud with a name and a value: a screen reader says them, and a script finds the control by
// its name.
func TestEachControlReadsWithANameAndAValue(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 100}}
	sel := NewSelector("Stereo", []string{"Left", "Both", "Right"}, nil)
	sel.SetSelected(1, nil)
	locked := NewSelector("Stereo", []string{"Left", "Both"}, nil)
	locked.Locked = true
	on := NewPower("Follow song")
	on.SetChecked(true, nil)
	dial := NewActionDial("Learn", nil)
	dial.Value = "Ready"
	for _, c := range []struct {
		what        string
		node        gunim.Accessible
		name, value string
		state       access.State
	}{
		{"knob", NewKnob(h, release), "Release", "100.00", 0},
		{"trim pot", NewTrimPot(h, release), "Release", "100.00", 0},
		{"selector", sel, "Stereo", "Both", 0},
		{"locked selector", locked, "Stereo", "Left", access.StateDisabled},
		{"power", on, "Follow song", "", access.StateChecked},
		{"action dial", dial, "Learn", "Ready", 0},
	} {
		info := c.node.Access()
		if info.Name != c.name || info.Value != c.value || info.State&c.state != c.state {
			t.Errorf("the %s reads as %q = %q, state %v; want %q = %q, state %v", c.what, info.Name, info.Value, info.State,
				c.name, c.value, c.state)
		}
	}
}
