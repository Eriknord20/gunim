package widget

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
)

func TestAThemedNodeListsItsChildWithoutAllocating(t *testing.T) {
	th := NewThemed(NewLabel("x"), theme.Make("test"))
	if n := testing.AllocsPerRun(10, func() { themedKids = th.Children() }); n != 0 {
		t.Fatalf("Children allocates %v times, want 0", n)
	}
}

// themedKids keeps the test's result alive.
var themedKids []gunim.Node
