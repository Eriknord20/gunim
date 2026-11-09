package widget

import (
	"testing"

	"github.com/marrasen/gunim/input"
)

func TestAPressAloneDoesNothingAndAReleaseOnThePartLands(t *testing.T) {
	var c Click[int]
	c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 1}, 3)
	if !c.Held() || c.At() != 3 {
		t.Fatalf("held %v at %d, want held at 3", c.Held(), c.At())
	}
	if !c.Release(input.PointerUp{Button: input.ButtonPrimary}, 3) {
		t.Fatal("a release on the part pressed should land")
	}
	if c.Release(input.PointerUp{Button: input.ButtonPrimary}, 3) {
		t.Fatal("a second release with no press should not land")
	}
}

func TestAReleaseElsewhereDoesNothing(t *testing.T) {
	var c Click[string]
	c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 1}, "ok")
	if c.Release(input.PointerUp{Button: input.ButtonPrimary}, "cancel") {
		t.Fatal("a release on another part should not land")
	}
	c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 1}, "ok")
	if c.Release(input.PointerUp{Button: input.ButtonPrimary, Pos: input.Away}, "") {
		t.Fatal("a release on nothing should not land")
	}
}

func TestOnlyThePrimaryButtonClicks(t *testing.T) {
	var c Click[int]
	c.Press(input.PointerDown{Button: input.ButtonSecondary, Clicks: 1}, 1)
	if c.Release(input.PointerUp{Button: input.ButtonSecondary}, 1) {
		t.Fatal("a right click should not land")
	}
}

func TestADoubleClicksSecondPressCounts(t *testing.T) {
	var c Click[int]
	c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 2}, 0)
	if c.Clicks() != 2 || !c.Release(input.PointerUp{Button: input.ButtonPrimary}, 0) {
		t.Fatal("a double click's second press should start a click that knows it is the second")
	}
}

func TestAnotherPressOrFocusGoingDropsTheClick(t *testing.T) {
	for _, e := range []input.Event{
		input.PointerDown{Button: input.ButtonSecondary, Clicks: 1},
		input.PointerDown{Button: input.ButtonPrimary, Clicks: 1},
		input.FocusLost{},
		input.WindowFocusLost{},
	} {
		var c Click[int]
		c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 1}, 0)
		c.Hear(e)
		if c.Held() || c.Release(input.PointerUp{Button: input.ButtonPrimary}, 0) {
			t.Fatalf("after %T the click is still held", e)
		}
	}
	var c Click[int]
	c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 1}, 0)
	c.Hear(input.PointerMove{})
	if !c.Held() {
		t.Fatal("a move drops the click")
	}
}

func TestCancelDropsTheClick(t *testing.T) {
	var c Click[int]
	c.Press(input.PointerDown{Button: input.ButtonPrimary, Clicks: 1}, 0)
	c.Cancel()
	if c.Release(input.PointerUp{Button: input.ButtonPrimary}, 0) {
		t.Fatal("a cancelled click should not land")
	}
}
